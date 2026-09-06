package fillerreview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
)

type TemporalStructureAssessmentLockConfig struct {
	PublicManifestPath   string
	PrivateAuthorityPath string
	ResultPath           string
	SnapshotPath         string
	ExpectedCases        int
	LockedAt             time.Time
	OutputPath           string
}

type TemporalStructureAssessmentLockResult struct {
	Assessments        int
	AssessmentSHA256   string
	RawResultSHA256    string
	SnapshotFileSHA256 string
}

// LockTemporalStructureAssessment joins a complete public-only paid result to
// private construction authority after inference. The paid runner never opens
// truth; the lock verifies raw responses and the exact capability snapshot.
func LockTemporalStructureAssessment(config TemporalStructureAssessmentLockConfig) (TemporalStructureAssessmentLockResult, error) {
	if strings.TrimSpace(config.PublicManifestPath) == "" || strings.TrimSpace(config.PrivateAuthorityPath) == "" || strings.TrimSpace(config.ResultPath) == "" || strings.TrimSpace(config.SnapshotPath) == "" || config.ExpectedCases <= 0 || config.LockedAt.IsZero() || strings.TrimSpace(config.OutputPath) == "" {
		return TemporalStructureAssessmentLockResult{}, fmt.Errorf("temporal structure lock requires challenge authority, result, snapshot, exact case count, lock time, and output")
	}
	manifest, _, publicSHA, authoritySHA, err := LoadTemporalStructureChallenge(config.PublicManifestPath, config.PrivateAuthorityPath, config.ExpectedCases)
	if err != nil {
		return TemporalStructureAssessmentLockResult{}, err
	}
	resultRaw, err := os.ReadFile(config.ResultPath)
	if err != nil {
		return TemporalStructureAssessmentLockResult{}, fmt.Errorf("read temporal structure result: %w", err)
	}
	result, err := readStrictJSON[TemporalStructureOpenRouterResult](config.ResultPath)
	if err != nil {
		return TemporalStructureAssessmentLockResult{}, fmt.Errorf("decode temporal structure result: %w", err)
	}
	selected, aliases, selectionSHA, err := selectTemporalStructureOpenRouterCases(manifest, result.SelectionAliases, config.ExpectedCases)
	if err != nil {
		return TemporalStructureAssessmentLockResult{}, err
	}
	if result.PublicManifestSHA256 != publicSHA || result.SelectionSHA256 != selectionSHA || !slices.Equal(result.SelectionAliases, aliases) || config.LockedAt.Before(result.CompletedAt) {
		return TemporalStructureAssessmentLockResult{}, fmt.Errorf("temporal structure result does not bind the challenge selection or predates its lock")
	}
	if err := validateTemporalStructureOpenRouterResult(result, manifest, selected); err != nil {
		return TemporalStructureAssessmentLockResult{}, err
	}
	if err := verifyTemporalStructureOpenRouterRawResponses(config.ResultPath, result); err != nil {
		return TemporalStructureAssessmentLockResult{}, err
	}

	snapshotRaw, err := os.ReadFile(config.SnapshotPath)
	if err != nil {
		return TemporalStructureAssessmentLockResult{}, fmt.Errorf("read temporal structure snapshot: %w", err)
	}
	var snapshot fillerbakeoff.OpenRouterSnapshot
	if err := decodeStrictReviewJSON(snapshotRaw, &snapshot); err != nil {
		return TemporalStructureAssessmentLockResult{}, fmt.Errorf("decode temporal structure snapshot: %w", err)
	}
	if err := fillerbakeoff.ValidateOpenRouterSnapshot(snapshot); err != nil {
		return TemporalStructureAssessmentLockResult{}, err
	}
	capabilitySHA := fillerbakeoff.OpenRouterSnapshotSHA256(snapshot)
	if capabilitySHA != result.CapabilitySnapshotSHA256 {
		return TemporalStructureAssessmentLockResult{}, fmt.Errorf("temporal structure snapshot does not bind the paid result")
	}
	model := openRouterTemporalModel(snapshot, result.Assessor.Model)
	if model.CanonicalSlug != result.ResolvedModel {
		return TemporalStructureAssessmentLockResult{}, fmt.Errorf("temporal structure snapshot does not bind the resolved model")
	}
	if err := validateTemporalStructureOpenRouterSnapshot(TemporalStructureOpenRouterConfig{
		Snapshot: snapshot, Model: result.Assessor.Model,
		UpstreamProvider: result.UpstreamProvider, UpstreamProviderSlug: result.UpstreamProviderSlug,
	}, snapshot.SourceBaseURL, result.CompletedAt); err != nil {
		return TemporalStructureAssessmentLockResult{}, fmt.Errorf("validate temporal structure model route at completion: %w", err)
	}
	estimatedMaximumCharge, err := estimateTemporalStructureOpenRouterCharge(TemporalStructureOpenRouterConfig{
		Snapshot: snapshot, Model: result.Assessor.Model, MaximumInputTokens: result.MaximumInputTokens,
		UpstreamProvider: result.UpstreamProvider, UpstreamProviderSlug: result.UpstreamProviderSlug,
	})
	if err != nil || estimatedMaximumCharge != result.EstimatedMaximumChargeNanoUSD {
		return TemporalStructureAssessmentLockResult{}, fmt.Errorf("temporal structure result price bound does not reproduce from the snapshot")
	}

	rawResultSHA := hashBytes(resultRaw)
	snapshotFileSHA := hashBytes(snapshotRaw)
	set := TemporalStructureAssessmentSet{
		SchemaVersion: TemporalStructureAssessmentSchemaVersion, ContractVersion: TemporalStructureAssessmentContractVersion,
		ChallengeID: manifest.ChallengeID, PublicManifestSHA256: publicSHA, PrivateAuthoritySHA256: authoritySHA,
		RawResultSHA256: rawResultSHA, SnapshotFileSHA256: snapshotFileSHA, CapabilitySnapshotSHA256: capabilitySHA,
		CompletedAt: result.CompletedAt, LockedAt: config.LockedAt.UTC(), Assessor: result.Assessor,
		Assessments: slices.Clone(result.Assessments), ProductionAdmissionAllowed: false,
	}
	durations := make(map[string]int64, len(manifest.Cases))
	for _, item := range manifest.Cases {
		durations[item.Alias] = item.Video.DurationMS
	}
	if _, err := validateTemporalStructureAssessmentSet(set, manifest, publicSHA, authoritySHA, durations); err != nil {
		return TemporalStructureAssessmentLockResult{}, err
	}
	setRaw, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return TemporalStructureAssessmentLockResult{}, err
	}
	setRaw = append(setRaw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, setRaw, 0o600); err != nil {
		return TemporalStructureAssessmentLockResult{}, fmt.Errorf("publish temporal structure assessment lock: %w", err)
	}
	return TemporalStructureAssessmentLockResult{
		Assessments: len(set.Assessments), AssessmentSHA256: hashBytes(setRaw),
		RawResultSHA256: rawResultSHA, SnapshotFileSHA256: snapshotFileSHA,
	}, nil
}

func verifyTemporalStructureOpenRouterRawResponses(resultPath string, result TemporalStructureOpenRouterResult) error {
	root := resultPath + ".private"
	for index, attempt := range result.Attempts {
		if attempt.State != temporalOpenRouterAttemptAccepted && attempt.State != temporalOpenRouterAttemptFailed && attempt.State != temporalOpenRouterAttemptUnsettled && attempt.State != temporalOpenRouterAttemptOverReservation {
			return fmt.Errorf("temporal structure attempt %d is not terminal", index)
		}
		if attempt.ResponseSHA256 == "" {
			if attempt.State != temporalOpenRouterAttemptUnsettled || attempt.RawResponsePath != "" {
				return fmt.Errorf("temporal structure attempt %d lacks a valid raw-response disposition", index)
			}
			continue
		}
		if !reviewSHA256(attempt.ResponseSHA256) || attempt.RawResponsePath != filepath.ToSlash(filepath.Join(temporalStructureOpenRouterResponsesDir, attempt.Alias+".json")) {
			return fmt.Errorf("temporal structure attempt %d has invalid raw-response authority", index)
		}
		path := filepath.Join(root, filepath.FromSlash(attempt.RawResponsePath))
		info, statErr := os.Lstat(path)
		digest, hashErr := hashFile(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || hashErr != nil || digest != attempt.ResponseSHA256 {
			return fmt.Errorf("temporal structure attempt %d raw-response binding failed", index)
		}
	}
	return nil
}
