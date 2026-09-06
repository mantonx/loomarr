package filler_test

import (
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
)

func TestAcquisitionArtifactValidate_BindsExactWatchRelativeBytes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	valid := filler.AcquisitionArtifact{
		ID: "artifact-1", AcquisitionID: "acq-1", SourceID: "archive:classic",
		Provider: "archive", SourceURL: "https://archive.org/details/one",
		StagingPath: ".loomarr-acquisitions/acq-1/one.mp4", MediaPath: "one.mp4",
		SidecarPath: "one.info.json", MediaSHA256: strings.Repeat("a", 64), MediaBytes: 42,
		State: filler.ArtifactStaged, CompletedAt: now, UpdatedAt: now,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid manifest: %v", err)
	}

	for name, mutate := range map[string]func(*filler.AcquisitionArtifact){
		"absolute media":   func(a *filler.AcquisitionArtifact) { a.MediaPath = "/tmp/one.mp4" },
		"escaping staging": func(a *filler.AcquisitionArtifact) { a.StagingPath = "../one.mp4" },
		"missing digest":   func(a *filler.AcquisitionArtifact) { a.MediaSHA256 = "" },
		"zero bytes":       func(a *filler.AcquisitionArtifact) { a.MediaBytes = 0 },
		"repair without reason": func(a *filler.AcquisitionArtifact) {
			a.State, a.RepairReason = filler.ArtifactRepair, ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid acquisition artifact passed validation")
			}
		})
	}
}

func TestAcquisitionArtifactOutcomeFrom_ExposesBoundedRepairReason(t *testing.T) {
	artifacts := []filler.AcquisitionArtifact{
		{State: filler.ArtifactRepair, RepairReason: "newest actionable reason"},
		{State: filler.ArtifactConsumed},
		{State: filler.ArtifactRepair, RepairReason: "older reason"},
	}
	got := filler.AcquisitionArtifactOutcomeFrom(artifacts)
	if got.Repair != 2 || got.Consumed != 1 || got.RepairReason != "newest actionable reason" {
		t.Fatalf("outcome = %+v", got)
	}
}
