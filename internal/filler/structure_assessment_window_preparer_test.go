package filler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/mediatools"
)

func TestStructureAssessmentWindowPreparerRendersCompleteSetAndReusesVerifiedMedia(t *testing.T) {
	root, input, plan := structureAssessmentWindowSource(t)
	preparer, calls := structureAssessmentWindowPreparerFixture(t, root, plan)

	first, err := preparer.PrepareWindows(context.Background(), input, plan)
	if err != nil {
		t.Fatal(err)
	}
	if calls.renders != len(plan.Windows) || calls.decodes != len(plan.Windows) ||
		first.Source != input.Source || len(first.Windows) != len(plan.Windows) ||
		first.Authority.Plan.SHA256 != plan.SHA256 || first.Authority.SHA256 == "" {
		t.Fatalf("first preparation = %+v calls=%+v", first, calls)
	}
	if err := fillerstructurewindow.ValidateMediaSet(first.Authority); err != nil {
		t.Fatal(err)
	}
	for ordinal, prepared := range first.Windows {
		window := plan.Windows[ordinal]
		if prepared.Window != window || prepared.Media != first.Authority.Windows[ordinal] ||
			prepared.Media.Media.ProfileSHA256 != fillerstructuremedia.CanonicalProfile().SHA256 ||
			!isContentHash(prepared.Media.Media.LineageSHA256) || !filepath.IsAbs(prepared.FullPath) {
			t.Fatalf("prepared window %d = %+v", ordinal, prepared)
		}
		want := fillerstructuremedia.PartArguments(calls.snapshots[ordinal], window.MediaStartMS, window.MediaEndMS-window.MediaStartMS, calls.outputs[ordinal])
		if !reflect.DeepEqual(calls.arguments[ordinal], want) {
			t.Fatalf("window %d arguments\n got: %v\nwant: %v", ordinal, calls.arguments[ordinal], want)
		}
		lineage, err := loadStructureAssessmentWindowLineage(
			structureAssessmentWindowLineagePath(root, prepared.Media.Media.LineageSHA256),
			prepared.Media.Media.LineageSHA256, plan,
		)
		if err != nil {
			t.Fatal(err)
		}
		if lineage.Window != window || lineage.PlanSHA256 != plan.SHA256 ||
			lineage.Source != structureAssessmentSourceIdentity(input.Source) {
			t.Fatalf("window %d lineage = %+v", ordinal, lineage)
		}
	}
	if calls.snapshots[0] == input.FullPath {
		t.Fatal("window rendering used the mutable source path")
	}
	for _, path := range calls.snapshots[1:] {
		if path != calls.snapshots[0] {
			t.Fatalf("windows used different source snapshots: %v", calls.snapshots)
		}
	}

	second, err := preparer.PrepareWindows(context.Background(), input, plan)
	if err != nil {
		t.Fatal(err)
	}
	if calls.renders != len(plan.Windows) || calls.decodes != 2*len(plan.Windows) || !reflect.DeepEqual(first, second) {
		t.Fatalf("verified reuse = %+v first=%+v calls=%+v", second, first, calls)
	}
}

func TestStructureAssessmentWindowPreparerReturnsNoPartialSet(t *testing.T) {
	root, input, plan := structureAssessmentWindowSource(t)
	preparer, calls := structureAssessmentWindowPreparerFixture(t, root, plan)
	baseRun := preparer.run
	preparer.run = func(ctx context.Context, executable string, arguments []string) error {
		if argumentAfter(arguments, "-ss") == "105.000" {
			return errors.New("render failed")
		}
		return baseRun(ctx, executable, arguments)
	}
	result, err := preparer.PrepareWindows(context.Background(), input, plan)
	if err == nil || !reflect.DeepEqual(result, StructureAssessmentWindowMediaSet{}) || calls.renders != 1 {
		t.Fatalf("partial result=%+v error=%v calls=%+v", result, err, calls)
	}
}

func TestStructureAssessmentWindowPreparerRejectsPlanAndRetainedReuseDrift(t *testing.T) {
	t.Run("plan source", func(t *testing.T) {
		root, input, plan := structureAssessmentWindowSource(t)
		preparer, calls := structureAssessmentWindowPreparerFixture(t, root, plan)
		plan.Source.Bytes++
		plan.SHA256 = fillerstructurewindow.PlanSHA256(plan)
		if _, err := preparer.PrepareWindows(context.Background(), input, plan); err == nil || calls.renders != 0 {
			t.Fatalf("error=%v calls=%+v", err, calls)
		}
	})

	for _, target := range []string{"lineage", "media"} {
		t.Run(target, func(t *testing.T) {
			root, input, plan := structureAssessmentWindowSource(t)
			preparer, _ := structureAssessmentWindowPreparerFixture(t, root, plan)
			prepared, err := preparer.PrepareWindows(context.Background(), input, plan)
			if err != nil {
				t.Fatal(err)
			}
			first := prepared.Windows[0]
			path := first.FullPath
			if target == "lineage" {
				path = structureAssessmentWindowLineagePath(root, first.Media.Media.LineageSHA256)
			}
			if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := preparer.PrepareWindows(context.Background(), input, plan); err == nil {
				t.Fatal("tampered retained window was accepted")
			}
		})
	}
}

func TestStructureAssessmentMediaPreparersRejectIncompleteDecode(t *testing.T) {
	t.Run("complete video", func(t *testing.T) {
		root, input := structureAssessmentPreparerSource(t)
		preparer := structureAssessmentPreparerFixture(root, structureAssessmentToolFixture(), func(_ context.Context, _ string, arguments []string) error {
			return os.WriteFile(arguments[len(arguments)-1], []byte("normalized"), 0o600)
		})
		preparer.decode = func(context.Context, string, string) error { return errors.New("corrupt") }
		if _, err := preparer.Prepare(context.Background(), input); err == nil {
			t.Fatal("incompletely decoded full-video media was accepted")
		}
	})

	t.Run("window", func(t *testing.T) {
		root, input, plan := structureAssessmentWindowSource(t)
		preparer, _ := structureAssessmentWindowPreparerFixture(t, root, plan)
		preparer.decode = func(context.Context, string, string) error { return errors.New("corrupt") }
		if _, err := preparer.PrepareWindows(context.Background(), input, plan); err == nil {
			t.Fatal("incompletely decoded window media was accepted")
		}
	})
}

func TestStructureAssessmentWindowOutputRejectsPerWindowByteOverrun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.mp4")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(fillerstructurewindow.CanonicalProfile().MaximumWindowBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	profile := fillerstructurewindow.CanonicalProfile()
	window := fillerstructurewindow.Window{MediaStartMS: 0, MediaEndMS: 10_000}
	probe := Probed{DurationMs: 10_000, Width: 960, Height: 720, Cadence: "30/1", SampleAspect: "1:1"}
	if err := validateStructureAssessmentWindowOutput(path, probe, window, profile); err == nil {
		t.Fatal("oversized normalized window was accepted")
	}
}

type structureAssessmentWindowCalls struct {
	renders, decodes int
	arguments        [][]string
	snapshots        []string
	outputs          []string
}

func structureAssessmentWindowSource(t *testing.T) (string, StructureAssessmentSource, fillerstructurewindow.Plan) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "incoming", "compilation.mp4")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("long-source", 128)), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	clipHash, err := ClipID(path)
	if err != nil {
		t.Fatal(err)
	}
	input := StructureAssessmentSource{Source: SplitSourceAsset{
		Role: SplitSourceLegacyPlayback, SHA256: digest, Bytes: size, ClipHash: clipHash,
		Path: "incoming/compilation.mp4", DurationMs: 300_000,
	}, FullPath: path}
	plan, err := fillerstructurewindow.NewPlan(fillerstructure.Source{SHA256: digest, Bytes: size, DurationMS: 300_000})
	if err != nil {
		t.Fatal(err)
	}
	return root, input, plan
}

func structureAssessmentWindowPreparerFixture(t *testing.T, root string, plan fillerstructurewindow.Plan) (*FFmpegStructureAssessmentMediaPreparer, *structureAssessmentWindowCalls) {
	t.Helper()
	calls := &structureAssessmentWindowCalls{}
	preparer := &FFmpegStructureAssessmentMediaPreparer{
		sourceRoot: root, mediaRoot: root, ffmpegPath: "/fixture/ffmpeg", snapshot: snapshotOwnedFile,
		identify: func(context.Context, string) (mediatools.MediaToolIdentity, error) {
			return structureAssessmentToolFixture(), nil
		},
	}
	preparer.run = func(_ context.Context, _ string, arguments []string) error {
		start, err := strconv.ParseFloat(argumentAfter(arguments, "-ss"), 64)
		if err != nil {
			return err
		}
		startMS := int64(start * 1_000)
		ordinal := -1
		for candidate, window := range plan.Windows {
			if window.MediaStartMS == startMS {
				ordinal = candidate
				break
			}
		}
		if ordinal < 0 {
			return errors.New("unknown fixture window start")
		}
		output := arguments[len(arguments)-1]
		calls.renders++
		calls.arguments = append(calls.arguments, append([]string(nil), arguments...))
		calls.snapshots = append(calls.snapshots, argumentAfter(arguments, "-i"))
		calls.outputs = append(calls.outputs, output)
		return os.WriteFile(output, []byte(strings.Repeat("window-"+strconv.Itoa(ordinal), 128)), 0o600)
	}
	preparer.probe = func(_ context.Context, path string) (Probed, error) {
		if strings.Contains(filepath.Base(path), "structure-source-") {
			return Probed{DurationMs: plan.Source.DurationMS, Width: 640, Height: 480, Cadence: "24/1", SampleAspect: "1:1"}, nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return Probed{}, err
		}
		ordinal := -1
		for candidate := range plan.Windows {
			if strings.HasPrefix(string(raw), "window-"+strconv.Itoa(candidate)) {
				ordinal = candidate
				break
			}
		}
		if ordinal < 0 {
			return Probed{}, errors.New("unknown fixture media")
		}
		window := plan.Windows[ordinal]
		return Probed{DurationMs: window.MediaEndMS - window.MediaStartMS, Width: 960, Height: 720, Cadence: "30/1", SampleAspect: "1:1"}, nil
	}
	preparer.decode = func(_ context.Context, _ string, _ string) error {
		calls.decodes++
		return nil
	}
	return preparer, calls
}
