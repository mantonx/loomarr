package fillerreview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

const temporalStructureMediaOutputMaximumBytes = 256 << 10

// Decode completely reads the first video and audio streams. Requiring both streams here keeps a
// source with missing or corrupt audio from becoming a valid long-reel certification fixture.
func (media *FFmpegTemporalStructureMedia) Decode(ctx context.Context, path string) error {
	if media == nil || media.media == nil || strings.TrimSpace(path) == "" {
		return errors.New("temporal structure media decode requires an adapter and path")
	}
	output := &boundedTemporalStructureMediaOutput{}
	command := exec.CommandContext(ctx, media.media.identity.FFmpeg.Path,
		"-nostdin", "-hide_banner", "-nostats", "-v", "error", "-i", path,
		"-map", "0:v:0", "-map", "0:a:0", "-f", "null", "-")
	command.Stdout, command.Stderr = output, output
	if err := runTemporalStructureMediaCommand(ctx, command, output); err != nil {
		return fmt.Errorf("decode temporal structure media: %w", err)
	}
	return nil
}

type boundedTemporalStructureMediaOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	overflow bool
}

func (output *boundedTemporalStructureMediaOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	remaining := temporalStructureMediaOutputMaximumBytes - output.buffer.Len()
	if remaining > 0 {
		_, _ = output.buffer.Write(data[:min(len(data), remaining)])
	}
	if len(data) > remaining {
		output.overflow = true
	}
	return len(data), nil
}

func (output *boundedTemporalStructureMediaOutput) overflowed() bool {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.overflow
}

func (output *boundedTemporalStructureMediaOutput) message() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return strings.TrimSpace(output.buffer.String())
}

func runTemporalStructureMediaCommand(ctx context.Context, command *exec.Cmd, output *boundedTemporalStructureMediaOutput) error {
	err := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if output.overflowed() {
		return errors.New("ffmpeg output exceeded 256 KiB")
	}
	message := output.message()
	if err != nil {
		return fmt.Errorf("%w: %s", err, message)
	}
	if message != "" {
		return fmt.Errorf("ffmpeg reported errors despite a successful exit: %s", message)
	}
	return nil
}

var _ TemporalStructureWindowCorpusMedia = (*FFmpegTemporalStructureMedia)(nil)
