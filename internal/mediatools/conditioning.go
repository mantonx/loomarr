package mediatools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const (
	ConditioningMaxStreams        = 8
	ConditioningMaxCuts           = 8
	ConditioningMaxDurationMs     = 120_000
	ConditioningMaxSnapshotBytes  = int64(1 << 30)
	conditioningProbeOutputLimit  = 1 << 20
	conditioningFFmpegOutputLimit = 256 << 10
	conditioningSnapshotCopyChunk = 64 << 10
	conditioningFrameReadInterval = "%+120.000001"
	// ffmpeg and ffprobe render terminal timestamps independently. A detector end
	// may exceed the decoded EOF by a decimal microsecond even when both describe
	// the same final audio sample; accept at most one millisecond, then clamp it.
	conditioningDetectorEndToleranceMs = 1
	conditioningDetectorTail           = "tpad=stop_mode=add:stop=1:color=black,tpad=stop_mode=add:stop=1:color=white,"
)

var (
	ErrConditioningResourceLimit = errors.New("conditioning measurement resource limit exceeded")
	ErrConditioningOutput        = errors.New("conditioning measurement output invalid")
)

// StreamKind is the closed set of presented stream kinds measured by conditioning.
type StreamKind string

const (
	StreamVideo StreamKind = "video"
	StreamAudio StreamKind = "audio"
)

// OptionalMilliseconds distinguishes an observed zero from unavailable evidence.
type OptionalMilliseconds struct {
	Milliseconds int64 `json:"milliseconds"`
	Available    bool  `json:"available"`
}

// Rational preserves an exact ffprobe rational such as 30000/1001.
type Rational struct {
	Numerator   int64 `json:"numerator"`
	Denominator int64 `json:"denominator"`
}

// ConditioningStream identifies and measures one presented audio or video stream.
type ConditioningStream struct {
	Kind     StreamKind           `json:"kind"`
	Index    int                  `json:"index"`
	Start    OptionalMilliseconds `json:"start"`
	Duration OptionalMilliseconds `json:"duration"`
	Cadence  *Rational            `json:"cadence,omitempty"`
}

// ConditioningSkew is audio minus video. It remains unavailable unless exactly one presented
// audio stream and one presented video stream make the pairing unambiguous.
type ConditioningSkew struct {
	Start OptionalMilliseconds `json:"start"`
	End   OptionalMilliseconds `json:"end"`
}

// ConditioningTruePeakState represents finite true peak, digital silence, or no measurement
// without placing a non-finite float in a future wire payload.
type ConditioningTruePeakState string

const (
	TruePeakUnavailable      ConditioningTruePeakState = "unavailable"
	TruePeakFinite           ConditioningTruePeakState = "finite"
	TruePeakNegativeInfinity ConditioningTruePeakState = "negative_infinity"
)

// ConditioningTruePeak contains a finite dBTP only when State is TruePeakFinite.
type ConditioningTruePeak struct {
	State ConditioningTruePeakState `json:"state"`
	DBTP  float64                   `json:"dbtp,omitempty"`
}

// ConditioningLoudness contains integrated loudness and an explicitly represented true peak.
type ConditioningLoudness struct {
	IntegratedLUFS float64              `json:"integratedLufs"`
	TruePeak       ConditioningTruePeak `json:"truePeak"`
	Available      bool                 `json:"available"`
}

// ConditioningRequest names one local artifact, an optional single parent, and up to eight
// intended intervals in that parent.
type ConditioningRequest struct {
	Path         string
	ParentPath   string
	IntendedCuts []Interval
}

// ConditioningCutMeasurement contains independently matched evidence in request interval order.
type ConditioningCutMeasurement struct {
	Intended Interval                `json:"intended"`
	Streams  []ConditioningCutStream `json:"streams"`
}

// ConditioningCutStream contains actual-minus-intended edge errors for one exact stream identity.
type ConditioningCutStream struct {
	Kind       StreamKind           `json:"kind"`
	Index      int                  `json:"index"`
	StartError OptionalMilliseconds `json:"startError"`
	EndError   OptionalMilliseconds `json:"endError"`
}

// ConditioningMeasurement is evidence only. It carries no verdict or policy threshold.
type ConditioningMeasurement struct {
	ContainerDurationMs int64                        `json:"containerDurationMs"`
	Streams             []ConditioningStream         `json:"streams"`
	AVSkew              ConditioningSkew             `json:"avSkew"`
	Loudness            ConditioningLoudness         `json:"loudness"`
	Quality             MediaQuality                 `json:"quality"`
	Cuts                []ConditioningCutMeasurement `json:"cuts,omitempty"`
}

type conditioningProbeJSON struct {
	Streams []conditioningProbeStreamJSON `json:"streams"`
	Format  struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

type conditioningProbeStreamJSON struct {
	Index        *int   `json:"index"`
	CodecType    string `json:"codec_type"`
	StartTime    string `json:"start_time"`
	Duration     string `json:"duration"`
	AvgFrameRate string `json:"avg_frame_rate"`
	SampleRate   string `json:"sample_rate"`
}

type conditioningProbeFrameJSON struct {
	StreamIndex         *int   `json:"stream_index"`
	PTS                 string `json:"pts_time"`
	BestEffortTimestamp string `json:"best_effort_timestamp_time"`
	Duration            string `json:"duration_time"`
	NumberOfSamples     *int64 `json:"nb_samples"`
}

type conditioningStreamProjection struct {
	stream conditioningProbeStreamJSON
	kind   StreamKind
	index  int
}

// MeasureConditioning performs a bounded, read-only inspection using the binaries already owned by
// FFmpegTools. It does not rewrite media or make an admission decision.
func (t *FFmpegTools) MeasureConditioning(ctx context.Context, req ConditioningRequest) (ConditioningMeasurement, error) {
	if t == nil {
		return ConditioningMeasurement{}, fmt.Errorf("%w: media tools are required", ErrConditioningOutput)
	}
	if len(req.IntendedCuts) > ConditioningMaxCuts {
		return ConditioningMeasurement{}, fmt.Errorf("%w: %d cuts exceeds %d", ErrConditioningResourceLimit, len(req.IntendedCuts), ConditioningMaxCuts)
	}
	if (strings.TrimSpace(req.ParentPath) == "") != (len(req.IntendedCuts) == 0) {
		return ConditioningMeasurement{}, fmt.Errorf("%w: parent and intended cuts must be supplied together", ErrConditioningOutput)
	}
	for _, intended := range req.IntendedCuts {
		if intended.StartMs < 0 || intended.EndMs <= intended.StartMs {
			return ConditioningMeasurement{}, fmt.Errorf("%w: intended cut must be a positive interval", ErrConditioningOutput)
		}
	}

	inputs, err := snapshotConditioningInputs(ctx, req.Path, req.ParentPath)
	if err != nil {
		return ConditioningMeasurement{}, err
	}
	defer inputs.close()
	artifactPath, parentPath := inputs.artifact, inputs.parent

	raw, err := runConditioningCommand(ctx, t.FFprobePath, conditioningProbeOutputLimit, false,
		"-v", "error", "-show_entries", "format=duration:stream=index,codec_type,start_time,duration,avg_frame_rate,sample_rate",
		"-of", "json", artifactPath)
	if err != nil {
		return ConditioningMeasurement{}, fmt.Errorf("condition media probe: %w", err)
	}
	var probed conditioningProbeJSON
	if err := json.Unmarshal(raw, &probed); err != nil {
		return ConditioningMeasurement{}, fmt.Errorf("%w: parse ffprobe JSON: %v", ErrConditioningOutput, err)
	}
	streams, err := validateConditioningStreamProjection(probed.Streams)
	if err != nil {
		return ConditioningMeasurement{}, err
	}
	duration, err := parseConditioningContainerDuration(probed.Format.Duration)
	if err != nil {
		return ConditioningMeasurement{}, err
	}

	measurement := ConditioningMeasurement{ContainerDurationMs: duration.Milliseconds}
	for _, projected := range streams {
		stream, kind, index := projected.stream, projected.kind, projected.index
		measured := ConditioningStream{Kind: kind, Index: index}
		measured.Start, err = parseConditioningMilliseconds(stream.StartTime)
		if err != nil {
			return ConditioningMeasurement{}, fmt.Errorf("%w: stream %d start", err, index)
		}
		measured.Duration, err = parseConditioningDuration(stream.Duration)
		if err != nil {
			return ConditioningMeasurement{}, fmt.Errorf("%w: stream %d duration", err, index)
		}
		if kind == StreamVideo {
			measured.Cadence, err = parseConditioningRational(stream.AvgFrameRate)
			if err != nil {
				return ConditioningMeasurement{}, fmt.Errorf("%w: stream %d cadence", err, index)
			}
		}
		measurement.Streams = append(measurement.Streams, measured)
	}
	measurement.AVSkew, err = conditioningSkew(measurement.Streams)
	if err != nil {
		return ConditioningMeasurement{}, err
	}

	detectorStreams, err := selectConditioningDetectorStreams(measurement.Streams)
	if err != nil {
		return ConditioningMeasurement{}, err
	}
	frames, err := conditioningSelectedFrames(ctx, t.FFprobePath, artifactPath, detectorStreams)
	if err != nil {
		return ConditioningMeasurement{}, fmt.Errorf("condition decoded frame probe: %w", err)
	}
	if err := bindConditioningDetectorEOFs(&detectorStreams, probed.Streams, frames); err != nil {
		return ConditioningMeasurement{}, err
	}
	detectorOutput, err := t.conditioningDetectorOutput(ctx, artifactPath, detectorStreams)
	if err != nil {
		return ConditioningMeasurement{}, err
	}
	measurement.Quality, err = parseConditioningDetectorEvents(detectorOutput, measurement.ContainerDurationMs, detectorStreams)
	if err != nil {
		return ConditioningMeasurement{}, err
	}
	measurement.Loudness, err = parseConditioningLoudness(detectorOutput)
	if err != nil {
		return ConditioningMeasurement{}, err
	}
	if hasConditioningStream(measurement.Streams, StreamAudio) && !measurement.Loudness.Available {
		return ConditioningMeasurement{}, fmt.Errorf("%w: audio loudness summary is incomplete", ErrConditioningOutput)
	}
	for _, intended := range req.IntendedCuts {
		matched, err := t.measureConditioningCut(ctx, artifactPath, parentPath, measurement.ContainerDurationMs, measurement.Streams, intended)
		if err != nil {
			return ConditioningMeasurement{}, err
		}
		matched.Intended = intended
		measurement.Cuts = append(measurement.Cuts, matched)
	}
	return measurement, nil
}

// conditioningSelectedFrames bounds each selected stream independently. A
// single valid audio stream can contain thousands of decoded frames and exceed
// a shared probe-output cap even though its own bounded evidence is valid.
func conditioningSelectedFrames(ctx context.Context, ffprobePath, artifactPath string, selected conditioningDetectorStreams) ([]conditioningProbeFrameJSON, error) {
	streams := []*ConditioningStream{selected.video, selected.audio}
	frames := make([]conditioningProbeFrameJSON, 0)
	for _, stream := range streams {
		if stream == nil {
			continue
		}
		specifier, err := conditioningStreamSpecifier(stream.Kind)
		if err != nil {
			return nil, err
		}
		frameEntries := "frame=pts_time,best_effort_timestamp_time,duration_time"
		if stream.Kind == StreamAudio {
			frameEntries = "frame=pts_time,best_effort_timestamp_time,nb_samples"
		}
		raw, err := runConditioningCommand(ctx, ffprobePath, conditioningProbeOutputLimit, false,
			"-v", "error", "-read_intervals", conditioningFrameReadInterval, "-select_streams", specifier, "-show_frames",
			"-show_entries", frameEntries,
			"-of", "compact=p=0:nk=1", artifactPath)
		if err != nil {
			return nil, err
		}
		probe, err := parseConditioningCompactFrames(raw, stream.Kind, stream.Index)
		if err != nil {
			return nil, err
		}
		frames = append(frames, probe...)
	}
	return frames, nil
}

func conditioningStreamSpecifier(kind StreamKind) (string, error) {
	switch kind {
	case StreamVideo:
		return "v:0", nil
	case StreamAudio:
		return "a:0", nil
	default:
		return "", fmt.Errorf("%w: selected detector stream has unknown kind %q", ErrConditioningOutput, kind)
	}
}

func parseConditioningCompactFrames(raw []byte, kind StreamKind, index int) ([]conditioningProbeFrameJSON, error) {
	frames := make([]conditioningProbeFrameJSON, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 3 {
			return nil, fmt.Errorf("%w: decoded frame record has %d fields", ErrConditioningOutput, len(fields))
		}
		frame := conditioningProbeFrameJSON{StreamIndex: new(int), PTS: fields[0], BestEffortTimestamp: fields[1], Duration: fields[2]}
		*frame.StreamIndex = index
		if kind == StreamAudio {
			samples, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: selected audio frame has invalid sample count", ErrConditioningOutput)
			}
			frame.Duration = ""
			frame.NumberOfSamples = &samples
		}
		frames = append(frames, frame)
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("%w: selected %s stream %d has no decoded frames", ErrConditioningOutput, kind, index)
	}
	return frames, nil
}

type conditioningInputSnapshots struct {
	dir      string
	artifact string
	parent   string
}

type regularFileSnapshot struct {
	path   string
	sha256 string
	bytes  int64
}

func snapshotConditioningInputs(ctx context.Context, artifact, parent string) (conditioningInputSnapshots, error) {
	if err := ctx.Err(); err != nil {
		return conditioningInputSnapshots{}, err
	}
	dir, err := os.MkdirTemp("", "loomarr-conditioning-")
	if err != nil {
		return conditioningInputSnapshots{}, fmt.Errorf("%w: create private input snapshot: %v", ErrConditioningOutput, err)
	}
	inputs := conditioningInputSnapshots{dir: dir}
	fail := func(err error) (conditioningInputSnapshots, error) {
		inputs.close()
		return conditioningInputSnapshots{}, err
	}
	artifactSnapshot, err := snapshotConditioningRegularFile(ctx, dir, "artifact", artifact)
	if err != nil {
		return fail(err)
	}
	inputs.artifact = artifactSnapshot.path
	if parent != "" {
		parentSnapshot, err := snapshotConditioningRegularFile(ctx, dir, "parent", parent)
		if err != nil {
			return fail(fmt.Errorf("conditioning parent: %w", err))
		}
		inputs.parent = parentSnapshot.path
	}
	return inputs, nil
}

func (s conditioningInputSnapshots) close() {
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
}

func snapshotConditioningRegularFile(ctx context.Context, dir, name, path string) (regularFileSnapshot, error) {
	if strings.TrimSpace(path) == "" || path == "-" {
		return regularFileSnapshot{}, fmt.Errorf("%w: local regular media path is required", ErrConditioningOutput)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return regularFileSnapshot{}, fmt.Errorf("%w: resolve local media path: %v", ErrConditioningOutput, err)
	}
	file, err := openConditioningRegularFile(abs)
	if err != nil {
		return regularFileSnapshot{}, fmt.Errorf("%w: open local media path: %v", ErrConditioningOutput, err)
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		return regularFileSnapshot{}, fmt.Errorf("%w: media path is not a regular file", ErrConditioningOutput)
	}
	if opened.Size() > ConditioningMaxSnapshotBytes {
		return regularFileSnapshot{}, fmt.Errorf("%w: media snapshot is %d bytes; maximum is %d", ErrConditioningResourceLimit, opened.Size(), ConditioningMaxSnapshotBytes)
	}
	// The opened descriptor is the authority. Independently cap bytes read so a concurrent append or
	// stale size report cannot turn snapshot creation into an unbounded operation.
	// Retain the caller's basename after an internal role prefix. ffmpeg still gets a useful format
	// hint, while artifact and parent cannot collide when both were named identically.
	destination := filepath.Join(dir, name+"-"+filepath.Base(abs))
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return regularFileSnapshot{}, fmt.Errorf("%w: create private media snapshot: %v", ErrConditioningOutput, err)
	}
	digest := sha256.New()
	copyErr := copyConditioningSnapshot(ctx, io.MultiWriter(out, digest), file)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		return regularFileSnapshot{}, errors.Join(copyErr, closeErr)
	}
	snapshotInfo, err := os.Stat(destination)
	if err != nil {
		return regularFileSnapshot{}, fmt.Errorf("%w: stat private media snapshot: %v", ErrConditioningOutput, err)
	}
	return regularFileSnapshot{path: destination, sha256: hex.EncodeToString(digest.Sum(nil)), bytes: snapshotInfo.Size()}, nil
}

func copyConditioningSnapshot(ctx context.Context, destination io.Writer, source io.Reader) error {
	buffer := make([]byte, conditioningSnapshotCopyChunk)
	var copied int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := ConditioningMaxSnapshotBytes + 1 - copied
		if remaining <= 0 {
			return fmt.Errorf("%w: media snapshot exceeded %d bytes", ErrConditioningResourceLimit, ConditioningMaxSnapshotBytes)
		}
		chunk := buffer
		if int64(len(chunk)) > remaining {
			chunk = chunk[:remaining]
		}
		n, readErr := source.Read(chunk)
		if n > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			if copied > ConditioningMaxSnapshotBytes-int64(n) {
				return fmt.Errorf("%w: media snapshot exceeded %d bytes", ErrConditioningResourceLimit, ConditioningMaxSnapshotBytes)
			}
			written, writeErr := destination.Write(chunk[:n])
			copied += int64(written)
			if writeErr != nil {
				return fmt.Errorf("%w: write private media snapshot: %v", ErrConditioningOutput, writeErr)
			}
			if written != n {
				return fmt.Errorf("%w: write private media snapshot: %v", ErrConditioningOutput, io.ErrShortWrite)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("%w: read opened media object: %v", ErrConditioningOutput, readErr)
		}
		if n == 0 {
			return fmt.Errorf("%w: opened media object made no read progress", ErrConditioningOutput)
		}
	}
}

func validateConditioningStreamProjection(raw []conditioningProbeStreamJSON) ([]conditioningStreamProjection, error) {
	if len(raw) > ConditioningMaxStreams {
		return nil, fmt.Errorf("%w: %d streams exceeds %d", ErrConditioningResourceLimit, len(raw), ConditioningMaxStreams)
	}
	projected := make([]conditioningStreamProjection, 0, len(raw))
	seenIndexes := make(map[int]struct{}, len(raw))
	for _, stream := range raw {
		if stream.Index == nil || *stream.Index < 0 {
			return nil, fmt.Errorf("%w: missing or negative stream index", ErrConditioningOutput)
		}
		index := *stream.Index
		if _, duplicate := seenIndexes[index]; duplicate {
			return nil, fmt.Errorf("%w: duplicate stream index %d", ErrConditioningOutput, index)
		}
		seenIndexes[index] = struct{}{}
		if kind, known := conditioningStreamKind(stream.CodecType); known {
			projected = append(projected, conditioningStreamProjection{stream: stream, kind: kind, index: index})
		}
	}
	return projected, nil
}

func hasConditioningStream(streams []ConditioningStream, kind StreamKind) bool {
	for _, stream := range streams {
		if stream.Kind == kind {
			return true
		}
	}
	return false
}

func conditioningStreamKind(raw string) (StreamKind, bool) {
	switch raw {
	case string(StreamVideo):
		return StreamVideo, true
	case string(StreamAudio):
		return StreamAudio, true
	default:
		return "", false
	}
}

func parseConditioningMilliseconds(raw string) (OptionalMilliseconds, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "N/A") {
		return OptionalMilliseconds{}, nil
	}
	seconds, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return OptionalMilliseconds{}, fmt.Errorf("%w: invalid time scalar %q", ErrConditioningOutput, raw)
	}
	scaled := seconds * 1000
	// float64 cannot distinguish MaxInt64 from the next integer. Keep one representable unit of
	// headroom before converting so an out-of-range float can never wrap to MinInt64.
	if math.IsInf(scaled, 0) || scaled >= float64(math.MaxInt64) || scaled <= float64(math.MinInt64) {
		return OptionalMilliseconds{}, fmt.Errorf("%w: time scalar overflows milliseconds", ErrConditioningOutput)
	}
	return OptionalMilliseconds{Milliseconds: int64(math.Round(scaled)), Available: true}, nil
}

func parseConditioningDuration(raw string) (OptionalMilliseconds, error) {
	value, err := parseConditioningMilliseconds(raw)
	if err != nil || !value.Available {
		return value, err
	}
	if value.Milliseconds < 0 {
		return OptionalMilliseconds{}, fmt.Errorf("%w: negative duration", ErrConditioningOutput)
	}
	return value, nil
}

func parseConditioningContainerDuration(raw string) (OptionalMilliseconds, error) {
	seconds, ok := new(big.Rat).SetString(strings.TrimSpace(raw))
	if !ok || seconds.Sign() <= 0 {
		return OptionalMilliseconds{}, fmt.Errorf("%w: positive container duration is required", ErrConditioningOutput)
	}
	if seconds.Cmp(big.NewRat(ConditioningMaxDurationMs, 1_000)) > 0 {
		return OptionalMilliseconds{}, fmt.Errorf("%w: container duration exceeds %dms", ErrConditioningResourceLimit, ConditioningMaxDurationMs)
	}
	milliseconds, ok := roundPositiveConditioningRational(new(big.Rat).Mul(seconds, big.NewRat(1_000, 1)))
	if !ok || milliseconds <= 0 {
		return OptionalMilliseconds{}, fmt.Errorf("%w: positive container duration is required", ErrConditioningOutput)
	}
	return OptionalMilliseconds{Milliseconds: milliseconds, Available: true}, nil
}

func parseConditioningRational(raw string) (*Rational, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "N/A") || trimmed == "0/0" {
		return nil, nil
	}
	numerator, denominator, ok := strings.Cut(trimmed, "/")
	if !ok {
		return nil, fmt.Errorf("%w: invalid cadence %q", ErrConditioningOutput, raw)
	}
	n, nErr := strconv.ParseInt(numerator, 10, 64)
	d, dErr := strconv.ParseInt(denominator, 10, 64)
	if nErr != nil || dErr != nil || n <= 0 || d <= 0 {
		return nil, fmt.Errorf("%w: invalid cadence %q", ErrConditioningOutput, raw)
	}
	return &Rational{Numerator: n, Denominator: d}, nil
}

func conditioningSkew(streams []ConditioningStream) (ConditioningSkew, error) {
	var audio, video *ConditioningStream
	for i := range streams {
		switch streams[i].Kind {
		case StreamAudio:
			if audio != nil {
				return ConditioningSkew{}, nil
			}
			audio = &streams[i]
		case StreamVideo:
			if video != nil {
				return ConditioningSkew{}, nil
			}
			video = &streams[i]
		}
	}
	var skew ConditioningSkew
	if audio == nil || video == nil {
		return skew, nil
	}
	var err error
	if audio.Start.Available && video.Start.Available {
		skew.Start.Milliseconds, err = checkedConditioningSub(audio.Start.Milliseconds, video.Start.Milliseconds)
		if err != nil {
			return ConditioningSkew{}, err
		}
		skew.Start.Available = true
	}
	if audio.Start.Available && audio.Duration.Available && video.Start.Available && video.Duration.Available {
		audioEnd, audioErr := checkedConditioningAdd(audio.Start.Milliseconds, audio.Duration.Milliseconds)
		videoEnd, videoErr := checkedConditioningAdd(video.Start.Milliseconds, video.Duration.Milliseconds)
		if audioErr != nil || videoErr != nil {
			return ConditioningSkew{}, fmt.Errorf("%w: stream end overflows", ErrConditioningOutput)
		}
		skew.End.Milliseconds, err = checkedConditioningSub(audioEnd, videoEnd)
		if err != nil {
			return ConditioningSkew{}, err
		}
		skew.End.Available = true
	}
	return skew, nil
}

func checkedConditioningAdd(a, b int64) (int64, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, fmt.Errorf("%w: millisecond arithmetic overflow", ErrConditioningOutput)
	}
	return a + b, nil
}

func checkedConditioningSub(a, b int64) (int64, error) {
	if b == math.MinInt64 {
		if a >= 0 {
			return 0, fmt.Errorf("%w: millisecond arithmetic overflow", ErrConditioningOutput)
		}
		return a - b, nil
	}
	return checkedConditioningAdd(a, -b)
}

type conditioningDetectorStreams struct {
	video       *ConditioningStream
	audio       *ConditioningStream
	videoEOF    conditioningDetectorScalar
	audioEOF    conditioningDetectorScalar
	videoTailMs int64
}

func selectConditioningDetectorStreams(streams []ConditioningStream) (conditioningDetectorStreams, error) {
	var selected conditioningDetectorStreams
	for i := range streams {
		stream := &streams[i]
		switch stream.Kind {
		case StreamVideo:
			if selected.video == nil || stream.Index < selected.video.Index {
				selected.video = stream
			}
		case StreamAudio:
			if selected.audio == nil || stream.Index < selected.audio.Index {
				selected.audio = stream
			}
		}
	}
	for _, stream := range []*ConditioningStream{selected.video, selected.audio} {
		if stream != nil && (!stream.Duration.Available || stream.Duration.Milliseconds <= 0) {
			return conditioningDetectorStreams{}, fmt.Errorf("%w: selected %s stream %d has no positive EOF", ErrConditioningOutput, stream.Kind, stream.Index)
		}
	}
	return selected, nil
}

func bindConditioningDetectorEOFs(selected *conditioningDetectorStreams, probedStreams []conditioningProbeStreamJSON, frames []conditioningProbeFrameJSON) error {
	if selected.video != nil {
		eof, err := conditioningDecodedStreamEOF(StreamVideo, selected.video.Index, "", frames)
		if err != nil {
			return err
		}
		selected.videoEOF = eof
		tailMs, err := conditioningVideoDetectorTailAllowance(selected.video.Index, frames, *selected.video)
		if err != nil {
			return err
		}
		selected.videoTailMs = tailMs
	}
	if selected.audio != nil {
		sampleRate := ""
		for _, stream := range probedStreams {
			if stream.Index != nil && *stream.Index == selected.audio.Index {
				sampleRate = stream.SampleRate
				break
			}
		}
		eof, err := conditioningDecodedStreamEOF(StreamAudio, selected.audio.Index, sampleRate, frames)
		if err != nil {
			return err
		}
		selected.audioEOF = eof
	}
	return nil
}

func conditioningDecodedStreamEOF(kind StreamKind, index int, sampleRateRaw string, frames []conditioningProbeFrameJSON) (conditioningDetectorScalar, error) {
	var first, last *big.Rat
	var sampleRate int64
	if kind == StreamAudio {
		var err error
		sampleRate, err = strconv.ParseInt(sampleRateRaw, 10, 64)
		if err != nil || sampleRate <= 0 {
			return conditioningDetectorScalar{}, fmt.Errorf("%w: selected audio stream %d has invalid sample rate", ErrConditioningOutput, index)
		}
	}
	for _, frame := range frames {
		if frame.StreamIndex == nil || *frame.StreamIndex != index {
			continue
		}
		ptsRaw := frame.BestEffortTimestamp
		if ptsRaw == "" || strings.EqualFold(ptsRaw, "N/A") {
			ptsRaw = frame.PTS
		}
		pts, ok := new(big.Rat).SetString(ptsRaw)
		if !ok {
			return conditioningDetectorScalar{}, fmt.Errorf("%w: selected %s stream %d has invalid frame timestamp", ErrConditioningOutput, kind, index)
		}
		var span *big.Rat
		if kind == StreamAudio {
			if frame.NumberOfSamples == nil || *frame.NumberOfSamples <= 0 {
				return conditioningDetectorScalar{}, fmt.Errorf("%w: selected audio stream %d has invalid decoded sample count", ErrConditioningOutput, index)
			}
			span = big.NewRat(*frame.NumberOfSamples, sampleRate)
		} else {
			span, ok = new(big.Rat).SetString(frame.Duration)
			if !ok || span.Sign() <= 0 {
				return conditioningDetectorScalar{}, fmt.Errorf("%w: selected video stream %d has invalid frame duration", ErrConditioningOutput, index)
			}
		}
		end := new(big.Rat).Add(pts, span)
		if first == nil || pts.Cmp(first) < 0 {
			first = new(big.Rat).Set(pts)
		}
		if last == nil || end.Cmp(last) > 0 {
			last = end
		}
	}
	if first == nil || last == nil || last.Cmp(first) <= 0 {
		return conditioningDetectorScalar{}, fmt.Errorf("%w: selected %s stream %d has no decoded EOF", ErrConditioningOutput, kind, index)
	}
	spanSeconds := new(big.Rat).Sub(last, first)
	if spanSeconds.Cmp(big.NewRat(ConditioningMaxDurationMs, 1_000)) > 0 {
		return conditioningDetectorScalar{}, fmt.Errorf("%w: selected %s stream %d decoded EOF exceeds %dms", ErrConditioningResourceLimit, kind, index, ConditioningMaxDurationMs)
	}
	spanMs := new(big.Rat).Mul(spanSeconds, big.NewRat(1_000, 1))
	eof, ok := roundPositiveConditioningRational(spanMs)
	if !ok || eof <= 0 || eof > ConditioningMaxDurationMs {
		return conditioningDetectorScalar{}, fmt.Errorf("%w: selected %s stream %d decoded EOF is out of range", ErrConditioningOutput, kind, index)
	}
	return conditioningDetectorScalar{seconds: spanSeconds, ms: eof}, nil
}

func (t *FFmpegTools) conditioningDetectorOutput(ctx context.Context, path string, streams conditioningDetectorStreams) (string, error) {
	args := []string{"-nostdin", "-hide_banner", "-nostats", "-v", "info", "-i", path}
	if streams.video != nil {
		// One black frame followed by one white frame guarantees a change regardless of the final
		// artifact pixels. Evidence is clamped to the selected video's exact decoded EOF, so these
		// syntax-closing frames cannot become conditioning facts.
		args = append(args, "-map", fmt.Sprintf("0:%d", streams.video.Index), "-vf", "setpts=PTS-STARTPTS,"+conditioningDetectorTail+qualityVideoFilters)
	} else {
		args = append(args, "-vn")
	}
	if streams.audio != nil {
		args = append(args, "-map", fmt.Sprintf("0:%d", streams.audio.Index), "-af", "asetpts=PTS-STARTPTS,"+qualityAudioFilter+",ebur128=peak=true:framelog=quiet")
	} else {
		args = append(args, "-an")
	}
	args = append(args, "-f", "null", "-")
	raw, err := runConditioningCommand(ctx, FFmpegOr(t.FFmpegPath), conditioningFFmpegOutputLimit, true, args...)
	if err != nil {
		return "", fmt.Errorf("condition media decode: %w", err)
	}
	return string(raw), nil
}

var (
	integratedLoudnessPattern        = regexp.MustCompile(`(?m)^\s*I:\s*(-?[0-9]+(?:\.[0-9]+)?)\s+LUFS\s*$`)
	truePeakPattern                  = regexp.MustCompile(`(?mi)^\s*Peak:\s*(-?(?:[0-9]+(?:\.[0-9]+)?|inf))\s+dBFS\s*$`)
	conditioningParsedDetectorLine   = regexp.MustCompile(`^\s*\[Parsed_(blackdetect|silencedetect|freezedetect)_([0-9]+) @ (0x[0-9A-Fa-f]+)\]\s+(.+?)\s*$`)
	conditioningLegacyDetectorLine   = regexp.MustCompile(`^\s*\[(blackdetect|silencedetect|freezedetect) @ (0x[0-9A-Fa-f]+)\]\s+(.+?)\s*$`)
	conditioningDetectorToken        = regexp.MustCompile(`(?:black_(?:start|end|duration)|silence_(?:start|end|duration)|freeze_(?:start|end|duration)):`)
	conditioningBlackPayload         = regexp.MustCompile(`^black_start:(-?[0-9]+(?:\.[0-9]+)?) black_end:(-?[0-9]+(?:\.[0-9]+)?) black_duration:(-?[0-9]+(?:\.[0-9]+)?)$`)
	conditioningSilenceStart         = regexp.MustCompile(`^silence_start: (-?[0-9]+(?:\.[0-9]+)?)$`)
	conditioningSilenceEnd           = regexp.MustCompile(`^silence_end: (-?[0-9]+(?:\.[0-9]+)?) \| silence_duration: (-?[0-9]+(?:\.[0-9]+)?)$`)
	conditioningFreezeStartModern    = regexp.MustCompile(`^lavfi\.freezedetect\.freeze_start: (-?[0-9]+(?:\.[0-9]+)?)$`)
	conditioningFreezeDurationModern = regexp.MustCompile(`^lavfi\.freezedetect\.freeze_duration: (-?[0-9]+(?:\.[0-9]+)?)$`)
	conditioningFreezeEndModern      = regexp.MustCompile(`^lavfi\.freezedetect\.freeze_end: (-?[0-9]+(?:\.[0-9]+)?)$`)
	conditioningFreezeStartLegacy    = regexp.MustCompile(`^freeze_start: (-?[0-9]+(?:\.[0-9]+)?)$`)
	conditioningFreezeDurationLegacy = regexp.MustCompile(`^freeze_duration: (-?[0-9]+(?:\.[0-9]+)?)$`)
	conditioningFreezeEndLegacy      = regexp.MustCompile(`^freeze_end: (-?[0-9]+(?:\.[0-9]+)?)$`)
)

type conditioningDetectorScalar struct {
	seconds *big.Rat
	ms      int64
}

type conditioningDetectorTimeline struct {
	eof    conditioningDetectorScalar
	tailMs int64
}

func (t conditioningDetectorTimeline) maximumSeconds() *big.Rat {
	return new(big.Rat).Add(t.eof.seconds, big.NewRat(t.tailMs, 1_000))
}

type conditioningOpenDetectorEvent struct {
	start    conditioningDetectorScalar
	duration *conditioningDetectorScalar
}

type conditioningDetectorIntervalKey struct {
	kind       string
	start, end int64
}

type conditioningDetectorIdentity struct {
	kind     string
	instance string
	address  string
	modern   bool
}

func parseConditioningDetectorEvents(raw string, containerDurationMs int64, streams conditioningDetectorStreams) (MediaQuality, error) {
	quality := MediaQuality{
		EvidenceVersion: MediaQualityEvidenceV1,
		Provenance:      MediaQualityProvenanceFFmpegDetectors,
		DurationMs:      containerDurationMs,
	}
	zero := conditioningDetectorScalar{seconds: new(big.Rat)}
	videoTimeline := conditioningDetectorTimeline{eof: zero}
	audioTimeline := conditioningDetectorTimeline{eof: zero}
	if streams.video != nil {
		videoTimeline.eof = streams.videoEOF
		videoTimeline.tailMs = streams.videoTailMs
	}
	if streams.audio != nil {
		audioTimeline.eof = streams.audioEOF
	}
	var silence, freeze *conditioningOpenDetectorEvent
	seen := make(map[conditioningDetectorIntervalKey]struct{})
	identities := make(map[string]conditioningDetectorIdentity)
	for _, line := range strings.Split(raw, "\n") {
		identity, payload, matched := conditioningDetectorLine(line)
		if !matched {
			if conditioningDetectorToken.MatchString(line) {
				return MediaQuality{}, fmt.Errorf("%w: detector line does not match an observed ffmpeg prefix", ErrConditioningOutput)
			}
			continue
		}
		if existing, found := identities[identity.kind]; found && existing != identity {
			return MediaQuality{}, fmt.Errorf("%w: detector identity or grammar changed within one measurement", ErrConditioningOutput)
		}
		identities[identity.kind] = identity
		switch identity.kind {
		case "blackdetect":
			match := conditioningBlackPayload.FindStringSubmatch(payload)
			if match == nil {
				return MediaQuality{}, fmt.Errorf("%w: blackdetect payload does not match its grammar", ErrConditioningOutput)
			}
			interval, err := completeConditioningDetectorInterval("black", match[1], match[2], match[3], videoTimeline, seen)
			if err != nil {
				return MediaQuality{}, err
			}
			if interval != nil {
				quality.Black = append(quality.Black, *interval)
			}
		case "silencedetect":
			if match := conditioningSilenceStart.FindStringSubmatch(payload); match != nil {
				if silence != nil {
					return MediaQuality{}, fmt.Errorf("%w: repeated silencedetect start", ErrConditioningOutput)
				}
				start, err := parseConditioningDetectorTime(match[1], audioTimeline.maximumSeconds())
				if err != nil {
					return MediaQuality{}, err
				}
				silence = &conditioningOpenDetectorEvent{start: start}
				continue
			}
			match := conditioningSilenceEnd.FindStringSubmatch(payload)
			if match == nil || silence == nil {
				return MediaQuality{}, fmt.Errorf("%w: malformed or repeated silencedetect start", ErrConditioningOutput)
			}
			interval, err := completeConditioningDetectorIntervalFromStart("silence", silence.start, match[1], match[2], audioTimeline, seen)
			if err != nil {
				return MediaQuality{}, err
			}
			if interval != nil {
				quality.Silence = append(quality.Silence, *interval)
			}
			silence = nil
		case "freezedetect":
			startPattern, durationPattern, endPattern := conditioningFreezeStartLegacy, conditioningFreezeDurationLegacy, conditioningFreezeEndLegacy
			if identity.modern {
				startPattern, durationPattern, endPattern = conditioningFreezeStartModern, conditioningFreezeDurationModern, conditioningFreezeEndModern
			}
			if match := startPattern.FindStringSubmatch(payload); match != nil {
				if freeze != nil {
					return MediaQuality{}, fmt.Errorf("%w: repeated freezedetect start", ErrConditioningOutput)
				}
				start, err := parseConditioningDetectorTime(match[1], videoTimeline.maximumSeconds())
				if err != nil {
					return MediaQuality{}, err
				}
				freeze = &conditioningOpenDetectorEvent{start: start}
				continue
			}
			if match := durationPattern.FindStringSubmatch(payload); match != nil {
				if freeze == nil || freeze.duration != nil {
					return MediaQuality{}, fmt.Errorf("%w: unmatched or repeated freezedetect duration", ErrConditioningOutput)
				}
				duration, err := parseConditioningDetectorDuration(match[1], videoTimeline.maximumSeconds())
				if err != nil {
					return MediaQuality{}, err
				}
				freeze.duration = &duration
				continue
			}
			match := endPattern.FindStringSubmatch(payload)
			if match == nil || freeze == nil || freeze.duration == nil {
				return MediaQuality{}, fmt.Errorf("%w: malformed or incomplete freezedetect end", ErrConditioningOutput)
			}
			interval, err := completeConditioningDetectorIntervalFromScalars("freeze", freeze.start, match[1], *freeze.duration, videoTimeline, seen)
			if err != nil {
				return MediaQuality{}, err
			}
			if interval != nil {
				quality.Freeze = append(quality.Freeze, *interval)
			}
			freeze = nil
		default:
			return MediaQuality{}, fmt.Errorf("%w: unknown conditioning detector", ErrConditioningOutput)
		}
	}
	if silence != nil || freeze != nil {
		return MediaQuality{}, fmt.Errorf("%w: detector event has no matching end", ErrConditioningOutput)
	}
	quality.Black = normaliseIntervals(quality.Black, videoTimeline.eof.ms)
	quality.Silence = normaliseIntervals(quality.Silence, audioTimeline.eof.ms)
	quality.Freeze = normaliseIntervals(quality.Freeze, videoTimeline.eof.ms)
	return quality, nil
}

func conditioningDetectorLine(line string) (identity conditioningDetectorIdentity, payload string, matched bool) {
	if values := conditioningParsedDetectorLine.FindStringSubmatch(line); values != nil {
		return conditioningDetectorIdentity{kind: values[1], instance: values[2], address: values[3], modern: true}, values[4], true
	}
	if values := conditioningLegacyDetectorLine.FindStringSubmatch(line); values != nil {
		return conditioningDetectorIdentity{kind: values[1], address: values[2]}, values[3], true
	}
	return conditioningDetectorIdentity{}, "", false
}

func completeConditioningDetectorInterval(kind, startRaw, endRaw, durationRaw string, timeline conditioningDetectorTimeline, seen map[conditioningDetectorIntervalKey]struct{}) (*Interval, error) {
	start, err := parseConditioningDetectorTime(startRaw, timeline.maximumSeconds())
	if err != nil {
		return nil, err
	}
	duration, err := parseConditioningDetectorDuration(durationRaw, timeline.maximumSeconds())
	if err != nil {
		return nil, err
	}
	return completeConditioningDetectorIntervalFromScalars(kind, start, endRaw, duration, timeline, seen)
}

func completeConditioningDetectorIntervalFromStart(kind string, start conditioningDetectorScalar, endRaw, durationRaw string, timeline conditioningDetectorTimeline, seen map[conditioningDetectorIntervalKey]struct{}) (*Interval, error) {
	duration, err := parseConditioningDetectorDuration(durationRaw, timeline.maximumSeconds())
	if err != nil {
		return nil, err
	}
	return completeConditioningDetectorIntervalFromScalars(kind, start, endRaw, duration, timeline, seen)
}

func completeConditioningDetectorIntervalFromScalars(kind string, start conditioningDetectorScalar, endRaw string, detectorDuration conditioningDetectorScalar, timeline conditioningDetectorTimeline, seen map[conditioningDetectorIntervalKey]struct{}) (*Interval, error) {
	end, err := parseConditioningDetectorEndTime(endRaw, timeline.maximumSeconds())
	if err != nil || end.seconds.Cmp(start.seconds) <= 0 || end.ms <= start.ms {
		return nil, fmt.Errorf("%w: detector interval is inverted or out of range", ErrConditioningOutput)
	}
	delta := new(big.Rat).Sub(end.seconds, start.seconds)
	disagreement := new(big.Rat).Sub(delta, detectorDuration.seconds)
	disagreement.Abs(disagreement)
	if disagreement.Cmp(big.NewRat(1, 1_000)) > 0 {
		return nil, fmt.Errorf("%w: detector duration does not equal end minus start", ErrConditioningOutput)
	}
	key := conditioningDetectorIntervalKey{kind: kind, start: start.ms, end: end.ms}
	if _, duplicate := seen[key]; duplicate {
		return nil, fmt.Errorf("%w: duplicate complete detector interval", ErrConditioningOutput)
	}
	seen[key] = struct{}{}
	clampedStart := min(start.ms, timeline.eof.ms)
	clampedEnd := min(end.ms, timeline.eof.ms)
	if clampedEnd <= clampedStart {
		return nil, nil
	}
	return &Interval{StartMs: clampedStart, EndMs: clampedEnd}, nil
}

func parseConditioningDetectorTime(raw string, maximumSeconds *big.Rat) (conditioningDetectorScalar, error) {
	value, err := parseConditioningDetectorScalar(raw)
	if err != nil || value.seconds.Cmp(maximumSeconds) > 0 {
		return conditioningDetectorScalar{}, fmt.Errorf("%w: detector time %q is outside the permitted timeline", ErrConditioningOutput, raw)
	}
	return value, nil
}

func parseConditioningDetectorEndTime(raw string, maximumSeconds *big.Rat) (conditioningDetectorScalar, error) {
	value, err := parseConditioningDetectorScalar(raw)
	if err != nil {
		return conditioningDetectorScalar{}, err
	}
	if value.seconds.Cmp(maximumSeconds) <= 0 {
		return value, nil
	}
	excess := new(big.Rat).Sub(value.seconds, maximumSeconds)
	if excess.Cmp(big.NewRat(conditioningDetectorEndToleranceMs, 1_000)) <= 0 {
		return value, nil
	}
	return conditioningDetectorScalar{}, fmt.Errorf("%w: detector end time %q is outside the permitted timeline", ErrConditioningOutput, raw)
}

func parseConditioningDetectorDuration(raw string, maximumSeconds *big.Rat) (conditioningDetectorScalar, error) {
	value, err := parseConditioningDetectorScalar(raw)
	if err != nil || value.seconds.Sign() <= 0 || value.seconds.Cmp(maximumSeconds) > 0 || value.ms <= 0 {
		return conditioningDetectorScalar{}, fmt.Errorf("%w: invalid detector duration %q", ErrConditioningOutput, raw)
	}
	return value, nil
}

func parseConditioningDetectorScalar(raw string) (conditioningDetectorScalar, error) {
	seconds, ok := new(big.Rat).SetString(raw)
	if !ok || seconds.Sign() < 0 {
		return conditioningDetectorScalar{}, fmt.Errorf("%w: invalid detector scalar %q", ErrConditioningOutput, raw)
	}
	scaled := new(big.Rat).Mul(seconds, big.NewRat(1_000, 1))
	ms, ok := roundPositiveConditioningRational(scaled)
	if !ok {
		return conditioningDetectorScalar{}, fmt.Errorf("%w: detector scalar overflows milliseconds", ErrConditioningOutput)
	}
	return conditioningDetectorScalar{seconds: seconds, ms: ms}, nil
}

func roundPositiveConditioningRational(value *big.Rat) (int64, bool) {
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(value.Num(), value.Denom(), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(value.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, false
	}
	return quotient.Int64(), true
}

func conditioningDetectorTailAllowance(stream ConditioningStream) int64 {
	if stream.Cadence == nil || stream.Cadence.Numerator <= 0 || stream.Cadence.Denominator <= 0 {
		return 0
	}
	numerator := big.NewInt(stream.Cadence.Numerator)
	scaledDenominator := new(big.Int).Mul(big.NewInt(stream.Cadence.Denominator), big.NewInt(1_000))
	frameMs := new(big.Int).Add(scaledDenominator, new(big.Int).Sub(numerator, big.NewInt(1)))
	frameMs.Quo(frameMs, numerator)
	if !frameMs.IsInt64() || frameMs.Int64() >= ConditioningMaxDurationMs {
		return ConditioningMaxDurationMs
	}
	return max(int64(1), frameMs.Int64()) + 1
}

func conditioningVideoDetectorTailAllowance(index int, frames []conditioningProbeFrameJSON, stream ConditioningStream) (int64, error) {
	fallback := conditioningDetectorTailAllowance(stream)
	var latest, previous *big.Rat
	for _, frame := range frames {
		if frame.StreamIndex == nil || *frame.StreamIndex != index {
			continue
		}
		ptsRaw := frame.BestEffortTimestamp
		if ptsRaw == "" || strings.EqualFold(ptsRaw, "N/A") {
			ptsRaw = frame.PTS
		}
		pts, ok := new(big.Rat).SetString(ptsRaw)
		if !ok || pts.Sign() < 0 {
			return 0, fmt.Errorf("%w: selected video stream %d has invalid frame timestamp", ErrConditioningOutput, index)
		}
		switch {
		case latest == nil || pts.Cmp(latest) > 0:
			previous = latest
			latest = pts
		case pts.Cmp(latest) < 0 && (previous == nil || pts.Cmp(previous) > 0):
			previous = pts
		}
	}
	if latest == nil || previous == nil {
		return fallback, nil
	}
	deltaMs := new(big.Rat).Mul(new(big.Rat).Sub(latest, previous), big.NewRat(1_000, 1))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(deltaMs.Num(), deltaMs.Denom(), remainder)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() || quotient.Int64() >= ConditioningMaxDurationMs {
		return ConditioningMaxDurationMs, nil
	}
	return max(fallback, quotient.Int64()+1), nil
}

func parseConditioningLoudness(raw string) (ConditioningLoudness, error) {
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(line, "I:") && strings.Contains(line, "LUFS") && integratedLoudnessPattern.FindStringSubmatch(line) == nil {
			return ConditioningLoudness{}, fmt.Errorf("%w: integrated loudness summary does not match its grammar", ErrConditioningOutput)
		}
		if strings.Contains(line, "Peak:") && strings.Contains(line, "dBFS") && truePeakPattern.FindStringSubmatch(line) == nil {
			return ConditioningLoudness{}, fmt.Errorf("%w: true-peak summary does not match its grammar", ErrConditioningOutput)
		}
	}
	integratedMatches := integratedLoudnessPattern.FindAllStringSubmatch(raw, -1)
	peakMatches := truePeakPattern.FindAllStringSubmatch(raw, -1)
	if len(integratedMatches) == 0 && len(peakMatches) == 0 {
		return ConditioningLoudness{TruePeak: ConditioningTruePeak{State: TruePeakUnavailable}}, nil
	}
	if len(integratedMatches) != 1 || len(peakMatches) != 1 {
		return ConditioningLoudness{}, fmt.Errorf("%w: loudness summary must contain exactly one integrated and one peak value", ErrConditioningOutput)
	}
	integrated, peak := integratedMatches[0], peakMatches[0]
	lufs, err := strconv.ParseFloat(integrated[1], 64)
	if err != nil || math.IsNaN(lufs) || math.IsInf(lufs, 0) {
		return ConditioningLoudness{}, fmt.Errorf("%w: invalid integrated loudness", ErrConditioningOutput)
	}
	measurement := ConditioningLoudness{IntegratedLUFS: lufs, Available: true}
	if strings.EqualFold(peak[1], "-inf") {
		measurement.TruePeak.State = TruePeakNegativeInfinity
		return measurement, nil
	}
	dbtp, err := strconv.ParseFloat(peak[1], 64)
	if err != nil || math.IsNaN(dbtp) || math.IsInf(dbtp, 0) {
		return ConditioningLoudness{}, fmt.Errorf("%w: invalid true peak", ErrConditioningOutput)
	}
	measurement.TruePeak = ConditioningTruePeak{State: TruePeakFinite, DBTP: dbtp}
	return measurement, nil
}

const conditioningEdgeWindowMs int64 = 3_000

type conditioningPacketProbe struct {
	Streams []conditioningProbeStreamJSON `json:"streams"`
	Packets []struct {
		StreamIndex *int   `json:"stream_index"`
		PTS         string `json:"pts_time"`
		Duration    string `json:"duration_time"`
		Flags       string `json:"flags"`
		DataHash    string `json:"data_hash"`
	} `json:"packets"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

type conditioningPacket struct {
	kind     StreamKind
	index    int
	pts      OptionalMilliseconds
	duration OptionalMilliseconds
	hash     string
}

func (t *FFmpegTools) measureConditioningCut(ctx context.Context, childPath, parentPath string, childDurationMs int64, childStreams []ConditioningStream, intended Interval) (ConditioningCutMeasurement, error) {
	measured := ConditioningCutMeasurement{}
	childStart, err := t.conditioningPackets(ctx, childPath, 0, min(conditioningEdgeWindowMs, childDurationMs))
	if err != nil {
		return measured, fmt.Errorf("measure child start edge: %w", err)
	}
	childEndStart := max(int64(0), childDurationMs-conditioningEdgeWindowMs)
	childEnd, err := t.conditioningPackets(ctx, childPath, childEndStart, childDurationMs)
	if err != nil {
		return measured, fmt.Errorf("measure child end edge: %w", err)
	}
	startUpper, err := checkedConditioningAdd(intended.StartMs, conditioningEdgeWindowMs)
	if err != nil {
		return measured, err
	}
	endUpper, err := checkedConditioningAdd(intended.EndMs, conditioningEdgeWindowMs)
	if err != nil {
		return measured, err
	}
	parentStart, err := t.conditioningPackets(ctx, parentPath, max(int64(0), intended.StartMs-conditioningEdgeWindowMs), startUpper)
	if err != nil {
		return measured, fmt.Errorf("measure parent start edge: %w", err)
	}
	parentEnd, err := t.conditioningPackets(ctx, parentPath, max(int64(0), intended.EndMs-conditioningEdgeWindowMs), endUpper)
	if err != nil {
		return measured, fmt.Errorf("measure parent end edge: %w", err)
	}

	for _, stream := range childStreams {
		out := ConditioningCutStream{Kind: stream.Kind, Index: stream.Index}
		if child := earliestPresentedPacket(childStart, stream.Kind, stream.Index); child != nil {
			if parent := uniquePacketWithHash(parentStart, stream.Kind, stream.Index, child.hash); parent != nil {
				value, subErr := checkedConditioningSub(parent.pts.Milliseconds, intended.StartMs)
				if subErr != nil {
					return measured, subErr
				}
				out.StartError = OptionalMilliseconds{Milliseconds: value, Available: true}
			}
		}
		child, latestErr := latestPresentedPacket(childEnd, stream.Kind, stream.Index)
		if latestErr != nil {
			return measured, latestErr
		}
		if child != nil {
			if parent := uniquePacketWithHash(parentEnd, stream.Kind, stream.Index, child.hash); parent != nil && parent.duration.Available {
				parentEndMs, addErr := checkedConditioningAdd(parent.pts.Milliseconds, parent.duration.Milliseconds)
				if addErr != nil {
					return measured, addErr
				}
				value, subErr := checkedConditioningSub(parentEndMs, intended.EndMs)
				if subErr != nil {
					return measured, subErr
				}
				out.EndError = OptionalMilliseconds{Milliseconds: value, Available: true}
			}
		}
		measured.Streams = append(measured.Streams, out)
	}
	return measured, nil
}

func (t *FFmpegTools) conditioningPackets(ctx context.Context, path string, startMs, endMs int64) ([]conditioningPacket, error) {
	if endMs <= startMs {
		return nil, fmt.Errorf("%w: packet interval must be positive", ErrConditioningOutput)
	}
	interval := msToSeconds(startMs) + "%" + msToSeconds(endMs)
	raw, err := runConditioningCommand(ctx, t.FFprobePath, conditioningProbeOutputLimit, false,
		"-v", "error", "-read_intervals", interval, "-show_packets", "-show_streams", "-show_format", "-show_data_hash", "sha256",
		"-show_entries", "stream=index,codec_type:packet=stream_index,pts_time,duration_time,flags,data_hash:format=duration", "-of", "json", path)
	if err != nil {
		return nil, err
	}
	var probed conditioningPacketProbe
	if err := json.Unmarshal(raw, &probed); err != nil {
		return nil, fmt.Errorf("%w: parse packet JSON: %v", ErrConditioningOutput, err)
	}
	streams, err := validateConditioningStreamProjection(probed.Streams)
	if err != nil {
		return nil, err
	}
	_, err = parseConditioningContainerDuration(probed.Format.Duration)
	if err != nil {
		return nil, fmt.Errorf("parent/child packet probe: %w", err)
	}
	kinds := make(map[int]StreamKind, len(streams))
	for _, stream := range streams {
		kinds[stream.index] = stream.kind
	}
	packets := make([]conditioningPacket, 0, len(probed.Packets))
	for _, packet := range probed.Packets {
		if packet.StreamIndex == nil || *packet.StreamIndex < 0 {
			return nil, fmt.Errorf("%w: packet has missing or negative stream index", ErrConditioningOutput)
		}
		index := *packet.StreamIndex
		kind, known := kinds[index]
		if !known || strings.Contains(packet.Flags, "D") {
			continue
		}
		pts, err := parseConditioningMilliseconds(packet.PTS)
		if err != nil {
			return nil, fmt.Errorf("%w: packet stream %d PTS", err, index)
		}
		packetDuration, err := parseConditioningDuration(packet.Duration)
		if err != nil {
			return nil, fmt.Errorf("%w: packet stream %d duration", err, index)
		}
		if packetDuration.Available && packetDuration.Milliseconds == 0 {
			packetDuration = OptionalMilliseconds{}
		}
		if !pts.Available || packet.DataHash == "" {
			continue
		}
		packets = append(packets, conditioningPacket{kind: kind, index: index, pts: pts, duration: packetDuration, hash: packet.DataHash})
	}
	return packets, nil
}

func earliestPresentedPacket(packets []conditioningPacket, kind StreamKind, index int) *conditioningPacket {
	var earliest *conditioningPacket
	for i := range packets {
		if packets[i].kind == kind && packets[i].index == index && (earliest == nil || packets[i].pts.Milliseconds < earliest.pts.Milliseconds) {
			earliest = &packets[i]
		}
	}
	return earliest
}

func latestPresentedPacket(packets []conditioningPacket, kind StreamKind, index int) (*conditioningPacket, error) {
	var latest *conditioningPacket
	var latestEnd int64
	for i := range packets {
		if packets[i].kind != kind || packets[i].index != index {
			continue
		}
		if !packets[i].duration.Available {
			return nil, nil
		}
		end, err := checkedConditioningAdd(packets[i].pts.Milliseconds, packets[i].duration.Milliseconds)
		if err != nil {
			return nil, err
		}
		if latest == nil || end > latestEnd {
			latest = &packets[i]
			latestEnd = end
		}
	}
	return latest, nil
}

func uniquePacketWithHash(packets []conditioningPacket, kind StreamKind, index int, hash string) *conditioningPacket {
	var matched *conditioningPacket
	for i := range packets {
		if packets[i].kind != kind || packets[i].index != index || packets[i].hash != hash {
			continue
		}
		if matched != nil {
			return nil
		}
		matched = &packets[i]
	}
	return matched
}

type conditioningOutput struct {
	mu       sync.Mutex
	limit    int
	buf      bytes.Buffer
	overflow bool
	cancel   context.CancelFunc
}

func (w *conditioningOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.limit - w.buf.Len()
	if remaining > 0 {
		_, _ = w.buf.Write(p[:min(remaining, len(p))])
	}
	if len(p) > remaining && !w.overflow {
		w.overflow = true
		w.cancel()
	}
	return len(p), nil
}

func (w *conditioningOutput) exceeded() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflow
}

func (w *conditioningOutput) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.buf.Bytes())
}

// runConditioningCommand is the single subprocess boundary for conditioning. Either stream hitting
// its execution cap cancels the process immediately; caller cancellation remains discoverable with
// errors.Is.
func runConditioningCommand(ctx context.Context, executable string, limit int, combined bool, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &conditioningOutput{limit: limit, cancel: cancel}
	stderr := &conditioningOutput{limit: limit, cancel: cancel}
	if combined {
		stderr = stdout
	}
	cmd := exec.CommandContext(runCtx, executable, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("conditioning tool canceled: %w", ctxErr)
	}
	if stdout.exceeded() || stderr.exceeded() {
		return nil, fmt.Errorf("%w: tool output exceeds %d bytes", ErrConditioningResourceLimit, limit)
	}
	if runErr != nil {
		return nil, fmt.Errorf("conditioning tool: %w: %s", runErr, stderr.bytes())
	}
	return stdout.bytes(), nil
}
