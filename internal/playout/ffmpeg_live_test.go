//go:build ffmpeg

// Tests that EXECUTE ffmpeg. Behind a build tag because unit tests must not depend on an
// external binary being present — the same reasoning AGENTS.md applies to the network. Run
// with `make test-ffmpeg`.
//
// These exist because arg-shape tests assert my own stated invariants and cannot catch an
// invariant that is wrong. "Every rung has even dimensions" is checkable in Go; "every rung
// actually encodes" is only checkable by encoding.

package playout

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/schedule"
)

func ffmpegBin(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("FFMPEG_PATH"); p != "" {
		return p
	}
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on PATH")
	}
	return p
}

func ffprobeBin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe on PATH")
	}
	return p
}

// The test card must produce a VALID MPEG-TS with BOTH streams. The audio half is the point:
// a video-only MPEG-TS is a classic cause of a player refusing to play or showing no
// timeline, and `anullsrc` is what puts it there.
func TestLive_TestCardProducesValidMpegTsWithAudio(t *testing.T) {
	bin := ffmpegBin(t)
	probe := ffprobeBin(t)
	out := t.TempDir() + "/card.ts"

	p := DefaultProfile()
	p.Width, p.Height = 640, 360 // small: this is about validity, not throughput
	// CardFontFor, not FindFont: a font FILE is not enough, because drawtext is a compile-time
	// option. Homebrew's ffmpeg carries no libfreetype while macOS ships Arial, so FindFont
	// here produced `-vf drawtext=…` against a build that has no such filter — "Filter not
	// found", exit 8, and this test failing for the same reason a real channel would go dead.
	args := TestCardArgs(p, CardFontFor(bin)(), "Loomarr", "channel 1")
	// Bound it and write to a file rather than the pipe the real thing uses.
	args = replaceOutput(args, "-t", "2", out)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Through Start, NOT exec directly: TestCardArgs asks for `-progress pipe:3`, and fd 3
	// exists only because Start wires it via ExtraFiles. Running these args with plain exec
	// fails at startup with "Error parsing global options: Bad file descriptor" — which is
	// exactly how this bug was found, and why this test goes through the real supervisor.
	var samples int
	proc, err := Start(ctx, bin, args, nil, func(Progress) { samples++ })
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// Nothing consumes stdout here (output goes to the file), but the pipe still must be
	// drained or ffmpeg blocks writing to it.
	go func() { _, _ = io.Copy(io.Discard, proc.Stdout) }()
	if err := proc.Wait(); err != nil {
		t.Fatalf("test card failed to encode: %v\nlast stderr: %s", err, proc.LastError())
	}
	if samples == 0 {
		t.Error("no progress samples — `-progress pipe:3` is not being read")
	}

	got, err := exec.CommandContext(ctx, probe, "-v", "error",
		"-show_entries", "format=format_name", "-show_entries", "stream=codec_type,codec_name",
		"-of", "default=noprint_wrappers=1", out).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	s := string(got)
	for _, want := range []string{"format_name=mpegts", "codec_type=video", "codec_name=h264", "codec_type=audio"} {
		if !strings.Contains(s, want) {
			t.Errorf("probe missing %q — the card is not a playable MPEG-TS:\n%s", want, s)
		}
	}
}

// Every rung on every ladder must actually encode. A rung can satisfy "even dimensions,
// descending bitrate" and still be rejected by an encoder — only ffmpeg knows.
func TestLive_EveryLadderRungEncodes(t *testing.T) {
	bin := ffmpegBin(t)
	enc := DetectObserved(context.Background(), bin, DefaultProfile(), "", nil).Chosen
	t.Logf("verifying ladders against %s", enc)

	for _, tier := range []Tier{TierQuality, TierBalanced, TierEfficient} {
		for active := 0; active <= 8; active++ {
			p := Resolve(tier, enc, 8, active)
			args := []string{"-hide_banner", "-loglevel", "error"}
			args = append(args, deviceInitArgs(enc)...)
			args = append(args, "-f", "lavfi", "-i",
				"testsrc=duration=1:size="+itoa(p.Width)+"x"+itoa(p.Height)+":rate="+itoa(p.Framerate))
			if vf := hardwareUploadFilter(enc); vf != "" {
				args = append(args, "-vf", vf)
			}
			args = append(args, p.videoEncodeArgs()...)
			args = append(args, "-frames:v", "10", "-f", "null", "-")

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			b, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
			cancel()
			if err != nil {
				t.Errorf("%s active=%d (%dx%d @%dk) does not encode: %v\n%s",
					tier, active, p.Width, p.Height, p.VideoBitrate, err, b)
			}
		}
	}
}

// Detect must always return something usable, and must never claim an encoder works without
// having encoded with it.
func TestLive_DetectChoosesSomethingThatActuallyWorks(t *testing.T) {
	bin := ffmpegBin(t)
	c := DetectObserved(context.Background(), bin, DefaultProfile(), "", nil)

	if c.Chosen == "" {
		t.Fatal("Detect returned no encoder — software is always a valid answer")
	}
	if c.MaxChannels < 1 {
		t.Errorf("MaxChannels = %d, want at least 1", c.MaxChannels)
	}
	// ⚠ A WORKING ENCODER MUST HAVE A MEASURED SPEED. This is the assertion that catches a VACUOUS
	// probe, and it is here because this test was green for months while trialEncode encoded
	// nothing at all.
	//
	// The failure composed from three individually reasonable decisions: os.CreateTemp creates the
	// output file, ffmpeg without `-y` refuses to overwrite it and EXITS ZERO, and hasKeyframe is
	// deliberately best-effort about an unreadable file. Result: `Works: true` for every encoder
	// the build lists — including h264_amf on a machine with no AMD hardware — and Speed 0 for all
	// of them, so MaxChannels sat at capacityFloor (measured: 2 instead of 11 on an RTX 3080 Ti).
	//
	// "Works" alone cannot detect that, because a vacuous probe reports exactly what a real one
	// does. A measured throughput cannot be faked by an encode that never ran.
	for _, x := range c.All {
		if x.Works && x.Speed <= 0 {
			t.Errorf("%s reports Works with no measured speed — the trial exited cleanly without "+
				"encoding anything, so this probe proves nothing", x.Encoder)
		}
		if !x.Works && x.Err == "" {
			t.Errorf("%s failed with no reason recorded; the wizard's transcode check shows that "+
				"text to an operator", x.Encoder)
		}
	}

	// Whatever it chose must appear in All as Works.
	for _, x := range c.All {
		if x.Encoder == c.Chosen {
			if !x.Works {
				t.Errorf("Detect chose %s but its probe did not pass: %q", c.Chosen, x.Err)
			}
			return
		}
	}
	t.Errorf("Detect chose %s but it is not in the probe results", c.Chosen)
}

// A failed probe must carry ffmpeg's own message, not a category we invented — that text is
// what the wizard's transcode check shows an operator.
func TestLive_FailedProbesCarryFfmpegsOwnMessage(t *testing.T) {
	c := DetectObserved(context.Background(), ffmpegBin(t), DefaultProfile(), "", nil)
	for _, x := range c.All {
		if x.Works || x.Err == "" {
			continue
		}
		if x.Err == "failed" || x.Err == "error" {
			t.Errorf("%s: error is a useless category, want ffmpeg's text: %q", x.Encoder, x.Err)
		}
		t.Logf("%s: %s", x.Encoder, x.Err)
	}
}

// embyStreamURL returns a real library stream URL, or skips.
//
// Set PLAYOUT_TEST_STREAM_URL to an Emby/Jellyfin `/Videos/{id}/stream?static=true&api_key=…`
// URL. Deliberately an env var rather than reading Loomarr's settings: this test asserts
// ffmpeg's behaviour against real content, not the app's configuration plumbing.
func embyStreamURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("PLAYOUT_TEST_STREAM_URL")
	if u == "" {
		t.Skip("set PLAYOUT_TEST_STREAM_URL to a library stream URL to run this")
	}
	return u
}

// The child args must actually encode REAL library content — which is where the synthetic
// card stops being useful. A real remux brings HEVC, 10-bit, HDR, multiple audio tracks and
// subtitles, and every one of those is a way for the output to differ from the profile and
// silently break the parent's `-c copy`.
//
// So this asserts the OUTPUT PROPERTIES, not just that ffmpeg exited 0: exact resolution,
// exact framerate, h264 + aac, and exactly one of each. Those are the concat preconditions.
func TestLive_ProgramArgsNormalizeRealContent(t *testing.T) {
	bin := ffmpegBin(t)
	probe := ffprobeBin(t)
	url := embyStreamURL(t)
	out := t.TempDir() + "/program.ts"

	p := DefaultProfile()
	enc := DetectObserved(context.Background(), bin, p, "", nil).Chosen
	p.Encoder = enc
	t.Logf("normalizing real content with %s", enc)

	// Seek in, so this also exercises the mid-program tune-in path against HTTP.
	args := transcodeArgs(p, url, 60*time.Second, 3*time.Second)
	args = replaceOutput(args, out)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var samples int
	proc, err := Start(ctx, bin, args, nil, func(Progress) { samples++ })
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, proc.Stdout) }()
	if err := proc.Wait(); err != nil {
		t.Fatalf("encoding real content failed: %v\nlast stderr: %s", err, proc.LastError())
	}
	if samples == 0 {
		t.Error("no progress samples")
	}

	// Exact geometry + codecs. `-of csv` so each stream is one line and we can count them.
	got, err := exec.CommandContext(ctx, probe, "-v", "error",
		"-show_entries", "stream=codec_type,codec_name,width,height,r_frame_rate,channels",
		"-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	s := strings.TrimSpace(string(got))
	t.Logf("probe:\n%s", s)

	// Exactly two streams — one video, one audio. A 3-audio-track remux must be reduced to
	// one, because a varying track count breaks -c copy on the parent exactly like a varying
	// resolution does.
	//
	// `format=nb_streams` rather than counting `stream=` lines: an MPEG-TS carries its program
	// map periodically, so ffprobe legitimately reports the same streams more than once. An
	// earlier version of this test counted lines, saw 5, and failed against output that was
	// completely correct.
	nb, err := exec.CommandContext(ctx, probe, "-v", "error",
		"-show_entries", "format=nb_streams", "-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatalf("ffprobe nb_streams: %v", err)
	}
	if got := strings.TrimSpace(string(nb)); got != "2" {
		t.Errorf("nb_streams = %s, want 2 (one video, one audio) — a varying track count "+
			"breaks -c copy on the parent:\n%s", got, s)
	}
	// Resolution and framerate must match the profile EXACTLY, not approximately.
	if !strings.Contains(s, itoa(p.Width)+","+itoa(p.Height)) {
		t.Errorf("output is not %dx%d — the continuous mux would reject it mid-stream:\n%s",
			p.Width, p.Height, s)
	}
	if !strings.Contains(s, itoa(p.Framerate)+"/1") {
		t.Errorf("framerate is not %d/1:\n%s", p.Framerate, s)
	}
	for _, want := range []string{"h264", "aac"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s:\n%s", want, s)
		}
	}
	// Stereo: a 4.0 or 5.1 source must be downmixed, or the layout varies between programs.
	if !strings.Contains(s, ",2") {
		t.Errorf("audio is not 2-channel — a varying layout breaks -c copy:\n%s", s)
	}
}

// A channel is one decoder timeline, even when its source blocks are not. Two individually valid
// H.264/AAC files may still disagree on geometry, frame cadence, pixel format or audio layout; the
// old codec-only copy decision passed both through and made the shared MPEG-TS change shape at EOF.
// Media servers stalled while reinitialising there, and the live HLS remux inherited the same
// anonymous format change. This is the smallest real-media reproduction of that transition bug.
func TestLive_BaselineSessionKeepsOneFormatAcrossBlockBoundary(t *testing.T) {
	bin := ffmpegBin(t)
	probe := ffprobeBin(t)
	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	type source struct {
		path          string
		width, height int
		colour        string
	}
	sources := []source{
		{path: dir + "/episode.ts", width: 320, height: 180, colour: "red"},
		{path: dir + "/commercial.ts", width: 640, height: 360, colour: "blue"},
	}
	for _, src := range sources {
		args := []string{
			"-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", fmt.Sprintf("color=c=%s:s=%dx%d:r=25:d=2", src.colour, src.width, src.height),
			"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
			"-map", "0:v:0", "-map", "1:a:0", "-shortest", "-t", "2",
			"-c:v", "libx264", "-preset", "ultrafast", "-g", "25", "-sc_threshold", "0",
			"-pix_fmt", "yuv420p", "-c:a", "aac", "-ar", "48000", "-ac", "2",
			"-f", "mpegts", src.path,
		}
		if out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput(); err != nil {
			t.Fatalf("generate %dx%d source: %v\n%s", src.width, src.height, err, out)
		}
	}

	profile := DefaultProfile()
	profile.Width, profile.Height = 320, 180
	profile.Framerate = 25
	profile.Encoder = EncoderSoftware

	parts := make([]string, 0, len(sources))
	for i, src := range sources {
		part := fmt.Sprintf("%s/block-%d.ts", dir, i)
		format := MediaFormat{
			VideoCodec: "h264", Width: src.width, Height: src.height,
			FrameRate: 25, PixelFormat: "yuv420p",
			AudioCodec: "aac", AudioChannels: 2, AudioSampleRate: 48000,
		}
		plan := ConformCopyPlan(format, PlanCopy(format, PlanBaseline), profile, "h264")
		spec := ProgramSpec{
			Profile: profile, Input: src.path, Limit: 2 * time.Second,
			Plan: plan, Source: format,
		}
		proc, err := Start(ctx, bin, replaceOutput(ProgramArgs(spec), part), nil, nil)
		if err != nil {
			t.Fatalf("block %d start: %v", i, err)
		}
		go func() { _, _ = io.Copy(io.Discard, proc.Stdout) }()
		if err := proc.Wait(); err != nil {
			t.Fatalf("block %d: %v\n%s", i, err, proc.LastError())
		}
		parts = append(parts, part)
	}

	joined := dir + "/channel.ts"
	proc, err := StartPipedObserved(ctx, bin, BlockMuxArgs(), nil, nil, nil, diagnostics.ProcessSpec{})
	if err != nil {
		t.Fatalf("channel mux start: %v", err)
	}
	joinedFile, err := os.Create(joined)
	if err != nil {
		t.Fatal(err)
	}
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(joinedFile, proc.Stdout)
		copyDone <- copyErr
	}()
	for _, part := range parts {
		input, openErr := os.Open(part)
		if openErr != nil {
			t.Fatal(openErr)
		}
		_, copyErr := io.Copy(proc.Stdin, input)
		_ = input.Close()
		if copyErr != nil {
			t.Fatal(copyErr)
		}
	}
	_ = proc.Stdin.Close()
	if err := <-copyDone; err != nil {
		t.Fatal(err)
	}
	if err := proc.Wait(); err != nil {
		t.Fatalf("channel mux: %v\n%s", err, proc.LastError())
	}
	if err := joinedFile.Close(); err != nil {
		t.Fatal(err)
	}

	for _, codecType := range []string{"v", "a"} {
		first, err := exec.CommandContext(ctx, probe, "-v", "error", "-select_streams", codecType+":0",
			"-show_entries", "stream=index", "-of", "csv=p=0", joined).Output()
		if err != nil || strings.TrimSpace(string(first)) == "" {
			t.Fatalf("probe first %s stream: output %q, err %v", codecType, first, err)
		}
		extra, err := exec.CommandContext(ctx, probe, "-v", "error", "-select_streams", codecType+":1",
			"-show_entries", "stream=index", "-of", "csv=p=0", joined).Output()
		if err != nil {
			t.Fatalf("probe extra %s stream: %v", codecType, err)
		}
		if strings.TrimSpace(string(extra)) != "" {
			t.Fatalf("channel has an extra %s stream: %q", codecType, extra)
		}
	}

	frames, err := exec.CommandContext(ctx, probe, "-v", "error", "-select_streams", "v:0",
		"-show_frames", "-show_entries", "frame=width,height", "-of", "csv=p=0", joined).Output()
	if err != nil {
		t.Fatalf("probe channel frames: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(frames)), "\n") {
		// ffprobe appends a side-data description to frames carrying H.264 SEI, so width and
		// height are the stable leading columns rather than necessarily the entire CSV row.
		if !strings.HasPrefix(line, "320,180") {
			t.Fatalf("broadcast format changed at the block boundary: frame is %s, want every frame 320,180", line)
		}
	}
}

// A logical Airing change does not require a decoder reset once both blocks conform to the same
// live-session format. The HLS presentation must keep advancing, retain wall-clock mapping, and
// avoid an unnecessary EXT-X-DISCONTINUITY that would itself flush the browser decoder.
func TestLive_HLSKeepsAStableTimelineAcrossBlockBoundaries(t *testing.T) {
	bin := ffmpegBin(t)
	probe := ffprobeBin(t)
	dir := t.TempDir()

	blocks := []string{dir + "/episode.ts", dir + "/commercial.ts", dir + "/return.ts"}
	for i, colour := range []string{"red", "blue", "red"} {
		args := []string{
			"-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", fmt.Sprintf("color=c=%s:s=320x180:r=25:d=5", colour),
			"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
			"-map", "0:v:0", "-map", "1:a:0", "-shortest", "-t", "5",
			"-c:v", "libx264", "-preset", "ultrafast", "-g", "50", "-sc_threshold", "0",
			"-pix_fmt", "yuv420p", "-c:a", "aac", "-ar", "48000", "-ac", "2",
			"-f", "mpegts", "-mpegts_flags", "+initial_discontinuity", blocks[i],
		}
		if out, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
			t.Fatalf("generate block %d: %v\n%s", i, err, out)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var opened atomic.Int64
	source := BlockSource(func(ctx context.Context, _ string, _ EncodePlan) (Block, error) {
		i := int(opened.Add(1)) - 1
		if i >= len(blocks) {
			<-ctx.Done()
			return Block{}, ctx.Err()
		}
		content, err := os.Open(blocks[i])
		return Block{
			Content: content,
			Identity: AiringIdentity{
				StartedAt: time.Unix(int64(i), 0), Kind: schedule.SlotProgram,
				ContentID: fmt.Sprintf("block-%d", i),
			},
		}, err
	})
	channel, err := BlockSpawner(bin, source, nil)(ctx, "channel", PlanBaseline)
	if err != nil {
		t.Fatalf("channel start: %v", err)
	}
	hlsDir := dir + "/hls"
	if err := os.Mkdir(hlsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	hls, err := startHLSFFmpeg(ctx, bin, hlsDir, PlanBaseline, nil)
	if err != nil {
		t.Fatalf("HLS start: %v", err)
	}
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(hls.stdin, channel.Stdout)
		_ = hls.closeStdin()
		close(copyDone)
	}()

	manifestPath := hlsDir + "/" + hlsPlaylistName
	var manifest []byte
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		manifest, _ = os.ReadFile(manifestPath)
		if strings.Count(string(manifest), "#EXTINF:") >= 3 && opened.Load() >= 3 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if strings.Count(string(manifest), "#EXTINF:") < 3 {
		t.Fatalf("HLS did not advance across blocks:\n%s\nlast error: %s", manifest, hls.LastError())
	}
	if !strings.Contains(string(manifest), "#EXT-X-PROGRAM-DATE-TIME:") {
		t.Fatalf("HLS lost its wall-clock mapping:\n%s", manifest)
	}
	if strings.Contains(string(manifest), "#EXT-X-DISCONTINUITY") {
		t.Fatalf("stable blocks forced an unnecessary decoder reset:\n%s", manifest)
	}

	entries, err := os.ReadDir(hlsDir)
	if err != nil {
		t.Fatal(err)
	}
	segments := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".ts") {
			continue
		}
		segments++
		got, err := exec.CommandContext(ctx, probe, "-v", "error", "-select_streams", "v:0",
			"-show_entries", "stream=width,height", "-of", "csv=p=0", hlsDir+"/"+entry.Name()).Output()
		if err != nil {
			t.Fatalf("probe %s: %v", entry.Name(), err)
		}
		if !strings.HasPrefix(strings.TrimSpace(string(got)), "320,180") {
			t.Fatalf("%s changed decoder geometry: %s", entry.Name(), got)
		}
	}
	if segments < 2 {
		t.Fatalf("HLS wrote %d segments, want at least two across the block boundary", segments)
	}

	// Decode the first twelve seconds down to one RGB pixel at 5fps. Solid red → blue → red makes
	// the exact content transitions observable without screenshots: an inserted black frame, a
	// repeated outgoing run, or a missing second boundary changes this compact sequence. Container
	// and timestamp checks alone cannot catch that viewer-visible blip.
	// Snapshot the open-ended live manifest before decoding it. Feeding the live form to a finite
	// verifier makes ffmpeg correctly wait for future segments forever — a harness hang, not a
	// playback result.
	snapshotPath := hlsDir + "/snapshot.m3u8"
	if err := os.WriteFile(snapshotPath, []byte(string(manifest)+"#EXT-X-ENDLIST\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pixels, err := exec.CommandContext(ctx, bin,
		"-hide_banner", "-loglevel", "error", "-i", snapshotPath, "-t", "12",
		"-vf", "fps=5,scale=1:1:flags=neighbor,format=rgb24", "-f", "rawvideo", "pipe:1").Output()
	if err != nil {
		t.Fatalf("decode HLS transition frames: %v\n%s", err, pixels)
	}
	var runs []byte
	var frameColours []byte
	for i := 0; i+2 < len(pixels); i += 3 {
		r, g, b := pixels[i], pixels[i+1], pixels[i+2]
		var colour byte
		switch {
		case r > 160 && g < 80 && b < 80:
			colour = 'R'
		case b > 160 && r < 80 && g < 80:
			colour = 'B'
		default:
			t.Fatalf("unexpected transition frame rgb(%d,%d,%d); content visibly blipped", r, g, b)
		}
		if len(runs) == 0 || runs[len(runs)-1] != colour {
			runs = append(runs, colour)
		}
		frameColours = append(frameColours, colour)
	}
	if got := string(runs); got != "RBR" {
		t.Fatalf("decoded content runs = %q, want RBR; a block repeated or disappeared", got)
	}
	var transitions []int
	for i := 1; i < len(frameColours); i++ {
		if frameColours[i] != frameColours[i-1] {
			transitions = append(transitions, i)
		}
	}
	if len(transitions) != 2 || transitions[0] < 24 || transitions[0] > 26 ||
		transitions[1] < 49 || transitions[1] > 51 {
		t.Fatalf("colour transitions at frames %v (5fps), want about [25 50]; a block boundary moved", transitions)
	}

	cancel()
	<-copyDone
	_ = hls.wait()
	_ = channel.Wait()
}

// Confirmed compilation Segments are MP4 edit-list cuts: stream copy seeks to the preceding
// keyframe and the container hides that preroll. Internal playout then stream-copies the Segment
// into MPEG-TS. If that second remux drops the edit, the incoming commercial begins with up to one
// GOP of the material before its cut — observed by a viewer as an approximately two-second replay
// immediately before the break transition.
func TestLive_DirectCopyOfTrimmedMP4DoesNotExposeKeyframePreroll(t *testing.T) {
	bin := ffmpegBin(t)
	dir := t.TempDir()
	source := dir + "/compilation.mp4"
	cut := dir + "/commercial.mp4"
	transport := dir + "/commercial.ts"

	// Keyframes every two seconds. The cut starts at 5s, midway through the 4s→6s GOP; red is the
	// preceding content and blue is the requested commercial.
	if out, err := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=red:s=320x180:r=25:d=5",
		"-f", "lavfi", "-i", "color=c=blue:s=320x180:r=25:d=7",
		"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000:d=12",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]",
		"-map", "[v]", "-map", "2:a:0", "-t", "12", "-c:v", "libx264", "-preset", "ultrafast",
		"-g", "50", "-keyint_min", "50", "-sc_threshold", "0", "-pix_fmt", "yuv420p",
		"-c:a", "aac", source).CombinedOutput(); err != nil {
		t.Fatalf("generate compilation: %v\n%s", err, out)
	}
	// This is mediatools.Cut's production shape.
	if out, err := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-y",
		"-ss", "5", "-t", "5", "-i", source, "-c", "copy", cut).CombinedOutput(); err != nil {
		t.Fatalf("cut commercial: %v\n%s", err, out)
	}

	profile := DefaultProfile()
	profile.Width, profile.Height, profile.Framerate, profile.Encoder = 320, 180, 25, EncoderSoftware
	sourceFormat, err := FFprobeFormatNextTo(bin)(context.Background(), cut)
	if err != nil {
		t.Fatalf("probe cut commercial: %v", err)
	}
	plan := ConformCopyPlan(sourceFormat, PlanCopy(sourceFormat, PlanBaseline), profile, "h264")
	if plan.CopyVideo {
		t.Fatal("trimmed commercial with discard preroll was direct-copied; its preceding GOP would become visible")
	}
	spec := ProgramSpec{
		Profile: profile, Input: cut, Limit: 5 * time.Second,
		TargetLUFS: "-16", Plan: plan, Source: sourceFormat,
	}
	proc, err := Start(context.Background(), bin, replaceOutput(ProgramArgs(spec), transport), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, proc.Stdout) }()
	if err := proc.Wait(); err != nil {
		t.Fatalf("remux commercial: %v\n%s", err, proc.LastError())
	}

	pixels, err := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-i", transport,
		"-t", "0.5", "-vf", "fps=5,scale=1:1:flags=neighbor,format=rgb24",
		"-f", "rawvideo", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+2 < len(pixels); i += 3 {
		r, g, b := pixels[i], pixels[i+1], pixels[i+2]
		if b <= 160 || r >= 80 || g >= 80 {
			t.Fatalf("commercial began with preroll rgb(%d,%d,%d), want blue from its first frame", r, g, b)
		}
	}
}

// replaceOutput swaps the trailing "pipe:1" for a bounded file output.
// makeHDRSource synthesizes an HDR10 clip — PQ transfer, BT.2020 primaries, 10-bit — into the
// test's temp dir.
//
// SYNTHESIZED, NOT COMMITTED, and that is a deliberate departure from ErsatzTV, whose test suite
// carries ~3MB of fixture `.ts` files across a resolution × codec × bit-depth × HDR × anamorphic
// matrix. Every axis in that matrix is producible by the ffmpeg this build tag already requires,
// so committing the bytes buys nothing and costs a repo that grows with the matrix. Measured here:
// this clip is ~26KB and takes under half a second to make.
//
// It also fixes the gap that let the defect ship. Every other live test sources `testsrc` or a
// real Emby URL — 8-bit, SDR, progressive, square-pixel — so no gate had ever put non-SDR content
// through the filter chain. The encoder-family axis was well covered; the SOURCE axis was not.
func makeHDRSource(t *testing.T, bin string) string {
	t.Helper()
	out := t.TempDir() + "/hdr.ts"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=1280x720:rate=25:duration=2",
		"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
		"-c:v", "libx265", "-pix_fmt", "yuv420p10le",
		"-x265-params", "colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc",
		"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc",
		"-c:a", "aac", "-shortest", "-t", "2", "-f", "mpegts", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot synthesize an HDR source with this build (libx265?): %v\n%s", err, b)
	}
	return out
}

// probeColor returns pix_fmt, transfer, primaries, matrix and range for a file's video stream.
func probeColor(t *testing.T, probe, path string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	got, err := exec.CommandContext(ctx, probe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=pix_fmt,color_transfer,color_primaries,color_space,color_range",
		"-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	// An MPEG-TS repeats its program map, so ffprobe can print the same stream more than once.
	return strings.TrimSpace(strings.Split(strings.TrimSpace(string(got)), "\n")[0])
}

// AN HDR PROGRAM MUST COME OUT AS HONEST SDR — tone-mapped AND correctly labelled.
//
// This is the test that could not have been written as an arg-shape assertion, and the defect it
// guards was BOTH halves at once. Before this change the chain ended in a bare `format=yuv420p`
// with no colour tags, so a real HDR10 source produced:
//
//	yuv420p,bt2020nc,smpte2084,bt2020
//
// — 8-bit SDR-range pixels still announcing PQ/BT.2020. A player that believes the tags applies an
// HDR transfer to SDR data, which is worse than doing nothing, and no client-side handling can
// recover it because the information needed is gone.
//
// The two assertions are independent ON PURPOSE. Tags alone would pass if the filter silently did
// nothing; a changed picture alone would pass while still mislabelled. Both defects were live
// simultaneously, and either one checked without the other reads as success.
func TestLive_HDRSourceIsTonemappedAndLabelledSDR(t *testing.T) {
	bin := ffmpegBin(t)
	probe := ffprobeBin(t)

	if !TonemapperFor(bin)() {
		t.Skip("this ffmpeg build has no zscale/tonemap — the code correctly emits no chain")
	}
	src := makeHDRSource(t, bin)

	// Confirm the fixture really IS HDR. A source that quietly lost its tags would make every
	// assertion below pass for the wrong reason — the fixture-collapse trap.
	if got := probeColor(t, probe, src); !strings.Contains(got, "smpte2084") {
		t.Fatalf("synthesized source is not HDR (%s); the rest of this test would be vacuous", got)
	}

	p := DefaultProfile()
	p.Encoder = DetectObserved(context.Background(), bin, p, "", nil).Chosen

	run := func(name string, spec ProgramSpec) string {
		t.Helper()
		out := t.TempDir() + "/" + name + ".ts"
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		proc, err := Start(ctx, bin, replaceOutput(ProgramArgs(spec), out), nil, nil)
		if err != nil {
			t.Fatalf("%s: start: %v", name, err)
		}
		go func() { _, _ = io.Copy(io.Discard, proc.Stdout) }()
		if err := proc.Wait(); err != nil {
			t.Fatalf("%s: encode failed: %v\nlast stderr: %s", name, err, proc.LastError())
		}
		return out
	}

	base := ProgramSpec{Profile: p, Input: src, Limit: 1 * time.Second, Source: hdrSource()}

	tonemapped := base
	tonemapped.Tonemap = true
	withTM := run("tonemapped", tonemapped)

	untouched := base // Tonemap false — what a build without zscale produces
	withoutTM := run("untouched", untouched)

	// HALF ONE: the labels are honest.
	got := probeColor(t, probe, withTM)
	t.Logf("tone-mapped output: %s", got)
	for _, want := range []string{"bt709"} {
		if !strings.Contains(got, want) {
			t.Errorf("tone-mapped output is not labelled %s: %s", want, got)
		}
	}
	for _, unwanted := range []string{"smpte2084", "bt2020"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("output still carries the source's HDR tag %q — SDR pixels announcing PQ is "+
				"the half no client can recover from: %s", unwanted, got)
		}
	}

	// HALF TWO: the picture actually changed. Frame hashes, because "did the filter run" and
	// "are the tags right" are different questions and the tags can be right while the filter did
	// nothing at all.
	hashTM, hashPlain := frameHash(t, bin, withTM), frameHash(t, bin, withoutTM)
	if hashTM == hashPlain {
		t.Errorf("tone-mapped and untouched output are pixel-identical (%s) — the tags changed "+
			"but the filter did no work", hashTM)
	}
}

// frameHash is the hash of a file's first VIDEO frame.
//
// ⚠ `-map 0:v:0` is load-bearing. Without it framehash also emits the AUDIO frames, and since both
// files here carry identical silence, reading the wrong line reports two different pictures as
// identical. Cost me one wrong conclusion before the stream index was pinned.
func frameHash(t *testing.T, bin, path string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "-hide_banner", "-loglevel", "error",
		"-i", path, "-map", "0:v:0", "-frames:v", "1", "-f", "framehash", "-").Output()
	if err != nil {
		t.Fatalf("framehash: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, ",")
		return strings.TrimSpace(f[len(f)-1])
	}
	t.Fatalf("no frame hash in output: %s", out)
	return ""
}

// CAN THIS FFMPEG ADVANCE A CONCAT PLAYLIST PAST A CHUNKED HTTP ENTRY?
//
// A chunked finite response is the real programme contract: its unknowable encoded byte length
// means net/http cannot send Content-Length. Go must observe that EOF and open the next block; it
// must not delegate advancement to a media-tool demuxer whose behavior can change by release.
func TestLive_BlockSpawnerAdvancesPastAChunkedHTTPBlock(t *testing.T) {
	bin := ffmpegBin(t)

	// One short MPEG-TS, shaped like a program child's output.
	seg := t.TempDir() + "/seg.ts"
	if o, err := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x180:rate=25:duration=2",
		"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
		"-shortest", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac",
		"-f", "mpegts", "-mpegts_flags", "+initial_discontinuity", seg).CombinedOutput(); err != nil {
		t.Fatalf("could not build the segment: %v\n%s", err, o)
	}
	body, err := os.ReadFile(seg)
	if err != nil {
		t.Fatal(err)
	}

	var fetches atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		// NO Content-Length, flushed per chunk — byte-for-byte how pipeChild streams a live
		// programme, which is what makes this a faithful test rather than a synthetic one.
		w.Header().Set("Content-Type", "video/mp2t")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		for i := 0; i < len(body); i += 64 * 1024 {
			end := min(i+64*1024, len(body))
			_, _ = w.Write(body[i:end])
			if f != nil {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	source := BlockSource(func(ctx context.Context, _ string, _ EncodePlan) (Block, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		if err != nil {
			return Block{}, err
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			return Block{}, err
		}
		if fetches.Load() >= 3 {
			// Let the third body reach the mux before ending the live session.
			time.AfterFunc(100*time.Millisecond, cancel)
		}
		return Block{
			Content: resp.Body,
			Identity: AiringIdentity{
				StartedAt: time.Unix(fetches.Load(), 0), Kind: schedule.SlotProgram,
				ContentID: fmt.Sprintf("fetch-%d", fetches.Load()),
			},
		}, nil
	})
	proc, err := BlockSpawner(bin, source, nil)(ctx, "channel", PlanBaseline)
	if err != nil {
		t.Fatalf("block spawner start: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, proc.Stdout) }()
	_ = proc.Wait() // context cancellation ends this intentionally live process.

	if got := fetches.Load(); got < 3 {
		t.Fatalf("block source opened %d times, want at least 3 finite blocks", got)
	}
}

func replaceOutput(args []string, extra ...string) []string {
	out := make([]string, 0, len(args)+len(extra))
	for _, a := range args {
		if a == "pipe:1" {
			continue
		}
		out = append(out, a)
	}
	return append(out, extra...)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
