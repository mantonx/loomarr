package fillervisualsafety

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

func openVisualCorpusNominationSet(root string, allowIncomplete bool) (VisualCorpusNominationSet, error) {
	if err := validatePrivateReviewDirectory(root); err != nil {
		return VisualCorpusNominationSet{}, err
	}
	for _, directory := range []string{visualCorpusNominationAssetsDirectory, visualCorpusNominationRightsDirectory} {
		if err := validatePrivateReviewDirectory(filepath.Join(root, directory)); err != nil {
			return VisualCorpusNominationSet{}, err
		}
	}
	set, err := readReviewJSON[VisualCorpusNominationSet](filepath.Join(root, visualCorpusNominationSetFilename))
	if err != nil || validateVisualCorpusNominationSet(set) != nil {
		return VisualCorpusNominationSet{}, errors.New("visual corpus nomination set is invalid")
	}
	expected := map[string]bool{
		".": true, visualCorpusNominationAssetsDirectory: true, visualCorpusNominationRightsDirectory: true,
		visualCorpusNominationSetFilename: false,
	}
	if allowIncomplete {
		expected[visualCorpusNominationIncompleteName] = false
	}
	for _, candidate := range set.Candidates {
		expected[candidate.AssetRelativePath] = false
		expected[candidate.RightsRelativePath] = false
	}
	seen := make(map[string]struct{}, len(expected))
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, relErr := filepath.Rel(root, current)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		wantDirectory, ok := expected[relative]
		if !ok || wantDirectory != entry.IsDir() {
			return errors.New("visual corpus nomination set contains unexpected evidence")
		}
		seen[relative] = struct{}{}
		return nil
	})
	if err != nil || len(seen) != len(expected) {
		return VisualCorpusNominationSet{}, errors.New("visual corpus nomination set topology is invalid")
	}
	for _, candidate := range set.Candidates {
		assetRaw, readErr := readVisualCorpusInput(root, candidate.AssetRelativePath, candidate.Asset)
		if readErr != nil {
			return VisualCorpusNominationSet{}, errors.New("visual corpus nomination asset is invalid")
		}
		if _, _, _, _, inspectErr := inspectVisualCorpusImage(assetRaw); inspectErr != nil {
			return VisualCorpusNominationSet{}, errors.New("visual corpus nomination asset is malformed")
		}
		rightsRaw, readErr := readVisualCorpusInput(root, candidate.RightsRelativePath, candidate.RightsEvidence)
		if readErr != nil {
			return VisualCorpusNominationSet{}, errors.New("visual corpus nomination rights evidence is invalid")
		}
		evidence, decodeErr := decodeVisualCorpusRightsEvidence(rightsRaw)
		if decodeErr != nil || evidence.InventorySHA256 != set.InventorySHA256 ||
			evidence.MaterializationSHA256 != set.MaterializationSHA256 || evidence.ReviewedBy != set.ReviewedBy ||
			!evidence.ReviewedAt.Equal(set.LockedAt) || validateVisualCorpusRightsEvidence(candidate, evidence, set.LockedAt) != nil {
			return VisualCorpusNominationSet{}, errors.New("visual corpus nomination rights evidence drifted")
		}
	}
	return set, nil
}

func validateVisualCorpusNominationSet(set VisualCorpusNominationSet) error {
	if set.SchemaVersion != VisualCorpusNominationSetSchemaVersion || set.ContractVersion != VisualCorpusNominationSetContractVersion ||
		!validDigest(set.WorksheetSHA256) || !validDigest(set.ReviewDecisionsSHA256) ||
		!validDigest(set.InventorySHA256) || !validDigest(set.MaterializationSHA256) ||
		set.LockedAt.IsZero() || set.LockedAt.Location() != time.UTC || !validIdentity(set.ReviewedBy) ||
		set.ReviewedCaseCount <= 0 || set.ReviewedCaseCount > MaximumVisualCorpusDraftCases || set.ExcludedCaseCount < 0 ||
		set.ReviewedCaseCount != len(set.Candidates)+set.ExcludedCaseCount ||
		len(set.Candidates) == 0 || len(set.Candidates) > MaximumVisualCorpusDraftCases || set.CandidateModelOutput ||
		set.TruthAuthorityCreated || set.TrainingAllowed || set.ProductionUseAllowed ||
		set.SHA256 == "" || set.SHA256 != VisualCorpusNominationSetSHA256(set) {
		return errors.New("visual corpus nomination set contract is invalid")
	}
	seenCandidate := make(map[string]struct{}, len(set.Candidates))
	seenWork := make(map[string]struct{}, len(set.Candidates))
	seenFamily := make(map[string]struct{}, len(set.Candidates))
	seenIndependence := make(map[string]struct{}, len(set.Candidates))
	seenAsset := make(map[string]struct{}, len(set.Candidates))
	seenRights := make(map[string]struct{}, len(set.Candidates))
	seenPositiveCreator := make(map[string]struct{}, len(set.Candidates))
	previous := ""
	for _, candidate := range set.Candidates {
		if previous != "" && strings.Compare(previous, candidate.CandidateID) >= 0 {
			return errors.New("visual corpus nomination candidates are not canonical")
		}
		previous = candidate.CandidateID
		if validateVisualCorpusDraftCandidate(candidate) != nil || duplicateIdentity(seenCandidate, candidate.CandidateID) ||
			duplicateIdentity(seenWork, candidate.SourceWorkID) || duplicateIdentity(seenFamily, candidate.SourceFamilyID) ||
			duplicateIdentity(seenIndependence, candidate.IndependenceGroupID) || duplicateIdentity(seenAsset, candidate.Asset.SHA256) ||
			duplicateIdentity(seenRights, candidate.RightsEvidence.SHA256) ||
			(candidate.Nomination == VisualCorpusNominationPositive && duplicateIdentity(seenPositiveCreator, candidate.CreatorID)) {
			return errors.New("visual corpus nomination set contains invalid or repeated evidence")
		}
	}
	return nil
}
