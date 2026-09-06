package filler

import (
	"testing"
	"time"
)

func segmentScreeningFixture(t *testing.T) SegmentScreeningEvidence {
	t.Helper()
	subject := screeningSubjectFixture(t)
	records := passingAxisEvidence(t, subject)
	results := make([]SegmentScreeningResult, 0, len(records))
	for _, record := range records {
		results = append(results, record.Evidence.Result())
	}
	evidence, err := NewSegmentScreeningEvidence(subject, results, screeningAirworthinessDecision(t, subject, records), time.Date(2026, time.September, 5, 14, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func TestSegmentScreeningRequiresAllFiveIndependentPasses(t *testing.T) {
	evidence := segmentScreeningFixture(t)
	if !evidence.Passes() || len(evidence.Results) != 5 || evidence.Results[0].Axis != ScreenPlayback || evidence.Results[1].Axis != ScreenRights || evidence.Results[2].Axis != ScreenSpokenSafety || evidence.Results[3].Axis != ScreenVisualSafety || evidence.Results[4].Axis != ScreenWrittenSafety {
		t.Fatalf("screening = %+v", evidence)
	}
	for _, axis := range []SegmentScreeningAxis{ScreenVisualSafety, ScreenSpokenSafety, ScreenWrittenSafety, ScreenRights, ScreenPlayback} {
		t.Run(string(axis), func(t *testing.T) {
			candidate := evidence
			candidate.Results = append([]SegmentScreeningResult(nil), evidence.Results...)
			for index := range candidate.Results {
				if candidate.Results[index].Axis == axis {
					candidate.Results[index].Outcome = ScreenHold
				}
			}
			candidate.SHA256 = SegmentScreeningEvidenceSHA256(candidate)
			if candidate.Passes() {
				t.Fatalf("%s hold passed screening", axis)
			}
		})
	}
}

func TestValidateSegmentScreeningRejectsCoverageAndIdentityDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SegmentScreeningEvidence)
	}{
		{name: "missing axis", mutate: func(e *SegmentScreeningEvidence) { e.Results = e.Results[:4] }},
		{name: "duplicate axis", mutate: func(e *SegmentScreeningEvidence) { e.Results[1].Axis = e.Results[0].Axis }},
		{name: "unknown outcome", mutate: func(e *SegmentScreeningEvidence) { e.Results[0].Outcome = "maybe" }},
		{name: "missing authority", mutate: func(e *SegmentScreeningEvidence) { e.Results[0].AuthoritySHA256 = "" }},
		{name: "unsafe reason text", mutate: func(e *SegmentScreeningEvidence) { e.Results[0].ReasonCode = "contains restricted phrase" }},
		{name: "subject", mutate: func(e *SegmentScreeningEvidence) { e.SubjectSHA256 = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := segmentScreeningFixture(t)
			test.mutate(&evidence)
			evidence.SHA256 = SegmentScreeningEvidenceSHA256(evidence)
			if err := ValidateSegmentScreeningEvidence(evidence); err == nil {
				t.Fatal("mutated screening was accepted")
			}
		})
	}
}
