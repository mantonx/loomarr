package playout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/diagnostics"
)

const (
	capabilityEvidenceVersion = 1
	capabilityEvidenceName    = ".host-capability-v1.json"
	capabilityEvidenceMaxAge  = 7 * 24 * time.Hour
	capabilityValidationSecs  = 1
	capabilityIdentityTimeout = 250 * time.Millisecond
)

type capabilityEvidence struct {
	Version     int       `json:"version"`
	Fingerprint string    `json:"fingerprint"`
	Encoder     Encoder   `json:"encoder"`
	MaxChannels int       `json:"maxChannels"`
	ObservedAt  time.Time `json:"observedAt"`
}

type capabilityEvidenceDependencies struct {
	now         func() time.Time
	fingerprint func(context.Context, string, string, Profile) (string, error)
	detect      func(context.Context, string, Profile, string) Capacity
	validate    func(context.Context, string, Encoder, Profile) Capability
}

// DetectObservedWithEvidence reuses a persisted hardware measurement only after a short real
// encoder trial proves it still emits keyframe-bearing MPEG-TS on the current FFmpeg/GPU host.
// reused distinguishes that bounded restart validation from a full candidate/capacity benchmark.
func DetectObservedWithEvidence(
	ctx context.Context, ffmpegPath string, profile Profile, gpu, root string,
	manager *diagnostics.ProcessManager,
) (capacity Capacity, reused bool) {
	return detectObservedWithEvidence(ctx, ffmpegPath, profile, gpu, root, capabilityEvidenceDependencies{
		now:         time.Now,
		fingerprint: hostCapabilityFingerprint,
		detect: func(ctx context.Context, ffmpeg string, profile Profile, gpu string) Capacity {
			return DetectObserved(ctx, ffmpeg, profile, gpu, manager)
		},
		validate: func(ctx context.Context, ffmpeg string, encoder Encoder, profile Profile) Capability {
			return trialEncodeObserved(ctx, ffmpeg, encoder, profile, capabilityValidationSecs, manager)
		},
	})
}

// LoadMatchingObservedCapabilityEvidence performs only the cheap restart identity check. It never
// launches an encoder trial or the full candidate/capacity benchmark. Callers may therefore use a
// still-fresh measurement immediately when the FFmpeg/GPU/profile fingerprint is unchanged, while
// DetectObservedWithEvidence revalidates that measurement asynchronously.
func LoadMatchingObservedCapabilityEvidence(
	ctx context.Context, ffmpegPath string, profile Profile, gpu, root string,
) (Capacity, bool) {
	capacity, _, ok := matchingCapabilityEvidence(ctx, ffmpegPath, profile, gpu, root, capabilityEvidenceDependencies{
		now:         time.Now,
		fingerprint: hostCapabilityFingerprint,
	})
	return capacity, ok
}

func detectObservedWithEvidence(
	ctx context.Context, ffmpegPath string, profile Profile, gpu, root string,
	deps capabilityEvidenceDependencies,
) (Capacity, bool) {
	if deps.now == nil {
		deps.now = time.Now
	}
	validated, fingerprint, ok := validatedCapabilityEvidence(ctx, ffmpegPath, profile, gpu, root, deps)
	if ok {
		return validated, true
	}

	capacity := deps.detect(ctx, ffmpegPath, profile, gpu)
	if fingerprint != "" && !IsSoftwareEncoder(capacity.Chosen) && capacity.MaxChannels > 0 {
		_ = storeCapabilityEvidence(root, capabilityEvidence{
			Version: capabilityEvidenceVersion, Fingerprint: fingerprint,
			Encoder: capacity.Chosen, MaxChannels: capacity.MaxChannels, ObservedAt: deps.now().UTC(),
		})
	}
	return capacity, false
}

func validatedCapabilityEvidence(
	ctx context.Context, ffmpegPath string, profile Profile, gpu, root string,
	deps capabilityEvidenceDependencies,
) (Capacity, string, bool) {
	capacity, fingerprint, ok := matchingCapabilityEvidence(ctx, ffmpegPath, profile, gpu, root, deps)
	if !ok || deps.validate == nil {
		return Capacity{}, fingerprint, false
	}
	validated := deps.validate(ctx, ffmpegPath, capacity.Chosen, profile)
	if !validated.Works {
		return Capacity{}, fingerprint, false
	}
	return capacity, fingerprint, true
}

func matchingCapabilityEvidence(
	ctx context.Context, ffmpegPath string, profile Profile, gpu, root string,
	deps capabilityEvidenceDependencies,
) (Capacity, string, bool) {
	if deps.now == nil {
		deps.now = time.Now
	}
	fingerprint, err := deps.fingerprint(ctx, ffmpegPath, gpu, profile)
	if err != nil {
		return Capacity{}, "", false
	}
	evidence, ok := loadCapabilityEvidence(root, fingerprint, deps.now())
	if !ok {
		return Capacity{}, fingerprint, false
	}
	return Capacity{Chosen: evidence.Encoder, MaxChannels: evidence.MaxChannels}, fingerprint, true
}

func loadCapabilityEvidence(root, fingerprint string, now time.Time) (capabilityEvidence, bool) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(fingerprint) == "" {
		return capabilityEvidence{}, false
	}
	body, err := os.ReadFile(filepath.Join(root, capabilityEvidenceName))
	if err != nil {
		return capabilityEvidence{}, false
	}
	var evidence capabilityEvidence
	if err := json.Unmarshal(body, &evidence); err != nil || evidence.Version != capabilityEvidenceVersion ||
		evidence.Fingerprint != fingerprint || evidence.ObservedAt.IsZero() || now.Before(evidence.ObservedAt) ||
		now.Sub(evidence.ObservedAt) > capabilityEvidenceMaxAge || evidence.MaxChannels <= 0 ||
		IsSoftwareEncoder(evidence.Encoder) || !supportedHardwareEncoder(evidence.Encoder) {
		return capabilityEvidence{}, false
	}
	return evidence, true
}

func storeCapabilityEvidence(root string, evidence capabilityEvidence) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("playout: empty capability evidence root")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("playout: create capability evidence root: %w", err)
	}
	body, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("playout: encode capability evidence: %w", err)
	}
	tmp, err := os.CreateTemp(root, ".host-capability-*")
	if err != nil {
		return fmt.Errorf("playout: create capability evidence: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filepath.Join(root, capabilityEvidenceName)); err != nil {
		return fmt.Errorf("playout: publish capability evidence: %w", err)
	}
	committed = true
	directory, err := os.Open(root)
	if err != nil {
		return fmt.Errorf("playout: open capability evidence root: %w", err)
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("playout: sync capability evidence root: %w", err)
	}
	return nil
}

func supportedHardwareEncoder(encoder Encoder) bool {
	for _, candidate := range h264Engines {
		if candidate != EncoderSoftware && candidate == encoder {
			return true
		}
	}
	return false
}

func hostCapabilityFingerprint(
	ctx context.Context, ffmpegPath, gpu string, profile Profile,
) (string, error) {
	if strings.TrimSpace(ffmpegPath) == "" {
		ffmpegPath = "ffmpeg"
	}
	resolved, err := exec.LookPath(ffmpegPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	// This command runs on the first live fallback after restart. A wedged executable must turn
	// evidence reuse into a quick miss, never become the cold-start latency itself.
	versionCtx, cancel := context.WithTimeout(ctx, capabilityIdentityTimeout)
	defer cancel()
	version, err := exec.CommandContext(versionCtx, resolved, "-version").Output()
	if err != nil {
		return "", err
	}
	if len(version) > 64<<10 {
		version = version[:64<<10]
	}
	digest := sha256.New()
	_, _ = fmt.Fprintf(digest, "%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%s\x00%d\x00%d\x00%d\x00%d\x00%d\x00",
		runtime.GOOS, runtime.GOARCH, resolved, info.Size(), info.ModTime().UnixNano(), info.Mode(),
		strings.TrimSpace(gpu), profile.Width, profile.Height, profile.Framerate,
		profile.VideoBitrate, profile.AudioBitrate)
	_, _ = digest.Write(version)
	return hex.EncodeToString(digest.Sum(nil)), nil
}
