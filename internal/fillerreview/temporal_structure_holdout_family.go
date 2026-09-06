package fillerreview

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerreference"
)

const (
	temporalStructureHoldoutFamilyAuditSchemaVersion = 3
	temporalStructureHoldoutReferenceCases           = 300
)

const temporalStructureHoldoutLegacyReferenceContract = "filler-reference-cohort-2026-08-31-v1"

type temporalStructureHoldoutFamilyAudit = fillerreference.FamilyAudit
type temporalStructureHoldoutFingerprint = fillerreference.FamilyFingerprint
type temporalStructureHoldoutDuplicateFamily = fillerreference.DuplicateFamily

func loadTemporalStructureHoldoutReferenceAudit(path string, plannedAt time.Time) (fillerreference.Audit, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fillerreference.Audit{}, "", err
	}
	audit, err := readStrictJSON[fillerreference.Audit](path)
	if err != nil {
		return fillerreference.Audit{}, "", err
	}
	currentIdentity := audit.SchemaVersion == fillerreference.AuditSchemaVersion && audit.Contract == fillerreference.ContractVersion
	legacyIdentity := audit.SchemaVersion == 2 && audit.Contract == temporalStructureHoldoutLegacyReferenceContract
	validInputs := reviewSHA256(audit.Inputs.ManifestSHA256) && reviewSHA256(audit.Inputs.PacketsSHA256) && reviewSHA256(audit.Inputs.MappingSHA256) && reviewSHA256(audit.Inputs.DownloadLedgerSHA256)
	if currentIdentity {
		validInputs = validInputs && reviewSHA256(audit.Inputs.ContentReviewSHA256)
	} else if legacyIdentity {
		validInputs = validInputs && audit.Inputs.ContentReviewSHA256 == ""
	}
	if (!currentIdentity && !legacyIdentity) || !validInputs || audit.GeneratedAt.IsZero() || plannedAt.Before(audit.GeneratedAt) || strings.TrimSpace(audit.Summary.Mapping) == "" || audit.Summary.Contract != audit.Contract || audit.Summary.Cases != temporalStructureHoldoutReferenceCases || audit.Summary.Cases != len(audit.Cases) || audit.Summary.Candidates+audit.Summary.Holds+audit.Summary.Excluded != len(audit.Cases) {
		return fillerreference.Audit{}, "", fmt.Errorf("temporal structure holdout reference audit is invalid")
	}
	counts := map[fillerreference.Disposition]int{}
	seen := make(map[string]struct{}, len(audit.Cases))
	for _, item := range audit.Cases {
		if strings.TrimSpace(item.CaseID) == "" || !reviewSHA256(item.ContentSHA256) || strings.TrimSpace(item.SourceLocalFile) == "" {
			return fillerreference.Audit{}, "", fmt.Errorf("temporal structure holdout reference audit contains an invalid case")
		}
		if _, duplicate := seen[item.CaseID]; duplicate {
			return fillerreference.Audit{}, "", fmt.Errorf("temporal structure holdout reference audit repeats a case")
		}
		switch item.Disposition {
		case fillerreference.DispositionCandidate, fillerreference.DispositionHold, fillerreference.DispositionExclude:
		default:
			return fillerreference.Audit{}, "", fmt.Errorf("temporal structure holdout reference audit contains an invalid disposition")
		}
		seen[item.CaseID] = struct{}{}
		counts[item.Disposition]++
	}
	if counts[fillerreference.DispositionCandidate] != audit.Summary.Candidates || counts[fillerreference.DispositionHold] != audit.Summary.Holds || counts[fillerreference.DispositionExclude] != audit.Summary.Excluded {
		return fillerreference.Audit{}, "", fmt.Errorf("temporal structure holdout reference audit summary drift")
	}
	return audit, hashBytes(raw), nil
}

func loadTemporalStructureHoldoutFamily(path string, selection fillereval.TemporalTruthSelection, reference fillerreference.Audit, referenceSHA string, plannedAt time.Time) (temporalStructureHoldoutFamilyAudit, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return temporalStructureHoldoutFamilyAudit{}, "", err
	}
	audit, err := readStrictJSON[temporalStructureHoldoutFamilyAudit](path)
	if err != nil {
		return temporalStructureHoldoutFamilyAudit{}, "", err
	}
	if audit.SchemaVersion != temporalStructureHoldoutFamilyAuditSchemaVersion || audit.Algorithm != fillerreference.DuplicateAlgorithm || audit.GeneratedAt.IsZero() || audit.GeneratedAt.Before(reference.GeneratedAt) || plannedAt.Before(audit.GeneratedAt) || audit.SourceAudit != referenceSHA || audit.Summary.Cases != len(audit.Fingerprints) || audit.Summary.RelatedPairs != len(audit.Pairs) || audit.Summary.ClosestNonMatches != len(audit.ClosestPairs) || audit.Summary.DuplicateFamilies != len(audit.Families) {
		return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout family authority is invalid")
	}
	referenceCases := make(map[string]fillerreference.Case, len(reference.Cases))
	for _, item := range reference.Cases {
		referenceCases[item.CaseID] = item
	}
	seen := make(map[string]struct{}, len(audit.Fingerprints))
	seenContent := make(map[string]struct{}, len(audit.Fingerprints))
	seenFiles := make(map[string]struct{}, len(audit.Fingerprints))
	for _, item := range audit.Fingerprints {
		if strings.TrimSpace(item.CaseID) == "" || !reviewSHA256(item.ContentSHA256) || strings.TrimSpace(item.LocalFile) == "" || len(item.FrameHashes) == 0 || len(item.AudioRMS) == 0 {
			return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout family fingerprint is invalid")
		}
		expected, exists := referenceCases[item.CaseID]
		if !exists || expected.Disposition == fillerreference.DispositionExclude || expected.ContentSHA256 != item.ContentSHA256 || expected.SourceLocalFile != item.LocalFile {
			return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout family content drift for %q", item.CaseID)
		}
		if _, duplicate := seen[item.CaseID]; duplicate {
			return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout family repeats a case")
		}
		if _, duplicate := seenContent[item.ContentSHA256]; duplicate {
			return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout family repeats source bytes")
		}
		if _, duplicate := seenFiles[item.LocalFile]; duplicate {
			return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout family repeats a source file")
		}
		seen[item.CaseID] = struct{}{}
		seenContent[item.ContentSHA256] = struct{}{}
		seenFiles[item.LocalFile] = struct{}{}
	}
	if len(seen) != reference.Summary.Candidates+reference.Summary.Holds {
		return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout family authority does not cover the reference audit")
	}
	for _, item := range reference.Cases {
		_, fingerprinted := seen[item.CaseID]
		if (item.Disposition == fillerreference.DispositionExclude) == fingerprinted {
			return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout family authority does not match reference dispositions")
		}
	}
	for _, item := range selection.Cases {
		expected, exists := referenceCases[item.CaseID]
		if !exists || expected.ContentSHA256 != item.ContentSHA256 {
			return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout selection does not match the reference audit")
		}
	}
	memberFamily, seenFamilyIDs := map[string]string{}, map[string]struct{}{}
	nonCliqueFamilies := 0
	for _, family := range audit.Families {
		if strings.TrimSpace(family.FamilyID) == "" || len(family.Members) < 2 {
			return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout duplicate family is invalid")
		}
		if _, duplicate := seenFamilyIDs[family.FamilyID]; duplicate {
			return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout repeats a duplicate family id")
		}
		seenFamilyIDs[family.FamilyID] = struct{}{}
		if !family.CompleteClique {
			nonCliqueFamilies++
		}
		preferredSeen := family.PreferredCase == ""
		for _, member := range family.Members {
			if _, exists := seen[member]; !exists || memberFamily[member] != "" {
				return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout duplicate family membership is invalid")
			}
			memberFamily[member] = family.FamilyID
			preferredSeen = preferredSeen || member == family.PreferredCase
		}
		if !preferredSeen {
			return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout duplicate family preferred case is not a member")
		}
	}
	if nonCliqueFamilies != audit.Summary.NonCliqueFamilies {
		return temporalStructureHoldoutFamilyAudit{}, "", fmt.Errorf("temporal structure holdout duplicate family summary drift")
	}
	if err := validateTemporalStructureHoldoutFamilyGraph(reference, audit); err != nil {
		return temporalStructureHoldoutFamilyAudit{}, "", err
	}
	return audit, hashBytes(raw), nil
}

func validateTemporalStructureHoldoutFamilyGraph(reference fillerreference.Audit, audit temporalStructureHoldoutFamilyAudit) error {
	// The historical audit predates the current content-review input. Normalize
	// only its schema identity so the canonical family builder can independently
	// recompute the pair graph; the supplied audit bytes remain the receipt authority.
	normalized := reference
	normalized.SchemaVersion = fillerreference.AuditSchemaVersion
	normalized.Contract = fillerreference.ContractVersion
	if normalized.Inputs.ContentReviewSHA256 == "" {
		normalized.Inputs.ContentReviewSHA256 = hashBytes([]byte("legacy-reference-content-review-not-present"))
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("normalize temporal structure holdout reference audit: %w", err)
	}
	expected, err := fillerreference.BuildFamilyAudit(raw, audit.Fingerprints, audit.GeneratedAt)
	if err != nil {
		return fmt.Errorf("recompute temporal structure holdout family authority: %w", err)
	}
	if !reflect.DeepEqual(audit.Fingerprints, expected.Fingerprints) ||
		!reflect.DeepEqual(audit.Pairs, expected.Pairs) ||
		!reflect.DeepEqual(audit.ClosestPairs, expected.ClosestPairs) ||
		!reflect.DeepEqual(audit.Families, expected.Families) ||
		audit.Summary != expected.Summary {
		return fmt.Errorf("temporal structure holdout family authority does not match the canonical duplicate graph")
	}
	return nil
}
