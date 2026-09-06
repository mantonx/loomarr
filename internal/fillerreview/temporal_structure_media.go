package fillerreview

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
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
		arguments := fillerstructuremedia.ConcatArguments("concat.txt", output)
		commandOutput := &boundedTemporalStructureMediaOutput{}
		command := exec.CommandContext(ctx, media.media.identity.FFmpeg.Path, arguments...)
		command.Dir = temporary
		command.Stdout, command.Stderr = commandOutput, commandOutput
		if err := runTemporalStructureMediaCommand(ctx, command, commandOutput); err != nil {
			return TemporalStructureRenderResult{}, fmt.Errorf("join normalized parts: %w", err)
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
	arguments := fillerstructuremedia.PartArguments(segment.SourcePath, segment.StartMS, segment.DurationMS, output)
	commandOutput := &boundedTemporalStructureMediaOutput{}
	command := exec.CommandContext(ctx, media.media.identity.FFmpeg.Path, arguments...)
	command.Stdout, command.Stderr = commandOutput, commandOutput
	if err := runTemporalStructureMediaCommand(ctx, command, commandOutput); err != nil {
		return TemporalTruthVideoInfo{}, fmt.Errorf("render normalized structure part: %w", err)
	}
	return media.media.probeReviewVideo(ctx, output)
}
