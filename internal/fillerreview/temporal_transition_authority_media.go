package fillerreview

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/loomarr/loomarr/internal/mediatools"
)

type ExecTemporalTransitionEvidenceMedia struct {
	ffmpeg   string
	identity TemporalTruthToolIdentity
}

func NewExecTemporalTransitionEvidenceMedia(ctx context.Context, ffmpegPath string) (*ExecTemporalTransitionEvidenceMedia, error) {
	identity, err := temporalTruthExecutableIdentity(ctx, ffmpegPath)
	if err != nil {
		return nil, fmt.Errorf("identify transition FFmpeg: %w", err)
	}
	return &ExecTemporalTransitionEvidenceMedia{ffmpeg: identity.Path, identity: identity}, nil
}

func (media *ExecTemporalTransitionEvidenceMedia) Identity() TemporalTruthToolIdentity {
	return media.identity
}

func (media *ExecTemporalTransitionEvidenceMedia) MeasureEdges(ctx context.Context, path string, durationMS int64) (TemporalTransitionEdges, error) {
	if durationMS < TemporalTransitionEdgeWindowMS {
		return TemporalTransitionEdges{}, fmt.Errorf("transition source is shorter than the edge window")
	}
	head, err := media.measureEdge(ctx, path, 0, TemporalTransitionEdgeWindowMS, true)
	if err != nil {
		return TemporalTransitionEdges{}, err
	}
	tail, err := media.measureEdge(ctx, path, durationMS-TemporalTransitionEdgeWindowMS, durationMS, false)
	if err != nil {
		return TemporalTransitionEdges{}, err
	}
	return TemporalTransitionEdges{Head: head, Tail: tail}, nil
}

func (media *ExecTemporalTransitionEvidenceMedia) measureEdge(ctx context.Context, path string, startMS, endMS int64, head bool) (TemporalTransitionEdge, error) {
	videoFilter := "scale=960:720:force_original_aspect_ratio=decrease,pad=960:720:(ow-iw)/2:(oh-ih)/2,fps=30,trim=duration=1,setpts=PTS-STARTPTS,format=yuv420p,blackdetect=d=0.040:pix_th=0.10"
	audioFilter := "aresample=48000,aformat=channel_layouts=stereo,atrim=duration=1,asetpts=PTS-STARTPTS,silencedetect=n=-40dB:d=0.040"
	var detector bytes.Buffer
	command := exec.CommandContext(ctx, media.ffmpeg,
		"-nostdin", "-hide_banner", "-nostats", "-v", "info",
		"-ss", mediatools.MsToFFmpegTime(startMS), "-t", mediatools.MsToFFmpegTime(endMS-startMS), "-i", path,
		"-vf", videoFilter, "-af", audioFilter, "-f", "null", "-")
	command.Stderr = &detector
	if err := command.Run(); err != nil {
		return TemporalTransitionEdge{}, fmt.Errorf("measure transition detectors: %w: %s", err, truncateTransitionOutput(detector.String()))
	}
	black, silence, err := parseTemporalTransitionDetectors(detector.String(), startMS, endMS)
	if err != nil {
		return TemporalTransitionEdge{}, err
	}
	pcm, err := exec.CommandContext(ctx, media.ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-ss", mediatools.MsToFFmpegTime(startMS), "-t", mediatools.MsToFFmpegTime(endMS-startMS), "-i", path,
		"-vn", "-ac", "2", "-ar", "48000", "-f", "s16le", "-").Output()
	if err != nil {
		return TemporalTransitionEdge{}, fmt.Errorf("measure transition level support: %w", err)
	}
	const supportBytes = int(TemporalTransitionSupportWindowMS) * 48_000 * 2 * 2 / 1_000
	if len(pcm) < supportBytes {
		return TemporalTransitionEdge{}, fmt.Errorf("transition audio support is incomplete")
	}
	if head {
		pcm = pcm[:supportBytes]
	} else {
		pcm = pcm[len(pcm)-supportBytes:]
	}
	rms, peak := temporalTransitionLevels(pcm)
	return TemporalTransitionEdge{
		StartMS: startMS, EndMS: endMS, Black: black, Silence: silence,
		RMSMilliDBFS: rms, PeakMilliDBFS: peak,
	}, nil
}

var (
	temporalTransitionBlackPattern        = regexp.MustCompile(`black_start:([-0-9.]+) black_end:([-0-9.]+) black_duration:([-0-9.]+)`)
	temporalTransitionSilenceStartPattern = regexp.MustCompile(`silence_start: ([-0-9.]+)`)
	temporalTransitionSilenceEndPattern   = regexp.MustCompile(`silence_end: ([-0-9.]+) \| silence_duration: ([-0-9.]+)`)
)

func parseTemporalTransitionDetectors(raw string, offsetMS, endMS int64) ([]mediatools.Interval, []mediatools.Interval, error) {
	spanMS := endMS - offsetMS
	var black, silence []mediatools.Interval
	type silenceStart struct {
		milliseconds int64
		raw          string
	}
	var open *silenceStart
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(line, "black_start:") {
			match := temporalTransitionBlackPattern.FindStringSubmatch(line)
			if match == nil || !temporalTransitionDurationMatches(match[1], match[2], match[3]) {
				return nil, nil, fmt.Errorf("parse transition black interval: malformed detector output")
			}
			interval, err := temporalTransitionInterval(match[1], match[2], spanMS, offsetMS)
			if err != nil {
				return nil, nil, fmt.Errorf("parse transition black interval: %w", err)
			}
			black = append(black, interval)
		}
		if strings.Contains(line, "silence_start:") {
			match := temporalTransitionSilenceStartPattern.FindStringSubmatch(line)
			if match == nil {
				return nil, nil, fmt.Errorf("parse transition silence interval: malformed start")
			}
			if open != nil {
				return nil, nil, fmt.Errorf("parse transition silence interval: repeated start")
			}
			start, err := temporalTransitionMilliseconds(match[1], spanMS)
			if err != nil {
				return nil, nil, err
			}
			open = &silenceStart{milliseconds: start, raw: match[1]}
			continue
		}
		if strings.Contains(line, "silence_end:") {
			match := temporalTransitionSilenceEndPattern.FindStringSubmatch(line)
			if match == nil {
				return nil, nil, fmt.Errorf("parse transition silence interval: malformed end")
			}
			if open == nil {
				return nil, nil, fmt.Errorf("parse transition silence interval: end without start")
			}
			finish, err := temporalTransitionMilliseconds(match[1], spanMS)
			if err != nil || finish <= open.milliseconds || !temporalTransitionDurationMatches(open.raw, match[1], match[2]) {
				return nil, nil, fmt.Errorf("parse transition silence interval: invalid end")
			}
			silence = append(silence, mediatools.Interval{StartMs: offsetMS + open.milliseconds, EndMs: offsetMS + finish})
			open = nil
		}
	}
	if open != nil {
		silence = append(silence, mediatools.Interval{StartMs: offsetMS + open.milliseconds, EndMs: endMS})
	}
	return normalizeTemporalTransitionIntervals(black), normalizeTemporalTransitionIntervals(silence), nil
}

func temporalTransitionDurationMatches(startRaw, endRaw, durationRaw string) bool {
	start, startErr := strconv.ParseFloat(startRaw, 64)
	end, endErr := strconv.ParseFloat(endRaw, 64)
	duration, durationErr := strconv.ParseFloat(durationRaw, 64)
	if startErr != nil || endErr != nil || durationErr != nil || math.IsNaN(start) || math.IsNaN(end) || math.IsNaN(duration) || math.IsInf(start, 0) || math.IsInf(end, 0) || math.IsInf(duration, 0) || duration <= 0 {
		return false
	}
	return math.Abs((end-start)-duration) <= 0.002
}

func normalizeTemporalTransitionIntervals(intervals []mediatools.Interval) []mediatools.Interval {
	if len(intervals) == 0 {
		return nil
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].StartMs < intervals[j].StartMs })
	result := make([]mediatools.Interval, 0, len(intervals))
	for _, interval := range intervals {
		if len(result) == 0 || interval.StartMs > result[len(result)-1].EndMs {
			result = append(result, interval)
			continue
		}
		if interval.EndMs > result[len(result)-1].EndMs {
			result[len(result)-1].EndMs = interval.EndMs
		}
	}
	return result
}

func temporalTransitionInterval(startRaw, endRaw string, spanMS, offsetMS int64) (mediatools.Interval, error) {
	start, err := temporalTransitionMilliseconds(startRaw, spanMS)
	if err != nil {
		return mediatools.Interval{}, err
	}
	end, err := temporalTransitionMilliseconds(endRaw, spanMS)
	if err != nil || end <= start {
		return mediatools.Interval{}, fmt.Errorf("invalid interval end")
	}
	return mediatools.Interval{StartMs: offsetMS + start, EndMs: offsetMS + end}, nil
}

func temporalTransitionMilliseconds(raw string, maximum int64) (int64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < -float64(TemporalTransitionBoundaryToleranceMS)/1_000 {
		return 0, fmt.Errorf("invalid detector timestamp")
	}
	ms := int64(math.Round(value * 1_000))
	if ms < 0 {
		ms = 0
	}
	if ms > maximum+TemporalTransitionBoundaryToleranceMS {
		return 0, fmt.Errorf("detector timestamp exceeds edge window")
	}
	if ms > maximum {
		ms = maximum
	}
	return ms, nil
}

func temporalTransitionLevels(pcm []byte) (int64, int64) {
	var sum float64
	var peak float64
	for index := 0; index+1 < len(pcm); index += 2 {
		sample := float64(int16(uint16(pcm[index])|uint16(pcm[index+1])<<8)) / 32768
		absolute := math.Abs(sample)
		if absolute > peak {
			peak = absolute
		}
		sum += sample * sample
	}
	rms := math.Sqrt(sum / float64(len(pcm)/2))
	return temporalTransitionMilliDBFS(rms), temporalTransitionMilliDBFS(peak)
}

func temporalTransitionMilliDBFS(value float64) int64 {
	if value <= 0 {
		return -120_000
	}
	result := int64(math.Round(20 * math.Log10(value) * 1_000))
	if result < -120_000 {
		return -120_000
	}
	if result > 0 {
		return 0
	}
	return result
}

func truncateTransitionOutput(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 1_000 {
		return value
	}
	return value[len(value)-1_000:]
}
