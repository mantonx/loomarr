package clipfetch_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/clipfetch"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestRecoverAcquisitionArtifacts_ReplaysProviderArchiveBeforePublication(t *testing.T) {
	watch := t.TempDir()
	dsn := "sqlite://" + filepath.Join(t.TempDir(), "acquisitions.db")
	failMarker := filepath.Join(watch, ".fail-provider-archive-once")
	if err := os.WriteFile(failMarker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ytdlp := testkit.Executable(t, "yt-dlp", `#!/bin/sh
archive=""; result=""
while test "$#" -gt 0; do case "$1" in --download-archive) archive="$2"; shift 2 ;; --print-to-file) result="$3"; shift 3 ;; *) shift ;; esac; done
if test -s "$archive"; then exit 0; fi
stage=$(dirname "$result")
watch=$(dirname "$(dirname "$(dirname "$stage")")")
printf 'video bytes' > "$stage/download.mp4"
printf '{"id":"owned-id","title":"download"}\n' > "$stage/download.info.json"
printf 'youtube owned-id\nyoutube unreported-id\n' > "$archive"
printf 'owned-id\t"%s"\n' "$stage/download.mp4" >> "$result"
if test -f "$watch/.fail-provider-archive-once"; then
  rm "$watch/.fail-provider-archive-once"
  mkdir "$watch/.yt-dlp-archive.txt"
fi
`)

	source := clipfetch.Source{
		ID: "youtube:restart", AcquisitionID: "acq-restart", Kind: clipfetch.YouTube,
		URL: "https://youtube.com/watch?v=owned-id",
	}
	firstStore, err := store.Open(t.Context(), dsn, true)
	if err != nil {
		t.Fatal(err)
	}
	seedArchiveRecoveryRun(t, firstStore, source)
	first := clipfetch.New(clipfetch.NewYtDlpDownloader(ytdlp, "ffmpeg"), nil, watch, discardLog()).WithArtifactWriter(firstStore).Run(t.Context(), []clipfetch.Source{source})
	if first.Failed != 1 {
		t.Fatalf("first acquisition = %+v, want transient archive commit failure", first)
	}
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), dsn, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	blocked, err := clipfetch.RecoverAcquisitionArtifacts(t.Context(), watch, watch, reopened, time.Now)
	if err == nil {
		t.Fatalf("blocked recovery = %+v, want provider archive replay failure", blocked)
	}
	artifact, found, lookupErr := reopened.AcquisitionArtifactForClip(t.Context(), "download.mp4", "")
	if lookupErr != nil || !found || artifact.ProviderArchiveCommitted {
		t.Fatalf("blocked artifact = %+v, found=%v, err=%v", artifact, found, lookupErr)
	}
	if _, statErr := os.Stat(filepath.Join(watch, filepath.FromSlash(artifact.StagingPath))); statErr != nil {
		t.Fatalf("blocked recovery did not retain staged bytes: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(watch, artifact.MediaPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("blocked recovery made media visible: %v", statErr)
	}
	if err := os.Remove(filepath.Join(watch, ".yt-dlp-archive.txt")); err != nil {
		t.Fatal(err)
	}
	result, err := clipfetch.RecoverAcquisitionArtifacts(t.Context(), watch, watch, reopened, func() time.Time {
		return time.Unix(1_900_000_000, 0).UTC()
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Published != 1 {
		t.Fatalf("recovery = %+v, want one publication after archive replay", result)
	}
	archive, err := os.ReadFile(filepath.Join(watch, ".yt-dlp-archive.txt"))
	if err != nil || string(archive) != "youtube owned-id\n" {
		t.Fatalf("replayed provider archive = %q, %v; want only exact reported output", archive, err)
	}

	retry := source
	retry.AcquisitionID = "acq-after-restart"
	seedArchiveRecoveryRun(t, reopened, retry)
	deduped := clipfetch.New(clipfetch.NewYtDlpDownloader(ytdlp, "ffmpeg"), nil, watch, discardLog()).WithArtifactWriter(reopened).Run(t.Context(), []clipfetch.Source{retry})
	if deduped.Fetched != 0 || len(deduped.Artifacts) != 0 {
		t.Fatalf("post-restart acquisition = %+v, want provider archive deduplication", deduped)
	}
	if _, err := os.Stat(filepath.Join(watch, "download.mp4")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestRecoverAcquisitionArtifacts_MissingSidecarRetainsExactArchiveIdentity(t *testing.T) {
	watch := t.TempDir()
	dsn := "sqlite://" + filepath.Join(t.TempDir(), "acquisitions.db")
	failMarker := filepath.Join(watch, ".fail-provider-archive-once")
	if err := os.WriteFile(failMarker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ytdlp := testkit.Executable(t, "yt-dlp", `#!/bin/sh
archive=""; result=""
while test "$#" -gt 0; do case "$1" in --download-archive) archive="$2"; shift 2 ;; --print-to-file) result="$3"; shift 3 ;; *) shift ;; esac; done
if test -s "$archive"; then exit 0; fi
stage=$(dirname "$result")
watch=$(dirname "$(dirname "$(dirname "$stage")")")
printf 'video bytes' > "$stage/download.mp4"
printf 'youtube owned-id\nyoutube unreported-id\n' > "$archive"
printf 'owned-id\t"%s"\n' "$stage/download.mp4" >> "$result"
if test -f "$watch/.fail-provider-archive-once"; then
  rm "$watch/.fail-provider-archive-once"
  mkdir "$watch/.yt-dlp-archive.txt"
fi
`)
	source := clipfetch.Source{ID: "youtube:missing-sidecar", AcquisitionID: "acq-missing-sidecar", Kind: clipfetch.YouTube, URL: "https://youtube.com/watch?v=owned-id"}
	firstStore, err := store.Open(t.Context(), dsn, true)
	if err != nil {
		t.Fatal(err)
	}
	seedArchiveRecoveryRun(t, firstStore, source)
	first := clipfetch.New(clipfetch.NewYtDlpDownloader(ytdlp, "ffmpeg"), nil, watch, discardLog()).WithArtifactWriter(firstStore).Run(t.Context(), []clipfetch.Source{source})
	if first.Failed != 1 {
		t.Fatalf("first acquisition = %+v, want retained missing-sidecar repair", first)
	}
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.Open(t.Context(), dsn, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	blocked, err := clipfetch.RecoverAcquisitionArtifacts(t.Context(), watch, watch, reopened, time.Now)
	if err == nil {
		t.Fatalf("blocked recovery = %+v, want provider archive replay failure", blocked)
	}
	artifact, found, lookupErr := reopened.AcquisitionArtifactForClip(t.Context(), "download.mp4", "")
	if lookupErr != nil || !found || artifact.ProviderArchiveCommitted || artifact.ProviderArchiveEntry != "youtube owned-id" || artifact.State != filler.ArtifactRepair {
		t.Fatalf("blocked artifact = %+v, found=%v, err=%v", artifact, found, lookupErr)
	}
	if _, statErr := os.Stat(filepath.Join(watch, filepath.FromSlash(artifact.StagingPath))); statErr != nil {
		t.Fatalf("missing-sidecar repair did not retain staged bytes: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(watch, artifact.MediaPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing-sidecar repair made media visible: %v", statErr)
	}
	if err := os.Remove(filepath.Join(watch, ".yt-dlp-archive.txt")); err != nil {
		t.Fatal(err)
	}

	result, err := clipfetch.RecoverAcquisitionArtifacts(t.Context(), watch, watch, reopened, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Published != 0 || result.Repair != 1 {
		t.Fatalf("recovery = %+v, want one held missing-provenance repair", result)
	}
	archive, err := os.ReadFile(filepath.Join(watch, ".yt-dlp-archive.txt"))
	if err != nil || string(archive) != "youtube owned-id\n" {
		t.Fatalf("replayed provider archive = %q, %v; want only exact owned output", archive, err)
	}
	if _, statErr := os.Stat(filepath.Join(watch, artifact.MediaPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("recovery published missing-provenance media: %v", statErr)
	}

	retry := source
	retry.AcquisitionID = "acq-after-missing-sidecar"
	seedArchiveRecoveryRun(t, reopened, retry)
	deduped := clipfetch.New(clipfetch.NewYtDlpDownloader(ytdlp, "ffmpeg"), nil, watch, discardLog()).WithArtifactWriter(reopened).Run(t.Context(), []clipfetch.Source{retry})
	if deduped.Empty != 1 || deduped.Fetched != 0 || len(deduped.Artifacts) != 0 {
		t.Fatalf("post-restart acquisition = %+v, want provider archive deduplication", deduped)
	}
}

func seedArchiveRecoveryRun(t *testing.T, db store.Store, source clipfetch.Source) {
	t.Helper()
	if err := db.UpsertAcquisitionRun(t.Context(), filler.AcquisitionRun{
		ID: source.AcquisitionID, SourceID: source.ID, Trigger: filler.AcquisitionPull,
		Status: filler.AcquisitionRunning, Requested: 1,
	}); err != nil {
		t.Fatal(err)
	}
}
