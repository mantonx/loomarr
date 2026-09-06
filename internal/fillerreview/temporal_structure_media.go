package fillerreview

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/loomarr/loomarr/internal/mediatools"
)

// FFmpegTemporalStructureMedia normalizes every source segment to one fixed
// challenge profile before joining compatible streams. The resulting join
// times come from the normalized part probes, not assumed source timestamps.
type FFmpegTemporalStructureMedia struct {
	media *FFmpegTemporalTruthMedia
}

func NewFFmpegTemporalStructureMedia(ctx context.Context, ffmpegName, ffprobeName string) (*FFmpegTemporalStructureMedia, error) {
	media, err := NewFFmpegTemporalTruthMedia(ctx, ffmpegName, ffprobeName)
	if err != nil {
		return nil, err
	}
	return &FFmpegTemporalStructureMedia{media: media}, nil
}

func (media *FFmpegTemporalStructureMedia) Identity() TemporalTruthMediaIdentity {
	return media.media.Identity()
}

func (media *FFmpegTemporalStructureMedia) Probe(ctx context.Context, path string) (TemporalTruthVideoInfo, error) {
	return media.media.probeReviewVideo(ctx, path)
}

func (media *FFmpegTemporalStructureMedia) Render(ctx context.Context, segments []TemporalStructureRenderSegment, output string) (TemporalStructureRenderResult, error) {
	if len(segments) == 0 {
		return TemporalStructureRenderResult{}, fmt.Errorf("at least one render segment is required")
	}
	temporary, err := os.MkdirTemp(filepath.Dir(output), ".temporal-structure-render-*")
	if err != nil {
		return TemporalStructureRenderResult{}, err
	}
	defer func() { _ = os.RemoveAll(temporary) }()

	result := TemporalStructureRenderResult{Parts: make([]TemporalStructureRenderedPart, 0, len(segments))}
	partNames := make([]string, 0, len(segments))
	for index, segment := range segments {
		name := fmt.Sprintf("part-%03d.mp4", index)
		partPath := filepath.Join(temporary, name)
		info, err := media.writePart(ctx, segment, partPath)
		if err != nil {
			return TemporalStructureRenderResult{}, fmt.Errorf("render part %d: %w", index, err)
		}
		if err := validateTemporalStructureVideoProfile(info); err != nil {
			return TemporalStructureRenderResult{}, fmt.Errorf("render part %d: %w", index, err)
		}
		partNames = append(partNames, name)
		result.Parts = append(result.Parts, TemporalStructureRenderedPart{DurationMS: info.DurationMS})
	}
	if len(partNames) == 1 {
		if err := os.Rename(filepath.Join(temporary, partNames[0]), output); err != nil {
			return TemporalStructureRenderResult{}, err
		}
	} else {
		listPath := filepath.Join(temporary, "concat.txt")
		var list strings.Builder
		for _, name := range partNames {
			_, _ = fmt.Fprintf(&list, "file '%s'\n", name)
		}
		if err := os.WriteFile(listPath, []byte(list.String()), 0o600); err != nil {
			return TemporalStructureRenderResult{}, err
		}
		arguments := temporalStructureConcatArguments("concat.txt", output)
		var stderr bytes.Buffer
		command := exec.CommandContext(ctx, media.media.identity.FFmpeg.Path, arguments...)
		command.Dir = temporary
		command.Stderr = &stderr
		if err := command.Run(); err != nil {
			return TemporalStructureRenderResult{}, fmt.Errorf("join normalized parts: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	result.Video, err = media.media.probeReviewVideo(ctx, output)
	if err != nil {
		return TemporalStructureRenderResult{}, err
	}
	if err := validateTemporalStructureVideoProfile(result.Video); err != nil {
		return TemporalStructureRenderResult{}, err
	}
	return result, nil
}

func (media *FFmpegTemporalStructureMedia) writePart(ctx context.Context, segment TemporalStructureRenderSegment, output string) (TemporalTruthVideoInfo, error) {
	arguments := temporalStructurePartArguments(segment.SourcePath, segment.StartMS, segment.DurationMS, output)
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, media.media.identity.FFmpeg.Path, arguments...)
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return TemporalTruthVideoInfo{}, fmt.Errorf("render normalized structure part: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return media.media.probeReviewVideo(ctx, output)
}

func temporalStructurePartArguments(source string, startMS, durationMS int64, output string) []string {
	return []string{
		"-nostdin", "-hide_banner", "-v", "error", "-y",
		"-threads", "1", "-ss", mediatools.MsToFFmpegTime(startMS), "-t", mediatools.MsToFFmpegTime(durationMS), "-i", source,
		"-map", "0:v:0", "-map", "0:a:0?", "-map_metadata", "-1", "-map_chapters", "-1",
		"-vf", "fps=30,scale=w=960:h=720:force_original_aspect_ratio=decrease,pad=960:720:(ow-iw)/2:(oh-ih)/2,setsar=1",
		"-c:v", "libx264", "-preset", "medium", "-crf", "23", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-ar", "48000", "-ac", "2",
		"-threads", "1", "-filter_threads", "1", "-filter_complex_threads", "1",
		"-fflags", "+bitexact", "-flags:v", "+bitexact", "-flags:a", "+bitexact", "-video_track_timescale", "90000",
		"-metadata", "creation_time=", "-metadata", "encoder=", "-metadata:s:v", "encoder=", "-metadata:s:a", "encoder=",
		"-movflags", "+faststart", output,
	}
}

func temporalStructureConcatArguments(list, output string) []string {
	return []string{
		"-nostdin", "-hide_banner", "-v", "error", "-y",
		"-f", "concat", "-safe", "1", "-i", list,
		"-map", "0:v:0", "-map", "0:a:0?", "-map_metadata", "-1", "-map_chapters", "-1",
		"-c", "copy", "-fflags", "+bitexact",
		"-metadata", "creation_time=", "-metadata", "encoder=", "-metadata:s:v", "encoder=", "-metadata:s:a", "encoder=",
		"-movflags", "+faststart", output,
	}
}
