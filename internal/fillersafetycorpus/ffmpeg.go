package fillersafetycorpus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

const (
	maximumToolBytes       = 512 << 20
	maximumToolOutputBytes = 64 << 10
)

type ffmpegWrapper struct {
	ffmpegPath, ffprobePath string
	ffmpeg, ffprobe         string
	recipe                  string
}

func (wrapper *ffmpegWrapper) Identity(ctx context.Context) (fillersafety.ToolIdentity, fillersafety.ToolIdentity, string, error) {
	ffmpeg, err := resolveTool(wrapper.ffmpegPath)
	if err != nil {
		return fillersafety.ToolIdentity{}, fillersafety.ToolIdentity{}, "", err
	}
	ffprobe, err := resolveTool(wrapper.ffprobePath)
	if err != nil {
		return fillersafety.ToolIdentity{}, fillersafety.ToolIdentity{}, "", err
	}
	ffmpegIdentity, err := identifyTool(ctx, ffmpeg)
	if err != nil {
		return fillersafety.ToolIdentity{}, fillersafety.ToolIdentity{}, "", err
	}
	ffprobeIdentity, err := identifyTool(ctx, ffprobe)
	if err != nil {
		return fillersafety.ToolIdentity{}, fillersafety.ToolIdentity{}, "", err
	}
	wrapper.ffmpeg, wrapper.ffprobe = ffmpeg, ffprobe
	recipe := wrapper.recipe
	if recipe == "" {
		recipe = VCTKNeutralVideoRecipe
	}
	return ffmpegIdentity, ffprobeIdentity, hashBytes([]byte(recipe)), nil
}

func (wrapper *ffmpegWrapper) Wrap(ctx context.Context, input, output string) (wrappedMedia, error) {
	if wrapper.ffmpeg == "" || wrapper.ffprobe == "" {
		return wrappedMedia{}, fmt.Errorf("spoken-safety media wrapper is not identified")
	}
	args := []string{
		"-nostdin", "-hide_banner", "-nostats", "-v", "error", "-y",
		"-f", "lavfi", "-i", "color=c=0x202830:s=640x360:r=30",
		"-i", input, "-map", "0:v:0", "-map", "1:a:0", "-shortest",
		"-map_metadata", "-1", "-map_chapters", "-1", "-sn", "-dn",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "28", "-pix_fmt", "yuv420p",
		"-g", "30", "-keyint_min", "30", "-sc_threshold", "0",
		"-c:a", "aac", "-b:a", "96k", "-ar", "48000", "-ac", "1", "-threads", "1",
		"-fflags", "+bitexact", "-flags:v", "+bitexact", "-flags:a", "+bitexact",
		"-movflags", "+faststart", output,
	}
	command := exec.CommandContext(ctx, wrapper.ffmpeg, args...) //nolint:gosec // executable and arguments are validated private inputs
	stdout := &boundedBuffer{remaining: maximumToolOutputBytes}
	stderr := &boundedBuffer{remaining: maximumToolOutputBytes}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil || stdout.overflow || stderr.overflow {
		return wrappedMedia{}, fmt.Errorf("spoken-safety ffmpeg wrapping failed")
	}
	durationMS, err := probeCompleteAV(ctx, wrapper.ffprobe, output)
	if err != nil {
		return wrappedMedia{}, err
	}
	if err := decodeCompleteAV(ctx, wrapper.ffmpeg, output); err != nil {
		return wrappedMedia{}, err
	}
	info, err := os.Lstat(output)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return wrappedMedia{}, fmt.Errorf("spoken-safety wrapper output is invalid")
	}
	digest, bytes, err := hashRegularFile(output, maximumToolBytes)
	if err != nil || bytes != info.Size() {
		return wrappedMedia{}, fmt.Errorf("spoken-safety wrapper output cannot be bound")
	}
	return wrappedMedia{SHA256: digest, Bytes: bytes, DurationMS: durationMS}, nil
}

func resolveTool(value string) (string, error) {
	path, err := exec.LookPath(value)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("media tool must resolve to a regular file")
	}
	return path, nil
}

func identifyTool(ctx context.Context, path string) (fillersafety.ToolIdentity, error) {
	digest, _, err := hashRegularFile(path, maximumToolBytes)
	if err != nil {
		return fillersafety.ToolIdentity{}, err
	}
	var stdout, stderr boundedBuffer
	stdout.remaining, stderr.remaining = maximumToolOutputBytes, maximumToolOutputBytes
	command := exec.CommandContext(ctx, path, "-version") //nolint:gosec // resolved regular executable chosen explicitly by operator
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil || stdout.overflow || stderr.overflow {
		return fillersafety.ToolIdentity{}, fmt.Errorf("media tool version probe failed")
	}
	fields := strings.Fields(strings.SplitN(stdout.String(), "\n", 2)[0])
	if len(fields) < 3 {
		return fillersafety.ToolIdentity{}, fmt.Errorf("media tool version is malformed")
	}
	version := strings.Join(fields[:3], " ")
	if len(version) > 128 {
		return fillersafety.ToolIdentity{}, fmt.Errorf("media tool version is too long")
	}
	return fillersafety.ToolIdentity{Version: version, BinarySHA256: digest}, nil
}

func hashRegularFile(path string, maximum int64) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return "", 0, fmt.Errorf("bounded regular file is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", 0, fmt.Errorf("file identity changed while opening")
	}
	hash := sha256.New()
	bytes, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil || bytes <= 0 || bytes > maximum {
		return "", 0, fmt.Errorf("hash bounded regular file")
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), bytes, nil
}

func probeCompleteAV(ctx context.Context, ffprobe, path string) (int64, error) {
	command := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration:stream=codec_type", "-of", "json", path) //nolint:gosec // resolved executable and private output path
	var stdout, stderr boundedBuffer
	stdout.remaining, stderr.remaining = maximumToolOutputBytes, maximumToolOutputBytes
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil || stdout.overflow || stderr.overflow {
		return 0, fmt.Errorf("spoken-safety ffprobe validation failed")
	}
	var result struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return 0, fmt.Errorf("VCTK ffprobe output is malformed")
	}
	hasAudio, hasVideo := false, false
	for _, stream := range result.Streams {
		hasAudio = hasAudio || stream.CodecType == "audio"
		hasVideo = hasVideo || stream.CodecType == "video"
	}
	durationMS, err := decimalSecondsToMS(result.Format.Duration)
	if err != nil || !hasAudio || !hasVideo || durationMS <= 0 {
		return 0, fmt.Errorf("spoken-safety wrapped source lacks complete audio, video, or duration")
	}
	return durationMS, nil
}

func decodeCompleteAV(ctx context.Context, ffmpeg, path string) error {
	command := exec.CommandContext(ctx, ffmpeg,
		"-nostdin", "-hide_banner", "-nostats", "-v", "error", "-xerror", "-i", path,
		"-map", "0:v:0", "-map", "0:a:0", "-f", "null", "-") //nolint:gosec // resolved executable and private output path
	stdout := &boundedBuffer{remaining: maximumToolOutputBytes}
	stderr := &boundedBuffer{remaining: maximumToolOutputBytes}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil || stdout.overflow || stderr.overflow {
		return fmt.Errorf("spoken-safety wrapped source does not fully decode")
	}
	return nil
}

func decimalSecondsToMS(value string) (int64, error) {
	seconds, ok := new(big.Rat).SetString(value)
	if !ok || seconds.Sign() <= 0 {
		return 0, fmt.Errorf("invalid media duration")
	}
	milliseconds := new(big.Rat).Mul(seconds, big.NewRat(1000, 1))
	quotient := new(big.Int).Quo(milliseconds.Num(), milliseconds.Denom())
	remainder := new(big.Int).Rem(milliseconds.Num(), milliseconds.Denom())
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(milliseconds.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("media duration exceeds integer range")
	}
	return quotient.Int64(), nil
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	overflow  bool
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	if len(value) > buffer.remaining {
		value = value[:buffer.remaining]
		buffer.overflow = true
	}
	if len(value) != 0 {
		_, _ = buffer.buffer.Write(value)
		buffer.remaining -= len(value)
	}
	return written, nil
}

func (buffer *boundedBuffer) Bytes() []byte  { return buffer.buffer.Bytes() }
func (buffer *boundedBuffer) String() string { return buffer.buffer.String() }
