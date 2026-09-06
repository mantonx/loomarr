package fillerairworthinessprojection

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerairworthiness"
	"github.com/loomarr/loomarr/internal/fillersafety"
)

const testRuleID = "rule-111111111111111111111111"

func TestProjectSpokenPublishesClosedSlurObservation(t *testing.T) {
	t.Parallel()
	report := spokenReport(t, fillersafety.AudioDetected, []string{testRuleID})
	authority := spokenAuthority(t)
	projection, err := ProjectSpoken(spokenSubject(report), report, authority)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Evidence.Coverage != fillerairworthiness.CoverageComplete || len(projection.Evidence.Observations) != 1 {
		t.Fatalf("projection = %#v", projection)
	}
	observation := projection.Evidence.Observations[0]
	if observation.Flag != fillerairworthiness.FlagSlurOrDegradingLanguage ||
		observation.StartMS != 100 || observation.EndMS != 800 ||
		observation.Severity != fillerairworthiness.SeverityHigh ||
		observation.Context != fillerairworthiness.ContextPresence {
		t.Fatalf("observation = %#v", observation)
	}
	if strings.Contains(string(projection.RawEvidence), "restricted-word") {
		t.Fatal("projection raw evidence invented restricted text")
	}
}

func TestProjectSpokenNegativeCoversOnlyCertifiedFlag(t *testing.T) {
	t.Parallel()
	report := spokenReport(t, fillersafety.AudioAbsent, []string{})
	projection, err := ProjectSpoken(spokenSubject(report), report, spokenAuthority(t))
	if err != nil {
		t.Fatal(err)
	}
	if projection.Evidence.Coverage != fillerairworthiness.CoverageComplete || len(projection.Evidence.Observations) != 0 ||
		!slices.Equal(projection.Evidence.Profile.CertifiedFlags, []fillerairworthiness.Flag{fillerairworthiness.FlagSlurOrDegradingLanguage}) {
		t.Fatalf("projection = %#v", projection)
	}
}

func TestProjectSpokenUnknownPositiveNeverBecomesClear(t *testing.T) {
	t.Parallel()
	report := spokenReport(t, fillersafety.AudioDetected, []string{"rule-222222222222222222222222"})
	projection, err := ProjectSpoken(spokenSubject(report), report, spokenAuthority(t))
	if err != nil {
		t.Fatal(err)
	}
	if projection.Evidence.Coverage != fillerairworthiness.CoverageIncomplete || len(projection.Evidence.Observations) != 0 {
		t.Fatalf("unknown positive projection = %#v", projection)
	}
}

func TestProjectSpokenRejectsReportOrSubjectDrift(t *testing.T) {
	t.Parallel()
	report := spokenReport(t, fillersafety.AudioDetected, []string{testRuleID})
	subject := spokenSubject(report)
	subject.EvidenceSHA256 = strings.Repeat("e", 64)
	if _, err := ProjectSpoken(subject, report, spokenAuthority(t)); err == nil {
		t.Fatal("source-drifted subject projected")
	}
	report.Run.CertificationSHA256 = strings.Repeat("f", 64)
	if _, err := ProjectSpoken(spokenSubject(report), report, spokenAuthority(t)); err == nil {
		t.Fatal("mutated report projected")
	}
}

func spokenAuthority(t *testing.T) SpokenAuthority {
	t.Helper()
	authority, err := SealSpokenAuthority(SpokenAuthority{
		PolicySHA256: strings.Repeat("b", 64), CertificationSHA256: strings.Repeat("c", 64),
		ProposerSHA256: strings.Repeat("d", 64), EvaluationImplementation: "spoken-safety-evaluator-v1",
		Rules: []Rule{{ID: testRuleID, Flag: fillerairworthiness.FlagSlurOrDegradingLanguage,
			Severity: fillerairworthiness.SeverityHigh, Context: fillerairworthiness.ContextPresence}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func spokenSubject(report fillersafety.EvaluationReport) Subject {
	return Subject{SHA256: strings.Repeat("a", 64), EvidenceSHA256: report.Run.SourceSHA256, EvidenceBytes: report.Run.SourceBytes, DurationMS: report.Run.DurationMS}
}

func spokenReport(t *testing.T, state fillersafety.AudioState, ruleIDs []string) fillersafety.EvaluationReport {
	t.Helper()
	evidence := fillersafety.Evidence{
		ProposalState: fillersafety.ProposalComplete,
		Candidates:    []fillersafety.Candidate{{ID: "candidate-1", StartMS: 100, EndMS: 800}},
		Audio:         []fillersafety.AudioAssessment{{CandidateID: "candidate-1", State: state, MatchedRuleIDs: ruleIDs}},
		Video:         fillersafety.VideoNotRun,
	}
	if state == fillersafety.AudioAbsent {
		evidence.Video = fillersafety.VideoNoSignal
	}
	run := fillersafety.LedgerRun{
		ID: "run-1", ClipHash: "clip-1", AuthoritySHA256: strings.Repeat("1", 64),
		SourceSHA256: strings.Repeat("2", 64), SourceBytes: 1_000, DurationMS: 1_000,
		CertificationSHA256: strings.Repeat("c", 64), PolicySHA256: strings.Repeat("b", 64),
		ProposerSHA256: strings.Repeat("d", 64), Implementation: "spoken-safety-evaluator-v1",
		CreatedAt: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
	}
	eventIDs := []string{"source-event", "proposal-event", "reserve-event", "settle-event"}
	createdAt := run.CreatedAt.Add(time.Second)
	result := fillersafety.Reduce(evidence)
	terminal := fillersafety.LedgerEvent{
		ID: "terminal-event", RunID: run.ID, Ordinal: len(eventIDs), Kind: fillersafety.LedgerTerminal,
		CreatedAt: createdAt, Terminal: &fillersafety.TerminalResult{Evidence: evidence, Result: result, EventIDs: eventIDs},
	}
	terminalSHA, err := fillersafety.LedgerEventSHA256(terminal)
	if err != nil {
		t.Fatal(err)
	}
	report := fillersafety.EvaluationReport{
		SchemaVersion: fillersafety.EvaluationReportSchemaVersion, ContractVersion: fillersafety.EvaluationReportContractVersion,
		Run: run, Evidence: evidence, Result: result, TerminalEventID: terminal.ID,
		TerminalEventIDs: eventIDs, TerminalCreatedAt: createdAt, TerminalSHA256: terminalSHA,
	}
	report.SHA256 = fillersafety.EvaluationReportSHA256(report)
	return report
}
