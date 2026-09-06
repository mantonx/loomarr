package fillervisualsafety

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

const MaximumPortableWorkerShutdownTime = 5 * time.Second

// RunPortableCoverage executes one exact local worker for one complete source.
// It returns no partial coverage or inference evidence on any decoder, worker,
// identity, protocol, response, timeout, or shutdown failure.
func RunPortableCoverage(ctx context.Context, source *PreparedSource, ffmpegPath, workerPath string, capability PortableCapability) (PortableExecution, error) {
	if ctx == nil || ctx.Err() != nil || source == nil || ValidatePortableCapability(capability) != nil ||
		len(source.Plan.Points) > capability.MaximumFramesPerSource {
		return PortableExecution{}, errors.New("portable visual-safety execution input is invalid")
	}
	worker, err := resolvePortableWorker(ctx, workerPath, capability.Worker)
	if err != nil {
		return PortableExecution{}, err
	}

	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	cmd := exec.CommandContext(workerCtx, worker, "serve") //nolint:gosec // exact operator-selected executable is hashed above and below
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return PortableExecution{}, fmt.Errorf("portable visual-safety worker stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return PortableExecution{}, fmt.Errorf("portable visual-safety worker stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return PortableExecution{}, fmt.Errorf("portable visual-safety worker stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return PortableExecution{}, fmt.Errorf("portable visual-safety worker start: %w", err)
	}
	diagnostics := &portableWorkerDiagnostics{}
	diagnosticsDone := make(chan struct{})
	go func() {
		defer close(diagnosticsDone)
		_, _ = io.Copy(diagnostics, stderr)
	}()

	responses := make([]PortableFrameResponse, 0, len(source.Plan.Points))
	coverage, runErr := DecodeCoverage(workerCtx, source, ffmpegPath,
		func(frameCtx context.Context, frame FrameEvidence, payload []byte) error {
			request, sealErr := SealPortableFrameRequest(capability, source.Plan, frame, PixelRGB24)
			if sealErr != nil {
				return sealErr
			}
			response, exchangeErr := exchangePortableFrame(frameCtx, cancelWorker, stdin, stdout, capability, source.Plan, request, payload)
			if exchangeErr != nil {
				return exchangeErr
			}
			responses = append(responses, response)
			return nil
		})
	shutdownErr := shutdownPortableWorker(workerCtx, cancelWorker, cmd, stdin, stdout, diagnosticsDone)
	identityErr := verifyPortableWorker(ctx, worker, capability.Worker)
	if runErr != nil {
		return PortableExecution{}, runErr
	}
	if shutdownErr != nil {
		return PortableExecution{}, portableWorkerFailure(shutdownErr, diagnostics.String())
	}
	if diagnostics.Overflowed() {
		return PortableExecution{}, errors.New("portable visual-safety worker diagnostics exceeded their bound")
	}
	if identityErr != nil {
		return PortableExecution{}, identityErr
	}
	inference, err := SealPortableInferenceEvidence(capability, source.Plan, coverage, responses)
	if err != nil {
		return PortableExecution{}, err
	}
	return PortableExecution{Coverage: coverage, Inference: inference}, nil
}

type portableExchangeResult struct {
	response PortableFrameResponse
	err      error
}

func exchangePortableFrame(ctx context.Context, cancelWorker context.CancelFunc, stdin io.Writer, stdout io.Reader, capability PortableCapability, plan CoveragePlan, request PortableFrameRequest, payload []byte) (PortableFrameResponse, error) {
	result := make(chan portableExchangeResult, 1)
	started := time.Now()
	go func() {
		if err := WritePortableFrameRequest(stdin, request, payload); err != nil {
			result <- portableExchangeResult{err: err}
			return
		}
		response, err := ReadPortableFrameResponse(stdout)
		result <- portableExchangeResult{response: response, err: err}
	}()
	timer := time.NewTimer(time.Duration(capability.MaximumFrameLatencyMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case got := <-result:
		if got.err != nil {
			return PortableFrameResponse{}, got.err
		}
		if elapsed := time.Since(started); elapsed > time.Duration(capability.MaximumFrameLatencyMS)*time.Millisecond {
			return PortableFrameResponse{}, errors.New("portable visual-safety worker exceeded its frame latency")
		}
		if err := ValidatePortableFrameResponse(capability, plan, request, got.response); err != nil {
			return PortableFrameResponse{}, err
		}
		return got.response, nil
	case <-ctx.Done():
		cancelWorker()
		<-result
		return PortableFrameResponse{}, ctx.Err()
	case <-timer.C:
		cancelWorker()
		<-result
		return PortableFrameResponse{}, errors.New("portable visual-safety worker exceeded its frame latency")
	}
}

func shutdownPortableWorker(ctx context.Context, cancelWorker context.CancelFunc, cmd *exec.Cmd, stdin io.Closer, stdout io.Reader, diagnosticsDone <-chan struct{}) error {
	closeErr := stdin.Close()
	type eofResult struct {
		count int
		err   error
	}
	eof := make(chan eofResult, 1)
	go func() {
		var extra [1]byte
		count, err := stdout.Read(extra[:])
		eof <- eofResult{count: count, err: err}
	}()
	timer := time.NewTimer(MaximumPortableWorkerShutdownTime)
	defer timer.Stop()
	var outputErr error
	select {
	case got := <-eof:
		if got.count != 0 || !errors.Is(got.err, io.EOF) {
			outputErr = errors.New("portable visual-safety worker emitted unexpected output")
		}
	case <-ctx.Done():
		cancelWorker()
		<-eof
		outputErr = ctx.Err()
	case <-timer.C:
		cancelWorker()
		<-eof
		outputErr = errors.New("portable visual-safety worker did not stop")
	}
	<-diagnosticsDone
	waitErr := cmd.Wait()
	if closeErr != nil {
		return closeErr
	}
	if outputErr != nil {
		return outputErr
	}
	return waitErr
}
