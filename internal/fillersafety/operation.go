package fillersafety

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/openroutermedia"
)

const evaluationImplementation = "spoken-safety-evaluator-v1"

type evaluationOperation struct {
	repository ExecutionRepository
	cascade    evaluator
	budget     HostedCallBudget
	now        func() time.Time
}

var _ EvaluationOperation = (*evaluationOperation)(nil)

func newEvaluationOperation(
	repository ExecutionRepository,
	cascade evaluator,
	budget HostedCallBudget,
	now func() time.Time,
) (*evaluationOperation, error) {
	if repository == nil || cascade.proposer == nil || !validProposerIdentity(cascade.proposerIdentity) ||
		cascade.audioExtractor == nil || cascade.audio == nil || cascade.video == nil || now == nil ||
		budget.PerClipNanoUSD < 0 || budget.PerDayNanoUSD < 0 || budget.PerRunNanoUSD < 0 {
		return nil, ErrEvaluationInvalid
	}
	return &evaluationOperation{repository: repository, cascade: cascade, budget: budget, now: now}, nil
}

func (o *evaluationOperation) Evaluate(ctx context.Context, request EvaluationRequest) (EvaluationReport, error) {
	if o == nil || ctx == nil || ctx.Err() != nil || !boundedLedgerID(request.RunID) || request.StartedAt.IsZero() {
		return EvaluationReport{}, ErrEvaluationInvalid
	}
	if err := validateSourceAuthority(request.Source.Authority); err != nil {
		return EvaluationReport{}, err
	}
	authoritySHA256, err := sourceAuthoritySHA256(request.Source.Authority)
	if err != nil {
		return EvaluationReport{}, ErrEvaluationInvalid
	}
	run := evaluationLedgerRun(request, authoritySHA256, proposerIdentitySHA256(o.cascade.proposerIdentity))
	created, err := o.repository.BeginSpokenSafetyRun(ctx, run)
	if err != nil {
		return EvaluationReport{}, err
	}
	if !created {
		return o.completedReport(ctx, run)
	}
	plan, err := PlanCompleteMedia(ctx, request.Source)
	if err != nil {
		return EvaluationReport{}, err
	}
	defer func() { _ = plan.Close() }()

	timeline := evaluationTimeline{last: run.CreatedAt, now: o.now}
	source := LedgerEvent{
		ID: evaluationLedgerID(run.ID, "source", 0), RunID: run.ID, Ordinal: 0,
		Kind: LedgerSourcePlanned, CreatedAt: timeline.next(),
		Source: &SourcePlanned{Audio: plan.Audio, Video: plan.Video},
	}
	if err := o.repository.AppendSpokenSafetyEvent(ctx, source); err != nil {
		return EvaluationReport{}, err
	}
	journal := &ledgerCascadeJournal{
		repository: o.repository, run: run, timeline: &timeline, budget: o.budget,
		eventIDs: []string{source.ID},
	}
	completed, err := o.cascade.evaluate(ctx, &plan, journal)
	if err != nil {
		return EvaluationReport{}, err
	}
	terminal := LedgerEvent{
		ID:    evaluationLedgerID(run.ID, "terminal", len(journal.eventIDs)),
		RunID: run.ID, Ordinal: len(journal.eventIDs), Kind: LedgerTerminal, CreatedAt: timeline.next(),
		Terminal: &TerminalResult{
			Evidence: completed.Evidence, Result: completed.Result, EventIDs: slices.Clone(journal.eventIDs),
		},
	}
	if err := o.repository.AppendSpokenSafetyEvent(ctx, terminal); err != nil {
		return EvaluationReport{}, err
	}
	return evaluationReport(run, terminal)
}

func (o *evaluationOperation) completedReport(ctx context.Context, run LedgerRun) (EvaluationReport, error) {
	events, err := o.repository.ListSpokenSafetyEvents(ctx, run.ID)
	if err != nil {
		return EvaluationReport{}, err
	}
	if len(events) == 0 || events[len(events)-1].Terminal == nil {
		return EvaluationReport{}, ErrEvaluationIncomplete
	}
	for index, event := range events {
		if event.RunID != run.ID || event.Ordinal != index {
			return EvaluationReport{}, ErrEvaluationIncomplete
		}
		if err := ValidateLedgerAppend(events[:index], event); err != nil {
			return EvaluationReport{}, ErrEvaluationIncomplete
		}
	}
	terminal := events[len(events)-1].Terminal
	ids := make([]string, 0, len(events)-1)
	for _, event := range events[:len(events)-1] {
		ids = append(ids, event.ID)
	}
	if !slices.Equal(ids, terminal.EventIDs) || !sameResult(terminal.Result, Reduce(terminal.Evidence)) {
		return EvaluationReport{}, ErrEvaluationIncomplete
	}
	return evaluationReport(run, events[len(events)-1])
}

func evaluationReport(run LedgerRun, terminal LedgerEvent) (EvaluationReport, error) {
	if terminal.Terminal == nil {
		return EvaluationReport{}, ErrEvaluationIncomplete
	}
	digest, err := LedgerEventSHA256(terminal)
	if err != nil {
		return EvaluationReport{}, ErrEvaluationIncomplete
	}
	return EvaluationReport{
		Run: run, Evidence: terminal.Terminal.Evidence, Result: terminal.Terminal.Result,
		TerminalEventID: terminal.ID, TerminalSHA256: digest,
	}, nil
}

func evaluationLedgerRun(request EvaluationRequest, authoritySHA256, proposerSHA256 string) LedgerRun {
	authority := request.Source.Authority
	return LedgerRun{
		ID: request.RunID, ClipHash: authority.SourceSHA256,
		AuthoritySHA256: authoritySHA256, SourceSHA256: authority.SourceSHA256,
		SourceBytes: authority.SourceBytes, DurationMS: authority.DurationMS,
		CertificationSHA256: authority.CertificationSHA256,
		PolicySHA256:        authority.PolicySHA256, ProposerSHA256: proposerSHA256,
		Implementation: evaluationImplementation, CreatedAt: request.StartedAt.UTC(),
	}
}

func proposerIdentitySHA256(identity proposerIdentity) string {
	raw, err := json.Marshal(struct {
		Implementation string `json:"implementation"`
		Platform       string `json:"platform"`
		RuntimeVersion string `json:"runtimeVersion"`
		RuntimeSHA256  string `json:"runtimeSha256"`
		ModelSHA256    string `json:"modelSha256"`
		ConfigSHA256   string `json:"configSha256"`
	}{
		Implementation: identity.Implementation, Platform: identity.Platform,
		RuntimeVersion: identity.RuntimeVersion, RuntimeSHA256: identity.RuntimeSHA256,
		ModelSHA256: identity.ModelSHA256, ConfigSHA256: identity.ConfigSHA256,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func canonicalJSONSHA256(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func evaluationLedgerID(runID, part string, ordinal int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("spoken-safety-evaluation\x00%s\x00%s\x00%d", runID, part, ordinal)))
	return fmt.Sprintf("safety-%x", sum[:12])
}

type evaluationTimeline struct {
	last time.Time
	now  func() time.Time
}

func (t *evaluationTimeline) next() time.Time {
	next := t.now().UTC()
	if !next.After(t.last) {
		next = t.last.Add(time.Nanosecond)
	}
	t.last = next
	return next
}

func isBudgetHeld(event LedgerEvent) bool {
	return event.Reserve != nil && event.Reserve.State == ReservationHeldBudget
}

func validHostedIdentity(identity hostedCallIdentity) bool {
	return boundedPublicIdentity(identity.RequestedProvider) && boundedPublicIdentity(identity.RequestedModel) &&
		boundedPublicIdentity(identity.ResolvedProvider) && boundedPublicIdentity(identity.ResolvedModel) &&
		boundedPublicIdentity(identity.UpstreamProvider) && validSHA256(identity.CapabilitySHA256) &&
		validSHA256(identity.PromptSHA256) && validSHA256(identity.SchemaSHA256) && identity.MaxChargeNanoUSD > 0
}

func closedFailure(attemptState string, callErr error) SettlementFailure {
	if callErr == nil {
		return FailureNone
	}
	if errors.Is(callErr, openroutermedia.ErrRouteMismatch) {
		return FailureRouteMismatch
	}
	if attemptState == string(AudioInvalidResponse) || attemptState == string(AudioDetectedUnprojectable) ||
		attemptState == string(VideoInvalidResponse) || attemptState == string(VideoProhibitedUnprojectable) {
		return FailureInvalidResponse
	}
	return FailureTransport
}

func sameResult(first, second Result) bool {
	return first.Outcome == second.Outcome && slices.Equal(first.Reasons, second.Reasons)
}
