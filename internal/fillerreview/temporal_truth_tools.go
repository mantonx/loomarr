package fillerreview

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/loomarr/loomarr/internal/mediatools"
)

type FFmpegTemporalTruthMedia struct {
	identity TemporalTruthMediaIdentity
	tools    *mediatools.FFmpegTools
}

func NewFFmpegTemporalTruthMedia(ctx context.Context, ffmpegName, ffprobeName string) (*FFmpegTemporalTruthMedia, error) {
	ffmpeg, err := temporalTruthExecutableIdentity(ctx, ffmpegName)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg identity: %w", err)
	}
	ffprobe, err := temporalTruthExecutableIdentity(ctx, ffprobeName)
	if err != nil {
		return nil, fmt.Errorf("ffprobe identity: %w", err)
	}
	return &FFmpegTemporalTruthMedia{
		identity: TemporalTruthMediaIdentity{FFmpeg: ffmpeg, FFprobe: ffprobe},
		tools:    mediatools.NewFFmpegTools(ffmpeg.Path, ffprobe.Path, "", "", ""),
	}, nil
}

func (media *FFmpegTemporalTruthMedia) Identity() TemporalTruthMediaIdentity { return media.identity }

func (media *FFmpegTemporalTruthMedia) Analyze(ctx context.Context, path string, startMS, durationMS int64, threshold float64) ([]mediatools.Interval, []mediatools.Interval, []int64, error) {
	endMS := startMS + durationMS
	black, silence, err := media.tools.Boundaries(ctx, path, startMS, endMS)
	if err != nil {
		return nil, nil, nil, err
	}
	cuts, err := media.tools.SceneCutsIn(ctx, path, startMS, endMS, threshold)
	if err != nil {
		return nil, nil, nil, err
	}
	for index := range black {
		black[index].StartMs -= startMS
		black[index].EndMs -= startMS
	}
	for index := range silence {
		silence[index].StartMs -= startMS
		silence[index].EndMs -= startMS
	}
	for index := range cuts {
		cuts[index] -= startMS
	}
	return black, silence, cuts, nil
}

func (media *FFmpegTemporalTruthMedia) WriteReviewVideo(ctx context.Context, source string, startMS, durationMS int64, output string) (TemporalTruthVideoInfo, error) {
	arguments := temporalTruthReviewVideoArguments(source, startMS, durationMS, output)
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, media.identity.FFmpeg.Path, arguments...)
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return TemporalTruthVideoInfo{}, fmt.Errorf("ffmpeg review video: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return media.probeReviewVideo(ctx, output)
}

func temporalTruthReviewVideoArguments(source string, startMS, durationMS int64, output string) []string {
	return []string{
		"-nostdin", "-hide_banner", "-v", "error", "-y",
		// Pin the input decoder as well as the output encoder. The Theora decoder
		// otherwise produced byte-different H.264 review files from identical input
		// on repeated executions, despite a single-threaded encoder.
		"-threads", "1", "-ss", mediatools.MsToFFmpegTime(startMS), "-t", mediatools.MsToFFmpegTime(durationMS), "-i", source,
		"-map", "0:v:0", "-map", "0:a:0?", "-map_metadata", "-1", "-map_chapters", "-1",
		"-vf", "scale=w='min(iw,1280)':h=-2,setsar=1",
		"-c:v", "libx264", "-preset", "medium", "-crf", "23", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-ar", "48000", "-ac", "2",
		"-threads", "1", "-filter_threads", "1", "-filter_complex_threads", "1",
		"-fflags", "+bitexact", "-flags:v", "+bitexact", "-flags:a", "+bitexact",
		"-metadata", "creation_time=", "-metadata", "encoder=", "-metadata:s:v", "encoder=", "-metadata:s:a", "encoder=",
		"-movflags", "+faststart", output,
	}
}

func (media *FFmpegTemporalTruthMedia) Frames(ctx context.Context, path string, atMS []int64) ([][]byte, error) {
	return media.tools.FramesAt(ctx, path, atMS)
}

func (media *FFmpegTemporalTruthMedia) probeReviewVideo(ctx context.Context, path string) (TemporalTruthVideoInfo, error) {
	output, err := exec.CommandContext(ctx, media.identity.FFprobe.Path,
		"-v", "error", "-show_entries", "format=duration:stream=codec_type,codec_name,width,height,pix_fmt,avg_frame_rate,sample_rate,channels",
		"-of", "json", path).Output()
	if err != nil {
		return TemporalTruthVideoInfo{}, fmt.Errorf("ffprobe review video: %w", err)
	}
	return decodeTemporalTruthVideoProbe(output)
}

func decodeTemporalTruthVideoProbe(output []byte) (TemporalTruthVideoInfo, error) {
	var probe struct {
		Programs     []json.RawMessage `json:"programs"`
		StreamGroups []json.RawMessage `json:"stream_groups"`
		Streams      []struct {
			CodecType   string `json:"codec_type"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
			CodecName   string `json:"codec_name"`
			PixelFormat string `json:"pix_fmt"`
			FrameRate   string `json:"avg_frame_rate"`
			SampleRate  string `json:"sample_rate"`
			Channels    int    `json:"channels"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&probe); err != nil {
		return TemporalTruthVideoInfo{}, fmt.Errorf("decode ffprobe review video: %w", err)
	}
	if len(probe.Programs) != 0 || len(probe.StreamGroups) != 0 {
		return TemporalTruthVideoInfo{}, fmt.Errorf("review video contains unexpected programs or stream groups")
	}
	seconds, err := strconv.ParseFloat(probe.Format.Duration, 64)
	if err != nil || seconds <= 0 {
		return TemporalTruthVideoInfo{}, fmt.Errorf("ffprobe returned invalid review-video duration")
	}
	result := TemporalTruthVideoInfo{DurationMS: int64(seconds*1000 + 0.5)}
	for _, stream := range probe.Streams {
		if stream.CodecType == "video" {
			if result.Width != 0 {
				return TemporalTruthVideoInfo{}, fmt.Errorf("review video contains more than one video stream")
			}
			result.Width, result.Height = stream.Width, stream.Height
			result.Profile.VideoCodec, result.Profile.PixelFormat, result.Profile.FrameRate = stream.CodecName, stream.PixelFormat, stream.FrameRate
		}
		if stream.CodecType == "audio" {
			result.HasAudio = true
			result.Profile.AudioStreams++
			if result.Profile.AudioStreams == 1 {
				result.Profile.AudioCodec = stream.CodecName
				result.Profile.Channels = stream.Channels
				// Absent or malformed measurements remain zero and cannot satisfy a profile.
				result.Profile.SampleRate, _ = strconv.Atoi(stream.SampleRate)
			}
		}
	}
	if result.Width <= 0 || result.Height <= 0 {
		return TemporalTruthVideoInfo{}, fmt.Errorf("review video contains no measured video stream")
	}
	return result, nil
}

type ExecTemporalTruthOCR struct {
	identity TemporalTruthToolIdentity
}

func NewExecTemporalTruthOCR(enginePath, sourcePath, version string) (*ExecTemporalTruthOCR, error) {
	resolvedEngine, err := filepath.Abs(enginePath)
	if err != nil {
		return nil, err
	}
	if err := ensureTemporalTruthExecutable(resolvedEngine); err != nil {
		return nil, err
	}
	engineSHA, err := hashFile(resolvedEngine)
	if err != nil {
		return nil, err
	}
	sourceSHA, err := hashFile(sourcePath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(version) == "" {
		return nil, fmt.Errorf("OCR version is required")
	}
	return &ExecTemporalTruthOCR{identity: TemporalTruthToolIdentity{Path: resolvedEngine, Version: version, BinarySHA256: engineSHA, SourceSHA256: sourceSHA}}, nil
}

func (ocr *ExecTemporalTruthOCR) Identity() TemporalTruthToolIdentity { return ocr.identity }

func (ocr *ExecTemporalTruthOCR) Recognize(ctx context.Context, inputs []TemporalTruthOCRInput) ([]TemporalTruthOCRResult, error) {
	arguments := make([]string, len(inputs))
	byPath := make(map[string]TemporalTruthOCRInput, len(inputs))
	for index, input := range inputs {
		resolved, err := filepath.Abs(input.Path)
		if err != nil || !reviewSHA256(input.SHA256) {
			return nil, fmt.Errorf("OCR input %d has invalid path or hash", index)
		}
		arguments[index] = resolved
		byPath[resolved] = input
	}
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, ocr.identity.Path, arguments...)
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("OCR engine: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	type engineResult struct {
		Path         string                        `json:"path"`
		Observations []TemporalTruthOCRObservation `json:"observations"`
	}
	results := make(map[string]TemporalTruthOCRResult, len(inputs))
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for line := 1; scanner.Scan(); line++ {
		var result engineResult
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&result); err != nil {
			return nil, fmt.Errorf("OCR result line %d: %w", line, err)
		}
		resolved, err := filepath.Abs(result.Path)
		input, exists := byPath[resolved]
		if err != nil || !exists {
			return nil, fmt.Errorf("OCR result line %d names an unknown frame", line)
		}
		if _, duplicate := results[resolved]; duplicate {
			return nil, fmt.Errorf("OCR result repeats frame %q", resolved)
		}
		accepted := make([]TemporalTruthOCRObservation, 0, len(result.Observations))
		for _, observation := range result.Observations {
			observation.Text = strings.TrimSpace(observation.Text)
			if observation.Text != "" && observation.Confidence >= 0.5 {
				accepted = append(accepted, observation)
			}
		}
		results[resolved] = TemporalTruthOCRResult{SHA256: input.SHA256, Observations: accepted}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	ordered := make([]TemporalTruthOCRResult, 0, len(inputs))
	for _, path := range arguments {
		result, exists := results[path]
		if !exists {
			return nil, fmt.Errorf("OCR omitted frame %q", path)
		}
		ordered = append(ordered, result)
	}
	return ordered, nil
}

func temporalTruthExecutableIdentity(ctx context.Context, name string) (TemporalTruthToolIdentity, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return TemporalTruthToolIdentity{}, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return TemporalTruthToolIdentity{}, err
	}
	digest, err := hashFile(path)
	if err != nil {
		return TemporalTruthToolIdentity{}, err
	}
	output, err := exec.CommandContext(ctx, path, "-version").Output()
	if err != nil {
		return TemporalTruthToolIdentity{}, err
	}
	version, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
	return TemporalTruthToolIdentity{Path: path, Version: version, BinarySHA256: digest}, nil
}

func ensureTemporalTruthExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("tool is not an executable regular file")
	}
	return nil
}
