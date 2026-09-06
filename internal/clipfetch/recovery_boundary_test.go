package clipfetch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/store"
)

func recoveryBoundaryArtifact(t *testing.T, root, id string, state filler.AcquisitionArtifactState, at time.Time) filler.AcquisitionArtifact {
	t.Helper()
	media := filepath.Join(root, "download-"+id+".mp4")
	if err := os.WriteFile(media, []byte("recovery boundary bytes "+id), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filler.WriteSidecarTags(media, filler.SidecarTags{SourceID: "youtube:classic", AcquisitionID: "acq-boundary"}, true); err != nil {
		t.Fatal(err)
	}
	digest, size, err := filler.FileSHA256(media)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := filler.ClipID(media)
	if err != nil {
		t.Fatal(err)
	}
	return filler.AcquisitionArtifact{
		ID: id, AcquisitionID: "acq-boundary", SourceID: "youtube:classic", Provider: "youtube",
		SourceURL: "https://youtube.com/watch?v=boundary", StagingPath: filepath.ToSlash(filepath.Join(".loomarr-acquisitions", id, "download.mp4")),
		MediaPath: filepath.Base(media), SidecarPath: strings.TrimSuffix(filepath.Base(media), ".mp4") + ".info.json",
		MediaSHA256: digest, MediaBytes: size, ClipHash: hash, State: state, RepairReason: repairReason(state),
		CompletedAt: at, UpdatedAt: at,
	}
}

func repairReason(state filler.AcquisitionArtifactState) string {
	if state == filler.ArtifactRepair {
		return "boundary repair"
	}
	return ""
}

func seedBoundaryRun(t *testing.T, db store.Store, at time.Time) {
	t.Helper()
	if err := db.UpsertAcquisitionRun(t.Context(), filler.AcquisitionRun{ID: "acq-boundary", Trigger: filler.AcquisitionPull, SourceID: "youtube:classic", Status: filler.AcquisitionSuccess, Requested: 1, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
}

func moveBoundaryMedia(t *testing.T, watch, catalog string, artifact filler.AcquisitionArtifact, sidecar bool) string {
	t.Helper()
	source := filepath.Join(watch, artifact.MediaPath)
	destination, err := filler.ClipPath(catalog, artifact.ClipHash, filepath.Ext(artifact.MediaPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, destination); err != nil {
		t.Fatal(err)
	}
	if sidecar {
		if err := filler.WriteSidecarTags(destination, filler.SidecarTags{SourceID: artifact.SourceID, AcquisitionID: artifact.AcquisitionID}, true); err != nil {
			t.Fatal(err)
		}
	}
	return destination
}

func TestRecoverAcquisitionArtifacts_RestoresMissingCatalogProvenance(t *testing.T) {
	watch, catalog := t.TempDir(), t.TempDir()
	db := openRecoveryStore(t)
	at := time.Unix(1_900_000_000, 0).UTC()
	seedBoundaryRun(t, db, at)
	artifact := recoveryBoundaryArtifact(t, watch, "missing-sidecar", filler.ArtifactPublished, at)
	if err := db.UpsertAcquisitionArtifacts(t.Context(), []filler.AcquisitionArtifact{artifact}); err != nil {
		t.Fatal(err)
	}
	destination := moveBoundaryMedia(t, watch, catalog, artifact, false)
	if _, err := RecoverAcquisitionArtifacts(t.Context(), watch, catalog, db, func() time.Time { return at.Add(time.Minute) }); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.AcquisitionArtifactForClip(t.Context(), "unrelated.mp4", artifact.ClipHash)
	if err != nil || !found || got.State != filler.ArtifactConsumed {
		t.Fatalf("artifact = %+v, found=%v, err=%v", got, found, err)
	}
	if _, err := os.Stat(strings.TrimSuffix(destination, ".mp4") + ".info.json"); err != nil {
		t.Fatalf("restored catalog provenance: %v", err)
	}
}

func TestRecoverAcquisitionArtifacts_DoesNotReplaceMalformedCatalogProvenance(t *testing.T) {
	watch, catalog := t.TempDir(), t.TempDir()
	db := openRecoveryStore(t)
	at := time.Unix(1_900_000_000, 0).UTC()
	seedBoundaryRun(t, db, at)
	artifact := recoveryBoundaryArtifact(t, watch, "malformed-sidecar", filler.ArtifactPublished, at)
	if err := db.UpsertAcquisitionArtifacts(t.Context(), []filler.AcquisitionArtifact{artifact}); err != nil {
		t.Fatal(err)
	}
	destination := moveBoundaryMedia(t, watch, catalog, artifact, false)
	sidecar := strings.TrimSuffix(destination, ".mp4") + ".info.json"
	if err := os.WriteFile(sidecar, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverAcquisitionArtifacts(t.Context(), watch, catalog, db, func() time.Time { return at.Add(time.Minute) }); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.AcquisitionArtifactForClip(t.Context(), artifact.MediaPath, artifact.ClipHash)
	if err != nil || !found || got.State != filler.ArtifactRepair {
		t.Fatalf("artifact = %+v, found=%v, err=%v", got, found, err)
	}
	contents, err := os.ReadFile(sidecar)
	if err != nil || string(contents) != "not json" {
		t.Fatalf("sidecar was changed: %q, %v", contents, err)
	}
}

func TestRecoverAcquisitionArtifacts_PagesPastOldNonterminalRows(t *testing.T) {
	for _, state := range []filler.AcquisitionArtifactState{filler.ArtifactRepair, filler.ArtifactPublished} {
		t.Run(string(state), func(t *testing.T) {
			watch, catalog := t.TempDir(), t.TempDir()
			db := openRecoveryStore(t)
			at := time.Unix(1_900_000_000, 0).UTC()
			seedBoundaryRun(t, db, at)
			for i := 0; i < 500; i++ {
				artifact := recoveryBoundaryArtifact(t, watch, "old-"+time.Unix(int64(i), 0).UTC().Format("150405.000000000"), state, at.Add(time.Duration(i)*time.Second))
				if err := db.UpsertAcquisitionArtifacts(t.Context(), []filler.AcquisitionArtifact{artifact}); err != nil {
					t.Fatal(err)
				}
			}
			target := recoveryBoundaryArtifact(t, watch, "target", filler.ArtifactStaged, at.Add(501*time.Second))
			if err := db.UpsertAcquisitionArtifacts(t.Context(), []filler.AcquisitionArtifact{target}); err != nil {
				t.Fatal(err)
			}
			stage := filepath.Join(watch, target.StagingPath)
			if err := os.MkdirAll(filepath.Dir(stage), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(watch, target.MediaPath), stage); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(watch, target.SidecarPath), strings.TrimSuffix(stage, ".mp4")+".info.json"); err != nil {
				t.Fatal(err)
			}
			if _, err := RecoverAcquisitionArtifacts(t.Context(), watch, catalog, db, func() time.Time { return at.Add(10 * time.Minute) }); err != nil {
				t.Fatal(err)
			}
			got, found, err := db.AcquisitionArtifactForClip(t.Context(), target.MediaPath, target.ClipHash)
			if err != nil || !found || got.State != filler.ArtifactPublished {
				t.Fatalf("target = %+v, found=%v, err=%v", got, found, err)
			}
		})
	}
}
