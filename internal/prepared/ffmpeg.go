package prepared

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/loomarr/loomarr/internal/diagnostics"
)

const (
	// MediaManifestName is the validated VOD media playlist every prepared packager publishes.
	MediaManifestName = "media.m3u8"
	// CurrentPackagingVersion changes whenever the prepared byte layout or manifest contract makes
	// older publications incompatible. It participates in immutable publication identity.
	CurrentPackagingVersion = 1
)

var ErrUnsupportedRendition = errors.New("prepared: unsupported rendition")

// FFmpegPackager is the real prepared-media driver. It produces finite fMP4 HLS as fast as the
// machine allows; it is control-plane work and deliberately carries no realtime pacing flags.
type FFmpegPackager struct {
	path        string
	videoArgs   VideoArgs
	diagnostics *diagnostics.ProcessManager
}

// WithDiagnostics observes packaging without making diagnostics part of packaging correctness.
func (p *FFmpegPackager) WithDiagnostics(manager *diagnostics.ProcessManager) *FFmpegPackager {
	if p != nil {
		p.diagnostics = manager
	}
	return p
}

// VideoPlan separates arguments that ffmpeg requires before its input from filters and encoder
// arguments that belong after it. That positional split is required by hardware device setup.
type VideoPlan struct {
	InputArgs  []string
	OutputArgs []string
}

// VideoArgs returns the ffmpeg plan that implements a rendition. It lets the playout policy supply
// the already-detected host encoder without prepared importing the live playout package (which
// would create an import cycle).
type VideoArgs func(RenditionContract) (VideoPlan, error)

func NewFFmpegPackager(path string, videoArgs ...VideoArgs) *FFmpegPackager {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "ffmpeg"
	}
	args := softwareVideoArgs
	if len(videoArgs) > 0 && videoArgs[0] != nil {
		args = videoArgs[0]
	}
	return &FFmpegPackager{path: path, videoArgs: args}
}

func (p *FFmpegPackager) Package(
	ctx context.Context, workspace string, input Input, audioTrack int, rendition RenditionContract,
) (Output, error) {
	if p == nil {
		return Output{}, ErrPackagerUnavailable
	}
	args, err := ffmpegPackageArgsWith(workspace, input, audioTrack, rendition, p.videoArgs)
	if err != nil {
		return Output{}, err
	}
	cmd := exec.CommandContext(ctx, p.path, args...) //nolint:gosec // args are built from validated contracts
	stderr, pipeErr := cmd.StderrPipe()
	if pipeErr != nil {
		return Output{}, fmt.Errorf("prepared: ffmpeg stderr: %w", pipeErr)
	}
	cmd.Stdout = io.Discard
	run := p.diagnostics.Begin(diagnostics.ProcessSpec{
		Purpose: "prepared_package", Target: fmt.Sprintf("%s-%dx%d", rendition.VideoCodec, rendition.Width, rendition.Height),
		Executable: p.path, Args: diagnosticArgs(args, input.url),
	})
	if err := cmd.Start(); err != nil {
		if run != nil {
			run.Finish(diagnostics.ProcessResult{Err: err})
		}
		return Output{}, fmt.Errorf("prepared: ffmpeg package: %w", err)
	}
	var output boundedOutput
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			output.add(line)
			if run != nil {
				run.RecordOutput(line)
			}
		}
	}()
	err = cmd.Wait()
	<-drained
	if run != nil {
		run.Finish(diagnostics.ProcessResult{Err: err, Cancelled: ctx.Err() != nil, TerminationReason: cancellationReason(ctx)})
	}
	if err != nil {
		diagnostic := strings.ReplaceAll(commandDiagnostic(output.bytes()), input.url, "[input]")
		return Output{}, fmt.Errorf("prepared: ffmpeg package: %w: %s", err, diagnostic)
	}
	return collectPackagedOutput(workspace)
}

type boundedOutput struct {
	mu   sync.Mutex
	data []byte
}

func (b *boundedOutput) add(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, line...)
	b.data = append(b.data, '\n')
	const limit = 64 << 10
	if len(b.data) > limit {
		b.data = append([]byte(nil), b.data[len(b.data)-limit:]...)
	}
}

func (b *boundedOutput) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

func cancellationReason(ctx context.Context) string {
	if ctx.Err() != nil {
		return "context cancelled"
	}
	return ""
}

func ffmpegPackageArgs(workspace string, input Input, audioTrack int, r RenditionContract) ([]string, error) {
	return ffmpegPackageArgsWith(workspace, input, audioTrack, r, softwareVideoArgs)
}

func ffmpegPackageArgsWith(
	workspace string, input Input, audioTrack int, r RenditionContract, videoArgs VideoArgs,
) ([]string, error) {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(input.url) == "" || audioTrack < 0 ||
		r.Width <= 0 || r.Height <= 0 || r.FrameRate <= 0 || r.VideoBitrateKbps <= 0 ||
		r.AudioBitrateKbps <= 0 || r.SegmentDurationMS <= 0 || r.PackagingVersion != CurrentPackagingVersion ||
		videoArgs == nil {
		return nil, ErrUnsupportedRendition
	}
	video, err := videoArgs(r)
	if err != nil || len(video.OutputArgs) == 0 {
		if err == nil {
			err = ErrUnsupportedRendition
		}
		return nil, err
	}
	audioChannels := 2
	switch strings.ToLower(r.AudioLayout) {
	case "", "stereo":
	case "5.1":
		audioChannels = 6
	default:
		return nil, ErrUnsupportedRendition
	}
	if !strings.EqualFold(r.AudioCodec, "aac") {
		return nil, ErrUnsupportedRendition
	}
	segmentSeconds := float64(r.SegmentDurationMS) / 1000
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	args = append(args, video.InputArgs...)
	if input.http {
		args = append(args,
			"-reconnect", "1", "-reconnect_on_network_error", "1",
			"-reconnect_streamed", "1", "-multiple_requests", "1",
		)
	}
	args = append(args,
		"-probesize", "256k", "-analyzeduration", "500000",
		"-i", input.url, "-map", "0:v:0", "-map", fmt.Sprintf("0:a:%d", audioTrack),
	)
	args = append(args, video.OutputArgs...)
	args = append(args,
		"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", r.AudioBitrateKbps), "-ac", strconv.Itoa(audioChannels),
		"-f", "hls", "-hls_time", fmt.Sprintf("%.3f", segmentSeconds),
		"-hls_playlist_type", "vod", "-hls_flags", "independent_segments",
		"-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(workspace, "segment-%06d.m4s"),
		filepath.Join(workspace, MediaManifestName),
	)
	return args, nil
}

func diagnosticArgs(args []string, input string) []string {
	clean := append([]string(nil), args...)
	for i := range clean {
		if clean[i] == input {
			clean[i] = "[input]"
		}
	}
	return clean
}

func softwareVideoArgs(r RenditionContract) (VideoPlan, error) {
	videoEncoder := ""
	switch strings.ToLower(r.VideoCodec) {
	case "h264":
		videoEncoder = "libx264"
	case "hevc", "h265":
		videoEncoder = "libx265"
	default:
		return VideoPlan{}, ErrUnsupportedRendition
	}
	pixelFormat := strings.ToLower(r.PixelFormat)
	if pixelFormat == "" {
		pixelFormat = "yuv420p"
	}
	if pixelFormat != "yuv420p" && pixelFormat != "yuv420p10le" {
		return VideoPlan{}, ErrUnsupportedRendition
	}
	if r.HDR != "" && !strings.EqualFold(r.HDR, "sdr") {
		return VideoPlan{}, ErrUnsupportedRendition
	}

	segmentSeconds := float64(r.SegmentDurationMS) / 1000
	gop := max(1, r.FrameRate*r.SegmentDurationMS/1000)
	filter := fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1",
		r.Width, r.Height, r.Width, r.Height,
	)
	args := []string{"-vf", filter, "-c:v", videoEncoder}
	if r.VideoProfile != "" {
		args = append(args, "-profile:v", r.VideoProfile)
	}
	if r.VideoLevel != "" {
		args = append(args, "-level:v", r.VideoLevel)
	}
	args = append(args,
		"-pix_fmt", pixelFormat,
		"-r", strconv.Itoa(r.FrameRate),
		"-b:v", fmt.Sprintf("%dk", r.VideoBitrateKbps),
		"-maxrate", fmt.Sprintf("%dk", r.VideoBitrateKbps*2),
		"-bufsize", fmt.Sprintf("%dk", r.VideoBitrateKbps*2),
		"-g", strconv.Itoa(gop), "-keyint_min", strconv.Itoa(gop),
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%.3f)", segmentSeconds),
	)
	if videoEncoder == "libx265" {
		args = append(args, "-x265-params", fmt.Sprintf("keyint=%d:min-keyint=%d:scenecut=0", gop, gop), "-tag:v", "hvc1")
	} else {
		args = append(args, "-sc_threshold", "0")
	}
	return VideoPlan{OutputArgs: args}, nil
}

func collectPackagedOutput(workspace string) (Output, error) {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return Output{}, fmt.Errorf("prepared: read packaged output: %w", err)
	}
	files := make([]string, 0, len(entries))
	hasManifest, hasInit, segments := false, false, 0
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		switch {
		case name == MediaManifestName:
			hasManifest = true
		case name == "init.mp4":
			hasInit = true
		case strings.HasPrefix(name, "segment-") && strings.HasSuffix(name, ".m4s"):
			segments++
		default:
			continue
		}
		files = append(files, name)
	}
	if !hasManifest || !hasInit || segments == 0 {
		return Output{}, ErrIncomplete
	}
	slices.Sort(files)
	return Output{Files: files}, nil
}

func commandDiagnostic(output []byte) string {
	const limit = 4096
	if len(output) > limit {
		output = output[len(output)-limit:]
	}
	return strings.TrimSpace(string(output))
}
