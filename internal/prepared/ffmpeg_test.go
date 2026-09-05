package prepared

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func baselineRendition() RenditionContract {
	return RenditionContract{
		VideoCodec: "h264", VideoProfile: "high", VideoLevel: "4.1", PixelFormat: "yuv420p", HDR: "sdr",
		AudioCodec: "aac", AudioLayout: "stereo", Width: 1920, Height: 1080, FrameRate: 25,
		VideoBitrateKbps: 5000, AudioBitrateKbps: 160, SegmentDurationMS: 2000, PackagingVersion: 1,
	}
}

func TestFFmpegPackageArgsPinTheReusableRendition(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	args, err := ffmpegPackageArgs(workspace, LocalInput("/media/movie.mkv"), 2, baselineRendition())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-map 0:v:0", "-map 0:a:2", "-c:v libx264", "-profile:v high", "-level:v 4.1",
		"-pix_fmt yuv420p", "-r 25", "-b:v 5000k", "-c:a aac", "-b:a 160k", "-ac 2",
		"-force_key_frames expr:gte(t,n_forced*2.000)", "-hls_time 2.000",
		"-hls_playlist_type vod", "-hls_segment_type fmp4", "-hls_fmp4_init_filename init.mp4",
		filepath.Join(workspace, "segment-%06d.m4s"), filepath.Join(workspace, MediaManifestName),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q:\n%s", want, joined)
		}
	}
}

func TestFFmpegPackageArgsEncodeHEVCForCompatibleRendition(t *testing.T) {
	t.Parallel()
	r := baselineRendition()
	r.VideoCodec = "hevc"
	r.VideoProfile = "main10"
	r.PixelFormat = "yuv420p10le"
	args, err := ffmpegPackageArgs(t.TempDir(), LocalInput("/media/movie.mkv"), 0, r)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v libx265") || !strings.Contains(joined, "-tag:v hvc1") ||
		!strings.Contains(joined, "-x265-params keyint=50:min-keyint=50:scenecut=0") || strings.Contains(joined, "-sc_threshold") {
		t.Fatalf("HEVC args do not emit hvc1-compatible libx265: %s", joined)
	}
}

func TestFFmpegPackagerUsesInjectedHardwareVideoArgs(t *testing.T) {
	t.Parallel()
	called := false
	videoArgs := func(r RenditionContract) (VideoPlan, error) {
		called = true
		if r != baselineRendition() {
			t.Fatalf("video args received %+v, want the rendition unchanged", r)
		}
		return VideoPlan{
			InputArgs:  []string{"-hwaccel", "cuda"},
			OutputArgs: []string{"-vf", "format=yuv420p", "-c:v", "h264_nvenc", "-preset", "p7"},
		}, nil
	}
	args, err := ffmpegPackageArgsWith(
		t.TempDir(), LocalInput("/media/movie.mkv"), 0, baselineRendition(), videoArgs,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("injected video argument policy was not called")
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v h264_nvenc -preset p7") || strings.Contains(joined, "-c:v libx264") {
		t.Fatalf("injected hardware encoder not used: %s", joined)
	}
	if !strings.Contains(joined, "-hwaccel cuda -probesize 256k -analyzeduration 500000 -i /media/movie.mkv") {
		t.Fatalf("hardware input args are not before -i: %s", joined)
	}
}

func TestFFmpegPackageArgsRejectUnidentifiedOutputProperties(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*RenditionContract){
		"bitrate":           func(r *RenditionContract) { r.VideoBitrateKbps = 0 },
		"framerate":         func(r *RenditionContract) { r.FrameRate = 0 },
		"video codec":       func(r *RenditionContract) { r.VideoCodec = "av1" },
		"audio codec":       func(r *RenditionContract) { r.AudioCodec = "eac3" },
		"packaging version": func(r *RenditionContract) { r.PackagingVersion = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			r := baselineRendition()
			mutate(&r)
			if _, err := ffmpegPackageArgs(t.TempDir(), LocalInput("/media/movie.mkv"), 0, r); err == nil {
				t.Fatal("ffmpegPackageArgs accepted an unsupported or incomplete rendition")
			}
		})
	}
}

func TestFFmpegPackageArgsBoundAndReconnectOnlyHTTPInputs(t *testing.T) {
	t.Parallel()
	remote, err := ffmpegPackageArgs(t.TempDir(), HTTPInput("http://media/original"), 0, baselineRendition())
	if err != nil {
		t.Fatal(err)
	}
	remoteArgs := strings.Join(remote, " ")
	for _, want := range []string{
		"-reconnect 1", "-reconnect_on_network_error 1", "-reconnect_streamed 1",
		"-multiple_requests 1", "-probesize 256k", "-analyzeduration 500000",
	} {
		if !strings.Contains(remoteArgs, want) {
			t.Errorf("HTTP packaging args missing %q: %s", want, remoteArgs)
		}
	}
	local, err := ffmpegPackageArgs(t.TempDir(), LocalInput("/media/original.mkv"), 0, baselineRendition())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(local, " "), "-reconnect") {
		t.Fatal("local prepared input received HTTP-only reconnect options")
	}
}

func TestPreparedDiagnosticArgsNeverContainTheAuthenticatedInput(t *testing.T) {
	t.Parallel()
	input := "http://media/Videos/1/stream?api_key=secret"
	args, err := ffmpegPackageArgs(t.TempDir(), HTTPInput(input), 0, baselineRendition())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(diagnosticArgs(args, input), " ")
	if strings.Contains(joined, input) || strings.Contains(joined, "secret") || !strings.Contains(joined, "[input]") {
		t.Fatalf("diagnostic args exposed transient input: %s", joined)
	}
}

func TestCollectPackagedOutputDeclaresOnlyACompleteHLSPublication(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	for _, name := range []string{MediaManifestName, "init.mp4", "segment-000001.m4s", "segment-000000.m4s"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "ffmpeg.log"), []byte("ignore"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := collectPackagedOutput(workspace)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"init.mp4", MediaManifestName, "segment-000000.m4s", "segment-000001.m4s"}
	if !slices.Equal(out.Files, want) {
		t.Fatalf("files = %#v, want %#v", out.Files, want)
	}

	if err := os.Remove(filepath.Join(workspace, "init.mp4")); err != nil {
		t.Fatal(err)
	}
	if _, err := collectPackagedOutput(workspace); err == nil {
		t.Fatal("collectPackagedOutput accepted fMP4 without its init segment")
	}
}
