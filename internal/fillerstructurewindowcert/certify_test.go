package fillerstructurewindowcert

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func TestCertifyRequiresTwoExactFamiliesAcrossEverySeamSlice(t *testing.T) {
	suite, results := certificationFixture(t)
	report, err := Certify(suite, results, time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusPassed || report.DecidedCases != 6 || report.HeldCases != 0 ||
		report.WrongCases != 0 || len(report.AssessorProfiles) != 2 || len(report.Slices) != len(RequiredSlices()) ||
		report.TrainingAllowed || report.AutomaticMaterializationAllowed || report.SHA256 != ReportSHA256(report) {
		t.Fatalf("report = %+v", report)
	}
	for _, result := range report.Slices {
		if !result.Passed || result.Cases != MinimumSliceCases || result.DecidedCases != MinimumSliceCases {
			t.Fatalf("slice = %+v", result)
		}
	}
}

func TestCertifyFailsOnHeldOrWrongFamilyWithoutGrantingAuthority(t *testing.T) {
	suite, results := certificationFixture(t)
	results[0].Stitches = results[0].Stitches[:1]
	report, err := Certify(suite, results, time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusFailed || report.HeldCases != 1 || report.AutomaticMaterializationAllowed ||
		!slices.Contains(report.FailureCodes, "stitch_count") {
		t.Fatalf("held report = %+v", report)
	}

	suite, results = certificationFixture(t)
	wrong := slices.Clone(suite.Cases[0].Truth)
	wrong = append(slices.Clone(wrong[:2]), wrong[3:]...)
	wrong[1].EndMS = wrong[2].StartMS
	results[0].Stitches = []fillerstructurewindow.StitchResult{
		stitchForTimeline(t, suite.Cases[0].MediaSet, assessorProfile("assessor-a", "family-a"), wrong),
		stitchForTimeline(t, suite.Cases[0].MediaSet, assessorProfile("assessor-b", "family-b"), wrong),
	}
	report, err = Certify(suite, results, time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusFailed || report.WrongCases != 1 ||
		!slices.Contains(report.FailureCodes, "family_under_split") ||
		!slices.Contains(report.FailureCodes, "reducer_under_split") {
		t.Fatalf("wrong report = %+v", report)
	}
}

func TestSuiteRejectsDeclaredRatherThanMeasuredTraits(t *testing.T) {
	suite, _ := certificationFixture(t)
	suite.Cases[0].MeasuredEvidence = nil
	suite.SHA256 = SuiteSHA256(suite)
	if err := ValidateSuite(suite); err == nil {
		t.Fatal("suite accepted unmeasured content traits")
	}
}

func TestCertifyRejectsUnreplayableStitch(t *testing.T) {
	suite, results := certificationFixture(t)
	results[0].Stitches[0].Segments[0].EndMS++
	if _, err := Certify(suite, results, time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("certification accepted a tampered stitch")
	}
}

func certificationFixture(t *testing.T) (Suite, []CaseResult) {
	t.Helper()
	cases := make([]Case, MinimumSliceCases)
	results := make([]CaseResult, MinimumSliceCases)
	truth := certificationTruth()
	for index := range MinimumSliceCases {
		set := certificationMediaSet(t, index)
		item := Case{
			ID: fmt.Sprintf("case-%02d", index), MediaSet: set, Truth: slices.Clone(truth),
			MeasuredEvidence: []MeasuredSliceEvidence{
				{
					Slice: SliceWordlessJoin, EvidenceContract: WordlessEvidenceContract,
					EvidenceSHA256: fixtureDigest(400 + index), TargetBoundaryMS: 110_000, TargetWindowOrdinal: -1,
				},
				{
					Slice: SliceHighMotionWindow, EvidenceContract: MotionEvidenceContract,
					EvidenceSHA256: fixtureDigest(500 + index), TargetWindowOrdinal: 1,
				},
			},
		}
		cases[index] = item
		results[index] = CaseResult{CaseID: item.ID, Stitches: []fillerstructurewindow.StitchResult{
			stitchForTimeline(t, set, assessorProfile("assessor-a", "family-a"), truth),
			stitchForTimeline(t, set, assessorProfile("assessor-b", "family-b"), truth),
		}}
	}
	suite, err := NewSuite(cases)
	if err != nil {
		t.Fatal(err)
	}
	return suite, results
}

func certificationTruth() []fillerstructure.Segment {
	return []fillerstructure.Segment{
		{StartMS: 0, EndMS: 110_000, Role: fillerstructure.RoleProgrammeFragment},
		{StartMS: 110_000, EndMS: 118_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 118_000, EndMS: 120_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 120_000, EndMS: 150_000, Role: fillerstructure.RolePromo},
		{StartMS: 150_000, EndMS: 260_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 260_000, EndMS: 360_000, Role: fillerstructure.RoleProgrammeFragment},
	}
}

func certificationMediaSet(t *testing.T, seed int) fillerstructurewindow.MediaSet {
	t.Helper()
	source := fillerstructure.Source{SHA256: fixtureDigest(seed + 1), Bytes: int64(100_000_000 + seed), DurationMS: 360_000}
	plan, err := fillerstructurewindow.NewPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	media := make([]fillerstructure.AssessmentMedia, len(plan.Windows))
	for ordinal, window := range plan.Windows {
		media[ordinal] = fillerstructure.AssessmentMedia{
			SHA256:        fixtureDigest(1000 + seed*10 + ordinal),
			Bytes:         int64(10<<20) + int64(ordinal*1000),
			DurationMS:    window.MediaEndMS - window.MediaStartMS,
			ProfileSHA256: plan.Profile.AssessmentMediaProfileSHA256,
			LineageSHA256: fixtureDigest(2000 + seed*10 + ordinal),
		}
		if ordinal == 0 {
			media[ordinal].Bytes = int64(30<<20) + int64(seed)
		}
	}
	set, err := fillerstructurewindow.NewMediaSet(plan, media)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func stitchForTimeline(t *testing.T, set fillerstructurewindow.MediaSet, profile fillerstructure.AssessorProfile, timeline []fillerstructure.Segment) fillerstructurewindow.StitchResult {
	t.Helper()
	assessments := make([]fillerstructurewindow.Assessment, len(set.Plan.Windows))
	for ordinal, window := range set.Plan.Windows {
		var clipped []fillerstructure.Segment
		for _, segment := range timeline {
			start := max(segment.StartMS, window.MediaStartMS)
			end := min(segment.EndMS, window.MediaEndMS)
			if start < end {
				clipped = append(clipped, fillerstructure.Segment{StartMS: start, EndMS: end, Role: segment.Role})
			}
		}
		assessment, err := fillerstructurewindow.NewAssessment(fillerstructurewindow.AssessmentInput{
			MediaSet: set, WindowOrdinal: ordinal, Assessor: profile, Segments: clipped,
			AssessedAt: time.Date(2026, 9, 7, 10, 0, ordinal, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		assessments[ordinal] = assessment
	}
	stitch, err := fillerstructurewindow.Stitch(set, assessments, BoundaryToleranceMS)
	if err != nil {
		t.Fatal(err)
	}
	return stitch
}

func assessorProfile(id, family string) fillerstructure.AssessorProfile {
	return fillerstructure.AssessorProfile{
		ID: id, ModelFamily: family, Provider: "provider", Model: family + "/model",
		ModelDigest: strings.Repeat("d", 64), CapabilitySHA256: strings.Repeat("e", 64),
		PromptVersion: "window-v1", EvidenceContract: "window-evidence-v1",
	}
}

func fixtureDigest(value int) string { return fmt.Sprintf("%064x", value) }
