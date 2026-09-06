package filler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/loomarr/loomarr/internal/mediatools"
)

type segmentScreeningArtifactObservation struct {
	State    string `json:"state"`
	SHA256   string `json:"sha256,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
	ClipHash string `json:"clipHash,omitempty"`
}

// inspectSegmentScreeningArtifact binds all reads to one regular-file descriptor and compares
// both the full byte digest and sparse catalog identity. A missing or unsafe artifact is an
// inspectable mismatch; cancellation and filesystem failures remain operational errors.
func inspectSegmentScreeningArtifact(ctx context.Context, path, expectedSHA256 string, expectedBytes int64, expectedClipHash string) (segmentScreeningArtifactObservation, bool, error) {
	if err := ctx.Err(); err != nil {
		return segmentScreeningArtifactObservation{}, false, err
	}
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || !isContentHash(expectedSHA256) || expectedBytes <= 0 ||
		expectedBytes > mediatools.ConditioningMaxSnapshotBytes || !isContentHash(expectedClipHash) {
		return segmentScreeningArtifactObservation{}, false, fmt.Errorf("segment screening artifact request is invalid")
	}
	listed, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return segmentScreeningArtifactObservation{State: "missing"}, false, nil
	}
	if err != nil {
		return segmentScreeningArtifactObservation{}, false, fmt.Errorf("inspect segment screening artifact: %w", err)
	}
	if listed.Mode()&os.ModeSymlink != 0 || !listed.Mode().IsRegular() || listed.Size() <= 0 || listed.Size() > mediatools.ConditioningMaxSnapshotBytes {
		return segmentScreeningArtifactObservation{State: "unsafe", Bytes: listed.Size()}, false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return segmentScreeningArtifactObservation{}, false, fmt.Errorf("open segment screening artifact: %w", err)
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil {
		return segmentScreeningArtifactObservation{}, false, fmt.Errorf("stat segment screening artifact: %w", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(listed, opened) || opened.Size() != listed.Size() {
		return segmentScreeningArtifactObservation{State: "changed", Bytes: opened.Size()}, false, nil
	}

	hash := sha256.New()
	reader := &segmentScreeningContextReader{ctx: ctx, reader: io.NewSectionReader(file, 0, opened.Size())}
	if copied, err := io.CopyBuffer(hash, reader, make([]byte, 128<<10)); err != nil || copied != opened.Size() {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return segmentScreeningArtifactObservation{}, false, fmt.Errorf("hash segment screening artifact: %w", err)
	}
	clipHash, err := ClipIDFromReaderAt(file, opened.Size())
	if err != nil {
		return segmentScreeningArtifactObservation{}, false, fmt.Errorf("identify segment screening artifact: %w", err)
	}
	after, err := file.Stat()
	if err != nil {
		return segmentScreeningArtifactObservation{}, false, fmt.Errorf("restat segment screening artifact: %w", err)
	}
	if after.Size() != opened.Size() {
		return segmentScreeningArtifactObservation{State: "changed", Bytes: after.Size()}, false, nil
	}
	observation := segmentScreeningArtifactObservation{
		State: "observed", SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: opened.Size(), ClipHash: clipHash,
	}
	matches := observation.SHA256 == expectedSHA256 && observation.Bytes == expectedBytes && observation.ClipHash == expectedClipHash
	return observation, matches, nil
}

type segmentScreeningContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *segmentScreeningContextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
