package fillervisualsafety

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	visualCorpusReviewDirectory  = "review"
	visualCorpusCasesDirectory   = "cases"
	visualCorpusRightsDirectory  = "rights"
	visualCorpusPolicyFilename   = "policy.json"
	visualCorpusBoardFilename    = "index.html"
	visualCorpusManifestFilename = "manifest.json"
	visualCorpusOwnerFilename    = "owner-map.json"
	visualCorpusIncompleteName   = ".incomplete"
	maximumVisualAliasSeedBytes  = int64(64)
)

type preparedVisualCorpusCandidate struct {
	review VisualCorpusDraftReviewCase
	owner  VisualCorpusDraftOwnerCase
}

func prepareVisualCorpusDraft(ctx context.Context, config VisualCorpusDraftConfig) (VisualCorpusDraftResult, error) {
	parent, err := reserveReviewOutput(config.OutputDir)
	if err != nil {
		return VisualCorpusDraftResult{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(config.OutputDir)
		}
	}()
	if err := validateVisualCorpusDraftConfig(ctx, config); err != nil {
		return VisualCorpusDraftResult{}, err
	}
	seed, err := readPrivateReviewFile(config.AliasSeedPath, maximumVisualAliasSeedBytes)
	if err != nil || len(seed) < sha256.Size || digestBytes(seed) != config.Authority.AliasSeedSHA256 {
		return VisualCorpusDraftResult{}, errors.New("visual corpus draft alias seed is invalid")
	}
	if err := writeReviewFile(filepath.Join(config.OutputDir, visualCorpusIncompleteName), []byte("incomplete\n")); err != nil {
		return VisualCorpusDraftResult{}, err
	}
	reviewRoot := filepath.Join(config.OutputDir, visualCorpusReviewDirectory)
	casesRoot := filepath.Join(reviewRoot, visualCorpusCasesDirectory)
	rightsRoot := filepath.Join(reviewRoot, visualCorpusRightsDirectory)
	for _, directory := range []string{reviewRoot, casesRoot, rightsRoot} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return VisualCorpusDraftResult{}, errors.New("create visual corpus draft directory")
		}
	}
	policyAsset, err := copyReviewPolicy(config.PolicyPath, filepath.Join(reviewRoot, visualCorpusPolicyFilename), config.Authority.PolicySHA256)
	if err != nil {
		return VisualCorpusDraftResult{}, err
	}
	policyRaw, err := readPrivateReviewFile(config.PolicyPath, maximumReviewPolicyBytes)
	if err != nil {
		return VisualCorpusDraftResult{}, errors.New("open visual corpus draft policy")
	}
	policy, err := decodeCandidateBlindReviewPolicy(policyRaw)
	if err != nil {
		return VisualCorpusDraftResult{}, err
	}
	prepared := make([]preparedVisualCorpusCandidate, 0, len(config.Authority.Candidates))
	seenAlias := make(map[string]struct{}, len(config.Authority.Candidates))
	seenPerceptual := make(map[string]string, len(config.Authority.Candidates))
	for _, candidate := range config.Authority.Candidates {
		if err := ctx.Err(); err != nil {
			return VisualCorpusDraftResult{}, fmt.Errorf("prepare visual corpus draft: %w", err)
		}
		value, err := prepareVisualCorpusCandidate(config, seed, casesRoot, rightsRoot, candidate)
		if err != nil {
			return VisualCorpusDraftResult{}, err
		}
		if duplicateIdentity(seenAlias, value.review.Alias) {
			return VisualCorpusDraftResult{}, errors.New("visual corpus draft alias collision")
		}
		if previous, exists := seenPerceptual[value.review.PerceptualHash]; exists {
			return VisualCorpusDraftResult{}, fmt.Errorf("visual corpus draft normalized-image collision between %s and %s", previous, candidate.CandidateID)
		}
		seenPerceptual[value.review.PerceptualHash] = candidate.CandidateID
		prepared = append(prepared, value)
	}
	reviewCases := make([]VisualCorpusDraftReviewCase, len(prepared))
	ownerCases := make([]VisualCorpusDraftOwnerCase, len(prepared))
	for index, value := range prepared {
		reviewCases[index], ownerCases[index] = value.review, value.owner
	}
	boardRaw, err := renderVisualCorpusReviewBoard(config.Authority.SHA256, policy, reviewCases)
	if err != nil {
		return VisualCorpusDraftResult{}, err
	}
	boardPath := filepath.Join(reviewRoot, visualCorpusBoardFilename)
	if err := writeReviewFile(boardPath, boardRaw); err != nil {
		return VisualCorpusDraftResult{}, err
	}
	boardAsset := reviewAsset(visualCorpusBoardFilename, boardRaw)
	manifest := VisualCorpusDraftManifest{
		SchemaVersion: VisualCorpusDraftManifestSchemaVersion, ContractVersion: VisualCorpusDraftManifestContractVersion,
		PreparedAt: config.PreparedAt.UTC(), AuthoritySHA256: config.Authority.SHA256,
		Policy: policyAsset, ReviewBoard: boardAsset, Cases: reviewCases,
		CandidateModelOutput: false, TruthAuthorityCreated: false, TrainingAllowed: false,
		ProductionAdmissionAllowed: false,
	}
	manifest.SHA256 = VisualCorpusDraftManifestSHA256(manifest)
	owner := VisualCorpusDraftOwnerMap{
		SchemaVersion: VisualCorpusDraftOwnerSchemaVersion, ContractVersion: VisualCorpusDraftOwnerContractVersion,
		PreparedAt: config.PreparedAt.UTC(), Authority: config.Authority,
		ReviewSHA256: manifest.SHA256, Cases: ownerCases,
	}
	owner.SHA256 = VisualCorpusDraftOwnerSHA256(owner)
	if err := writeReviewJSON(filepath.Join(reviewRoot, visualCorpusManifestFilename), manifest); err != nil {
		return VisualCorpusDraftResult{}, err
	}
	if err := writeReviewJSON(filepath.Join(config.OutputDir, visualCorpusOwnerFilename), owner); err != nil {
		return VisualCorpusDraftResult{}, err
	}
	if _, _, err := openVisualCorpusDraft(config.OutputDir, true); err != nil {
		return VisualCorpusDraftResult{}, fmt.Errorf("verify visual corpus draft: %w", err)
	}
	for _, directory := range []string{casesRoot, rightsRoot, reviewRoot, config.OutputDir} {
		if err := syncReviewDirectory(directory); err != nil {
			return VisualCorpusDraftResult{}, err
		}
	}
	if err := os.Remove(filepath.Join(config.OutputDir, visualCorpusIncompleteName)); err != nil {
		return VisualCorpusDraftResult{}, errors.New("publish visual corpus draft")
	}
	if err := syncReviewDirectory(config.OutputDir); err != nil {
		return VisualCorpusDraftResult{}, err
	}
	if err := syncReviewDirectory(parent); err != nil {
		return VisualCorpusDraftResult{}, err
	}
	published = true
	return VisualCorpusDraftResult{ManifestSHA256: manifest.SHA256, OwnerMapSHA256: owner.SHA256, CaseCount: len(reviewCases)}, nil
}

func validateVisualCorpusDraftConfig(ctx context.Context, config VisualCorpusDraftConfig) error {
	if ctx == nil || ctx.Err() != nil || ValidateVisualCorpusDraftAuthority(config.Authority) != nil ||
		!cleanAbsoluteReviewPath(config.SourceRoot) || !cleanAbsoluteReviewPath(config.PolicyPath) ||
		!cleanAbsoluteReviewPath(config.AliasSeedPath) || !cleanAbsoluteReviewPath(config.OutputDir) ||
		config.PreparedAt.IsZero() || config.PreparedAt.Location() != time.UTC ||
		config.PreparedAt.Before(config.Authority.AuthoredAt) {
		return errors.New("visual corpus draft configuration is invalid")
	}
	if err := validatePrivateReviewDirectory(config.SourceRoot); err != nil {
		return errors.New("visual corpus draft source root is not private")
	}
	if visualCorpusPathsOverlap(config.SourceRoot, config.OutputDir) || config.PolicyPath == config.AliasSeedPath {
		return errors.New("visual corpus draft input and output paths overlap")
	}
	return nil
}

func visualCorpusPathsOverlap(left, right string) bool {
	resolvedLeft, leftErr := filepath.EvalSymlinks(left)
	resolvedRight, rightErr := filepath.EvalSymlinks(right)
	if leftErr != nil || rightErr != nil {
		return true
	}
	return visualCorpusPathContains(resolvedLeft, resolvedRight) || visualCorpusPathContains(resolvedRight, resolvedLeft)
}

func visualCorpusPathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func prepareVisualCorpusCandidate(config VisualCorpusDraftConfig, seed []byte, casesRoot, rightsRoot string, candidate VisualCorpusDraftCandidate) (preparedVisualCorpusCandidate, error) {
	assetRaw, err := readVisualCorpusInput(config.SourceRoot, candidate.AssetRelativePath, candidate.Asset)
	if err != nil {
		return preparedVisualCorpusCandidate{}, err
	}
	mediaType, width, height, perceptual, err := inspectVisualCorpusImage(assetRaw)
	if err != nil {
		return preparedVisualCorpusCandidate{}, fmt.Errorf("visual corpus candidate %s image is invalid", candidate.CandidateID)
	}
	rightsRaw, err := readVisualCorpusInput(config.SourceRoot, candidate.RightsRelativePath, candidate.RightsEvidence)
	if err != nil {
		return preparedVisualCorpusCandidate{}, err
	}
	rights, err := decodeVisualCorpusRightsEvidence(rightsRaw)
	if err != nil || validateVisualCorpusRightsEvidence(candidate, rights, config.Authority.AuthoredAt) != nil {
		return preparedVisualCorpusCandidate{}, fmt.Errorf("visual corpus candidate %s rights evidence is invalid", candidate.CandidateID)
	}
	alias := visualCorpusAlias(seed, config.Authority.SHA256, candidate.CandidateID)
	extension := ".png"
	if mediaType == "image/jpeg" {
		extension = ".jpg"
	}
	assetRelative := path.Join(visualCorpusCasesDirectory, alias+extension)
	rightsRelative := path.Join(visualCorpusRightsDirectory, alias+".json")
	if err := writeReviewFile(filepath.Join(casesRoot, alias+extension), assetRaw); err != nil {
		return preparedVisualCorpusCandidate{}, err
	}
	if err := writeReviewFile(filepath.Join(rightsRoot, alias+".json"), rightsRaw); err != nil {
		return preparedVisualCorpusCandidate{}, err
	}
	if _, err := readVisualCorpusInput(config.SourceRoot, candidate.AssetRelativePath, candidate.Asset); err != nil {
		return preparedVisualCorpusCandidate{}, errors.New("visual corpus source changed during preparation")
	}
	if _, err := readVisualCorpusInput(config.SourceRoot, candidate.RightsRelativePath, candidate.RightsEvidence); err != nil {
		return preparedVisualCorpusCandidate{}, errors.New("visual corpus rights evidence changed during preparation")
	}
	return preparedVisualCorpusCandidate{
		review: VisualCorpusDraftReviewCase{
			Alias: alias, Asset: CandidateBlindReviewAsset{RelativePath: assetRelative, SHA256: candidate.Asset.SHA256, Bytes: candidate.Asset.Bytes},
			RightsEvidence: CandidateBlindReviewAsset{RelativePath: rightsRelative, SHA256: candidate.RightsEvidence.SHA256, Bytes: candidate.RightsEvidence.Bytes},
			MediaType:      mediaType, Width: width, Height: height, PerceptualHash: perceptual,
		},
		owner: VisualCorpusDraftOwnerCase{Alias: alias, Candidate: candidate},
	}, nil
}
