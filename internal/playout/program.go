package playout

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Per-program encode args (§9.1, prior-art §1) — DIRECT PLAY by default (V47).
//
// A program is COPIED when its codec already fits the target, and transcoded only for the
// streams that do not (copyplan.go, PlanCopy). The common case — an h264 file to a browser or
// TV — copies the video untouched (instant, no GPU) and at most re-encodes an incompatible audio
// track. Transcoding the whole program is the exception, for a codec the target genuinely cannot
// play (HEVC/MPEG-2 to an h264-only client).
//
// Every child conforms to the session's pinned broadcast format before it enters the continuous
// copy mux. Direct copy is therefore conservative: codec, geometry, cadence, pixel format and audio
// shape must already match; unknown or mismatched properties transcode to the pinned profile. A
// logical airing boundary alone does not require an HLS decoder discontinuity.
//
// The transcode flags below are each verified in Tunarr's source or against the live dev Emby
// (prior-art §5a–§5c); the ones that look redundant are the ones a real failure found.

// readrateInitialBurst is how many seconds of content ffmpeg may read flat-out on a genuine
// mid-program tune-in before settling to realtime pacing.
//
// This is the TUNE-IN LATENCY FIX (prior-art §5a, Tunarr's ReadrateInputOption). Realtime
// pacing alone is correct but feels broken: a player joining a live stream has an empty
// buffer and must wait for it to fill at 1.0x before showing anything. The burst fills it
// immediately, then pacing settles down so we do not race ahead of wall-clock.
//
// Deliberately NOT applied to synthetic sources — pacing a lavfi generator with a burst
// stalls the pipeline (see TestCardArgs, which uses plain `-re`).
const readrateInitialBurst = 10

// tuneInBurstThreshold keeps ordinary programme boundaries on the wall clock. A new session that
// joins at least this far into an airing needs the latency win; a child opened near offset zero is
// the parent advancing normally and racing it ahead would make the next resolve replay its tail.
const tuneInBurstThreshold = time.Duration(readrateInitialBurst) * time.Second

// ProgramSpec is everything one program's encode needs. A struct rather than the old positional
// ladder (ProgramArgs → …WithAudio → …Normalised, which had reached six parameters): the copy plan
// is a first-class field, not a seventh positional, and adding the next knob widens a struct instead
// of forking another function.
type ProgramSpec struct {
	Profile Profile
	// Input is the ffmpeg input — a local file path (direct play) or an HTTP URL (fallback). The
	// input-option branch (reconnect flags) keys on isHTTP, so both are handled from the one field.
	Input         string
	Offset, Limit time.Duration
	AudioTrack    int      // the N in -map 0:a:N (PickAudioTrack); 0 = the file's first track
	TargetLUFS    string   // filler loudness normalisation (§10 V40); "" = none (library titles)
	Plan          CopyPlan // per-stream copy/transcode decision (PlanCopy); zero value = transcode both
	// UnpacedInput is reserved for immutable prepared media whose downstream Channel mux is the
	// wall-clock pacing authority. Leaving read-rate on this child would pace the authoritative
	// intra-segment seek itself and turn that discarded distance into cold-start latency.
	UnpacedInput bool

	// Source is the PROBE the Plan was derived from — the full MediaFormat, not the two booleans
	// it reduces to.
	//
	// copyplan.go has promised this since it was written ("Probe once, keep it all… so later
	// features need no second ffprobe"), and until now nothing collected on it: the resolver
	// probed, computed CopyVideo/CopyAudio, and dropped everything else, so the HDR flag it went
	// to the trouble of parsing had no production caller at all. Tone-mapping is the first
	// feature to need it; SAR, field order and the copy path's missing geometry guard are the
	// same shape and can now read from here rather than growing a second probe each.
	//
	// The zero value is the safe direction on purpose. A probe that failed yields a zero
	// MediaFormat, whose HDR() is false, so an unprobed source is treated as SDR — washed-out at
	// worst. The opposite default would tone-map SDR content, which damages a picture that was
	// correct.
	Source MediaFormat
	// Tonemap reports whether this ffmpeg BUILD can tone-map (zscale + tonemap present). Resolved
	// by the composition root via TonemapperFor, not probed here, because ProgramArgs is a pure
	// function and asking a binary from inside it would make every arg test exec ffmpeg.
	//
	// It is deliberately separate from Source.HDR(): one is a property of the CONTENT, the other
	// of the INSTALL, and both must hold. See filters.go for why a missing filter is fatal rather
	// than degrading if emitted anyway.
	Tonemap bool
}

// tonemapStep returns the HDR→SDR filter chain for this program, or "" when it should not run.
//
// Both conditions are required and they fail in opposite directions, which is why neither is
// folded into the other: HDR content on a build without zscale must NOT emit the filter (the graph
// would fail at init and the channel would die — see filters.go), and an SDR source on a build
// that CAN tone-map must not be tone-mapped either (it would compress a range that was already
// correct).
func (s ProgramSpec) tonemapStep() string {
	if !s.Tonemap || !s.Source.HDR() {
		return ""
	}
	return hdrToSDRChain
}

// ProgramArgs builds the args to encode (or COPY) ONE program, starting Offset in, for Limit.
//
// This is what the "what's on now" endpoint spawns per program. It streams finite MPEG-TS to stdout
// and then EXITS — that EOF is the sequencing signal (prior-art §1). Nothing here loops.
//
// The copy plan drives the shape:
//   - Plan.CopyVideo ⇒ `-c:v copy`, and the whole transcode apparatus (hardware device init,
//     hardware decode, scale filter, video encode) is SKIPPED — a copy decodes nothing, so setting
//     up a decoder/encoder would be wasted work and, worse, a chance for a hardware-init failure to
//     take down a program that needed no hardware at all.
//   - else the video transcodes to the Profile (the exception path, unchanged from before).
//   - Plan.CopyAudio ⇒ `-c:a copy`; else the audio alone transcodes to AAC.
func ProgramArgs(spec ProgramSpec) []string {
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-progress", progressPipeArg(), "-nostats",
	}

	// Hardware setup is a TRANSCODE concern: a video copy neither decodes nor encodes, so it needs
	// no device and no hardware decoder. Emitting them for a copy is not just wasteful — a device
	// init that fails (no /dev/dri in a container) would kill a program that could have copied fine.
	if !spec.Plan.CopyVideo {
		// HARDWARE DEVICE SETUP, before everything — it is a global option; after `-i` it silently
		// applies to nothing. Reused from the capability prober (per-encoder correct: QSV needs an
		// explicit render node, Vulkan names its device differently).
		args = append(args, deviceInitArgs(spec.Profile.Encoder)...)
		// HARDWARE DECODE. Measured on a 4K 10-bit HEVC film with an RTX 3080 Ti: the child went
		// from 341% CPU to ~0%, the GPU decoder taking it instead. For 4K sources the decode
		// dominates, so moving only the encode to the GPU barely helped.
		args = append(args, hardwareDecodeArgs(spec.Profile.Encoder)...)
	}

	// --- Input options (before -i, so they apply to THIS input) ---
	p, streamURL, offset, limit, audioTrack, targetLUFS := spec.Profile, spec.Input, spec.Offset, spec.Limit, spec.AudioTrack, spec.TargetLUFS

	// Reconnect flags, CHILD tier — and ONLY for an http input. See isHTTP.
	//
	// A child fetching a program from an HTTP media source should survive a network blip
	// mid-program rather than killing the slot. These must NOT include `-reconnect_at_eof`,
	// which belongs to the parent, where a child's EOF is the advance signal; on a child it
	// means the child tries to continue past the end of its own program, presenting as an
	// intermittent stall (prior-art §5a: "the two tiers must not get each other's flags").
	if isHTTP(streamURL) {
		args = append(args,
			"-reconnect", "1",
			"-reconnect_on_network_error", "1",
			"-reconnect_streamed", "1",
			"-multiple_requests", "1",
		)
	}

	// Ordinary live children own their input pacing. An immutable prepared child deliberately does
	// not: its downstream Channel mux is the sole pacing authority, and pacing before -ss makes
	// FFmpeg consume the discarded part of the current segment at 1x before emitting transport.
	if !spec.UnpacedInput {
		args = append(args, "-readrate", "1.0")
		// The burst is only for a genuine mid-program tune-in with enough media left to absorb it.
		// Applying it at offset zero makes every child finish ten seconds before its wall-clock
		// boundary; applying it to that short remaining tail repeats the same mistake. Both presented
		// live as commercials arriving and leaving about ten seconds late.
		if offset >= tuneInBurstThreshold && limit > tuneInBurstThreshold {
			args = append(args, "-readrate_initial_burst", strconv.Itoa(readrateInitialBurst))
		}
	}

	// THE SEEK, and its placement is load-bearing. `-ss` BEFORE `-i` makes ffmpeg seek —
	// over HTTP the server serves a byte range, verified at 2.9s wall-clock for a 40-minute
	// offset into a 4K remux (prior-art §5c). After `-i` it would decode and DISCARD from
	// the start of the file, which for the same offset takes minutes and burns a core
	// producing nothing.
	//
	// Sub-second precision is deliberate: a channel is a wall clock, and rounding every
	// tune-in to whole seconds would accumulate drift across a cycle.
	if offset > 0 {
		args = append(args, "-ss", seconds(offset))
	}

	args = append(args, "-i", streamURL)

	// --- Output options ---

	// EXPLICIT TRACK SELECTION, and it is mandatory rather than tidy (prior-art §5b). The
	// verified test item carried THREE audio tracks (dts, flac, ac3) plus subtitles. Without
	// maps, ffmpeg's default selection picks by its own heuristics — so the track count can
	// differ between programs, and a varying track count breaks the parent's `-c copy`
	// exactly like a varying resolution does.
	//
	// First video, ONE audio, nothing else. Subtitles are dropped: burning them in would
	// vary per item and there is no subtitle track in a normalized MPEG-TS profile.
	//
	// Which audio is chosen by the caller (see audio.go). It used to be hardcoded `0:a:0` —
	// the first track in the file — which is how a channel ended up playing a film in Russian:
	// the release simply carried its Russian dub first. Exactly one audio track either way,
	// because a varying track count breaks `-c copy` as surely as a varying resolution.
	args = append(args, "-map", "0:v:0", "-map", "0:a:"+strconv.Itoa(audioTrack))

	// `-t` bounds the child to its slot. This is what makes the child exit at the program
	// boundary rather than playing to the end of the file — which matters when the lineup
	// gives an item less time than its full duration (a rolling window, or a slot the
	// scheduler trimmed).
	if limit > 0 {
		args = append(args, "-t", seconds(limit))
	}

	// VIDEO: copy (direct play — the fast path) or transcode to the Profile (the exception).
	if spec.Plan.CopyVideo {
		// `-c:v copy` passes the source video through untouched. No scale filter, no encoder —
		// those are transcode-only. This is the whole point: an h264 file plays with zero video
		// re-encode.
		args = append(args, "-c:v", "copy")
	} else {
		// The tone-map carries its own output labelling — its final zscale rewrites the frames'
		// colour properties and ffmpeg propagates them, so no `-colorspace`/`-color_trc` flags
		// are added here. See hdrToSDRChain: they were measured to be redundant.
		args = append(args, p.scaleFilterArgs(spec.tonemapStep())...)
		args = append(args, p.videoEncodeArgs()...)
	}

	// AUDIO: copy when the target plays it, else transcode ONLY the audio (cheap) to AAC. The
	// loudness filter (filler) is a transcode-time concern, so a copy skips it — a copied advert
	// keeps its own levels, which is acceptable and far better than a needless re-encode.
	if spec.Plan.CopyAudio {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, p.audioEncodeArgsNormalised(targetLUFS)...)
	}

	// `+initial_discontinuity` tells the downstream demuxer the first timestamps are not
	// necessarily zero — true for anything joining a live stream mid-flight, and true here
	// because we seeked.
	return append(args,
		"-f", "mpegts", "-mpegts_flags", "+initial_discontinuity", "pipe:1",
	)
}

// scaleFilterArgs normalizes any input geometry to the profile's.
//
// A REAL filter-graph failure, and exactly the class ErsatzTV warns about (prior-art §5b):
// `-vf scale=1280:720` straight into `h264_vulkan` fails with "Impossible to convert between
// the formats supported by the filter 'Parsed_scale_0' and the filter 'auto_scale_0'" and a
// 40-line pixel-format dump. The cause is not the scale — it is that `scale` emits CPU
// frames while a hardware encoder wants GPU frames, and nothing uploads between them.
//
// So hardware gets `scale=W:H,format=nv12,hwupload`: scale on the CPU, convert to a format
// the uploader accepts, THEN upload. Software needs no upload step and gets the bare scale
// plus an explicit pixel format.
//
// `force_original_aspect_ratio=decrease` + `pad` letterboxes rather than stretching, so a
// 4:3 episode in a 16:9 profile keeps its geometry. The pad is what preserves the profile's
// exact output dimensions, which `-c copy` requires — a bare aspect-preserving scale would
// emit 960x720 for 4:3 content and break concatenation.
// `tonemap` is the HDR→SDR chain (ProgramSpec.tonemapStep), or "" for the overwhelmingly common
// SDR case. It is a PARAMETER rather than something derived here because a Profile describes the
// OUTPUT and tone-mapping is a fact about the INPUT — and because capability.go builds this same
// chain for its trial encode against a synthetic lavfi source that has no input to speak of.
func (p Profile) scaleFilterArgs(tonemap string) []string {
	if p.Width <= 0 || p.Height <= 0 {
		return nil
	}
	scale := fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2",
		p.Width, p.Height, p.Width, p.Height)

	// Framerate is pinned too: a 24fps film and a 25fps episode must not produce different
	// output rates, or `-c copy` on the parent is invalid.
	fps := fmt.Sprintf("fps=%d", p.Framerate)

	parts := []string{scale, fps}

	// TONE-MAP AFTER THE SCALE, BEFORE THE FORMAT/UPLOAD STEP. Both placements are deliberate:
	//
	//   - After scale, because tone-mapping is per-pixel and the scale has already cut a 4K frame
	//     to 1080p by this point. Doing it first would tone-map four times the pixels for a
	//     result no viewer can distinguish, on a realtime budget.
	//   - Before the format/upload step, because the chain's last zscale preserves BIT DEPTH — a
	//     10-bit HDR source is still 10-bit when it leaves the tone-map, and `format=yuv420p`
	//     (or `format=nv12,hwupload`) is what takes it to 8. Reversed, the tone-map would work on
	//     an already-truncated picture, which is most of what this was meant to fix.
	//
	// Both orders RUN — neither errors — so nothing but the assertions in program_test.go protects
	// this. Measured against a real HDR10 source (2026-08-09, ffmpeg n9.0): swapping the tone-map
	// and the format step yields a different frame hash, so the order is doing work rather than
	// being a stylistic preference.
	if tonemap != "" {
		parts = append(parts, tonemap)
	}

	// The upload step comes from the capability prober's own helper, not a local copy. It is
	// per-encoder correct in ways a generic "format=nv12,hwupload" is not — QSV needs
	// `extra_hw_frames=64` or its lookahead intermittently fails to allocate frames, and the
	// families that accept CPU frames directly (nvenc, amf, videotoolbox, rkmpp, v4l2m2m)
	// must get NO upload at all. A hand-rolled version here got QSV wrong and drifted from
	// the prober within one commit of being written.
	if up := hardwareUploadFilter(p.Encoder); up != "" {
		// These families upload to GPU memory, and their upload filter already pins the
		// pixel format (nv12) on the way.
		parts = append(parts, up)
	} else {
		// EVERY OTHER FAMILY gets an explicit 8-bit pixel format — software AND the hardware
		// encoders that take CPU frames directly (nvenc, amf, videotoolbox, rkmpp, v4l2m2m).
		//
		// This `else` used to be `else if p.Encoder == EncoderSoftware`, which left exactly
		// those hardware families with NO pixel-format normalization. A 10-bit source then
		// reached the encoder as yuv420p10le, and h264_nvenc — which encodes 8-bit H.264 only
		// — rejected it:
		//
		//	[h264_nvenc] No capable devices found
		//	[out#0/mpegts] Nothing was written into output file
		//
		// That message names the DEVICE, not the pixel format, so it reads as "your GPU is
		// missing" while the GPU is fine. Found on a live channel: a 4K 10-bit HEVC film
		// played on libx264 (which had this filter) and died the moment nvenc was selected.
		//
		// Every prior live test used software, so every prior live test had the fix.
		parts = append(parts, "format=yuv420p")
	}
	return []string{"-vf", strings.Join(parts, ",")}
}

// isHTTP reports whether a URL uses a protocol that accepts ffmpeg's `-reconnect*` options.
//
// THIS CONDITION IS LOAD-BEARING, and omitting it is a hard failure rather than a missed
// optimization. `-reconnect*` are private options of ffmpeg's HTTP protocol, not global ones:
// against a local file input, `-reconnect 1` produces "Option reconnect not found" and
// ffmpeg exits 8 before opening anything. Tunarr applies them conditionally on
// `protocol === 'http'` for exactly this reason (prior-art §5a).
//
// It matters in production, not just in tests: filler clips are local files (§10 FILLER_DIR),
// so an unconditional flag list means every commercial break fails to start — as a channel
// that dies at the first break, with a message that names an option rather than a file.
func isHTTP(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// seconds formats a duration for ffmpeg's -ss / -t, keeping millisecond precision.
//
// Not strconv on a float: %g would emit exponent notation for small values ("1e-06") which
// ffmpeg parses as a filename-ish token, and %f would pad to six decimals. Milliseconds are
// as fine as a seek is meaningfully accurate.
func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}
