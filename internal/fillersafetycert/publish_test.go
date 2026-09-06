package fillersafetycert

import (
	"os"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

func TestPublishCertifiesExhaustiveSourceDisjointCascade(t *testing.T) {
	t.Parallel()
	fixture := newCertificationFixture(t)

	report, digest, err := Publish(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	if report.CertificationStatus != StatusPassed || report.DetectedPositiveSources != MinimumPositiveFamilies ||
		report.PositiveFamilies != MinimumPositiveFamilies || report.MissedPositiveSources != 0 ||
		report.SourceRecallExactLower95 < 0.95 || report.CleanSources != MinimumCleanFamilies ||
		report.CleanFalsePositiveSources != 0 || report.CoverageHolds != 0 || !validSHA256(digest) ||
		report.TrainingAllowed || report.IngestionAllowed || report.SchedulingAllowed || report.ProductionAdmissionAllowed {
		t.Fatalf("report=%+v digest=%q", report, digest)
	}
	info, err := os.Stat(fixture.outputPath)
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("output mode=%v err=%v", info.Mode(), err)
	}
	raw, err := os.ReadFile(fixture.outputPath)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSensitiveVocabulary(t, string(raw))
}

func TestCleanFalsePositivesRemainCountedWhenCoverageHolds(t *testing.T) {
	t.Parallel()
	fixture := newCertificationFixture(t)
	for index := MinimumPositiveFamilies; index < MinimumPositiveFamilies+3; index++ {
		item := fixture.authority.Cases[index]
		item.Label = LabelPositive
		fixture.manifest.Runs[index] = fixtureResultRun(fixture.authority, fixture.manifest.AuthoritySHA256, item, index)
	}

	mixed := &fixture.manifest.Runs[MinimumPositiveFamilies]
	mixed.Events[1].Proposal.Candidates = append(mixed.Events[1].Proposal.Candidates, fillersafety.Candidate{ID: "candidate-two", StartMS: 400, EndMS: 600})
	mixed.Events[len(mixed.Events)-1].Terminal.Evidence.Candidates = mixed.Events[1].Proposal.Candidates
	mixed.Events[len(mixed.Events)-1].Terminal.Evidence.Audio = append(mixed.Events[len(mixed.Events)-1].Terminal.Evidence.Audio,
		fillersafety.AudioAssessment{CandidateID: "candidate-two", State: fillersafety.AudioFailed, MatchedRuleIDs: []string{}})
	reserve := *mixed.Events[2].Reserve
	reserve.EvaluationID, reserve.RequestSHA256, reserve.CandidateID = mixed.Run.ID+"-evaluation-two", fixtureSHA(88_001), "candidate-two"
	reserveEvent := fillersafety.LedgerEvent{ID: mixed.Run.ID + "-reserve-two", RunID: mixed.Run.ID, Ordinal: 4, Kind: fillersafety.LedgerInferenceReserved, Reserve: &reserve, CreatedAt: mixed.Events[3].CreatedAt.Add(time.Nanosecond)}
	settle := *mixed.Events[3].Settle
	settle.ReservationEventID, settle.EvaluationID = reserveEvent.ID, reserve.EvaluationID
	settle.State, settle.Failure, settle.Outcome = fillersafety.SettlementFailed, fillersafety.FailureTransport, ""
	settleEvent := fillersafety.LedgerEvent{ID: mixed.Run.ID + "-settle-two", RunID: mixed.Run.ID, Ordinal: 5, Kind: fillersafety.LedgerInferenceSettled, Settle: &settle, CreatedAt: reserveEvent.CreatedAt.Add(time.Nanosecond)}
	terminal := mixed.Events[len(mixed.Events)-1]
	terminal.Ordinal, terminal.CreatedAt = 6, settleEvent.CreatedAt.Add(time.Nanosecond)
	terminal.Terminal.EventIDs = []string{mixed.Events[0].ID, mixed.Events[1].ID, mixed.Events[2].ID, mixed.Events[3].ID, reserveEvent.ID, settleEvent.ID}
	mixed.Events = append(mixed.Events[:4], reserveEvent, settleEvent, terminal)
	refreshRunTerminalEvidence(t, mixed)

	hold := &fixture.manifest.Runs[MinimumPositiveFamilies+1]
	hold.Events[3].Settle.State, hold.Events[3].Settle.Failure, hold.Events[3].Settle.Outcome = fillersafety.SettlementFailed, fillersafety.FailureTransport, ""
	hold.Events[len(hold.Events)-1].Terminal.Evidence.Audio[0] = fillersafety.AudioAssessment{CandidateID: "candidate-one", State: fillersafety.AudioFailed, MatchedRuleIDs: []string{}}
	refreshRunTerminalEvidence(t, hold)
	fixture.rewriteManifest(t)

	report, _, err := Publish(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	if report.CleanFalsePositiveSources != 2 || report.CoverageHolds != 2 || report.CertificationStatus != StatusFailed ||
		report.TrainingAllowed || report.IngestionAllowed || report.SchedulingAllowed || report.ProductionAdmissionAllowed {
		t.Fatalf("report=%+v", report)
	}
	if report.Cases[MinimumPositiveFamilies].Outcome != OutcomeCoverageHold || !report.Cases[MinimumPositiveFamilies].FalsePositive ||
		report.Cases[MinimumPositiveFamilies+1].Outcome != OutcomeCoverageHold || report.Cases[MinimumPositiveFamilies+1].FalsePositive ||
		report.Cases[MinimumPositiveFamilies+2].Outcome != OutcomeFalsePositive || !report.Cases[MinimumPositiveFamilies+2].FalsePositive ||
		report.Cases[MinimumPositiveFamilies+3].Outcome != OutcomeClean || report.Cases[MinimumPositiveFamilies+3].FalsePositive {
		t.Fatalf("cases=%+v", report.Cases[MinimumPositiveFamilies:])
	}
	expectedMetrics := []CleanSliceMetric{
		{Slice: "homophone_near_match", CleanSources: 25, FalsePositives: 0, FalsePositiveRate: 0, Passed: true},
		{Slice: "locale:en-US", CleanSources: 100, FalsePositives: 2, FalsePositiveRate: 0.02, Passed: false},
		{Slice: "music_only", CleanSources: 25, FalsePositives: 1, FalsePositiveRate: 0.04, Passed: false},
		{Slice: "target_locale", CleanSources: 25, FalsePositives: 1, FalsePositiveRate: 0.04, Passed: false},
		{Slice: "wordless", CleanSources: 25, FalsePositives: 0, FalsePositiveRate: 0, Passed: true},
	}
	if len(report.CleanSlices) != len(expectedMetrics) {
		t.Fatalf("clean slice count=%d want=%d metrics=%+v", len(report.CleanSlices), len(expectedMetrics), report.CleanSlices)
	}
	for index, want := range expectedMetrics {
		if report.CleanSlices[index] != want {
			t.Fatalf("clean slice metric[%d]=%+v want=%+v", index, report.CleanSlices[index], want)
		}
	}
}

func TestDevelopmentAuthorityCanOnlyPassDiagnostically(t *testing.T) {
	t.Parallel()
	fixture := newCertificationFixture(t)
	fixture.authority.ChallengeKind = ChallengeDevelopment
	fixture.rewriteAuthority(t)

	report, _, err := Publish(fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	if report.CertificationStatus != StatusDiagnosticPassed {
		t.Fatalf("status=%s", report.CertificationStatus)
	}
}

func TestPublishDoesNotOverwriteAReport(t *testing.T) {
	t.Parallel()
	fixture := newCertificationFixture(t)
	if err := os.WriteFile(fixture.outputPath, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Publish(fixture.config()); err == nil {
		t.Fatal("expected existing output to be preserved")
	}
	raw, err := os.ReadFile(fixture.outputPath)
	if err != nil || string(raw) != "existing" {
		t.Fatalf("output=%q err=%v", raw, err)
	}
}
