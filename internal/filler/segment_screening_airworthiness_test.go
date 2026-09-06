package filler

import (
	"slices"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerairworthiness"
)

func TestSegmentAirworthinessRejectsCertifiedPositiveDespiteOtherAxisHold(t *testing.T) {
	t.Parallel()
	subject := screeningChildSubjectFixture(t)
	records := passingAxisEvidence(t, subject)
	visual := &records[0].Evidence
	visual.Suitability.Observations = []fillerairworthiness.Observation{{
		ID: "visual-positive-1", Flag: fillerairworthiness.FlagAdultNudity,
		Severity: fillerairworthiness.SeverityLow, Context: fillerairworthiness.ContextDepiction,
		StartMS: 1_000, EndMS: 2_000,
	}}
	visual.SHA256 = SegmentScreeningAxisEvidenceSHA256(*visual)
	records[0].Evidence = *visual
	records[1].Evidence.Suitability.Coverage = fillerairworthiness.CoverageIncomplete
	records[1].Evidence.Outcome = ScreenHold
	records[1].Evidence.ReasonCode = "spoken_coverage_incomplete"
	records[1].Evidence.SHA256 = SegmentScreeningAxisEvidenceSHA256(records[1].Evidence)

	decision, err := evaluateSegmentAirworthiness(
		subject, records, screeningAirworthinessEvaluator(t, screeningProfiles(records)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Verdict != fillerairworthiness.VerdictReject || len(decision.Triggers) != 1 ||
		decision.Triggers[0].Flag != fillerairworthiness.FlagAdultNudity {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestSegmentAggregateRequiresAirworthinessPass(t *testing.T) {
	t.Parallel()
	subject := screeningChildSubjectFixture(t)
	records := passingAxisEvidence(t, subject)
	archive, err := NewSegmentAirworthinessEvaluator(fillerairworthiness.ProfileRestrictedArchive, screeningProfiles(records))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := evaluateSegmentAirworthiness(subject, records, archive)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]SegmentScreeningResult, 0, len(records))
	for _, record := range records {
		results = append(results, record.Evidence.Result())
	}
	evidence, err := NewSegmentScreeningEvidence(subject, results, decision, records[0].Evidence.AssessedAt)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Passes() || ValidateSegmentScreeningEvidence(evidence) != nil ||
		evidence.Airworthiness.Verdict != fillerairworthiness.VerdictHold {
		t.Fatalf("restricted archive aggregate = %#v", evidence)
	}
}

func TestSafetyAxisEvidenceRequiresBoundSuitabilityWhileOtherAxesForbidIt(t *testing.T) {
	t.Parallel()
	subject := screeningChildSubjectFixture(t)
	records := passingAxisEvidence(t, subject)

	safety := records[0]
	safety.Evidence.Suitability = nil
	safety.Evidence.SHA256 = SegmentScreeningAxisEvidenceSHA256(safety.Evidence)
	if ValidateRecordedSegmentScreeningAxisEvidence(safety) == nil {
		t.Fatal("safety evidence without suitability record validated")
	}

	rightsIndex := slices.IndexFunc(records, func(record RecordedSegmentScreeningAxisEvidence) bool {
		return record.Evidence.Profile.Axis == ScreenRights
	})
	rights := records[rightsIndex]
	rights.Evidence.Suitability = cloneSuitabilityAxisEvidence(records[0].Evidence.Suitability)
	rights.Evidence.SHA256 = SegmentScreeningAxisEvidenceSHA256(rights.Evidence)
	if ValidateRecordedSegmentScreeningAxisEvidence(rights) == nil {
		t.Fatal("rights evidence carrying suitability record validated")
	}
}
