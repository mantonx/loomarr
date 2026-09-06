package filler

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerairworthiness"
	"github.com/loomarr/loomarr/internal/fillerairworthinessprojection"
	"github.com/loomarr/loomarr/internal/fillersafety"
)

const spokenSafetyRuleFixture = "rule-111111111111111111111111"

func TestSpokenSafetyEvaluatorProjectsPositiveAndReplaysSettledOperation(t *testing.T) {
	media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
	repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
	if err != nil {
		t.Fatal(err)
	}
	authority := spokenSafetyAuthorityFixture(t)
	calls := 0
	var request SpokenSafetyProducerRequest
	producer := spokenSafetyProducerFunc(func(_ context.Context, got SpokenSafetyProducerRequest) (fillersafety.EvaluationReport, error) {
		calls++
		request = got
		return spokenSafetyReportFixture(t, got.OperationSHA256, got.Subject, authority, fillersafety.AudioDetected, []string{spokenSafetyRuleFixture}), nil
	})
	evaluator, err := NewSpokenSafetyEvaluator(authority, repository, producer)
	if err != nil {
		t.Fatal(err)
	}

	first, err := evaluator.Evaluate(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	if first.Evidence.Outcome != ScreenPass || first.Evidence.ReasonCode != "spoken_safety_evidence_complete" ||
		first.Evidence.Suitability == nil || first.Evidence.Suitability.Coverage != fillerairworthiness.CoverageComplete ||
		len(first.Evidence.Suitability.Observations) != 1 ||
		first.Evidence.Suitability.Observations[0].Flag != fillerairworthiness.FlagSlurOrDegradingLanguage {
		t.Fatalf("spoken positive = %+v", first)
	}
	wantOperation := segmentScreeningOperationSHA256(media.Subject.SHA256, evaluator.projected.profile)
	if calls != 1 || request.OperationSHA256 != wantOperation || request.EvidencePath != media.EvidencePath ||
		request.Subject != projectedSafetySubject(media.Subject) {
		t.Fatalf("producer request = %+v, calls = %d", request, calls)
	}
	if bytes.Contains(first.RawEvidence, []byte(filepath.Dir(media.EvidencePath))) {
		t.Fatal("spoken evidence leaked its private media path")
	}
	assertSafetyObservationRejectsAirworthiness(t, media.Subject, first, fillerairworthiness.FlagSlurOrDegradingLanguage)

	if err := repository.PutSegmentScreeningAxisEvidence(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second, err := evaluator.Evaluate(t.Context(), media)
	if err != nil || second.Evidence.SHA256 != first.Evidence.SHA256 || calls != 1 {
		t.Fatalf("spoken replay = %+v, calls = %d, error = %v", second, calls, err)
	}
	if err := os.WriteFile(media.EvidencePath, bytes.Repeat([]byte("x"), int(media.Subject.EvidenceBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.Evaluate(t.Context(), media); err == nil || calls != 1 {
		t.Fatalf("artifact drift replay error = %v, calls = %d", err, calls)
	}
}

func TestSpokenSafetyEvaluatorHoldsUnknownPositive(t *testing.T) {
	media := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
	repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
	if err != nil {
		t.Fatal(err)
	}
	authority := spokenSafetyAuthorityFixture(t)
	evaluator, err := NewSpokenSafetyEvaluator(authority, repository, spokenSafetyProducerFunc(
		func(_ context.Context, request SpokenSafetyProducerRequest) (fillersafety.EvaluationReport, error) {
			return spokenSafetyReportFixture(t, request.OperationSHA256, request.Subject, authority,
				fillersafety.AudioDetected, []string{"rule-222222222222222222222222"}), nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}

	recorded, err := evaluator.Evaluate(t.Context(), media)
	if err != nil || recorded.Evidence.Outcome != ScreenHold || recorded.Evidence.ReasonCode != "spoken_safety_evidence_incomplete" ||
		recorded.Evidence.Suitability == nil || recorded.Evidence.Suitability.Coverage != fillerairworthiness.CoverageIncomplete ||
		len(recorded.Evidence.Suitability.Observations) != 0 {
		t.Fatalf("unknown spoken positive = %+v, error = %v", recorded, err)
	}
}

func TestSpokenSafetyEvaluatorRejectsArtifactAndProducerDriftBeforeAuthority(t *testing.T) {
	authority := spokenSafetyAuthorityFixture(t)
	for _, test := range []struct {
		name   string
		mutate func(*SegmentScreeningMedia)
		report func(*fillersafety.EvaluationReport)
	}{
		{name: "artifact identity", mutate: func(media *SegmentScreeningMedia) {
			body := bytes.Repeat([]byte("x"), int(media.Subject.EvidenceBytes))
			if err := os.WriteFile(media.EvidencePath, body, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "producer source bytes", report: func(report *fillersafety.EvaluationReport) {
			report.Run.SourceBytes++
			report.SHA256 = fillersafety.EvaluationReportSHA256(*report)
		}},
		{name: "producer operation", report: func(report *fillersafety.EvaluationReport) {
			report.Run.ID = "different-operation"
		}},
		{name: "producer certification", report: func(report *fillersafety.EvaluationReport) {
			report.Run.CertificationSHA256 = strings.Repeat("f", 64)
			report.SHA256 = fillersafety.EvaluationReportSHA256(*report)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := playbackIntegrityMediaFixture(t, validPlaybackIntegrityQuality())
			if test.mutate != nil {
				test.mutate(&candidate)
			}
			calls := 0
			repository, err := NewFileSegmentScreeningEvidenceRepository(filepath.Join(t.TempDir(), "screening-evidence"))
			if err != nil {
				t.Fatal(err)
			}
			evaluator, err := NewSpokenSafetyEvaluator(authority, repository, spokenSafetyProducerFunc(
				func(_ context.Context, request SpokenSafetyProducerRequest) (fillersafety.EvaluationReport, error) {
					calls++
					report := spokenSafetyReportFixture(t, request.OperationSHA256, request.Subject, authority,
						fillersafety.AudioAbsent, []string{})
					if test.report != nil {
						test.report(&report)
					}
					return report, nil
				},
			))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := evaluator.Evaluate(t.Context(), candidate); err == nil {
				t.Fatal("drifted spoken operation produced authority")
			}
			wantCalls := 1
			if test.mutate != nil {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("producer calls = %d, want %d", calls, wantCalls)
			}
		})
	}
}

type spokenSafetyProducerFunc func(context.Context, SpokenSafetyProducerRequest) (fillersafety.EvaluationReport, error)

func (produce spokenSafetyProducerFunc) EvaluateSpokenSafety(ctx context.Context, request SpokenSafetyProducerRequest) (fillersafety.EvaluationReport, error) {
	return produce(ctx, request)
}

func spokenSafetyAuthorityFixture(t *testing.T) fillerairworthinessprojection.SpokenAuthority {
	t.Helper()
	authority, err := fillerairworthinessprojection.SealSpokenAuthority(fillerairworthinessprojection.SpokenAuthority{
		PolicySHA256: strings.Repeat("b", 64), CertificationSHA256: strings.Repeat("c", 64),
		ProposerSHA256: strings.Repeat("d", 64), EvaluationImplementation: "spoken-safety-evaluator-v1",
		Rules: []fillerairworthinessprojection.Rule{{
			ID: spokenSafetyRuleFixture, Flag: fillerairworthiness.FlagSlurOrDegradingLanguage,
			Severity: fillerairworthiness.SeverityHigh, Context: fillerairworthiness.ContextPresence,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func spokenSafetyReportFixture(t *testing.T, runID string, subject fillerairworthinessprojection.Subject, authority fillerairworthinessprojection.SpokenAuthority, state fillersafety.AudioState, ruleIDs []string) fillersafety.EvaluationReport {
	t.Helper()
	evidence := fillersafety.Evidence{
		ProposalState: fillersafety.ProposalComplete,
		Candidates:    []fillersafety.Candidate{{ID: "candidate-1", StartMS: 100, EndMS: 800}},
		Audio:         []fillersafety.AudioAssessment{{CandidateID: "candidate-1", State: state, MatchedRuleIDs: slices.Clone(ruleIDs)}},
		Video:         fillersafety.VideoNotRun,
	}
	if state == fillersafety.AudioAbsent {
		evidence.Video = fillersafety.VideoNoSignal
	}
	createdAt := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	run := fillersafety.LedgerRun{
		ID: runID, ClipHash: subject.EvidenceSHA256, AuthoritySHA256: strings.Repeat("1", 64),
		SourceSHA256: subject.EvidenceSHA256, SourceBytes: subject.EvidenceBytes, DurationMS: subject.DurationMS,
		CertificationSHA256: authority.CertificationSHA256, PolicySHA256: authority.PolicySHA256,
		ProposerSHA256: authority.ProposerSHA256, Implementation: authority.EvaluationImplementation, CreatedAt: createdAt,
	}
	eventIDs := []string{"source-event", "proposal-event", "reserve-event", "settle-event"}
	terminalAt := createdAt.Add(time.Second)
	result := fillersafety.Reduce(evidence)
	terminal := fillersafety.LedgerEvent{
		ID: "terminal-event", RunID: run.ID, Ordinal: len(eventIDs), Kind: fillersafety.LedgerTerminal, CreatedAt: terminalAt,
		Terminal: &fillersafety.TerminalResult{Evidence: evidence, Result: result, EventIDs: eventIDs},
	}
	terminalSHA256, err := fillersafety.LedgerEventSHA256(terminal)
	if err != nil {
		t.Fatal(err)
	}
	report := fillersafety.EvaluationReport{
		SchemaVersion: fillersafety.EvaluationReportSchemaVersion, ContractVersion: fillersafety.EvaluationReportContractVersion,
		Run: run, Evidence: evidence, Result: result, TerminalEventID: terminal.ID, TerminalEventIDs: eventIDs,
		TerminalCreatedAt: terminalAt, TerminalSHA256: terminalSHA256,
	}
	report.SHA256 = fillersafety.EvaluationReportSHA256(report)
	return report
}

func assertSafetyObservationRejectsAirworthiness(t *testing.T, subject SegmentScreeningSubject, recorded RecordedSegmentScreeningAxisEvidence, flag fillerairworthiness.Flag) {
	t.Helper()
	records := passingAxisEvidence(t, subject)
	index := slices.IndexFunc(records, func(candidate RecordedSegmentScreeningAxisEvidence) bool {
		return candidate.Evidence.Profile.Axis == recorded.Evidence.Profile.Axis
	})
	records[index] = recorded
	decision, err := evaluateSegmentAirworthiness(subject, records, screeningAirworthinessEvaluator(t, screeningProfiles(records)))
	if err != nil || decision.Verdict != fillerairworthiness.VerdictReject || !slices.Contains(decision.ObservedFlags, flag) {
		t.Fatalf("Airworthiness decision = %+v, error = %v", decision, err)
	}
}
