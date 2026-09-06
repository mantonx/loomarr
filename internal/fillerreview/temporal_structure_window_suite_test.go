package fillerreview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

func TestBuildTemporalStructureWindowCertificationSuiteUsesOnlyLockedPreModelEvidence(t *testing.T) {
	config, motion := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	result, err := BuildTemporalStructureWindowCertificationSuite(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cases != TemporalStructureWindowCorpusCases || result.WordlessCases < fillerstructurewindowcert.MinimumSliceCases ||
		result.HighMotionCases != fillerstructurewindowcert.MinimumSliceCases || result.WindowsMeasured <= result.Cases ||
		motion.calls != result.WindowsMeasured || !reviewSHA256(result.EvidenceSHA256) || !reviewSHA256(result.SuiteSHA256) {
		t.Fatalf("result=%+v motionCalls=%d", result, motion.calls)
	}
	suitePath := filepath.Join(config.OutputDir, "private", "suite.json")
	suite, _, err := LoadTemporalStructureWindowCertificationSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	if suite.SHA256 != result.SuiteSHA256 || len(suite.Cases) != result.Cases {
		t.Fatalf("suite identity drifted: %+v", suite)
	}
	evidence := readStrictTestJSON[TemporalStructureWindowMeasuredEvidence](t, filepath.Join(config.OutputDir, "private", "measured-evidence.json"))
	if evidence.SHA256 != result.EvidenceSHA256 || evidence.TrainingAllowed || evidence.ProductionAdmissionAllowed {
		t.Fatalf("measured evidence drifted: %+v", evidence)
	}
	for _, item := range suite.Cases {
		for _, measured := range item.MeasuredEvidence {
			if measured.EvidenceSHA256 != evidence.SHA256 {
				t.Fatalf("case %q does not bind measured evidence", item.ID)
			}
		}
	}

	repeatConfig, _ := temporalStructureWindowSuiteFixtureFromExisting(t, config, filepath.Join(t.TempDir(), "repeat"))
	repeat, err := BuildTemporalStructureWindowCertificationSuite(t.Context(), repeatConfig)
	if err != nil {
		t.Fatal(err)
	}
	if repeat != result {
		t.Fatalf("repeat result differs: first=%+v repeat=%+v", result, repeat)
	}
	for _, name := range []string{"measured-evidence.json", "suite.json"} {
		first, err := os.ReadFile(filepath.Join(config.OutputDir, "private", name))
		if err != nil {
			t.Fatal(err)
		}
		second, err := os.ReadFile(filepath.Join(repeatConfig.OutputDir, "private", name))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("repeated %s differs", name)
		}
	}
}

func TestBuildTemporalStructureWindowCertificationSuiteFailsAtomically(t *testing.T) {
	config, motion := temporalStructureWindowSuiteFixture(t, filepath.Join(t.TempDir(), "suite"))
	motion.failAt = 3
	_, err := BuildTemporalStructureWindowCertificationSuite(t.Context(), config)
	if err == nil || !strings.Contains(err.Error(), "fixture motion failure") {
		t.Fatalf("motion failure = %v", err)
	}
	if _, err := os.Lstat(config.OutputDir); !os.IsNotExist(err) {
		t.Fatalf("failed measurement published output: %v", err)
	}
}

func TestParseTemporalStructureWindowMicrolumaIsExactAndBounded(t *testing.T) {
	for _, test := range []struct {
		value string
		want  int64
	}{
		{value: "0", want: 0},
		{value: "3.05962", want: 3_059_620},
		{value: "0.0428834", want: 42_883},
		{value: "0.0428836", want: 42_884},
		{value: "3.76157e-05", want: 38},
		{value: "5.78704e-06", want: 6},
		{value: "6.07639E-05", want: 61},
		{value: "255.000000", want: 255_000_000},
	} {
		got, err := parseTemporalStructureWindowMicroluma(test.value)
		if err != nil || got != test.want {
			t.Fatalf("parse %q = %d, %v; want %d", test.value, got, err, test.want)
		}
	}
	for _, value := range []string{"", "-1", "+1", "256", "1.nope", "1.2.3", "1e", "1e+", "1e1e1", "2.56e2"} {
		if _, err := parseTemporalStructureWindowMicroluma(value); err == nil {
			t.Fatalf("invalid microluma %q accepted", value)
		}
	}
}

func TestTemporalStructureWindowWordlessEvidenceRequiresRetainedNonSpeechMarkersOnly(t *testing.T) {
	if temporalStructureWindowTranscriptIsWordless(nil) {
		t.Fatal("missing transcript was treated as wordless evidence")
	}
	markers := []fillerbakeoff.TranscriptSegment{
		{StartMS: 0, EndMS: 1_000, Text: "[Music]"},
		{StartMS: 1_000, EndMS: 2_000, Text: " [Applause] "},
	}
	if !temporalStructureWindowTranscriptIsWordless(markers) {
		t.Fatal("closed non-speech markers were not treated as wordless evidence")
	}
	markers = append(markers, fillerbakeoff.TranscriptSegment{StartMS: 2_000, EndMS: 3_000, Text: "Buy now"})
	if temporalStructureWindowTranscriptIsWordless(markers) {
		t.Fatal("lexical transcript was treated as wordless evidence")
	}
}

type fakeTemporalStructureWindowMotionMeasurer struct {
	calls        int
	failAt       int
	framesByPath map[string]int64
}

func (m *fakeTemporalStructureWindowMotionMeasurer) Identity() TemporalTruthToolIdentity {
	return TemporalTruthToolIdentity{Path: "/fixture/ffmpeg", Version: "fixture-v1", BinarySHA256: strings.Repeat("a", 64)}
}

func (m *fakeTemporalStructureWindowMotionMeasurer) Measure(_ context.Context, path string) (TemporalStructureWindowMotionSample, error) {
	m.calls++
	if m.failAt > 0 && m.calls == m.failAt {
		return TemporalStructureWindowMotionSample{}, errors.New("fixture motion failure")
	}
	if filepath.Ext(path) != ".mp4" {
		return TemporalStructureWindowMotionSample{}, errors.New("fixture expected prepared mp4")
	}
	frames := m.framesByPath[path]
	if frames <= 0 {
		return TemporalStructureWindowMotionSample{}, errors.New("fixture lacks window duration")
	}
	mean := int64(m.calls * 1_000)
	return TemporalStructureWindowMotionSample{
		Frames: frames, SumMicroluma: frames * mean, P95Microluma: mean * 2, MaximumMicroluma: mean * 3,
	}, nil
}

func temporalStructureWindowSuiteFixture(t *testing.T, output string) (TemporalStructureWindowSuiteConfig, *fakeTemporalStructureWindowMotionMeasurer) {
	t.Helper()
	fixture := newTemporalStructureHoldoutFixtureWithEvidence(t, func(manifest *TemporalTruthEvidenceManifest, mapping *TemporalTruthEvidencePrivateMap) {
		mappingByAlias := make(map[string]*TemporalTruthEvidencePrivateEntry, len(mapping.Entries))
		for index := range mapping.Entries {
			mappingByAlias[mapping.Entries[index].Alias] = &mapping.Entries[index]
		}
		for index := range manifest.Cases {
			item := &manifest.Cases[index]
			item.TranscriptSegments = []fillerbakeoff.TranscriptSegment{{StartMS: 0, EndMS: 1_000, Text: "[Music]"}}
			mappingByAlias[item.Alias].TranscriptSHA256 = hashBytes([]byte("wordless-transcript-" + item.Alias))
		}
	})
	holdout := filepath.Join(t.TempDir(), "holdout")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(holdout)); err != nil {
		t.Fatal(err)
	}
	planRoot := filepath.Join(t.TempDir(), "plan")
	seed := "window-suite-seed"
	if _, err := BuildTemporalStructureWindowCorpusPlan(TemporalStructureWindowCorpusConfig{
		HoldoutAuthoringPath: filepath.Join(holdout, "authoring.json"), HoldoutReceiptPath: filepath.Join(holdout, "receipt.json"),
		Seed: seed, PlannedAt: fixture.plannedAt.Add(time.Hour), OutputDir: planRoot,
	}); err != nil {
		t.Fatal(err)
	}
	corpusRoot := filepath.Join(t.TempDir(), "corpus")
	corpusConfig, _ := temporalStructureWindowMediaFixtureFromExisting(t, TemporalStructureWindowMediaConfig{
		PlanPath: filepath.Join(planRoot, "plan.json"), HoldoutAuthoringPath: filepath.Join(holdout, "authoring.json"),
		HoldoutReceiptPath: filepath.Join(holdout, "receipt.json"), SourceRoot: fixture.root, Seed: seed,
		RenderedAt: fixture.plannedAt.Add(2 * time.Hour), OutputDir: corpusRoot,
	}, corpusRoot)
	if _, err := BuildTemporalStructureWindowCorpusMedia(t.Context(), corpusConfig); err != nil {
		t.Fatal(err)
	}
	windowRoot := filepath.Join(t.TempDir(), "windows")
	windowConfig, _ := temporalStructureWindowSetFixture(corpusConfig, windowRoot)
	if _, err := BuildTemporalStructureWindowSet(t.Context(), windowConfig); err != nil {
		t.Fatal(err)
	}
	config := TemporalStructureWindowSuiteConfig{
		WindowSetManifestPath:  filepath.Join(windowRoot, "public", "manifest.json"),
		WindowSetAuthorityPath: filepath.Join(windowRoot, "private", "authority.json"),
		CorpusManifestPath:     filepath.Join(corpusRoot, "public", "manifest.json"),
		CorpusAuthorityPath:    filepath.Join(corpusRoot, "private", "authority.json"),
		HoldoutAuthoringPath:   filepath.Join(holdout, "authoring.json"), HoldoutReceiptPath: filepath.Join(holdout, "receipt.json"),
		EvidenceManifestPath: fixture.manifest, EvidencePrivateMapPath: fixture.privateMap,
		MeasuredAt: fixture.plannedAt.Add(4 * time.Hour), OutputDir: output,
	}
	return temporalStructureWindowSuiteFixtureFromExisting(t, config, output)
}

func temporalStructureWindowSuiteFixtureFromExisting(t *testing.T, config TemporalStructureWindowSuiteConfig, output string) (TemporalStructureWindowSuiteConfig, *fakeTemporalStructureWindowMotionMeasurer) {
	t.Helper()
	manifest := readStrictTestJSON[TemporalStructureWindowSetManifest](t, config.WindowSetManifestPath)
	motion := &fakeTemporalStructureWindowMotionMeasurer{framesByPath: make(map[string]int64)}
	root := filepath.Dir(config.WindowSetManifestPath)
	for _, item := range manifest.Cases {
		for ordinal, window := range item.Windows {
			motion.framesByPath[filepath.Join(root, filepath.FromSlash(window.Path))] = item.MediaSet.Windows[ordinal].Media.DurationMS*30/1_000 - 1
		}
	}
	config.OutputDir, config.Motion = output, motion
	return config, motion
}
