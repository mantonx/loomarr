package app

import (
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestFetchStoreAdapter_ListAcquisitionRemoteStatesMapsArtifactLifecycle(t *testing.T) {
	st := testkit.MigratedSQLiteStore(t)
	now := time.Unix(1_900_000_000, 0).UTC()
	run := filler.AcquisitionRun{ID: "identity-roundtrip", Trigger: filler.AcquisitionSource, SourceID: "archive:classic", Status: filler.AcquisitionRunning, Requested: 1, StartedAt: now, UpdatedAt: now}
	if err := st.UpsertAcquisitionRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	artifacts := []filler.AcquisitionArtifact{
		{ID: "staged-artifact", AcquisitionID: run.ID, SourceID: run.SourceID, Provider: "archive", RemoteID: "remote-Staged", SourceURL: "https://archive.org/details/remote-Staged", StagingPath: "stage/staged.mp4", MediaPath: "staged.mp4", MediaSHA256: strings.Repeat("a", 64), MediaBytes: 1, State: filler.ArtifactStaged, CompletedAt: now, UpdatedAt: now},
		{ID: "consumed-artifact", AcquisitionID: run.ID, SourceID: run.SourceID, Provider: "archive", RemoteID: "remote-Consumed", SourceURL: "https://archive.org/details/remote-Consumed", StagingPath: "stage/consumed.mp4", MediaPath: "consumed.mp4", MediaSHA256: strings.Repeat("b", 64), MediaBytes: 1, State: filler.ArtifactConsumed, CompletedAt: now, UpdatedAt: now},
	}
	if err := st.UpsertAcquisitionArtifacts(t.Context(), artifacts); err != nil {
		t.Fatal(err)
	}
	states, err := (fetchStoreAdapter{st: st}).ListAcquisitionRemoteStates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   string
		want filler.ExistingRemoteState
	}{{"remote-Staged", filler.RemoteQueued}, {"remote-Consumed", filler.RemoteCatalogued}} {
		key := (filler.RemoteIdentity{Provider: "archive", SourceID: run.SourceID, RemoteID: tc.id}).Key()
		if states[key] != tc.want {
			t.Fatalf("state[%q] = %q, want %q", key, states[key], tc.want)
		}
	}
}
