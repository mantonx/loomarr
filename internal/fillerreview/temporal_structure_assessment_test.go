package fillerreview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func TestLoadTemporalStructureAssessmentRequiresCompleteBoundAuthority(t *testing.T) {
	challenge := newTemporalStructureComparisonFixture(t)
	set := challenge.assessmentSet("assessor-a", "qwen", "qwen/model")
	path := writeTemporalHumanJSON(t, t.TempDir(), "assessment.json", set)
	loaded, err := loadTemporalStructureAssessment(path, challenge.manifest, challenge.publicSHA, challenge.authoritySHA)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.fileSHA == "" || len(loaded.byAlias) != 3 || loaded.set.Assessor.ID != "assessor-a" {
		t.Fatalf("loaded assessment = %+v", loaded)
	}
}

func TestLoadTemporalStructureAssessmentRejectsSemanticAndAccountingDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*temporalStructureComparisonFixture, *TemporalStructureAssessmentSet)
		want   string
	}{
		{name: "manifest digest", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			set.PublicManifestSHA256 = strings.Repeat("f", 64)
		}, want: "identity"},
		{name: "production disposition", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			set.ProductionAdmissionAllowed = true
		}, want: "identity"},
		{name: "missing case", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			set.Assessments = set.Assessments[:2]
		}, want: "supplied 2 cases"},
		{name: "unknown alias", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			set.Assessments[0].Alias = "case-unknown"
		}, want: "unknown alias"},
		{name: "out of range evidence", mutate: func(f *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			set.Assessments[0].Unit.DecisiveAtMS = []int64{f.durationByAlias[set.Assessments[0].Alias] + 1}
		}, want: "decisive timestamps"},
		{name: "unsorted evidence", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			set.Assessments[0].Unit.DecisiveAtMS = []int64{2, 1}
		}, want: "decisive timestamps"},
		{name: "non standalone role", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			item := temporalStructureAssessmentByTruth(set, fillereval.UnitCompilation)
			item.Role = &TemporalStructureRoleClaim{Kind: fillereval.TemporalRolePromo, DecisiveAtMS: []int64{1}, Reason: "wrong"}
		}, want: "non-standalone"},
		{name: "segment gap", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			item := temporalStructureAssessmentByTruth(set, fillereval.UnitCompilation)
			item.Segments[1].StartMS++
		}, want: "breaks complete coverage"},
		{name: "segment overlap", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			item := temporalStructureAssessmentByTruth(set, fillereval.UnitCompilation)
			item.Segments[1].StartMS--
		}, want: "breaks complete coverage"},
		{name: "segment evidence outside interval", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			item := temporalStructureAssessmentByTruth(set, fillereval.UnitCompilation)
			item.Segments[0].DecisiveAtMS = []int64{item.Segments[0].EndMS + 1}
		}, want: "outside its interval"},
		{name: "programme segment not explicit", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			item := temporalStructureAssessmentByTruth(set, fillereval.UnitProgrammeExcerpt)
			item.Segments[0].Role = fillereval.TemporalSegmentNonFiller
		}, want: "explicit programme_fragment"},
		{name: "failure mixed with label", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			item := &set.Assessments[0]
			item.OperationalFailure = &fillereval.TemporalOperationalFailure{Code: fillereval.TemporalFailureTimeout, Detail: "timeout", Retryable: true}
			item.Inference.Calls[len(item.Inference.Calls)-1].OperationalFailure = fillereval.TemporalFailureTimeout
		}, want: "mixed"},
		{name: "aggregate accounting", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			set.Assessments[0].Inference.PromptTokens++
		}, want: "aggregate accounting"},
		{name: "inference after completion", mutate: func(_ *temporalStructureComparisonFixture, set *TemporalStructureAssessmentSet) {
			set.Assessments[0].Inference.AssessedAt = set.CompletedAt.Add(time.Second)
		}, want: "inference timing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			challenge := newTemporalStructureComparisonFixture(t)
			set := challenge.assessmentSet("assessor-a", "qwen", "qwen/model")
			test.mutate(&challenge, &set)
			path := writeTemporalHumanJSON(t, t.TempDir(), "assessment.json", set)
			if _, err := loadTemporalStructureAssessment(path, challenge.manifest, challenge.publicSHA, challenge.authoritySHA); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadTemporalStructureAssessmentAllowsUnusableSegmentWithoutDecisiveEvidence(t *testing.T) {
	challenge := newTemporalStructureComparisonFixture(t)
	set := challenge.assessmentSet("assessor-a", "qwen", "qwen/model")
	item := temporalStructureAssessmentByTruth(&set, fillereval.UnitCompilation)
	item.Segments[0].Role = fillereval.TemporalSegmentUnusable
	item.Segments[0].DecisiveAtMS = nil
	path := writeTemporalHumanJSON(t, t.TempDir(), "assessment.json", set)
	if _, err := loadTemporalStructureAssessment(path, challenge.manifest, challenge.publicSHA, challenge.authoritySHA); err != nil {
		t.Fatal(err)
	}
}

func TestLoadTemporalStructureAssessmentRejectsUnknownFields(t *testing.T) {
	challenge := newTemporalStructureComparisonFixture(t)
	set := challenge.assessmentSet("assessor-a", "qwen", "qwen/model")
	raw, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	version := fmt.Sprintf(`"schemaVersion":%d`, TemporalStructureAssessmentSchemaVersion)
	raw = []byte(strings.Replace(string(raw), version, version+`,"futureField":true`, 1))
	path := filepath.Join(t.TempDir(), "assessment.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTemporalStructureAssessment(path, challenge.manifest, challenge.publicSHA, challenge.authoritySHA); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

type temporalStructureComparisonFixture struct {
	root            string
	manifestPath    string
	authorityPath   string
	manifest        TemporalStructureChallengeManifest
	authority       TemporalStructureChallengeAuthority
	publicSHA       string
	authoritySHA    string
	durationByAlias map[string]int64
}

func newTemporalStructureComparisonFixture(t *testing.T) temporalStructureComparisonFixture {
	t.Helper()
	structure := newTemporalStructureFixture(t)
	root, result := structure.build(t, "comparison-seed")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	authorityPath := filepath.Join(root, "private", "authority.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	authority := readStrictTestJSON[TemporalStructureChallengeAuthority](t, authorityPath)
	durations := make(map[string]int64, len(manifest.Cases))
	for _, item := range manifest.Cases {
		durations[item.Alias] = item.Video.DurationMS
	}
	return temporalStructureComparisonFixture{
		root: root, manifestPath: manifestPath, authorityPath: authorityPath, manifest: manifest, authority: authority,
		publicSHA: result.PublicManifestSHA256, authoritySHA: result.AuthoritySHA256, durationByAlias: durations,
	}
}

func (fixture temporalStructureComparisonFixture) assessmentSet(id, family, model string) TemporalStructureAssessmentSet {
	completedAt := fixture.manifest.GeneratedAt.Add(2 * time.Hour)
	set := TemporalStructureAssessmentSet{
		SchemaVersion: TemporalStructureAssessmentSchemaVersion, ContractVersion: TemporalStructureAssessmentContractVersion,
		ChallengeID: fixture.manifest.ChallengeID, PublicManifestSHA256: fixture.publicSHA, PrivateAuthoritySHA256: fixture.authoritySHA,
		RawResultSHA256: strings.Repeat("c", 64), SnapshotFileSHA256: strings.Repeat("b", 64),
		CapabilitySnapshotSHA256: strings.Repeat("d", 64), CompletedAt: completedAt, LockedAt: completedAt.Add(time.Hour),
		Assessor: fillereval.TemporalAssessorIdentity{
			ID: id, Provider: "provider", Model: model, ModelFamily: family, ModelDigest: strings.Repeat("e", 64), PromptVersion: "structure-v1",
		},
	}
	for _, truth := range fixture.authority.Cases {
		duration := fixture.durationByAlias[truth.Alias]
		decisive := []int64{1_000}
		switch truth.Unit {
		case fillereval.UnitCompilation:
			decisive = []int64{truth.JoinTimesMS[0] - 100}
		case fillereval.UnitProgrammeExcerpt:
			decisive = []int64{100, duration - 100}
		}
		assessment := TemporalStructureAssessment{
			Alias: truth.Alias, Unit: &TemporalStructureUnitClaim{Kind: truth.Unit, DecisiveAtMS: decisive, Reason: "closed unit"},
			Inference: temporalStructureTestInference(completedAt.Add(-time.Minute), false),
		}
		switch truth.Unit {
		case fillereval.UnitStandalone:
			assessment.Role = &TemporalStructureRoleClaim{Kind: truth.Role, DecisiveAtMS: []int64{1_500}, Reason: "closed role"}
			assessment.Segments = []TemporalStructureSegmentClaim{{StartMS: 0, EndMS: duration, Role: fillereval.TemporalSegmentRole(truth.Role), DecisiveAtMS: []int64{1_500}, Reason: "closed role"}}
		case fillereval.UnitCompilation:
			for _, part := range truth.Segments {
				assessment.Segments = append(assessment.Segments, TemporalStructureSegmentClaim{
					StartMS: part.OutputStartMS, EndMS: part.OutputEndMS,
					Role:         fillereval.TemporalSegmentRole(part.SourceRole),
					DecisiveAtMS: []int64{part.OutputStartMS + min(int64(1_000), part.RenderedMS/2)}, Reason: "constructed bounded item",
				})
			}
		default:
			assessment.Segments = []TemporalStructureSegmentClaim{{StartMS: 0, EndMS: duration, Role: fillereval.TemporalSegmentProgrammeFragment, DecisiveAtMS: []int64{100}, Reason: "programme dependency"}}
		}
		set.Assessments = append(set.Assessments, assessment)
	}
	return set
}

func temporalStructureTestInference(at time.Time, _ bool) fillereval.TemporalInference {
	calls := []fillereval.TemporalInferenceCall{{Axis: "structure", Attempt: 1, ResponseSHA256: strings.Repeat("1", 64), LatencyMS: 10, PromptTokens: 20, CompletionTokens: 5}}
	result := fillereval.TemporalInference{AssessedAt: at, Attempts: len(calls), Calls: calls}
	for _, call := range calls {
		result.LatencyMS += call.LatencyMS
		result.PromptTokens += call.PromptTokens
		result.CompletionTokens += call.CompletionTokens
	}
	return result
}

func temporalStructureAssessmentByTruth(set *TemporalStructureAssessmentSet, unit fillereval.UnitKind) *TemporalStructureAssessment {
	for index := range set.Assessments {
		if set.Assessments[index].Unit != nil && set.Assessments[index].Unit.Kind == unit {
			return &set.Assessments[index]
		}
	}
	panic("test fixture unit disappeared")
}
