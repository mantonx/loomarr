package fillerstructurewindow

import (
	"testing"
	"time"
)

func TestCallReservationAndLedgerEntryBindExactWindowSettlement(t *testing.T) {
	set := callRecordMediaSetFixture(t)
	callInput := acceptedCallRecordInput(set)
	recorded, err := NewRecordedAssessment(callInput)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := NewCallReservation(CallReservationInput{
		RequestSHA256: callInput.RequestSHA256, MediaSet: set, WindowOrdinal: callInput.WindowOrdinal,
		Assessor: callInput.Assessor, MetadataSnapshotSHA256: callInput.MetadataSnapshotSHA256,
		PromptSHA256: callInput.PromptSHA256, SchemaSHA256: callInput.SchemaSHA256,
		ExpectedResolvedModel: "resolved-model", UpstreamProvider: callInput.UpstreamProvider,
		UpstreamProviderSlug: callInput.UpstreamProviderSlug, RequestedNanoUSD: callInput.RequestedNanoUSD,
		MaximumChargeNanoUSD: 1_500, RequestedAt: callInput.AssessedAt.Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := CallLedgerEntry{Reservation: reservation, State: CallLedgerSettled, Record: &recorded.Record}
	if err := ValidateCallLedgerEntry(entry); err != nil {
		t.Fatal(err)
	}
	drifted := entry
	record := *entry.Record
	drifted.Record = &record
	drifted.Record.MetadataSnapshotSHA256 = "1111111111111111111111111111111111111111111111111111111111111111"
	drifted.Record.SHA256 = CallRecordSHA256(*drifted.Record)
	if err := ValidateCallLedgerEntry(drifted); err == nil {
		t.Fatal("settlement from another metadata snapshot was accepted")
	}
	entry.Record.WindowOrdinal++
	entry.Record.SHA256 = CallRecordSHA256(*entry.Record)
	if err := ValidateCallLedgerEntry(entry); err == nil {
		t.Fatal("settlement for another window was accepted")
	}
}

func TestCallReservationRejectsDriftedMediaSet(t *testing.T) {
	set := callRecordMediaSetFixture(t)
	input := acceptedCallRecordInput(set)
	reservation, err := NewCallReservation(CallReservationInput{
		RequestSHA256: input.RequestSHA256, MediaSet: set, WindowOrdinal: input.WindowOrdinal,
		Assessor: input.Assessor, MetadataSnapshotSHA256: input.MetadataSnapshotSHA256,
		PromptSHA256: input.PromptSHA256, SchemaSHA256: input.SchemaSHA256,
		ExpectedResolvedModel: "resolved-model", UpstreamProvider: input.UpstreamProvider,
		UpstreamProviderSlug: input.UpstreamProviderSlug, RequestedNanoUSD: input.RequestedNanoUSD,
		MaximumChargeNanoUSD: 1_500, RequestedAt: input.AssessedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	reservation.MediaSet.Windows[0].Media.Bytes++
	reservation.SHA256 = CallReservationSHA256(reservation)
	if err := ValidateCallReservation(reservation); err == nil {
		t.Fatal("reservation with drifted media authority was accepted")
	}
}
