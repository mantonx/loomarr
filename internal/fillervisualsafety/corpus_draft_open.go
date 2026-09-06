package fillervisualsafety

import (
	"encoding/hex"
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

func openVisualCorpusDraft(root string, allowIncomplete bool) (VisualCorpusDraftManifest, VisualCorpusDraftOwnerMap, error) {
	if err := validatePrivateReviewDirectory(root); err != nil {
		return VisualCorpusDraftManifest{}, VisualCorpusDraftOwnerMap{}, err
	}
	reviewRoot := filepath.Join(root, visualCorpusReviewDirectory)
	for _, directory := range []string{reviewRoot, filepath.Join(reviewRoot, visualCorpusCasesDirectory), filepath.Join(reviewRoot, visualCorpusRightsDirectory)} {
		if err := validatePrivateReviewDirectory(directory); err != nil {
			return VisualCorpusDraftManifest{}, VisualCorpusDraftOwnerMap{}, err
		}
	}
	manifest, err := readReviewJSON[VisualCorpusDraftManifest](filepath.Join(reviewRoot, visualCorpusManifestFilename))
	if err != nil {
		return VisualCorpusDraftManifest{}, VisualCorpusDraftOwnerMap{}, err
	}
	owner, err := readReviewJSON[VisualCorpusDraftOwnerMap](filepath.Join(root, visualCorpusOwnerFilename))
	if err != nil {
		return VisualCorpusDraftManifest{}, VisualCorpusDraftOwnerMap{}, err
	}
	if err := validateVisualCorpusDraftDocuments(manifest, owner); err != nil {
		return VisualCorpusDraftManifest{}, VisualCorpusDraftOwnerMap{}, err
	}
	if err := validateVisualCorpusDraftTopology(root, manifest, allowIncomplete); err != nil {
		return VisualCorpusDraftManifest{}, VisualCorpusDraftOwnerMap{}, err
	}
	if err := validateVisualCorpusDraftAssets(reviewRoot, manifest, owner); err != nil {
		return VisualCorpusDraftManifest{}, VisualCorpusDraftOwnerMap{}, err
	}
	return manifest, owner, nil
}

func validateVisualCorpusDraftDocuments(manifest VisualCorpusDraftManifest, owner VisualCorpusDraftOwnerMap) error {
	if manifest.SchemaVersion != VisualCorpusDraftManifestSchemaVersion ||
		manifest.ContractVersion != VisualCorpusDraftManifestContractVersion ||
		manifest.PreparedAt.IsZero() || manifest.PreparedAt.Location() != time.UTC ||
		!validDigest(manifest.AuthoritySHA256) || manifest.Policy.RelativePath != visualCorpusPolicyFilename ||
		!validReviewAsset(manifest.Policy, maximumReviewPolicyBytes) ||
		manifest.ReviewBoard.RelativePath != visualCorpusBoardFilename ||
		!validReviewAsset(manifest.ReviewBoard, maximumReviewManifestBytes) || manifest.CandidateModelOutput ||
		manifest.TruthAuthorityCreated || manifest.TrainingAllowed || manifest.ProductionAdmissionAllowed ||
		manifest.SHA256 == "" || manifest.SHA256 != VisualCorpusDraftManifestSHA256(manifest) {
		return errors.New("visual corpus draft manifest is invalid")
	}
	if owner.SchemaVersion != VisualCorpusDraftOwnerSchemaVersion || owner.ContractVersion != VisualCorpusDraftOwnerContractVersion ||
		owner.PreparedAt != manifest.PreparedAt || ValidateVisualCorpusDraftAuthority(owner.Authority) != nil ||
		owner.Authority.SHA256 != manifest.AuthoritySHA256 || owner.ReviewSHA256 != manifest.SHA256 ||
		owner.PreparedAt.Before(owner.Authority.AuthoredAt) ||
		owner.SHA256 == "" || owner.SHA256 != VisualCorpusDraftOwnerSHA256(owner) ||
		len(owner.Cases) != len(owner.Authority.Candidates) || len(manifest.Cases) != len(owner.Cases) {
		return errors.New("visual corpus draft owner map is invalid")
	}
	seenAlias := make(map[string]struct{}, len(manifest.Cases))
	seenAsset := make(map[string]struct{}, len(manifest.Cases))
	seenRights := make(map[string]struct{}, len(manifest.Cases))
	seenPerceptual := make(map[string]struct{}, len(manifest.Cases))
	for index, candidate := range manifest.Cases {
		ownerCase := owner.Cases[index]
		if !reflect.DeepEqual(ownerCase.Candidate, owner.Authority.Candidates[index]) || ownerCase.Alias != candidate.Alias ||
			!validIdentity(candidate.Alias) || duplicateIdentity(seenAlias, candidate.Alias) ||
			!validReviewAsset(candidate.Asset, MaximumVisualCorpusAssetBytes) ||
			!validReviewAsset(candidate.RightsEvidence, MaximumVisualCorpusRightsBytes) ||
			duplicateIdentity(seenAsset, candidate.Asset.SHA256) || duplicateIdentity(seenRights, candidate.RightsEvidence.SHA256) ||
			(candidate.MediaType != "image/jpeg" && candidate.MediaType != "image/png") || candidate.Width <= 0 || candidate.Height <= 0 ||
			int64(candidate.Width) > MaximumVisualCorpusPixels/int64(candidate.Height) || !validPerceptualHash(candidate.PerceptualHash) ||
			duplicateIdentity(seenPerceptual, candidate.PerceptualHash) {
			return errors.New("visual corpus draft review case is invalid")
		}
		expectedExtension := ".png"
		if candidate.MediaType == "image/jpeg" {
			expectedExtension = ".jpg"
		}
		if candidate.Asset.RelativePath != path.Join(visualCorpusCasesDirectory, candidate.Alias+expectedExtension) ||
			candidate.RightsEvidence.RelativePath != path.Join(visualCorpusRightsDirectory, candidate.Alias+".json") {
			return errors.New("visual corpus draft review case path is invalid")
		}
	}
	return nil
}

func validateVisualCorpusDraftTopology(root string, manifest VisualCorpusDraftManifest, allowIncomplete bool) error {
	expected := map[string]bool{
		".":                         true,
		visualCorpusOwnerFilename:   false,
		visualCorpusReviewDirectory: true,
		path.Join(visualCorpusReviewDirectory, visualCorpusCasesDirectory):   true,
		path.Join(visualCorpusReviewDirectory, visualCorpusRightsDirectory):  true,
		path.Join(visualCorpusReviewDirectory, visualCorpusPolicyFilename):   false,
		path.Join(visualCorpusReviewDirectory, visualCorpusBoardFilename):    false,
		path.Join(visualCorpusReviewDirectory, visualCorpusManifestFilename): false,
	}
	if allowIncomplete {
		expected[visualCorpusIncompleteName] = false
	}
	for _, candidate := range manifest.Cases {
		expected[path.Join(visualCorpusReviewDirectory, candidate.Asset.RelativePath)] = false
		expected[path.Join(visualCorpusReviewDirectory, candidate.RightsEvidence.RelativePath)] = false
	}
	seen := make(map[string]struct{}, len(expected))
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		wantDirectory, exists := expected[relative]
		if !exists || wantDirectory != entry.IsDir() {
			return errors.New("visual corpus draft contains unexpected evidence")
		}
		seen[relative] = struct{}{}
		return nil
	})
	if err != nil || len(seen) != len(expected) {
		return errors.New("visual corpus draft topology is invalid")
	}
	return nil
}

func validateVisualCorpusDraftAssets(reviewRoot string, manifest VisualCorpusDraftManifest, owner VisualCorpusDraftOwnerMap) error {
	policyRaw, err := readPrivateReviewFile(filepath.Join(reviewRoot, manifest.Policy.RelativePath), maximumReviewPolicyBytes)
	if err != nil || int64(len(policyRaw)) != manifest.Policy.Bytes || digestBytes(policyRaw) != manifest.Policy.SHA256 ||
		manifest.Policy.SHA256 != owner.Authority.PolicySHA256 {
		return errors.New("visual corpus draft policy asset is invalid")
	}
	policy, err := decodeCandidateBlindReviewPolicy(policyRaw)
	if err != nil {
		return err
	}
	for index, candidate := range manifest.Cases {
		assetRaw, err := readPrivateReviewFile(filepath.Join(reviewRoot, filepath.FromSlash(candidate.Asset.RelativePath)), MaximumVisualCorpusAssetBytes)
		if err != nil || int64(len(assetRaw)) != candidate.Asset.Bytes || digestBytes(assetRaw) != candidate.Asset.SHA256 {
			return errors.New("visual corpus draft image asset is invalid")
		}
		mediaType, width, height, perceptual, err := inspectVisualCorpusImage(assetRaw)
		if err != nil || mediaType != candidate.MediaType || width != candidate.Width || height != candidate.Height || perceptual != candidate.PerceptualHash {
			return errors.New("visual corpus draft image asset drifted")
		}
		rightsRaw, err := readPrivateReviewFile(filepath.Join(reviewRoot, filepath.FromSlash(candidate.RightsEvidence.RelativePath)), MaximumVisualCorpusRightsBytes)
		if err != nil || int64(len(rightsRaw)) != candidate.RightsEvidence.Bytes || digestBytes(rightsRaw) != candidate.RightsEvidence.SHA256 {
			return errors.New("visual corpus draft rights asset is invalid")
		}
		rights, err := decodeVisualCorpusRightsEvidence(rightsRaw)
		if err != nil || validateVisualCorpusRightsEvidence(owner.Cases[index].Candidate, rights, owner.Authority.AuthoredAt) != nil {
			return errors.New("visual corpus draft rights asset drifted")
		}
	}
	boardRaw, err := renderVisualCorpusReviewBoard(manifest.AuthoritySHA256, policy, manifest.Cases)
	if err != nil || int64(len(boardRaw)) != manifest.ReviewBoard.Bytes || digestBytes(boardRaw) != manifest.ReviewBoard.SHA256 {
		return errors.New("visual corpus draft review board identity is invalid")
	}
	actualBoard, err := readPrivateReviewFile(filepath.Join(reviewRoot, manifest.ReviewBoard.RelativePath), manifest.ReviewBoard.Bytes)
	if err != nil || !reflect.DeepEqual(actualBoard, boardRaw) {
		return errors.New("visual corpus draft review board drifted")
	}
	return nil
}

func validReviewAsset(asset CandidateBlindReviewAsset, maximum int64) bool {
	return validCorpusRelativePath(asset.RelativePath) && validDigest(asset.SHA256) && asset.Bytes > 0 && asset.Bytes <= maximum
}

func validPerceptualHash(value string) bool {
	if len(value) != 16 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
