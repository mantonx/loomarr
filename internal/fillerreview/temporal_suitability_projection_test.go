package fillerreview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func TestPublishTemporalSuitabilityProjectionQuarantinesSourceAcrossDerivatives(t *testing.T) {
	fixture := newTemporalSuitabilityProjectionFixture(t)
	firstOutput := filepath.Join(t.TempDir(), "projection.json")
	secondOutput := filepath.Join(t.TempDir(), "projection.json")
	config := fixture.config(firstOutput)
	report, firstSHA, err := PublishTemporalSuitabilityProjection(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PublishTemporalSuitabilityProjection(config); err == nil {
		t.Fatal("projection overwrote an immutable output")
	}
	config.OutputPath = secondOutput
	repeat, secondSHA, err := PublishTemporalSuitabilityProjection(config)
	if err != nil {
		t.Fatal(err)
	}
	if firstSHA != secondSHA || !reflect.DeepEqual(report, repeat) {
		t.Fatal("frozen suitability projection did not reproduce byte-for-byte")
	}
	if report.ProhibitedSources != 1 || report.CoverageHoldSources != 1 || report.CandidateNoSignalSources != 1 || report.ProhibitedCases != 2 || report.CoverageHoldCases != 1 || report.TrainingAllowed || report.IngestionAllowed || report.SchedulingAllowed || report.ProductionAdmissionAllowed {
		t.Fatalf("report = %+v", report)
	}

	var bounded, other, programme TemporalSuitabilitySourceDisposition
	for _, item := range report.SourceDispositions {
		switch item.SourceID {
		case "bounded-commercial-secret":
			bounded = item
		case "bounded-promo-secret":
			other = item
		case "programme-parent-secret":
			programme = item
		}
	}
	if bounded.Disposition != TemporalSuitabilityDispositionProhibited || len(bounded.DerivedAliases) != 2 || len(bounded.Observations) != 1 || len(bounded.Observations[0].Witnesses) != 2 {
		t.Fatalf("quarantined source = %+v", bounded)
	}
	if other.Disposition != TemporalSuitabilityDispositionCandidate || programme.Disposition != TemporalSuitabilityDispositionCoverage {
		t.Fatalf("non-flagged sources were over-cleared or over-quarantined: other=%+v programme=%+v", other, programme)
	}
	for _, item := range report.CaseDispositions {
		if containsSortedString(item.SourceIDs, bounded.SourceID) && item.EffectiveDisposition != TemporalSuitabilityDispositionProhibited {
			t.Fatalf("derivative did not inherit source quarantine: %+v", item)
		}
	}
	report.SourceDispositions[0].Observations[0].Witnesses[0].EvidenceAlias = "unknown-derivative"
	if err := validateTemporalSuitabilityProjectionReport(report); err == nil {
		t.Fatal("projection accepted a witness outside its source derivatives")
	}
}

func TestProjectTemporalSuitabilityObservationSplitsCrossSegmentRange(t *testing.T) {
	item := TemporalStructureChallengeAuthorityCase{
		Alias: "case-cross", Segments: []TemporalStructureChallengeAuthorityPart{
			{SourceID: "first", SourceDurationMS: 10_000, RequestedMS: 10_000, RenderedMS: 10_000, OutputStartMS: 0, OutputEndMS: 10_000},
			{SourceID: "second", SourceDurationMS: 12_000, RequestedMS: 12_000, RenderedMS: 12_000, OutputStartMS: 10_000, OutputEndMS: 22_000},
		},
	}
	projected, err := projectTemporalSuitabilityObservation(item, TemporalSuitabilityObservation{
		Kind: SuitabilityHatefulOrDegradingSlur, Modality: SuitabilityModalityAudio, StartMS: 9_500, EndMS: 10_500,
	}, "assessor")
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 2 || projected["first"][0].StartMS != 9_500 || projected["first"][0].EndMS != 10_000 || projected["second"][0].StartMS != 0 || projected["second"][0].EndMS != 500 {
		t.Fatalf("cross-segment projection = %+v", projected)
	}
}

func TestPublishTemporalSuitabilityComparisonRejectsTimeBeforeCompletedResult(t *testing.T) {
	fixture := newTemporalSuitabilityProjectionFixture(t)
	_, _, err := PublishTemporalSuitabilityComparison(TemporalSuitabilityComparisonConfig{
		EvidenceManifestPath: fixture.manifest, StructureAuthorityPath: fixture.authority,
		FirstResultPath: fixture.first, SecondResultPath: fixture.second,
		ComparedAt: fixture.projectedAt.Add(-3 * time.Hour), ExpectedCases: 3,
		OutputPath: filepath.Join(t.TempDir(), "comparison.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "predates") {
		t.Fatalf("error = %v", err)
	}
}

func TestPublishTemporalSuitabilityProjectionFailsClosedOnAmbiguousSourceAndUnmappedObservation(t *testing.T) {
	t.Run("ambiguous source", func(t *testing.T) {
		fixture := newTemporalSuitabilityProjectionFixture(t)
		authority := readStrictTestJSON[TemporalStructureChallengeAuthority](t, fixture.authority)
		for caseIndex := range authority.Cases {
			for partIndex := range authority.Cases[caseIndex].Segments {
				part := &authority.Cases[caseIndex].Segments[partIndex]
				if part.SourceID == "bounded-commercial-secret" && len(authority.Cases[caseIndex].Segments) > 1 {
					part.SourceSHA256 = strings.Repeat("f", 64)
				}
			}
		}
		writeTemporalSuitabilityProjectionJSON(t, fixture.authority, authority)
		_, _, err := PublishTemporalSuitabilityProjection(fixture.config(filepath.Join(t.TempDir(), "projection.json")))
		if err == nil || !strings.Contains(err.Error(), "ambiguous authority") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("unmapped observation", func(t *testing.T) {
		fixture := newTemporalSuitabilityProjectionFixture(t)
		authority := readStrictTestJSON[TemporalStructureChallengeAuthority](t, fixture.authority)
		var excerptAlias string
		for caseIndex := range authority.Cases {
			if authority.Cases[caseIndex].Unit == fillereval.UnitProgrammeExcerpt {
				excerptAlias = authority.Cases[caseIndex].Alias
				part := &authority.Cases[caseIndex].Segments[0]
				part.RenderedMS -= 500
				part.OutputEndMS -= 500
			}
		}
		writeTemporalSuitabilityProjectionJSON(t, fixture.authority, authority)
		first := readStrictTestJSON[TemporalSuitabilityResult](t, fixture.first)
		setTemporalSuitabilityProjectionFlag(&first, excerptAlias, TemporalSuitabilityObservation{Kind: SuitabilityExplicitNudity, Modality: SuitabilityModalityVideo, StartMS: 19_700, EndMS: 19_900})
		writeTemporalSuitabilityProjectionResultFile(t, fixture.first, first)
		fixture.rebuildComparison(t)
		_, _, err := PublishTemporalSuitabilityProjection(fixture.config(filepath.Join(t.TempDir(), "projection.json")))
		if err == nil || !strings.Contains(err.Error(), "overlaps no construction segment") {
			t.Fatalf("error = %v", err)
		}
	})
}

type temporalSuitabilityProjectionFixture struct {
	manifest, authority, first, second, comparison string
	projectedAt                                    time.Time
}

func newTemporalSuitabilityProjectionFixture(t *testing.T) temporalSuitabilityProjectionFixture {
	t.Helper()
	structure := newTemporalStructureFixture(t)
	root, _ := structure.build(t, "projection-seed")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	authorityPath := filepath.Join(root, "private", "authority.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	authority := readStrictTestJSON[TemporalStructureChallengeAuthority](t, authorityPath)
	manifestSHA, err := hashFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	aliases := temporalStructureAliases(manifest)
	sort.Strings(aliases)
	completedAt := manifest.GeneratedAt.Add(time.Hour)
	firstAssessor := fillereval.TemporalAssessorIdentity{ID: "first", Provider: "openrouter", Model: "model-one", ModelFamily: "family-one", ModelDigest: "digest-one", PromptVersion: "prompt-one"}
	secondAssessor := fillereval.TemporalAssessorIdentity{ID: "second", Provider: "openrouter", Model: "model-two", ModelFamily: "family-two", ModelDigest: "digest-two", PromptVersion: "prompt-two"}
	firstPath := writeTemporalSuitabilityProjectionResult(t, root, "first", manifestSHA, aliases, firstAssessor, completedAt)
	secondPath := writeTemporalSuitabilityProjectionResult(t, root, "second", manifestSHA, aliases, secondAssessor, completedAt)

	standaloneAlias, compilationAlias, excerptAlias := "", "", ""
	for _, item := range authority.Cases {
		switch item.Unit {
		case fillereval.UnitStandalone:
			standaloneAlias = item.Alias
		case fillereval.UnitCompilation:
			compilationAlias = item.Alias
		case fillereval.UnitProgrammeExcerpt:
			excerptAlias = item.Alias
		}
	}
	first := readStrictTestJSON[TemporalSuitabilityResult](t, firstPath)
	setTemporalSuitabilityProjectionFlag(&first, standaloneAlias, TemporalSuitabilityObservation{Kind: SuitabilityExplicitNudity, Modality: SuitabilityModalityVideo, StartMS: 2_000, EndMS: 3_000})
	setTemporalSuitabilityProjectionFlag(&first, compilationAlias, TemporalSuitabilityObservation{Kind: SuitabilityExplicitNudity, Modality: SuitabilityModalityVideo, StartMS: 2_200, EndMS: 2_800})
	setTemporalSuitabilityProjectionCoverage(&first, excerptAlias)
	writeTemporalSuitabilityProjectionResultFile(t, firstPath, first)
	second := readStrictTestJSON[TemporalSuitabilityResult](t, secondPath)
	setTemporalSuitabilityProjectionCoverage(&second, excerptAlias)
	writeTemporalSuitabilityProjectionResultFile(t, secondPath, second)

	fixture := temporalSuitabilityProjectionFixture{
		manifest: manifestPath, authority: authorityPath, first: firstPath, second: secondPath,
		comparison: filepath.Join(root, "comparison.json"), projectedAt: completedAt.Add(2 * time.Hour),
	}
	fixture.rebuildComparison(t)
	return fixture
}

func (fixture temporalSuitabilityProjectionFixture) config(output string) TemporalSuitabilityProjectionConfig {
	return TemporalSuitabilityProjectionConfig{
		PublicManifestPath: fixture.manifest, StructureAuthorityPath: fixture.authority,
		SuitabilityComparisonPath: fixture.comparison, FirstResultPath: fixture.first, SecondResultPath: fixture.second,
		ExpectedCases: 3, ProjectedAt: fixture.projectedAt, OutputPath: output,
	}
}

func (fixture temporalSuitabilityProjectionFixture) rebuildComparison(t *testing.T) {
	t.Helper()
	_ = os.Remove(fixture.comparison)
	_, _, err := PublishTemporalSuitabilityComparison(TemporalSuitabilityComparisonConfig{
		EvidenceManifestPath: fixture.manifest, StructureAuthorityPath: fixture.authority,
		FirstResultPath: fixture.first, SecondResultPath: fixture.second,
		ComparedAt: fixture.projectedAt.Add(-time.Hour), ExpectedCases: 3, OutputPath: fixture.comparison,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func writeTemporalSuitabilityProjectionResult(t *testing.T, root, name, evidenceSHA string, aliases []string, assessor fillereval.TemporalAssessorIdentity, completedAt time.Time) string {
	t.Helper()
	path := filepath.Join(root, name+".json")
	result := TemporalSuitabilityResult{
		SchemaVersion: TemporalSuitabilityResultSchemaVersion, ContractVersion: TemporalSuitabilityResultContract,
		EvidenceManifestSHA256: evidenceSHA, SelectionSHA256: temporalTruthJSONSHA(aliases), Assessor: assessor,
		SelectionAliases: append([]string(nil), aliases...), Requests: len(aliases), CompletedAt: completedAt, ProductionAdmissionAllowed: false,
	}
	responseRoot := filepath.Join(path+".private", temporalSuitabilityResponsesDir)
	if err := os.MkdirAll(responseRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for index, alias := range aliases {
		response := []byte(`{"fixture":` + fmt.Sprintf("%d", index) + `}`)
		responseSHA := hashBytes(response)
		responsePath := filepath.Join(responseRoot, alias+".json")
		if err := os.WriteFile(responsePath, response, 0o600); err != nil {
			t.Fatal(err)
		}
		inference := fillereval.TemporalInference{AssessedAt: completedAt, Attempts: 1, Calls: []fillereval.TemporalInferenceCall{{Axis: "suitability", Attempt: 1, ResponseSHA256: responseSHA}}}
		result.Assessments = append(result.Assessments, TemporalSuitabilityAssessment{
			EvidenceAlias: alias, VisualAssessment: suitabilityVisualCompleted, SpokenLanguageAssessment: suitabilityLanguageCompleted,
			Outcome: SuitabilityOutcomeNoSignalObserved, RawResponseSHA256: responseSHA, Inference: inference,
		})
		result.Attempts = append(result.Attempts, SuitabilityOpenRouterAttempt{
			EvidenceAlias: alias, ResponseSHA256: responseSHA,
			RawResponsePath: filepath.ToSlash(filepath.Join(temporalSuitabilityResponsesDir, alias+".json")),
		})
	}
	writeTemporalSuitabilityProjectionResultFile(t, path, result)
	return path
}

func writeTemporalSuitabilityProjectionResultFile(t *testing.T, path string, result TemporalSuitabilityResult) {
	t.Helper()
	writeTemporalSuitabilityProjectionJSON(t, path, result)
}

func writeTemporalSuitabilityProjectionJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func setTemporalSuitabilityProjectionFlag(result *TemporalSuitabilityResult, alias string, flag TemporalSuitabilityObservation) {
	for index := range result.Assessments {
		if result.Assessments[index].EvidenceAlias == alias {
			result.Assessments[index].Flags = []TemporalSuitabilityObservation{flag}
			result.Assessments[index].Outcome = SuitabilityOutcomeProhibitedSignal
		}
	}
}

func setTemporalSuitabilityProjectionCoverage(result *TemporalSuitabilityResult, alias string) {
	for index := range result.Assessments {
		if result.Assessments[index].EvidenceAlias == alias {
			result.Assessments[index].SpokenLanguageAssessment = suitabilityLanguageInsufficient
			result.Assessments[index].Outcome = SuitabilityOutcomeCoverageHold
		}
	}
}
