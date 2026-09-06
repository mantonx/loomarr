package fillerstructureopenrouter

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/openroutermedia"
)

const (
	structureSchemaName = "filler_temporal_structure"
	structureTitle      = "Loomarr filler structure assessment"
	settlementTimeout   = 5 * time.Second
)

var errReservationHeld = errors.New("filler structure OpenRouter reservation held by budget")

type Assessor struct {
	config Config
}

func New(config Config) (*Assessor, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &Assessor{config: config}, nil
}

func (a *Assessor) Profile() fillerstructure.AssessorProfile { return a.config.Profile }

func (a *Assessor) AssessCompleteTimeline(ctx context.Context, media filler.StructureAssessmentMedia) (fillerstructure.RecordedAssessment, error) {
	video, err := readBoundVideo(ctx, media)
	if err != nil {
		return fillerstructure.RecordedAssessment{}, err
	}
	source := fillerstructure.Source{SHA256: media.Source.SHA256, Bytes: media.Source.Bytes, DurationMS: media.Source.DurationMs}
	requestedAt := a.config.Now().UTC().Round(0)
	var reservationState fillerstructure.AssessmentReservationState
	callResult, callErr := openroutermedia.Call(ctx, a.config.Client, a.config.BaseURL, openroutermedia.Config{
		APIKey: a.config.APIKey, Model: a.config.Model, ResolvedModel: a.config.ResolvedModel,
		UpstreamProvider: a.config.UpstreamProvider, ProviderSlug: a.config.UpstreamProviderSlug,
		SchemaName: structureSchemaName, Schema: fillerstructure.DirectVideoSchema(media.Source.DurationMs),
		SystemPrompt: fillerstructure.DirectVideoSystemPrompt, Content: fillerstructure.DirectVideoContent(media.Source.DurationMs),
		Videos:    []openroutermedia.Video{{MIMEType: "video/mp4", Base64: base64.StdEncoding.EncodeToString(video)}},
		MaxTokens: a.config.MaxTokens, ReservationNanoUSD: a.config.ReservationNanoUSD,
		DisableReasoning: a.config.DisableReasoning, EnableReasoning: a.config.EnableReasoning,
		Title: structureTitle,
		Reserve: func(requestSHA256 string) error {
			reservation, reservationErr := fillerstructure.NewAssessmentReservation(fillerstructure.AssessmentReservationInput{
				RequestSHA256: requestSHA256, Source: source, Media: media.Assessment, Assessor: a.config.Profile,
				MetadataSnapshotSHA256: a.config.MetadataSnapshotSHA256,
				PromptSHA256:           fillerstructure.DirectVideoPromptSHA256(media.Source.DurationMs),
				SchemaSHA256:           fillerstructure.DirectVideoSchemaSHA256(media.Source.DurationMs),
				ExpectedResolvedModel:  a.config.ResolvedModel,
				UpstreamProvider:       a.config.UpstreamProvider, UpstreamProviderSlug: a.config.UpstreamProviderSlug,
				RequestedNanoUSD: a.config.ReservationNanoUSD, MaximumChargeNanoUSD: a.config.MaximumChargeNanoUSD,
				RequestedAt: requestedAt,
			})
			if reservationErr != nil {
				return reservationErr
			}
			state, reserveErr := a.config.Ledger.Reserve(ctx, reservation)
			if reserveErr != nil {
				return reserveErr
			}
			reservationState = state
			switch state {
			case fillerstructure.AssessmentReservationAccepted:
				return nil
			case fillerstructure.AssessmentReservationHeldBudget:
				return errReservationHeld
			default:
				return fmt.Errorf("filler structure OpenRouter ledger returned invalid reservation state %q", state)
			}
		},
	})
	if callResult.RequestSHA256 == "" || reservationState == "" {
		return fillerstructure.RecordedAssessment{}, fmt.Errorf("filler structure OpenRouter call acquired no durable reservation: %w", callErr)
	}
	recorded, err := a.recordAssessment(media, a.config.Now().UTC().Round(0), reservationState, callResult, callErr)
	if err != nil {
		return fillerstructure.RecordedAssessment{}, err
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settlementTimeout)
	defer cancel()
	if err := a.config.Ledger.Settle(settleCtx, recorded.Record); err != nil {
		return fillerstructure.RecordedAssessment{}, fmt.Errorf("settle filler structure OpenRouter assessment: %w", err)
	}
	return recorded, nil
}

func validateConfig(config Config) error {
	parsed, err := url.Parse(config.BaseURL)
	secure := err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.RawQuery == "" && parsed.Fragment == ""
	testOnly := config.AllowInsecureTestURL && err == nil && parsed.Scheme == "http" && parsed.Host != "" && parsed.RawQuery == "" && parsed.Fragment == ""
	if fillerstructure.ValidateAssessorProfile(config.Profile) != nil || config.Profile.Provider != "openrouter" ||
		!validMetadataSnapshotSHA256(config.MetadataSnapshotSHA256) ||
		config.Profile.Model != config.Model || config.Profile.PromptVersion != fillerstructure.DirectVideoPromptVersion ||
		strings.TrimSpace(config.APIKey) == "" || !secure && !testOnly || strings.TrimRight(config.BaseURL, "/") != config.BaseURL ||
		strings.TrimSpace(config.ResolvedModel) != config.ResolvedModel || config.ResolvedModel == "" ||
		strings.TrimSpace(config.UpstreamProvider) != config.UpstreamProvider || config.UpstreamProvider == "" ||
		strings.TrimSpace(config.UpstreamProviderSlug) != config.UpstreamProviderSlug || config.UpstreamProviderSlug == "" ||
		config.ReservationNanoUSD <= 0 || config.MaximumChargeNanoUSD <= 0 || config.MaximumChargeNanoUSD > config.ReservationNanoUSD ||
		config.MaxTokens <= 0 || config.MaxTokens > 4096 || config.DisableReasoning && config.EnableReasoning ||
		config.Client == nil || config.Ledger == nil || config.Now == nil {
		return fmt.Errorf("filler structure OpenRouter assessor configuration is invalid")
	}
	return nil
}

func validMetadataSnapshotSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func readBoundVideo(ctx context.Context, media filler.StructureAssessmentMedia) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(media.FullPath) || filepath.Clean(media.FullPath) != media.FullPath || strings.ToLower(filepath.Ext(media.FullPath)) != ".mp4" ||
		fillerstructure.ValidateAssessmentMedia(
			fillerstructure.Source{SHA256: media.Source.SHA256, Bytes: media.Source.Bytes, DurationMS: media.Source.DurationMs},
			media.Assessment,
		) != nil {
		return nil, fmt.Errorf("filler structure OpenRouter media identity is invalid")
	}
	pathInfo, err := os.Lstat(media.FullPath)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("filler structure OpenRouter video is not a retained regular file")
	}
	file, err := os.Open(media.FullPath)
	if err != nil {
		return nil, fmt.Errorf("open filler structure video: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() != media.Assessment.Bytes || info.Size() > MaximumVideoBytes {
		return nil, fmt.Errorf("filler structure OpenRouter video is unavailable or outside its byte ceiling")
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaximumVideoBytes+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) != info.Size() || int64(len(raw)) > MaximumVideoBytes {
		return nil, fmt.Errorf("read filler structure OpenRouter video")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != media.Assessment.SHA256 {
		return nil, fmt.Errorf("filler structure OpenRouter video bytes drifted")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}

var _ filler.CompleteTimelineStructureAssessor = (*Assessor)(nil)
