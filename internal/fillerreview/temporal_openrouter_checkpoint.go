package fillerreview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	temporalOpenRouterCheckpointSchemaVersion = 1
	temporalOpenRouterCheckpointFilename      = "temporal-checkpoint.json"
	temporalOpenRouterAttemptReserved         = "reserved"
	temporalOpenRouterAttemptAccepted         = "accepted"
	temporalOpenRouterAttemptFailed           = "failed"
	temporalOpenRouterAttemptUnsettled        = "unsettled"
	temporalOpenRouterAttemptOverReservation  = "over_reservation"
)

type TemporalOpenRouterAttempt struct {
	Alias              string                         `json:"alias"`
	Axis               string                         `json:"axis"`
	Attempt            int                            `json:"attempt"`
	RequestedAt        time.Time                      `json:"requestedAt"`
	RequestSHA256      string                         `json:"requestSha256"`
	ResponseSHA256     string                         `json:"responseSha256,omitempty"`
	GenerationID       string                         `json:"generationId,omitempty"`
	State              string                         `json:"state"`
	LatencyMS          int64                          `json:"latencyMs,omitempty"`
	PromptTokens       int64                          `json:"promptTokens,omitempty"`
	CompletionTokens   int64                          `json:"completionTokens,omitempty"`
	ChargedAmountUSD   string                         `json:"chargedAmountUsd,omitempty"`
	ChargedNanoUSD     int64                          `json:"chargedNanoUsd,omitempty"`
	ReservedNanoUSD    int64                          `json:"reservedNanoUsd"`
	OperationalFailure fillereval.TemporalFailureCode `json:"operationalFailure,omitempty"`
}

type temporalOpenRouterCheckpointIdentity struct {
	SchemaVersion            int    `json:"schemaVersion"`
	PackageSHA256            string `json:"packageSha256"`
	SelectionSHA256          string `json:"selectionSha256"`
	CapabilitySnapshotSHA256 string `json:"capabilitySnapshotSha256"`
	BaseURL                  string `json:"baseUrl"`
	Model                    string `json:"model"`
	ResolvedModel            string `json:"resolvedModel"`
	ModelFamily              string `json:"modelFamily"`
	UpstreamProvider         string `json:"upstreamProvider"`
	UpstreamProviderSlug     string `json:"upstreamProviderSlug"`
	PromptVersion            string `json:"promptVersion"`
	PromptSHA256             string `json:"promptSha256"`
	AssessorID               string `json:"assessorId"`
	BatchID                  string `json:"batchId"`
	ExpectedPackageCases     int    `json:"expectedPackageCases"`
	ExpectedCalibrationCases int    `json:"expectedCalibrationCases"`
	MaxRequests              int    `json:"maxRequests"`
	MaxSpendNanoUSD          int64  `json:"maxSpendNanoUsd"`
	MaxChargeNanoUSD         int64  `json:"maxChargeNanoUsd"`
}

type temporalOpenRouterCheckpoint struct {
	Identity    temporalOpenRouterCheckpointIdentity `json:"identity"`
	Attempts    []TemporalOpenRouterAttempt          `json:"attempts"`
	Assessments []fillereval.TemporalAssessment      `json:"assessments"`
}

func buildTemporalOpenRouterCheckpointIdentity(config OpenRouterTemporalConfig, loaded temporalInferencePackage, baseURL string) temporalOpenRouterCheckpointIdentity {
	model := openRouterTemporalModel(config.Snapshot, config.Model)
	return temporalOpenRouterCheckpointIdentity{
		SchemaVersion: temporalOpenRouterCheckpointSchemaVersion, PackageSHA256: loaded.PackageSHA256,
		SelectionSHA256: loaded.SelectionSHA256, CapabilitySnapshotSHA256: fillerbakeoff.OpenRouterSnapshotSHA256(config.Snapshot),
		BaseURL: baseURL, Model: config.Model, ResolvedModel: model.CanonicalSlug, ModelFamily: config.ModelFamily,
		UpstreamProvider: config.UpstreamProvider, UpstreamProviderSlug: config.UpstreamProviderSlug,
		PromptVersion: OpenRouterTemporalPromptVersion, PromptSHA256: temporalOpenRouterPromptSHA256(),
		AssessorID: config.AssessorID, BatchID: loaded.BatchID,
		ExpectedPackageCases: config.ExpectedPackageCases, ExpectedCalibrationCases: config.ExpectedCalibrationCases,
		MaxRequests: config.MaxRequests, MaxSpendNanoUSD: config.MaxSpendNanoUSD, MaxChargeNanoUSD: config.MaxChargeNanoUSD,
	}
}

func loadTemporalOpenRouterCheckpoint(dir string, identity temporalOpenRouterCheckpointIdentity) (temporalOpenRouterCheckpoint, error) {
	if err := ensureOpenRouterCheckpointDir(dir); err != nil {
		return temporalOpenRouterCheckpoint{}, err
	}
	path := filepath.Join(dir, temporalOpenRouterCheckpointFilename)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return temporalOpenRouterCheckpoint{Identity: identity}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return temporalOpenRouterCheckpoint{}, fmt.Errorf("OpenRouter temporal checkpoint must be a private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return temporalOpenRouterCheckpoint{}, fmt.Errorf("read OpenRouter temporal checkpoint: %w", err)
	}
	var checkpoint temporalOpenRouterCheckpoint
	if err := decodeStrictReviewJSON(raw, &checkpoint); err != nil {
		return temporalOpenRouterCheckpoint{}, fmt.Errorf("decode OpenRouter temporal checkpoint: %w", err)
	}
	if !reflect.DeepEqual(checkpoint.Identity, identity) {
		return temporalOpenRouterCheckpoint{}, fmt.Errorf("OpenRouter temporal checkpoint identity drift")
	}
	if err := validateTemporalOpenRouterCheckpoint(checkpoint); err != nil {
		return temporalOpenRouterCheckpoint{}, err
	}
	return checkpoint, nil
}

func persistTemporalOpenRouterCheckpoint(dir string, checkpoint temporalOpenRouterCheckpoint) error {
	if err := validateTemporalOpenRouterCheckpoint(checkpoint); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(dir, ".temporal-checkpoint-*")
	if err != nil {
		return fmt.Errorf("create OpenRouter temporal checkpoint: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, filepath.Join(dir, temporalOpenRouterCheckpointFilename)); err != nil {
		return fmt.Errorf("publish OpenRouter temporal checkpoint: %w", err)
	}
	return syncOpenRouterReviewDir(dir)
}

func validateTemporalOpenRouterCheckpoint(checkpoint temporalOpenRouterCheckpoint) error {
	identity := checkpoint.Identity
	if identity.SchemaVersion != temporalOpenRouterCheckpointSchemaVersion || !reviewSHA256(identity.PackageSHA256) || !reviewSHA256(identity.SelectionSHA256) || !reviewSHA256(identity.CapabilitySnapshotSHA256) || !reviewSHA256(identity.PromptSHA256) || identity.BaseURL == "" || identity.Model == "" || identity.ResolvedModel == "" || identity.ModelFamily == "" || identity.UpstreamProvider == "" || identity.UpstreamProviderSlug == "" || identity.PromptVersion == "" || identity.AssessorID == "" || identity.BatchID == "" || identity.ExpectedPackageCases <= 0 || identity.ExpectedCalibrationCases <= 0 || identity.MaxRequests < identity.ExpectedCalibrationCases || identity.MaxRequests > identity.ExpectedCalibrationCases*2 || identity.MaxSpendNanoUSD <= 0 || identity.MaxChargeNanoUSD <= 0 || identity.MaxChargeNanoUSD > identity.MaxSpendNanoUSD {
		return fmt.Errorf("OpenRouter temporal checkpoint identity is invalid")
	}
	seenAssessments := make(map[string]struct{}, len(checkpoint.Assessments))
	for _, assessment := range checkpoint.Assessments {
		if assessment.Alias == "" {
			return fmt.Errorf("OpenRouter temporal checkpoint contains an unbound assessment")
		}
		if _, duplicate := seenAssessments[assessment.Alias]; duplicate {
			return fmt.Errorf("OpenRouter temporal checkpoint repeats assessment alias %q", assessment.Alias)
		}
		seenAssessments[assessment.Alias] = struct{}{}
	}
	if len(checkpoint.Attempts) > identity.MaxRequests || len(checkpoint.Assessments) > identity.ExpectedCalibrationCases {
		return fmt.Errorf("OpenRouter temporal checkpoint counts exceed their ceilings")
	}
	seenAxis := map[string]struct{}{}
	var consumed int64
	for _, attempt := range checkpoint.Attempts {
		key := attempt.Alias + "\x00" + attempt.Axis
		if _, duplicate := seenAxis[key]; duplicate {
			return fmt.Errorf("OpenRouter temporal checkpoint repeats alias/axis %q", key)
		}
		seenAxis[key] = struct{}{}
		settled, chargeErr := fillereval.USDToNanoCeil(attempt.ChargedAmountUSD)
		stateValid := attempt.State == temporalOpenRouterAttemptReserved || attempt.State == temporalOpenRouterAttemptAccepted || attempt.State == temporalOpenRouterAttemptFailed || attempt.State == temporalOpenRouterAttemptUnsettled
		chargeKnown := attempt.State == temporalOpenRouterAttemptAccepted || attempt.State == temporalOpenRouterAttemptFailed
		if attempt.Alias == "" || (attempt.Axis != "unit" && attempt.Axis != "role") || (attempt.Axis == "unit" && attempt.Attempt != 1) || (attempt.Axis == "role" && attempt.Attempt != 2) || attempt.RequestedAt.IsZero() || !reviewSHA256(attempt.RequestSHA256) || !stateValid || attempt.LatencyMS < 0 || attempt.PromptTokens < 0 || attempt.CompletionTokens < 0 || attempt.ReservedNanoUSD != identity.MaxChargeNanoUSD || attempt.ChargedNanoUSD < 0 || attempt.ChargedNanoUSD > attempt.ReservedNanoUSD || (chargeKnown && (chargeErr != nil || settled != attempt.ChargedNanoUSD)) || (!chargeKnown && (attempt.ChargedAmountUSD != "" || attempt.ChargedNanoUSD != 0)) || attempt.State == temporalOpenRouterAttemptAccepted && attempt.OperationalFailure != "" || (attempt.State == temporalOpenRouterAttemptFailed || attempt.State == temporalOpenRouterAttemptUnsettled) && !validTemporalOpenRouterFailure(attempt.OperationalFailure) || attempt.State == temporalOpenRouterAttemptReserved && (attempt.ResponseSHA256 != "" || attempt.GenerationID != "" || attempt.OperationalFailure != "") || attempt.ResponseSHA256 != "" && !reviewSHA256(attempt.ResponseSHA256) {
			return fmt.Errorf("OpenRouter temporal checkpoint attempt ledger is invalid")
		}
		cost := attempt.ChargedNanoUSD
		if !chargeKnown {
			cost = attempt.ReservedNanoUSD
		}
		if consumed > identity.MaxSpendNanoUSD-cost {
			return fmt.Errorf("OpenRouter temporal checkpoint exceeds its spend ceiling")
		}
		consumed += cost
	}
	return nil
}

func validateTemporalOpenRouterCheckpointAgainstSelection(checkpoint temporalOpenRouterCheckpoint, loaded temporalInferencePackage) error {
	if len(checkpoint.Assessments) > len(loaded.Cases) {
		return fmt.Errorf("OpenRouter temporal checkpoint has too many completed assessments")
	}
	attemptIndex := 0
	for assessmentIndex, assessment := range checkpoint.Assessments {
		if assessment.Alias != loaded.Cases[assessmentIndex].Alias {
			return fmt.Errorf("OpenRouter temporal checkpoint assessments are not an ordered selection prefix")
		}
		for _, call := range assessment.Inference.Calls {
			if assessment.OperationalFailure != nil && (assessment.OperationalFailure.Code == fillereval.TemporalFailureEvidence || assessment.OperationalFailure.Code == fillereval.TemporalFailureContextExhausted) && call.ResponseSHA256 == "" {
				continue
			}
			if attemptIndex >= len(checkpoint.Attempts) {
				return fmt.Errorf("OpenRouter temporal checkpoint assessment lacks an attempt ledger row")
			}
			attempt := checkpoint.Attempts[attemptIndex]
			if attempt.Alias != assessment.Alias || attempt.Axis != call.Axis || attempt.Attempt != call.Attempt || attempt.ResponseSHA256 != call.ResponseSHA256 || attempt.LatencyMS != call.LatencyMS || attempt.PromptTokens != call.PromptTokens || attempt.CompletionTokens != call.CompletionTokens || attempt.OperationalFailure != call.OperationalFailure {
				return fmt.Errorf("OpenRouter temporal checkpoint assessment and attempt ledger drift for alias %q axis %q", assessment.Alias, call.Axis)
			}
			attemptIndex++
		}
	}
	if attemptIndex != len(checkpoint.Attempts) {
		return fmt.Errorf("OpenRouter temporal checkpoint has a settled attempt without a completed assessment; explicit recovery is required")
	}
	return nil
}

func validTemporalOpenRouterFailure(code fillereval.TemporalFailureCode) bool {
	return code == fillereval.TemporalFailureTimeout || code == fillereval.TemporalFailureProvider || code == fillereval.TemporalFailureInvalidResponse || code == fillereval.TemporalFailureEvidence || code == fillereval.TemporalFailureContextExhausted
}

func temporalOpenRouterCheckpointSpend(checkpoint temporalOpenRouterCheckpoint) (int64, error) {
	var consumed int64
	for _, attempt := range checkpoint.Attempts {
		cost := attempt.ChargedNanoUSD
		if attempt.State == temporalOpenRouterAttemptReserved || attempt.State == temporalOpenRouterAttemptUnsettled {
			cost = attempt.ReservedNanoUSD
		}
		if consumed > checkpoint.Identity.MaxSpendNanoUSD-cost {
			return 0, fmt.Errorf("OpenRouter temporal checkpoint exhausts its spend ceiling")
		}
		consumed += cost
	}
	return consumed, nil
}

func temporalOpenRouterPromptSHA256() string {
	return hashBytes([]byte(strings.Join([]string{temporalHostedUnitSystemPrompt, temporalHostedRoleSystemPrompt}, "\x00")))
}
