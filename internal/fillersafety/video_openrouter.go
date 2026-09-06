package fillersafety

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/openroutermedia"
)

const (
	maxCompleteVideoBytes = 64 << 20
	videoPromptVersion    = "spoken-safety-complete-video-v1"
	videoMaxTokens        = 1024
)

const videoSystemPrompt = `Screen one identity-blind candidate filler clip for two prohibited broadcast-suitability signals. This is high-recall screening, not a general content summary.

explicit_nudity means filmed or photographic visible genitals, exposed female breasts or nipples, or clearly exposed buttocks. Flag uncertain borderline visibility rather than silently clearing it. Do not flag ordinary swimwear, cleavage, bare male chests, statues, drawings, or non-explicit affection.

hateful_or_degrading_slur means audible or visibly written use of a group-directed slur targeting race, ethnicity, nationality, religion, sex, sexual orientation, gender identity, or disability. Context, quotation, age, or historical provenance does not make the clip airable. Do not repeat, quote, transcribe, or describe the slur itself.

Inspect the complete supplied video, including its audio. Set visualAssessment to insufficient if the video was not processed across its full span or visibility prevents a reliable screen. Set spokenLanguageAssessment to insufficient if the audio was unavailable, not processed across its full span, or too unclear to screen.

Return only the closed flags and approximate locations. Never return a quote, source identity, person identity, or free-form description. If a prohibited signal may be present but its exact time is uncertain, return the narrowest conservative range that contains it. Empty flags means only that this assessor observed no prohibited signal; it never certifies the clip as clean.`

const videoUserPromptTemplate = "Duration milliseconds: <duration-ms>. Inspect the complete supplied video without identifying its source."

type openRouterVideoConfig struct {
	Client           *http.Client
	Snapshot         openroutermedia.CapabilitySnapshot
	Now              func() time.Time
	BaseURL          string
	APIKey           string
	Model            string
	ResolvedModel    string
	UpstreamProvider string
	ProviderSlug     string
	CapabilitySHA256 string
	PromptSHA256     string
	MaxChargeNanoUSD int64
	DisableReasoning bool
}

type openRouterVideoCorroborator struct {
	config openRouterVideoConfig
}

type videoFlag struct {
	Kind     string `json:"kind"`
	StartMS  int64  `json:"startMs"`
	EndMS    int64  `json:"endMs"`
	Modality string `json:"modality"`
}

type videoModelOutput struct {
	VisualAssessment string      `json:"visualAssessment"`
	SpokenAssessment string      `json:"spokenLanguageAssessment"`
	Flags            []videoFlag `json:"flags"`
}

type videoAttempt struct {
	State     VideoState
	Flags     []videoFlag
	Transport openroutermedia.Result
}

func (c *openRouterVideoCorroborator) identity(_ int64) hostedCallIdentity {
	return hostedCallIdentity{
		RequestedProvider: "openrouter", RequestedModel: c.config.Model,
		ResolvedProvider: "openrouter", ResolvedModel: c.config.ResolvedModel,
		UpstreamProvider: c.config.UpstreamProvider, CapabilitySHA256: c.config.CapabilitySHA256,
		PromptSHA256: c.config.PromptSHA256, SchemaSHA256: videoSchemaContractSHA256(),
		MaxChargeNanoUSD: c.config.MaxChargeNanoUSD,
	}
}

func (c *openRouterVideoCorroborator) corroborate(
	ctx context.Context,
	plan *CompleteMediaPlan,
	reserve func(string) error,
) (videoAttempt, error) {
	attempt := videoAttempt{State: VideoFailed, Flags: []videoFlag{}}
	if err := validateOpenRouterVideoInput(c, ctx, plan, reserve); err != nil {
		return attempt, err
	}
	mimeType, valid := completeVideoMIME(plan.SourcePath)
	if !valid || plan.SourceBytes > maxCompleteVideoBytes {
		attempt.State = VideoIncomplete
		return attempt, fmt.Errorf("spoken-safety complete video requires bounded supported media")
	}
	video, err := readBoundedCompleteVideo(plan.SourcePath)
	if err != nil || int64(len(video)) != plan.SourceBytes {
		attempt.State = VideoIncomplete
		return attempt, fmt.Errorf("spoken-safety complete video bytes are unavailable")
	}
	schema := videoOutputSchema(plan.Video.EndMS)
	authority, err := openroutermedia.NewRouteAuthority(c.config.Snapshot, c.config.CapabilitySHA256, openroutermedia.RouteRequirements{
		BaseURL: c.config.BaseURL, RequestedModel: c.config.Model, CanonicalModel: c.config.ResolvedModel,
		UpstreamProvider: c.config.UpstreamProvider, ProviderSlug: c.config.ProviderSlug,
		RequiredInputModalities: []string{"text", "video"}, MaxTokens: videoMaxTokens, Now: c.config.Now,
	})
	if err != nil {
		return attempt, err
	}
	transport, err := openroutermedia.Call(ctx, c.config.Client, c.config.BaseURL, openroutermedia.Config{
		Authority: authority, APIKey: c.config.APIKey, Model: c.config.Model, ResolvedModel: c.config.ResolvedModel,
		UpstreamProvider: c.config.UpstreamProvider, ProviderSlug: c.config.ProviderSlug,
		SchemaName: "filler_suitability", Schema: schema,
		SystemPrompt: videoSystemPrompt,
		Content:      fmt.Sprintf("Duration milliseconds: %d. Inspect the complete supplied video without identifying its source.", plan.Video.EndMS),
		Videos:       []openroutermedia.Video{{MIMEType: mimeType, Base64: base64.StdEncoding.EncodeToString(video)}},
		MaxTokens:    videoMaxTokens, ReservationNanoUSD: c.config.MaxChargeNanoUSD,
		DisableReasoning: c.config.DisableReasoning,
		Title:            "Loomarr spoken-safety complete-video corroboration",
		Reserve:          reserve,
	})
	attempt.Transport = transport
	if err != nil {
		return attempt, err
	}
	var output videoModelOutput
	decoder := json.NewDecoder(bytes.NewBufferString(transport.StructuredOutput))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		attempt.State = VideoInvalidResponse
		return attempt, fmt.Errorf("decode spoken-safety video decision")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		attempt.State = VideoInvalidResponse
		return attempt, fmt.Errorf("decode spoken-safety video decision")
	}
	state, flags, err := validateVideoModelOutput(output, plan.Video.EndMS)
	attempt.State, attempt.Flags = state, flags
	return attempt, err
}

// videoSchemaContractSHA256 is stable across source durations. The exact duration-bounded schema
// remains part of each request hash; certification binds this template and its maximum domain.
func videoSchemaContractSHA256() string {
	return canonicalJSONSHA256(struct {
		Version           string         `json:"version"`
		MaximumDurationMS int64          `json:"maximumDurationMs"`
		Schema            map[string]any `json:"schema"`
	}{
		Version: videoPromptVersion, MaximumDurationMS: maxCompleteAudioWindowMS,
		Schema: videoOutputSchema(maxCompleteAudioWindowMS),
	})
}

func readBoundedCompleteVideo(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxCompleteVideoBytes+1))
	if err != nil || len(data) > maxCompleteVideoBytes {
		return nil, fmt.Errorf("complete video exceeds its byte ceiling")
	}
	return data, nil
}

func completeVideoMIME(path string) (string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4":
		return "video/mp4", true
	case ".mpeg", ".mpg":
		return "video/mpeg", true
	case ".mov":
		return "video/mov", true
	case ".webm":
		return "video/webm", true
	default:
		return "", false
	}
}

func videoPromptSHA256() string {
	raw, err := json.Marshal(struct {
		Version      string `json:"version"`
		System       string `json:"system"`
		UserTemplate string `json:"userTemplate"`
		MaxTokens    int    `json:"maxTokens"`
	}{Version: videoPromptVersion, System: videoSystemPrompt, UserTemplate: videoUserPromptTemplate, MaxTokens: videoMaxTokens})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
