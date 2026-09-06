package fillerreview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	TemporalStructureOpenRouterResultSchemaVersion = 4
	TemporalStructureOpenRouterResultContract      = "filler-temporal-structure-openrouter-v4"
	TemporalStructureOpenRouterMaximumVideoBytes   = int64(64 << 20)
	TemporalStructureOpenRouterReasoningDisabled   = "disabled"
	TemporalStructureOpenRouterReasoningRequired   = "provider_required"
)

type TemporalStructureOpenRouterConfig struct {
	PublicManifestPath   string
	CaseAliases          []string
	CheckpointDir        string
	BaseURL              string
	APIKey               string
	Snapshot             fillerbakeoff.OpenRouterSnapshot
	Model                string
	ModelFamily          string
	UpstreamProvider     string
	UpstreamProviderSlug string
	AssessorID           string
	ReasoningMode        string
	ExpectedCases        int
	PerCaseTimeout       time.Duration
	MaxRequests          int
	MaxSpendNanoUSD      int64
	ReservationNanoUSD   int64
	MaximumInputTokens   int64
	AllowInsecureTestURL bool
	Client               *http.Client
	Now                  func() time.Time
}

type TemporalStructureOpenRouterResult struct {
	SchemaVersion                 int                                  `json:"schemaVersion"`
	ContractVersion               string                               `json:"contractVersion"`
	ChallengeID                   string                               `json:"challengeId"`
	PublicManifestSHA256          string                               `json:"publicManifestSha256"`
	SelectionSHA256               string                               `json:"selectionSha256"`
	CapabilitySnapshotSHA256      string                               `json:"capabilitySnapshotSha256"`
	ResolvedModel                 string                               `json:"resolvedModel"`
	UpstreamProvider              string                               `json:"upstreamProvider"`
	UpstreamProviderSlug          string                               `json:"upstreamProviderSlug"`
	ReasoningMode                 string                               `json:"reasoningMode"`
	PromptSHA256                  string                               `json:"promptSha256"`
	Assessor                      fillereval.TemporalAssessorIdentity  `json:"assessor"`
	SelectionAliases              []string                             `json:"selectionAliases"`
	MaxRequests                   int                                  `json:"maxRequests"`
	MaxSpendNanoUSD               int64                                `json:"maxSpendNanoUsd"`
	ReservationNanoUSD            int64                                `json:"reservationNanoUsd"`
	MaximumInputTokens            int64                                `json:"maximumInputTokens"`
	EstimatedMaximumChargeNanoUSD int64                                `json:"estimatedMaximumChargeNanoUsd"`
	Requests                      int                                  `json:"requests"`
	ChargedNanoUSD                int64                                `json:"chargedNanoUsd"`
	ConsumedNanoUSD               int64                                `json:"consumedNanoUsd"`
	UnknownChargeReservations     int                                  `json:"unknownChargeReservations"`
	OverReservationNanoUSD        int64                                `json:"overReservationNanoUsd"`
	CompletedAt                   time.Time                            `json:"completedAt"`
	ProductionAdmissionAllowed    bool                                 `json:"productionAdmissionAllowed"`
	Assessments                   []TemporalStructureAssessment        `json:"assessments"`
	Attempts                      []TemporalStructureOpenRouterAttempt `json:"attempts"`
}

// RunOpenRouterTemporalStructure performs one serial, full-video atomic
// structure assessment per selected case. It opens only the public challenge;
// private construction truth enters later at the lock boundary.
func RunOpenRouterTemporalStructure(ctx context.Context, config TemporalStructureOpenRouterConfig) (result TemporalStructureOpenRouterResult, err error) {
	baseURL, client, now, err := validateTemporalStructureOpenRouterConfig(config)
	if err != nil {
		return TemporalStructureOpenRouterResult{}, err
	}
	publicHeader, err := readStrictJSON[TemporalStructureChallengeManifest](config.PublicManifestPath)
	if err != nil {
		return TemporalStructureOpenRouterResult{}, fmt.Errorf("read public structure challenge: %w", err)
	}
	manifest, manifestSHA, err := LoadTemporalStructureChallengePublic(config.PublicManifestPath, len(publicHeader.Cases))
	if err != nil {
		return TemporalStructureOpenRouterResult{}, err
	}
	if now().UTC().Before(manifest.GeneratedAt) {
		return TemporalStructureOpenRouterResult{}, fmt.Errorf("OpenRouter structure assessment clock predates the challenge")
	}
	selected, aliases, selectionSHA, err := selectTemporalStructureOpenRouterCases(manifest, config.CaseAliases, config.ExpectedCases)
	if err != nil {
		return TemporalStructureOpenRouterResult{}, err
	}
	identity := buildTemporalStructureOpenRouterCheckpointIdentity(config, manifest, manifestSHA, selectionSHA, baseURL)
	activeLock, err := acquireOpenRouterActiveRunLock(config.CheckpointDir, identity, now, nil)
	if err != nil {
		return TemporalStructureOpenRouterResult{}, err
	}
	defer func() {
		if releaseErr := activeLock.release(); releaseErr != nil {
			result = TemporalStructureOpenRouterResult{}
			err = errors.Join(err, releaseErr)
		}
	}()
	checkpoint, err := loadTemporalStructureOpenRouterCheckpoint(config.CheckpointDir, identity, selected)
	if err != nil {
		return TemporalStructureOpenRouterResult{}, err
	}
	if len(checkpoint.Attempts) > len(checkpoint.Assessments) {
		return TemporalStructureOpenRouterResult{}, fmt.Errorf("OpenRouter structure checkpoint has an unsettled crash reservation for alias %q", checkpoint.Attempts[len(checkpoint.Attempts)-1].Alias)
	}
	if len(checkpoint.Attempts) > 0 && checkpoint.Attempts[len(checkpoint.Attempts)-1].State == temporalOpenRouterAttemptOverReservation {
		return TemporalStructureOpenRouterResult{}, fmt.Errorf("OpenRouter structure checkpoint ended with an over-reservation charge")
	}
	root := filepath.Dir(config.PublicManifestPath)
	for index := len(checkpoint.Assessments); index < len(selected); index++ {
		assessment, assessErr := assessOpenRouterTemporalStructureCase(ctx, client, baseURL, root, config, selected[index], &checkpoint, selected, now)
		if assessErr != nil {
			return TemporalStructureOpenRouterResult{}, assessErr
		}
		checkpoint.Assessments = append(checkpoint.Assessments, assessment)
		if err := persistTemporalStructureOpenRouterCheckpoint(config.CheckpointDir, checkpoint, selected); err != nil {
			return TemporalStructureOpenRouterResult{}, fmt.Errorf("persist completed OpenRouter structure case: %w", err)
		}
	}
	consumed, err := temporalStructureOpenRouterCheckpointSpend(checkpoint)
	if err != nil {
		return TemporalStructureOpenRouterResult{}, err
	}
	model := openRouterTemporalModel(config.Snapshot, config.Model)
	result = TemporalStructureOpenRouterResult{
		SchemaVersion: TemporalStructureOpenRouterResultSchemaVersion, ContractVersion: TemporalStructureOpenRouterResultContract,
		ChallengeID: manifest.ChallengeID, PublicManifestSHA256: manifestSHA, SelectionSHA256: selectionSHA,
		CapabilitySnapshotSHA256: identity.CapabilitySnapshotSHA256, ResolvedModel: model.CanonicalSlug,
		UpstreamProvider: config.UpstreamProvider, UpstreamProviderSlug: config.UpstreamProviderSlug,
		ReasoningMode: config.ReasoningMode, PromptSHA256: identity.PromptSHA256,
		Assessor: fillereval.TemporalAssessorIdentity{
			ID: config.AssessorID, Provider: "openrouter", Model: config.Model, ModelFamily: config.ModelFamily,
			ModelDigest: identity.CapabilitySnapshotSHA256, PromptVersion: TemporalStructureOpenRouterPromptVersion,
		},
		SelectionAliases: aliases, MaxRequests: config.MaxRequests, MaxSpendNanoUSD: config.MaxSpendNanoUSD,
		ReservationNanoUSD: config.ReservationNanoUSD, MaximumInputTokens: config.MaximumInputTokens,
		EstimatedMaximumChargeNanoUSD: identity.EstimatedMaximumChargeNanoUSD,
		Requests:                      len(checkpoint.Attempts), ConsumedNanoUSD: consumed,
		CompletedAt: now().UTC(), ProductionAdmissionAllowed: false,
		Assessments: slices.Clone(checkpoint.Assessments), Attempts: slices.Clone(checkpoint.Attempts),
	}
	for _, attempt := range checkpoint.Attempts {
		result.ChargedNanoUSD += attempt.ChargedNanoUSD
		if attempt.State == temporalOpenRouterAttemptUnsettled {
			result.UnknownChargeReservations++
		}
		if attempt.State == temporalOpenRouterAttemptOverReservation {
			result.OverReservationNanoUSD += attempt.ChargedNanoUSD - attempt.ReservedNanoUSD
		}
	}
	if err := validateTemporalStructureOpenRouterResult(result, manifest, selected); err != nil {
		return TemporalStructureOpenRouterResult{}, err
	}
	return result, nil
}

func selectTemporalStructureOpenRouterCases(manifest TemporalStructureChallengeManifest, requested []string, expected int) ([]TemporalStructureChallengePublicCase, []string, string, error) {
	byAlias := make(map[string]TemporalStructureChallengePublicCase, len(manifest.Cases))
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
		return nil, nil, "", fmt.Errorf("structure selection has %d unique cases; want exactly %d", len(aliases), expected)
	}
	selected := make([]TemporalStructureChallengePublicCase, 0, len(aliases))
	for _, alias := range aliases {
		item, exists := byAlias[alias]
		if !exists {
			return nil, nil, "", fmt.Errorf("structure selection names unknown public alias %q", alias)
		}
		selected = append(selected, item)
	}
	return selected, aliases, temporalTruthJSONSHA(aliases), nil
}
