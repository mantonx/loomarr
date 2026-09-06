package fillerreview

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func temporalStructureTestProfile() TemporalTruthVideoProfile {
	return TemporalTruthVideoProfile{VideoCodec: "h264", PixelFormat: "yuv420p", FrameRate: "30/1", AudioCodec: "aac", SampleRate: 48_000, Channels: 2, AudioStreams: 1}
}

func TestTemporalStructureRejectsNonconformingRenderProfileBeforePublication(t *testing.T) {
	mutations := map[string]func(*TemporalTruthVideoInfo){
		"width":           func(v *TemporalTruthVideoInfo) { v.Width = 640 },
		"height":          func(v *TemporalTruthVideoInfo) { v.Height = 480 },
		"missing-audio":   func(v *TemporalTruthVideoInfo) { v.HasAudio = false },
		"video-codec":     func(v *TemporalTruthVideoInfo) { v.Profile.VideoCodec = "hevc" },
		"pixel-format":    func(v *TemporalTruthVideoInfo) { v.Profile.PixelFormat = "yuv444p" },
		"frame-rate":      func(v *TemporalTruthVideoInfo) { v.Profile.FrameRate = "30000/1001" },
		"missing-rate":    func(v *TemporalTruthVideoInfo) { v.Profile.FrameRate = "" },
		"invalid-rate":    func(v *TemporalTruthVideoInfo) { v.Profile.FrameRate = "30/0" },
		"audio-codec":     func(v *TemporalTruthVideoInfo) { v.Profile.AudioCodec = "ac3" },
		"sample-rate":     func(v *TemporalTruthVideoInfo) { v.Profile.SampleRate = 44_100 },
		"channels":        func(v *TemporalTruthVideoInfo) { v.Profile.Channels = 1 },
		"multiple-audio":  func(v *TemporalTruthVideoInfo) { v.Profile.AudioStreams = 2 },
		"missing-profile": func(v *TemporalTruthVideoInfo) { v.Profile = TemporalTruthVideoProfile{} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			fixture := newTemporalStructureFixture(t)
			fixture.media.renderVideoMutator = mutate
			output := filepath.Join(t.TempDir(), "challenge")
			_, err := buildFixtureTemporalStructureChallenge(context.Background(), fixture.config(output, "profile-seed"))
			if err == nil || !strings.Contains(err.Error(), "measured profile") {
				t.Fatalf("profile mismatch error = %v", err)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("invalid profile published output: %v", err)
			}
		})
	}
}

func TestTemporalStructureManifestBindsMeasuredProfile(t *testing.T) {
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "profile-seed")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	for _, item := range manifest.Cases {
		if item.Profile != temporalStructureTestProfile() || item.Video.Width != 960 || item.Video.Height != 720 {
			t.Fatalf("manifest did not preserve measured profile: %+v", item)
		}
	}
	manifest.Cases[0].Profile.SampleRate = 44_100
	writeTemporalHumanJSON(t, filepath.Dir(manifestPath), "manifest.json", manifest)
	if _, _, err := LoadTemporalStructureChallengePublic(manifestPath, 3); err == nil || !strings.Contains(err.Error(), "measured profile") {
		t.Fatalf("loader accepted nonconforming profile: %v", err)
	}

	root, _ = fixture.build(t, "profile-missing-seed")
	manifestPath = filepath.Join(root, "public", "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	cases, ok := document["cases"].([]any)
	if !ok || len(cases) == 0 {
		t.Fatal("manifest has no cases")
	}
	firstCase, ok := cases[0].(map[string]any)
	if !ok {
		t.Fatal("manifest case is not an object")
	}
	delete(firstCase, "profile")
	writeTemporalHumanJSON(t, filepath.Dir(manifestPath), "manifest.json", document)
	if _, _, err := LoadTemporalStructureChallengePublic(manifestPath, 3); err == nil || !strings.Contains(err.Error(), "measured profile") {
		t.Fatalf("loader accepted missing profile: %v", err)
	}
}

func TestTemporalTruthProbeExtractsStreamProfile(t *testing.T) {
	raw := `{"streams":[{"codec_type":"video","codec_name":"h264","width":960,"height":720,"pix_fmt":"yuv420p","avg_frame_rate":"30/1"},{"codec_type":"audio","codec_name":"aac","sample_rate":"48000","channels":2}],"format":{"duration":"2.0"}}`
	info, err := decodeTemporalTruthVideoProbe([]byte(raw))
	if err != nil || info.DurationMS != 2000 || info.Profile != temporalStructureTestProfile() {
		t.Fatalf("probe = %+v, %v", info, err)
	}
	if err := validateTemporalStructureVideoProfile(info); err != nil {
		t.Fatal(err)
	}
	for _, rate := range []string{"0", "N/A", "44100"} {
		info, err := decodeTemporalTruthVideoProbe([]byte(strings.Replace(raw, "48000", rate, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if err := validateTemporalStructureVideoProfile(info); err == nil {
			t.Fatalf("accepted invalid measured sample rate %q", rate)
		}
	}
}
