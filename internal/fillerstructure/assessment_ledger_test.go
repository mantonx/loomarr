package fillerstructure

import (
	"strings"
	"testing"
	"time"
)

func TestAssessmentReservationAndLedgerEntryBindSettlement(t *testing.T) {
	input := acceptedAssessmentInput()
	reservation, err := NewAssessmentReservation(AssessmentReservationInput{
		RequestSHA256: input.RequestSHA256, Source: input.Source, Media: input.Media,
		Assessor: input.Assessor, MetadataSnapshotSHA256: input.MetadataSnapshotSHA256,
		PromptSHA256: input.PromptSHA256, SchemaSHA256: input.SchemaSHA256,
		ExpectedResolvedModel: input.ResolvedModel,
		UpstreamProvider:      input.UpstreamProvider, UpstreamProviderSlug: input.UpstreamProviderSlug,
		RequestedNanoUSD: input.RequestedNanoUSD, MaximumChargeNanoUSD: input.RequestedNanoUSD,
		RequestedAt: input.AssessedAt.Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := NewAssessmentRecord(input)
	if err != nil {
		t.Fatal(err)
	}
	entry := AssessmentLedgerEntry{Reservation: reservation, State: AssessmentLedgerSettled, Record: &recorded.Record}
	if err := ValidateAssessmentLedgerEntry(entry); err != nil {
		t.Fatal(err)
	}

	mutations := map[string]func(*AssessmentLedgerEntry){
		"request":  func(entry *AssessmentLedgerEntry) { entry.Record.RequestSHA256 = strings.Repeat("0", 64) },
		"snapshot": func(entry *AssessmentLedgerEntry) { entry.Record.MetadataSnapshotSHA256 = strings.Repeat("1", 64) },
		"source":   func(entry *AssessmentLedgerEntry) { entry.Record.Source.DurationMS++ },
		"media":    func(entry *AssessmentLedgerEntry) { entry.Record.Media.SHA256 = strings.Repeat("9", 64) },
		"model":    func(entry *AssessmentLedgerEntry) { entry.Record.ResolvedModel = "another-model" },
		"time": func(entry *AssessmentLedgerEntry) {
			entry.Record.AssessedAt = reservation.RequestedAt.Add(-time.Second)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			drifted := entry
			record := *entry.Record
			drifted.Record = &record
			mutate(&drifted)
			drifted.Record.SHA256 = AssessmentRecordSHA256(*drifted.Record)
			if err := ValidateAssessmentLedgerEntry(drifted); err == nil {
				t.Fatal("expected drifted settlement to fail")
			}
		})
	}
}

func TestAssessmentLedgerOpenStatesCannotContainARecord(t *testing.T) {
	input := acceptedAssessmentInput()
	reservation, err := NewAssessmentReservation(AssessmentReservationInput{
		RequestSHA256: input.RequestSHA256, Source: input.Source, Media: input.Media,
		Assessor: input.Assessor, MetadataSnapshotSHA256: input.MetadataSnapshotSHA256,
		PromptSHA256: input.PromptSHA256, SchemaSHA256: input.SchemaSHA256,
		ExpectedResolvedModel: input.ResolvedModel,
		UpstreamProvider:      input.UpstreamProvider, UpstreamProviderSlug: input.UpstreamProviderSlug,
		RequestedNanoUSD: input.RequestedNanoUSD, MaximumChargeNanoUSD: input.RequestedNanoUSD,
		RequestedAt: input.AssessedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []AssessmentLedgerState{AssessmentLedgerOpen, AssessmentLedgerHeldBudget} {
		if err := ValidateAssessmentLedgerEntry(AssessmentLedgerEntry{Reservation: reservation, State: state}); err != nil {
			t.Fatalf("state %q: %v", state, err)
		}
	}
}
