package fillerreview

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

type temporalStructureWindowNoopLedger struct{}

func (temporalStructureWindowNoopLedger) Reserve(context.Context, fillerstructurewindow.CallReservation) (fillerstructurewindow.CallReservationState, error) {
	return fillerstructurewindow.CallReservationAccepted, nil
}

func (temporalStructureWindowNoopLedger) Settle(context.Context, fillerstructurewindow.CallRecord) error {
	return nil
}

func TestNewTemporalStructureWindowOpenRouterFamilyPinsSnapshotPriceAndProductionRuntime(t *testing.T) {
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	config := temporalStructureWindowOpenRouterFamilyTestConfig(t, now)
	family, err := NewTemporalStructureWindowOpenRouterFamily(config)
	if err != nil {
		t.Fatal(err)
	}
	if family.Runtime == nil || family.Profile != family.Runtime.Profile() ||
		family.Profile.PromptVersion != fillerstructurewindow.DirectVideoPromptVersion ||
		family.Profile.EvidenceContract != fillerstructurewindow.CallRecordContractVersion ||
		family.EstimatedMaximumChargeNanoUSD != 3_048_000 {
		t.Fatalf("family=%+v", family)
	}
}

func TestNewTemporalStructureWindowOpenRouterFamilyRejectsStaleOrUnderReservedRoute(t *testing.T) {
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	stale := temporalStructureWindowOpenRouterFamilyTestConfig(t, now)
	stale.Snapshot.RetrievedAt = now.Add(-25 * time.Hour)
	if _, err := NewTemporalStructureWindowOpenRouterFamily(stale); err == nil || !strings.Contains(err.Error(), "24-hour") {
		t.Fatalf("stale error=%v", err)
	}
	under := temporalStructureWindowOpenRouterFamilyTestConfig(t, now)
	under.ReservationNanoUSD = 3_047_999
	if _, err := NewTemporalStructureWindowOpenRouterFamily(under); err == nil || !strings.Contains(err.Error(), "below the snapshot price bound") {
		t.Fatalf("reservation error=%v", err)
	}
}

func temporalStructureWindowOpenRouterFamilyTestConfig(t *testing.T, now time.Time) TemporalStructureWindowOpenRouterFamilyConfig {
	t.Helper()
	snapshot := openRouterReviewSnapshot("http://127.0.0.1:8081/api/v1", now)
	snapshot.Models[0].InputModalities = []string{"text", "video"}
	return TemporalStructureWindowOpenRouterFamilyConfig{
		BaseURL: snapshot.SourceBaseURL, APIKey: "test-key", Snapshot: snapshot,
		Model: "review/vendor-model", ModelFamily: "family-a", UpstreamProvider: "Provider Route",
		UpstreamProviderSlug: "provider/route", AssessorID: "assessor-a",
		ReasoningMode: TemporalStructureOpenRouterReasoningDisabled, ReservationNanoUSD: 3_000_000_000,
		MaximumInputTokens: 1_000, EvidenceRoot: t.TempDir(), Ledger: temporalStructureWindowNoopLedger{},
		AllowInsecureTestURL: true, Now: func() time.Time { return now },
	}
}
