package mediatools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const DerivativeRecipeVersion = 1

type DerivativeRole string

const (
	DerivativeEvidence DerivativeRole = "evidence"
	DerivativePlayback DerivativeRole = "playback"
)

// DerivativeRecipe is the complete semantic identity of one media rewrite. It deliberately has
// no width, height, frame-rate, deinterlace, crop, denoise, sharpen, or colour fields: the shipped
// recipes preserve those source properties. Adding one is a new reviewed recipe, not a hidden arg.
type DerivativeRecipe struct {
	Version           int            `json:"version"`
	ID                string         `json:"id"`
	Role              DerivativeRole `json:"role"`
	Container         string         `json:"container"`
	VideoCodec        string         `json:"videoCodec"`
	VideoEncoder      string         `json:"videoEncoder"`
	CRF               int            `json:"crf"`
	Preset            string         `json:"preset"`
	PixelFormat       string         `json:"pixelFormat"`
	AudioCodec        string         `json:"audioCodec"`
	AudioEncoder      string         `json:"audioEncoder"`
	AudioKbps         int            `json:"audioKbps"`
	AudioRateHz       int            `json:"audioRateHz"`
	AudioChannels     int            `json:"audioChannels"`
	KeyframeSeconds   int            `json:"keyframeSeconds"`
	TargetLUFS        float64        `json:"targetLufs"`
	SelectFirstAV     bool           `json:"selectFirstAv"`
	NormalizeTimebase bool           `json:"normalizeTimebase"`
	StripMetadata     bool           `json:"stripMetadata"`
	FastStart         bool           `json:"fastStart"`
	PreserveGeometry  bool           `json:"preserveGeometry"`
	PreserveCadence   bool           `json:"preserveCadence"`
}

func EvidenceDerivativeRecipe() DerivativeRecipe {
	return DerivativeRecipe{
		Version: DerivativeRecipeVersion, ID: "filler-evidence-v1", Role: DerivativeEvidence,
		Container: "mp4", VideoCodec: "h264", VideoEncoder: "libx264", CRF: 14, Preset: "slow", PixelFormat: "yuv420p",
		AudioCodec: "aac", AudioEncoder: "aac", AudioKbps: 256, AudioRateHz: 48_000, AudioChannels: 2,
		KeyframeSeconds: 1, SelectFirstAV: true, NormalizeTimebase: true, StripMetadata: true,
		FastStart: true, PreserveGeometry: true, PreserveCadence: true,
	}
}

func PlaybackDerivativeRecipe(profile MezzanineProfile, targetLUFS float64) DerivativeRecipe {
	return DerivativeRecipe{
		Version: DerivativeRecipeVersion, ID: "filler-playback-v1", Role: DerivativePlayback,
		Container: "mp4", VideoCodec: profile.VideoCodec, VideoEncoder: "libx264", CRF: profile.CRF,
		Preset: profile.Preset, PixelFormat: profile.PixelFormat,
		AudioCodec: profile.AudioCodec, AudioEncoder: profile.AudioCodec, AudioKbps: profile.AudioKbps,
		AudioRateHz: profile.AudioRateHz, AudioChannels: profile.AudioCh,
		KeyframeSeconds: profile.KeyframeSeconds, TargetLUFS: targetLUFS,
		SelectFirstAV: true, NormalizeTimebase: true, StripMetadata: true,
		FastStart: true, PreserveGeometry: true, PreserveCadence: true,
	}
}

func (r DerivativeRecipe) Validate() error {
	if r.Version != DerivativeRecipeVersion || r.ID == "" || r.Container != "mp4" {
		return errors.New("derivative recipe requires the supported version, id, and MP4 container")
	}
	if r.Role != DerivativeEvidence && r.Role != DerivativePlayback {
		return fmt.Errorf("derivative recipe role %q is unsupported", r.Role)
	}
	if r.VideoCodec != "h264" || r.VideoEncoder != "libx264" || r.CRF < 0 || r.CRF > 51 || r.Preset == "" || r.PixelFormat != "yuv420p" {
		return errors.New("derivative video recipe is invalid")
	}
	if r.AudioCodec != "aac" || r.AudioEncoder != "aac" || r.AudioKbps <= 0 || r.AudioRateHz != 48_000 || r.AudioChannels != 2 {
		return errors.New("derivative audio recipe is invalid")
	}
	if r.KeyframeSeconds <= 0 || !r.SelectFirstAV || !r.NormalizeTimebase || !r.StripMetadata || !r.FastStart || !r.PreserveGeometry || !r.PreserveCadence {
		return errors.New("derivative compatibility and preservation policy is incomplete")
	}
	if r.Role == DerivativeEvidence && r.TargetLUFS != 0 {
		return errors.New("evidence derivative must not apply playout loudness")
	}
	if math.IsNaN(r.TargetLUFS) || math.IsInf(r.TargetLUFS, 0) || r.TargetLUFS > 0 || r.TargetLUFS < -70 {
		return errors.New("derivative loudness target is invalid")
	}
	return nil
}

func (r DerivativeRecipe) Digest() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("encode derivative recipe: %w", err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func (r DerivativeRecipe) Profile() MezzanineProfile {
	return MezzanineProfile{
		VideoCodec: r.VideoCodec, CRF: r.CRF, Preset: r.Preset, PixelFormat: r.PixelFormat,
		AudioCodec: r.AudioCodec, AudioKbps: r.AudioKbps, AudioRateHz: r.AudioRateHz,
		AudioCh: r.AudioChannels, KeyframeSeconds: r.KeyframeSeconds,
	}
}

// MediaToolIdentity binds a recipe execution to the actual executable, not merely the configured
// word "ffmpeg". ExecutableSHA256 distinguishes two locally patched builds with the same banner.
type MediaToolIdentity struct {
	Name             string `json:"name"`
	Version          string `json:"version"`
	ExecutableSHA256 string `json:"executableSha256"`
}

func (t MediaToolIdentity) Validate() error {
	if t.Name != "ffmpeg" || t.Version == "" || len(t.ExecutableSHA256) != 64 ||
		strings.Trim(t.ExecutableSHA256, "0123456789abcdef") != "" {
		return errors.New("media tool identity is incomplete")
	}
	return nil
}

// IdentifyFFmpeg records both the banner and complete executable digest. The path itself is not
// portable provenance and is intentionally omitted from the returned identity.
func IdentifyFFmpeg(ctx context.Context, configured string) (MediaToolIdentity, error) {
	path, err := exec.LookPath(FFmpegOr(configured))
	if err != nil {
		return MediaToolIdentity{}, fmt.Errorf("identify ffmpeg: %w", err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return MediaToolIdentity{}, fmt.Errorf("identify ffmpeg symlink: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return MediaToolIdentity{}, fmt.Errorf("identify ffmpeg executable: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > ConditioningMaxSnapshotBytes {
		return MediaToolIdentity{}, errors.New("identify ffmpeg: executable is not a bounded regular file")
	}
	digest := sha256.New()
	buffer := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return MediaToolIdentity{}, err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			_, _ = digest.Write(buffer[:n])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return MediaToolIdentity{}, fmt.Errorf("identify ffmpeg digest: %w", readErr)
		}
	}
	versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := runDerivativeCommand(versionCtx, path, false, "-version")
	if err != nil {
		return MediaToolIdentity{}, fmt.Errorf("identify ffmpeg version: %w", err)
	}
	if len(output) > 256<<10 {
		return MediaToolIdentity{}, errors.New("identify ffmpeg version: output exceeds 256 KiB")
	}
	version, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
	identity := MediaToolIdentity{Name: "ffmpeg", Version: version, ExecutableSHA256: hex.EncodeToString(digest.Sum(nil))}
	if err := identity.Validate(); err != nil {
		return MediaToolIdentity{}, err
	}
	return identity, nil
}
