package fillervisualsafety_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
	"github.com/loomarr/loomarr/internal/testkit"
)

const portableWorkerTestMode = "LOOMARR_TEST_PORTABLE_WORKER_MODE"

func TestRunPortableCoverageSealsEveryWorkerResponseBesideCompleteDecode(t *testing.T) {
	t.Parallel()

	source, _ := preparedDecoderSource(t)
	defer func() { _ = source.Close() }()
	ffmpeg := decoderExecutable(t, t.TempDir()+"/arguments", []string{"0", "1", "2", "2.999"}, 0)
	worker := portableWorkerExecutable(t, "success")
	capability := portableWorkerCapability(t, worker, 2_000)

	execution, err := fillervisualsafety.RunPortableCoverage(context.Background(), source, ffmpeg, worker, capability)
	if err != nil {
		t.Fatalf("RunPortableCoverage() error = %v", err)
	}
	if len(execution.Coverage.Frames) != 4 || len(execution.Inference.Responses) != 4 {
		t.Fatalf("execution coverage = %d, responses = %d", len(execution.Coverage.Frames), len(execution.Inference.Responses))
	}
	if execution.Inference.CoverageEvidenceSHA256 != execution.Coverage.SHA256 ||
		execution.Inference.Responses[3].FrameSHA256 != execution.Coverage.Frames[3].SHA256 {
		t.Fatal("inference evidence is not bound to complete decoded coverage")
	}
	if err := fillervisualsafety.ValidatePortableInferenceEvidence(capability, source.Plan, execution.Coverage, execution.Inference); err != nil {
		t.Fatalf("ValidatePortableInferenceEvidence() error = %v", err)
	}

	missing := execution.Inference
	missing.Responses = missing.Responses[:len(missing.Responses)-1]
	missing.SHA256 = fillervisualsafety.PortableInferenceEvidenceSHA256(missing)
	if err := fillervisualsafety.ValidatePortableInferenceEvidence(capability, source.Plan, execution.Coverage, missing); err == nil {
		t.Fatal("inference evidence accepted a missing terminal frame")
	}
}

func TestRunPortableCoverageReturnsNoPartialEvidenceOnWorkerFailure(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		mode      string
		latencyMS int64
	}{
		"response identity drift": {mode: "wrong-response", latencyMS: 2_000},
		"process failure":         {mode: "failure", latencyMS: 2_000},
		"real latency exceeded":   {mode: "slow", latencyMS: 50},
	}
	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source, _ := preparedDecoderSource(t)
			defer func() { _ = source.Close() }()
			ffmpeg := decoderExecutable(t, t.TempDir()+"/arguments", []string{"0", "1", "2", "2.999"}, 0)
			worker := portableWorkerExecutable(t, test.mode)
			capability := portableWorkerCapability(t, worker, test.latencyMS)
			execution, err := fillervisualsafety.RunPortableCoverage(context.Background(), source, ffmpeg, worker, capability)
			if err == nil || execution.Coverage.SHA256 != "" || execution.Inference.SHA256 != "" {
				t.Fatalf("RunPortableCoverage() = %+v, %v", execution, err)
			}
		})
	}
}

func TestRunPortableCoverageRejectsExecutableDriftBeforeDecode(t *testing.T) {
	t.Parallel()

	source, _ := preparedDecoderSource(t)
	defer func() { _ = source.Close() }()
	worker := portableWorkerExecutable(t, "success")
	input := portableCapabilityInput()
	input.Worker.ExecutableSHA256 = repeatedDigest("a")
	input.MaximumFramesPerSource = len(source.Plan.Points)
	input.MaximumFrameBytes = 36
	capability, err := fillervisualsafety.SealPortableCapability(input)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := fillervisualsafety.RunPortableCoverage(context.Background(), source, "must-not-run", worker, capability)
	if err == nil || execution.Coverage.SHA256 != "" || !strings.Contains(err.Error(), "identity drifted") {
		t.Fatalf("RunPortableCoverage() = %+v, %v", execution, err)
	}
}

// TestPortableWorkerProcess is entered through an exact, hashed POSIX wrapper.
// The parent test process returns immediately; the child speaks the real wire.
func TestPortableWorkerProcess(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "serve" {
		return
	}
	mode := os.Getenv(portableWorkerTestMode)
	reader := bufio.NewReader(os.Stdin)
	for {
		if _, err := reader.Peek(1); errors.Is(err, io.EOF) {
			os.Exit(0)
		} else if err != nil {
			os.Exit(2)
		}
		request, _, err := fillervisualsafety.ReadPortableFrameRequest(reader, fillervisualsafety.MaximumFrameBytes)
		if err != nil {
			os.Exit(3)
		}
		switch mode {
		case "failure":
			_, _ = fmt.Fprintln(os.Stderr, "model inference failed")
			os.Exit(7)
		case "slow":
			time.Sleep(200 * time.Millisecond)
		}
		response := fillervisualsafety.PortableFrameResponse{
			SchemaVersion:   fillervisualsafety.PortableFrameResponseSchemaVersion,
			ContractVersion: fillervisualsafety.PortableFrameResponseContractVersion,
			RequestSHA256:   request.SHA256, CapabilitySHA256: request.CapabilitySHA256,
			FrameSHA256: request.Frame.SHA256, ElapsedMS: 1, Models: validPortableScores(),
		}
		if mode == "wrong-response" {
			response.FrameSHA256 = repeatedDigest("9")
		}
		response.SHA256 = fillervisualsafety.PortableFrameResponseSHA256(response)
		if err := fillervisualsafety.WritePortableFrameResponse(os.Stdout, response); err != nil {
			os.Exit(4)
		}
	}
}

func portableWorkerExecutable(t *testing.T, mode string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("%s=%q\nexport %s\nexec %q -test.run '^TestPortableWorkerProcess$' -- \"$@\"\n",
		portableWorkerTestMode, mode, portableWorkerTestMode, executable)
	return testkit.POSIXExecutable(t, "loomarr-visual-worker", body)
}

func portableWorkerCapability(t *testing.T, worker string, latencyMS int64) fillervisualsafety.PortableCapability {
	t.Helper()
	raw, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	input := portableCapabilityInput()
	input.Worker.ExecutableSHA256 = hex.EncodeToString(digest[:])
	input.MaximumFramesPerSource = 4
	input.MaximumFrameBytes = 36
	input.MaximumFrameLatencyMS = latencyMS
	capability, err := fillervisualsafety.SealPortableCapability(input)
	if err != nil {
		t.Fatal(err)
	}
	return capability
}
