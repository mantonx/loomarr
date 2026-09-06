package fillersafety

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestEvaluationReportAuthenticatesItsTerminalEvidence(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})
	report, err := fixture.operation.Evaluate(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateEvaluationReport(report); err != nil {
		t.Fatalf("validate report: %v", err)
	}
}

func TestEvaluationReportRejectsIdentityEvidenceAndDigestDrift(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})
	report, err := fixture.operation.Evaluate(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*EvaluationReport)
	}{
		{"run", func(value *EvaluationReport) { value.Run.PolicySHA256 = strings.Repeat("f", 64) }},
		{"evidence", func(value *EvaluationReport) { value.Evidence.Video = VideoFailed }},
		{"result", func(value *EvaluationReport) { value.Result.Outcome = OutcomeHold }},
		{"terminal id", func(value *EvaluationReport) { value.TerminalEventID = "different-terminal" }},
		{"terminal events", func(value *EvaluationReport) { value.TerminalEventIDs = slices.Clone(value.TerminalEventIDs[1:]) }},
		{"duplicate terminal event", func(value *EvaluationReport) {
			value.TerminalEventIDs = append(value.TerminalEventIDs, value.TerminalEventIDs[0])
		}},
		{"terminal time", func(value *EvaluationReport) { value.TerminalCreatedAt = value.TerminalCreatedAt.Add(time.Second) }},
		{"terminal digest", func(value *EvaluationReport) { value.TerminalSHA256 = strings.Repeat("e", 64) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := report
			candidate.TerminalEventIDs = slices.Clone(report.TerminalEventIDs)
			test.mutate(&candidate)
			if ValidateEvaluationReport(candidate) == nil {
				t.Fatal("drifted report validated")
			}
		})
	}
}
