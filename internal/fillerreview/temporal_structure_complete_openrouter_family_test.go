package fillerreview

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

type temporalStructureCompleteNoopLedger struct{}

func (temporalStructureCompleteNoopLedger) Reserve(context.Context, fillerstructure.AssessmentReservation) (fillerstructure.AssessmentReservationState, error) {
	return fillerstructure.AssessmentReservationAccepted, nil
}

func (temporalStructureCompleteNoopLedger) Settle(context.Context, fillerstructure.AssessmentRecord) error {
	return nil
}

func TestNewTemporalStructureCompleteOpenRouterFamilyPinsSnapshotPriceAndProductionRuntime(t *testing.T) {
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	config := temporalStructureCompleteOpenRouterFamilyTestConfig(t, now)
	family, err := NewTemporalStructureCompleteOpenRouterFamily(config)
	if err != nil {
		t.Fatal(err)
	}
	if family.Runtime == nil || family.Profile != family.Runtime.Profile() ||
		family.Profile.PromptVersion != fillerstructure.DirectVideoPromptVersion ||
		family.Profile.EvidenceContract != fillerstructure.AssessmentRecordContractVersion ||
		family.EstimatedMaximumChargeNanoUSD != 3_048_000 {
		t.Fatalf("family=%+v", family)
	}
}

func TestNewTemporalStructureCompleteOpenRouterFamilyRejectsStaleOrUnderReservedRoute(t *testing.T) {
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	stale := temporalStructureCompleteOpenRouterFamilyTestConfig(t, now)
	stale.Snapshot.RetrievedAt = now.Add(-25 * time.Hour)
	if _, err := NewTemporalStructureCompleteOpenRouterFamily(stale); err == nil || !strings.Contains(err.Error(), "24-hour") {
		t.Fatalf("stale error=%v", err)
	}
	under := temporalStructureCompleteOpenRouterFamilyTestConfig(t, now)
	under.ReservationNanoUSD = 3_047_999
	if _, err := NewTemporalStructureCompleteOpenRouterFamily(under); err == nil || !strings.Contains(err.Error(), "below the snapshot price bound") {
		t.Fatalf("reservation error=%v", err)
	}
}

func temporalStructureCompleteOpenRouterFamilyTestConfig(t *testing.T, now time.Time) TemporalStructureCompleteOpenRouterFamilyConfig {
	t.Helper()
	snapshot := openRouterReviewSnapshot("http://127.0.0.1:8081/api/v1", now)
	snapshot.Models[0].InputModalities = []string{"text", "video"}
	return TemporalStructureCompleteOpenRouterFamilyConfig{
		BaseURL: snapshot.SourceBaseURL, APIKey: "test-key", Snapshot: snapshot,
		Model: "review/vendor-model", ModelFamily: "family-a", UpstreamProvider: "Provider Route",
		UpstreamProviderSlug: "provider/route", AssessorID: "assessor-a",
		ReasoningMode: TemporalStructureOpenRouterReasoningDisabled, ReservationNanoUSD: 3_000_000_000,
		MaximumInputTokens: 1_000, EvidenceRoot: t.TempDir(), Ledger: temporalStructureCompleteNoopLedger{},
		AllowInsecureTestURL: true, Now: func() time.Time { return now },
	}
}
