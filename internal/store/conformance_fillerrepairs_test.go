package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
)

func testFillerAcquisitionRepairSummary(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_900_200_000, 0).UTC()
	empty, err := s.AcquisitionRepairSummary(ctx)
	if err != nil || empty.Count != 0 || empty.LatestReason != "" {
		t.Fatalf("empty repair summary = %+v, %v", empty, err)
	}
	for i, id := range []string{"run-old", "run-new"} {
		at := now.Add(time.Duration(i) * time.Minute)
		if err := s.UpsertAcquisitionRun(ctx, filler.AcquisitionRun{ID: id, Trigger: filler.AcquisitionSource, SourceID: "source", Status: filler.AcquisitionSuccess, StartedAt: at, CompletedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	longReason := strings.Repeat("界", 513)
	artifacts := []filler.AcquisitionArtifact{
		{ID: "repair-a", AcquisitionID: "run-old", SourceID: "source", Provider: "youtube", SourceURL: "https://youtube.example/a", MediaPath: "a.mp4", MediaSHA256: strings.Repeat("a", 64), MediaBytes: 1, State: filler.ArtifactRepair, RepairReason: "older", CompletedAt: now, UpdatedAt: now},
		{ID: "repair-z", AcquisitionID: "run-old", SourceID: "source", Provider: "youtube", SourceURL: "https://youtube.example/z", MediaPath: "z.mp4", MediaSHA256: strings.Repeat("b", 64), MediaBytes: 1, State: filler.ArtifactRepair, RepairReason: longReason, CompletedAt: now, UpdatedAt: now},
	}
	if err := s.UpsertAcquisitionArtifacts(ctx, artifacts); err != nil {
		t.Fatal(err)
	}
	history, err := s.ListAcquisitionRuns(ctx, 1, now)
	if err != nil || len(history) != 1 || history[0].ID != "run-new" {
		t.Fatalf("bounded history = %+v, %v; want newest successful run only", history, err)
	}
	summary, err := s.AcquisitionRepairSummary(ctx)
	if err != nil || summary.Count != 2 || summary.LatestReason != strings.Repeat("界", 512) {
		t.Fatalf("repair summary = %+v, %v", summary, err)
	}
	stored, err := s.ListRecoverableAcquisitionArtifacts(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var storedReason string
	for _, artifact := range stored {
		if artifact.ID == "repair-z" {
			storedReason = artifact.RepairReason
		}
	}
	if len(stored) != 2 || storedReason != longReason {
		t.Fatalf("stored repair reason changed: got %d runes, want %d", len([]rune(storedReason)), len([]rune(longReason)))
	}
	artifacts[1].State, artifacts[1].RepairReason, artifacts[1].UpdatedAt = filler.ArtifactConsumed, "", now.Add(time.Minute)
	if err := s.UpsertAcquisitionArtifacts(ctx, artifacts[1:]); err != nil {
		t.Fatal(err)
	}
	summary, err = s.AcquisitionRepairSummary(ctx)
	if err != nil || summary.Count != 1 || summary.LatestReason != "older" {
		t.Fatalf("repair summary after repair = %+v, %v", summary, err)
	}
}
