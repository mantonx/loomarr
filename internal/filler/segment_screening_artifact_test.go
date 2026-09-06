package filler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectSegmentScreeningArtifactVerifiesFullAndSparseIdentity(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "playback.mp4")
	left, right := sparseIdentityCollision()
	if err := os.WriteFile(path, left, 0o600); err != nil {
		t.Fatal(err)
	}
	sha256, bytes, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	clipHash, err := ClipID(path)
	if err != nil {
		t.Fatal(err)
	}
	observation, matches, err := inspectSegmentScreeningArtifact(t.Context(), path, sha256, bytes, clipHash)
	if err != nil || !matches || observation.SHA256 != sha256 || observation.ClipHash != clipHash {
		t.Fatalf("observation=%+v matches=%v error=%v", observation, matches, err)
	}
	if err := os.WriteFile(path, right, 0o600); err != nil {
		t.Fatal(err)
	}
	observation, matches, err = inspectSegmentScreeningArtifact(t.Context(), path, sha256, bytes, clipHash)
	if err != nil || matches || observation.ClipHash != clipHash || observation.SHA256 == sha256 {
		t.Fatalf("sparse collision was not caught by full digest: observation=%+v matches=%v error=%v", observation, matches, err)
	}
}

func TestInspectSegmentScreeningArtifactTreatsMissingAndSymlinkAsMismatch(t *testing.T) {
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing.mp4")
	digest := "1111111111111111111111111111111111111111111111111111111111111111"
	if observation, matches, err := inspectSegmentScreeningArtifact(t.Context(), missing, digest, 1, digest); err != nil || matches || observation.State != "missing" {
		t.Fatalf("missing observation=%+v matches=%v error=%v", observation, matches, err)
	}
	target := filepath.Join(directory, "target.mp4")
	link := filepath.Join(directory, "link.mp4")
	if err := os.WriteFile(target, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if observation, matches, err := inspectSegmentScreeningArtifact(t.Context(), link, digest, 5, digest); err != nil || matches || observation.State != "unsafe" {
		t.Fatalf("symlink observation=%+v matches=%v error=%v", observation, matches, err)
	}
}

func TestInspectSegmentScreeningArtifactObservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	digest := "1111111111111111111111111111111111111111111111111111111111111111"
	if _, _, err := inspectSegmentScreeningArtifact(ctx, filepath.Join(t.TempDir(), "playback.mp4"), digest, 1, digest); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want cancellation", err)
	}
}
