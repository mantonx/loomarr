package fillersafety

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/loomarr/loomarr/internal/proctree"
)

const (
	maxSherpaStdoutBytes = 4 << 20
	maxSherpaStderrBytes = 256 << 10
	maxSherpaSourceMS    = int64(30 * time.Minute / time.Millisecond)
	maxSherpaRunTime     = 30 * time.Minute
)

type sherpaBoundedOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	count    int
	limit    int
	exceeded bool
	cancel   context.CancelFunc
	retain   bool
}

func (w *sherpaBoundedOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	if w.exceeded {
		w.mu.Unlock()
		return len(data), nil
	}
	remaining := w.limit - w.count
	if len(data) > remaining {
		if w.retain && remaining > 0 {
			_, _ = w.buffer.Write(data[:remaining])
		}
		w.exceeded = true
		cancel := w.cancel
		w.mu.Unlock()
		cancel()
		return len(data), nil
	}
	w.count += len(data)
	if w.retain {
		_, _ = w.buffer.Write(data)
	}
	w.mu.Unlock()
	return len(data), nil
}

func (w *sherpaBoundedOutput) result() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.buffer.Bytes()), w.exceeded
}

func runSherpaKeywordSpotter(ctx context.Context, proposer *sherpaProposer, wavPath string, durationMS int64) ([]byte, error) {
	if ctx == nil || proposer == nil || durationMS <= 0 || durationMS > maxSherpaSourceMS || !filepath.IsAbs(wavPath) {
		return nil, fmt.Errorf("spoken-safety acoustic proposal input is invalid")
	}
	runCtx, cancel := context.WithTimeout(ctx, sherpaTimeout(durationMS))
	defer cancel()
	stdout := &sherpaBoundedOutput{limit: maxSherpaStdoutBytes, cancel: cancel, retain: true}
	stderr := &sherpaBoundedOutput{limit: maxSherpaStderrBytes, cancel: cancel}
	args := []string{
		"--tokens=" + proposer.artifacts.tokens,
		"--encoder=" + proposer.artifacts.encoder,
		"--decoder=" + proposer.artifacts.decoder,
		"--joiner=" + proposer.artifacts.joiner,
		"--provider=cpu",
		"--num-threads=2",
		"--keywords-file=" + proposer.artifacts.keywords,
		"--keywords-score=4",
		"--keywords-threshold=" + sherpaKeywordThreshold,
		wavPath,
	}
	command := exec.Command(proposer.artifacts.runtime, args...) //nolint:gosec // executable and arguments are private staged artifacts
	command.Dir = filepath.Dir(wavPath)
	command.Env = sherpaEnvironment(proposer.artifacts.library)
	command.Stdout = stdout
	command.Stderr = stderr
	supervisor, err := proctree.Start(runCtx, command)
	if err != nil {
		return nil, fmt.Errorf("start spoken-safety acoustic proposer")
	}
	waitErr := supervisor.Wait()
	output, stdoutExceeded := stdout.result()
	_, stderrExceeded := stderr.result()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("spoken-safety acoustic proposer canceled")
	}
	if stdoutExceeded || stderrExceeded {
		return nil, fmt.Errorf("spoken-safety acoustic proposer exceeded its output bound")
	}
	if runCtx.Err() != nil || supervisor.Stopped() {
		return nil, fmt.Errorf("spoken-safety acoustic proposer exceeded its runtime bound")
	}
	if waitErr != nil {
		return nil, fmt.Errorf("spoken-safety acoustic proposer failed")
	}
	return output, nil
}

func sherpaTimeout(durationMS int64) time.Duration {
	duration := time.Duration(durationMS) * time.Millisecond
	timeout := 30*time.Second + 2*duration
	if timeout > maxSherpaRunTime {
		return maxSherpaRunTime
	}
	return timeout
}

func sherpaEnvironment(libraryPath string) []string {
	environment := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "LD_LIBRARY_PATH=") || strings.HasPrefix(item, "DYLD_LIBRARY_PATH=") {
			continue
		}
		environment = append(environment, item)
	}
	libraryDir := filepath.Dir(libraryPath)
	return append(environment, "LD_LIBRARY_PATH="+libraryDir, "DYLD_LIBRARY_PATH="+libraryDir)
}
