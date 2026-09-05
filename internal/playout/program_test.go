package playout

import (
	"strings"
	"testing"
	"time"
)

const testStreamURL = "http://emby.local:8096/Videos/abc/stream?static=true&api_key=k"

// transcodeArgs builds a full-TRANSCODE program (the zero CopyPlan copies nothing) — what these
// tests assert. It replaces the old positional ProgramArgs, keeping the call sites readable while
// production takes a ProgramSpec. Audio track 0, no loudness target.
func transcodeArgs(p Profile, input string, offset, limit time.Duration) []string {
	return ProgramArgs(ProgramSpec{Profile: p, Input: input, Offset: offset, Limit: limit})
}

// transcodeArgsLUFS is transcodeArgs with a loudness target (the filler path).
func transcodeArgsLUFS(p Profile, input string, offset, limit time.Duration, lufs string) []string {
	return ProgramArgs(ProgramSpec{Profile: p, Input: input, Offset: offset, Limit: limit, TargetLUFS: lufs})
}

// transcodeArgsAudio is transcodeArgs with an explicit audio track index.
func transcodeArgsAudio(p Profile, input string, offset, limit time.Duration, track int) []string {
	return ProgramArgs(ProgramSpec{Profile: p, Input: input, Offset: offset, Limit: limit, AudioTrack: track})
}

// Arg ORDER is the thing that bites, because ffmpeg is order-sensitive in ways that fail
// silently rather than loudly. This finds the index of a flag so order can be asserted.
func argIndex(args []string, flag string) int {
	for i, a := range args {
		if a == flag {
			return i
		}
	}
	return -1
}

// THE SEEK PLACEMENT. `-ss` before `-i` makes ffmpeg seek (the HTTP server serves a byte
// range, 2.9s for a 40-minute offset into 4K). After `-i` it decodes and DISCARDS from the
// start of the file — minutes of burnt CPU producing nothing, for identical-looking args.
func TestProgramArgs_SeekGoesBeforeTheInput(t *testing.T) {
	args := transcodeArgs(DefaultProfile(), testStreamURL, 40*time.Minute, time.Hour)

	ss, in := argIndex(args, "-ss"), argIndex(args, "-i")
	if ss < 0 {
		t.Fatal("no -ss for a mid-program tune-in — the joiner would restart the show")
	}
	if ss > in {
		t.Errorf("-ss at %d is AFTER -i at %d: ffmpeg would decode-and-discard 40 minutes "+
			"of 4K rather than seeking", ss, in)
	}
	if v, _ := argsAfter(args, "-ss"); v != "2400.000" {
		t.Errorf("-ss = %q, want 2400.000", v)
	}
}

// Tuning in at the very start must not emit a zero seek — harmless, but it means the arg
// builder is not distinguishing "no offset" from "offset of nothing".
func TestProgramArgs_NoSeekWhenStartingAtTheBeginning(t *testing.T) {
	args := transcodeArgs(DefaultProfile(), testStreamURL, 0, time.Hour)
	if argIndex(args, "-ss") >= 0 {
		t.Errorf("emitted a seek for a zero offset: %v", args)
	}
}

// Sub-second precision matters: a channel is a wall clock, and rounding every tune-in to
// whole seconds accumulates drift across a cycle.
func TestProgramArgs_SeekKeepsSubSecondPrecision(t *testing.T) {
	args := transcodeArgs(DefaultProfile(), testStreamURL, 90*time.Second+500*time.Millisecond, 0)
	if v, _ := argsAfter(args, "-ss"); v != "90.500" {
		t.Errorf("-ss = %q, want 90.500 — sub-second precision was lost", v)
	}
}

// EXPLICIT TRACK MAPS, mandatory not tidy. The verified test item had THREE audio tracks;
// without maps ffmpeg's default selection can vary between programs, and a varying track
// count breaks the parent's `-c copy` exactly like a varying resolution does.
func TestProgramArgs_MapsExactlyOneVideoAndOneAudioTrack(t *testing.T) {
	got := joined(transcodeArgs(DefaultProfile(), testStreamURL, 0, time.Hour))
	if !strings.Contains(got, "-map 0:v:0") {
		t.Error("no explicit video map — track selection could vary between programs")
	}
	if !strings.Contains(got, "-map 0:a:0") {
		t.Error("no explicit audio map — a 3-audio-track remux would break -c copy on the parent")
	}
	if strings.Count(got, "-map ") != 2 {
		t.Errorf("want exactly two maps (one video, one audio), got %q", got)
	}
}

// A child must EXIT at its slot boundary — that EOF is the sequencing signal the parent's
// block supervisor acts on. A child that played to the end of the file would overrun its slot.
func TestProgramArgs_BoundsTheChildToItsSlot(t *testing.T) {
	args := transcodeArgs(DefaultProfile(), testStreamURL, 0, 20*time.Minute)
	if v, ok := argsAfter(args, "-t"); !ok || v != "1200.000" {
		t.Errorf("-t = %q, want 1200.000 so the child exits at the slot boundary", v)
	}
	// And a child must never loop — that would pin the channel to one program forever.
	if strings.Contains(joined(args), "-stream_loop") {
		t.Error("a child must not loop; only the parent does")
	}
}

// RECONNECT TIERS MUST NOT BE CROSSED (prior-art §5a). `-reconnect_at_eof` on a CHILD means
// the child tries to continue past the end of its own program, which presents as an
// intermittent stall rather than an error.
func TestProgramArgs_UsesChildReconnectTierNotTheParentOne(t *testing.T) {
	got := joined(transcodeArgs(DefaultProfile(), testStreamURL, 0, time.Hour))

	for _, want := range []string{
		"-reconnect 1", "-reconnect_on_network_error 1",
		"-reconnect_streamed 1", "-multiple_requests 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("child is missing %q — a network blip would kill the slot", want)
		}
	}
	if strings.Contains(got, "-reconnect_at_eof") {
		t.Error("-reconnect_at_eof is the PARENT's flag; on a child it causes intermittent stalls")
	}
}

// `-reconnect*` are options on ffmpeg's HTTP PROTOCOL, not global ones. Against a local file
// input they are a HARD FAILURE — "Option reconnect not found", exit 8, before ffmpeg opens
// anything. Found by executing the args, not by reading them: the arg-shape tests asserted
// the flags were present and passed happily while ffmpeg rejected them.
//
// This is a production path, not a test artifact: filler clips are local files (§10), so an
// unconditional flag list means every commercial break fails to start.
func TestArgs_ReconnectFlagsOnlyForHttpInputs(t *testing.T) {
	local := transcodeArgs(DefaultProfile(), "/media/filler/clip.mp4", 0, time.Minute)
	if got := joined(local); strings.Contains(got, "-reconnect") {
		t.Errorf("local child carries a -reconnect flag; ffmpeg exits 8 with "+
			"\"Option reconnect not found\": %q", got)
	}

	// …but an http input must still get them.
	if got := joined(transcodeArgs(DefaultProfile(), testStreamURL, 0, time.Minute)); !strings.Contains(got, "-reconnect 1") {
		t.Error("an http child lost its reconnect flags — a network blip would kill the slot")
	}
}

// THE FILTER-GRAPH FAILURE the synthetic card could not find (prior-art §5b): scale emits CPU
// frames, and a hardware encoder that wants GPU frames fails with a 40-line pixel-format dump
// that never names the real problem.
//
// The invariant is NOT "every hardware encoder uploads" — an earlier version of this test
// asserted that and was simply wrong about ffmpeg. Only the families with a separate
// hardware frame pool (vaapi, qsv, vulkan) need it; nvenc, amf, videotoolbox, rkmpp and
// v4l2m2m accept CPU frames directly, and forcing an upload on them CAUSES the failure this
// test exists to prevent. The real invariant is: whoever uploads does it AFTER scaling, and
// the per-encoder decision comes from one place.
func TestScaleFilter_UploadsAfterScalingWhereRequired(t *testing.T) {
	for _, enc := range []Encoder{
		EncoderVAAPI, EncoderNVENC, EncoderQSV, EncoderVulkan,
		EncoderAMF, EncoderVideoToolbox, EncoderRKMPP, EncoderV4L2M2M,
	} {
		p := Profile{Width: 1280, Height: 720, Framerate: 25, Encoder: enc}
		vf, ok := argsAfter(p.scaleFilterArgs(""), "-vf")
		if !ok {
			t.Errorf("%s: no filter chain", enc)
			continue
		}
		// Whether this family uploads is the prober's call, not this test's — asking the same
		// helper the args use is what keeps the two from drifting.
		if hardwareUploadFilter(enc) == "" {
			if strings.Contains(vf, "hwupload") {
				t.Errorf("%s: accepts CPU frames directly; an upload here fails at init: %q", enc, vf)
			}
			continue
		}
		if !strings.Contains(vf, "hwupload") {
			t.Errorf("%s: no hwupload after scale — the encoder gets CPU frames and fails "+
				"at init: %q", enc, vf)
		}
		// Order is the whole point: uploading before scaling would run a CPU filter on GPU
		// frames, which is the same error in the other direction.
		if strings.Index(vf, "hwupload") < strings.Index(vf, "scale=") {
			t.Errorf("%s: hwupload precedes scale: %q", enc, vf)
		}
		if !strings.Contains(vf, "format=nv12") {
			t.Errorf("%s: no nv12 conversion before upload: %q", enc, vf)
		}
	}
}

// A hardware encoder that uploads frames also needs its DEVICE initialised, before the input.
// Without it the error is "[hwupload] A hardware device reference is required to upload frames
// to" — which reads like a filter bug. The prober found this once; ProgramArgs reproduced it
// by not reusing the prober's helper, so this pins the composition.
func TestProgramArgs_InitialisesTheHardwareDeviceBeforeTheInput(t *testing.T) {
	for _, enc := range encoderPreference {
		want := deviceInitArgs(enc)
		if len(want) == 0 {
			continue // this family initialises its own context
		}
		p := Profile{Width: 1280, Height: 720, Framerate: 25, Encoder: enc}
		args := transcodeArgs(p, testStreamURL, 0, time.Minute)

		if !strings.Contains(joined(args), joined(want)) {
			t.Errorf("%s: missing device init %v — hwupload would fail with a message that "+
				"names the filter, not the device: %v", enc, want, args)
			continue
		}
		// Global option: after -i it silently applies to nothing.
		if argIndex(args, want[0]) > argIndex(args, "-i") {
			t.Errorf("%s: device init is after -i, so it applies to nothing", enc)
		}
	}
}

// Software needs no upload — and adding one would fail, since there is no hardware device.
func TestScaleFilter_SoftwareGetsNoHardwareUpload(t *testing.T) {
	p := Profile{Width: 1280, Height: 720, Framerate: 25, Encoder: EncoderSoftware}
	vf, _ := argsAfter(p.scaleFilterArgs(""), "-vf")
	if strings.Contains(vf, "hwupload") {
		t.Errorf("software must not hwupload (there is no device): %q", vf)
	}
	// yuv420p explicitly: a 10-bit HDR source would otherwise carry its pixel format through
	// and produce a stream many players cannot decode.
	if !strings.Contains(vf, "format=yuv420p") {
		t.Errorf("no yuv420p — a 10-bit HDR source would pass its format through: %q", vf)
	}
}

// EVERY knob `-c copy` depends on must be pinned, for every encoder family. This is the test
// that catches "a child quietly differed" before it becomes a channel that dies mid-program.
func TestProgramArgs_PinsEverythingConcatDependsOn(t *testing.T) {
	for _, enc := range encoderPreference {
		p := Profile{Width: 1280, Height: 720, Framerate: 25, VideoBitrate: 3000,
			AudioBitrate: 128, Encoder: enc}
		got := joined(transcodeArgs(p, testStreamURL, 0, time.Hour))

		// Resolution AND the pad that preserves it: a bare aspect-preserving scale would
		// emit 960x720 for 4:3 content and break concatenation.
		if !strings.Contains(got, "scale=1280:720") {
			t.Errorf("%s: resolution not pinned: %q", enc, got)
		}
		if !strings.Contains(got, "pad=1280:720") {
			t.Errorf("%s: no pad — 4:3 content would emit different dimensions", enc)
		}
		// Framerate: a 24fps film and a 25fps episode must not produce different rates.
		if !strings.Contains(got, "fps=25") {
			t.Errorf("%s: framerate not pinned: %q", enc, got)
		}
		// Codec + audio layout.
		if !strings.Contains(got, "-c:v "+string(enc)) {
			t.Errorf("%s: video codec not set", enc)
		}
		if !strings.Contains(got, "-c:a aac") || !strings.Contains(got, "-ac 2") ||
			!strings.Contains(got, "-ar 48000") {
			t.Errorf("%s: audio layout not pinned — breaks -c copy like video does", enc)
		}
		// Container + the mid-flight timestamp flag.
		if !strings.Contains(got, "-f mpegts") {
			t.Errorf("%s: not muxing to mpegts", enc)
		}
		if !strings.Contains(got, "+initial_discontinuity") {
			t.Errorf("%s: no initial_discontinuity — we seeked, so timestamps are not zero-based", enc)
		}
	}
}

// Realtime pacing WITH a burst. Pacing alone is correct but feels broken: a joining player
// has an empty buffer and waits for it to fill at 1.0x before showing anything.
func TestProgramArgs_PacesRealtimeWithATuneInBurst(t *testing.T) {
	args := transcodeArgs(DefaultProfile(), testStreamURL, 5*time.Minute, time.Hour)

	if v, ok := argsAfter(args, "-readrate"); !ok || v != "1.0" {
		t.Errorf("-readrate = %q, want 1.0 — without pacing we race ahead of wall-clock", v)
	}
	if v, ok := argsAfter(args, "-readrate_initial_burst"); !ok || v == "0" {
		t.Errorf("-readrate_initial_burst = %q, want a burst so tune-in is not slow", v)
	}
	// Pacing must be an INPUT option (before -i) or it applies to nothing.
	if rr := argIndex(args, "-readrate"); rr > argIndex(args, "-i") {
		t.Error("-readrate is after -i, so it applies to no input")
	}
}

// A burst is tune-in acceleration, not ordinary channel pacing. At a programme boundary the
// parent asks for the new child at offset zero; bursting that child makes it reach EOF ten seconds
// before the wall clock boundary, so the next resolve returns the same programme and repeats its
// tail. The viewer sees every commercial transition roughly ten seconds late in both directions.
func TestProgramArgs_DoesNotBurstAtAProgrammeBoundary(t *testing.T) {
	for _, offset := range []time.Duration{0, time.Second, 9 * time.Second} {
		args := transcodeArgs(DefaultProfile(), testStreamURL, offset, time.Hour)
		if v, ok := argsAfter(args, "-readrate_initial_burst"); ok {
			t.Errorf("offset %s: -readrate_initial_burst = %q; a boundary child must stay on wall clock", offset, v)
		}
	}
}

// The initial child can finish early when a viewer tunes into the last few seconds of an airing.
// Its follow-up request is still the SAME airing until the wall clock catches up; bursting that
// short tail again turns one early finish into a replay loop.
func TestProgramArgs_DoesNotBurstTheShortTailBeforeABoundary(t *testing.T) {
	args := transcodeArgs(DefaultProfile(), testStreamURL, 40*time.Minute, 9*time.Second)
	if v, ok := argsAfter(args, "-readrate_initial_burst"); ok {
		t.Errorf("-readrate_initial_burst = %q; a tail shorter than the burst must play at wall-clock pace", v)
	}
}

// Progress on a dedicated fd, same as the card — stdout carries the MPEG-TS, so mixing
// progress text into it would corrupt the transport stream.
func TestProgramArgs_ProgressIsStructuredAndOffStdout(t *testing.T) {
	for name, args := range map[string][]string{
		"child": transcodeArgs(DefaultProfile(), testStreamURL, 0, time.Hour),
		"mux":   BlockMuxArgs(),
	} {
		v, ok := argsAfter(args, "-progress")
		if !ok || !strings.HasPrefix(v, "pipe:") {
			t.Errorf("%s: -progress = %q, want a pipe fd", name, v)
		}
		if v == "pipe:1" {
			t.Errorf("%s: progress on stdout would corrupt the MPEG-TS", name)
		}
	}
}

func TestBlockMuxArgs_BoundsPipeInputAnalysis(t *testing.T) {
	args := BlockMuxArgs()
	input := argIndex(args, "-i")
	for flag, want := range map[string]string{
		"-readrate":        "1.0",
		"-probesize":       "256k",
		"-analyzeduration": "500000",
	} {
		got, ok := argsAfter(args, flag)
		if !ok || got != want {
			t.Errorf("%s = %q, want %q", flag, got, want)
		}
		if index := argIndex(args, flag); index < 0 || index > input {
			t.Errorf("%s must precede -i so it applies to the MPEG-TS pipe input", flag)
		}
	}
	if joined := strings.Join(args, " "); !strings.Contains(joined, "-map 0:v:0 -map 0:a:0") {
		t.Errorf("mux must select one canonical video/audio pair, args = %q", joined)
	}
}

// Both processes write the stream to stdout, which is what Process.Stdout fans out.
func TestArgs_OutputGoesToStdout(t *testing.T) {
	for name, args := range map[string][]string{
		"child": transcodeArgs(DefaultProfile(), testStreamURL, 0, time.Hour),
		"mux":   BlockMuxArgs(),
		"card":  TestCardArgs(DefaultProfile(), "", "CH", ""),
	} {
		if args[len(args)-1] != "pipe:1" {
			t.Errorf("%s: last arg = %q, want pipe:1", name, args[len(args)-1])
		}
	}
}

// seconds must never emit exponent notation — ffmpeg would parse "1e-06" as a token rather
// than a duration.
func TestSeconds_NeverUsesExponentNotation(t *testing.T) {
	for _, d := range []time.Duration{
		time.Microsecond, time.Millisecond, time.Second,
		90*time.Minute + 500*time.Millisecond, 0,
	} {
		got := seconds(d)
		if strings.ContainsAny(got, "eE") {
			t.Errorf("seconds(%v) = %q, which ffmpeg cannot parse", d, got)
		}
	}
}

// EVERY encoder must get an explicit 8-bit pixel format, one way or another.
//
// This is the invariant that was missing, and it cost a live channel. The chain previously
// added `format=yuv420p` for SOFTWARE ONLY, so nvenc/amf/videotoolbox/rkmpp/v4l2m2m — the
// families that take CPU frames directly — received whatever the source had. A 10-bit HEVC
// film then reached h264_nvenc as yuv420p10le and it refused:
//
//	[h264_nvenc] No capable devices found
//
// which names the DEVICE, not the format, so it reads as a missing GPU. The old test suite
// asserted the upload step and the scale/pad/fps pins, but never that the OUTPUT FORMAT is
// 8-bit — the one thing a 10-bit source can break.
func TestScaleFilter_EveryEncoderPinsAnEightBitPixelFormat(t *testing.T) {
	for _, enc := range encoderPreference {
		p := Profile{Width: 1920, Height: 1080, Framerate: 25, Encoder: enc}
		vf, ok := argsAfter(p.scaleFilterArgs(""), "-vf")
		if !ok {
			t.Errorf("%s: no filter chain at all", enc)
			continue
		}
		// nv12 (hardware upload path) and yuv420p (everything else) are both 8-bit. What
		// must never happen is NEITHER, which is what let 10-bit through.
		if !strings.Contains(vf, "format=nv12") && !strings.Contains(vf, "format=yuv420p") {
			t.Errorf("%s: no pixel-format pin — a 10-bit source reaches the encoder as "+
				"yuv420p10le and is rejected with a message that blames the device: %q", enc, vf)
		}
	}
}

// And specifically for the families that accept CPU frames: they must get yuv420p, since they
// have no upload filter to pin the format for them.
func TestScaleFilter_CPUFrameEncodersGetYuv420p(t *testing.T) {
	for _, enc := range []Encoder{
		EncoderNVENC, EncoderAMF, EncoderVideoToolbox, EncoderRKMPP, EncoderV4L2M2M,
	} {
		p := Profile{Width: 1920, Height: 1080, Framerate: 25, Encoder: enc}
		vf, _ := argsAfter(p.scaleFilterArgs(""), "-vf")
		if !strings.Contains(vf, "format=yuv420p") {
			t.Errorf("%s takes CPU frames and has no upload filter, so it needs an explicit "+
				"8-bit format: %q", enc, vf)
		}
		// …and must NOT have an upload, which would fail with no hardware frame context.
		if strings.Contains(vf, "hwupload") {
			t.Errorf("%s: unexpected hwupload — it accepts CPU frames directly: %q", enc, vf)
		}
	}
}

// --- Hardware decode ---

// DECODING is where the CPU actually goes on high-resolution sources. Measured on a 4K 10-bit
// HEVC film: moving only the ENCODE to the GPU took CPU from 260% to 341% (it ROSE — the decode
// was always the cost, and it stopped being throttled by a slow software encoder); adding GPU
// decode took it to ~0%.
func TestProgramArgs_HardwareEncodersAlsoDecodeOnTheGPU(t *testing.T) {
	want := map[Encoder]string{
		EncoderNVENC:        "cuda",
		EncoderVAAPI:        "vaapi",
		EncoderQSV:          "vaapi", // both decode through VAAPI on Linux
		EncoderVideoToolbox: "videotoolbox",
		EncoderVulkan:       "vulkan",
	}
	for enc, accel := range want {
		p := Profile{Width: 1280, Height: 720, Framerate: 25, Encoder: enc}
		args := transcodeArgs(p, testStreamURL, 0, time.Minute)
		got := joined(args)
		if !strings.Contains(got, "-hwaccel "+accel) {
			t.Errorf("%s: no -hwaccel %s — the decode stays on the CPU and dominates on 4K: %q",
				enc, accel, got)
		}
		// -hwaccel is an INPUT option: after -i it applies to the next input, i.e. nothing.
		if hw := argIndex(args, "-hwaccel"); hw > argIndex(args, "-i") {
			t.Errorf("%s: -hwaccel is after -i, so it applies to no input", enc)
		}
	}
}

// Software must NOT hardware-decode: it would decode on the GPU only to download every frame
// back for a CPU encode, which is strictly slower than decoding on the CPU.
func TestProgramArgs_SoftwareDoesNotHardwareDecode(t *testing.T) {
	p := Profile{Width: 1280, Height: 720, Framerate: 25, Encoder: EncoderSoftware}
	if got := joined(transcodeArgs(p, testStreamURL, 0, time.Minute)); strings.Contains(got, "-hwaccel") {
		t.Errorf("libx264 asked for hardware decode; the download would cost more than it saves: %q", got)
	}
}

// ⚠ NEVER `-hwaccel_output_format`. It keeps frames in GPU memory (faster) but turns any
// unsupported input into a HARD FAILURE instead of a silent fallback to software decode.
// ffmpeg cannot hardware-decode every codec, and a channel must not die because one film in its
// lineup is VC-1.
//
// It is ALSO what keeps the CPU scale/pad chain working: `scale_cuda` has no pad option, so 4:3
// content through a GPU-only chain emits 1440x1080 instead of a letterboxed 1920x1080 — which
// breaks the continuous mux's `-c copy` on any channel mixing aspect ratios (verified against
// real ffmpeg).
func TestProgramArgs_NoHwaccelOutputFormat(t *testing.T) {
	for _, enc := range encoderPreference {
		p := Profile{Width: 1920, Height: 1080, Framerate: 25, Encoder: enc}
		got := joined(transcodeArgs(p, testStreamURL, 0, time.Minute))
		if strings.Contains(got, "-hwaccel_output_format") {
			t.Errorf("%s: -hwaccel_output_format makes an unsupported codec fatal instead of "+
				"falling back to software decode, AND strands the CPU pad filter: %q", enc, got)
		}
		// The CPU scale+pad must survive, since that is what pins the output dimensions.
		if !strings.Contains(got, "pad=1920:1080") {
			t.Errorf("%s: lost the CPU pad — 4:3 content would emit 1440x1080 and break "+
				"-c copy on the parent: %q", enc, got)
		}
	}
}

// Loudness normalisation (§10 V40).
//
// ⚠ **The default path must be byte-identical to what shipped before V40.** A feature film
// normalised to advert loudness loses its dynamic range, and the problem being solved — adverts
// recorded a decade apart at wildly different levels, measured at an 11 dB spread across real
// fetched clips — is a FILLER problem. So no target means no filter at all, not a filter with a
// neutral value.
func TestProgramArgs_NoLoudnessFilterWithoutATarget(t *testing.T) {
	args := transcodeArgs(DefaultProfile(), testStreamURL, 0, time.Minute)

	if i := argIndex(args, "-af"); i != -1 {
		t.Errorf("an audio filter was added with no target: %v", args[i:i+2])
	}
	if strings.Contains(strings.Join(args, " "), "loudnorm") {
		t.Error("loudnorm reached a library program; only filler is normalised")
	}
}

// A filler clip gets the filter, at the requested target.
func TestProgramArgs_NormalisesFillerToTheTarget(t *testing.T) {
	args := transcodeArgsLUFS(DefaultProfile(), testStreamURL, 0, time.Minute, "-23")

	i := argIndex(args, "-af")
	if i == -1 {
		t.Fatalf("no audio filter for a normalised clip: %v", args)
	}
	filter := args[i+1]
	if !strings.Contains(filter, "loudnorm") || !strings.Contains(filter, "I=-23") {
		t.Errorf("filter = %q, want loudnorm at I=-23", filter)
	}
	// ⚠ A true-peak ceiling is not optional: `loudnorm` raising a quiet clip toward the target
	// can push transients past 0 dBFS, and the lossy AAC encode below overshoots further. -1 dBTP
	// is the EBU R128 ceiling and leaves that headroom.
	if !strings.Contains(filter, "TP=-1") {
		t.Errorf("filter = %q, want a true-peak ceiling — normalising up can clip", filter)
	}
}

// ⚠ The filter must precede the CODEC it feeds. ffmpeg is order-sensitive in ways that fail
// silently: `-af` after `-c:a` is accepted and applies to nothing.
func TestProgramArgs_LoudnessFilterComesBeforeTheAudioCodec(t *testing.T) {
	args := transcodeArgsLUFS(DefaultProfile(), testStreamURL, 0, time.Minute, "-23")

	af, codec := argIndex(args, "-af"), argIndex(args, "-c:a")
	if af == -1 || codec == -1 {
		t.Fatalf("missing -af (%d) or -c:a (%d)", af, codec)
	}
	if af > codec {
		t.Errorf("-af at %d comes after -c:a at %d; it would apply to nothing", af, codec)
	}
}

// The single-pass form, deliberately. Two-pass loudnorm measures the whole file before emitting a
// frame — fine for a batch transcode, fatal for a live stream that must start now.
func TestProgramArgs_LoudnessIsSinglePass(t *testing.T) {
	args := transcodeArgsLUFS(DefaultProfile(), testStreamURL, 0, time.Minute, "-23")

	if joined := strings.Join(args, " "); strings.Contains(joined, "measured_I") {
		t.Error("two-pass loudnorm would stall the stream until the whole clip was read")
	}
}

// --- HDR → SDR tone-mapping ----------------------------------------------------

// hdrSource is a PQ/BT.2020 10-bit source, as ffprobe reports one.
func hdrSource() MediaFormat {
	return MediaFormat{
		VideoCodec: "hevc", PixelFormat: "yuv420p10le",
		ColorTransfer: "smpte2084", Width: 3840, Height: 2160,
	}
}

// BOTH CONDITIONS ARE REQUIRED, and they fail in opposite directions.
//
// Content-is-HDR and build-can-tone-map are independent facts, and folding either into the other
// breaks something: emitting the chain on a build without zscale kills the channel at graph-init,
// while tone-mapping an SDR source compresses a range that was already correct.
func TestTonemapStep_NeedsBothHDRContentAndACapableBuild(t *testing.T) {
	for _, tc := range []struct {
		name    string
		spec    ProgramSpec
		wantMap bool
	}{
		{"hdr source, capable build", ProgramSpec{Source: hdrSource(), Tonemap: true}, true},
		{"hdr source, build without zscale", ProgramSpec{Source: hdrSource(), Tonemap: false}, false},
		{"sdr source, capable build", ProgramSpec{Source: MediaFormat{ColorTransfer: "bt709"}, Tonemap: true}, false},
		{"unprobed source, capable build", ProgramSpec{Tonemap: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.spec.tonemapStep() != ""; got != tc.wantMap {
				t.Errorf("tone-map = %v, want %v", got, tc.wantMap)
			}
		})
	}
}

// HLG is HDR too. `arib-std-b67` is the other transfer function broadcast HDR ships as, and the
// chain handles it identically (zscale reads the input transfer from the stream) — so the only
// thing that could get it wrong is the predicate.
func TestTonemapStep_CoversHLGNotJustPQ(t *testing.T) {
	hlg := ProgramSpec{Source: MediaFormat{ColorTransfer: "arib-std-b67"}, Tonemap: true}
	if hlg.tonemapStep() == "" {
		t.Error("HLG source was not tone-mapped; it is HDR exactly as PQ is")
	}
}

// PLACEMENT IS THE CORRECTNESS BIT, and getting it wrong produces no error — only a worse picture.
//
//   - After `fps`/`scale`, because tone-mapping is per-pixel and the scale has already cut a 4K
//     frame to 1080p. Before it, the same result costs four times the pixels on a realtime budget.
//   - Before `format=yuv420p`, because the chain's last zscale PRESERVES BIT DEPTH. Reversed, the
//     8-bit truncation happens first and the tone-map operates on an already-flattened picture —
//     which is most of what this change exists to fix.
func TestScaleFilter_TonemapsAfterScalingAndBeforePixelFormat(t *testing.T) {
	p := Profile{Width: 1920, Height: 1080, Framerate: 25, Encoder: EncoderSoftware}
	vf, ok := argsAfter(p.scaleFilterArgs(hdrToSDRChain), "-vf")
	if !ok {
		t.Fatal("no filter chain")
	}

	iScale := strings.Index(vf, "scale=1920:1080")
	iTonemap := strings.Index(vf, "tonemap=tonemap=")
	iFormat := strings.Index(vf, "format=yuv420p")
	if iScale < 0 || iTonemap < 0 || iFormat < 0 {
		t.Fatalf("chain is missing a step (scale=%d tonemap=%d format=%d): %q", iScale, iTonemap, iFormat, vf)
	}
	if iScale > iTonemap {
		t.Errorf("tone-map runs before the scale — four times the pixels for the same result: %q", vf)
	}
	if iTonemap > iFormat {
		t.Errorf("tone-map runs after the 8-bit conversion, so it maps an already-truncated "+
			"picture — the defect this fixes: %q", vf)
	}
}

// The hardware families upload to GPU memory, and the tone-map is a CPU filter — so it must land
// before the upload, or it is handed frames it cannot read.
func TestScaleFilter_TonemapsBeforeTheHardwareUpload(t *testing.T) {
	for _, enc := range []Encoder{EncoderVAAPI, EncoderQSV, EncoderVulkan} {
		p := Profile{Width: 1920, Height: 1080, Framerate: 25, Encoder: enc}
		vf, _ := argsAfter(p.scaleFilterArgs(hdrToSDRChain), "-vf")
		iTonemap, iUpload := strings.Index(vf, "tonemap=tonemap="), strings.Index(vf, "hwupload")
		if iTonemap < 0 || iUpload < 0 {
			t.Errorf("%s: chain missing tonemap or hwupload: %q", enc, vf)
			continue
		}
		if iTonemap > iUpload {
			t.Errorf("%s: CPU tone-map placed after the GPU upload; it cannot read those frames: %q", enc, vf)
		}
	}
}

// An SDR program must be byte-for-byte what it was before tone-mapping existed. The overwhelming
// majority of programs take this path, so a regression here is a regression everywhere.
func TestScaleFilter_SDRChainIsUnchanged(t *testing.T) {
	p := Profile{Width: 1920, Height: 1080, Framerate: 25, Encoder: EncoderSoftware}
	vf, _ := argsAfter(p.scaleFilterArgs(""), "-vf")
	for _, unwanted := range []string{"zscale", "tonemap"} {
		if strings.Contains(vf, unwanted) {
			t.Errorf("SDR chain picked up %q: %q", unwanted, vf)
		}
	}
}

// THE MISLABELLING HALF, which is the one no client could recover from: SDR pixels still
// announcing PQ/BT.2020, because ffmpeg carries source colour metadata to the output when nothing
// changes it.
//
// The relabelling is done by the tone-map's final zscale, not by output flags — see hdrToSDRChain
// for the measurement showing the flags were redundant. So the assertion is that the CHAIN is
// present or absent, and the live test (TestLive_HDRSourceIsTonemappedAndLabelledSDR) is what
// proves the resulting file is actually tagged bt709.
func TestProgramArgs_TonemapAppearsOnlyWhenBothConditionsHold(t *testing.T) {
	base := ProgramSpec{
		Profile: Profile{Width: 1920, Height: 1080, Framerate: 25, VideoBitrate: 5000, Encoder: EncoderSoftware},
		Input:   "/m.mkv",
	}

	hdr := base
	hdr.Source, hdr.Tonemap = hdrSource(), true
	got := joined(ProgramArgs(hdr))
	if !strings.Contains(got, "tonemap=tonemap=") {
		t.Fatalf("HDR program was not tone-mapped: %q", got)
	}
	// The relabelling is the chain's own last step; without it the output keeps the source's
	// smpte2084/bt2020 tags while carrying SDR pixels.
	if !strings.Contains(got, "zscale=p=bt709") {
		t.Errorf("tone-map does not convert back to bt709, so the output stays labelled HDR: %q", got)
	}

	// SDR on a capable build: untouched.
	sdr := base
	sdr.Tonemap = true
	if got := joined(ProgramArgs(sdr)); strings.Contains(got, "tonemap") {
		t.Errorf("an SDR program was tone-mapped, compressing a range that was already correct: %q", got)
	}

	// HDR on a build that cannot tone-map: no chain at all. Emitting it would fail at graph-init
	// and take the channel with it, which is strictly worse than a flat picture.
	incapable := base
	incapable.Source = hdrSource()
	if got := joined(ProgramArgs(incapable)); strings.Contains(got, "zscale") {
		t.Errorf("emitted the chain on a build without zscale — graph-init would fail: %q", got)
	}
}

// A COPY is never tone-mapped. `-c:v copy` decodes nothing, so there is no filter graph to put the
// chain in — and an HDR program copied to an HDR-capable client is CORRECT, not a defect.
func TestProgramArgs_CopyIsNeverTonemapped(t *testing.T) {
	spec := ProgramSpec{
		Profile: Profile{Width: 1920, Height: 1080, Framerate: 25, Encoder: EncoderSoftware},
		Input:   "/m.mkv",
		Source:  hdrSource(),
		Tonemap: true,
		Plan:    CopyPlan{CopyVideo: true, CopyAudio: true},
	}
	got := joined(ProgramArgs(spec))
	if strings.Contains(got, "tonemap") || strings.Contains(got, "-color_trc") {
		t.Errorf("a video copy grew a filter chain: %q", got)
	}
}
