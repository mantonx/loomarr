package filler_test

import (
	"testing"

	"github.com/loomarr/loomarr/internal/filler"
)

func TestProjectReadinessPrioritisesTheNextOperatorAction(t *testing.T) {
	t.Parallel()
	base := filler.ReadinessInput{
		Fetch: filler.FetchStatus{Enabled: true},
		Pool: filler.PoolReport{
			Eligible: 10,
			Channels: []filler.ChannelCoverage{{
				ChannelID: "ch-1", Report: filler.CoverageReport{Level: filler.MatchExact, Total: 10},
			}},
		},
	}
	tests := []struct {
		name string
		edit func(*filler.ReadinessInput)
		want filler.ReadinessAction
	}{
		{"ready", func(*filler.ReadinessInput) {}, filler.ReadinessNone},
		{"fetch disabled", func(in *filler.ReadinessInput) { in.Fetch.Enabled = false }, filler.ReadinessEnableFetch},
		{"catalog ceiling before failed work", func(in *filler.ReadinessInput) {
			in.Fetch.StoppedBy, in.Pipeline.Recoverable = "catalog", 2
		}, filler.ReadinessFreeCatalog},
		{"disk ceiling", func(in *filler.ReadinessInput) { in.Fetch.StoppedBy = "disk" }, filler.ReadinessFreeDisk},
		{"latest acquisition failed", func(in *filler.ReadinessInput) {
			in.Runs = []filler.AcquisitionRun{{Status: filler.AcquisitionError, Failed: 2}}
		}, filler.ReadinessRetryAcquisition},
		{"failed work before review", func(in *filler.ReadinessInput) {
			in.Pipeline.Rejected, in.Pipeline.Recoverable, in.Pipeline.NeedsDecision = 4, 2, 3
		}, filler.ReadinessRetryWork},
		{"terminal refusal is audit only", func(in *filler.ReadinessInput) {
			in.Pipeline.Rejected = 4
		}, filler.ReadinessNone},
		{"review before pool quality", func(in *filler.ReadinessInput) {
			in.Pipeline.NeedsDecision = 3
			in.Pool.Channels[0].Report.Level = filler.MatchAudience
		}, filler.ReadinessReviewIncoming},
		{"empty eligible pool", func(in *filler.ReadinessInput) { in.Pool.Eligible = 0 }, filler.ReadinessAddFiller},
		{"weakest channel", func(in *filler.ReadinessInput) {
			in.Pool.Channels[0].Report.Level = filler.MatchAudience
		}, filler.ReadinessImproveCoverage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			in.Pool.Channels = append([]filler.ChannelCoverage(nil), base.Pool.Channels...)
			tt.edit(&in)
			got := filler.ProjectReadiness(in)
			if got.Next != tt.want {
				t.Fatalf("next = %q, want %q (%+v)", got.Next, tt.want, got)
			}
			if (tt.want == filler.ReadinessNone) != got.Ready {
				t.Fatalf("ready = %v for %q", got.Ready, tt.want)
			}
		})
	}
}

func TestProjectReadinessOutstandingRepairsOutrankRecentSuccessfulHistory(t *testing.T) {
	got := filler.ProjectReadiness(filler.ReadinessInput{
		Fetch: filler.FetchStatus{Enabled: true},
		Pool:  filler.PoolReport{Eligible: 1},
		Runs:  []filler.AcquisitionRun{{Status: filler.AcquisitionSuccess}},
		Repairs: filler.AcquisitionRepairSummary{
			Count: 2, LatestReason: "latest retained repair",
		},
	})
	if got.Next != filler.ReadinessRetryAcquisition || got.Count != 2 {
		t.Fatalf("readiness = %+v, want retained repairs to be actionable", got)
	}
	if got.Repairs.Count != 2 || got.Repairs.LatestReason != "latest retained repair" {
		t.Fatalf("repair summary = %+v", got.Repairs)
	}
}
