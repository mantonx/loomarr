package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func testFillerStructureWindowCallLedger(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	at := time.Date(2026, time.September, 12, 6, 0, 0, 0, time.UTC)
	budget := InferenceBudget{PerClipNanoUSD: 200, PerDayNanoUSD: 2_000}

	acceptedReservation := structureWindowCallReservationFixture(t, "1", "a", at)
	state, err := s.ReserveStructureWindowCall(ctx, acceptedReservation, budget)
	if err != nil || state != fillerstructurewindow.CallReservationAccepted {
		t.Fatalf("accepted reservation state=%q error=%v", state, err)
	}
	open, err := s.GetStructureWindowCallLedgerEntry(ctx, acceptedReservation.RequestSHA256)
	if err != nil || open.State != fillerstructurewindow.CallLedgerOpen || open.Record != nil {
		t.Fatalf("open ledger entry=%+v error=%v", open, err)
	}
	if _, err := s.ReserveStructureWindowCall(ctx, acceptedReservation, budget); !errors.Is(err, fillerstructurewindow.ErrCallLedgerConflict) {
		t.Fatalf("duplicate reservation error=%v", err)
	}
	accepted := structureWindowCallRecordFixture(t, acceptedReservation, fillerstructure.AssessmentRecordAccepted, 40)
	if err := s.SettleStructureWindowCall(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleStructureWindowCall(ctx, accepted); err != nil {
		t.Fatalf("idempotent settlement: %v", err)
	}
	closed, err := s.GetStructureWindowCallLedgerEntry(ctx, acceptedReservation.RequestSHA256)
	if err != nil || closed.State != fillerstructurewindow.CallLedgerSettled || closed.Record == nil || closed.Record.SHA256 != accepted.SHA256 {
		t.Fatalf("closed ledger entry=%+v error=%v", closed, err)
	}
	drifted := accepted
	drifted.AssessedAt = drifted.AssessedAt.Add(time.Second)
	drifted.SHA256 = fillerstructurewindow.CallRecordSHA256(drifted)
	if err := s.SettleStructureWindowCall(ctx, drifted); !errors.Is(err, fillerstructurewindow.ErrCallLedgerConflict) {
		t.Fatalf("drifted settlement error=%v", err)
	}
	evaluation, err := s.GetInferenceEvaluation(ctx, "structure-window-"+acceptedReservation.RequestSHA256)
	if err != nil || evaluation.State != InferenceCompleted || evaluation.ReservedNanoUSD != 40 || evaluation.Outcome != "window_complete" ||
		evaluation.Versions.Evidence != acceptedReservation.MediaSet.SHA256 {
		t.Fatalf("shared accepted accounting=%+v error=%v", evaluation, err)
	}

	unsettledReservation := structureWindowCallReservationFixture(t, "2", "b", at.Add(time.Minute))
	if state, err = s.ReserveStructureWindowCall(ctx, unsettledReservation, budget); err != nil || state != fillerstructurewindow.CallReservationAccepted {
		t.Fatalf("unsettled reservation state=%q error=%v", state, err)
	}
	unsettled := structureWindowCallRecordFixture(t, unsettledReservation, fillerstructure.AssessmentRecordUnsettled, 0)
	if err := s.SettleStructureWindowCall(ctx, unsettled); err != nil {
		t.Fatal(err)
	}
	evaluation, err = s.GetInferenceEvaluation(ctx, "structure-window-"+unsettledReservation.RequestSHA256)
	if err != nil || evaluation.State != InferenceFailed || evaluation.ReservedNanoUSD != unsettledReservation.RequestedNanoUSD {
		t.Fatalf("shared unsettled accounting=%+v error=%v", evaluation, err)
	}

	heldReservation := structureWindowCallReservationFixture(t, "3", "c", at.Add(2*time.Minute))
	heldBudget := InferenceBudget{PerClipNanoUSD: 50, PerDayNanoUSD: 2_000}
	if state, err = s.ReserveStructureWindowCall(ctx, heldReservation, heldBudget); err != nil || state != fillerstructurewindow.CallReservationHeldBudget {
		t.Fatalf("held reservation state=%q error=%v", state, err)
	}
	entries, err := s.ListOpenStructureWindowCallLedgerEntries(ctx, 10)
	if err != nil || len(entries) != 1 || entries[0].State != fillerstructurewindow.CallLedgerHeldBudget {
		t.Fatalf("open ledger entries=%+v error=%v", entries, err)
	}
	held := structureWindowCallRecordFixture(t, heldReservation, fillerstructure.AssessmentRecordHeldBudget, 0)
	if err := s.SettleStructureWindowCall(ctx, held); err != nil {
		t.Fatal(err)
	}

	overReservation := structureWindowCallReservationFixture(t, "4", "d", at.Add(3*time.Minute))
	if state, err = s.ReserveStructureWindowCall(ctx, overReservation, budget); err != nil || state != fillerstructurewindow.CallReservationAccepted {
		t.Fatalf("over reservation state=%q error=%v", state, err)
	}
	over := structureWindowCallRecordFixture(t, overReservation, fillerstructure.AssessmentRecordOverReservation, 120)
	if err := s.SettleStructureWindowCall(ctx, over); err != nil {
		t.Fatal(err)
	}
	evaluation, err = s.GetInferenceEvaluation(ctx, "structure-window-"+overReservation.RequestSHA256)
	if err != nil || evaluation.State != InferenceHeldBudget || evaluation.ChargedNanoUSD != 120 || evaluation.ReservedNanoUSD != 120 {
		t.Fatalf("shared over-reservation accounting=%+v error=%v", evaluation, err)
	}
	entries, err = s.ListOpenStructureWindowCallLedgerEntries(ctx, 10)
	if err != nil || len(entries) != 0 {
		t.Fatalf("remaining open ledger entries=%+v error=%v", entries, err)
	}
}

func structureWindowCallReservationFixture(t *testing.T, requestDigit, sourceDigit string, at time.Time) fillerstructurewindow.CallReservation {
	t.Helper()
	source := fillerstructure.Source{SHA256: strings.Repeat(sourceDigit, 64), Bytes: 8_192, DurationMS: 10_000}
	plan, err := fillerstructurewindow.NewPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	set, err := fillerstructurewindow.NewMediaSet(plan, []fillerstructure.AssessmentMedia{{
		SHA256: strings.Repeat("8", 64), Bytes: 4_096, DurationMS: 10_000,
		ProfileSHA256: plan.Profile.AssessmentMediaProfileSHA256, LineageSHA256: strings.Repeat("7", 64),
	}})
	if err != nil {
		t.Fatal(err)
	}
	profile := fillerstructure.AssessorProfile{
		ID: "window-assessor-" + requestDigit, ModelFamily: "family-" + requestDigit,
		Provider: "openrouter", Model: "requested-model-" + requestDigit,
		ModelDigest: strings.Repeat("a", 64), CapabilitySHA256: strings.Repeat("b", 64),
		PromptVersion:    fillerstructurewindow.DirectVideoPromptVersion,
		EvidenceContract: fillerstructurewindow.CallRecordContractVersion,
	}
	reservation, err := fillerstructurewindow.NewCallReservation(fillerstructurewindow.CallReservationInput{
		RequestSHA256: strings.Repeat(requestDigit, 64), MediaSet: set, WindowOrdinal: 0, Assessor: profile,
		MetadataSnapshotSHA256: strings.Repeat("6", 64),
		PromptSHA256:           fillerstructurewindow.DirectVideoPromptSHA256(10_000),
		SchemaSHA256:           fillerstructurewindow.DirectVideoSchemaSHA256(10_000),
		ExpectedResolvedModel:  "resolved-model-" + requestDigit, UpstreamProvider: "Provider",
		UpstreamProviderSlug: "provider", RequestedNanoUSD: 100, MaximumChargeNanoUSD: 80, RequestedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	return reservation
}

func structureWindowCallRecordFixture(t *testing.T, reservation fillerstructurewindow.CallReservation, state fillerstructure.AssessmentRecordState, charge int64) fillerstructurewindow.CallRecord {
	t.Helper()
	input := fillerstructurewindow.CallRecordInput{
		MediaSet: reservation.MediaSet, WindowOrdinal: reservation.WindowOrdinal, Assessor: reservation.Assessor,
		MetadataSnapshotSHA256: reservation.MetadataSnapshotSHA256,
		PromptSHA256:           reservation.PromptSHA256, SchemaSHA256: reservation.SchemaSHA256,
		RequestSHA256: reservation.RequestSHA256, UpstreamProvider: reservation.UpstreamProvider,
		UpstreamProviderSlug: reservation.UpstreamProviderSlug, RequestedNanoUSD: reservation.RequestedNanoUSD,
		AssessedAt: reservation.RequestedAt.Add(time.Second), State: state,
	}
	switch state {
	case fillerstructure.AssessmentRecordAccepted:
		input.RawResponse = []byte(`{"id":"generation"}`)
		input.StructuredOutput = `{"segments":[{"endMs":10000,"role":"commercial","decisiveAtMs":[1000],"reason":"offer"}]}`
		input.ResolvedProvider, input.ResolvedModel, input.GenerationID = "openrouter", reservation.ExpectedResolvedModel, "generation"
		input.ReservedNanoUSD, input.ChargeKnown = reservation.RequestedNanoUSD, true
		input.ChargedAmountUSD, input.ChargedNanoUSD, input.AccountedNanoUSD = "0.00000004", charge, charge
	case fillerstructure.AssessmentRecordUnsettled:
		input.Failure = fillerstructure.AssessmentFailureTransport
		input.ReservedNanoUSD, input.AccountedNanoUSD = reservation.RequestedNanoUSD, reservation.RequestedNanoUSD
	case fillerstructure.AssessmentRecordHeldBudget:
		input.Failure = fillerstructure.AssessmentFailureBudget
	case fillerstructure.AssessmentRecordOverReservation:
		input.Failure, input.RawResponse = fillerstructure.AssessmentFailureOverReservation, []byte(`{"id":"generation"}`)
		input.GenerationID, input.ReservedNanoUSD, input.ChargeKnown = "generation", reservation.RequestedNanoUSD, true
		input.ChargedAmountUSD, input.ChargedNanoUSD, input.AccountedNanoUSD = "0.00000012", charge, charge
	}
	recorded, err := fillerstructurewindow.NewRecordedAssessment(input)
	if err != nil {
		t.Fatal(err)
	}
	return recorded.Record
}
