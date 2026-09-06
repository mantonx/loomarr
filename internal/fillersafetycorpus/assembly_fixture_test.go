package fillersafetycorpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

type assemblyFixture struct {
	root, planPath, output string
	plan                   AssemblyPlan
	cohorts                []PreparedCohort
}

func newAssemblyFixture(t *testing.T) *assemblyFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	preparedAt := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	policy := writeAuthorityFile(t, root, "policy.json", []byte("private spoken-safety policy fixture"))
	positiveSlices := []string{
		fillersafetycert.SliceAccentLocale, fillersafetycert.SliceClipping, fillersafetycert.SliceCodecTransform,
		fillersafetycert.SliceDerivativeCompilation, fillersafetycert.SliceMusicOverlap,
		fillersafetycert.SliceNoise, fillersafetycert.SlicePartialToken, fillersafetycert.SlicePhoneticConfusable,
		fillersafetycert.SlicePlacement,
		fillersafetycert.SliceQuietSpeech, fillersafetycert.SliceSpeedPitch,
	}
	positive := makePreparedFixtureCohort(t, root, "01-positive", PreparedCohortKindPositiveCandidate,
		"consented-known-script", 59, preparedAt, policy.SHA256, func(index int) []string {
			return []string{positiveSlices[index%len(positiveSlices)]}
		})
	target := makePreparedFixtureCohort(t, root, "02-target", PreparedCohortKindCleanCandidate,
		VCTKDatasetID, 100, preparedAt, policy.SHA256, func(int) []string {
			return []string{fillersafetycert.SliceTargetLocale}
		})
	cleanSlices := []string{fillersafetycert.SliceMusicOnly, fillersafetycert.SliceNearMatch, fillersafetycert.SliceWordless}
	other := makePreparedFixtureCohort(t, root, "03-other-clean", PreparedCohortKindCleanCandidate,
		"curated-other-clean", len(cleanSlices), preparedAt, policy.SHA256, func(index int) []string {
			return []string{cleanSlices[index]}
		})
	cohorts := []PreparedCohort{positive, target, other}
	plan := AssemblyPlan{
		SchemaVersion: AssemblyPlanSchemaVersion, ContractVersion: AssemblyPlanContractVersion,
		AssembledAt: preparedAt.Add(time.Hour), ChallengeKind: fillersafetycert.ChallengeCertification,
		Policy: policy, ProposerSHA256: fixtureSHA(6000), ProposerFamily: "complete-audio-window-proposer",
		Implementation: "spoken-safety-evaluator-v1",
		AudioRoute:     assemblyFixtureRoute([]string{"audio"}, "native-audio", 6100),
		VideoRoute:     assemblyFixtureRoute([]string{"audio", "video"}, "complete-video", 6200),
		ExpectedCases:  162, MaximumInputBytes: 64 << 20, MaximumOutputBytes: 64 << 20,
		MaximumWallTimeMS: int64(time.Minute / time.Millisecond),
	}
	for index, cohort := range cohorts {
		rootName := []string{"01-positive", "02-target", "03-other-clean"}[index]
		cohortPath := filepath.ToSlash(filepath.Join(rootName, "cohort.json"))
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cohortPath)))
		if err != nil {
			t.Fatal(err)
		}
		plan.Cohorts = append(plan.Cohorts, AssemblyCohort{
			CohortPath: cohortPath, SourceRoot: rootName, SHA256: bytesSHA(raw), Kind: cohort.Kind,
			Dataset: cohort.Dataset, ExpectedCases: len(cohort.Cases), MaximumBytes: 16 << 20,
		})
	}
	planPath := filepath.Join(root, "assembly-plan.json")
	writePrivateJSONFixture(t, planPath, plan)
	return &assemblyFixture{root: root, planPath: planPath, output: filepath.Join(root, "assembled"), plan: plan, cohorts: cohorts}
}

func makePreparedFixtureCohort(
	t *testing.T,
	inputRoot, cohortRoot, kind, dataset string,
	count int,
	preparedAt time.Time,
	policySHA string,
	slicesFor func(int) []string,
) PreparedCohort {
	t.Helper()
	root := filepath.Join(inputRoot, cohortRoot)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rights := writeAuthorityFile(t, root, "evidence/rights.json", []byte("private rights "+cohortRoot))
	toolSeed := len(cohortRoot) * 100
	cohort := PreparedCohort{
		SchemaVersion: PreparedCohortSchemaVersion, ContractVersion: PreparedCohortContractVersion,
		PreparedAt: preparedAt, Kind: kind, Dataset: dataset, ReleaseAuthoritySHA256: fixtureSHA(toolSeed + 1),
		RecipeSHA256: fixtureSHA(toolSeed + 2),
		FFmpeg:       fillersafety.ToolIdentity{Version: "ffmpeg fixture " + cohortRoot, BinarySHA256: fixtureSHA(toolSeed + 3)},
		FFprobe:      fillersafety.ToolIdentity{Version: "ffprobe fixture " + cohortRoot, BinarySHA256: fixtureSHA(toolSeed + 4)},
	}
	for index := 0; index < count; index++ {
		caseID := fmt.Sprintf("%s-case-%03d", cohortRoot, index+1)
		caseRoot := filepath.ToSlash(filepath.Join("cases", caseID))
		source := writeAuthorityFile(t, root, caseRoot+"/source.mp4", []byte(fmt.Sprintf("complete audiovisual %s %03d", cohortRoot, index+1)))
		transcript := writeAuthorityFile(t, root, caseRoot+"/transcript.txt", []byte(fmt.Sprintf("private transcript %s %03d", cohortRoot, index+1)))
		provenance := writeAuthorityFile(t, root, caseRoot+"/provenance.json", []byte(fmt.Sprintf("private provenance %s %03d", cohortRoot, index+1)))
		item := PreparedCohortCase{
			CaseID: caseID, SourcePath: source.Path,
			SourceAuthority: fillersafety.SourceAuthority{
				SchemaVersion: fillersafety.SourceAuthoritySchemaVersion, PolicySHA256: policySHA,
				Implementation: "spoken-safety-evaluator-v1", SourceID: caseID, SourceSHA256: source.SHA256,
				SourceBytes: source.Bytes, DurationMS: 1_000, HasAudio: true, HasVideo: true,
				MeasuredAt: preparedAt, FFmpeg: cohort.FFmpeg, FFprobe: cohort.FFprobe,
			},
			SourceFamily:   fmt.Sprintf("%s-family-%03d", cohortRoot, index+1),
			TranscriptPath: transcript.Path, TranscriptSHA256: transcript.SHA256, TranscriptBytes: transcript.Bytes,
			TruthProvenancePath: provenance.Path, TruthProvenanceSHA256: provenance.SHA256,
			TruthProvenanceBytes: provenance.Bytes, RightsPath: rights.Path, RightsSHA256: rights.SHA256,
			RightsBytes: rights.Bytes, Claim: kind, Locale: "en-US", Slices: slicesFor(index),
		}
		if kind == PreparedCohortKindPositiveCandidate {
			item.PositiveIntervals = []PreparedPositiveInterval{{RuleID: "rule-000000000000000000000001", StartMS: 100, EndMS: 900}}
		}
		cohort.Cases = append(cohort.Cases, item)
	}
	slices.SortFunc(cohort.Cases, func(a, b PreparedCohortCase) int { return strings.Compare(a.CaseID, b.CaseID) })
	writePrivateJSONFixture(t, filepath.Join(root, "cohort.json"), cohort)
	return cohort
}

func assemblyFixtureRoute(modalities []string, rung string, seed int) fillersafetycert.RouteAuthority {
	return fillersafetycert.RouteAuthority{
		Role: "spoken-safety", Rung: rung, Modalities: modalities,
		RequestedProvider: "openrouter", RequestedModel: "vendor/model",
		ResolvedProvider: "openrouter", ResolvedModel: "vendor/model-2026",
		UpstreamProvider: "provider", UpstreamProviderSlug: "provider/slug",
		ReasoningMode: fillersafetycert.ReasoningDisabled, ModelFamily: "family-" + rung,
		CapabilitySHA256: fixtureSHA(seed), PromptSHA256: fixtureSHA(seed + 1), SchemaSHA256: fixtureSHA(seed + 2),
	}
}

func rewriteAssemblyCohort(t *testing.T, fixture *assemblyFixture, index int) {
	t.Helper()
	authority := fixture.plan.Cohorts[index]
	path := filepath.Join(fixture.root, filepath.FromSlash(authority.CohortPath))
	writePrivateJSONFixture(t, path, fixture.cohorts[index])
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture.plan.Cohorts[index].SHA256 = bytesSHA(raw)
	rewriteAssemblyPlan(t, fixture)
}

func rewriteAssemblyPlan(t *testing.T, fixture *assemblyFixture) {
	t.Helper()
	writePrivateJSONFixture(t, fixture.planPath, fixture.plan)
}

func readAssemblyJSON[T any](t *testing.T, path string) T {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
