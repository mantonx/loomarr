package fillerreview

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/httpx"
)

func validateTemporalSuitabilityConfig(config TemporalSuitabilityConfig) (string, *http.Client, func() time.Time, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = fillerbakeoff.OpenRouterBaseURL
	}
	parsed, err := url.Parse(baseURL)
	loopback := err == nil && config.AllowInsecureTestURL && reviewLoopback(parsed.Hostname())
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (!loopback && (parsed.Scheme != "https" || parsed.Hostname() != "openrouter.ai" || parsed.Path != "/api/v1")) {
		return "", nil, nil, fmt.Errorf("OpenRouter suitability screening requires the canonical HTTPS API base")
	}
	if config.APIKey == "" || strings.TrimSpace(config.EvidenceManifestPath) == "" || strings.TrimSpace(config.CheckpointDir) == "" || strings.TrimSpace(config.Model) == "" || strings.Contains(strings.ToLower(config.Model), "latest") || strings.TrimSpace(config.ModelFamily) == "" || strings.TrimSpace(config.UpstreamProvider) == "" || strings.TrimSpace(config.UpstreamProviderSlug) == "" || strings.TrimSpace(config.AssessorID) == "" || !validTemporalSuitabilityReasoningMode(config.ReasoningMode) || config.ExpectedCases <= 0 || config.MaxRequests != config.ExpectedCases || config.PerCaseTimeout <= 0 || config.MaxSpendNanoUSD <= 0 || config.MaxChargeNanoUSD <= 0 || config.MaxChargeNanoUSD > config.MaxSpendNanoUSD {
		return "", nil, nil, fmt.Errorf("OpenRouter suitability screening requires exact identity and one bounded request per expected case")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	if err := validateTemporalSuitabilitySnapshot(config, baseURL, now().UTC()); err != nil {
		return "", nil, nil, err
	}
	client := config.Client
	if client == nil {
		client = httpx.NewNamed("filler-suitability-openrouter", httpx.TimeoutLLM)
	}
	copy := *client
	copy.Timeout = 0
	copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return baseURL, &copy, now, nil
}

func validateTemporalSuitabilitySnapshot(config TemporalSuitabilityConfig, baseURL string, now time.Time) error {
	if err := fillerbakeoff.ValidateOpenRouterSnapshot(config.Snapshot); err != nil {
		return err
	}
	if config.Snapshot.SourceBaseURL != baseURL {
		return fmt.Errorf("OpenRouter suitability snapshot does not bind the request base")
	}
	age := now.Sub(config.Snapshot.RetrievedAt)
	if age < 0 || age > 24*time.Hour {
		return fmt.Errorf("OpenRouter suitability screening is outside the snapshot's 24-hour window")
	}
	model := openRouterTemporalModel(config.Snapshot, config.Model)
	if model.ID == "" || !slices.Contains(model.InputModalities, "text") || !slices.Contains(model.InputModalities, "video") {
		return fmt.Errorf("OpenRouter suitability model is absent or lacks text/video input")
	}
	for _, endpoint := range model.Endpoints {
		if endpoint.ProviderName == config.UpstreamProvider && endpoint.ProviderSlug == config.UpstreamProviderSlug && endpoint.ZDR && endpoint.Status == 0 && endpoint.MaxCompletionTokens >= temporalSuitabilityMaxTokens && slices.Contains(endpoint.SupportedParameters, "response_format") && slices.Contains(endpoint.SupportedParameters, "structured_outputs") && slices.Contains(endpoint.SupportedParameters, "reasoning") {
			return nil
		}
	}
	return fmt.Errorf("OpenRouter suitability route is absent, non-ZDR, or lacks strict structured output with explicit reasoning control")
}

func buildTemporalSuitabilityCheckpointIdentity(config TemporalSuitabilityConfig, manifestSHA, selectionSHA, baseURL string) temporalSuitabilityCheckpointIdentity {
	model := openRouterTemporalModel(config.Snapshot, config.Model)
	return temporalSuitabilityCheckpointIdentity{
		SchemaVersion:          temporalSuitabilityCheckpointSchemaVersion,
		EvidenceManifestSHA256: manifestSHA, SelectionSHA256: selectionSHA,
		CapabilitySnapshotSHA256: fillerbakeoff.OpenRouterSnapshotSHA256(config.Snapshot),
		BaseURL:                  baseURL, Model: config.Model, ResolvedModel: model.CanonicalSlug, ModelFamily: config.ModelFamily,
		UpstreamProvider: config.UpstreamProvider, UpstreamProviderSlug: config.UpstreamProviderSlug,
		PromptVersion: TemporalSuitabilityPromptVersion, PromptSHA256: temporalSuitabilityPromptSHA256(), AssessorID: config.AssessorID,
		ReasoningMode: config.ReasoningMode,
		ExpectedCases: config.ExpectedCases, MaxRequests: config.MaxRequests,
		MaxSpendNanoUSD: config.MaxSpendNanoUSD, MaxChargeNanoUSD: config.MaxChargeNanoUSD,
	}
}

func validTemporalSuitabilityReasoningMode(value string) bool {
	return value == TemporalSuitabilityReasoningDisabled || value == TemporalSuitabilityReasoningRequired
}

func validateTemporalSuitabilityWire(wire temporalSuitabilityWire, durationMS int64) error {
	if durationMS <= 0 || (wire.VisualAssessment != suitabilityVisualCompleted && wire.VisualAssessment != suitabilityVisualInsufficient) || (wire.SpokenLanguageAssessment != suitabilityLanguageCompleted && wire.SpokenLanguageAssessment != suitabilityLanguageInsufficient) || len(wire.Flags) > 16 {
		return fmt.Errorf("suitability coverage assessment or flag count is invalid")
	}
	seen := make(map[string]struct{}, len(wire.Flags))
	for _, flag := range wire.Flags {
		kind := SuitabilityFlag(flag.Kind)
		modality := SuitabilityModality(flag.Modality)
		if (kind != SuitabilityExplicitNudity && kind != SuitabilityHatefulOrDegradingSlur) || flag.StartMS < 0 || flag.EndMS <= flag.StartMS || flag.EndMS > durationMS || (modality != SuitabilityModalityVideo && modality != SuitabilityModalityAudio && modality != SuitabilityModalityTranscript) || kind == SuitabilityExplicitNudity && modality != SuitabilityModalityVideo || kind == SuitabilityHatefulOrDegradingSlur && modality == SuitabilityModalityVideo {
			return fmt.Errorf("suitability flag has an invalid kind, range, or modality")
		}
		key := flag.Kind + "\x00" + flag.Modality + fmt.Sprintf("\x00%d\x00%d", flag.StartMS, flag.EndMS)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("suitability response repeats one flag observation")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateTemporalSuitabilityAssessment(assessment TemporalSuitabilityAssessment, durationMS int64) error {
	if strings.TrimSpace(assessment.EvidenceAlias) == "" || assessment.Inference.AssessedAt.IsZero() || assessment.Inference.Attempts != 1 || len(assessment.Inference.Calls) != 1 || assessment.Inference.Calls[0].Axis != "suitability" || assessment.Inference.Calls[0].Attempt != 1 {
		return fmt.Errorf("suitability assessment identity or inference is invalid")
	}
	if assessment.OperationalFailure != nil {
		if assessment.VisualAssessment != "" || assessment.SpokenLanguageAssessment != "" || len(assessment.Flags) != 0 || assessment.Outcome != "" || !validTemporalOpenRouterFailure(assessment.OperationalFailure.Code) || assessment.Inference.Calls[0].OperationalFailure != assessment.OperationalFailure.Code {
			return fmt.Errorf("suitability operational failure is mixed with semantic output")
		}
		return nil
	}
	wire := temporalSuitabilityWire{VisualAssessment: assessment.VisualAssessment, SpokenLanguageAssessment: assessment.SpokenLanguageAssessment}
	for _, flag := range assessment.Flags {
		wire.Flags = append(wire.Flags, temporalSuitabilityFlagWire{Kind: string(flag.Kind), StartMS: flag.StartMS, EndMS: flag.EndMS, Modality: string(flag.Modality)})
	}
	if err := validateTemporalSuitabilityWire(wire, durationMS); err != nil {
		return err
	}
	expectedOutcome := SuitabilityOutcomeNoSignalObserved
	if len(assessment.Flags) > 0 {
		expectedOutcome = SuitabilityOutcomeProhibitedSignal
	} else if assessment.VisualAssessment == suitabilityVisualInsufficient || assessment.SpokenLanguageAssessment == suitabilityLanguageInsufficient {
		expectedOutcome = SuitabilityOutcomeCoverageHold
	}
	if assessment.Outcome != expectedOutcome || !reviewSHA256(assessment.RawResponseSHA256) || assessment.Inference.Calls[0].ResponseSHA256 != assessment.RawResponseSHA256 || assessment.Inference.Calls[0].OperationalFailure != "" {
		return fmt.Errorf("suitability outcome or response binding is invalid")
	}
	return nil
}

func validateTemporalSuitabilityResult(result TemporalSuitabilityResult, manifest TemporalTruthEvidenceManifest, selected []TemporalTruthEvidenceCase) error {
	if result.SchemaVersion != TemporalSuitabilityResultSchemaVersion || result.ContractVersion != TemporalSuitabilityResultContract || !reviewSHA256(result.EvidenceManifestSHA256) || !reviewSHA256(result.SelectionSHA256) || !reviewSHA256(result.CapabilitySnapshotSHA256) || result.PromptSHA256 != temporalSuitabilityPromptSHA256() || result.ResolvedModel == "" || result.UpstreamProvider == "" || result.UpstreamProviderSlug == "" || result.Assessor.ID == "" || result.Assessor.ModelFamily == "" || result.Assessor.PromptVersion != TemporalSuitabilityPromptVersion || result.Requests != len(selected) || result.Requests != len(result.Attempts) || len(result.Assessments) != len(selected) || result.MaxRequests != len(selected) || result.MaxSpendNanoUSD <= 0 || result.MaxChargeNanoUSD <= 0 || result.ChargedNanoUSD < 0 || result.ConsumedNanoUSD < result.ChargedNanoUSD || result.CompletedAt.IsZero() || result.ProductionAdmissionAllowed || len(result.SelectionAliases) != len(selected) {
		return fmt.Errorf("OpenRouter suitability result identity, counts, accounting, or admission boundary is invalid")
	}
	if result.EvidenceManifestSHA256 == "" || result.SelectionSHA256 != temporalTruthJSONSHA(result.SelectionAliases) || result.UnknownChargeReservations < 0 || result.UnknownChargeReservations > result.Requests {
		return fmt.Errorf("OpenRouter suitability result selection or unknown reservations are invalid")
	}
	if manifest.SelectionSHA256 == "" {
		return fmt.Errorf("suitability evidence manifest lacks selection identity")
	}
	for index, item := range selected {
		if result.SelectionAliases[index] != item.Alias || result.Assessments[index].EvidenceAlias != item.Alias {
			return fmt.Errorf("OpenRouter suitability result is not an ordered selection")
		}
		if err := validateTemporalSuitabilityAssessment(result.Assessments[index], item.DurationMS); err != nil {
			return err
		}
	}
	return nil
}
