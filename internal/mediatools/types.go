package mediatools

import (
	"context"
	"path/filepath"
	"strings"
)

// The shapes the media tools return. They lived in internal/filler/split.go, which is where the
// compilation splitter first needed them — but every one of them describes TOOL OUTPUT rather
// than a scheduling concept, and their own doc comments always said so: a Chapter is "one
// embedded chapter from ffprobe", a TranscriptSegment is "one whisper utterance". They belong
// with the code that produces them.

// Interval is a [StartMs, EndMs) span inside a media file.
type Interval struct {
	StartMs int64 `json:"startMs"`
	EndMs   int64 `json:"endMs"`
}

// MediaQualityProvenance is the closed producer identity for durable detector facts.
type MediaQualityProvenance string

const (
	// MediaQualityEvidenceV1 is the closed schema understood by durable conditioning recovery.
	MediaQualityEvidenceV1 = 1
	// MediaQualityProvenanceFFmpegDetectors identifies the owned black/silence/freeze decode.
	MediaQualityProvenanceFFmpegDetectors MediaQualityProvenance = "ffmpeg_detectors"
)

// MediaQuality is the measured content inside a playable container. A file can carry valid audio
// and video streams while every video frame is black, the audio samples are silent, or one damaged
// frame is repeated for most of the runtime; ffprobe's stream-presence gate cannot see any of
// those. Intervals are normalised, non-overlapping and clamped to DurationMs before this value is
// returned or persisted.
type MediaQuality struct {
	EvidenceVersion int                    `json:"evidenceVersion,omitempty"`
	Provenance      MediaQualityProvenance `json:"provenance,omitempty"`
	DurationMs      int64                  `json:"durationMs"`
	Black           []Interval             `json:"black,omitempty"`
	Silence         []Interval             `json:"silence,omitempty"`
	Freeze          []Interval             `json:"freeze,omitempty"`
}

// Chapter is one embedded chapter from ffprobe (triage, §10 V34).
type Chapter struct {
	StartMs int64
	EndMs   int64
	Title   string
}

// TranscriptSegment is one whisper utterance with its offset INSIDE the probed
// span (milliseconds relative to the span start, so rescue math is local).
type TranscriptSegment struct {
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Text    string `json:"text"`
}

// Probed is what one ffprobe pass learns about a clip.
//
// A STRUCT rather than a second probe call: ffprobe returns duration and stream height in one
// invocation, so splitting them would double the exec cost per file for no benefit — and would
// create a state where a clip has a duration but silently lost its quality, which is exactly
// the kind of half-populated row that is painful to notice later.
type Probed struct {
	DurationMs int64 `json:"durationMs"`
	// Width is the VIDEO stream's width in pixels; zero when unavailable.
	Width int `json:"width"`
	// Height is the VIDEO stream's height in pixels; 0 when the file has no video stream or
	// the probe could not tell. Quality is derived from it (see QualityFromHeight) rather
	// than stored raw, because "1080p" is what a person reads and 1088 is what some encoders
	// actually write.
	Height int `json:"height"`
	// Cadence, sample/display aspect and field order are exact ffprobe observations. Empty means
	// unavailable; transforms may not turn that absence into a claim about the source.
	Cadence          string `json:"cadence,omitempty"`
	SampleAspect     string `json:"sampleAspect,omitempty"`
	DisplayAspect    string `json:"displayAspect,omitempty"`
	FieldOrder       string `json:"fieldOrder,omitempty"`
	VideoStartMs     int64  `json:"videoStartMs,omitempty"`
	VideoDurationMs  int64  `json:"videoDurationMs,omitempty"`
	VideoTimingKnown bool   `json:"videoTimingKnown,omitempty"`
	AudioStartMs     int64  `json:"audioStartMs,omitempty"`
	AudioDurationMs  int64  `json:"audioDurationMs,omitempty"`
	AudioTimingKnown bool   `json:"audioTimingKnown,omitempty"`
	// Silent reports that the file carries NO audio stream at all (§10 V40).
	//
	// ⚠ Presence, not loudness — a clip CAN be legitimately quiet, and that is normalisation's
	// problem at playout, not grounds for a reject. This is the harder failure: a video-only file
	// plays as dead air in the middle of a break, which reads as the stream having dropped.
	//
	// ⚠ **Phrased NEGATIVELY on purpose, so the zero value is permissive.** The first cut was
	// `HasAudio bool`, which made `false` mean "reject" — and retroactively changed the meaning
	// of every `Probed{...}` literal written before this field existed. Nine test doubles that
	// were correct when written started rejecting every clip, and the suite panicked on an empty
	// catalog. A gate whose zero value denies is a gate that breaks its own callers.
	//
	// Costs nothing extra to fill: the probe already asks for `codec_type` per stream so it can
	// find the VIDEO height, and this reads the same answer.
	Silent bool `json:"silent,omitempty"`
	// NoVideo reports that ffprobe returned no video stream at all. It is explicit rather than
	// inferred from Height == 0: injected/older probers may know duration without dimensions, and
	// the zero value must remain permissive for the same compatibility reason as Silent above.
	NoVideo bool `json:"noVideo,omitempty"`
}

// Prober reads a media file's duration and dimensions. Satisfied by FFprobe; injected so the
// scanner is testable without executing a binary.
type Prober func(ctx context.Context, path string) (Probed, error)

// SidecarPathFor is where a media file's info-JSON sidecar lives — `<name>.info.json`, the
// convention yt-dlp writes and the transcode path must carry across a rename.
func SidecarPathFor(mediaPath string) string {
	return strings.TrimSuffix(mediaPath, filepath.Ext(mediaPath)) + ".info.json"
}

// PreviewWidth is the pixel width of every generated still and hover preview. One constant
// because the two must match: the still is the poster frame the animation replaces on hover, and
// a size change between them shows as a jump.
const PreviewWidth = 320
