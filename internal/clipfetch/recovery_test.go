package clipfetch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/store"
)

func openRecoveryStore(t *testing.T) store.Store {
	t.Helper()
	db, err := store.Open(t.Context(), "sqlite://"+filepath.Join(t.TempDir(), "recovery.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedRecoveryArtifact(t *testing.T, db store.Store, artifact filler.AcquisitionArtifact) {
	t.Helper()
	if err := db.UpsertAcquisitionRun(t.Context(), filler.AcquisitionRun{
		ID: artifact.AcquisitionID, Trigger: filler.AcquisitionPull, SourceID: artifact.SourceID,
		Status: filler.AcquisitionSuccess, Requested: 1, UpdatedAt: artifact.UpdatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertAcquisitionArtifacts(t.Context(), []filler.AcquisitionArtifact{artifact}); err != nil {
		t.Fatal(err)
	}
}

func stagedRecoveryArtifact(t *testing.T, root string) filler.AcquisitionArtifact {
	t.Helper()
	stage := filepath.Join(root, ".loomarr-acquisitions", "acq-1", "000", "download.mp4")
	if err := os.MkdirAll(filepath.Dir(stage), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, []byte("downloaded video bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filler.WriteSidecarTags(stage, filler.SidecarTags{
		SourceID: "youtube:classic", AcquisitionID: "acq-1",
	}, true); err != nil {
		t.Fatal(err)
	}
	digest, size, err := filler.FileSHA256(stage)
	if err != nil {
		t.Fatal(err)
	}
	clipHash, err := filler.ClipID(stage)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	return filler.AcquisitionArtifact{
		ID: "artifact-1", AcquisitionID: "acq-1", SourceID: "youtube:classic",
		Provider: "youtube", SourceURL: "https://youtube.com/watch?v=one",
		StagingPath: filepath.ToSlash(filepath.Join(".loomarr-acquisitions", "acq-1", "000", "download.mp4")),
		MediaPath:   "download.mp4", SidecarPath: "download.info.json",
		MediaSHA256: digest, MediaBytes: size, ClipHash: clipHash,
		State: filler.ArtifactStaged, CompletedAt: now, UpdatedAt: now,
	}
}

func TestRecoverAcquisitionArtifacts_PublishesValidatedStagedPair(t *testing.T) {
	root := t.TempDir()
	artifact := stagedRecoveryArtifact(t, root)
	db := openRecoveryStore(t)
	seedRecoveryArtifact(t, db, artifact)

	result, err := RecoverAcquisitionArtifacts(t.Context(), root, root, db, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := db.AcquisitionArtifactForClip(t.Context(), artifact.MediaPath, artifact.ClipHash)
	if err != nil || !found || result.Published != 1 || got.State != filler.ArtifactPublished {
		t.Fatalf("recovery = %+v, artifact = %+v, found=%v, err=%v", result, got, found, err)
	}
	for _, path := range []string{"download.mp4", "download.info.json"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("published %s: %v", path, err)
		}
	}
}

func TestRecoverAcquisitionArtifacts_DigestSubstitutionBecomesRepair(t *testing.T) {
	root := t.TempDir()
	artifact := stagedRecoveryArtifact(t, root)
	stage := filepath.Join(root, filepath.FromSlash(artifact.StagingPath))
	if err := os.WriteFile(stage, []byte("substituted bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := openRecoveryStore(t)
	seedRecoveryArtifact(t, db, artifact)

	result, err := RecoverAcquisitionArtifacts(t.Context(), root, root, db, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := db.AcquisitionArtifactForClip(t.Context(), artifact.MediaPath, artifact.ClipHash)
	if err != nil || !found || result.Repair != 1 || got.State != filler.ArtifactRepair || got.RepairReason == "" {
		t.Fatalf("recovery = %+v, artifact = %+v, found=%v, err=%v", result, got, found, err)
	}
	if _, err := os.Stat(filepath.Join(root, artifact.MediaPath)); !os.IsNotExist(err) {
		t.Fatalf("substituted media was published: %v", err)
	}
}
