package playout

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/proctree"
)

// eagerAttacher models a warm live session whose initial burst arrives as the sink is attached.
type eagerAttacher struct {
	drained chan struct{}
}

func (a *eagerAttacher) AttachSink(_ context.Context, _ string, _ EncodePlan, sink sessionSink) (sinkLease, error) {
	if !sink.offer([]byte("initial session burst")) {
		return sinkLease{}, errors.New("sink rejected initial burst")
	}
	close(a.drained)
	return sinkLease{release: func() { sink.close() }}, nil
}

// burstAttacher models the larger-than-memory startup burst a warm session produces when its
// readrate initial burst is released. It deliberately keeps the sink open after the burst so
// the remux stays alive while the test inspects what reached ffmpeg's stdin.
type burstAttacher struct {
	chunks [][]byte
	sent   chan struct{}
}

func (a *burstAttacher) AttachSink(_ context.Context, _ string, _ EncodePlan, sink sessionSink) (sinkLease, error) {
	for _, chunk := range a.chunks {
		if !sink.offer(chunk) {
			return sinkLease{}, errors.New("sink rejected startup burst")
		}
	}
	close(a.sent)
	return sinkLease{release: func() { sink.close() }}, nil
}

type recordingWriteCloser struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	wakeup chan struct{}
}

func (w *recordingWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	select {
	case w.wakeup <- struct{}{}:
	default:
	}
	return n, err
}

func (w *recordingWriteCloser) Close() error { return nil }

func (w *recordingWriteCloser) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.buf.Bytes())
}

// fakeAttacher stands in for the session Manager, counting how many times Attach is called so a
// test can assert the "N HLS viewers share ONE session" invariant.
type fakeAttacher struct {
	attaches atomic.Int32
	detaches atomic.Int32
	mu       sync.Mutex
	sinks    []sessionSink
}

func (f *fakeAttacher) AttachSink(_ context.Context, _ string, _ EncodePlan, sink sessionSink) (sinkLease, error) {
	f.attaches.Add(1)
	f.mu.Lock()
	f.sinks = append(f.sinks, sink)
	f.mu.Unlock()
	return sinkLease{release: func() {
		f.detaches.Add(1)
		sink.close()
	}}, nil
}

// newTestHLSManager builds a manager whose ffmpeg spawn is faked: it writes a stub master
// playlist so awaitPlaylist succeeds, and returns a real-but-trivial process (a `true` command)
// so teardown's Wait has something to reap.
func newTestHLSManager(t *testing.T, att HLSAttacher) *HLSManager {
	t.Helper()
	m, err := NewHLSManager(att, "ffmpeg", t.TempDir(), 20*time.Millisecond, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	m.spawn = func(ctx context.Context, bin, dir string, _ EncodePlan, log *slog.Logger) (*hlsProcess, error) {
		// Write a stub playlist that REFERENCES A SEGMENT — awaitPlaylist waits for a `.ts` line
		// (the readiness signal), so a header-only stub would (correctly) never be considered ready.
		stub := "#EXTM3U\n#EXT-X-VERSION:6\n#EXTINF:4.0,\nseg-0.ts\n"
		if werr := os.WriteFile(filepath.Join(dir, hlsPlaylistName), []byte(stub), 0o644); werr != nil {
			return nil, werr
		}
		cmd := exec.Command("sleep", "3600")
		stdin, _ := cmd.StdinPipe()
		supervised, serr := proctree.Start(ctx, cmd)
		if serr != nil {
			return nil, serr
		}
		return &hlsProcess{proc: supervised, stdin: stdin}, nil
	}
	t.Cleanup(m.Stop)
	return m
}

// newTestHLSManagerWithPlaylist is newTestHLSManager with the stub playlist supplied by the
// caller, so a test can drive the REAL readiness predicate with a real playlist body.
func newTestHLSManagerWithPlaylist(t *testing.T, att HLSAttacher, stub string) *HLSManager {
	t.Helper()
	m, err := NewHLSManager(att, "ffmpeg", t.TempDir(), 20*time.Millisecond, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	// The negative case has to let the wait EXPIRE, so the production 45s would make this a
	// 45-second unit test. Short enough to be quick, long enough that the poll (100ms) runs
	// several times before giving up — a timeout below one poll interval would pass for the wrong
	// reason, never having read the file at all.
	m.readyTimeout = 600 * time.Millisecond
	m.spawn = func(ctx context.Context, bin, dir string, _ EncodePlan, log *slog.Logger) (*hlsProcess, error) {
		if werr := os.WriteFile(filepath.Join(dir, hlsPlaylistName), []byte(stub), 0o644); werr != nil {
			return nil, werr
		}
		cmd := exec.Command("sleep", "3600")
		stdin, _ := cmd.StdinPipe()
		supervised, serr := proctree.Start(ctx, cmd)
		if serr != nil {
			return nil, serr
		}
		return &hlsProcess{proc: supervised, stdin: stdin}, nil
	}
	t.Cleanup(m.Stop)
	return m
}

// READINESS MUST NOT NAME A CONTAINER (§9.1 V48).
//
// The HEVC plans emit fragmented MP4 — `seg-N.m4s` behind an `#EXT-X-MAP:URI="init.mp4"` — so
// their playlist contains no `.ts` anywhere. The readiness predicate matched the literal `.ts`,
// which meant awaitPlaylist could never see an fMP4 playlist become ready: every HEVC-capable
// client waited out the 45s timeout and received a 502, on the exact path V48 added to fix the
// HEVC black screen.
//
// The bodies below are what ffmpeg actually writes for each `-hls_segment_type`, so this fails
// against a container-specific predicate and passes only against a structural one.
func TestHLSManager_ReadyForBothSegmentContainers(t *testing.T) {
	const tsPlaylist = "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:4\n#EXTINF:4.000000,\nseg-0.ts\n"
	const fmp4Playlist = "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:4\n" +
		"#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:4.000000,\nseg-0.m4s\n"

	for _, tc := range []struct {
		name string
		plan EncodePlan
		body string
	}{
		{"mpegts", PlanBaseline, tsPlaylist},
		{"fmp4", PlanHEVC10, fmp4Playlist},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestHLSManagerWithPlaylist(t, &fakeAttacher{}, tc.body)
			path, detach, err := m.Playlist("ch1", tc.plan)
			if err != nil {
				t.Fatalf("channel never became ready: %v — a client on this plan gets a 502", err)
			}
			defer detach()
			if path == "" {
				t.Error("ready but no playlist path")
			}
		})
	}
}

// A HEADER-ONLY playlist must still NOT be ready — the stall the readiness gate exists to prevent.
//
// This is the other half of the predicate, and the reason it matches `#EXTINF` rather than
// anything looser: an fMP4 playlist carries `#EXT-X-MAP` with the init segment BEFORE any media
// segment exists, so a predicate keyed on that tag would call this ready and hand hls.js a
// playlist with nothing to fetch.
func TestHLSManager_HeaderOnlyPlaylistIsNotReady(t *testing.T) {
	const headerOnly = "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:4\n#EXT-X-MAP:URI=\"init.mp4\"\n"

	m := newTestHLSManagerWithPlaylist(t, &fakeAttacher{}, headerOnly)
	if _, detach, err := m.Playlist("ch1", PlanHEVC10); err == nil {
		if detach != nil {
			detach()
		}
		t.Fatal("a header-only playlist was reported ready — hls.js would parse it, find no media and stall")
	}
}

// A remux that has already exited cannot become ready. Waiting out the 45-second production
// timeout hides the failure behind a long black screen and prevents the client from retrying.
func TestHLSManager_StopsWaitingWhenRemuxExits(t *testing.T) {
	att := &fakeAttacher{}
	m, err := NewHLSManager(att, "ffmpeg", t.TempDir(), 20*time.Millisecond, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	m.readyTimeout = 2 * time.Second
	m.spawn = func(ctx context.Context, _ string, _ string, _ EncodePlan, _ *slog.Logger) (*hlsProcess, error) {
		cmd := exec.CommandContext(ctx, "sh", "-c", "exit 7")
		stdin, _ := cmd.StdinPipe()
		supervised, err := proctree.Start(ctx, cmd)
		if err != nil {
			return nil, err
		}
		return newHLSProcess(supervised, stdin, slog.New(slog.DiscardHandler)), nil
	}
	t.Cleanup(m.Stop)

	before := time.Now()
	_, detach, err := m.Playlist("ch1", PlanBaseline)
	if detach != nil {
		detach()
	}
	if err == nil {
		t.Fatal("exited remux was reported ready")
	}
	if elapsed := time.Since(before); elapsed > 500*time.Millisecond {
		t.Fatalf("waited %s after remux had already exited", elapsed)
	}
	if _, retryDetach, retryErr := m.Playlist("ch1", PlanBaseline); retryErr == nil {
		if retryDetach != nil {
			retryDetach()
		}
		t.Fatal("retry unexpectedly reported the exited remux ready")
	}
	if got := att.attaches.Load(); got != 2 {
		t.Fatalf("retry made %d total session attaches, want 2 — it rejoined the dead remux", got)
	}
}

// The core invariant: three browser viewers of one channel share ONE remux and therefore ONE
// session Attach — three tabs cost one encoder, exactly like three TVs (§9.1).
func TestHLSManager_ViewersShareOneRemux(t *testing.T) {
	att := &fakeAttacher{}
	m := newTestHLSManager(t, att)

	var detaches []func()
	for i := 0; i < 3; i++ {
		_, d, err := m.Playlist("ch1", PlanBaseline)
		if err != nil {
			t.Fatalf("viewer %d: %v", i, err)
		}
		detaches = append(detaches, d)
	}

	if got := att.attaches.Load(); got != 1 {
		t.Fatalf("3 viewers caused %d session attaches, want 1 (shared encoder)", got)
	}
	for _, d := range detaches {
		d()
	}
}

// A warm session can produce its first burst before the HLS ffmpeg child has finished spawning.
// If HLS waits until after spawn to drain the session viewer, Manager's deliberately-small viewer
// buffer fills and drops the remux. The browser then waits the full readiness timeout on a channel
// whose parent is healthy — the intermittent cold black screen seen in the real runtime.
func TestHLSManager_DrainsSessionWhileRemuxSpawns(t *testing.T) {
	att := &eagerAttacher{drained: make(chan struct{})}
	m, err := NewHLSManager(att, "ffmpeg", t.TempDir(), 20*time.Millisecond, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	m.readyTimeout = time.Second
	m.spawn = func(ctx context.Context, _ string, dir string, _ EncodePlan, _ *slog.Logger) (*hlsProcess, error) {
		select {
		case <-att.drained:
		case <-time.After(200 * time.Millisecond):
			return nil, errors.New("session was not drained while the remux spawned")
		}
		stub := []byte("#EXTM3U\n#EXTINF:4.0,\nseg-0.ts\n")
		if err := os.WriteFile(filepath.Join(dir, hlsPlaylistName), stub, 0o644); err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, "sleep", "3600")
		stdin, _ := cmd.StdinPipe()
		supervised, err := proctree.Start(ctx, cmd)
		if err != nil {
			return nil, err
		}
		return newHLSProcess(supervised, stdin, nil), nil
	}
	t.Cleanup(m.Stop)

	_, detach, err := m.Playlist("ch1", PlanBaseline)
	if err != nil {
		t.Fatalf("warm session burst was lost during HLS startup: %v", err)
	}
	detach()
}

// Draining early is not enough: the relay must preserve EVERY MPEG-TS byte in order while ffmpeg
// starts. Dropping an arbitrary oldest chunk corrupts PAT/PMT or a reference frame; the real ffmpeg
// then exits with decoder errors, and the client follows stale segment URLs into a 404/retry loop.
func TestHLSManager_PreservesStartupBurstWhileRemuxSpawns(t *testing.T) {
	const chunkCount = 320 // larger than the removed 256-chunk lossy relay
	chunks := make([][]byte, 0, chunkCount)
	var want bytes.Buffer
	for i := range chunkCount {
		chunk := []byte(fmt.Sprintf("chunk-%04d\n", i))
		chunks = append(chunks, chunk)
		_, _ = want.Write(chunk)
	}

	att := &burstAttacher{chunks: chunks, sent: make(chan struct{})}
	m, err := NewHLSManager(att, "ffmpeg", t.TempDir(), 20*time.Millisecond, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	m.readyTimeout = time.Second
	recorded := &recordingWriteCloser{wakeup: make(chan struct{}, 1)}
	m.spawn = func(ctx context.Context, _ string, dir string, _ EncodePlan, _ *slog.Logger) (*hlsProcess, error) {
		select {
		case <-att.sent:
		case <-time.After(time.Second):
			return nil, errors.New("session burst was not drained while the remux spawned")
		}
		stub := []byte("#EXTM3U\n#EXTINF:4.0,\nseg-0.ts\n")
		if err := os.WriteFile(filepath.Join(dir, hlsPlaylistName), stub, 0o644); err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, "sleep", "3600")
		supervised, err := proctree.Start(ctx, cmd)
		if err != nil {
			return nil, err
		}
		return newHLSProcess(supervised, recorded, nil), nil
	}
	t.Cleanup(m.Stop)

	_, detach, err := m.Playlist("ch1", PlanBaseline)
	if err != nil {
		t.Fatal(err)
	}
	defer detach()

	deadline := time.Now().Add(time.Second)
	for len(recorded.bytes()) < want.Len() && time.Now().Before(deadline) {
		select {
		case <-recorded.wakeup:
		case <-time.After(10 * time.Millisecond):
		}
	}
	got := recorded.bytes()
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("ffmpeg received %d startup bytes, want %d byte-for-byte in order", len(got), want.Len())
	}
}

func TestHLSRelay_FailsCleanlyAtItsMemoryBound(t *testing.T) {
	relay := newHLSRelay()
	relay.maxBytes = 4
	if relay.offer([]byte("1234")) != true {
		t.Fatal("relay rejected bytes within its bound")
	}
	if relay.offer([]byte("5")) {
		t.Fatal("relay accepted bytes beyond its bound")
	}
	if _, err := relay.next(); err == nil || !strings.Contains(err.Error(), "startup queue exceeded") {
		t.Fatalf("overflow next error = %v, want explicit bounded failure", err)
	}
}

// A second manifest poll can arrive while the first request is still waiting for ffmpeg to write
// the first segment. It shares the existing remux, but it is not allowed to skip that remux's
// readiness gate and hand Origin a path that does not exist yet — that race is an immediate 502
// during an otherwise healthy channel start.
func TestHLSManager_JoinerWaitsForExistingRemuxToBecomeReady(t *testing.T) {
	att := &fakeAttacher{}
	m, err := NewHLSManager(att, "ffmpeg", t.TempDir(), 20*time.Millisecond, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	m.readyTimeout = 2 * time.Second
	dirReady := make(chan string, 1)
	m.spawn = func(ctx context.Context, _ string, dir string, _ EncodePlan, _ *slog.Logger) (*hlsProcess, error) {
		cmd := exec.CommandContext(ctx, "sleep", "3600")
		stdin, _ := cmd.StdinPipe()
		supervised, err := proctree.Start(ctx, cmd)
		if err != nil {
			return nil, err
		}
		dirReady <- dir
		return newHLSProcess(supervised, stdin, nil), nil
	}
	t.Cleanup(m.Stop)

	type result struct {
		detach func()
		err    error
	}
	first := make(chan result, 1)
	go func() {
		_, detach, err := m.Playlist("ch1", PlanBaseline)
		first <- result{detach: detach, err: err}
	}()
	dir := <-dirReady

	deadline := time.Now().Add(time.Second)
	for {
		m.mu.Lock()
		started := len(m.remuxes) == 1
		m.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first request never published its starting remux")
		}
		time.Sleep(time.Millisecond)
	}

	second := make(chan result, 1)
	go func() {
		_, detach, err := m.Playlist("ch1", PlanBaseline)
		second <- result{detach: detach, err: err}
	}()
	select {
	case got := <-second:
		if got.detach != nil {
			got.detach()
		}
		t.Fatal("joiner returned before the shared remux had a playable segment")
	case <-time.After(100 * time.Millisecond):
	}

	stub := []byte("#EXTM3U\n#EXTINF:4.0,\nseg-0.ts\n")
	if err := os.WriteFile(filepath.Join(dir, hlsPlaylistName), stub, 0o644); err != nil {
		t.Fatal(err)
	}
	for i, ch := range []chan result{first, second} {
		select {
		case got := <-ch:
			if got.err != nil {
				t.Fatalf("viewer %d: %v", i+1, got.err)
			}
			got.detach()
		case <-time.After(time.Second):
			t.Fatalf("viewer %d did not observe the ready playlist", i+1)
		}
	}
}

// After the last viewer leaves and the grace window elapses, the remux tears down and releases
// its single session refcount — so an abandoned channel stops encoding.
func TestHLSManager_TeardownReleasesSessionAfterGrace(t *testing.T) {
	att := &fakeAttacher{}
	m := newTestHLSManager(t, att)

	_, detach, err := m.Playlist("ch1", PlanBaseline)
	if err != nil {
		t.Fatal(err)
	}
	detach()

	// Grace is 20ms in the test manager; give it room to fire.
	deadline := time.Now().Add(2 * time.Second)
	for att.detaches.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if att.detaches.Load() != 1 {
		t.Fatalf("session was not detached after the last viewer + grace (detaches=%d)", att.detaches.Load())
	}
}

// A viewer arriving within the grace window keeps the remux alive — no fresh Attach, no
// interruption for the people (re)joining.
func TestHLSManager_RejoinWithinGraceKeepsOneAttach(t *testing.T) {
	att := &fakeAttacher{}
	m := newTestHLSManager(t, att)

	_, d1, err := m.Playlist("ch1", PlanBaseline)
	if err != nil {
		t.Fatal(err)
	}
	d1() // last viewer leaves; grace timer arms

	// Re-join immediately, well within the 20ms grace.
	_, d2, err := m.Playlist("ch1", PlanBaseline)
	if err != nil {
		t.Fatal(err)
	}
	defer d2()

	if got := att.attaches.Load(); got != 1 {
		t.Fatalf("a rejoin within grace caused %d attaches, want 1", got)
	}
}

// HLS has its own client-level grace above the session manager. Its retained remux sink must become
// idle demand when the client releases, allowing the one-slot session manager to reclaim that warm
// channel for the next foreground HLS tune.
func TestHLSManager_AtCapacityReclaimsIdleWarmSession(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	sessions := testManager(t, spawn, 1, time.Minute)
	m := newTestHLSManager(t, sessions)
	m.grace = time.Minute

	_, detachFirst, err := m.Playlist("ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder("ch1").w.Write([]byte("transport")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	detachFirst()
	stats := sessions.Stats(time.Now())
	if len(stats) != 1 || stats[0].Viewers != 0 {
		t.Fatalf("first HLS release left session state %+v, want one idle session", stats)
	}

	_, detachSecond, err := m.Playlist("ch2", PlanFull)
	if err != nil {
		t.Fatalf("foreground HLS tune was refused while a warm session was idle: %v", err)
	}
	defer detachSecond()

	select {
	case <-encoder("ch1").stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("idle HLS session was not reclaimed for the foreground tune")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := m.AssetPath("ch1", PlanFull, hlsPlaylistName); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reclaimed session left its HLS remux and assets alive")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Aggregate warm-session eviction has to retire the delivery adapter as well as the parent. An
// idle HLS remux still owns a sink, process, and scratch tree even though its lease reports zero
// viewer demand; closing the LRU parent must cascade through that sink and make stale assets vanish.
func TestHLSManager_WarmIdleHotSetEvictionRetiresRemuxAssets(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	sessions := testManager(t, spawn, 0, time.Minute)
	m := newTestHLSManager(t, sessions)
	m.grace = time.Minute

	for _, channelID := range []string{"ch1", "ch2", "ch3"} {
		_, detach, err := m.Playlist(channelID, PlanFull)
		if err != nil {
			t.Fatalf("start %s HLS: %v", channelID, err)
		}
		if _, err := encoder(channelID).w.Write([]byte("transport")); err != nil {
			t.Fatal(err)
		}
		sessions.ReportProgram(channelID, PlanFull, EncoderSoftware, false, Progress{})
		// The fan-out pump is asynchronous; allow it to publish the byte before release makes the
		// HLS sink idle. This mirrors the established capacity-reclamation test above.
		time.Sleep(20 * time.Millisecond)
		detach()
	}

	select {
	case <-encoder("ch1").stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("oldest idle HLS parent was not stopped")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := m.AssetPath("ch1", PlanFull, hlsPlaylistName); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("warm hot-set eviction left the HLS remux and assets alive")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, channelID := range []string{"ch2", "ch3"} {
		if _, ok := m.AssetPath(channelID, PlanFull, hlsPlaylistName); !ok {
			t.Fatalf("newer warm remux %s was retired before the LRU", channelID)
		}
	}
}

// The HLS remux is an internal sink, not a viewer. Once the manifest request releases its client
// lease, the underlying session must report zero active viewers even though the remux stays warm and
// continues consuming transport through its own grace interval.
func TestHLSManager_IdleWarmRemuxDoesNotCountAsActiveSessionViewer(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	sessions := testManager(t, spawn, 4, time.Minute)
	m := newTestHLSManager(t, sessions)
	m.grace = time.Minute

	_, detach, err := m.Playlist("ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder("ch1").w.Write([]byte("transport")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	detach()

	stats := sessions.Stats(time.Now())
	if len(stats) != 1 {
		t.Fatalf("live sessions = %d, want one warm session", len(stats))
	}
	if stats[0].Viewers != 0 {
		t.Fatalf("active viewers = %d, want zero after the HLS client released", stats[0].Viewers)
	}
}

func TestHLSManager_StopChannelStopsEveryPlanAndLeavesOtherChannels(t *testing.T) {
	att := &fakeAttacher{}
	m := newTestHLSManager(t, att)

	for _, tc := range []struct {
		channel string
		plan    EncodePlan
	}{
		{"ch1", PlanBaseline},
		{"ch1", PlanHEVC10},
		{"ch2", PlanFull},
	} {
		if _, _, err := m.Playlist(tc.channel, tc.plan); err != nil {
			t.Fatalf("Playlist(%s, %s): %v", tc.channel, tc.plan, err)
		}
	}

	m.StopChannel("ch1")

	if got := att.detaches.Load(); got != 2 {
		t.Fatalf("session detaches = %d, want the two ch1 plans", got)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.remuxes) != 1 || m.remuxes[remuxKey{channel: "ch2", plan: PlanFull}] == nil {
		t.Fatalf("remaining remuxes = %#v, want only ch2", m.remuxes)
	}
}
