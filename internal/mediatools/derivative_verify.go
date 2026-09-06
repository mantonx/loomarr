package mediatools

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	DerivativeQCVersion        = 1
	derivativeQCOutputLimit    = 4 << 20
	derivativeKeyframeSlackMs  = int64(250)
	derivativeTerminalSlackMs  = int64(500)
	derivativeLoudnessSlackLU  = 2.0
	derivativeTopLevelBoxLimit = 4096
)

// DerivativeQC is measured output evidence, separate from the recipe that requested the shape.
// A command line containing +faststart or force_key_frames is intent; this is what the bytes did.
type DerivativeQC struct {
	Version               int                  `json:"version"`
	FastStart             bool                 `json:"fastStart"`
	CompleteDecode        bool                 `json:"completeDecode"`
	Seekable              bool                 `json:"seekable"`
	FirstVideoKeyframeMs  int64                `json:"firstVideoKeyframeMs"`
	MaxVideoKeyframeGapMs int64                `json:"maxVideoKeyframeGapMs"`
	TerminalKeyframeGapMs int64                `json:"terminalKeyframeGapMs"`
	Loudness              ConditioningLoudness `json:"loudness"`
}

// ValidateDerivativeQC checks the persisted verification against the recipe and measured output.
func ValidateDerivativeQC(qc DerivativeQC, durationMs int64, keyframeSeconds int, hadAudio bool, targetLUFS float64) error {
	if qc.Version != DerivativeQCVersion || durationMs <= 0 || keyframeSeconds <= 0 ||
		!qc.FastStart || !qc.CompleteDecode || !qc.Seekable {
		return errors.New("derivative compatibility verification is incomplete")
	}
	maximumGap := int64(keyframeSeconds)*1000 + derivativeKeyframeSlackMs
	if qc.FirstVideoKeyframeMs < 0 || qc.FirstVideoKeyframeMs > derivativeKeyframeSlackMs ||
		qc.MaxVideoKeyframeGapMs < 0 || qc.MaxVideoKeyframeGapMs > maximumGap ||
		qc.TerminalKeyframeGapMs < 0 || qc.TerminalKeyframeGapMs > maximumGap+derivativeTerminalSlackMs {
		return errors.New("derivative keyframe verification is outside the recipe bound")
	}
	if !hadAudio {
		if qc.Loudness.Available || qc.Loudness.TruePeak.State != TruePeakUnavailable {
			return errors.New("silent derivative carries invented loudness evidence")
		}
		return nil
	}
	if !validDerivativeLoudness(qc.Loudness) {
		return errors.New("derivative loudness verification is unavailable or invalid")
	}
	if targetLUFS != 0 && math.Abs(qc.Loudness.IntegratedLUFS-targetLUFS) > derivativeLoudnessSlackLU {
		return fmt.Errorf("derivative loudness %.1f LUFS missed target %.1f LUFS", qc.Loudness.IntegratedLUFS, targetLUFS)
	}
	if targetLUFS != 0 && qc.Loudness.TruePeak.State == TruePeakFinite && qc.Loudness.TruePeak.DBTP > -0.5 {
		return fmt.Errorf("derivative true peak %.1f dBTP exceeds the normalized playback ceiling", qc.Loudness.TruePeak.DBTP)
	}
	return nil
}

func validDerivativeLoudness(loudness ConditioningLoudness) bool {
	if !loudness.Available || math.IsNaN(loudness.IntegratedLUFS) || math.IsInf(loudness.IntegratedLUFS, 0) {
		return false
	}
	switch loudness.TruePeak.State {
	case TruePeakFinite:
		return !math.IsNaN(loudness.TruePeak.DBTP) && !math.IsInf(loudness.TruePeak.DBTP, 0)
	case TruePeakNegativeInfinity:
		return loudness.TruePeak.DBTP == 0
	default:
		return false
	}
}

// VerifyDerivative completely decodes a candidate, measures loudness, proves fast-start atom
// order, measures actual keyframe gaps, and performs an input-seek frame decode. Nothing is
// published merely because ffmpeg accepted the requested flags.
func VerifyDerivative(ctx context.Context, ffmpegPath, path string, durationMs int64, keyframeSeconds int, hadAudio bool, targetLUFS float64) (DerivativeQC, error) {
	if path == "" || durationMs <= 0 || keyframeSeconds <= 0 {
		return DerivativeQC{}, errors.New("verify derivative: path, duration, and keyframe interval are required")
	}
	fastStart, err := verifyFastStart(path)
	if err != nil {
		return DerivativeQC{}, fmt.Errorf("verify derivative fast-start: %w", err)
	}
	if !fastStart {
		return DerivativeQC{}, errors.New("verify derivative fast-start: moov atom does not precede media data")
	}
	first, maximum, terminal, err := verifyDerivativeKeyframes(ctx, FFprobePathNextTo(ffmpegPath), path, durationMs)
	if err != nil {
		return DerivativeQC{}, err
	}

	decodeArgs := []string{"-nostdin", "-hide_banner", "-nostats", "-v", "info", "-i", path, "-map", "0:v:0"}
	if hadAudio {
		decodeArgs = append(decodeArgs, "-map", "0:a:0", "-af", "ebur128=peak=true:framelog=quiet")
	} else {
		decodeArgs = append(decodeArgs, "-an")
	}
	decodeArgs = append(decodeArgs, "-f", "null", "-")
	decodeOutput, err := runDerivativeCommand(ctx, FFmpegOr(ffmpegPath), true, decodeArgs...)
	if err != nil {
		return DerivativeQC{}, fmt.Errorf("verify derivative complete decode: %w", err)
	}
	loudness, err := parseConditioningLoudness(string(decodeOutput))
	if err != nil {
		return DerivativeQC{}, fmt.Errorf("verify derivative loudness: %w", err)
	}

	seekMs := durationMs / 2
	seekArgs := []string{
		"-nostdin", "-hide_banner", "-v", "error", "-ss", MsToFFmpegTime(seekMs),
		"-i", path, "-map", "0:v:0", "-frames:v", "1", "-f", "null", "-",
	}
	if _, err := runDerivativeCommand(ctx, FFmpegOr(ffmpegPath), true, seekArgs...); err != nil {
		return DerivativeQC{}, fmt.Errorf("verify derivative seek at %dms: %w", seekMs, err)
	}
	qc := DerivativeQC{
		Version: DerivativeQCVersion, FastStart: true, CompleteDecode: true, Seekable: true,
		FirstVideoKeyframeMs: first, MaxVideoKeyframeGapMs: maximum, TerminalKeyframeGapMs: terminal,
		Loudness: loudness,
	}
	if err := ValidateDerivativeQC(qc, durationMs, keyframeSeconds, hadAudio, targetLUFS); err != nil {
		return DerivativeQC{}, fmt.Errorf("verify derivative: %w", err)
	}
	return qc, nil
}

func verifyFastStart(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 8 {
		return false, errors.New("output is not a non-empty regular MP4")
	}
	var ftyp, moov, mdat int64 = -1, -1, -1
	for offset, boxes := int64(0), 0; offset < info.Size(); boxes++ {
		if boxes >= derivativeTopLevelBoxLimit {
			return false, errors.New("MP4 has too many top-level boxes")
		}
		var header [16]byte
		if _, err := file.ReadAt(header[:8], offset); err != nil {
			return false, fmt.Errorf("read MP4 box at %d: %w", offset, err)
		}
		size := int64(binary.BigEndian.Uint32(header[:4]))
		headerSize := int64(8)
		switch size {
		case 1:
			if _, err := file.ReadAt(header[8:16], offset+8); err != nil {
				return false, fmt.Errorf("read extended MP4 box at %d: %w", offset, err)
			}
			if binary.BigEndian.Uint64(header[8:16]) > math.MaxInt64 {
				return false, errors.New("MP4 box size overflows")
			}
			size = int64(binary.BigEndian.Uint64(header[8:16]))
			headerSize = 16
		case 0:
			size = info.Size() - offset
		}
		if size < headerSize || size > info.Size()-offset {
			return false, fmt.Errorf("invalid MP4 box size %d at %d", size, offset)
		}
		switch string(header[4:8]) {
		case "ftyp":
			if ftyp < 0 {
				ftyp = offset
			}
		case "moov":
			if moov < 0 {
				moov = offset
			}
		case "mdat":
			if mdat < 0 {
				mdat = offset
			}
		}
		offset += size
	}
	if ftyp < 0 || moov < 0 || mdat < 0 {
		return false, errors.New("MP4 is missing ftyp, moov, or mdat")
	}
	return ftyp < moov && moov < mdat, nil
}

func verifyDerivativeKeyframes(ctx context.Context, ffprobePath, path string, durationMs int64) (int64, int64, int64, error) {
	raw, err := runDerivativeCommand(ctx, ffprobePath, false,
		"-v", "error", "-select_streams", "v:0", "-skip_frame", "nokey",
		"-show_frames", "-show_entries", "frame=best_effort_timestamp_time", "-of", "json", path)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("verify derivative keyframes: %w", err)
	}
	var document struct {
		Frames []struct {
			Timestamp string `json:"best_effort_timestamp_time"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return 0, 0, 0, fmt.Errorf("verify derivative keyframes: parse ffprobe: %w", err)
	}
	if len(document.Frames) == 0 {
		return 0, 0, 0, errors.New("verify derivative keyframes: output has no video keyframe")
	}
	timestamps := make([]int64, 0, len(document.Frames))
	for _, frame := range document.Frames {
		seconds, err := strconv.ParseFloat(strings.TrimSpace(frame.Timestamp), 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
			return 0, 0, 0, fmt.Errorf("verify derivative keyframes: invalid timestamp %q", frame.Timestamp)
		}
		milliseconds := int64(math.Round(seconds * 1000))
		if len(timestamps) > 0 && milliseconds <= timestamps[len(timestamps)-1] {
			return 0, 0, 0, errors.New("verify derivative keyframes: timestamps are not strictly increasing")
		}
		timestamps = append(timestamps, milliseconds)
	}
	first, maximum := timestamps[0], int64(0)
	for index := 1; index < len(timestamps); index++ {
		maximum = max(maximum, timestamps[index]-timestamps[index-1])
	}
	terminal := durationMs - timestamps[len(timestamps)-1]
	if terminal < 0 {
		return 0, 0, 0, errors.New("verify derivative keyframes: keyframe lies beyond output duration")
	}
	return first, maximum, terminal, nil
}

type derivativeOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	overflow bool
}

func (w *derivativeOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := derivativeQCOutputLimit - w.buffer.Len()
	if remaining > 0 {
		_, _ = w.buffer.Write(data[:min(len(data), remaining)])
	}
	if len(data) > remaining {
		w.overflow = true
	}
	return len(data), nil
}

func (w *derivativeOutput) result() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.buffer.Bytes()), w.overflow
}

func runDerivativeCommand(ctx context.Context, executable string, combined bool, args ...string) ([]byte, error) {
	stdout, stderr := &derivativeOutput{}, &derivativeOutput{}
	if combined {
		stderr = stdout
	}
	cmd := exec.CommandContext(ctx, executable, args...) //nolint:gosec // executable is the operator-configured media tool
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()
	stdoutBytes, stdoutOverflow := stdout.result()
	stderrBytes, stderrOverflow := stderr.result()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stdoutOverflow || stderrOverflow {
		return nil, fmt.Errorf("tool output exceeds %d bytes", derivativeQCOutputLimit)
	}
	if runErr != nil {
		return nil, fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(stderrBytes)))
	}
	return stdoutBytes, nil
}

// FFprobePathNextTo derives ffprobe from the configured ffmpeg path. They ship as one toolchain;
// a second independently configurable path would permit recipe and verifier drift.
func FFprobePathNextTo(ffmpegPath string) string {
	if ffmpegPath != "" && ffmpegPath != "ffmpeg" {
		dir, base := filepath.Split(ffmpegPath)
		return filepath.Join(dir, strings.Replace(base, "ffmpeg", "ffprobe", 1))
	}
	return "ffprobe"
}
