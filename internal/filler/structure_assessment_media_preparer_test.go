package filler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/mediatools"
)

func TestStructureAssessmentMediaPreparerRendersOnceAndReusesVerifiedDerivative(t *testing.T) {
	root, input := structureAssessmentPreparerSource(t)
	tool := structureAssessmentToolFixture()
	runs := 0
	var arguments []string
	preparer := structureAssessmentPreparerFixture(root, tool, func(_ context.Context, _ string, got []string) error {
		runs++
		arguments = append([]string(nil), got...)
		return os.WriteFile(got[len(got)-1], []byte(strings.Repeat("normalized-media", 128)), 0o600)
	})

	first, err := preparer.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if runs != 1 || first.Source != input.Source || first.FullPath == input.FullPath ||
		first.Assessment.ProfileSHA256 != fillerstructuremedia.CanonicalProfile().SHA256 ||
		!isContentHash(first.Assessment.LineageSHA256) {
		t.Fatalf("first preparation = %+v, runs=%d", first, runs)
	}
	if _, err := os.Stat(first.FullPath); err != nil {
		t.Fatalf("published media: %v", err)
	}
	inputArgument := argumentAfter(arguments, "-i")
	if inputArgument == "" || inputArgument == input.FullPath || arguments[len(arguments)-1] == first.FullPath {
		t.Fatalf("render did not use an owned snapshot and staging output: %v", arguments)
	}
	wantArguments := fillerstructuremedia.PartArguments(inputArgument, 0, input.Source.DurationMs, arguments[len(arguments)-1])
	if !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("render arguments drifted\n got: %v\nwant: %v", arguments, wantArguments)
	}

	second, err := preparer.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if runs != 1 || !reflect.DeepEqual(first, second) {
		t.Fatalf("verified reuse = %+v, first=%+v, runs=%d", second, first, runs)
	}
	lineage, err := loadStructureAssessmentLineage(
		structureAssessmentLineagePath(root, first.Assessment.LineageSHA256),
		first.Assessment.LineageSHA256,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lineage.Source != structureAssessmentSourceIdentity(input.Source) || lineage.Tool != tool ||
		lineage.Media.SHA256 != first.Assessment.SHA256 || lineage.Media.Bytes != first.Assessment.Bytes {
		t.Fatalf("lineage = %+v", lineage)
	}
}

func TestStructureAssessmentMediaPreparerFailsClosedBeforeRenderOnSourceDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, StructureAssessmentSource)
	}{
		{name: "bytes", mutate: func(t *testing.T, input StructureAssessmentSource) {
			t.Helper()
			if err := os.WriteFile(input.FullPath, []byte("changed source"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink", mutate: func(t *testing.T, input StructureAssessmentSource) {
			t.Helper()
			target := filepath.Join(filepath.Dir(input.FullPath), "target.mp4")
			if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(input.FullPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, input.FullPath); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "path", mutate: func(_ *testing.T, input StructureAssessmentSource) { input.FullPath += ".other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, input := structureAssessmentPreparerSource(t)
			if test.name == "path" {
				input.FullPath += ".other"
			} else {
				test.mutate(t, input)
			}
			runs := 0
			preparer := structureAssessmentPreparerFixture(root, structureAssessmentToolFixture(), func(context.Context, string, []string) error {
				runs++
				return nil
			})
			if _, err := preparer.Prepare(context.Background(), input); err == nil || runs != 0 {
				t.Fatalf("error=%v runs=%d", err, runs)
			}
		})
	}
}

func TestStructureAssessmentMediaPreparerRejectsInvalidOutputProfile(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Probed)
	}{
		{name: "wrong dimensions", mutate: func(probe *Probed) { probe.Width-- }},
		{name: "wrong cadence", mutate: func(probe *Probed) { probe.Cadence = "30000/1001" }},
		{name: "wrong aspect", mutate: func(probe *Probed) { probe.SampleAspect = "4:3" }},
		{name: "missing audio", mutate: func(probe *Probed) { probe.Silent = true }},
		{name: "missing video", mutate: func(probe *Probed) { probe.NoVideo = true }},
		{name: "duration drift", mutate: func(probe *Probed) { probe.DurationMs += 1_001 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, input := structureAssessmentPreparerSource(t)
			preparer := structureAssessmentPreparerFixture(root, structureAssessmentToolFixture(), func(_ context.Context, _ string, arguments []string) error {
				return os.WriteFile(arguments[len(arguments)-1], []byte("normalized"), 0o600)
			})
			baseProbe := preparer.probe
			preparer.probe = func(ctx context.Context, path string) (Probed, error) {
				probe, err := baseProbe(ctx, path)
				if strings.Contains(filepath.Base(path), "structure-media-") {
					test.mutate(&probe)
				}
				return probe, err
			}
			if _, err := preparer.Prepare(context.Background(), input); err == nil {
				t.Fatal("invalid output profile was accepted")
			}
		})
	}
}

func TestStructureAssessmentMediaPreparerRejectsOversizedOutput(t *testing.T) {
	root, input := structureAssessmentPreparerSource(t)
	preparer := structureAssessmentPreparerFixture(root, structureAssessmentToolFixture(), func(_ context.Context, _ string, arguments []string) error {
		file, err := os.Create(arguments[len(arguments)-1])
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		return file.Truncate(fillerstructuremedia.MaximumVideoBytes + 1)
	})
	if _, err := preparer.Prepare(context.Background(), input); err == nil {
		t.Fatal("oversized assessment media was accepted")
	}
}

func TestStructureAssessmentMediaPreparerRejectsTamperedReuseAuthority(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(*testing.T, string, StructureAssessmentMedia)
	}{
		{name: "lineage", tamper: func(t *testing.T, root string, media StructureAssessmentMedia) {
			t.Helper()
			path := structureAssessmentLineagePath(root, media.Assessment.LineageSHA256)
			if err := os.WriteFile(path, []byte(`{"tampered":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "media", tamper: func(t *testing.T, _ string, media StructureAssessmentMedia) {
			t.Helper()
			if err := os.WriteFile(media.FullPath, []byte("tampered"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, input := structureAssessmentPreparerSource(t)
			runs := 0
			preparer := structureAssessmentPreparerFixture(root, structureAssessmentToolFixture(), func(_ context.Context, _ string, arguments []string) error {
				runs++
				return os.WriteFile(arguments[len(arguments)-1], []byte("normalized"), 0o600)
			})
			media, err := preparer.Prepare(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			test.tamper(t, root, media)
			if _, err := preparer.Prepare(context.Background(), input); err == nil || runs != 1 {
				t.Fatalf("tampered reuse error=%v runs=%d", err, runs)
			}
		})
	}
}

func TestFFmpegStructureAssessmentMediaPreparerEndToEnd(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	root := t.TempDir()
	path := filepath.Join(root, "compilation.mp4")
	command := exec.Command(ffmpeg,
		"-nostdin", "-hide_banner", "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x480:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000",
		"-t", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", path,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("local ffmpeg cannot build the fixture: %v: %s", err, output)
	}
	digest, size, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	clipHash, err := ClipID(path)
	if err != nil {
		t.Fatal(err)
	}
	input := StructureAssessmentSource{
		Source: SplitSourceAsset{
			Role: SplitSourceLegacyPlayback, SHA256: digest, Bytes: size, ClipHash: clipHash,
			Path: "compilation.mp4", DurationMs: 1_000,
		},
		FullPath: path,
	}
	preparer, err := NewFFmpegStructureAssessmentMediaPreparer(root, root, ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	first, err := preparer.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := preparer.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("real ffmpeg reuse drifted: first=%+v second=%+v", first, second)
	}
}

func TestStructureAssessmentMediaPreparerSeparatesSourceAndPrivateMediaRoots(t *testing.T) {
	sourceRoot, input := structureAssessmentPreparerSource(t)
	mediaRoot := t.TempDir()
	preparer, err := NewFFmpegStructureAssessmentMediaPreparer(sourceRoot, mediaRoot, "/fixture/ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	preparer.probe = func(_ context.Context, path string) (Probed, error) {
		if strings.Contains(filepath.Base(path), "structure-source-") {
			return Probed{DurationMs: 10_000, Width: 640, Height: 480, Cadence: "24/1", SampleAspect: "1:1"}, nil
		}
		return Probed{DurationMs: 10_000, Width: 960, Height: 720, Cadence: "30/1", SampleAspect: "1:1"}, nil
	}
	preparer.identify = func(context.Context, string) (mediatools.MediaToolIdentity, error) {
		return structureAssessmentToolFixture(), nil
	}
	preparer.run = func(_ context.Context, _ string, arguments []string) error {
		return os.WriteFile(arguments[len(arguments)-1], []byte(strings.Repeat("normalized-media", 128)), 0o600)
	}
	preparer.decode = func(context.Context, string, string) error { return nil }
	preparer.snapshot = snapshotOwnedFile

	prepared, err := preparer.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !pathContains(mediaRoot, prepared.FullPath) || pathContains(sourceRoot, prepared.FullPath) {
		t.Fatalf("assessment path %q is not isolated under private media root %q", prepared.FullPath, mediaRoot)
	}
	if prepared.Source != input.Source {
		t.Fatalf("prepared source = %+v, want %+v", prepared.Source, input.Source)
	}
}

func structureAssessmentPreparerSource(t *testing.T) (string, StructureAssessmentSource) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "incoming", "compilation.mp4")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("source-media", 128)), 0o600); err != nil {
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
	return root, StructureAssessmentSource{
		Source: SplitSourceAsset{
			Role: SplitSourceLegacyPlayback, SHA256: digest, Bytes: size, ClipHash: clipHash,
			Path: "incoming/compilation.mp4", DurationMs: 10_000,
		},
		FullPath: path,
	}
}

func structureAssessmentPreparerFixture(root string, tool mediatools.MediaToolIdentity, run func(context.Context, string, []string) error) *FFmpegStructureAssessmentMediaPreparer {
	return &FFmpegStructureAssessmentMediaPreparer{
		sourceRoot: root, mediaRoot: root, ffmpegPath: "/fixture/ffmpeg",
		probe: func(_ context.Context, path string) (Probed, error) {
			if strings.Contains(filepath.Base(path), "structure-source-") {
				return Probed{DurationMs: 10_000, Width: 640, Height: 480, Cadence: "24/1", SampleAspect: "1:1"}, nil
			}
			return Probed{DurationMs: 10_000, Width: 960, Height: 720, Cadence: "30/1", SampleAspect: "1:1"}, nil
		},
		identify: func(context.Context, string) (mediatools.MediaToolIdentity, error) { return tool, nil },
		run:      run, decode: func(context.Context, string, string) error { return nil },
		snapshot: snapshotOwnedFile,
	}
}

func structureAssessmentToolFixture() mediatools.MediaToolIdentity {
	return mediatools.MediaToolIdentity{Name: "ffmpeg", Version: "ffmpeg fixture", ExecutableSHA256: strings.Repeat("a", 64)}
}

func argumentAfter(arguments []string, name string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == name {
			return arguments[index+1]
		}
	}
	return ""
}
