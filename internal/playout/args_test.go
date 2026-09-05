package playout

import (
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Arg-shape tests are cheap and run everywhere. The ones that actually EXECUTE ffmpeg
// live in ffmpeg_live_test.go behind a build tag, because unit tests must not depend on
// a binary being present (AGENTS.md: unit tests never touch the network, and the same
// spirit applies to external executables).

func argsAfter(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func joined(args []string) string { return strings.Join(args, " ") }

// The three details Tunarr's card taught us, asserted so a future "simplification"
// cannot quietly remove them (prior-art §5a).
func TestTestCardArgs_CarriesTheThreeLoadBearingFlags(t *testing.T) {
	got := joined(TestCardArgs(DefaultProfile(), "/f.ttf", "CH 1", ""))

	// A video-only MPEG-TS is a classic cause of a player refusing to play or showing
	// no timeline. The silent track is not optional.
	if !strings.Contains(got, "anullsrc") {
		t.Error("no anullsrc — a video-only MPEG-TS will not play reliably")
	}
	// Without -re, lavfi generates as fast as the CPU allows and floods the pipe.
	if !strings.Contains(got, "-re ") {
		t.Error("no -re — the synthetic source would race ahead of wall-clock")
	}
	// A generated source that EOFs ends the channel.
	if !strings.Contains(got, "-stream_loop -1") {
		t.Error("no -stream_loop -1 — the card would end")
	}
}

// Progress must use the platform's structured line protocol, never stdout where it would
// corrupt the MPEG-TS.
func TestTestCardArgs_ProgressIsStructured(t *testing.T) {
	args := TestCardArgs(DefaultProfile(), "", "", "")
	if v, ok := argsAfter(args, "-progress"); !ok || !strings.HasPrefix(v, "pipe:") {
		t.Errorf("-progress = %q, want the platform's structured pipe", v)
	}
	if !strings.Contains(joined(args), "-nostats") {
		t.Error("want -nostats so the only progress output is the structured stream")
	}
}

// Every segment boundary must land on a keyframe, and segment durations must not vary —
// a TARGETDURATION that lies is a player error, not a warning.
func TestGopArgs_PinKeyframesAndDisableSceneDetection(t *testing.T) {
	// 2-second GOP: keyframes are the most expensive frames, so a 1-second interval spends a
	// large share of the bitrate re-sending full pictures instead of detail. Asserted from the
	// constant rather than a literal, so the intent survives a retune of the interval.
	p := DefaultProfile()
	gop := strconv.Itoa(p.Framerate * gopKeyframeSeconds)
	got := joined(p.videoEncodeArgs())
	for _, want := range []string{"-g " + gop, "-keyint_min " + gop, "-sc_threshold 0"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

// Audio is fixed AAC stereo 48k. A varying audio layout across programs breaks `-c copy`
// on the parent exactly like video does.
func TestAudioEncodeArgs_FixedStereo48k(t *testing.T) {
	got := joined(DefaultProfile().audioEncodeArgs())
	for _, want := range []string{"-c:a aac", "-ac 2", "-ar 48000"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

// Each encoder family has its OWN option vocabulary, and an unknown option fails at INIT
// rather than being ignored — several (notably v4l2m2m on a Pi) are strict.
//
// The invariant is about `-tune` specifically, verified against real ffmpeg: only libx264
// and nvenc define it. `-preset` is NOT a safe thing to assert on, because QSV genuinely
// has its own `veryfast` (an int enum, `-h encoder=h264_qsv`) that merely shares libx264's
// spelling — an earlier version of this test asserted "no hardware encoder says veryfast"
// and was simply wrong about ffmpeg.
func TestVideoEncodeArgs_NoTuneOnEncodersThatLackIt(t *testing.T) {
	tuneless := []Encoder{
		EncoderVAAPI, EncoderQSV, EncoderAMF, EncoderVideoToolbox,
		EncoderRKMPP, EncoderV4L2M2M, EncoderVulkan,
	}
	for _, enc := range tuneless {
		got := joined(Profile{Encoder: enc, Framerate: 25}.videoEncodeArgs())
		if strings.Contains(got, "-tune") {
			t.Errorf("%s does not accept -tune; it would fail at init: %q", enc, got)
		}
	}
	// The two that DO define it should use it, with their own values.
	sw := joined(Profile{Encoder: EncoderSoftware, Framerate: 25}.videoEncodeArgs())
	if !strings.Contains(sw, "-preset veryfast") || !strings.Contains(sw, "-tune zerolatency") {
		t.Errorf("libx264 should be veryfast+zerolatency for live, got %q", sw)
	}
	// nvenc speaks pN presets, not libx264's words. p7 (slowest/best) because the GPU has the
	// headroom — measured at 5.4x realtime for 1080p — and it compounds with CQ rate control.
	nv := joined(Profile{Encoder: EncoderNVENC, Framerate: 25}.videoEncodeArgs())
	if !strings.Contains(nv, "-preset p7") || !strings.Contains(nv, "-tune hq") {
		t.Errorf("nvenc should use its own pN/hq vocabulary, got %q", nv)
	}
	// AMF speaks -quality, not -preset.
	amf := joined(Profile{Encoder: EncoderAMF, Framerate: 25}.videoEncodeArgs())
	if !strings.Contains(amf, "-quality") || strings.Contains(amf, "-preset") {
		t.Errorf("AMF uses -quality rather than -preset, got %q", amf)
	}
}

// Every family must at least be constructible — a missing case in the switch would emit a
// bare codec with no rate control, which encodes at whatever default the driver picks.
func TestVideoEncodeArgs_EveryFamilyGetsRateControl(t *testing.T) {
	for _, enc := range encoderPreference {
		got := joined(Profile{Encoder: enc, Framerate: 25, VideoBitrate: 4000}.videoEncodeArgs())
		if !strings.Contains(got, "-c:v "+string(enc)) {
			t.Errorf("%s: codec not set: %q", enc, got)
		}
		// EVERY family must have a CEILING, whichever rate-control mode it uses. Quality-
		// targeted encoders (nvenc: -rc vbr -cq N -b:v 0) legitimately have no -b:v target,
		// but an uncapped live encoder blows a client's buffer — so maxrate is the invariant,
		// not -b:v.
		if !strings.Contains(got, "-maxrate") {
			t.Errorf("%s: no bitrate ceiling — a live encoder can spike and blow a client buffer: %q", enc, got)
		}
		if !strings.Contains(got, "-b:v 4000k") && !strings.Contains(got, "-cq ") {
			t.Errorf("%s: neither a bitrate target nor a quality target: %q", enc, got)
		}
		if !strings.Contains(got, "-g 50") {
			t.Errorf("%s: no pinned GOP — segment boundaries need keyframes: %q", enc, got)
		}
	}
}

// A channel name comes from the database and is operator-supplied. An unescaped
// apostrophe breaks the filter graph; an unescaped colon silently introduces ANOTHER
// filter option, which is worse than broken.
func TestDrawTextFilter_EscapesOperatorText(t *testing.T) {
	got := drawTextFilter("/f.ttf", "Bob's 90s: Movies", "", 720)
	if strings.Contains(got, "text='Bob's") {
		t.Errorf("apostrophe not escaped — broken filter graph: %q", got)
	}
	if !strings.Contains(got, `Bob\'s`) {
		t.Errorf("want an escaped apostrophe, got %q", got)
	}
	if !strings.Contains(got, `90s\: Movies`) {
		t.Errorf("want an escaped colon (it introduces a filter option), got %q", got)
	}
}

// No font ⇒ no drawtext. drawtext without a fontfile fails at INIT on a minimal image,
// so a missing font must degrade to a plain colour field rather than kill the channel.
func TestDrawTextFilter_MissingFontDegradesInsteadOfFailing(t *testing.T) {
	if got := drawTextFilter("", "CH 1", "", 720); got != "" {
		t.Errorf("no font must yield no filter, got %q", got)
	}
	args := TestCardArgs(DefaultProfile(), "", "CH 1", "")
	if strings.Contains(joined(args), "drawtext") {
		t.Error("args carry drawtext with no font — ffmpeg would fail at init")
	}
	// …but the card itself must still be produced.
	if !strings.Contains(joined(args), "color=c=black") {
		t.Error("the colour field must survive a missing font")
	}
}

// FindFont must not invent a path: a non-existent fontfile is the init failure above.
func TestFindFont_ReturnsOnlyAnExistingFile(t *testing.T) {
	got := FindFont()
	if got == "" {
		t.Skip("no font on this system — the degrade path is covered above")
	}
	if _, err := exec.LookPath("test"); err == nil { // trivially available
		if !fileExists(got) {
			t.Errorf("FindFont returned %q which does not exist", got)
		}
	}
}

// THE ARTIFACT FIX. Capped CBR (`-b:v N -maxrate N`) leaves the encoder no headroom, so every
// hard scene — grain, motion, shadow detail in a dark HDR film — is crushed to fit the same
// budget as an easy one. Measured with SSIM against a near-lossless reference:
//
//	CBR 5000k            SSIM 0.98262
//	VBR cq 21, cap 10M   SSIM 0.98581
//
// An earlier comment in this file claimed hardware could not use a quality target. That was
// wrong about NVENC, and this test exists so the claim cannot quietly return.
func TestRateControl_NvencUsesAQualityTargetWithHeadroom(t *testing.T) {
	got := joined(Profile{Encoder: EncoderNVENC, Framerate: 25, VideoBitrate: 5000}.videoEncodeArgs())

	if !strings.Contains(got, "-rc vbr") || !strings.Contains(got, "-cq ") {
		t.Errorf("nvenc is not quality-targeted; capped CBR crushes hard scenes: %q", got)
	}
	// `-b:v 0` is load-bearing: with a non-zero bitrate, nvenc treats cq as an upper bound on
	// a bitrate-targeted encode and the result is nearly indistinguishable from plain CBR.
	if !strings.Contains(got, "-b:v 0") {
		t.Errorf("cq without -b:v 0 degenerates to CBR: %q", got)
	}
	// Headroom, but not unbounded — a live stream that spikes arbitrarily blows a client's
	// buffer. 2x the ladder target.
	if !strings.Contains(got, "-maxrate 10000k") {
		t.Errorf("want a 2x ceiling above the 5000k ladder target: %q", got)
	}
}

// The quality target tracks the ladder rung, so an operator's tier choice and the load-aware
// step-down still govern picture quality rather than being overridden by a fixed CQ.
func TestRateControl_QualityTargetFollowsTheLadder(t *testing.T) {
	var last int
	for _, bitrate := range []int{8000, 5000, 3000, 1500} {
		cq := Profile{Encoder: EncoderNVENC, VideoBitrate: bitrate}.constantQuality()
		if cq == 0 {
			t.Fatalf("no quality target at %dk", bitrate)
		}
		// Lower rungs mean a busier box, so the target loosens (a HIGHER cq is looser).
		if last != 0 && cq <= last {
			t.Errorf("cq did not loosen going from a higher rung to %dk: %d then %d",
				bitrate, last, cq)
		}
		last = cq
	}
}

// Only NVENC is measured, so only NVENC gets a quality target. VAAPI/QSV have plausible
// equivalents (-rc_mode CQP, -global_quality) but an unmeasured quality value is a channel that
// either looks bad or saturates the uplink — neither of which fails loudly.
func TestRateControl_UnmeasuredFamiliesStayBitrateTargeted(t *testing.T) {
	for _, enc := range []Encoder{EncoderVAAPI, EncoderQSV, EncoderVulkan, EncoderAMF, EncoderV4L2M2M} {
		got := joined(Profile{Encoder: enc, Framerate: 25, VideoBitrate: 5000}.videoEncodeArgs())
		if strings.Contains(got, "-cq ") {
			t.Errorf("%s got an unmeasured quality target: %q", enc, got)
		}
		if !strings.Contains(got, "-b:v 5000k") {
			t.Errorf("%s lost its bitrate target: %q", enc, got)
		}
	}
}

// AN HEVC ENCODER MUST GET ITS ENGINE'S HARDWARE PLUMBING (§9.1 V49).
//
// deviceInitArgs, hardwareUploadFilter and hardwareDecodeArgs each switch on the encoder. All
// three listed only the h264 constants, so every hevc_* encoder fell through to `default` and
// received nothing: no `-vaapi_device`, no `-init_hw_device` for QSV/Vulkan, no `-hwaccel cuda`
// for NVENC.
//
// The consequence was worse than a clean failure. The hardware encode produced no bytes, the
// program ladder fell back to libx264, and for an fMP4 (HEVC) session that mid-stream codec change
// is the black frame the plan exists to prevent — so a defect in the DEVICE SETUP surfaced as a
// codec bug three layers away.
//
// The assertion is pairwise rather than a table of expected strings: an engine's h264 and HEVC
// encoders run on the SAME hardware and must therefore be set up identically, which stays true if
// the args themselves are ever revised.
func TestHardwarePlumbing_HevcMatchesItsH264Sibling(t *testing.T) {
	for _, base := range h264Engines {
		hevc := hevcVariant(base)
		if hevc == base {
			t.Fatalf("%s has no HEVC sibling — hevcVariant and h264Engines have drifted apart", base)
		}

		if want, got := deviceInitArgs(base), deviceInitArgs(hevc); !slices.Equal(want, got) {
			t.Errorf("%s device init = %v, want %v (same engine as %s)", hevc, got, want, base)
		}
		if want, got := hardwareUploadFilter(base), hardwareUploadFilter(hevc); want != got {
			t.Errorf("%s upload filter = %q, want %q (same engine as %s)", hevc, got, want, base)
		}
		if want, got := hardwareDecodeArgs(base), hardwareDecodeArgs(hevc); !slices.Equal(want, got) {
			t.Errorf("%s decode args = %v, want %v (same engine as %s)", hevc, got, want, base)
		}
	}
}

func TestSoftwareEncoderForPreservesTheOutputCodec(t *testing.T) {
	for _, tc := range []struct {
		preferred Encoder
		want      Encoder
	}{
		{EncoderVideoToolbox, EncoderSoftware},
		{EncoderVTHEVC, EncoderSoftwareHEVC},
		{EncoderNVENC, EncoderSoftware},
		{EncoderNVENCHEVC, EncoderSoftwareHEVC},
		{EncoderSoftware, EncoderSoftware},
		{EncoderSoftwareHEVC, EncoderSoftwareHEVC},
	} {
		if got := SoftwareEncoderFor(tc.preferred); got != tc.want {
			t.Errorf("SoftwareEncoderFor(%q) = %q, want %q", tc.preferred, got, tc.want)
		}
		if !IsSoftwareEncoder(tc.want) {
			t.Errorf("IsSoftwareEncoder(%q) = false", tc.want)
		}
	}
}

// The three engines that need an explicit device must actually name one, in BOTH codecs.
//
// The pairwise test above would stay green if a refactor emptied both halves of a pair at once —
// which is close to the state this change found, since every HEVC encoder returned nothing. This
// pins the content, so "plumbing disappeared everywhere" cannot read as agreement.
func TestHardwarePlumbing_DeviceEnginesNameADevice(t *testing.T) {
	for _, base := range []Encoder{EncoderVAAPI, EncoderQSV, EncoderVulkan} {
		for _, enc := range []Encoder{base, hevcVariant(base)} {
			if got := deviceInitArgs(enc); len(got) == 0 {
				t.Errorf("%s got no device-init args; it cannot open its encode context without one", enc)
			}
		}
	}
	// NVENC takes no device init but MUST get hardware decode — losing `-hwaccel cuda` is the
	// measured 341%→0% CPU regression on 4K sources.
	for _, enc := range []Encoder{EncoderNVENC, EncoderNVENCHEVC} {
		if got := strings.Join(hardwareDecodeArgs(enc), " "); !strings.Contains(got, "cuda") {
			t.Errorf("%s lost its CUDA hardware decode: %q", enc, got)
		}
	}
}
