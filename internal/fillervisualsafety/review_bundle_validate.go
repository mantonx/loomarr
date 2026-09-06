package fillervisualsafety

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"
)

// OpenCandidateBlindReviewBundle reopens every published byte and rejects
// extra files, candidate evidence, owner-map drift, and damaged frame pixels.
func OpenCandidateBlindReviewBundle(root string) (CandidateBlindReviewManifest, CandidateBlindReviewOwnerMap, error) {
	return openCandidateBlindReviewBundle(root, false)
}

func openCandidateBlindReviewBundle(root string, allowIncomplete bool) (CandidateBlindReviewManifest, CandidateBlindReviewOwnerMap, error) {
	if err := validatePrivateReviewDirectory(root); err != nil {
		return CandidateBlindReviewManifest{}, CandidateBlindReviewOwnerMap{}, err
	}
	reviewDir := filepath.Join(root, reviewDirectoryName)
	if err := validatePrivateReviewDirectory(reviewDir); err != nil {
		return CandidateBlindReviewManifest{}, CandidateBlindReviewOwnerMap{}, err
	}
	if err := validatePrivateReviewDirectory(filepath.Join(reviewDir, reviewFramesName)); err != nil {
		return CandidateBlindReviewManifest{}, CandidateBlindReviewOwnerMap{}, err
	}
	manifest, err := readReviewJSON[CandidateBlindReviewManifest](filepath.Join(reviewDir, reviewManifestName))
	if err != nil {
		return CandidateBlindReviewManifest{}, CandidateBlindReviewOwnerMap{}, err
	}
	owner, err := readReviewJSON[CandidateBlindReviewOwnerMap](filepath.Join(root, ownerMapFilename))
	if err != nil {
		return CandidateBlindReviewManifest{}, CandidateBlindReviewOwnerMap{}, err
	}
	if err := validateCandidateBlindReviewManifest(manifest); err != nil {
		return CandidateBlindReviewManifest{}, CandidateBlindReviewOwnerMap{}, err
	}
	if err := validateCandidateBlindReviewOwner(manifest, owner); err != nil {
		return CandidateBlindReviewManifest{}, CandidateBlindReviewOwnerMap{}, err
	}
	if err := validateReviewTopology(root, manifest, allowIncomplete); err != nil {
		return CandidateBlindReviewManifest{}, CandidateBlindReviewOwnerMap{}, err
	}
	if err := validateReviewAssets(reviewDir, manifest, owner); err != nil {
		return CandidateBlindReviewManifest{}, CandidateBlindReviewOwnerMap{}, err
	}
	return manifest, owner, nil
}

func validateCandidateBlindReviewManifest(manifest CandidateBlindReviewManifest) error {
	if manifest.SchemaVersion != CandidateBlindReviewSchemaVersion ||
		manifest.ContractVersion != CandidateBlindReviewContractVersion ||
		manifest.PreparedAt.IsZero() || manifest.PreparedAt.Location() != time.UTC ||
		!validIdentity(manifest.Alias) || manifest.Policy.RelativePath != reviewPolicyName ||
		!validDigest(manifest.Policy.SHA256) || manifest.Policy.Bytes <= 0 || manifest.Policy.Bytes > maximumReviewPolicyBytes ||
		manifest.CoverageProfileSHA256 != manifest.Plan.Profile.SHA256 ||
		manifest.ReviewScope != ReviewScopeCompleteSource ||
		manifest.MinimumCoveredExposureMS != manifest.Plan.Profile.MinimumCoveredExposureMS ||
		ValidateCoveragePlan(manifest.Plan) != nil || ValidateCoverageEvidence(manifest.Plan, manifest.Coverage) != nil ||
		manifest.Source.RelativePath != reviewSourceName || manifest.Source.SHA256 != manifest.Plan.SourceSHA256 ||
		manifest.Source.Bytes <= 0 || manifest.Source.Bytes > MaximumSourceBytes ||
		len(manifest.Frames) != len(manifest.Coverage.Frames) ||
		manifest.CandidateEvidenceIncluded || manifest.CandidateScoresIncluded || manifest.TruthAuthorityCreated ||
		manifest.TrainingAllowed || manifest.ProductionAdmissionAllowed || manifest.SHA256 == "" ||
		manifest.SHA256 != CandidateBlindReviewSHA256(manifest) {
		return errors.New("candidate-blind visual review manifest is invalid")
	}
	for index, asset := range manifest.Frames {
		frame := manifest.Coverage.Frames[index]
		wantPath := path.Join(reviewFramesName, fmt.Sprintf("%06d-%012d.png", frame.Ordinal, frame.ObservedMS))
		if asset.Ordinal != frame.Ordinal || asset.RequestedMS != frame.RequestedMS || asset.ObservedMS != frame.ObservedMS ||
			asset.Width != frame.Width || asset.Height != frame.Height || asset.RGB24SHA256 != frame.SHA256 ||
			asset.RelativePath != wantPath || !validDigest(asset.PNGSHA256) || asset.PNGBytes <= 0 ||
			asset.PNGBytes > maximumReviewFrameAssetBytes {
			return errors.New("candidate-blind visual review frame manifest is invalid")
		}
	}
	return nil
}

func validateCandidateBlindReviewOwner(manifest CandidateBlindReviewManifest, owner CandidateBlindReviewOwnerMap) error {
	if owner.SchemaVersion != CandidateBlindOwnerSchemaVersion || owner.ContractVersion != CandidateBlindOwnerContractVersion ||
		owner.PreparedAt != manifest.PreparedAt || owner.PreparedAt.Location() != time.UTC || owner.Alias != manifest.Alias ||
		ValidateSourceAuthority(owner.SourceAuthority) != nil || !validIdentity(owner.SourceFamilyID) ||
		!validDigest(owner.RightsSHA256) ||
		(owner.SelectionOrigin != ReviewSelectionIndependentCorpus && owner.SelectionOrigin != ReviewSelectionTargetedDiagnostic) ||
		owner.ReviewSHA256 != manifest.SHA256 || owner.SHA256 == "" || owner.SHA256 != CandidateBlindReviewOwnerSHA256(owner) ||
		owner.SourceAuthority.PolicySHA256 != manifest.Policy.SHA256 ||
		owner.SourceAuthority.SHA256 != manifest.Plan.SourceAuthoritySHA256 ||
		owner.SourceAuthority.SourceSHA256 != manifest.Source.SHA256 ||
		owner.SourceAuthority.SourceBytes != manifest.Source.Bytes ||
		owner.SourceAuthority.DurationMS != manifest.Plan.DurationMS || owner.SourceAuthority.Video != manifest.Plan.Video ||
		manifest.PreparedAt.Before(owner.SourceAuthority.MeasuredAt) {
		return errors.New("candidate-blind visual review owner map is invalid")
	}
	return nil
}

func validateReviewTopology(root string, manifest CandidateBlindReviewManifest, allowIncomplete bool) error {
	expected := map[string]bool{
		".": true, ownerMapFilename: false, reviewDirectoryName: true,
		filepath.Join(reviewDirectoryName, reviewManifestName): false,
		filepath.Join(reviewDirectoryName, reviewPolicyName):   false,
		filepath.Join(reviewDirectoryName, reviewSourceName):   false,
		filepath.Join(reviewDirectoryName, reviewFramesName):   true,
	}
	for _, frame := range manifest.Frames {
		expected[filepath.Join(reviewDirectoryName, filepath.FromSlash(frame.RelativePath))] = false
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
			return errors.New("candidate-blind visual review contains unexpected evidence")
		}
		seen[relative] = struct{}{}
		return nil
	})
	if err != nil || len(seen) != len(expected) {
		return errors.New("candidate-blind visual review topology is invalid")
	}
	return nil
}

func validateReviewAssets(reviewDir string, manifest CandidateBlindReviewManifest, owner CandidateBlindReviewOwnerMap) error {
	total := int64(0)
	policyPath := filepath.Join(reviewDir, manifest.Policy.RelativePath)
	policyRaw, err := readPrivateReviewFile(policyPath, maximumReviewPolicyBytes)
	if err != nil || int64(len(policyRaw)) != manifest.Policy.Bytes || validateCandidateBlindReviewPolicy(policyRaw) != nil {
		return errors.New("candidate-blind visual review policy asset is invalid")
	}
	policyDigest := sha256.Sum256(policyRaw)
	if hex.EncodeToString(policyDigest[:]) != manifest.Policy.SHA256 || manifest.Policy.SHA256 != owner.SourceAuthority.PolicySHA256 {
		return errors.New("candidate-blind visual review policy asset is invalid")
	}
	total += manifest.Policy.Bytes
	sourcePath := filepath.Join(reviewDir, manifest.Source.RelativePath)
	sha, size, err := hashReviewFile(sourcePath, MaximumSourceBytes)
	if err != nil || sha != owner.SourceAuthority.SourceSHA256 || size != owner.SourceAuthority.SourceBytes {
		return errors.New("candidate-blind visual review source asset is invalid")
	}
	total += size
	for _, frame := range manifest.Frames {
		path := filepath.Join(reviewDir, filepath.FromSlash(frame.RelativePath))
		if total > MaximumReviewPackageBytes-frame.PNGBytes {
			return errors.New("candidate-blind visual review exceeds its byte ceiling")
		}
		total += frame.PNGBytes
		if err := validateReviewPNG(path, frame); err != nil {
			return err
		}
	}
	return nil
}

func validateCandidateBlindReviewPolicy(raw []byte) error {
	_, err := decodeCandidateBlindReviewPolicy(raw)
	return err
}

func decodeCandidateBlindReviewPolicy(raw []byte) (CandidateBlindReviewPolicy, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var policy CandidateBlindReviewPolicy
	if err := decoder.Decode(&policy); err != nil {
		return CandidateBlindReviewPolicy{}, errors.New("candidate-blind visual review policy is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return CandidateBlindReviewPolicy{}, errors.New("candidate-blind visual review policy has trailing content")
	}
	if policy.SchemaVersion != 1 || policy.Kind != "loomarr-visual-sensitive-content-development-policy-v1" ||
		!policy.DevelopmentOnly || policy.ProductionAdmissionAllowed ||
		len(policy.PolicyMatches) == 0 || len(policy.PolicyMatches) > 64 {
		return CandidateBlindReviewPolicy{}, errors.New("candidate-blind visual review policy is invalid")
	}
	seen := make(map[string]struct{}, len(policy.PolicyMatches))
	for _, match := range policy.PolicyMatches {
		if !validIdentity(match.ID) || !validIdentity(match.Definition) {
			return CandidateBlindReviewPolicy{}, errors.New("candidate-blind visual review policy match is invalid")
		}
		if _, exists := seen[match.ID]; exists {
			return CandidateBlindReviewPolicy{}, errors.New("candidate-blind visual review policy match is duplicated")
		}
		seen[match.ID] = struct{}{}
	}
	return policy, nil
}

func validateReviewPNG(path string, expected CandidateBlindReviewFrame) error {
	raw, err := readPrivateReviewFile(path, maximumReviewFrameAssetBytes)
	if err != nil || int64(len(raw)) != expected.PNGBytes {
		return errors.New("candidate-blind visual review frame asset is invalid")
	}
	pngDigest := sha256.Sum256(raw)
	if hex.EncodeToString(pngDigest[:]) != expected.PNGSHA256 {
		return errors.New("candidate-blind visual review frame asset is invalid")
	}
	reader := bytes.NewReader(raw)
	imageValue, err := png.Decode(reader)
	if err != nil || reader.Len() != 0 || imageValue.Bounds() != image.Rect(0, 0, expected.Width, expected.Height) {
		return errors.New("candidate-blind visual review PNG is invalid")
	}
	digest := sha256.New()
	var pixel [3]byte
	for y := 0; y < expected.Height; y++ {
		for x := 0; x < expected.Width; x++ {
			red, green, blue, alpha := imageValue.At(x, y).RGBA()
			if alpha != 0xffff {
				return errors.New("candidate-blind visual review PNG has transparency")
			}
			pixel[0], pixel[1], pixel[2] = byte(red>>8), byte(green>>8), byte(blue>>8)
			_, _ = digest.Write(pixel[:])
		}
	}
	if hex.EncodeToString(digest.Sum(nil)) != expected.RGB24SHA256 {
		return errors.New("candidate-blind visual review PNG pixels drifted")
	}
	return nil
}

func validatePrivateReviewDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("candidate-blind visual review directory is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return errors.New("candidate-blind visual review directory is not private")
	}
	return nil
}

func hashReviewFile(path string, maximum int64) (string, int64, error) {
	file, size, err := openPrivateReviewFile(path, maximum)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	written, err := io.Copy(digest, io.LimitReader(file, maximum+1))
	if err != nil || written != size {
		return "", 0, errors.New("read candidate-blind visual review file")
	}
	return hex.EncodeToString(digest.Sum(nil)), written, nil
}

func openPrivateReviewFile(path string, maximum int64) (*os.File, int64, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 ||
		info.Size() <= 0 || info.Size() > maximum {
		return nil, 0, errors.New("candidate-blind visual review file is invalid")
	}
	file, err := os.Open(path) //nolint:gosec // path is rooted in validated private package
	if err != nil {
		return nil, 0, errors.New("open candidate-blind visual review file")
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Size() != info.Size() {
		_ = file.Close()
		return nil, 0, errors.New("candidate-blind visual review file identity drifted")
	}
	return file, info.Size(), nil
}

func readPrivateReviewFile(path string, maximum int64) ([]byte, error) {
	file, size, err := openPrivateReviewFile(path, maximum)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) != size {
		return nil, errors.New("candidate-blind visual review file bytes drifted")
	}
	return raw, nil
}

func readReviewJSON[T any](path string) (T, error) {
	var zero T
	raw, err := readPrivateReviewFile(path, maximumReviewManifestBytes)
	if err != nil {
		return zero, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return zero, errors.New("candidate-blind visual review document is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return zero, errors.New("candidate-blind visual review document has trailing content")
	}
	return value, nil
}
