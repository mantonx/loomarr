package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

func testFillerSpokenSafetyExecutionPort(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	at := time.Date(2026, 9, 2, 15, 0, 0, 0, time.UTC)
	hash := strings.Repeat("a", 64)
	run := fillersafety.LedgerRun{
		ID: "execution-port-run", ClipHash: "execution-port-clip",
		AuthoritySHA256: hash, SourceSHA256: hash, SourceBytes: 4096, DurationMS: 10_000,
		CertificationSHA256: hash, PolicySHA256: hash, ProposerSHA256: hash,
		Implementation: "spoken-safety-v1", CreatedAt: at,
	}
	created, err := s.BeginSpokenSafetyRun(ctx, run)
	if err != nil || !created {
		t.Fatalf("begin run=%t, %v", created, err)
	}
	created, err = s.BeginSpokenSafetyRun(ctx, run)
	if err != nil || created {
		t.Fatalf("idempotent begin=%t, %v", created, err)
	}
	candidate := fillersafety.Candidate{ID: "execution-port-candidate", StartMS: 100, EndMS: 500}
	plan := fillersafety.LedgerEvent{
		ID: "execution-port-plan", RunID: run.ID, Ordinal: 0,
		Kind: fillersafety.LedgerSourcePlanned, CreatedAt: at.Add(time.Nanosecond),
		Source: &fillersafety.SourcePlanned{
			Audio: fillersafety.Span{EndMS: run.DurationMS}, Video: fillersafety.Span{EndMS: run.DurationMS},
		},
	}
	proposal := fillersafety.LedgerEvent{
		ID: "execution-port-proposal", RunID: run.ID, Ordinal: 1,
		Kind: fillersafety.LedgerProposalCompleted, CreatedAt: at.Add(2 * time.Nanosecond),
		Proposal: &fillersafety.ProposalCompleted{
			State: fillersafety.ProposalComplete, ProposerSHA256: hash,
			Candidates: []fillersafety.Candidate{candidate},
		},
	}
	for _, event := range []fillersafety.LedgerEvent{plan, proposal} {
		if err := s.AppendSpokenSafetyEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	reserveAt := at.Add(3 * time.Nanosecond)
	reservation := fillersafety.HostedCallReservation{
		EventID: "execution-port-reserve", RunID: run.ID, EvaluationID: "execution-port-inference",
		ClipHash: run.ClipHash, CandidateID: candidate.ID, RequestSHA256: hash,
		Role: "spoken-safety", Rung: "native-audio", RequestedProvider: "openrouter",
		RequestedModel: "vendor/model", UpstreamProvider: "provider", Modalities: []string{"audio"},
		DerivativeBytes: 2048, DerivativeDurationMS: 2400, RequestedNanoUSD: 100,
		Budget: fillersafety.HostedCallBudget{PerClipNanoUSD: 1000, PerDayNanoUSD: 1000, PerRunNanoUSD: 1000},
		Versions: fillersafety.HostedCallVersions{
			EvidenceSHA256: hash, ExtractorSHA256: hash, PromptSHA256: hash, SchemaSHA256: hash,
			TaxonomySHA256: hash, CertificationSHA256: hash, PolicySHA256: hash, CapabilitySHA256: hash,
		},
		Ordinal: 2, CreatedAt: reserveAt,
	}
	reserveEvent, err := s.ReserveSpokenSafetyCall(ctx, reservation)
	if err != nil || reserveEvent.Reserve == nil || reserveEvent.Reserve.State != fillersafety.ReservationAccepted {
		t.Fatalf("reserve event=%+v, %v", reserveEvent, err)
	}
	stored, err := s.GetInferenceEvaluation(ctx, reservation.EvaluationID)
	if err != nil || stored.Versions.Evidence != hash || stored.Versions.AdmissionPolicy != hash ||
		stored.DerivativeBytes != reservation.DerivativeBytes || stored.ReservedNanoUSD != reservation.RequestedNanoUSD {
		t.Fatalf("mapped reservation=%+v, %v", stored, err)
	}
	settleAt := at.Add(4 * time.Nanosecond)
	settlement := fillersafety.HostedCallSettlement{
		EventID: "execution-port-settle", RunID: run.ID, ReservationEventID: reserveEvent.ID,
		ResponseSHA256: strings.Repeat("b", 64), ResolvedProvider: "openrouter",
		ResolvedModel: "vendor/model-2026", UpstreamProvider: "provider", GenerationID: "generation-1",
		Outcome: string(fillersafety.AudioAbsent), ChargedAmountUSD: "0.00000005",
		ChargedNanoUSD: 50, ChargeKnown: true, PromptTokens: 10, CompletionTokens: 2,
		Ordinal: 3, CreatedAt: settleAt,
	}
	settleEvent, err := s.SettleSpokenSafetyCall(ctx, settlement)
	if err != nil || settleEvent.Settle == nil || settleEvent.Settle.State != fillersafety.SettlementCompleted ||
		settleEvent.Settle.Outcome != string(fillersafety.AudioAbsent) {
		t.Fatalf("settle event=%+v, %v", settleEvent, err)
	}
	stored, err = s.GetInferenceEvaluation(ctx, reservation.EvaluationID)
	if err != nil || stored.State != InferenceCompleted || stored.ChargedNanoUSD != 50 ||
		stored.FailureReason != "" || stored.Outcome != string(fillersafety.AudioAbsent) {
		t.Fatalf("mapped settlement=%+v, %v", stored, err)
	}
}
