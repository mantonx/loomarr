package filler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/mediatools"
)

const (
	structureAssessmentMediaSchemaVersion   = 1
	structureAssessmentMediaContractVersion = "filler-structure-assessment-media-lineage-v1"
	structureAssessmentIndexSchemaVersion   = 1
	structureAssessmentIndexContractVersion = "filler-structure-assessment-media-index-v1"
	structureAssessmentMediaDirName         = "structure-assessment"
	structureAssessmentLineageMaximumBytes  = 64 << 10
)

// StructureAssessmentMediaSourceIdentity deliberately excludes a pathname. A retained path is a
// location at which bytes may be found; it is not part of the decision authority.
type StructureAssessmentMediaSourceIdentity struct {
	Role       SplitSourceRole `json:"role"`
	SHA256     string          `json:"sha256"`
	Bytes      int64           `json:"bytes"`
	ClipHash   string          `json:"clipHash"`
	DurationMS int64           `json:"durationMs"`
}

// StructureAssessmentMediaDerivative identifies the complete normalized output without naming
// the machine-local location at which it was published.
type StructureAssessmentMediaDerivative struct {
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
	DurationMS int64  `json:"durationMs"`
}

// StructureAssessmentMediaLineage is the path-free, content-addressed proof for the exact bytes
// sent to independent complete-timeline assessors.
type StructureAssessmentMediaLineage struct {
	SchemaVersion   int                                    `json:"schemaVersion"`
	ContractVersion string                                 `json:"contractVersion"`
	OperationSHA256 string                                 `json:"operationSha256"`
	Source          StructureAssessmentMediaSourceIdentity `json:"source"`
	Profile         fillerstructuremedia.Profile           `json:"profile"`
	Tool            mediatools.MediaToolIdentity           `json:"tool"`
	Media           StructureAssessmentMediaDerivative     `json:"media"`
	SHA256          string                                 `json:"sha256"`
}

type structureAssessmentMediaOperation struct {
	SchemaVersion   int                                    `json:"schemaVersion"`
	ContractVersion string                                 `json:"contractVersion"`
	Source          StructureAssessmentMediaSourceIdentity `json:"source"`
	ProfileSHA256   string                                 `json:"profileSha256"`
	Tool            mediatools.MediaToolIdentity           `json:"tool"`
}

type structureAssessmentMediaIndex struct {
	SchemaVersion   int    `json:"schemaVersion"`
	ContractVersion string `json:"contractVersion"`
	OperationSHA256 string `json:"operationSha256"`
	LineageSHA256   string `json:"lineageSha256"`
}

func structureAssessmentSourceIdentity(source SplitSourceAsset) StructureAssessmentMediaSourceIdentity {
	return StructureAssessmentMediaSourceIdentity{
		Role: source.Role, SHA256: source.SHA256, Bytes: source.Bytes,
		ClipHash: source.ClipHash, DurationMS: source.DurationMs,
	}
}

func structureAssessmentOperationSHA256(source StructureAssessmentMediaSourceIdentity, profile fillerstructuremedia.Profile, tool mediatools.MediaToolIdentity) string {
	operation := structureAssessmentMediaOperation{
		SchemaVersion: structureAssessmentMediaSchemaVersion, ContractVersion: structureAssessmentMediaContractVersion,
		Source: source, ProfileSHA256: profile.SHA256, Tool: tool,
	}
	raw, err := json.Marshal(operation)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func structureAssessmentLineageSHA256(lineage StructureAssessmentMediaLineage) string {
	lineage.SHA256 = ""
	raw, err := json.Marshal(lineage)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validateStructureAssessmentLineage(lineage StructureAssessmentMediaLineage) error {
	if lineage.SchemaVersion != structureAssessmentMediaSchemaVersion ||
		lineage.ContractVersion != structureAssessmentMediaContractVersion ||
		!isContentHash(lineage.OperationSHA256) || !isContentHash(lineage.SHA256) ||
		lineage.SHA256 != structureAssessmentLineageSHA256(lineage) ||
		lineage.Profile != fillerstructuremedia.CanonicalProfile() ||
		lineage.Profile.SHA256 != fillerstructuremedia.ProfileSHA256(lineage.Profile) ||
		(lineage.Source.Role != SplitSourceEvidence && lineage.Source.Role != SplitSourceLegacyPlayback) {
		return errors.New("structure assessment media lineage authority is invalid")
	}
	if !isContentHash(lineage.Source.SHA256) || !isContentHash(lineage.Source.ClipHash) ||
		lineage.Source.Bytes <= 0 || lineage.Source.DurationMS <= 0 ||
		!isContentHash(lineage.Media.SHA256) || lineage.Media.Bytes <= 0 ||
		lineage.Media.Bytes > fillerstructuremedia.MaximumVideoBytes || lineage.Media.DurationMS <= 0 ||
		absoluteDurationDifference(lineage.Media.DurationMS, lineage.Source.DurationMS) > lineage.Profile.MaximumTimelineDriftMS {
		return errors.New("structure assessment media lineage identities are invalid")
	}
	if err := lineage.Tool.Validate(); err != nil {
		return fmt.Errorf("structure assessment media lineage tool: %w", err)
	}
	wantOperation := structureAssessmentOperationSHA256(lineage.Source, lineage.Profile, lineage.Tool)
	if lineage.OperationSHA256 != wantOperation {
		return errors.New("structure assessment media lineage operation does not reproduce")
	}
	return nil
}

func structureAssessmentMediaPath(clipDir, digest string) string {
	return filepath.Join(clipDir, MediaAssetRootName, structureAssessmentMediaDirName, "media", digest[:2], digest+".mp4")
}

func structureAssessmentLineagePath(clipDir, digest string) string {
	return filepath.Join(clipDir, MediaAssetRootName, structureAssessmentMediaDirName, "lineage", digest[:2], digest+".json")
}

func structureAssessmentIndexPath(clipDir, operation string) string {
	return filepath.Join(clipDir, MediaAssetRootName, structureAssessmentMediaDirName, "operations", operation[:2], operation+".json")
}

func loadStructureAssessmentIndex(path, operation string) (structureAssessmentMediaIndex, bool, error) {
	raw, err := readBoundedRegularFile(path, structureAssessmentLineageMaximumBytes)
	if errors.Is(err, os.ErrNotExist) {
		return structureAssessmentMediaIndex{}, false, nil
	}
	if err != nil {
		return structureAssessmentMediaIndex{}, false, fmt.Errorf("read structure assessment media index: %w", err)
	}
	var index structureAssessmentMediaIndex
	if err := strictStructureAssessmentJSON(raw, &index); err != nil ||
		index.SchemaVersion != structureAssessmentIndexSchemaVersion ||
		index.ContractVersion != structureAssessmentIndexContractVersion ||
		index.OperationSHA256 != operation || !isContentHash(index.LineageSHA256) {
		return structureAssessmentMediaIndex{}, false, errors.New("structure assessment media index is invalid")
	}
	return index, true, nil
}

func loadStructureAssessmentLineage(path, digest string) (StructureAssessmentMediaLineage, error) {
	raw, err := readBoundedRegularFile(path, structureAssessmentLineageMaximumBytes)
	if err != nil {
		return StructureAssessmentMediaLineage{}, fmt.Errorf("read structure assessment media lineage: %w", err)
	}
	var lineage StructureAssessmentMediaLineage
	if err := strictStructureAssessmentJSON(raw, &lineage); err != nil {
		return StructureAssessmentMediaLineage{}, fmt.Errorf("decode structure assessment media lineage: %w", err)
	}
	if lineage.SHA256 != digest {
		return StructureAssessmentMediaLineage{}, errors.New("structure assessment media lineage filename does not match content")
	}
	if err := validateStructureAssessmentLineage(lineage); err != nil {
		return StructureAssessmentMediaLineage{}, err
	}
	return lineage, nil
}

func strictStructureAssessmentJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func readBoundedRegularFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.New("artifact is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) != info.Size() || int64(len(raw)) > maximum {
		return nil, errors.New("artifact changed or exceeded its byte ceiling while read")
	}
	return raw, nil
}

func publishStructureAssessmentArtifact(ctx context.Context, staged, target string, expected []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.Link(staged, target); err == nil {
		_ = os.Remove(staged)
		return nil
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	existing, err := readBoundedRegularFile(target, max(int64(len(expected)), structureAssessmentLineageMaximumBytes))
	if err != nil || !bytes.Equal(existing, expected) {
		return errors.New("existing structure assessment artifact does not match exact bytes")
	}
	_ = os.Remove(staged)
	return nil
}

func publishStructureAssessmentMedia(ctx context.Context, staged, target, digest string, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.Link(staged, target); err == nil {
		_ = os.Remove(staged)
		return nil
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	existingDigest, existingSize, err := FileSHA256(target)
	if err != nil || existingDigest != digest || existingSize != size {
		return errors.New("existing structure assessment media does not match its content address")
	}
	equal, err := exactFileBytesEqual(ctx, staged, target, fillerstructuremedia.MaximumVideoBytes)
	if err != nil || !equal {
		return errors.New("existing structure assessment media does not match exact bytes")
	}
	_ = os.Remove(staged)
	return nil
}

func writeStructureAssessmentJSON(stageDir, pattern string, value any) (string, []byte, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 || len(raw) > structureAssessmentLineageMaximumBytes {
		return "", nil, errors.New("encode bounded structure assessment artifact")
	}
	file, err := os.CreateTemp(stageDir, pattern)
	if err != nil {
		return "", nil, err
	}
	path := file.Name()
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", nil, err
	}
	return path, raw, nil
}

func absoluteDurationDifference(left, right int64) int64 {
	if left < right {
		return right - left
	}
	return left - right
}
