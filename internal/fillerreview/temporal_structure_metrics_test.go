package fillerreview

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func TestCompareTemporalStructureAssessmentsScoresConstructionTruthAndBoundaries(t *testing.T) {
	fixture := newTemporalStructureComparisonFixture(t)
	firstPath := writeTemporalHumanJSON(t, t.TempDir(), "first.json", fixture.assessmentSet("assessor-a", "qwen", "qwen/model"))
	secondPath := writeTemporalHumanJSON(t, t.TempDir(), "second.json", fixture.assessmentSet("assessor-b", "claude", "anthropic/model"))
	report, err := CompareTemporalStructureAssessments(fixture.comparisonConfig(firstPath, secondPath))
	if err != nil {
		t.Fatal(err)
	}
	if report.Cases != 3 || report.AllAssessorsExactCorrect != 3 || report.ProductionAdmissionAllowed || len(report.DiagnosticCandidates) != 0 || report.Disposition.NextAction != "expand_provenance_grounded_challenge" || report.Disposition.BlindHumanAuditRequired || report.Disposition.TrainingAllowed {
		t.Fatalf("report disposition or totals = %+v", report)
	}
	if len(report.AssessorSummaries) != 2 || len(report.ConstructionSummaries) != 8 || len(report.SliceSummaries) != 4 || len(report.PairSummaries) != 1 {
		t.Fatalf("summary cardinality = %d/%d/%d/%d", len(report.AssessorSummaries), len(report.ConstructionSummaries), len(report.SliceSummaries), len(report.PairSummaries))
	}
	for _, summary := range report.SliceSummaries {
		if summary.Slice != TemporalStructureSliceMixedRoleJoins && summary.Slice != TemporalStructureSliceTwoItemCompilation || summary.TruthUnit != "" || summary.Cases != 1 || summary.ExactUnitCorrect != 1 || summary.ExactSegmentPlans != 1 || summary.CoverageComplete != 1 || summary.UnderSplits != 0 || summary.OverSplits != 0 || summary.SegmentRoleTargets != 2 || summary.SegmentRoleCorrect != 2 || summary.Boundary.TruthTargets != 1 || summary.Boundary.ComparableTargets != 1 || summary.Boundary.Within2000MS != 1 || summary.Boundary.MedianDistanceMS == nil || *summary.Boundary.MedianDistanceMS != 100 {
			t.Fatalf("slice summary = %+v", summary)
		}
	}
	for _, summary := range report.AssessorSummaries {
		if summary.ExactUnitCorrect != 3 || summary.StandaloneClassCorrect != 3 || summary.RoleComparable != 1 || summary.RoleCorrect != 1 || summary.ExactLabelCorrect != 3 || summary.ExactSegmentPlans != 3 || summary.CoverageComplete != 3 || summary.UnderSplits != 0 || summary.OverSplits != 0 || summary.SegmentRoleTargets != 4 || summary.SegmentRoleCorrect != 4 || summary.Boundary.TruthTargets != 3 || summary.Boundary.ComparableTargets != 3 || summary.Boundary.Within2000MS != 3 || summary.Boundary.Within5000MS != 3 || summary.Boundary.MedianDistanceMS == nil || *summary.Boundary.MedianDistanceMS != 100 {
			t.Fatalf("assessor summary = %+v", summary)
		}
	}
	pair := report.PairSummaries[0]
	if pair.OperationallyComparable != 3 || pair.ExactUnitAgreement != 3 || pair.StandaloneClassAgreement != 3 || pair.RoleComparable != 1 || pair.RoleAgreement != 1 || pair.ExactLabelAgreement != 3 || pair.ExactSegmentPlanAgreement != 3 {
		t.Fatalf("pair summary = %+v", pair)
	}
	for _, item := range report.CaseComparisons {
		if len(item.Assessments) != 2 {
			t.Fatalf("case assessment count = %+v", item)
		}
		if item.Truth.Unit == fillereval.UnitCompilation && (len(item.TruthSegments) != 2 || item.TruthSegments[0].Role == item.TruthSegments[1].Role) {
			t.Fatalf("compilation did not preserve mixed per-segment roles: %+v", item.TruthSegments)
		}
		if item.Truth.Unit == fillereval.UnitCompilation && (len(item.Truth.Slices) != 2 || item.Truth.Slices[0] != TemporalStructureSliceMixedRoleJoins) {
			t.Fatalf("compilation did not preserve private certification slices: %+v", item.Truth)
		}
		if item.Truth.Unit == fillereval.UnitStandalone && len(item.Assessments[0].BoundaryDistances) != 0 {
			t.Fatalf("standalone invented a structural boundary: %+v", item)
		}
	}
}

func TestScoreTemporalStructureSegmentsSeparatesUnderAndOverSplitting(t *testing.T) {
	truth := []TemporalStructureTruthSegment{
		{StartMS: 0, EndMS: 10_000, Role: fillereval.TemporalSegmentCommercial},
		{StartMS: 10_000, EndMS: 20_000, Role: fillereval.TemporalSegmentPromo},
	}
	tests := []struct {
		name      string
		predicted []TemporalStructurePredictedSegment
		under     int
		over      int
	}{
		{
			name: "under split",
			predicted: []TemporalStructurePredictedSegment{
				{StartMS: 0, EndMS: 20_000, Role: fillereval.TemporalSegmentCommercial},
			},
			under: 1,
		},
		{
			name: "over split",
			predicted: []TemporalStructurePredictedSegment{
				{StartMS: 0, EndMS: 5_000, Role: fillereval.TemporalSegmentCommercial},
				{StartMS: 5_000, EndMS: 10_000, Role: fillereval.TemporalSegmentCommercial},
				{StartMS: 10_000, EndMS: 20_000, Role: fillereval.TemporalSegmentPromo},
			},
			over: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := TemporalStructureAssessorCaseResult{Prediction: TemporalStructurePredictedLabel{Segments: test.predicted}}
			scoreTemporalStructureSegments(&result, truth)
			if result.UnderSplits != test.under || result.OverSplits != test.over || !result.CoverageComplete || result.ExactSegmentPlan {
				t.Fatalf("segment score = %+v", result)
			}
		})
	}
}

func TestCompareTemporalStructureAssessmentsSeparatesErrorsFailuresAndAgreement(t *testing.T) {
	fixture := newTemporalStructureComparisonFixture(t)
	first := fixture.assessmentSet("assessor-a", "qwen", "qwen/model")
	second := fixture.assessmentSet("assessor-b", "claude", "anthropic/model")

	standalone := temporalStructureAssessmentByTruth(&second, fillereval.UnitStandalone)
	standalone.Role.Kind = fillereval.TemporalRolePromo
	standalone.Segments[0].Role = fillereval.TemporalSegmentPromo
	compilation := temporalStructureAssessmentByTruth(&second, fillereval.UnitCompilation)
	compilation.Unit.Kind = fillereval.UnitStandalone
	compilation.Role = &TemporalStructureRoleClaim{Kind: fillereval.TemporalRolePromo, DecisiveAtMS: []int64{500}, Reason: "wrong role"}
	compilation.Segments = []TemporalStructureSegmentClaim{{StartMS: 0, EndMS: fixture.durationByAlias[compilation.Alias], Role: fillereval.TemporalSegmentPromo, DecisiveAtMS: []int64{500}, Reason: "wrong whole-video segment"}}
	compilation.Inference = temporalStructureTestInference(second.CompletedAt.Add(-time.Minute), true)
	excerpt := temporalStructureAssessmentByTruth(&second, fillereval.UnitProgrammeExcerpt)
	excerpt.Unit = nil
	excerpt.Segments = nil
	excerpt.OperationalFailure = &fillereval.TemporalOperationalFailure{Code: fillereval.TemporalFailureTimeout, Detail: "deadline", Retryable: true}
	excerpt.Inference.Calls[0].ResponseSHA256 = ""
	excerpt.Inference.Calls[0].OperationalFailure = fillereval.TemporalFailureTimeout

	firstPath := writeTemporalHumanJSON(t, t.TempDir(), "first.json", first)
	secondPath := writeTemporalHumanJSON(t, t.TempDir(), "second.json", second)
	report, err := CompareTemporalStructureAssessments(fixture.comparisonConfig(firstPath, secondPath))
	if err != nil {
		t.Fatal(err)
	}
	if report.AllAssessorsExactCorrect != 0 || len(report.DiagnosticCandidates) != 3 || report.Disposition.NextAction != "inspect_targeted_diagnostics" || len(report.Disposition.TargetedCases) != 3 {
		t.Fatalf("diagnostic disposition = %+v", report.Disposition)
	}
	secondSummary := report.AssessorSummaries[1]
	if secondSummary.AssessorID != "assessor-b" || secondSummary.OperationalFailures != 1 || secondSummary.UnitComparable != 2 || secondSummary.ExactUnitCorrect != 1 || secondSummary.StandaloneClassCorrect != 1 || secondSummary.RoleComparable != 1 || secondSummary.RoleCorrect != 0 || secondSummary.ExactLabelCorrect != 0 || secondSummary.ExactSegmentPlans != 0 || secondSummary.CoverageComplete != 2 || secondSummary.UnderSplits != 1 || secondSummary.OverSplits != 0 || secondSummary.SegmentRoleTargets != 3 || secondSummary.SegmentRoleCorrect != 0 || secondSummary.Boundary.TruthTargets != 3 || secondSummary.Boundary.ComparableTargets != 0 || secondSummary.Boundary.MedianDistanceMS != nil {
		t.Fatalf("second assessor summary = %+v", secondSummary)
	}
	pair := report.PairSummaries[0]
	if pair.OperationallyComparable != 2 || pair.ExactUnitAgreement != 1 || pair.StandaloneClassAgreement != 1 || pair.RoleComparable != 1 || pair.RoleAgreement != 0 || pair.ExactLabelAgreement != 0 || pair.ExactSegmentPlanAgreement != 0 {
		t.Fatalf("pair summary = %+v", pair)
	}
	reasons := make(map[string]struct{})
	for _, candidate := range report.DiagnosticCandidates {
		for _, reason := range candidate.Reasons {
			reasons[reason] = struct{}{}
		}
	}
	for _, want := range []string{"operational_failure", "unit_error", "role_error", "under_split", "segment_role_error", "model_unit_disagreement", "model_role_disagreement"} {
		if _, exists := reasons[want]; !exists {
			t.Fatalf("missing diagnostic reason %q in %+v", want, report.DiagnosticCandidates)
		}
	}
}

func TestCompareTemporalStructureAssessmentsRequiresIndependentModelsAndPostResultTime(t *testing.T) {
	fixture := newTemporalStructureComparisonFixture(t)
	first := fixture.assessmentSet("assessor-a", "same-family", "model-a")
	second := fixture.assessmentSet("assessor-b", "same-family", "model-b")
	firstPath := writeTemporalHumanJSON(t, t.TempDir(), "first.json", first)
	secondPath := writeTemporalHumanJSON(t, t.TempDir(), "second.json", second)
	config := fixture.comparisonConfig(firstPath, secondPath)
	if _, err := CompareTemporalStructureAssessments(config); err == nil || !strings.Contains(err.Error(), "two model families") {
		t.Fatalf("same-family error = %v", err)
	}

	second.Assessor.ModelFamily = "other-family"
	secondPath = writeTemporalHumanJSON(t, t.TempDir(), "second-other.json", second)
	config = fixture.comparisonConfig(firstPath, secondPath)
	config.ComparedAt = second.CompletedAt.Add(-time.Second)
	if _, err := CompareTemporalStructureAssessments(config); err == nil || !strings.Contains(err.Error(), "predates assessor") {
		t.Fatalf("pre-result comparison error = %v", err)
	}
}

func TestPublishTemporalStructureComparisonIsImmutable(t *testing.T) {
	fixture := newTemporalStructureComparisonFixture(t)
	firstPath := writeTemporalHumanJSON(t, t.TempDir(), "first.json", fixture.assessmentSet("assessor-a", "qwen", "qwen/model"))
	secondPath := writeTemporalHumanJSON(t, t.TempDir(), "second.json", fixture.assessmentSet("assessor-b", "claude", "anthropic/model"))
	config := fixture.comparisonConfig(firstPath, secondPath)
	config.OutputPath = filepath.Join(t.TempDir(), "comparison.json")
	if _, digest, err := PublishTemporalStructureComparison(config); err != nil || !reviewSHA256(digest) {
		t.Fatalf("publish digest=%q error=%v", digest, err)
	}
	if _, _, err := PublishTemporalStructureComparison(config); err == nil {
		t.Fatal("immutable comparison output was overwritten")
	}
}

func (fixture temporalStructureComparisonFixture) comparisonConfig(paths ...string) TemporalStructureComparisonConfig {
	return TemporalStructureComparisonConfig{
		PublicManifestPath: fixture.manifestPath, PrivateAuthorityPath: fixture.authorityPath,
		AssessmentPaths: paths, ExpectedCases: len(fixture.manifest.Cases), ComparedAt: fixture.manifest.GeneratedAt.Add(3 * time.Hour),
	}
}
