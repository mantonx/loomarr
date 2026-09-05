package suggest

import (
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/quality"
)

func TestClassifySuggestionQualityFailureStageBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		trace   DecisionTrace
		failure *Failure
		want    []quality.Observation
	}{
		{
			name:    "catalog failure",
			trace:   DecisionTrace{Version: DecisionTraceVersion, Terminal: TerminalRetrievalFailure},
			failure: &Failure{Code: FailureCodeNoGroundedTitles},
			want: []quality.Observation{
				{Stage: quality.StageRetrieval, Outcome: quality.OutcomeFailed},
				{Stage: quality.StageGeneration, Outcome: quality.OutcomeAbstained, Duration: time.Second},
				{Stage: quality.StageGrounding, Outcome: quality.OutcomeRejected},
			},
		},
		{
			name:    "alternate searches returned no candidates",
			trace:   DecisionTrace{Version: DecisionTraceVersion, Terminal: ReasonRetrievalEmpty},
			failure: &Failure{Code: FailureCodeNoGroundedTitles},
			want: []quality.Observation{
				{Stage: quality.StageRetrieval, Outcome: quality.OutcomeEmpty},
				{Stage: quality.StageGeneration, Outcome: quality.OutcomeAbstained, Duration: time.Second},
				{Stage: quality.StageGrounding, Outcome: quality.OutcomeRejected},
			},
		},
		{
			name:    "provider failed before retrieval",
			trace:   DecisionTrace{Version: DecisionTraceVersion, Terminal: TerminalProviderFailure},
			failure: &Failure{Code: FailureProvider},
			want: []quality.Observation{
				{Stage: quality.StageGeneration, Outcome: quality.OutcomeFailed, Duration: time.Second},
			},
		},
		{
			name:    "provider failed after retrieval",
			trace:   DecisionTrace{Version: DecisionTraceVersion, SurfacedTotal: 4, Terminal: TerminalProviderFailure},
			failure: &Failure{Code: FailureProvider},
			want: []quality.Observation{
				{Stage: quality.StageRetrieval, Outcome: quality.OutcomeSucceeded, CandidateCount: 4},
				{Stage: quality.StageGeneration, Outcome: quality.OutcomeFailed, Duration: time.Second},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifySuggestionQuality(tt.trace, time.Second, false, tt.failure)
			if len(got) != len(tt.want) {
				t.Fatalf("observations = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("observation %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestQualityObservationKeyIsOpaqueAndAttemptScoped(t *testing.T) {
	first := qualityObservationKey("job-sensitive-identity", 1, quality.StageRetrieval)
	replay := qualityObservationKey("job-sensitive-identity", 1, quality.StageRetrieval)
	nextAttempt := qualityObservationKey("job-sensitive-identity", 2, quality.StageRetrieval)
	if first != replay {
		t.Fatal("same terminal callback did not derive the same idempotency key")
	}
	if first == nextAttempt || len(first) != 64 || strings.Contains(first, "job-sensitive-identity") {
		t.Fatalf("attempt-scoped key = %q, next = %q", first, nextAttempt)
	}
}
