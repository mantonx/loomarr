package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/store"
)

type readinessStoreFuncs struct {
	pipeline func(context.Context, time.Time) (filler.PipelineOverview, error)
	runs     func(context.Context, int, time.Time) ([]filler.AcquisitionRun, error)
	repairs  func(context.Context) (filler.AcquisitionRepairSummary, error)
}

func (s readinessStoreFuncs) PipelineOverview(ctx context.Context, at time.Time) (filler.PipelineOverview, error) {
	return s.pipeline(ctx, at)
}

func (s readinessStoreFuncs) ListAcquisitionRuns(ctx context.Context, limit int, at time.Time) ([]filler.AcquisitionRun, error) {
	return s.runs(ctx, limit, at)
}

func (s readinessStoreFuncs) AcquisitionRepairSummary(ctx context.Context) (filler.AcquisitionRepairSummary, error) {
	return s.repairs(ctx)
}

func TestFillerReadinessComposesAuthoritativeServerFacts(t *testing.T) {
	poolAsked := false
	a := fillerServiceAdapter{
		readiness: readinessStoreFuncs{
			pipeline: func(context.Context, time.Time) (filler.PipelineOverview, error) {
				return filler.PipelineOverview{Rejected: 2, Recoverable: 2}, nil
			},
			runs: func(context.Context, int, time.Time) ([]filler.AcquisitionRun, error) { return nil, nil },
			repairs: func(context.Context) (filler.AcquisitionRepairSummary, error) {
				return filler.AcquisitionRepairSummary{}, nil
			},
		},
		pool: func(context.Context) (filler.PoolReport, error) {
			poolAsked = true
			return filler.PoolReport{Eligible: 12}, nil
		},
		now: func() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) },
	}

	got, err := a.Readiness(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// With no automatic fetcher configured, repairing acquisition is more important than the
	// rejected work behind it. This proves the adapter delegates the cross-domain priority rather
	// than exposing unrelated counters for a client to sort.
	if got.Next != filler.ReadinessEnableFetch {
		t.Fatalf("next = %q, want enable_fetch", got.Next)
	}
	if !poolAsked {
		t.Fatal("readiness did not include the live pool projection")
	}
}

func TestFillerReadinessRetainsOlderRepairBeyondHistoryPage(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "loomarr.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 21; i++ {
		at := now.Add(time.Duration(-21+i) * time.Minute)
		run := filler.AcquisitionRun{ID: "acq-" + string(rune('a'+i)), Trigger: filler.AcquisitionSource, SourceID: "source", Status: filler.AcquisitionSuccess, StartedAt: at, CompletedAt: at, UpdatedAt: at}
		if err := st.UpsertAcquisitionRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			artifact := filler.AcquisitionArtifact{ID: "repair-old", AcquisitionID: run.ID, SourceID: run.SourceID, Provider: "youtube", SourceURL: "https://youtube.example/old", MediaPath: "old.mp4", MediaSHA256: strings.Repeat("a", 64), MediaBytes: 1, State: filler.ArtifactRepair, RepairReason: "old repair remains actionable", CompletedAt: at, UpdatedAt: at}
			if err := st.UpsertAcquisitionArtifacts(ctx, []filler.AcquisitionArtifact{artifact}); err != nil {
				t.Fatal(err)
			}
		}
	}
	a := fillerServiceAdapter{readiness: st, pool: func(context.Context) (filler.PoolReport, error) { return filler.PoolReport{Eligible: 1}, nil }, now: func() time.Time { return now }}
	got, err := a.Readiness(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Runs) != 20 || got.Repairs.Count != 1 || got.Repairs.LatestReason != "old repair remains actionable" {
		t.Fatalf("readiness = %+v, want paged history and retained repair summary", got)
	}
}
