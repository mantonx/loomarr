package fillervisualsafety

import (
	"bytes"
	"context"
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
	"slices"
	"time"
)

const (
	reviewDirectoryName  = "review"
	ownerMapFilename     = "owner-map.json"
	reviewManifestName   = "manifest.json"
	reviewSourceName     = "source.media"
	reviewPolicyName     = "policy.json"
	reviewFramesName     = "frames"
	reviewIncompleteName = ".incomplete"
)

func buildCandidateBlindReviewBundle(ctx context.Context, config CandidateBlindReviewConfig) (CandidateBlindReviewResult, error) {
	parent, err := reserveReviewOutput(config.OutputDir)
	if err != nil {
		return CandidateBlindReviewResult{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(config.OutputDir)
		}
	}()
	if err := validateReviewConfig(ctx, config); err != nil {
		return CandidateBlindReviewResult{}, err
	}
	prepared, err := Prepare(ctx, config.Source, config.Profile)
	if err != nil {
		return CandidateBlindReviewResult{}, fmt.Errorf("prepare candidate-blind visual review: %w", err)
	}
	defer func() { _ = prepared.Close() }()

	if err := writeReviewFile(filepath.Join(config.OutputDir, reviewIncompleteName), []byte("incomplete\n")); err != nil {
		return CandidateBlindReviewResult{}, err
	}
	reviewDir := filepath.Join(config.OutputDir, reviewDirectoryName)
	framesDir := filepath.Join(reviewDir, reviewFramesName)
	if err := os.Mkdir(reviewDir, 0o700); err != nil {
		return CandidateBlindReviewResult{}, fmt.Errorf("create candidate-blind visual review directory")
	}
	if err := os.Mkdir(framesDir, 0o700); err != nil {
		return CandidateBlindReviewResult{}, fmt.Errorf("create candidate-blind visual frame directory")
	}

	sourceAsset, totalBytes, err := copyReviewSource(prepared.SnapshotPath, filepath.Join(reviewDir, reviewSourceName), prepared.Authority)
	if err != nil {
		return CandidateBlindReviewResult{}, err
	}
	policyAsset, err := copyReviewPolicy(config.PolicyPath, filepath.Join(reviewDir, reviewPolicyName), prepared.Authority.PolicySHA256)
	if err != nil {
		return CandidateBlindReviewResult{}, err
	}
	if totalBytes > MaximumReviewPackageBytes-policyAsset.Bytes {
		return CandidateBlindReviewResult{}, errors.New("candidate-blind visual review exceeds its byte ceiling")
	}
	totalBytes += policyAsset.Bytes
	frames := make([]CandidateBlindReviewFrame, 0, len(prepared.Plan.Points))
	coverage, err := DecodeCoverage(ctx, prepared, config.FFmpegPath,
		func(_ context.Context, frame FrameEvidence, raw []byte) error {
			asset, assetErr := writeReviewFrame(framesDir, frame, raw)
			if assetErr != nil {
				return assetErr
			}
			if totalBytes > MaximumReviewPackageBytes-asset.PNGBytes {
				return errors.New("candidate-blind visual review exceeds its byte ceiling")
			}
			totalBytes += asset.PNGBytes
			frames = append(frames, asset)
			return nil
		})
	if err != nil {
		return CandidateBlindReviewResult{}, fmt.Errorf("decode candidate-blind visual review: %w", err)
	}

	manifest := CandidateBlindReviewManifest{
		SchemaVersion: CandidateBlindReviewSchemaVersion, ContractVersion: CandidateBlindReviewContractVersion,
		PreparedAt: config.PreparedAt.UTC(), Alias: config.Alias, Policy: policyAsset,
		CoverageProfileSHA256: config.Profile.SHA256, ReviewScope: ReviewScopeCompleteSource,
		MinimumCoveredExposureMS: config.Profile.MinimumCoveredExposureMS,
		Plan:                     prepared.Plan, Coverage: coverage, Source: sourceAsset, Frames: slices.Clone(frames),
		CandidateEvidenceIncluded: false, CandidateScoresIncluded: false, TruthAuthorityCreated: false,
		TrainingAllowed: false, ProductionAdmissionAllowed: false,
	}
	manifest.SHA256 = CandidateBlindReviewSHA256(manifest)
	owner := CandidateBlindReviewOwnerMap{
		SchemaVersion: CandidateBlindOwnerSchemaVersion, ContractVersion: CandidateBlindOwnerContractVersion,
		PreparedAt: config.PreparedAt.UTC(), Alias: config.Alias, SourceAuthority: config.Source.Authority,
		SourceFamilyID: config.SourceFamilyID, RightsSHA256: config.RightsSHA256,
		SelectionOrigin: config.SelectionOrigin, ReviewSHA256: manifest.SHA256,
	}
	owner.SHA256 = CandidateBlindReviewOwnerSHA256(owner)
	if err := writeReviewJSON(filepath.Join(reviewDir, reviewManifestName), manifest); err != nil {
		return CandidateBlindReviewResult{}, err
	}
	if err := writeReviewJSON(filepath.Join(config.OutputDir, ownerMapFilename), owner); err != nil {
		return CandidateBlindReviewResult{}, err
	}
	if _, _, err := openCandidateBlindReviewBundle(config.OutputDir, true); err != nil {
		return CandidateBlindReviewResult{}, fmt.Errorf("verify candidate-blind visual review: %w", err)
	}
	if err := syncReviewDirectory(framesDir); err != nil {
		return CandidateBlindReviewResult{}, err
	}
	if err := syncReviewDirectory(reviewDir); err != nil {
		return CandidateBlindReviewResult{}, err
	}
	if err := syncReviewDirectory(config.OutputDir); err != nil {
		return CandidateBlindReviewResult{}, err
	}
	if err := os.Remove(filepath.Join(config.OutputDir, reviewIncompleteName)); err != nil {
		return CandidateBlindReviewResult{}, errors.New("publish candidate-blind visual review")
	}
	if err := syncReviewDirectory(config.OutputDir); err != nil {
		return CandidateBlindReviewResult{}, err
	}
	if err := syncReviewDirectory(parent); err != nil {
		return CandidateBlindReviewResult{}, err
	}
	published = true
	return CandidateBlindReviewResult{
		PackageSHA256: manifest.SHA256, OwnerMapSHA256: owner.SHA256, FrameCount: len(frames),
	}, nil
}

func validateReviewConfig(ctx context.Context, config CandidateBlindReviewConfig) error {
	if ctx == nil || ctx.Err() != nil || !validIdentity(config.Alias) || !validIdentity(config.SourceFamilyID) ||
		!validDigest(config.RightsSHA256) ||
		(config.SelectionOrigin != ReviewSelectionIndependentCorpus && config.SelectionOrigin != ReviewSelectionTargetedDiagnostic) ||
		ValidateSourceAuthority(config.Source.Authority) != nil || ValidateCoverageProfile(config.Profile) != nil ||
		config.Source.Authority.PolicySHA256 == "" || !cleanAbsoluteReviewPath(config.PolicyPath) ||
		config.Source.Path == "" || config.FFmpegPath == "" ||
		config.PreparedAt.IsZero() || config.PreparedAt.Location() != time.UTC ||
		config.PreparedAt.Before(config.Source.Authority.MeasuredAt) {
		return errors.New("candidate-blind visual review configuration is invalid")
	}
	return nil
}

func cleanAbsoluteReviewPath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value
}

func reserveReviewOutput(output string) (string, error) {
	if output == "" || !filepath.IsAbs(output) || filepath.Clean(output) != output {
		return "", errors.New("candidate-blind visual review output is invalid")
	}
	parent := filepath.Dir(output)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || !filepath.IsAbs(resolved) {
		return "", errors.New("candidate-blind visual review parent is invalid")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("candidate-blind visual review parent is invalid")
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", errors.New("candidate-blind visual review output already exists")
		}
		return "", errors.New("candidate-blind visual review output cannot be reserved")
	}
	return resolved, nil
}

func copyReviewSource(sourcePath, destination string, authority SourceAuthority) (CandidateBlindReviewAsset, int64, error) {
	source, err := os.Open(sourcePath) //nolint:gosec // prepared private snapshot
	if err != nil {
		return CandidateBlindReviewAsset{}, 0, errors.New("open candidate-blind visual source")
	}
	defer func() { _ = source.Close() }()
	destinationFile, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return CandidateBlindReviewAsset{}, 0, errors.New("create candidate-blind visual source")
	}
	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destinationFile, digest), io.LimitReader(source, authority.SourceBytes+1))
	if copyErr == nil {
		copyErr = destinationFile.Sync()
	}
	closeErr := destinationFile.Close()
	sha := hex.EncodeToString(digest.Sum(nil))
	if copyErr != nil || closeErr != nil || written != authority.SourceBytes || sha != authority.SourceSHA256 {
		return CandidateBlindReviewAsset{}, 0, errors.New("copy candidate-blind visual source")
	}
	return CandidateBlindReviewAsset{RelativePath: reviewSourceName, SHA256: sha, Bytes: written}, written, nil
}

func copyReviewPolicy(sourcePath, destination, wantSHA256 string) (CandidateBlindReviewAsset, error) {
	raw, err := readPrivateReviewFile(sourcePath, maximumReviewPolicyBytes)
	if err != nil {
		return CandidateBlindReviewAsset{}, errors.New("open candidate-blind visual review policy")
	}
	digest := sha256.Sum256(raw)
	sha := hex.EncodeToString(digest[:])
	if sha != wantSHA256 || validateCandidateBlindReviewPolicy(raw) != nil {
		return CandidateBlindReviewAsset{}, errors.New("candidate-blind visual review policy is invalid")
	}
	if err := writeReviewFile(destination, raw); err != nil {
		return CandidateBlindReviewAsset{}, err
	}
	return CandidateBlindReviewAsset{RelativePath: reviewPolicyName, SHA256: sha, Bytes: int64(len(raw))}, nil
}

func writeReviewFrame(directory string, frame FrameEvidence, raw []byte) (CandidateBlindReviewFrame, error) {
	payload, err := encodeReviewPNG(raw, frame.Width, frame.Height)
	if err != nil || int64(len(payload)) <= 0 || int64(len(payload)) > maximumReviewFrameAssetBytes {
		return CandidateBlindReviewFrame{}, errors.New("encode candidate-blind visual frame")
	}
	filename := fmt.Sprintf("%06d-%012d.png", frame.Ordinal, frame.ObservedMS)
	if err := writeReviewFile(filepath.Join(directory, filename), payload); err != nil {
		return CandidateBlindReviewFrame{}, err
	}
	digest := sha256.Sum256(payload)
	return CandidateBlindReviewFrame{
		Ordinal: frame.Ordinal, RequestedMS: frame.RequestedMS, ObservedMS: frame.ObservedMS,
		Width: frame.Width, Height: frame.Height, RGB24SHA256: frame.SHA256,
		RelativePath: path.Join(reviewFramesName, filename), PNGSHA256: hex.EncodeToString(digest[:]),
		PNGBytes: int64(len(payload)),
	}, nil
}

func encodeReviewPNG(raw []byte, width, height int) ([]byte, error) {
	want, err := rgb24FrameBytes(width, height)
	if err != nil || len(raw) != want {
		return nil, errors.New("candidate-blind visual frame geometry is invalid")
	}
	imageValue := image.NewNRGBA(image.Rect(0, 0, width, height))
	for source, target := 0, 0; source < len(raw); source, target = source+3, target+4 {
		imageValue.Pix[target], imageValue.Pix[target+1], imageValue.Pix[target+2], imageValue.Pix[target+3] =
			raw[source], raw[source+1], raw[source+2], 0xff
	}
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&output, imageValue); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeReviewJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if int64(len(raw)) > maximumReviewManifestBytes {
		return errors.New("candidate-blind visual review document exceeds its byte ceiling")
	}
	return writeReviewFile(path, raw)
}

func writeReviewFile(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("create candidate-blind visual review evidence")
	}
	writeErr := error(nil)
	if _, err := file.Write(raw); err != nil {
		writeErr = err
	} else if err := file.Sync(); err != nil {
		writeErr = err
	}
	if err := file.Close(); writeErr == nil {
		writeErr = err
	}
	if writeErr != nil {
		return errors.New("write candidate-blind visual review evidence")
	}
	return nil
}

func syncReviewDirectory(path string) error {
	directory, err := os.Open(path) //nolint:gosec // validated private directory
	if err != nil {
		return errors.New("open candidate-blind visual review directory")
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return errors.New("sync candidate-blind visual review directory")
	}
	return nil
}
