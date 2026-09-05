package playout

import (
	"fmt"
	"strconv"
	"time"
)

// ffmpeg argument construction for internal playout (§9.1).
//
// Args are built as a []string, never a shelled-out command line — a filter graph
// contains commas, colons and quotes, and handing that to a shell is how you get an
// injection or an unexplainable parse error.
//
// Shape borrowed from Tunarr/ErsatzTV (see docs/engineering/archive/playout-prior-art.md):
// options are appended in POSITIONAL buckets, because ffmpeg is order-sensitive in ways
// that are easy to get wrong — an input option after `-i` applies to the *next* input,
// and a filter before its input is a parse error.
//
//	global → per-input (opts then -i) → filter → per-output → output target

// Encoder is a video encoder ffmpeg can use. Not an exhaustive list of what ffmpeg
// supports — only what playout offers, which is H.264 in software or via one of the
// eight hardware families below (§15 `playout.encoder`).
type Encoder string

// The families ErsatzTV maintains pipelines for, which is the breadth real deployments
// need: NVIDIA, Intel, AMD on both OSes, Apple Silicon, and ARM SBCs. Availability is never
// assumed from this list — `Detect` asks the local ffmpeg build and then tries each one.
const (
	EncoderSoftware     Encoder = "libx264"           // always available
	EncoderNVENC        Encoder = "h264_nvenc"        // NVIDIA
	EncoderQSV          Encoder = "h264_qsv"          // Intel Quick Sync
	EncoderVAAPI        Encoder = "h264_vaapi"        // Intel AND AMD on Linux
	EncoderAMF          Encoder = "h264_amf"          // AMD on Windows
	EncoderVideoToolbox Encoder = "h264_videotoolbox" // Apple Silicon / Intel Macs
	EncoderRKMPP        Encoder = "h264_rkmpp"        // Rockchip SBCs
	EncoderV4L2M2M      Encoder = "h264_v4l2m2m"      // Raspberry Pi, V4L2 stateful encoders
	EncoderVulkan       Encoder = "h264_vulkan"       // cross-vendor, newer
)

// HEVC encoder variants (§9.1 V49). When an HEVC-capable client's session (EncodePlan hevc8/10)
// must TRANSCODE a non-HEVC program (a VP9/h264/mpeg2 commercial), it transcodes to HEVC — not h264 —
// so the fMP4 stream the browser plays stays uniformly one codec. (fMP4/MSE binds ONE decoder from
// its init segment and cannot survive a mid-stream codec change; mixing h264 into an HEVC fMP4 is a
// black frame.) Each is the hevc sibling of the h264 encoder above, on the same hardware engine, so
// it shares that family's preset vocabulary — see hevcVariant and the family-keyed videoEncodeArgs.
// ⚠ TYPED `Encoder`, not bare strings. They were untyped constants until 2026-08-09, and that is
// the mechanical reason nine encoders silently skipped every `switch enc Encoder` in capability.go:
// an untyped constant satisfies `Encoder` where one is expected, so nothing ever failed to compile
// — it just never matched a case either. Typing them does not by itself fix a missing case, but it
// makes the omission the kind of thing a reader and a linter can see.
const (
	EncoderSoftwareHEVC Encoder = "libx265"
	EncoderNVENCHEVC    Encoder = "hevc_nvenc"
	EncoderQSVHEVC      Encoder = "hevc_qsv"
	EncoderVAAPIHEVC    Encoder = "hevc_vaapi"
	EncoderAMFHEVC      Encoder = "hevc_amf"
	EncoderVTHEVC       Encoder = "hevc_videotoolbox"
	EncoderRKMPPHEVC    Encoder = "hevc_rkmpp"
	EncoderV4L2M2MHEVC  Encoder = "hevc_v4l2m2m"
	EncoderVulkanHEVC   Encoder = "hevc_vulkan"
)

// h264Engines is every encoder that NAMES a hardware engine — the canonical member of each
// engine's h264/HEVC pair. It is the iteration source for engineOf below and, deliberately, the
// same list encoderPreference draws from.
var h264Engines = []Encoder{
	EncoderSoftware, EncoderNVENC, EncoderQSV, EncoderVAAPI, EncoderAMF,
	EncoderVideoToolbox, EncoderRKMPP, EncoderV4L2M2M, EncoderVulkan,
}

// engineOf normalizes an encoder to the h264 constant naming its HARDWARE ENGINE, so a switch over
// engines matches an HEVC variant exactly as it matches its h264 sibling.
//
// ⚠ **Why this exists, and why it is DERIVED rather than written out.** Three functions in
// capability.go — deviceInitArgs, hardwareUploadFilter, hardwareDecodeArgs — key on the raw encoder
// value, and each listed only the h264 constants. So every hevc_* encoder fell to `default` and got
// `deviceInit=[] hwdec=[] upload=""`: no `-vaapi_device`, no `-init_hw_device` for QSV/Vulkan, no
// `-hwaccel cuda` for NVENC. The consequence was not a clean failure but a WORSE one — the
// hardware encode produced nothing, the ladder fell through to libx264, and for an HEVC fMP4
// session that mid-stream codec change is the black frame the plan exists to prevent.
//
// familyOf is NOT the right tool here and the distinction matters: it collapses VAAPI, Vulkan,
// VideoToolbox, RKMPP and V4L2M2M into `familyOther` because they share a *preset vocabulary*.
// These three functions need them kept APART, because they differ in exactly the thing being
// selected — the device, the upload filter, the decode flag.
//
// The map is built from hevcVariant rather than hand-written, so the pair list has one home. A
// tenth engine added there is normalized here with no second edit — which is the property the
// original three switches lacked.
var engineByEncoder = func() map[Encoder]Encoder {
	m := make(map[Encoder]Encoder, len(h264Engines)*2)
	for _, h := range h264Engines {
		m[h] = h
		m[hevcVariant(h)] = h
	}
	return m
}()

func engineOf(e Encoder) Encoder {
	if base, ok := engineByEncoder[e]; ok {
		return base
	}
	return e
}

// hevcVariant maps an h264 encoder to its HEVC sibling on the same hardware engine (§9.1 V49). The
// caller uses this when an hevc-plan session must transcode a non-HEVC program to keep the fMP4
// stream uniform. Returns the input unchanged if it has no known HEVC sibling — a safe degrade to
// h264 (the program still plays; only the fMP4-uniformity optimisation is lost for that encoder).
// HEVCEncoderFor is the exported entry the API's program path uses to pick the HEVC encoder for an
// hevc-plan transcode (§9.1 V49) — a thin wrapper over hevcVariant so the mapping stays single-sourced.
func HEVCEncoderFor(h264 Encoder) Encoder { return hevcVariant(h264) }

// SoftwareEncoderFor returns the software encoder that preserves the requested encoder's output
// codec. A live session pins one codec for its lifetime, so an HEVC hardware failure must fall back
// to libx265 rather than silently switching the stream to H.264/libx264.
func SoftwareEncoderFor(enc Encoder) Encoder {
	if engineOf(enc) != enc {
		return EncoderSoftwareHEVC
	}
	return EncoderSoftware
}

// IsSoftwareEncoder reports whether enc is either supported software codec. Treating libx265 as a
// hardware encoder would incorrectly acquire a GPU slot and attempt VRAM reclamation.
func IsSoftwareEncoder(enc Encoder) bool {
	return enc == EncoderSoftware || enc == EncoderSoftwareHEVC
}

func hevcVariant(h264 Encoder) Encoder {
	switch h264 {
	case EncoderSoftware:
		return EncoderSoftwareHEVC
	case EncoderNVENC:
		return EncoderNVENCHEVC
	case EncoderQSV:
		return EncoderQSVHEVC
	case EncoderVAAPI:
		return EncoderVAAPIHEVC
	case EncoderAMF:
		return EncoderAMFHEVC
	case EncoderVideoToolbox:
		return EncoderVTHEVC
	case EncoderRKMPP:
		return EncoderRKMPPHEVC
	case EncoderV4L2M2M:
		return EncoderV4L2M2MHEVC
	case EncoderVulkan:
		return EncoderVulkanHEVC
	default:
		return h264
	}
}

// encoderFamily groups an encoder (h264 OR hevc variant) by the hardware engine whose preset/rate
// vocabulary it uses — nvenc's `-preset p7` and CQ mode are identical for h264_nvenc and hevc_nvenc,
// libx26x share `-preset veryfast`, and so on. videoEncodeArgs switches on the FAMILY so an hevc
// variant reuses its h264 sibling's argument logic without a parallel switch.
type encoderFamily int

const (
	familyOther    encoderFamily = iota // takes quality from bitrate args alone (vaapi/vt/rkmpp/v4l2/vulkan)
	familySoftware                      // libx264 / libx265
	familyNVENC                         // h264_nvenc / hevc_nvenc
	familyQSV                           // h264_qsv / hevc_qsv
	familyAMF                           // h264_amf / hevc_amf
)

func familyOf(e Encoder) encoderFamily {
	switch e {
	case EncoderSoftware, EncoderSoftwareHEVC:
		return familySoftware
	case EncoderNVENC, EncoderNVENCHEVC:
		return familyNVENC
	case EncoderQSV, EncoderQSVHEVC:
		return familyQSV
	case EncoderAMF, EncoderAMFHEVC:
		return familyAMF
	default:
		return familyOther
	}
}

// Profile is the normalized output every program is encoded to.
//
// Normalization is not cosmetic — it is what makes the continuous copy mux legal. The
// mux consumes per-program streams with `-c copy`, which is only valid
// if every child produced identical resolution, framerate, codec and pixel format.
// A child that quietly differs produces a stream players reject mid-program, and the
// symptom (a channel that dies a few minutes in) points nowhere near the cause.
type Profile struct {
	Width     int
	Height    int
	Framerate int
	// VideoBitrate in kbit/s. 0 = let the encoder choose (CRF-ish for software).
	VideoBitrate int
	Encoder      Encoder
	// AudioBitrate in kbit/s; audio is always AAC stereo 48kHz. Fixed deliberately:
	// a varying audio layout across programs breaks `-c copy` exactly like video does,
	// and ErsatzTV's comment about ac3 downmix reinit failures is the warning
	// (prior-art §5).
	AudioBitrate int
}

// DefaultProfile is a conservative 720p/25 H.264 target. Chosen for compatibility over
// quality: this is what a media server will remux to whatever the client wants, so the
// job here is to be universally decodable, not to look best.
func DefaultProfile() Profile {
	return Profile{
		Width: 1280, Height: 720, Framerate: 25,
		VideoBitrate: 4000, Encoder: EncoderSoftware, AudioBitrate: 128,
	}
}

// videoEncodeArgs returns the codec + rate-control args for the profile's encoder.
//
// One switch, dispatching per encoder — the shape both reference implementations use.
// Families differ enough in rate control that a shared "just set -b:v" would be wrong,
// but they share the bitrate/GOP helpers below.
func (p Profile) videoEncodeArgs() []string {
	args := []string{"-c:v", string(p.Encoder)}
	// Each family has its OWN preset vocabulary, and an unknown preset name fails at init
	// rather than being ignored. That is why this is a switch over families and not a
	// shared "-preset" line: libx264's "veryfast" is meaningless to nvenc, and nvenc's
	// "p4" is meaningless to everything else. Keyed on the FAMILY (familyOf) so an hevc
	// variant (hevc_nvenc, libx265, …) reuses its h264 sibling's args — the preset/rate
	// vocabulary is identical within a hardware engine regardless of the output codec.
	switch familyOf(p.Encoder) {
	case familySoftware:
		// veryfast because playout is realtime and a dropped frame is worse than a
		// slightly larger one. `-tune zerolatency` stops the encoder buffering frames it
		// would rather reorder — for live, latency beats compression. (libx264 and libx265
		// share these option names.)
		args = append(args, "-preset", "veryfast", "-tune", "zerolatency")
	case familyNVENC:
		// p7 (slowest/best) rather than p4. Measured with SSIM against a near-lossless
		// reference of a hard scene — dark, grainy, 4K HDR source:
		//
		//	p4  SSIM 0.98262   1656ms per 20s of 1080p
		//	p7  SSIM 0.98295   3721ms per 20s of 1080p  ← 5.4x realtime, still ample
		//
		// The quality gain from the preset alone is small; the reason to take it is that a
		// GPU sitting at ~14% utilisation has the headroom for free, and it compounds with
		// the CQ rate control below. p1/ll is still avoided: viewra found it produced
		// visible grain artifacts in tone-mapped content (prior-art, viewra §6).
		args = append(args, "-preset", "p7", "-tune", "hq")
	case familyAMF:
		// AMF speaks quality presets, not numbered ones.
		args = append(args, "-quality", "balanced")
	case familyQSV:
		// QSV's preset names overlap libx264's spelling but are its own enum.
		args = append(args, "-preset", "veryfast")
	case familyOther:
		// vaapi / videotoolbox / rkmpp / v4l2m2m / vulkan (h264 AND hevc) take their quality from
		// the bitrate args alone. Adding a preset from another family's vocabulary is an init
		// failure, and several (notably v4l2m2m on a Pi) are strict about unknown options.
	}
	args = append(args, p.rateControlArgs()...)
	return append(args, p.gopArgs()...)
}

// rateControlArgs decides how the encoder spends its bits.
//
// THE ARTIFACTS THIS FIXES. The first hardware-encoded channel looked worse than the software
// one, and the cause was capped CBR: `-b:v N -maxrate N` gives the encoder no headroom at all,
// so every hard scene — grain, fast motion, the shadow detail in a dark HDR film — is crushed
// to fit the same budget as an easy one. Software never had this problem because it also got a
// CRF target, and an earlier comment here claimed hardware could not use one. That was simply
// wrong about NVENC, and SSIM against a near-lossless reference of a hard scene shows by how
// much:
//
//	CBR 5000k          (shipped)  SSIM 0.98262   12MB / 20s
//	VBR, cq 23, cap 8M            SSIM 0.98444   17MB
//	VBR, cq 21, cap 10M           SSIM 0.98581   23MB  ← chosen
//	VBR, cq 19, cap 12M           SSIM 0.98681   29MB
//
// cq 21 takes most of the available gain; 19 costs another 26% bitrate for a quarter as much
// improvement. The MAXRATE CAP IS STILL THERE — quality-targeted does not mean unbounded, and
// a live stream that spikes arbitrarily blows a client's buffer.
func (p Profile) rateControlArgs() []string {
	if p.VideoBitrate <= 0 {
		return nil
	}
	kbps := strconv.Itoa(p.VideoBitrate) + "k"

	if cq := p.constantQuality(); cq > 0 {
		// Quality-targeted with a ceiling: the encoder holds picture quality steady and
		// spends up to `maxrate` when a scene needs it. The cap is 2x the ladder's target
		// rather than the target itself, which is what buys the headroom.
		//
		// `-b:v 0` is REQUIRED, not decorative: with a non-zero bitrate set, nvenc treats cq
		// as an upper quality bound on a bitrate-targeted encode and the result is nearly
		// indistinguishable from plain CBR.
		return []string{
			"-rc", "vbr", "-cq", strconv.Itoa(cq), "-b:v", "0",
			"-maxrate", strconv.Itoa(p.VideoBitrate*2) + "k",
			"-bufsize", strconv.Itoa(p.VideoBitrate*4) + "k",
		}
	}

	// Bitrate-targeted for families with no usable quality mode. maxrate+bufsize as well as
	// -b:v: without a cap a live encoder can spike far above target on a hard scene.
	args := []string{"-b:v", kbps, "-maxrate", kbps, "-bufsize",
		strconv.Itoa(p.VideoBitrate*2) + "k"}
	// Software gets a CRF target on top; with maxrate/bufsize still set, libx264 holds
	// quality steady and only spends bits up to the cap.
	return append(args, p.qualityArgs()...)
}

// constantQuality returns the CQ/QP value for encoders with a usable quality mode, or 0.
//
// Only NVENC for now, and deliberately so — this is the one family measured. VAAPI has
// `-rc_mode CQP`, QSV has `-global_quality`, and both are plausible next steps, but each needs
// its own SSIM run against real content before being switched on: a wrong quality value is a
// channel that either looks bad or saturates the operator's uplink, and neither fails loudly.
func (p Profile) constantQuality() int {
	// NVENC's VBR+CQ rate control is identical for h264_nvenc and hevc_nvenc (same engine), so both
	// get the measured CQ ladder; every other family stays bitrate-targeted until measured.
	if familyOf(p.Encoder) != familyNVENC {
		return 0
	}
	// Derived from the ladder rung so the operator's tier choice still governs. The rungs
	// descend in bitrate, so a lower rung means a busier box and a correspondingly looser
	// quality target — the same "adapt to load" policy the ladder itself implements.
	switch {
	case p.VideoBitrate >= 6000:
		return 19
	case p.VideoBitrate >= 4000:
		return 21
	case p.VideoBitrate >= 2000:
		return 23
	default:
		return 26
	}
}

// gopKeyframeSeconds is how often a keyframe is forced.
//
// TWO seconds, not one. Keyframes are the most expensive frames in a stream, so a 1-second GOP
// spends a large share of the bitrate re-sending full pictures instead of detail — measurable,
// though modest next to the rate-control fix (SSIM 0.98262 → 0.98269 on its own).
//
// Two seconds is still short enough that a viewer tuning in mid-program waits at most ~2s for
// a decodable frame, which is well inside the time a media server spends buffering anyway.
const gopKeyframeSeconds = 2

// gopArgs pins the keyframe interval.
//
// Every HLS segment boundary must land on a keyframe or a client joining mid-stream sees
// nothing until the next one. `-sc_threshold 0` disables scene-change detection, which would
// otherwise insert keyframes at unpredictable places and make segment durations vary — and a
// TARGETDURATION that lies is a player error, not a warning.
//
// ⚠ **A keyframe is forced on FRAME 0, and this is the cold-start black-screen fix (measured).**
// The HLS remux (`-c copy -f hls`) can only cut a `.ts` segment on a keyframe. With only the 2s GOP
// below, a cold transcode child's first keyframe lands up to a full GOP in — and on a slow cold
// encoder init it lands late enough that `awaitPlaylist` (45s) times out with NO segment and the
// viewer gets a 502 / black frame (a channel measured at 45s vs 0.4s for a direct-play channel).
// `-forced-idr 1` makes the forced points real IDRs (not just P-frames flagged as keyframes, which
// some encoders emit and the remux cannot cut on), and the `eq(n,0)` term puts one on the very first
// frame so segment 0 cuts immediately. The `gte(t,prev_forced_t+GOP)` term keeps the steady 2s
// cadence. Output-side keyframe control, so it is hardware-agnostic — works for nvenc/vaapi/qsv/
// vulkan/software alike, and only ever emitted on the transcode path (a `-c copy` program never
// calls videoEncodeArgs).
func (p Profile) gopArgs() []string {
	gop := strconv.Itoa(p.Framerate * gopKeyframeSeconds)
	return []string{
		"-g", gop, "-keyint_min", gop, "-sc_threshold", "0",
		// Force a keyframe on the first frame (instant segment 0), then every gopKeyframeSeconds.
		"-forced-idr", "1",
		"-force_key_frames", fmt.Sprintf("expr:if(eq(n,0),1,gte(t,prev_forced_t+%d))", gopKeyframeSeconds),
	}
}

// audioEncodeArgs is fixed AAC stereo 48kHz — see Profile.AudioBitrate.
func (p Profile) audioEncodeArgs() []string {
	return p.audioEncodeArgsNormalised("")
}

// audioEncodeArgsNormalised is audioEncodeArgs plus an optional loudness filter (§10 V40).
//
// ⚠ **Normalisation happens HERE, at playout, and never rewrites the file on disk.** The clip
// folder holds files a person put there, and Loomarr has never modified them. In-place is
// destructive and unrepeatable: the original cannot be recovered, and a re-scan cannot tell it has
// already happened — so a second pass would normalise an already-normalised file, and a third
// would do it again. One filter on a stream already being encoded is reversible, and changing the
// target later simply works.
//
// ⚠ **Single-pass `loudnorm`, deliberately, despite ffmpeg's docs preferring two.** Two-pass
// measures the whole file first, which means reading it end-to-end BEFORE emitting a frame — fine
// for a batch transcode, fatal for a live stream that must start now. The single-pass form is
// approximate on the first second or so and correct thereafter, which is the right trade for a
// 30-second advert: nobody hears the ramp, and the alternative is a stall at every break.
//
// `targetLUFS` empty ⇒ no filter at all, which is exactly what a library program gets: normalising
// a feature film to advert loudness would flatten its dynamic range, and the problem this solves
// (adverts recorded a decade apart at wildly different levels) is a FILLER problem.
func (p Profile) audioEncodeArgsNormalised(targetLUFS string) []string {
	args := []string{}
	if targetLUFS != "" {
		// I=integrated target, TP=true-peak ceiling, LRA=loudness range. -1 dBTP is the EBU R128
		// ceiling and leaves headroom for the lossy encode below to overshoot without clipping.
		args = append(args, "-af", "loudnorm=I="+targetLUFS+":TP=-1:LRA=11")
	}
	return append(args,
		"-c:a", "aac",
		"-b:a", strconv.Itoa(p.AudioBitrate)+"k",
		"-ac", "2",
		"-ar", "48000",
	)
}

// TestCardArgs builds the args for a synthetic test card: a captioned colour field with
// silent audio, muxed to MPEG-TS on stdout, forever.
//
// This is deliberately the first thing playout can do, because it proves the whole
// pipeline — encode, mux, pipe, serve — with NO library content, no scheduler and no
// media server. If the card plays, everything downstream of the encoder works.
//
// Three details are load-bearing, each learned from Tunarr's `ffmpegText.ts` rather than
// guessed (prior-art §5a):
//
//   - `anullsrc` gives a SILENT AUDIO TRACK. A video-only MPEG-TS is a classic cause of
//     a player refusing to play or showing no timeline. It is not optional.
//   - `-re` reads the synthetic source at realtime. Without it lavfi generates as fast
//     as the CPU allows and floods the pipe, racing ahead of wall-clock.
//   - `-stream_loop -1` so a generated source never EOFs and ends the channel.
func TestCardArgs(p Profile, fontFile, title, subtitle string) []string {
	args := []string{
		"-hide_banner", "-loglevel", "error",
		// Progress as line-framed machine-readable key=value — never stdout, which carries
		// the MPEG-TS. Unix gives it dedicated fd 3; Windows demultiplexes exact protocol
		// lines from stderr because Go does not support ExtraFiles there. The platform value
		// and wiring live at one seam so they cannot drift into "Bad file descriptor" again.
		"-progress", progressPipeArg(), "-nostats",
	}
	// Video: a plain colour field, paced to realtime, looping forever.
	args = append(args,
		"-f", "lavfi", "-re", "-stream_loop", "-1",
		"-i", fmt.Sprintf("color=c=black:s=%dx%d:r=%d", p.Width, p.Height, p.Framerate),
	)
	// Audio: silence. Explicit layout + rate so it matches the encode profile exactly;
	// letting anullsrc default and then resampling is a needless filter.
	args = append(args,
		"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
	)
	if vf := drawTextFilter(fontFile, title, subtitle, p.Height); vf != "" {
		args = append(args, "-vf", vf)
	}
	args = append(args, p.videoEncodeArgs()...)
	args = append(args, p.audioEncodeArgs()...)
	// mpegts to stdout. `+initial_discontinuity` tells a downstream demuxer that the
	// first timestamps are not necessarily zero, which is true for anything joining a
	// live stream mid-flight (ErsatzTV sets the same flag).
	return append(args,
		"-f", "mpegts", "-mpegts_flags", "+initial_discontinuity", "pipe:1",
	)
}

// OfflineCardArgs is TestCardArgs bounded to a fixed duration — the card a channel plays when
// nothing is airing (§9.1).
//
// The bound is the whole difference, and it is required rather than cosmetic. TestCardArgs loops
// FOREVER by design (`-stream_loop -1`, so a generated source never EOFs mid-channel), which is
// right for a standing test pattern and wrong for a program slot: the block supervisor advances on
// its child's EOF, so a card that never ends means the channel can never pick up content that
// later lands. Bounding it makes the supervisor ask again.
//
// `-t` goes before the output target, where it applies to the OUTPUT. As an input option it
// would instead limit how much of the looping source is READ, which for an infinite generated
// source means something subtly different.
func OfflineCardArgs(p Profile, fontFile, title, subtitle string, d time.Duration) []string {
	args := TestCardArgs(p, fontFile, title, subtitle)
	// Insert before the trailing output target rather than appending: ffmpeg applies an option
	// to whatever comes after it, so `pipe:1 -t 30` would be a parse error.
	out := args[len(args)-1]
	args = append(args[:len(args)-1], "-t", seconds(d))
	return append(args, out)
}

// drawTextFilter centres a title, with an optional subtitle beneath it. Returns "" when
// there is no font — drawtext without a fontfile fails at init on a minimal image, so a
// missing font degrades to a plain colour field rather than killing the channel.
func drawTextFilter(fontFile, title, subtitle string, height int) string {
	if fontFile == "" || title == "" {
		return ""
	}
	size := height / 12 // scales with the profile instead of a fixed 30px
	f := fmt.Sprintf(
		"drawtext=fontfile=%s:fontsize=%d:fontcolor=white:x=(w-text_w)/2:y=(h-text_h)/2:text='%s'",
		fontFile, size, escapeDrawText(title))
	if subtitle != "" {
		f += fmt.Sprintf(
			",drawtext=fontfile=%s:fontsize=%d:fontcolor=white:x=(w-text_w)/2:y=(h+text_h+%d)/2:text='%s'",
			fontFile, size*2/3, size, escapeDrawText(subtitle))
	}
	return f
}

// escapeDrawText neutralises the characters that terminate or redirect a drawtext
// expression. A channel name is operator-supplied text arriving here from the database,
// so an unescaped apostrophe is a broken filter graph at best — and `:` introduces
// another filter option, which is worse than broken.
func escapeDrawText(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '\'', ':', '\\', '%':
			out = append(out, '\\', r)
		case '\n', '\r':
			out = append(out, ' ')
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
