package fillerreview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/mediatools"
)

func TestBuildTemporalTransitionAuthorityMeasuresEveryCaseAndReproducesBytes(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	build := func(output string) TemporalTransitionAuthorityResult {
		t.Helper()
		result, err := BuildTemporalTransitionAuthority(context.Background(), TemporalTransitionAuthorityConfig{
			EvidenceManifestPath: fixture.manifest, EvidencePrivateMapPath: fixture.privateMap,
			GeneratedAt: fixture.plannedAt.Add(-time.Minute), PerCaseTimeout: time.Second,
			OutputDir: output, Media: &fakeTemporalTransitionMedia{},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	firstRoot, secondRoot := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
	first, second := build(firstRoot), build(secondRoot)
	if first.Cases != 48 || first != second {
		t.Fatalf("transition results differ: first=%+v second=%+v", first, second)
	}
	firstRaw, err := os.ReadFile(filepath.Join(firstRoot, "authority.json"))
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, err := os.ReadFile(filepath.Join(secondRoot, "authority.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(firstRaw) != string(secondRaw) {
		t.Fatal("transition authority bytes differ")
	}
}

func TestBuildTemporalTransitionAuthorityFailsAtomicallyOnMeasurementError(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	output := filepath.Join(t.TempDir(), "authority")
	_, err := BuildTemporalTransitionAuthority(context.Background(), TemporalTransitionAuthorityConfig{
		EvidenceManifestPath: fixture.manifest, EvidencePrivateMapPath: fixture.privateMap,
		GeneratedAt: fixture.plannedAt.Add(-time.Minute), PerCaseTimeout: time.Second,
		OutputDir: output, Media: &fakeTemporalTransitionMedia{fail: true},
	})
	if err == nil || !strings.Contains(err.Error(), "measure temporal transition alias") {
		t.Fatalf("measurement error = %v", err)
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed authority was published: %v", statErr)
	}
}

func TestLoadTemporalTransitionAuthorityRejectsChangedSourceBinding(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	authority := readStrictTestJSON[TemporalTransitionAuthority](t, fixture.transition)
	authority.Cases[0].SourceSHA256 = strings.Repeat("f", 64)
	path := writeTemporalHumanJSON(t, t.TempDir(), "authority.json", authority)
	manifest, manifestSHA, err := LoadTemporalTruthEvidence(fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	privateMapRaw, err := os.ReadFile(fixture.privateMap)
	if err != nil {
		t.Fatal(err)
	}
	privateMap := readStrictTestJSON[TemporalTruthEvidencePrivateMap](t, fixture.privateMap)
	_, _, err = loadTemporalTransitionAuthority(path, manifest, manifestSHA, privateMap, hashBytes(privateMapRaw), fixture.plannedAt)
	if err == nil || !strings.Contains(err.Error(), "unbound case") {
		t.Fatalf("changed source binding error = %v", err)
	}
}

func TestParseTemporalTransitionDetectorsPreservesBoundaryIntervals(t *testing.T) {
	raw := "[blackdetect @ 0x1] black_start:0 black_end:0.080 black_duration:0.080\n" +
		"[silencedetect @ 0x2] silence_start: 0.950\n"
	black, silence, err := parseTemporalTransitionDetectors(raw, 5_000, 6_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(black) != 1 || black[0] != (mediatools.Interval{StartMs: 5_000, EndMs: 5_080}) || len(silence) != 1 || silence[0] != (mediatools.Interval{StartMs: 5_950, EndMs: 6_000}) {
		t.Fatalf("black=%+v silence=%+v", black, silence)
	}
}

func TestParseTemporalTransitionDetectorsClampsToleratedBoundaryDrift(t *testing.T) {
	raw := "[blackdetect @ 0x1] black_start:-0.021 black_end:1.019 black_duration:1.040\n" +
		"[silencedetect @ 0x2] silence_start: -0.010\n" +
		"[silencedetect @ 0x2] silence_end: 1.012 | silence_duration: 1.022\n"
	black, silence, err := parseTemporalTransitionDetectors(raw, 5_000, 6_000)
	if err != nil {
		t.Fatal(err)
	}
	want := []mediatools.Interval{{StartMs: 5_000, EndMs: 6_000}}
	if len(black) != 1 || black[0] != want[0] || len(silence) != 1 || silence[0] != want[0] {
		t.Fatalf("black=%+v silence=%+v", black, silence)
	}
}

func TestParseTemporalTransitionDetectorsMergesOverlappingIntervals(t *testing.T) {
	raw := "[blackdetect @ 0x1] black_start:0.100 black_end:0.400 black_duration:0.300\n" +
		"[blackdetect @ 0x1] black_start:0.350 black_end:0.600 black_duration:0.250\n" +
		"[silencedetect @ 0x2] silence_start: 0.200\n" +
		"[silencedetect @ 0x2] silence_end: 0.500 | silence_duration: 0.300\n" +
		"[silencedetect @ 0x2] silence_start: 0.450\n" +
		"[silencedetect @ 0x2] silence_end: 0.700 | silence_duration: 0.250\n"
	black, silence, err := parseTemporalTransitionDetectors(raw, 5_000, 6_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(black) != 1 || black[0] != (mediatools.Interval{StartMs: 5_100, EndMs: 5_600}) {
		t.Fatalf("black=%+v", black)
	}
	if len(silence) != 1 || silence[0] != (mediatools.Interval{StartMs: 5_200, EndMs: 5_700}) {
		t.Fatalf("silence=%+v", silence)
	}
}

func TestParseTemporalTransitionDetectorsRejectsDriftBeyondTolerance(t *testing.T) {
	for _, raw := range []string{
		"[blackdetect @ 0x1] black_start:-0.035 black_end:0.100 black_duration:0.135\n",
		"[silencedetect @ 0x2] silence_start: 1.035\n",
	} {
		if _, _, err := parseTemporalTransitionDetectors(raw, 5_000, 6_000); err == nil {
			t.Fatalf("accepted out-of-window detector output %q", raw)
		}
	}
}

func TestParseTemporalTransitionDetectorsRejectsMalformedOrInconsistentOutput(t *testing.T) {
	for _, raw := range []string{
		"[blackdetect @ 0x1] black_start:nope black_end:0.100 black_duration:0.100\n",
		"[blackdetect @ 0x1] black_start:0.100 black_end:0.400 black_duration:0.100\n",
		"[silencedetect @ 0x2] silence_start: nope\n",
		"[silencedetect @ 0x2] silence_start: 0.100\n[silencedetect @ 0x2] silence_end: nope | silence_duration: 0.100\n",
		"[silencedetect @ 0x2] silence_start: 0.100\n[silencedetect @ 0x2] silence_end: 0.400 | silence_duration: 0.100\n",
	} {
		if _, _, err := parseTemporalTransitionDetectors(raw, 5_000, 6_000); err == nil {
			t.Fatalf("accepted malformed detector output %q", raw)
		}
	}
}

type fakeTemporalTransitionMedia struct {
	fail bool
}

func (*fakeTemporalTransitionMedia) Identity() TemporalTruthToolIdentity {
	return TemporalTruthToolIdentity{Path: "/fixture/ffmpeg", Version: "ffmpeg fixture", BinarySHA256: strings.Repeat("a", 64)}
}

func (media *fakeTemporalTransitionMedia) MeasureEdges(_ context.Context, _ string, durationMS int64) (TemporalTransitionEdges, error) {
	if media.fail {
		return TemporalTransitionEdges{}, errors.New("fixture failure")
	}
	return TemporalTransitionEdges{
		Head: TemporalTransitionEdge{StartMS: 0, EndMS: 1_000, RMSMilliDBFS: -10_000, PeakMilliDBFS: -3_000},
		Tail: TemporalTransitionEdge{StartMS: durationMS - 1_000, EndMS: durationMS, RMSMilliDBFS: -10_000, PeakMilliDBFS: -3_000},
	}, nil
}
