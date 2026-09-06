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
	temporalStructureOpenRouterCheckpointSchemaVersion = 2
	temporalStructureOpenRouterCheckpointFilename      = "structure-checkpoint.json"
	temporalStructureOpenRouterResponsesDir            = "responses"
)

type TemporalStructureOpenRouterAttempt struct {
	Alias              string                         `json:"alias"`
	RequestedAt        time.Time                      `json:"requestedAt"`
	RequestSHA256      string                         `json:"requestSha256"`
	ResponseSHA256     string                         `json:"responseSha256,omitempty"`
	RawResponsePath    string                         `json:"rawResponsePath,omitempty"`
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

type temporalStructureOpenRouterCheckpointIdentity struct {
	SchemaVersion                 int       `json:"schemaVersion"`
	ChallengeID                   string    `json:"challengeId"`
	ChallengeGeneratedAt          time.Time `json:"challengeGeneratedAt"`
	PublicManifestSHA256          string    `json:"publicManifestSha256"`
	SelectionSHA256               string    `json:"selectionSha256"`
	CapabilitySnapshotSHA256      string    `json:"capabilitySnapshotSha256"`
	BaseURL                       string    `json:"baseUrl"`
	Model                         string    `json:"model"`
	ResolvedModel                 string    `json:"resolvedModel"`
	ModelFamily                   string    `json:"modelFamily"`
	UpstreamProvider              string    `json:"upstreamProvider"`
	UpstreamProviderSlug          string    `json:"upstreamProviderSlug"`
	PromptVersion                 string    `json:"promptVersion"`
	PromptSHA256                  string    `json:"promptSha256"`
	ReasoningMode                 string    `json:"reasoningMode"`
	AssessorID                    string    `json:"assessorId"`
	ExpectedCases                 int       `json:"expectedCases"`
	MaxRequests                   int       `json:"maxRequests"`
	MaxSpendNanoUSD               int64     `json:"maxSpendNanoUsd"`
	ReservationNanoUSD            int64     `json:"reservationNanoUsd"`
	MaximumInputTokens            int64     `json:"maximumInputTokens"`
	EstimatedMaximumChargeNanoUSD int64     `json:"estimatedMaximumChargeNanoUsd"`
}

type temporalStructureOpenRouterCheckpoint struct {
	Identity    temporalStructureOpenRouterCheckpointIdentity `json:"identity"`
	Attempts    []TemporalStructureOpenRouterAttempt          `json:"attempts"`
	Assessments []TemporalStructureAssessment                 `json:"assessments"`
}

func buildTemporalStructureOpenRouterCheckpointIdentity(config TemporalStructureOpenRouterConfig, manifest TemporalStructureChallengeManifest, manifestSHA, selectionSHA, baseURL string) temporalStructureOpenRouterCheckpointIdentity {
	model := openRouterTemporalModel(config.Snapshot, config.Model)
	estimatedMaximumCharge, _ := estimateTemporalStructureOpenRouterCharge(config)
	return temporalStructureOpenRouterCheckpointIdentity{
		SchemaVersion: temporalStructureOpenRouterCheckpointSchemaVersion,
		ChallengeID:   manifest.ChallengeID, ChallengeGeneratedAt: manifest.GeneratedAt,
		PublicManifestSHA256: manifestSHA, SelectionSHA256: selectionSHA,
		CapabilitySnapshotSHA256: fillerbakeoff.OpenRouterSnapshotSHA256(config.Snapshot),
		BaseURL:                  baseURL, Model: config.Model, ResolvedModel: model.CanonicalSlug, ModelFamily: config.ModelFamily,
		UpstreamProvider: config.UpstreamProvider, UpstreamProviderSlug: config.UpstreamProviderSlug,
		PromptVersion: TemporalStructureOpenRouterPromptVersion, PromptSHA256: temporalStructureOpenRouterPromptSHA256(),
		ReasoningMode: config.ReasoningMode, AssessorID: config.AssessorID,
		ExpectedCases: config.ExpectedCases, MaxRequests: config.MaxRequests,
		MaxSpendNanoUSD: config.MaxSpendNanoUSD, ReservationNanoUSD: config.ReservationNanoUSD,
		MaximumInputTokens: config.MaximumInputTokens, EstimatedMaximumChargeNanoUSD: estimatedMaximumCharge,
	}
}

func loadTemporalStructureOpenRouterCheckpoint(dir string, identity temporalStructureOpenRouterCheckpointIdentity, selected []TemporalStructureChallengePublicCase) (temporalStructureOpenRouterCheckpoint, error) {
	if err := ensureOpenRouterCheckpointDir(dir); err != nil {
		return temporalStructureOpenRouterCheckpoint{}, err
	}
	path := filepath.Join(dir, temporalStructureOpenRouterCheckpointFilename)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return temporalStructureOpenRouterCheckpoint{Identity: identity}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return temporalStructureOpenRouterCheckpoint{}, fmt.Errorf("OpenRouter structure checkpoint must be a private regular file")
	}
	checkpoint, err := readStrictJSON[temporalStructureOpenRouterCheckpoint](path)
	if err != nil {
		return temporalStructureOpenRouterCheckpoint{}, err
	}
	if !reflect.DeepEqual(checkpoint.Identity, identity) {
		return temporalStructureOpenRouterCheckpoint{}, fmt.Errorf("OpenRouter structure checkpoint identity drift")
	}
	if err := validateTemporalStructureOpenRouterCheckpoint(dir, checkpoint, selected); err != nil {
		return temporalStructureOpenRouterCheckpoint{}, err
	}
	return checkpoint, nil
}

func persistTemporalStructureOpenRouterCheckpoint(dir string, checkpoint temporalStructureOpenRouterCheckpoint, selected []TemporalStructureChallengePublicCase) error {
	if err := validateTemporalStructureOpenRouterCheckpoint(dir, checkpoint, selected); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(dir, ".structure-checkpoint-*")
	if err != nil {
		return err
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
	if err := os.Rename(temporaryPath, filepath.Join(dir, temporalStructureOpenRouterCheckpointFilename)); err != nil {
		return err
	}
	return syncOpenRouterReviewDir(dir)
}

func validateTemporalStructureOpenRouterCheckpoint(dir string, checkpoint temporalStructureOpenRouterCheckpoint, selected []TemporalStructureChallengePublicCase) error {
	identity := checkpoint.Identity
	if identity.SchemaVersion != temporalStructureOpenRouterCheckpointSchemaVersion || strings.TrimSpace(identity.ChallengeID) == "" || identity.ChallengeGeneratedAt.IsZero() || !reviewSHA256(identity.PublicManifestSHA256) || !reviewSHA256(identity.SelectionSHA256) || !reviewSHA256(identity.CapabilitySnapshotSHA256) || !reviewSHA256(identity.PromptSHA256) || identity.BaseURL == "" || identity.Model == "" || identity.ResolvedModel == "" || identity.ModelFamily == "" || identity.UpstreamProvider == "" || identity.UpstreamProviderSlug == "" || identity.PromptVersion != TemporalStructureOpenRouterPromptVersion || !validTemporalStructureOpenRouterReasoningMode(identity.ReasoningMode) || identity.AssessorID == "" || identity.ExpectedCases <= 0 || identity.MaxRequests != identity.ExpectedCases || identity.MaxSpendNanoUSD <= 0 || identity.ReservationNanoUSD <= 0 || identity.ReservationNanoUSD > identity.MaxSpendNanoUSD || identity.MaximumInputTokens <= 0 || identity.EstimatedMaximumChargeNanoUSD <= 0 || identity.EstimatedMaximumChargeNanoUSD > identity.ReservationNanoUSD {
		return fmt.Errorf("OpenRouter structure checkpoint identity is invalid")
	}
	if len(selected) != identity.ExpectedCases || len(checkpoint.Attempts) > identity.MaxRequests || len(checkpoint.Assessments) > identity.ExpectedCases {
		return fmt.Errorf("OpenRouter structure checkpoint counts exceed their identity")
	}
	countsBound := len(checkpoint.Attempts) == len(checkpoint.Assessments) || len(checkpoint.Attempts) == len(checkpoint.Assessments)+1 && checkpoint.Attempts[len(checkpoint.Attempts)-1].State == temporalOpenRouterAttemptReserved
	if !countsBound {
		return fmt.Errorf("OpenRouter structure checkpoint has an unbound settled attempt")
	}
	for index, attempt := range checkpoint.Attempts {
		if index >= len(selected) || attempt.Alias != selected[index].Alias || attempt.RequestedAt.IsZero() || !reviewSHA256(attempt.RequestSHA256) || attempt.LatencyMS < 0 || attempt.PromptTokens < 0 || attempt.CompletionTokens < 0 {
			return fmt.Errorf("OpenRouter structure checkpoint attempt %d is invalid", index)
		}
		if _, err := validateTemporalStructureAttemptAccounting(attempt, identity.ReservationNanoUSD); err != nil {
			return fmt.Errorf("OpenRouter structure checkpoint attempt %d has invalid settlement state", index)
		}
		if attempt.ResponseSHA256 != "" {
			if !reviewSHA256(attempt.ResponseSHA256) || attempt.RawResponsePath != filepath.ToSlash(filepath.Join(temporalStructureOpenRouterResponsesDir, attempt.Alias+".json")) {
				return fmt.Errorf("OpenRouter structure response binding is invalid")
			}
			responsePath := filepath.Join(dir, filepath.FromSlash(attempt.RawResponsePath))
			responseInfo, statErr := os.Lstat(responsePath)
			responseSHA, hashErr := hashFile(responsePath)
			if statErr != nil || !responseInfo.Mode().IsRegular() || responseInfo.Mode().Perm() != 0o600 || hashErr != nil || responseSHA != attempt.ResponseSHA256 {
				return fmt.Errorf("OpenRouter structure raw response binding failed")
			}
		}
	}
	if _, err := summarizeTemporalStructureAccounting(checkpoint.Attempts, identity.ReservationNanoUSD, identity.MaxSpendNanoUSD); err != nil {
		return fmt.Errorf("OpenRouter structure checkpoint accounting is invalid: %w", err)
	}
	for index, assessment := range checkpoint.Assessments {
		if assessment.Alias != selected[index].Alias {
			return fmt.Errorf("OpenRouter structure assessments are not an ordered selection prefix")
		}
		if err := validateTemporalStructureAssessment(assessment, selected[index].Video.DurationMS, identity.ChallengeGeneratedAt, assessment.Inference.AssessedAt); err != nil {
			return err
		}
		attempt := checkpoint.Attempts[index]
		call := assessment.Inference.Calls[0]
		if call.Axis != "structure" || call.ResponseSHA256 != attempt.ResponseSHA256 || call.LatencyMS != attempt.LatencyMS || call.PromptTokens != attempt.PromptTokens || call.CompletionTokens != attempt.CompletionTokens || call.OperationalFailure != attempt.OperationalFailure {
			return fmt.Errorf("OpenRouter structure assessment and attempt ledger drift")
		}
	}
	return nil
}

func temporalStructureOpenRouterCheckpointSpend(checkpoint temporalStructureOpenRouterCheckpoint) (int64, error) {
	summary, err := summarizeTemporalStructureAccounting(checkpoint.Attempts, checkpoint.Identity.ReservationNanoUSD, checkpoint.Identity.MaxSpendNanoUSD)
	if err != nil {
		return 0, fmt.Errorf("OpenRouter structure checkpoint exhausts its authorized spend: %w", err)
	}
	return summary.consumed, nil
}

func writeTemporalStructureOpenRouterRawResponse(dir, alias string, raw []byte) (string, error) {
	if len(raw) == 0 || strings.TrimSpace(alias) == "" {
		return "", fmt.Errorf("OpenRouter structure raw response is empty or unbound")
	}
	relative := filepath.ToSlash(filepath.Join(temporalStructureOpenRouterResponsesDir, alias+".json"))
	if err := writeTemporalTruthNew(filepath.Join(dir, filepath.FromSlash(relative)), raw, 0o600); err != nil {
		return "", err
	}
	return relative, nil
}
