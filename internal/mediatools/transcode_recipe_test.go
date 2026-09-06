package mediatools

import (
	"slices"
	"strings"
	"testing"
)

func TestTranscodeArgumentsMakeTheDerivativeContractExplicit(t *testing.T) {
	recipe := EvidenceDerivativeRecipe()
	args := transcodeArguments(TranscodeRequest{
		In: "source.mkv", Out: "evidence.mp4", HadAudio: true, Profile: recipe.Profile(),
	}, "evidence.tmp.mp4")
	wants := [][]string{
		{"-map", "0:v:0"}, {"-map", "0:a:0?"}, {"-map_metadata", "-1"}, {"-map_chapters", "-1"},
		{"-fps_mode", "passthrough"}, {"-avoid_negative_ts", "make_zero"}, {"-movflags", "+faststart"},
	}
	for _, want := range wants {
		if !containsArgumentPair(args, want[0], want[1]) {
			t.Errorf("transcode args missing %v: %v", want, args)
		}
	}
	for _, forbidden := range []string{"scale", "crop", "yadif", "bwdif", "hqdn3d", "unsharp", "loudnorm"} {
		if slices.Contains(args, forbidden) {
			t.Errorf("evidence args contain undeclared transform %q: %v", forbidden, args)
		}
		for _, arg := range args {
			if len(arg) >= len(forbidden) && arg[:len(forbidden)] == forbidden {
				t.Errorf("evidence args contain undeclared transform %q: %v", forbidden, args)
			}
		}
	}
}

func TestVerifyPreservedMediaRejectsUndeclaredSignalChanges(t *testing.T) {
	base := Probed{
		DurationMs: 30_000, Width: 720, Height: 480, Cadence: "30000/1001",
		SampleAspect: "8:9", DisplayAspect: "4:3", FieldOrder: "progressive",
		VideoStartMs: 0, VideoDurationMs: 30_000, VideoTimingKnown: true,
		AudioStartMs: 20, AudioDurationMs: 29_980, AudioTimingKnown: true,
	}
	equivalent := base
	equivalent.Cadence = "60000/2002"
	if err := verifyPreservedMedia(base, equivalent); err != nil {
		t.Fatalf("equivalent rational observations were refused: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Probed)
		want   string
	}{
		{name: "geometry", mutate: func(p *Probed) { p.Width = 640 }, want: "geometry changed"},
		{name: "cadence", mutate: func(p *Probed) { p.Cadence = "25/1" }, want: "cadence changed"},
		{name: "aspect", mutate: func(p *Probed) { p.DisplayAspect = "16:9" }, want: "display aspect changed"},
		{name: "interlace", mutate: func(p *Probed) { p.FieldOrder = "tt" }, want: "field order changed"},
		{name: "start skew", mutate: func(p *Probed) { p.AudioStartMs += 100 }, want: "A/V start skew changed"},
		{name: "end skew", mutate: func(p *Probed) { p.AudioDurationMs += 700 }, want: "A/V end skew changed"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			output := base
			test.mutate(&output)
			if err := verifyPreservedMedia(base, output); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("verify error = %v, want %q", err, test.want)
			}
		})
	}
}

func containsArgumentPair(args []string, first, second string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == first && args[index+1] == second {
			return true
		}
	}
	return false
}
