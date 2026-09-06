package clipfetch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
)

type recoveryStore struct{ artifacts []filler.AcquisitionArtifact }

func (s *recoveryStore) ListRecoverableAcquisitionArtifacts(context.Context, int) ([]filler.AcquisitionArtifact, error) {
	return append([]filler.AcquisitionArtifact(nil), s.artifacts...), nil
}

func (s *recoveryStore) UpsertAcquisitionArtifacts(_ context.Context, artifacts []filler.AcquisitionArtifact) error {
	for _, artifact := range artifacts {
		updated := false
		for index := range s.artifacts {
			if s.artifacts[index].ID == artifact.ID {
				s.artifacts[index] = artifact
				updated = true
			}
		}
		if !updated {
			s.artifacts = append(s.artifacts, artifact)
		}
	}
	return nil
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
	store := &recoveryStore{artifacts: []filler.AcquisitionArtifact{artifact}}

	result, err := RecoverAcquisitionArtifacts(t.Context(), root, root, store, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Published != 1 || store.artifacts[0].State != filler.ArtifactPublished {
		t.Fatalf("recovery = %+v, artifact = %+v", result, store.artifacts[0])
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
	store := &recoveryStore{artifacts: []filler.AcquisitionArtifact{artifact}}

	result, err := RecoverAcquisitionArtifacts(t.Context(), root, root, store, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Repair != 1 || store.artifacts[0].State != filler.ArtifactRepair || store.artifacts[0].RepairReason == "" {
		t.Fatalf("recovery = %+v, artifact = %+v", result, store.artifacts[0])
	}
	if _, err := os.Stat(filepath.Join(root, artifact.MediaPath)); !os.IsNotExist(err) {
		t.Fatalf("substituted media was published: %v", err)
	}
}
