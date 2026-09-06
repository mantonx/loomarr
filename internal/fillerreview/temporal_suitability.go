package fillerreview

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	TemporalSuitabilityResultSchemaVersion = 1
	TemporalSuitabilityResultContract      = "filler-suitability-openrouter-v1"
	TemporalSuitabilityMaximumVideoBytes   = int64(64 << 20)
	TemporalSuitabilityReasoningDisabled   = "disabled"
	TemporalSuitabilityReasoningRequired   = "provider_required"
)

type SuitabilityFlag string

const (
	SuitabilityExplicitNudity         SuitabilityFlag = "explicit_nudity"
	SuitabilityHatefulOrDegradingSlur SuitabilityFlag = "hateful_or_degrading_slur"
)

type SuitabilityModality string

const (
	SuitabilityModalityVideo      SuitabilityModality = "video"
	SuitabilityModalityAudio      SuitabilityModality = "audio"
	SuitabilityModalityTranscript SuitabilityModality = "transcript"
)

type TemporalSuitabilityOutcome string

const (
	SuitabilityOutcomeProhibitedSignal TemporalSuitabilityOutcome = "prohibited_signal"
	SuitabilityOutcomeCoverageHold     TemporalSuitabilityOutcome = "coverage_insufficient"
	SuitabilityOutcomeNoSignalObserved TemporalSuitabilityOutcome = "no_prohibited_signal_observed"
)

type TemporalSuitabilityConfig struct {
	EvidenceManifestPath   string
	StructureAuthorityPath string
	CaseAliases            []string
	CheckpointDir          string
	BaseURL                string
	APIKey                 string
	Snapshot               fillerbakeoff.OpenRouterSnapshot
	Model                  string
	ModelFamily            string
	UpstreamProvider       string
	UpstreamProviderSlug   string
	AssessorID             string
	ReasoningMode          string
	ExpectedCases          int
	PerCaseTimeout         time.Duration
	MaxRequests            int
	MaxSpendNanoUSD        int64
	MaxChargeNanoUSD       int64
	AllowInsecureTestURL   bool
	Client                 *http.Client
	Now                    func() time.Time
}

type TemporalSuitabilityResult struct {
	SchemaVersion              int                                 `json:"schemaVersion"`
	ContractVersion            string                              `json:"contractVersion"`
	EvidenceManifestSHA256     string                              `json:"evidenceManifestSha256"`
	SelectionSHA256            string                              `json:"selectionSha256"`
	CapabilitySnapshotSHA256   string                              `json:"capabilitySnapshotSha256"`
	ResolvedModel              string                              `json:"resolvedModel"`
	UpstreamProvider           string                              `json:"upstreamProvider"`
	UpstreamProviderSlug       string                              `json:"upstreamProviderSlug"`
	PromptSHA256               string                              `json:"promptSha256"`
	Assessor                   fillereval.TemporalAssessorIdentity `json:"assessor"`
	SelectionAliases           []string                            `json:"selectionAliases"`
	MaxRequests                int                                 `json:"maxRequests"`
	MaxSpendNanoUSD            int64                               `json:"maxSpendNanoUsd"`
	MaxChargeNanoUSD           int64                               `json:"maxChargeNanoUsd"`
	Requests                   int                                 `json:"requests"`
	ChargedNanoUSD             int64                               `json:"chargedNanoUsd"`
	ConsumedNanoUSD            int64                               `json:"consumedNanoUsd"`
	UnknownChargeReservations  int                                 `json:"unknownChargeReservations"`
	CompletedAt                time.Time                           `json:"completedAt"`
	ProductionAdmissionAllowed bool                                `json:"productionAdmissionAllowed"`
	Assessments                []TemporalSuitabilityAssessment     `json:"assessments"`
	Attempts                   []SuitabilityOpenRouterAttempt      `json:"attempts"`
}

type TemporalSuitabilityAssessment struct {
	EvidenceAlias            string                                 `json:"evidenceAlias"`
	VisualAssessment         string                                 `json:"visualAssessment,omitempty"`
	SpokenLanguageAssessment string                                 `json:"spokenLanguageAssessment,omitempty"`
	Flags                    []TemporalSuitabilityObservation       `json:"flags,omitempty"`
	Outcome                  TemporalSuitabilityOutcome             `json:"outcome,omitempty"`
	RawResponseSHA256        string                                 `json:"rawResponseSha256,omitempty"`
	OperationalFailure       *fillereval.TemporalOperationalFailure `json:"operationalFailure,omitempty"`
	Inference                fillereval.TemporalInference           `json:"inference"`
}

type TemporalSuitabilityObservation struct {
	Kind     SuitabilityFlag     `json:"kind"`
	StartMS  int64               `json:"startMs"`
	EndMS    int64               `json:"endMs"`
	Modality SuitabilityModality `json:"modality"`
}

// RunOpenRouterTemporalSuitability performs one serial, full-video screening
// request per selected case. A no-signal observation is diagnostic evidence,
// never permission to admit: production admission remains false until recall
// is certified independently for each prohibited flag.
func RunOpenRouterTemporalSuitability(ctx context.Context, config TemporalSuitabilityConfig) (result TemporalSuitabilityResult, err error) {
	baseURL, client, now, err := validateTemporalSuitabilityConfig(config)
	if err != nil {
		return TemporalSuitabilityResult{}, err
	}
	manifest, manifestSHA, err := loadTemporalSuitabilityEvidence(config.EvidenceManifestPath, config.StructureAuthorityPath)
	if err != nil {
		return TemporalSuitabilityResult{}, err
	}
	selected, aliases, selectionSHA, err := selectTemporalSuitabilityCases(manifest, config.CaseAliases, config.ExpectedCases)
	if err != nil {
		return TemporalSuitabilityResult{}, err
	}
	identity := buildTemporalSuitabilityCheckpointIdentity(config, manifestSHA, selectionSHA, baseURL)
	activeLock, err := acquireOpenRouterActiveRunLock(config.CheckpointDir, identity, now, nil)
	if err != nil {
		return TemporalSuitabilityResult{}, err
	}
	defer func() {
		if releaseErr := activeLock.release(); releaseErr != nil {
			result = TemporalSuitabilityResult{}
			err = errors.Join(err, releaseErr)
		}
	}()
	checkpoint, err := loadTemporalSuitabilityCheckpoint(config.CheckpointDir, identity, selected)
	if err != nil {
		return TemporalSuitabilityResult{}, err
	}
	if len(checkpoint.Attempts) > len(checkpoint.Assessments) {
		return TemporalSuitabilityResult{}, fmt.Errorf("OpenRouter suitability checkpoint has an unsettled crash reservation for alias %q", checkpoint.Attempts[len(checkpoint.Attempts)-1].EvidenceAlias)
	}
	root := filepath.Dir(config.EvidenceManifestPath)
	for index := len(checkpoint.Assessments); index < len(selected); index++ {
		assessment, assessErr := assessOpenRouterSuitabilityCase(ctx, client, baseURL, root, config, selected[index], &checkpoint, selected, now)
		if assessErr != nil {
			return TemporalSuitabilityResult{}, assessErr
		}
		checkpoint.Assessments = append(checkpoint.Assessments, assessment)
		if err := persistTemporalSuitabilityCheckpoint(config.CheckpointDir, checkpoint, selected); err != nil {
			return TemporalSuitabilityResult{}, fmt.Errorf("persist completed OpenRouter suitability case: %w", err)
		}
	}
	consumed, err := temporalSuitabilityCheckpointSpend(checkpoint)
	if err != nil {
		return TemporalSuitabilityResult{}, err
	}
	result = TemporalSuitabilityResult{
		SchemaVersion: TemporalSuitabilityResultSchemaVersion, ContractVersion: TemporalSuitabilityResultContract,
		EvidenceManifestSHA256: manifestSHA, SelectionSHA256: selectionSHA,
		CapabilitySnapshotSHA256: identity.CapabilitySnapshotSHA256, ResolvedModel: identity.ResolvedModel,
		UpstreamProvider: config.UpstreamProvider, UpstreamProviderSlug: config.UpstreamProviderSlug,
		PromptSHA256:     identity.PromptSHA256,
		Assessor:         fillereval.TemporalAssessorIdentity{ID: config.AssessorID, Provider: "openrouter", Model: config.Model, ModelFamily: config.ModelFamily, ModelDigest: identity.CapabilitySnapshotSHA256, PromptVersion: TemporalSuitabilityPromptVersion},
		SelectionAliases: aliases, MaxRequests: config.MaxRequests, MaxSpendNanoUSD: config.MaxSpendNanoUSD, MaxChargeNanoUSD: config.MaxChargeNanoUSD,
		Requests: len(checkpoint.Attempts), ConsumedNanoUSD: consumed, CompletedAt: now().UTC(), ProductionAdmissionAllowed: false,
		Assessments: slices.Clone(checkpoint.Assessments), Attempts: slices.Clone(checkpoint.Attempts),
	}
	for _, attempt := range checkpoint.Attempts {
		result.ChargedNanoUSD += attempt.ChargedNanoUSD
		if attempt.State == temporalOpenRouterAttemptUnsettled {
			result.UnknownChargeReservations++
		}
	}
	if err := validateTemporalSuitabilityResult(result, manifest, selected); err != nil {
		return TemporalSuitabilityResult{}, err
	}
	return result, nil
}

func assessOpenRouterSuitabilityCase(ctx context.Context, client *http.Client, baseURL, root string, config TemporalSuitabilityConfig, item TemporalTruthEvidenceCase, checkpoint *temporalSuitabilityCheckpoint, selected []TemporalTruthEvidenceCase, now func() time.Time) (TemporalSuitabilityAssessment, error) {
	caseCtx, cancel := context.WithTimeout(ctx, config.PerCaseTimeout)
	defer cancel()
	videoPath := filepath.Join(root, filepath.FromSlash(item.Video.Path))
	video, err := os.ReadFile(videoPath)
	if err != nil || int64(len(video)) != item.Video.Bytes || hashBytes(video) != item.Video.SHA256 || len(video) == 0 || int64(len(video)) > TemporalSuitabilityMaximumVideoBytes {
		return TemporalSuitabilityAssessment{}, fmt.Errorf("verified suitability video for alias %q is unavailable, drifted, or outside its byte ceiling", item.Alias)
	}
	started := time.Now()
	callResult, callErr := callOpenRouterStructured(caseCtx, client, baseURL, openRouterStructuredCallConfig{
		APIKey: config.APIKey, Model: config.Model, ResolvedModel: checkpoint.Identity.ResolvedModel,
		UpstreamProvider: config.UpstreamProvider, ProviderSlug: config.UpstreamProviderSlug,
		SchemaName: "filler_suitability", Schema: temporalSuitabilitySchema(item.DurationMS),
		SystemPrompt: temporalSuitabilitySystemPrompt, Content: temporalSuitabilityContent(item),
		Videos:    []openRouterStructuredVideo{{MIMEType: "video/mp4", Base64: base64.StdEncoding.EncodeToString(video)}},
		MaxTokens: temporalSuitabilityMaxTokens, MaxChargeNanoUSD: config.MaxChargeNanoUSD, DisableReasoning: config.ReasoningMode == TemporalSuitabilityReasoningDisabled,
		Title: temporalSuitabilityRequestTitle,
		Reserve: func(requestSHA string) error {
			spent, spendErr := temporalSuitabilityCheckpointSpend(*checkpoint)
			if spendErr != nil {
				return spendErr
			}
			if len(checkpoint.Attempts) >= config.MaxRequests || spent > config.MaxSpendNanoUSD-config.MaxChargeNanoUSD {
				return fmt.Errorf("%w before suitability alias %q", errTemporalOpenRouterBudget, item.Alias)
			}
			checkpoint.Attempts = append(checkpoint.Attempts, SuitabilityOpenRouterAttempt{
				EvidenceAlias: item.Alias, RequestedAt: now().UTC(), RequestSHA256: requestSHA,
				State: temporalOpenRouterAttemptReserved, ReservedNanoUSD: config.MaxChargeNanoUSD,
			})
			return persistTemporalSuitabilityCheckpoint(config.CheckpointDir, *checkpoint, selected)
		},
	})
	latency := max(int64(0), time.Since(started).Milliseconds())
	call := fillereval.TemporalInferenceCall{
		Axis: "suitability", Attempt: 1, ResponseSHA256: callResult.ResponseSHA256,
		LatencyMS: latency, PromptTokens: callResult.Wire.Usage.PromptTokens, CompletionTokens: callResult.Wire.Usage.CompletionTokens,
	}
	if callResult.ResponseSHA256 != "" {
		relative, writeErr := writeTemporalSuitabilityRawResponse(config.CheckpointDir, item.Alias, callResult.RawResponse)
		if writeErr != nil {
			return TemporalSuitabilityAssessment{}, fmt.Errorf("persist raw OpenRouter suitability response: %w", writeErr)
		}
		checkpoint.Attempts[len(checkpoint.Attempts)-1].RawResponsePath = relative
	}
	var wire temporalSuitabilityWire
	if callErr == nil {
		if decodeErr := decodeStrictReviewJSON([]byte(callResult.StructuredOutput), &wire); decodeErr != nil {
			callErr = fmt.Errorf("suitability assessment JSON is invalid: %w", decodeErr)
		}
	}
	var failure *temporalCallError
	if callErr != nil {
		failure = classifyTemporalOpenRouterFailure(caseCtx, callResult, callErr)
		call.OperationalFailure = failure.code
	} else if validateErr := validateTemporalSuitabilityWire(wire, item.DurationMS); validateErr != nil {
		failure = &temporalCallError{code: fillereval.TemporalFailureInvalidResponse, detail: validateErr.Error()}
		call.OperationalFailure = failure.code
	}
	if callResult.RequestSHA256 == "" || len(checkpoint.Attempts) == 0 || checkpoint.Attempts[len(checkpoint.Attempts)-1].RequestSHA256 != callResult.RequestSHA256 {
		if errors.Is(callErr, errTemporalOpenRouterBudget) {
			return TemporalSuitabilityAssessment{}, callErr
		}
		return TemporalSuitabilityAssessment{}, fmt.Errorf("OpenRouter suitability call for alias %q did not acquire a durable reservation: %w", item.Alias, callErr)
	}
	attempt := &checkpoint.Attempts[len(checkpoint.Attempts)-1]
	attempt.ResponseSHA256, attempt.GenerationID = callResult.ResponseSHA256, callResult.Wire.ID
	attempt.LatencyMS, attempt.PromptTokens, attempt.CompletionTokens = latency, call.PromptTokens, call.CompletionTokens
	if callResult.ChargeKnown {
		attempt.ChargedAmountUSD, attempt.ChargedNanoUSD = callResult.Wire.Usage.Cost.String(), callResult.ChargedNanoUSD
	}
	if failure == nil {
		attempt.State = temporalOpenRouterAttemptAccepted
	} else {
		attempt.OperationalFailure = failure.code
		if callResult.ChargeKnown {
			attempt.State = temporalOpenRouterAttemptFailed
		} else {
			attempt.State = temporalOpenRouterAttemptUnsettled
		}
	}
	if failure != nil {
		return temporalSuitabilityFailedAssessment(item.Alias, now().UTC(), call, failure), nil
	}
	assessment := temporalSuitabilityAssessmentFromWire(item.Alias, wire, callResult.ResponseSHA256, now().UTC(), call)
	if err := validateTemporalSuitabilityAssessment(assessment, item.DurationMS); err != nil {
		return TemporalSuitabilityAssessment{}, err
	}
	return assessment, nil
}

func temporalSuitabilityAssessmentFromWire(alias string, wire temporalSuitabilityWire, rawResponseSHA string, assessedAt time.Time, call fillereval.TemporalInferenceCall) TemporalSuitabilityAssessment {
	assessment := TemporalSuitabilityAssessment{
		EvidenceAlias: alias, VisualAssessment: wire.VisualAssessment, SpokenLanguageAssessment: wire.SpokenLanguageAssessment,
		RawResponseSHA256: rawResponseSHA, Inference: temporalInferenceFromCalls(assessedAt, []fillereval.TemporalInferenceCall{call}),
	}
	for _, flag := range wire.Flags {
		assessment.Flags = append(assessment.Flags, TemporalSuitabilityObservation{Kind: SuitabilityFlag(flag.Kind), StartMS: flag.StartMS, EndMS: flag.EndMS, Modality: SuitabilityModality(flag.Modality)})
	}
	sort.Slice(assessment.Flags, func(i, j int) bool {
		if assessment.Flags[i].StartMS != assessment.Flags[j].StartMS {
			return assessment.Flags[i].StartMS < assessment.Flags[j].StartMS
		}
		if assessment.Flags[i].Kind != assessment.Flags[j].Kind {
			return assessment.Flags[i].Kind < assessment.Flags[j].Kind
		}
		return assessment.Flags[i].EndMS < assessment.Flags[j].EndMS
	})
	switch {
	case len(assessment.Flags) > 0:
		assessment.Outcome = SuitabilityOutcomeProhibitedSignal
	case assessment.VisualAssessment == suitabilityVisualInsufficient || assessment.SpokenLanguageAssessment == suitabilityLanguageInsufficient:
		assessment.Outcome = SuitabilityOutcomeCoverageHold
	default:
		assessment.Outcome = SuitabilityOutcomeNoSignalObserved
	}
	return assessment
}

func temporalSuitabilityFailedAssessment(alias string, assessedAt time.Time, call fillereval.TemporalInferenceCall, failure *temporalCallError) TemporalSuitabilityAssessment {
	if failure == nil {
		failure = &temporalCallError{code: fillereval.TemporalFailureContextExhausted, detail: "suitability request was not reserved"}
	}
	return TemporalSuitabilityAssessment{
		EvidenceAlias: alias, RawResponseSHA256: call.ResponseSHA256,
		OperationalFailure: &fillereval.TemporalOperationalFailure{Code: failure.code, Detail: boundedTemporalDetail(failure.detail), Retryable: failure.retryable},
		Inference:          temporalInferenceFromCalls(assessedAt, []fillereval.TemporalInferenceCall{call}),
	}
}

func selectTemporalSuitabilityCases(manifest TemporalTruthEvidenceManifest, requested []string, expected int) ([]TemporalTruthEvidenceCase, []string, string, error) {
	byAlias := make(map[string]TemporalTruthEvidenceCase, len(manifest.Cases))
	for _, item := range manifest.Cases {
		byAlias[item.Alias] = item
	}
	aliases := slices.Clone(requested)
	if len(aliases) == 0 {
		for _, item := range manifest.Cases {
			aliases = append(aliases, item.Alias)
		}
	}
	sort.Strings(aliases)
	aliases = slices.Compact(aliases)
	if expected <= 0 || len(aliases) != expected {
		return nil, nil, "", fmt.Errorf("suitability selection has %d unique cases; want exactly %d", len(aliases), expected)
	}
	selected := make([]TemporalTruthEvidenceCase, 0, len(aliases))
	for _, alias := range aliases {
		item, exists := byAlias[alias]
		if !exists {
			return nil, nil, "", fmt.Errorf("suitability selection names unknown evidence alias %q", alias)
		}
		selected = append(selected, item)
	}
	selectionSHA := temporalTruthJSONSHA(aliases)
	return selected, aliases, selectionSHA, nil
}

func EncodeTemporalSuitabilityResult(result TemporalSuitabilityResult) ([]byte, error) {
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func DecodeTemporalSuitabilityResult(raw []byte) (TemporalSuitabilityResult, error) {
	var result TemporalSuitabilityResult
	if err := decodeStrictReviewJSON(raw, &result); err != nil {
		return TemporalSuitabilityResult{}, err
	}
	return result, nil
}
