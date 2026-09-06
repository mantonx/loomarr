package fillervisualsafety_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

const (
	portableWorkerPathEnv       = "LOOMARR_VISUAL_WORKER"
	portableWorkerCapabilityEnv = "LOOMARR_VISUAL_CAPABILITY"
)

// TestRunPortableCoverageWithConfiguredWorker is an explicit local diagnostic.
// The normal suite skips it; it never fetches a model or grants safety authority.
func TestRunPortableCoverageWithConfiguredWorker(t *testing.T) {
	worker := os.Getenv(portableWorkerPathEnv)
	capabilityPath := os.Getenv(portableWorkerCapabilityEnv)
	if worker == "" || capabilityPath == "" {
		t.Skip("portable visual worker diagnostic is not configured")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	capability := readPortableCapability(t, capabilityPath)

	path := t.TempDir() + "/generated-transport-control.mkv"
	generate := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=10:duration=3", "-an", "-c:v", "ffv1", "-y", path)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate transport control: %v: %s", err, output)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	authority, err := fillervisualsafety.SealSourceAuthority(fillervisualsafety.SourceAuthority{
		SourceID: "generated-transport-control", SourceSHA256: fmt.Sprintf("%x", digest), SourceBytes: int64(len(raw)),
		DurationMS: 3_000, PolicySHA256: repeatedDigest("7"), Implementation: "portable-worker-real-diagnostic-v1",
		Video: fillervisualsafety.VideoStreamIdentity{
			Index: 0, Codec: "ffv1", Width: 640, Height: 360, FirstFrameMS: 0, LastFrameMS: 2_900,
			FrameRateNumerator: 10, FrameRateDenominator: 1, TimeBaseNumerator: 1, TimeBaseDenominator: 1_000,
			DurationMS: 3_000,
		},
		Probe: fillervisualsafety.ToolIdentity{
			Name: "ffprobe", Version: "generated-control-fixture", ExecutableSHA256: repeatedDigest("8"),
		},
		MeasuredAt: time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := fillervisualsafety.SealCoverageProfile(fillervisualsafety.CoverageProfile{
		Implementation: "portable-worker-real-diagnostic-1s-v1", MaximumSourceDurationMS: 3_000,
		ObservationIntervalMS: 1_000, MaximumTimestampDriftMS: 1,
		MaximumObservations: 4, MinimumCoveredExposureMS: 1_003,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := fillervisualsafety.Prepare(context.Background(), fillervisualsafety.SourceRequest{
		Authority: authority, Path: path,
	}, profile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()

	execution, err := fillervisualsafety.RunPortableCoverage(context.Background(), prepared, ffmpeg, worker, capability)
	if err != nil {
		t.Fatalf("RunPortableCoverage(real worker) error = %v", err)
	}
	t.Logf("capability=%s coverage=%s inference=%s", capability.SHA256, execution.Coverage.SHA256, execution.Inference.SHA256)
	for index, response := range execution.Inference.Responses {
		t.Logf("frame=%d observed_ms=%d elapsed_ms=%d logits=%v", index,
			execution.Coverage.Frames[index].ObservedMS, response.ElapsedMS, response.Models[0].Logits)
	}
}

func readPortableCapability(t *testing.T, path string) fillervisualsafety.PortableCapability {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var capability fillervisualsafety.PortableCapability
	if err := decoder.Decode(&capability); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatal("portable capability has trailing content")
	}
	if err := fillervisualsafety.ValidatePortableCapability(capability); err != nil {
		t.Fatal(err)
	}
	return capability
}
