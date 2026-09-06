package fillervisualsafety

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/mediatools"
)

const MaximumVisualDecodeWallTime = time.Hour

// DecodedFrameConsumer receives one immutable-for-the-call RGB24 frame. It
// must finish all work with frameBytes before returning and must not retain it.
type DecodedFrameConsumer func(ctx context.Context, frame FrameEvidence, frameBytes []byte) error

// DecodeCoverage performs one complete source decode and streams only the
// planned RGB24 observations to consume. A successful result proves both EOF
// and exact agreement with the plan; partial evidence is never returned.
func DecodeCoverage(ctx context.Context, source *PreparedSource, ffmpegPath string, consume DecodedFrameConsumer) (CoverageEvidence, error) {
	if ctx == nil || ctx.Err() != nil || source == nil || source.snapshot == nil || consume == nil ||
		source.SnapshotPath == "" || source.SnapshotPath != source.snapshot.Path() ||
		ValidateSourceAuthority(source.Authority) != nil || ValidateCoveragePlan(source.Plan) != nil ||
		source.Plan.SourceAuthoritySHA256 != source.Authority.SHA256 ||
		source.Plan.SourceSHA256 != source.Authority.SourceSHA256 || source.Plan.DurationMS != source.Authority.DurationMS ||
		source.Plan.Video != source.Authority.Video {
		return CoverageEvidence{}, errors.New("visual-safety decoder input is invalid")
	}
	if err := verifyPreparedSnapshot(ctx, source); err != nil {
		return CoverageEvidence{}, err
	}
	resolved, decoder, err := resolveVisualDecoder(ctx, ffmpegPath)
	if err != nil {
		return CoverageEvidence{}, err
	}
	frameBytes, err := rgb24FrameBytes(source.Plan.Video.Width, source.Plan.Video.Height)
	if err != nil {
		return CoverageEvidence{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, MaximumVisualDecodeWallTime)
	defer cancel()
	cmd := exec.CommandContext(runCtx, resolved, visualDecodeArgs(source)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return CoverageEvidence{}, fmt.Errorf("visual-safety decoder stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return CoverageEvidence{}, fmt.Errorf("visual-safety decoder stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return CoverageEvidence{}, fmt.Errorf("visual-safety decoder start: %w", err)
	}

	timestamps := newDecoderTimestampQueue(len(source.Plan.Points) + 1)
	diagnostics := &decoderDiagnostics{}
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanDecoderTimestamps(stderr, timestamps, diagnostics)
	}()

	frames, decodeErr := consumeDecodedFrames(runCtx, stdout, timestamps, source.Plan, frameBytes, consume)
	if decodeErr != nil {
		cancel()
	}
	<-scanDone
	waitErr := cmd.Wait()
	if decodeErr != nil {
		return CoverageEvidence{}, decodeErr
	}
	if waitErr != nil {
		return CoverageEvidence{}, decoderFailure(runCtx, waitErr, diagnostics.String())
	}
	count, timestampErr := timestamps.result()
	if timestampErr != nil {
		return CoverageEvidence{}, fmt.Errorf("visual-safety decoder timestamp coverage is incomplete: %w", timestampErr)
	}
	if count != len(source.Plan.Points) {
		return CoverageEvidence{}, fmt.Errorf("visual-safety decoder timestamp coverage is incomplete: got %d, want %d",
			count, len(source.Plan.Points))
	}
	if err := verifyPreparedSnapshot(ctx, source); err != nil {
		return CoverageEvidence{}, err
	}
	_, after, err := resolveVisualDecoder(ctx, resolved)
	if err != nil || after != decoder {
		return CoverageEvidence{}, errors.New("visual-safety decoder identity drifted")
	}
	evidence, err := SealCoverageEvidence(source.Plan, decoder, frames, true)
	if err != nil {
		return CoverageEvidence{}, err
	}
	return evidence, nil
}

func consumeDecodedFrames(ctx context.Context, stdout io.Reader, timestamps *decoderTimestampQueue, plan CoveragePlan, frameBytes int, consume DecodedFrameConsumer) ([]FrameEvidence, error) {
	frames := make([]FrameEvidence, 0, len(plan.Points))
	for index, point := range plan.Points {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw := make([]byte, frameBytes)
		if _, err := io.ReadFull(stdout, raw); err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			return nil, errors.New("visual-safety decoder frame coverage is incomplete")
		}
		observedMS, err := timestamps.at(index)
		if err != nil {
			return nil, errors.New("visual-safety decoder timestamp coverage is incomplete")
		}
		digest := sha256.Sum256(raw)
		frame := FrameEvidence{
			Ordinal: point.Ordinal, RequestedMS: point.RequestedMS, ObservedMS: observedMS,
			SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(raw)), Width: plan.Video.Width, Height: plan.Video.Height,
		}
		if !validFrame(plan, index, frame) {
			return nil, errors.New("visual-safety decoder frame does not match its plan")
		}
		if err := consume(ctx, frame, raw); err != nil {
			return nil, fmt.Errorf("visual-safety frame consumer: %w", err)
		}
		if after := sha256.Sum256(raw); after != digest {
			return nil, errors.New("visual-safety frame consumer mutated decoded evidence")
		}
		frames = append(frames, frame)
	}
	var extra [1]byte
	if count, err := stdout.Read(extra[:]); count != 0 || !errors.Is(err, io.EOF) {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, errors.New("visual-safety decoder emitted an unexpected frame")
	}
	return frames, nil
}

func visualDecodeArgs(source *PreparedSource) []string {
	video := source.Plan.Video
	// The plan may collapse a regular grid point whose drift window overlaps a
	// distinct terminal frame. Stop the cadence term after the planned regular
	// points so FFmpeg cannot recreate that deliberately omitted grid point.
	// Selection uses the same rounded-millisecond timeline as showinfo parsing;
	// comparing fractional seconds to a rounded authority timestamp can drop an
	// exact terminal frame such as 90.990991s == 90,991ms.
	regularPoints := len(source.Plan.Points) - 1
	filter := fmt.Sprintf("select='isnan(prev_selected_t)+lt(selected_n\\,%d)*gte(round(t*1000)\\,%d+selected_n*%d)+gte(round(t*1000)\\,%d)',format=pix_fmts=rgb24,showinfo",
		regularPoints, video.FirstFrameMS, source.Plan.Profile.ObservationIntervalMS, video.LastFrameMS)
	return []string{
		"-nostdin", "-hide_banner", "-loglevel", "info", "-xerror", "-err_detect", "explode",
		"-max_error_rate", "0", "-copyts", "-noautorotate", "-threads:v", "1", "-i", source.SnapshotPath,
		"-map", "0:" + strconv.Itoa(video.Index), "-an", "-sn", "-dn",
		"-vf", filter, "-fps_mode", "passthrough", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1",
	}
}

func rgb24FrameBytes(width, height int) (int, error) {
	if width <= 0 || height <= 0 || int64(width) > math.MaxInt64/int64(height) {
		return 0, errors.New("visual-safety decoder frame geometry exceeds its bounds")
	}
	pixels := int64(width) * int64(height)
	if pixels > math.MaxInt64/3 {
		return 0, errors.New("visual-safety decoder frame geometry exceeds its bounds")
	}
	bytes := pixels * 3
	if bytes > MaximumFrameBytes || !validRGB24Bytes(width, height, bytes) {
		return 0, errors.New("visual-safety decoder frame geometry exceeds its bounds")
	}
	return int(bytes), nil
}

func resolveVisualDecoder(ctx context.Context, configured string) (string, ToolIdentity, error) {
	path, err := exec.LookPath(mediatools.FFmpegOr(configured))
	if err != nil {
		return "", ToolIdentity{}, fmt.Errorf("visual-safety decoder lookup: %w", err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", ToolIdentity{}, fmt.Errorf("visual-safety decoder symlink: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil || filepath.Clean(path) != path {
		return "", ToolIdentity{}, errors.New("visual-safety decoder path is invalid")
	}
	identity, err := mediatools.IdentifyFFmpeg(ctx, path)
	if err != nil {
		return "", ToolIdentity{}, err
	}
	return path, ToolIdentity{
		Name: identity.Name, Version: identity.Version, ExecutableSHA256: identity.ExecutableSHA256,
	}, nil
}

func verifyPreparedSnapshot(ctx context.Context, source *PreparedSource) error {
	info, err := os.Lstat(source.SnapshotPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() != source.Authority.SourceBytes {
		return errors.New("visual-safety prepared source identity drifted")
	}
	file, err := os.Open(source.SnapshotPath) //nolint:gosec // private path created and retained by Prepare
	if err != nil {
		return errors.New("visual-safety prepared source could not be opened")
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	buffer := make([]byte, 128<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			total += int64(count)
			if total > source.Authority.SourceBytes {
				return errors.New("visual-safety prepared source exceeds its authority")
			}
			_, _ = digest.Write(buffer[:count])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return errors.New("visual-safety prepared source could not be read")
		}
	}
	if total != source.Authority.SourceBytes || hex.EncodeToString(digest.Sum(nil)) != source.Authority.SourceSHA256 {
		return errors.New("visual-safety prepared source bytes drifted")
	}
	return nil
}

func decoderFailure(ctx context.Context, processErr error, diagnostics string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if diagnostics == "" {
		return fmt.Errorf("visual-safety decoder failed: %w", processErr)
	}
	lines := strings.Split(diagnostics, "\n")
	return fmt.Errorf("visual-safety decoder failed: %w: %s", processErr, lines[len(lines)-1])
}
