package fillervisualsafety

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

// OpenCandidateBlindOpenRouterReview reopens every persisted model input,
// reservation, raw response, and result byte in one completed review.
func OpenCandidateBlindOpenRouterReview(root string) (CandidateBlindOpenRouterResult, error) {
	return openCandidateBlindOpenRouterReview(root, false)
}

func openCandidateBlindOpenRouterReview(root string, allowIncomplete bool) (CandidateBlindOpenRouterResult, error) {
	if err := validatePrivateReviewDirectory(root); err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	if err := validatePrivateReviewDirectory(filepath.Join(root, candidateBlindHostedInputDir)); err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	checkpoint, err := readReviewJSON[candidateBlindOpenRouterCheckpoint](filepath.Join(root, candidateBlindOpenRouterCheckpointName))
	if err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	result, err := readReviewJSON[CandidateBlindOpenRouterResult](filepath.Join(root, candidateBlindOpenRouterResultName))
	if err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	if err := validateCandidateBlindOpenRouterResult(result, checkpoint); err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	if err := validateCandidateBlindOpenRouterTopology(root, result, allowIncomplete); err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	if err := validateCandidateBlindOpenRouterAssets(root, result); err != nil {
		return CandidateBlindOpenRouterResult{}, err
	}
	return result, nil
}

func validateCandidateBlindOpenRouterResult(result CandidateBlindOpenRouterResult, checkpoint candidateBlindOpenRouterCheckpoint) error {
	if result.SchemaVersion != CandidateBlindOpenRouterSchemaVersion || result.ContractVersion != CandidateBlindOpenRouterContractVersion ||
		!validDigest(result.ReviewPackageSHA256) || !validDigest(result.OwnerMapSHA256) ||
		(result.SelectionOrigin != ReviewSelectionIndependentCorpus && result.SelectionOrigin != ReviewSelectionTargetedDiagnostic) ||
		!validDigest(result.CapabilitySnapshotSHA256) || !validIdentity(result.Model) || !validIdentity(result.ModelFamily) ||
		!validIdentity(result.ResolvedModel) || !validIdentity(result.UpstreamProvider) || !validIdentity(result.UpstreamProviderSlug) ||
		!validIdentity(result.ReviewerID) || result.PromptVersion != CandidateBlindOpenRouterPromptVersion ||
		result.PromptSHA256 != candidateBlindOpenRouterPromptSHA256() || !validDigest(result.SchemaSHA256) ||
		result.MaxRequests != 1 || result.MaxChargeNanoUSD <= 0 || validateCandidateBlindHostedInput(result.Input) != nil ||
		validateCandidateBlindAcceptedAttempt(result.Attempt, result.MaxChargeNanoUSD) != nil ||
		validateCandidateBlindAssessment(result.Assessment) != nil || result.ReviewedAt.IsZero() ||
		result.ReviewedAt.Location() != time.UTC || result.ReviewedAt.Before(result.Attempt.RequestedAt) ||
		result.TruthAuthorityCreated || result.TrainingAllowed || result.ProductionAdmissionAllowed ||
		result.SHA256 == "" || result.SHA256 != CandidateBlindOpenRouterResultSHA256(result) {
		return errors.New("candidate-blind OpenRouter review result is invalid")
	}
	if checkpoint.SchemaVersion != 1 || checkpoint.ReviewPackageSHA256 != result.ReviewPackageSHA256 ||
		checkpoint.OwnerMapSHA256 != result.OwnerMapSHA256 || checkpoint.SelectionOrigin != result.SelectionOrigin ||
		checkpoint.CapabilitySnapshotSHA256 != result.CapabilitySnapshotSHA256 || checkpoint.Model != result.Model ||
		checkpoint.ModelFamily != result.ModelFamily || checkpoint.ResolvedModel != result.ResolvedModel ||
		checkpoint.UpstreamProvider != result.UpstreamProvider || checkpoint.UpstreamProviderSlug != result.UpstreamProviderSlug ||
		checkpoint.ReviewerID != result.ReviewerID || checkpoint.PromptVersion != result.PromptVersion ||
		checkpoint.PromptSHA256 != result.PromptSHA256 || checkpoint.SchemaSHA256 != result.SchemaSHA256 ||
		checkpoint.ReasoningEnabled != result.ReasoningEnabled || checkpoint.MaxRequests != result.MaxRequests ||
		checkpoint.MaxChargeNanoUSD != result.MaxChargeNanoUSD || !reflect.DeepEqual(checkpoint.Input, result.Input) ||
		checkpoint.Attempt != result.Attempt {
		return errors.New("candidate-blind OpenRouter review checkpoint drifted")
	}
	return nil
}

func validateCandidateBlindHostedInput(input CandidateBlindHostedInput) error {
	if input.SchemaVersion != 1 || !validDigest(input.ReviewPackageSHA256) || !validDigest(input.CoverageEvidenceSHA256) ||
		!validDigest(input.PolicySHA256) || !validTool(input.FFmpeg) || !validDigest(input.CarrierRecipeSHA256) ||
		input.Carrier.RelativePath != filepath.ToSlash(filepath.Join(candidateBlindHostedInputDir, candidateBlindHostedCarrierName)) ||
		!validDigest(input.Carrier.SHA256) || input.Carrier.Bytes <= 0 || input.Carrier.Bytes > candidateBlindMaximumCarrierBytes ||
		len(input.ContactSheets) == 0 || len(input.ContactSheets) > 64 || input.SHA256 == "" ||
		input.SHA256 != CandidateBlindHostedInputSHA256(input) {
		return errors.New("candidate-blind OpenRouter hosted input is invalid")
	}
	total := input.Carrier.Bytes
	nextOrdinal := 0
	for index, sheet := range input.ContactSheets {
		wantPath := filepath.ToSlash(filepath.Join(candidateBlindHostedInputDir, fmt.Sprintf("contact-%03d.jpg", index)))
		count := sheet.LastOrdinal - sheet.FirstOrdinal + 1
		if sheet.RelativePath != wantPath || !validDigest(sheet.SHA256) || sheet.Bytes <= 0 ||
			sheet.Bytes > candidateBlindMaximumSheetBytes || sheet.FirstOrdinal != nextOrdinal ||
			count <= 0 || count > candidateBlindFramesPerSheet || sheet.LastObservedMS < sheet.FirstObservedMS ||
			sheet.Columns != candidateBlindSheetColumns || sheet.Rows != (count+candidateBlindSheetColumns-1)/candidateBlindSheetColumns {
			return errors.New("candidate-blind OpenRouter contact-sheet identity is invalid")
		}
		if total > candidateBlindMaximumInputBytes-sheet.Bytes {
			return errors.New("candidate-blind OpenRouter hosted input exceeds its byte ceiling")
		}
		total += sheet.Bytes
		nextOrdinal = sheet.LastOrdinal + 1
	}
	return nil
}

func validateCandidateBlindAcceptedAttempt(attempt CandidateBlindOpenRouterAttempt, maximum int64) error {
	charged, err := fillereval.USDToNanoCeil(attempt.ChargedAmountUSD)
	if attempt.RequestedAt.IsZero() || attempt.RequestedAt.Location() != time.UTC || !validDigest(attempt.RequestSHA256) ||
		!validDigest(attempt.ResponseSHA256) || attempt.RawResponsePath != candidateBlindOpenRouterRawName ||
		!validIdentity(attempt.GenerationID) || attempt.State != CandidateBlindAttemptAccepted || attempt.LatencyMS < 0 ||
		attempt.PromptTokens < 0 || attempt.CompletionTokens < 0 || attempt.ReasoningBytes < 0 ||
		err != nil || charged != attempt.ChargedNanoUSD || charged < 0 || charged > maximum ||
		attempt.ReservedNanoUSD != maximum || attempt.OperationalFailure != "" {
		return errors.New("candidate-blind OpenRouter accepted attempt is invalid")
	}
	return nil
}

func validateCandidateBlindAssessment(assessment CandidateBlindOpenRouterAssessment) error {
	if assessment.CoverageAssessment != CandidateBlindCoverageCompleted && assessment.CoverageAssessment != CandidateBlindCoverageInsufficient {
		return errors.New("candidate-blind OpenRouter assessment coverage is invalid")
	}
	for index, match := range assessment.Matches {
		if !validIdentity(match.PolicyMatchID) || match.StartMS < 0 || match.StartMS >= match.EndMS ||
			(match.Certainty != CandidateBlindCertaintyObserved && match.Certainty != CandidateBlindCertaintyUncertain) ||
			index > 0 && compareCandidateBlindMatches(assessment.Matches[index-1], match) >= 0 {
			return errors.New("candidate-blind OpenRouter assessment match is invalid")
		}
	}
	want := CandidateBlindOutcomeNoSignal
	if len(assessment.Matches) > 0 {
		want = CandidateBlindOutcomeProhibitedSignal
	} else if assessment.CoverageAssessment == CandidateBlindCoverageInsufficient {
		want = CandidateBlindOutcomeCoverageHold
	}
	if assessment.Outcome != want {
		return errors.New("candidate-blind OpenRouter assessment outcome is invalid")
	}
	return nil
}

func validateCandidateBlindOpenRouterTopology(root string, result CandidateBlindOpenRouterResult, allowIncomplete bool) error {
	expected := map[string]bool{
		".": true, candidateBlindHostedInputDir: true,
		candidateBlindOpenRouterCheckpointName: false, candidateBlindOpenRouterRawName: false,
		candidateBlindOpenRouterResultName: false,
	}
	expected[filepath.FromSlash(result.Input.Carrier.RelativePath)] = false
	for _, sheet := range result.Input.ContactSheets {
		expected[filepath.FromSlash(sheet.RelativePath)] = false
	}
	if allowIncomplete {
		expected[reviewIncompleteName] = false
	}
	seen := make(map[string]struct{}, len(expected))
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		wantDirectory, exists := expected[relative]
		if !exists || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() != wantDirectory {
			return errors.New("candidate-blind OpenRouter review contains unexpected evidence")
		}
		seen[relative] = struct{}{}
		return nil
	})
	if err != nil || len(seen) != len(expected) {
		return errors.New("candidate-blind OpenRouter review topology is invalid")
	}
	return nil
}

func validateCandidateBlindOpenRouterAssets(root string, result CandidateBlindOpenRouterResult) error {
	for _, asset := range append([]CandidateBlindReviewAsset{result.Input.Carrier}, candidateBlindSheetAssets(result.Input.ContactSheets)...) {
		maximum := candidateBlindMaximumSheetBytes
		if asset.RelativePath == result.Input.Carrier.RelativePath {
			maximum = candidateBlindMaximumCarrierBytes
		}
		sha, size, err := hashReviewFile(filepath.Join(root, filepath.FromSlash(asset.RelativePath)), maximum)
		if err != nil || sha != asset.SHA256 || size != asset.Bytes {
			return errors.New("candidate-blind OpenRouter review input asset is invalid")
		}
	}
	sha, _, err := hashReviewFile(filepath.Join(root, candidateBlindOpenRouterRawName), maximumReviewManifestBytes)
	if err != nil || sha != result.Attempt.ResponseSHA256 {
		return errors.New("candidate-blind OpenRouter raw response is invalid")
	}
	return nil
}

func candidateBlindSheetAssets(sheets []CandidateBlindContactSheet) []CandidateBlindReviewAsset {
	assets := make([]CandidateBlindReviewAsset, 0, len(sheets))
	for _, sheet := range sheets {
		assets = append(assets, sheet.CandidateBlindReviewAsset)
	}
	return assets
}

func reviewBytesSHA256(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
