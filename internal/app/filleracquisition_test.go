package app

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/store"
)

// A manifest binds the bytes Loomarr published into the watch folder. Replacing those bytes
// before Sync must not turn the replacement into an operator drop merely because intake rekeys
// it before durable ownership is consulted.
func TestSync_SubstitutedPublishedWatchArtifactRemainsHeld(t *testing.T) {
	dir := t.TempDir()
	layout, err := filler.NewLayout(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.WatchDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	published := filepath.Join(layout.WatchDir(), "published.mp4")
	if err := os.WriteFile(published, []byte("downloaded bytes recorded by acquisition"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalHash, err := filler.ClipID(published)
	if err != nil {
		t.Fatal(err)
	}
	originalDigest, originalSize, err := filler.FileSHA256(published)
	if err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(t.Context(), "sqlite://"+filepath.Join(t.TempDir(), "loomarr.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Unix(1_800_000_000, 0).UTC()
	if err := st.UpsertAcquisitionRun(t.Context(), filler.AcquisitionRun{
		ID: "acq-1", Trigger: filler.AcquisitionSource, SourceID: "youtube:classic",
		Status: filler.AcquisitionRunning, Requested: 1, StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAcquisitionArtifacts(t.Context(), []filler.AcquisitionArtifact{{
		ID: "artifact-1", AcquisitionID: "acq-1", SourceID: "youtube:classic", Provider: "youtube",
		SourceURL: "https://youtube.example/watch?v=one", MediaPath: "published.mp4", SidecarPath: "published.info.json",
		MediaSHA256: originalDigest, MediaBytes: originalSize, ClipHash: originalHash,
		State: filler.ArtifactPublished, CompletedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(published, []byte("substituted bytes that must remain quarantined"), 0o600); err != nil {
		t.Fatal(err)
	}
	substitutedHash, err := filler.ClipID(published)
	if err != nil {
		t.Fatal(err)
	}

	source := filler.DirSource{Layout: layout, Probe: func(context.Context, string) (filler.Probed, error) {
		return filler.Probed{DurationMs: 30_000}, nil
	}}
	syncer := filler.NewSyncer(source, fillerStoreAdapter{st}, layout, func() time.Time { return now }, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithAcquisitionAuthority(st)
	if _, err := syncer.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	clip, err := st.GetClip(t.Context(), substitutedHash)
	if err == nil && !clip.Held {
		t.Fatalf("substituted published artifact escaped quarantine: %+v", clip)
	}
}

func TestSync_FailedClaimedMoveCannotLaunderReplacementAsOperatorDrop(t *testing.T) {
	dir := t.TempDir()
	layout, err := filler.NewLayout(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.WatchDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	failedArrival := filepath.Join(layout.WatchDir(), "claimed.mp4")
	if err := os.WriteFile(failedArrival, []byte("valid acquired bytes whose first move must fail"), 0o600); err != nil {
		t.Fatal(err)
	}
	failedHash, err := filler.ClipID(failedArrival)
	if err != nil {
		t.Fatal(err)
	}
	failedDigest, failedSize, err := filler.FileSHA256(failedArrival)
	if err != nil {
		t.Fatal(err)
	}
	failedDestination, err := filler.ClipPath(layout.ClipDir(), failedHash, filepath.Ext(failedArrival))
	if err != nil {
		t.Fatal(err)
	}
	failedDestinationDir := filepath.Dir(failedDestination)
	if err := os.MkdirAll(failedDestinationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(failedDestinationDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(failedDestinationDir, 0o755) })

	st, err := store.Open(t.Context(), "sqlite://"+filepath.Join(t.TempDir(), "loomarr.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Unix(1_800_000_000, 0).UTC()
	if err := st.UpsertAcquisitionRun(t.Context(), filler.AcquisitionRun{
		ID: "acq-failed-move", Trigger: filler.AcquisitionSource, SourceID: "youtube:classic",
		Status: filler.AcquisitionRunning, Requested: 2, StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	failedArtifact := filler.AcquisitionArtifact{
		ID: "artifact-failed-move", AcquisitionID: "acq-failed-move", SourceID: "youtube:classic", Provider: "youtube",
		SourceURL: "https://youtube.example/watch?v=failed-move", MediaPath: "claimed.mp4", SidecarPath: "claimed.info.json",
		MediaSHA256: failedDigest, MediaBytes: failedSize, ClipHash: failedHash,
		State: filler.ArtifactPublished, CompletedAt: now, UpdatedAt: now,
	}
	if err := st.UpsertAcquisitionArtifacts(t.Context(), []filler.AcquisitionArtifact{failedArtifact}); err != nil {
		t.Fatal(err)
	}

	source := filler.DirSource{Layout: layout, Probe: func(context.Context, string) (filler.Probed, error) {
		return filler.Probed{DurationMs: 30_000}, nil
	}}
	syncer := filler.NewSyncer(source, fillerStoreAdapter{st}, layout, func() time.Time { return now }, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithAcquisitionAuthority(st)
	if _, err := syncer.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(failedArrival); err != nil {
		t.Fatalf("claimed arrival after failed move: %v", err)
	}
	boundArtifacts, err := st.ListRecoverableAcquisitionArtifacts(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	boundArtifact := acquisitionArtifactWithID(t, boundArtifacts, failedArtifact.ID)
	boundPath, err := filepath.Rel(layout.ClipDir(), failedDestination)
	if err != nil {
		t.Fatal(err)
	}
	if boundArtifact.MediaPath != filepath.ToSlash(boundPath) || boundArtifact.SidecarPath != failedArtifact.SidecarPath || boundArtifact.State != filler.ArtifactPublished {
		t.Fatalf("failed move did not preserve destination and original-name bindings: %+v", boundArtifact)
	}

	if err := os.Chmod(failedDestinationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	replacementBytes := []byte("replacement bytes must not gain operator authority")
	if err := os.WriteFile(failedArrival, replacementBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	replacementHash, err := filler.ClipID(failedArrival)
	if err != nil {
		t.Fatal(err)
	}

	validArrival := filepath.Join(layout.WatchDir(), "valid.mp4")
	if err := os.WriteFile(validArrival, []byte("another exact acquired artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	validHash, err := filler.ClipID(validArrival)
	if err != nil {
		t.Fatal(err)
	}
	validDigest, validSize, err := filler.FileSHA256(validArrival)
	if err != nil {
		t.Fatal(err)
	}
	validArtifact := filler.AcquisitionArtifact{
		ID: "artifact-valid", AcquisitionID: "acq-failed-move", SourceID: "youtube:classic", Provider: "youtube",
		SourceURL: "https://youtube.example/watch?v=valid", MediaPath: "valid.mp4", SidecarPath: "valid.info.json",
		MediaSHA256: validDigest, MediaBytes: validSize, ClipHash: validHash,
		State: filler.ArtifactPublished, CompletedAt: now, UpdatedAt: now,
	}
	if err := st.UpsertAcquisitionArtifacts(t.Context(), []filler.AcquisitionArtifact{validArtifact}); err != nil {
		t.Fatal(err)
	}

	manualArrival := filepath.Join(layout.WatchDir(), "manual.mp4")
	if err := os.WriteFile(manualArrival, []byte("genuinely unowned operator bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	manualHash, err := filler.ClipID(manualArrival)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := syncer.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	retainedReplacement, err := os.ReadFile(failedArrival)
	if err != nil {
		t.Fatalf("mismatched retry did not remain in the watch folder: %v", err)
	}
	if !bytes.Equal(retainedReplacement, replacementBytes) {
		t.Fatal("mismatched retry bytes changed")
	}
	if replacement, err := st.GetClip(t.Context(), replacementHash); err == nil && !replacement.Held {
		t.Fatalf("replacement after failed claimed move escaped as operator clip: %+v", replacement)
	}
	valid, err := st.GetClip(t.Context(), validHash)
	if err != nil {
		t.Fatal(err)
	}
	if !valid.Held {
		t.Fatalf("valid acquired artifact was not held: %+v", valid)
	}
	manual, err := st.GetClip(t.Context(), manualHash)
	if err != nil {
		t.Fatal(err)
	}
	if manual.Held {
		t.Fatalf("genuinely unowned operator clip was held: %+v", manual)
	}

	artifacts, err := st.ListRecoverableAcquisitionArtifacts(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	repairedArtifact := acquisitionArtifactWithID(t, artifacts, failedArtifact.ID)
	if repairedArtifact.MediaSHA256 != failedDigest || repairedArtifact.MediaBytes != failedSize || repairedArtifact.ClipHash != failedHash {
		t.Fatalf("failed-move artifact lost its original content identity: %+v", repairedArtifact)
	}
	if repairedArtifact.State != filler.ArtifactRepair || repairedArtifact.RepairReason == "" {
		t.Fatalf("mismatched retry did not leave durable repair evidence: %+v", repairedArtifact)
	}
}

func TestSync_ClaimedArrivalSurvivesSparseHashCollisionAtDestination(t *testing.T) {
	dir := t.TempDir()
	layout, err := filler.NewLayout(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.WatchDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	head := bytes.Repeat([]byte("h"), 64*1024)
	tail := bytes.Repeat([]byte("t"), 64*1024)
	ownedBytes := append(append(append([]byte(nil), head...), bytes.Repeat([]byte("a"), 64*1024)...), tail...)
	collidingBytes := append(append(append([]byte(nil), head...), bytes.Repeat([]byte("b"), 64*1024)...), tail...)
	arrival := filepath.Join(layout.WatchDir(), "claimed-collision.mp4")
	if err := os.WriteFile(arrival, ownedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	clipHash, err := filler.ClipID(arrival)
	if err != nil {
		t.Fatal(err)
	}
	digest, size, err := filler.FileSHA256(arrival)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := filler.ClipPath(layout.ClipDir(), clipHash, filepath.Ext(arrival))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, collidingBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	collidingHash, err := filler.ClipID(destination)
	if err != nil {
		t.Fatal(err)
	}
	collidingDigest, _, err := filler.FileSHA256(destination)
	if err != nil {
		t.Fatal(err)
	}
	if collidingHash != clipHash || collidingDigest == digest {
		t.Fatalf("fixture does not isolate sparse/full identity: sparse %q/%q full %q/%q", clipHash, collidingHash, digest, collidingDigest)
	}

	st, err := store.Open(t.Context(), "sqlite://"+filepath.Join(t.TempDir(), "loomarr.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Unix(1_800_000_000, 0).UTC()
	if err := st.UpsertAcquisitionRun(t.Context(), filler.AcquisitionRun{
		ID: "acq-sparse-collision", Trigger: filler.AcquisitionSource, SourceID: "archive:classic",
		Status: filler.AcquisitionRunning, Requested: 1, StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	artifact := filler.AcquisitionArtifact{
		ID: "artifact-sparse-collision", AcquisitionID: "acq-sparse-collision", SourceID: "archive:classic", Provider: "archive",
		SourceURL: "https://archive.example/details/collision", MediaPath: "claimed-collision.mp4", SidecarPath: "claimed-collision.info.json",
		MediaSHA256: digest, MediaBytes: size, ClipHash: clipHash,
		State: filler.ArtifactPublished, CompletedAt: now, UpdatedAt: now,
	}
	if err := st.UpsertAcquisitionArtifacts(t.Context(), []filler.AcquisitionArtifact{artifact}); err != nil {
		t.Fatal(err)
	}

	source := filler.DirSource{Layout: layout, Probe: func(context.Context, string) (filler.Probed, error) {
		return filler.Probed{DurationMs: 30_000}, nil
	}}
	syncer := filler.NewSyncer(source, fillerStoreAdapter{st}, layout, func() time.Time { return now }, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithAcquisitionAuthority(st)
	if _, err := syncer.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(arrival)
	if err != nil {
		t.Fatalf("exact acquired arrival was discarded as a sparse-hash duplicate: %v", err)
	}
	if !bytes.Equal(retained, ownedBytes) {
		t.Fatal("retained acquired arrival bytes changed")
	}
	stillColliding, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stillColliding, collidingBytes) {
		t.Fatal("pre-existing colliding destination bytes changed")
	}
	artifacts, err := st.ListRecoverableAcquisitionArtifacts(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	repairedArtifact := acquisitionArtifactWithID(t, artifacts, artifact.ID)
	if repairedArtifact.State != filler.ArtifactRepair || repairedArtifact.RepairReason == "" {
		t.Fatalf("sparse collision did not leave durable repair evidence: %+v", repairedArtifact)
	}
}

func acquisitionArtifactWithID(t *testing.T, artifacts []filler.AcquisitionArtifact, id string) filler.AcquisitionArtifact {
	t.Helper()
	for _, artifact := range artifacts {
		if artifact.ID == id {
			return artifact
		}
	}
	t.Fatalf("acquisition artifact %q disappeared from durable recovery state", id)
	return filler.AcquisitionArtifact{}
}
