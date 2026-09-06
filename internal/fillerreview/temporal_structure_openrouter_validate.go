package fillerreview

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/httpx"
)

func validateTemporalStructureOpenRouterConfig(config TemporalStructureOpenRouterConfig) (string, *http.Client, func() time.Time, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = fillerbakeoff.OpenRouterBaseURL
	}
	parsed, err := url.Parse(baseURL)
	loopback := err == nil && config.AllowInsecureTestURL && reviewLoopback(parsed.Hostname())
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (!loopback && (parsed.Scheme != "https" || parsed.Hostname() != "openrouter.ai" || parsed.Path != "/api/v1")) {
		return "", nil, nil, fmt.Errorf("OpenRouter structure assessment requires the canonical HTTPS API base")
	}
	if config.APIKey == "" || strings.TrimSpace(config.PublicManifestPath) == "" || strings.TrimSpace(config.CheckpointDir) == "" || strings.TrimSpace(config.Model) == "" || strings.Contains(strings.ToLower(config.Model), "latest") || strings.TrimSpace(config.ModelFamily) == "" || strings.TrimSpace(config.UpstreamProvider) == "" || strings.TrimSpace(config.UpstreamProviderSlug) == "" || strings.TrimSpace(config.AssessorID) == "" || !validTemporalStructureOpenRouterReasoningMode(config.ReasoningMode) || config.ExpectedCases <= 0 || config.MaxRequests != config.ExpectedCases || config.PerCaseTimeout <= 0 || config.MaxSpendNanoUSD <= 0 || config.ReservationNanoUSD <= 0 || config.ReservationNanoUSD > config.MaxSpendNanoUSD {
		return "", nil, nil, fmt.Errorf("OpenRouter structure assessment requires exact identity and one bounded request per expected case")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	if err := validateTemporalStructureOpenRouterSnapshot(config, baseURL, now().UTC()); err != nil {
		return "", nil, nil, err
	}
	estimatedMaximumCharge, err := estimateTemporalStructureOpenRouterCharge(config)
	if err != nil {
		return "", nil, nil, err
	}
	if estimatedMaximumCharge > config.ReservationNanoUSD {
		return "", nil, nil, fmt.Errorf("OpenRouter structure accounting reservation %d nano-USD is below the snapshot price bound %d", config.ReservationNanoUSD, estimatedMaximumCharge)
	}
	client := config.Client
	if client == nil {
		client = httpx.NewNamed("filler-temporal-structure-openrouter", httpx.TimeoutLLM)
	}
	copy := *client
	copy.Timeout = 0
	copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return baseURL, &copy, now, nil
}

func validateTemporalStructureOpenRouterSnapshot(config TemporalStructureOpenRouterConfig, baseURL string, now time.Time) error {
	if err := fillerbakeoff.ValidateOpenRouterSnapshot(config.Snapshot); err != nil {
		return err
	}
	if config.Snapshot.SourceBaseURL != baseURL {
		return fmt.Errorf("OpenRouter structure snapshot does not bind the request base")
	}
	age := now.Sub(config.Snapshot.RetrievedAt)
	if age < 0 || age > 24*time.Hour {
		return fmt.Errorf("OpenRouter structure assessment is outside the snapshot's 24-hour window")
	}
	model := openRouterTemporalModel(config.Snapshot, config.Model)
	if model.ID == "" || !slices.Contains(model.InputModalities, "text") || !slices.Contains(model.InputModalities, "video") {
		return fmt.Errorf("OpenRouter structure model is absent or lacks text/video input")
	}
	for _, endpoint := range model.Endpoints {
		if endpoint.ProviderName == config.UpstreamProvider && endpoint.ProviderSlug == config.UpstreamProviderSlug && endpoint.ZDR && endpoint.Status == 0 && endpoint.MaxCompletionTokens >= temporalStructureOpenRouterMaxTokens && slices.Contains(endpoint.SupportedParameters, "response_format") && slices.Contains(endpoint.SupportedParameters, "structured_outputs") && slices.Contains(endpoint.SupportedParameters, "reasoning") {
			return nil
		}
	}
	return fmt.Errorf("OpenRouter structure route is absent, non-ZDR, or lacks strict structured output with explicit reasoning control")
}

func validateTemporalStructureOpenRouterResult(result TemporalStructureOpenRouterResult, manifest TemporalStructureChallengeManifest, selected []TemporalStructureChallengePublicCase) error {
	if result.SchemaVersion != TemporalStructureOpenRouterResultSchemaVersion || result.ContractVersion != TemporalStructureOpenRouterResultContract || result.ChallengeID != manifest.ChallengeID || !reviewSHA256(result.PublicManifestSHA256) || !reviewSHA256(result.SelectionSHA256) || !reviewSHA256(result.CapabilitySnapshotSHA256) || result.PromptSHA256 != temporalStructureOpenRouterPromptSHA256() || result.ResolvedModel == "" || result.UpstreamProvider == "" || result.UpstreamProviderSlug == "" || !validTemporalStructureOpenRouterReasoningMode(result.ReasoningMode) || result.Assessor.ID == "" || result.Assessor.Provider != "openrouter" || result.Assessor.Model == "" || strings.Contains(strings.ToLower(result.Assessor.Model), "latest") || result.Assessor.ModelFamily == "" || result.Assessor.ModelDigest != result.CapabilitySnapshotSHA256 || result.Assessor.PromptVersion != TemporalStructureOpenRouterPromptVersion || result.Requests != len(selected) || result.Requests != len(result.Attempts) || len(result.Assessments) != len(selected) || result.MaxRequests != len(selected) || result.MaxSpendNanoUSD <= 0 || result.ReservationNanoUSD <= 0 || result.ReservationNanoUSD > result.MaxSpendNanoUSD || result.MaximumInputTokens <= 0 || result.EstimatedMaximumChargeNanoUSD <= 0 || result.EstimatedMaximumChargeNanoUSD > result.ReservationNanoUSD || result.ChargedNanoUSD < 0 || result.ConsumedNanoUSD < result.ChargedNanoUSD || result.OverReservationNanoUSD < 0 || result.CompletedAt.Before(manifest.GeneratedAt) || result.ProductionAdmissionAllowed || len(result.SelectionAliases) != len(selected) {
		return fmt.Errorf("OpenRouter structure result identity, counts, accounting, or admission boundary is invalid")
	}
	if result.SelectionSHA256 != temporalTruthJSONSHA(result.SelectionAliases) || result.UnknownChargeReservations < 0 || result.UnknownChargeReservations > result.Requests {
		return fmt.Errorf("OpenRouter structure result selection or unknown reservations are invalid")
	}
	if err := validateTemporalStructureOpenRouterResultAttempts(result, manifest.GeneratedAt); err != nil {
		return err
	}
	for index, item := range selected {
		assessment := result.Assessments[index]
		attempt := result.Attempts[index]
		if result.SelectionAliases[index] != item.Alias || assessment.Alias != item.Alias || attempt.Alias != item.Alias {
			return fmt.Errorf("OpenRouter structure result is not an ordered selection")
		}
		if err := validateTemporalStructureAssessment(assessment, item.Video.DurationMS, manifest.GeneratedAt, result.CompletedAt); err != nil {
			return err
		}
		call := assessment.Inference.Calls[0]
		if call.ResponseSHA256 != attempt.ResponseSHA256 || call.LatencyMS != attempt.LatencyMS || call.PromptTokens != attempt.PromptTokens || call.CompletionTokens != attempt.CompletionTokens || call.OperationalFailure != attempt.OperationalFailure {
			return fmt.Errorf("OpenRouter structure result assessment and attempt drift")
		}
	}
	return nil
}

func validateTemporalStructureOpenRouterResultAttempts(result TemporalStructureOpenRouterResult, generatedAt time.Time) error {
	for index, attempt := range result.Attempts {
		if attempt.RequestedAt.Before(generatedAt) || attempt.RequestedAt.After(result.CompletedAt) || !reviewSHA256(attempt.RequestSHA256) || attempt.LatencyMS < 0 || attempt.PromptTokens < 0 || attempt.CompletionTokens < 0 {
			return fmt.Errorf("OpenRouter structure result attempt %d has invalid identity or accounting", index)
		}
		if _, err := validateTemporalStructureAttemptAccounting(attempt, result.ReservationNanoUSD); err != nil {
			return fmt.Errorf("OpenRouter structure result attempt %d has invalid terminal settlement", index)
		}
		unsettled := attempt.State == temporalOpenRouterAttemptUnsettled
		if attempt.ResponseSHA256 == "" {
			if !unsettled || attempt.RawResponsePath != "" {
				return fmt.Errorf("OpenRouter structure result attempt %d has no bound response", index)
			}
		} else if !reviewSHA256(attempt.ResponseSHA256) || attempt.RawResponsePath != filepath.ToSlash(filepath.Join(temporalStructureOpenRouterResponsesDir, attempt.Alias+".json")) {
			return fmt.Errorf("OpenRouter structure result attempt %d has invalid response authority", index)
		}
	}
	summary, err := summarizeTemporalStructureAccounting(result.Attempts, result.ReservationNanoUSD, result.MaxSpendNanoUSD)
	if err != nil {
		return fmt.Errorf("OpenRouter structure result accounting is invalid: %w", err)
	}
	if summary.charged != result.ChargedNanoUSD || summary.consumed != result.ConsumedNanoUSD || summary.unknown != result.UnknownChargeReservations || summary.overReservation != result.OverReservationNanoUSD {
		return fmt.Errorf("OpenRouter structure result aggregate spend or reservation accounting drift")
	}
	return nil
}

func validTemporalStructureOpenRouterReasoningMode(value string) bool {
	return value == TemporalStructureOpenRouterReasoningDisabled || value == TemporalStructureOpenRouterReasoningRequired
}
