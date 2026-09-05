package playout

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/proctree"
)

// ffmpeg process supervision for playout (§9.1).
//
// A playout encoder differs from a VOD one in a way that dominates the design: IT NEVER
// EXITS. A VOD transcode ends at EOF, so leaking one is self-correcting. A live encoder
// leaked is a core burned until the process dies — which is why viewra's "no
// client-disconnect detection, sweep on a timer" posture (prior-art, viewra §4) is not
// survivable here.
//
// So three things are non-negotiable:
//
//  1. Progress is structured and line-framed: a dedicated fd on Unix; deterministically
//     demultiplexed from stderr on Windows, where Go does not support ExtraFiles.
//  2. Kill the process TREE, not the process. ffmpeg spawns children; viewra's watchdog
//     killed only the parent while start used Setpgid, so children orphaned.
//  3. The context is the lifetime. When it is cancelled the process dies — no timer, no
//     idle sweep, no last-accessed timestamp.

// progressPipeArg and wireProgress are one platform seam. Unix keeps the structured stream on
// inherited fd 3; Windows cannot inherit Cmd.ExtraFiles, so ffmpeg writes the same line-framed
// protocol to stderr and the scanner demultiplexes only its exact keys from diagnostics.

type progressWiring struct {
	reader       io.ReadCloser
	combined     bool
	afterStart   func()
	closeFailure func()
}

// Progress is one sample of ffmpeg's structured progress output.
type Progress struct {
	// Frame is the encoded frame count; monotonic while healthy.
	Frame int64
	// Speed is the realtime multiple. Below ~1.0 sustained means the encoder cannot keep
	// up and the channel will stutter — the single most useful health signal there is.
	Speed float64
	// OutTimeMS is how much output has been produced, in milliseconds.
	OutTimeMS int64
}

// Process is a supervised ffmpeg. Its Stdout is the caller's to read — for playout that is
// the MPEG-TS the session fans out to viewers.
type Process struct {
	Stdout io.ReadCloser
	// Stdin is populated only for a process whose caller owns a live input stream (the channel
	// mux). Ordinary finite encoders leave it nil.
	Stdin io.WriteCloser
	proc  *proctree.Supervisor
	run   *diagnostics.ProcessHandle

	finishOnce sync.Once
	ioWG       sync.WaitGroup

	log *slog.Logger

	mu      sync.Mutex
	lastErr string
}

// Start launches ffmpeg with the given args under ctx.
//
// The process is put under a platform process-tree owner so teardown reaches every helper.
// Signalling only the parent leaves children running — the exact bug viewra has, where start
// uses Setpgid but the watchdog calls Process.Kill().
func Start(ctx context.Context, bin string, args []string, log *slog.Logger, onProgress func(Progress)) (*Process, error) {
	return startProcess(ctx, bin, args, log, onProgress, false, nil, diagnostics.ProcessSpec{})
}

// StartObserved launches ffmpeg while recording one best-effort diagnostic Process run.
func StartObserved(ctx context.Context, bin string, args []string, log *slog.Logger, onProgress func(Progress),
	manager *diagnostics.ProcessManager, spec diagnostics.ProcessSpec,
) (*Process, error) {
	return startProcess(ctx, bin, args, log, onProgress, false, manager, spec)
}

// StartPipedObserved launches ffmpeg with caller-owned stdin and best-effort Process-run diagnostics.
// The returned Process owns both pipe ends; closing Stdin is the clean EOF signal that lets the mux
// flush and exit. Pass a nil manager and zero spec when diagnostics are not needed.
func StartPipedObserved(ctx context.Context, bin string, args []string, log *slog.Logger, onProgress func(Progress),
	manager *diagnostics.ProcessManager, spec diagnostics.ProcessSpec,
) (*Process, error) {
	return startProcess(ctx, bin, args, log, onProgress, true, manager, spec)
}

func startProcess(
	ctx context.Context, bin string, args []string, log *slog.Logger,
	onProgress func(Progress), piped bool, manager *diagnostics.ProcessManager, spec diagnostics.ProcessSpec,
) (*Process, error) {
	cmd := exec.Command(bin, args...) //nolint:gosec // args are built by this package, never user text
	var stdin io.WriteCloser
	var err error
	if piped {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("stdin pipe: %w", err)
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if stdin != nil {
			_ = stdin.Close()
		}
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	progress, err := wireProgress(cmd)
	if err != nil {
		_ = stdout.Close()
		if stdin != nil {
			_ = stdin.Close()
		}
		return nil, err
	}

	// stderr is kept for the LAST error only. ffmpeg is chatty and a live process runs for
	// days; retaining all of it is a slow memory leak, and the useful part when something
	// breaks is the final line.
	var stderr io.ReadCloser
	if !progress.combined {
		stderr, err = cmd.StderrPipe()
		if err != nil {
			progress.closeFailure()
			_ = stdout.Close()
			if stdin != nil {
				_ = stdin.Close()
			}
			return nil, fmt.Errorf("stderr pipe: %w", err)
		}
	}

	p := &Process{Stdout: stdout, Stdin: stdin, log: log}
	supervised, err := proctree.Start(ctx, cmd)
	if err != nil {
		progress.closeFailure()
		_ = stdout.Close()
		if stdin != nil {
			_ = stdin.Close()
		}
		if stderr != nil {
			_ = stderr.Close()
		}
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	p.proc = supervised
	if manager != nil {
		if spec.Executable == "" {
			spec.Executable = bin
		}
		if len(spec.Args) == 0 {
			spec.Args = args
		}
		p.run = manager.Begin(spec)
	}
	if progress.afterStart != nil {
		progress.afterStart()
	}

	if progress.combined {
		p.ioWG.Add(1)
		go func() { defer p.ioWG.Done(); p.readCombined(progress.reader, onProgress) }()
	} else {
		p.ioWG.Add(2)
		go func() { defer p.ioWG.Done(); p.readProgress(progress.reader, onProgress) }()
		go func() { defer p.ioWG.Done(); p.readStderr(stderr) }()
	}

	return p, nil
}

// readProgress parses ffmpeg's key=value progress stream.
//
// Structured, line-oriented, on its own fd. viewra scraped stderr in 4096-byte reads looking
// for "frame=" substrings, and a chunked read can split a token across the buffer boundary —
// a bufio.Scanner over a dedicated pipe cannot.
func (p *Process) readProgress(r io.ReadCloser, onProgress func(Progress)) {
	ReadProgress(r, p.observeProgress(onProgress))
}

func (p *Process) observeProgress(onProgress func(Progress)) func(Progress) {
	if p.run == nil {
		return onProgress
	}
	return func(progress Progress) {
		p.run.ObserveProgress(diagnostics.ProcessProgress{
			Frame: progress.Frame, Speed: progress.Speed, OutTimeMS: progress.OutTimeMS,
		})
		if onProgress != nil {
			onProgress(progress)
		}
	}
}

// ReadProgress parses ffmpeg's `-progress` stream and calls onProgress once per block.
//
// ⚠ **Exported so the filler transcode stage shares this parser rather than growing a second
// copy** (§10 V51b). It is the same fd, the same key set and the same block-boundary rule, and the
// two would drift the moment ffmpeg changed a field name — `out_time_ms` already reports
// MICROSECONDS despite its name, which is exactly the sort of fact that gets fixed in one copy.
// The behaviour here is byte-for-byte what the method did before the extraction.
//
// It takes ownership of r and closes it. A nil onProgress still DRAINS the pipe: a full pipe
// blocks ffmpeg's writes and stalls the encode.
func ReadProgress(r io.ReadCloser, onProgress func(Progress)) {
	defer func() { _ = r.Close() }()
	if onProgress == nil {
		// Still drain it: a full pipe blocks ffmpeg's writes and stalls the encode.
		_, _ = io.Copy(io.Discard, r)
		return
	}
	var cur Progress
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		_, complete := consumeProgressLine(strings.TrimSpace(sc.Text()), &cur)
		if complete {
			onProgress(cur)
		}
	}
}

// consumeProgressLine parses one exact ffmpeg progress-protocol line. recognized is false for
// diagnostics, which lets Windows demultiplex its shared stderr stream without guessing at
// chunks or swallowing real errors. complete marks progress=continue|end block boundaries.
func consumeProgressLine(line string, cur *Progress) (recognized, complete bool) {
	k, v, ok := strings.Cut(line, "=")
	if !ok {
		return false, false
	}
	v = strings.TrimSpace(v)
	switch k {
	case "frame":
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return false, false
		}
		cur.Frame = n
	case "out_time_ms":
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return false, false
		}
		cur.OutTimeMS = n / 1000 // ffmpeg reports microseconds despite the name
	case "speed":
		if v == "N/A" {
			return true, false
		}
		f, err := strconv.ParseFloat(strings.TrimSuffix(v, "x"), 64)
		if err != nil || !strings.HasSuffix(v, "x") {
			return false, false
		}
		cur.Speed = f
	case "fps", "total_size", "out_time_us", "dup_frames", "drop_frames":
		if v == "N/A" {
			return true, false
		}
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return false, false
		}
	case "bitrate":
		if v == "N/A" {
			return true, false
		}
		rate, found := strings.CutSuffix(v, "kbits/s")
		if !found {
			return false, false
		}
		if _, err := strconv.ParseFloat(strings.TrimSpace(rate), 64); err != nil {
			return false, false
		}
	case "out_time":
		if v == "N/A" {
			return true, false
		}
		if !validProgressClock(v) {
			return false, false
		}
	case "progress":
		// ffmpeg terminates each block with progress=continue|end. Emit on the
		// boundary so a consumer sees whole samples, never half-updated ones.
		return v == "continue" || v == "end", v == "continue" || v == "end"
	default:
		if strings.HasPrefix(k, "stream_") && strings.HasSuffix(k, "_q") {
			if v == "N/A" {
				return true, false
			}
			if _, err := strconv.ParseFloat(v, 64); err == nil {
				return true, false
			}
		}
		return false, false
	}
	return true, false
}

func validProgressClock(v string) bool {
	if strings.HasPrefix(v, "-") || strings.HasPrefix(v, "+") {
		v = v[1:]
	}
	parts := strings.Split(v, ":")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts[:2] {
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	_, err := strconv.ParseFloat(parts[2], 64)
	return err == nil
}

// readStderr keeps only the most recent non-empty line.
func (p *Process) readStderr(r io.ReadCloser) {
	defer func() { _ = r.Close() }()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		p.recordStderr(sc.Text())
	}
}

func (p *Process) readCombined(r io.ReadCloser, onProgress func(Progress)) {
	defer func() { _ = r.Close() }()
	var cur Progress
	observe := p.observeProgress(onProgress)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		recognized, complete := consumeProgressLine(line, &cur)
		if recognized {
			if complete && observe != nil {
				observe(cur)
			}
			continue
		}
		p.recordStderr(line)
	}
}

func (p *Process) recordStderr(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	p.mu.Lock()
	p.lastErr = line
	p.mu.Unlock()
	if p.run != nil {
		p.run.RecordOutput(line)
	}
	if p.log != nil {
		// Debug, not warn: ffmpeg writes routine notices to stderr, and logging them
		// as problems trains an operator to ignore the log. viewra needed an explicit
		// non-fatal allowlist for exactly this.
		p.log.Debug("ffmpeg", "line", line)
	}
}

// LastError returns ffmpeg's most recent stderr line, for surfacing why a channel stopped.
func (p *Process) LastError() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErr
}

// Stop terminates the process tree and waits for it.
//
// Tree, not process: ffmpeg spawns children and signalling only the parent orphans them —
// on a 24/7 box that accumulates until something notices.
//
// The wait is synchronous and guarded by sync.Once: the monitoring goroutine may already
// have reaped the child ("no child processes" otherwise), and callers need Stop to mean
// "it is gone" so a replacement can take the slot.
func (p *Process) Stop() {
	if p.proc == nil {
		return
	}
	p.proc.Stop()
	p.ioWG.Wait()
	p.finish(p.proc.Wait())
}

// Wait blocks until the process exits, returning its error. Safe to call concurrently with
// Stop and more than once.
func (p *Process) Wait() error {
	if p.proc == nil {
		return nil
	}
	err := p.proc.Wait()
	p.ioWG.Wait()
	p.finish(err)
	return err
}

// ProcessRunID exposes only the opaque correlation id, never the diagnostics output path.
func (p *Process) ProcessRunID() string {
	if p == nil || p.run == nil {
		return ""
	}
	return p.run.ID()
}

func (p *Process) finish(err error) {
	p.finishOnce.Do(func() {
		if p.run == nil {
			return
		}
		cancelled := p.proc != nil && p.proc.Stopped()
		reason := ""
		if cancelled {
			reason = "process tree stopped"
		}
		p.run.Finish(diagnostics.ProcessResult{Err: err, Cancelled: cancelled, TerminationReason: reason})
	})
}
