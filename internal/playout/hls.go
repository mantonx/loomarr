package playout

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/proctree"
)

// Direct-play HLS for internal playout (§9.1 Watch, V46).
//
// A `<video>` element cannot play the raw MPEG-TS the session fans out — that stream is for a
// media server or ffmpeg. So the channel is repackaged into HLS a browser (hls.js / native Safari)
// or a native app (AVPlayer / ExoPlayer) can load.
//
// DIRECT PLAY: the repackage is a `-c copy`, NOT a transcode. The session already produces the
// channel's normalized H.264 (§9.1), so the remux only re-containers those bytes into HLS
// segments — near-zero CPU, and the first segment lands in about one keyframe interval, so a live
// channel PLAYS RIGHT AWAY. There is deliberately no adaptive-bitrate ladder: transcoding lower
// renditions needs a cold encoder spin-up that delayed first paint by many seconds, and for a LIVE
// channel that is the wrong trade — the whole point is that hitting Watch plays immediately. (ABR
// can return later as an enhancement that never blocks first paint.)
//
// The refcount is one level deeper than the session's: N HLS viewers of a channel share ONE remux
// (this file) → ONE session (session.go) → ONE source encoder. Three browser tabs cost one
// encoder, exactly as three TVs do — cost scales with channels watched, never with viewers (§9.1).

// hlsSegmentDuration is the target segment length. Short segments = lower join latency (a viewer
// waits at most this long for a keyframe boundary) at the cost of more files and more playlist
// churn. The session's encoder pins the keyframe cadence (args.go), so `-c copy` cuts land on
// keyframes predictably.
const hlsSegmentDuration = 4

// DVRHorizon is the one server-side time-shift contract shared by live HLS, prepared playback,
// and the Watch timeline (§9.1 V60). Media remains one bounded window per active
// (Channel, EncodePlan), never a private copy or encoder per viewer.
const DVRHorizon = 15 * time.Minute

// hlsWindow is the number of whole target-duration segments required to cover DVRHorizon.
// `delete_segments` plus the one-segment delete threshold bounds scratch just behind this window.
const hlsWindow = int(DVRHorizon/time.Second) / hlsSegmentDuration

// hlsRelayMaxBytes bounds the lossless startup queue. Only the current channel and its two adjacent
// warm candidates use HLS remuxes, so this does not scale with the guide's channel count. 128 MiB
// covers the ten-second readrate burst at more than 100 Mbit/s; if a child remains stalled beyond
// that, failing this remux cleanly is safer than either corrupting MPEG-TS or growing without bound.
const hlsRelayMaxBytes = 128 << 20

// hlsFirstSegmentTimeout is the UPPER BOUND on waiting for the first playlist to appear — a
// failsafe, not the expected wait. The normal case returns in a few seconds (the session warms up,
// ffmpeg writes the first segment + playlist, we serve it). This bound only fires when a channel is
// genuinely stuck (the session never produced bytes), in which case failing cleanly beats hanging
// the request forever. Deliberately generous, because the cost of it being slightly too short is a
// spurious failure on a channel that was about to work.
//
// ⚠ Sized for the SLOWEST cold start, which is a heavy transcode, not a copy (§9.1 V47 made this
// real). A cold browser session for an HEVC 1080p channel does a whole chain before the first
// keyframe-aligned segment can be cut: mux spin-up → supervisor opens the program URL → the
// program child's ffprobe → encoder init → enough transcoded frames to reach a keyframe → the
// `-c copy` remux segments on it. Measured on the dev box, that exceeded 20s on the FIRST attach
// (the session then warmed and every later attach was instant) — a real channel 502'd on the cold
// hit while working perfectly a second later. 45s covers that chain and still fails a genuinely
// dead channel inside a minute. A direct-play channel is unaffected: it returns in a few seconds
// as before, well under the bound.
const hlsFirstSegmentTimeout = 45 * time.Second

// firstSegmentPoll is how often the readiness wait re-checks for the playlist file. Short enough
// that the request returns promptly once ffmpeg writes it, not so short it spins the CPU.
const firstSegmentPoll = 100 * time.Millisecond

// HLSAttacher is the slice of Manager an HLS remux needs: register its bounded lossless sink for an
// EncodePlan. Named so hls.go depends on a capability, not the whole Manager, and so tests can drive
// the remux without a process. The remux attaches at the CLIENT's resolved
// plan (§9.1 V48): a baseline `<video>` gets HEVC/AC3 transcoded to h264/aac, while an HEVC-capable
// client gets it copied — the plan the Watch handler resolved from the DeviceProfile decides which.
type HLSAttacher interface {
	AttachSink(ctx context.Context, channelID string, plan EncodePlan, sink sessionSink) (sinkLease, error)
}

type hlsProcessCorrelator interface {
	ProcessRunID(channelID string, plan EncodePlan) string
}

// hlsSpawner starts the repackaging ffmpeg for a channel dir. A field on the manager
// (defaulting to startHLSFFmpeg) rather than a hard call, so the refcount and lifecycle can be
// tested without executing a binary — the same seam Manager's Spawner provides.
type hlsSpawner func(ctx context.Context, bin, dir string, plan EncodePlan, log *slog.Logger) (*hlsProcess, error)

// HLSManager owns the per-channel HLS remuxes. One per process, built beside the session Manager.
type HLSManager struct {
	attacher HLSAttacher
	ffmpeg   string
	log      *slog.Logger
	grace    time.Duration
	spawn    hlsSpawner

	// readyTimeout is how long a starting remux waits for its first segment. A field rather than
	// the constant used directly, for one reason: the NEGATIVE readiness cases (a header-only
	// playlist must never be reported ready) can only be asserted by letting the wait EXPIRE, and
	// at the production 45s that is a 45-second unit test. Same seam, and same justification, as
	// `spawn` above. Zero means hlsFirstSegmentTimeout.
	readyTimeout time.Duration

	// root is the base temp directory; each remux gets a subdir under it. Removed on Stop.
	root string

	mu sync.Mutex
	// Keyed by (channel, plan): a baseline (h264) remux and an HEVC-capable remux of the SAME channel
	// are distinct — they attach to different sessions with different copy plans, so each needs its
	// own segment dir and refcount (§9.1 V48). The tuner path does not go through here (it streams
	// MPEG-TS directly, streamHandler).
	remuxes map[remuxKey]*hlsRemux
}

// remuxKey identifies one HLS remux: a channel viewed at one EncodePlan. Comparable, so it is the
// map key directly — the same shape as session.go's sessionKey, and for the same reason (§9.1 V48).
type remuxKey struct {
	channel string
	plan    EncodePlan
}

// NewHLSManager builds the HLS repackager. `ffmpeg` is the same binary path playout encodes
// with; `attacher` is the session Manager. `baseDir` is where segment subdirs are created —
// EMPTY means the OS temp dir (`playout.hls_dir` unset, the default). Returns an error only if
// the root cannot be created — a real "playout cannot serve HLS" condition worth surfacing at
// boot, not at tune-in.
//
// The base is resolved ONCE at construction, not per-tune-in: the segment root is process-owned
// scratch, not a per-request choice, and re-reading it live would let an operator's mid-session
// edit strand live segments in the old directory. A changed `playout.hls_dir` takes effect on the
// next restart, like the ffmpeg path it sits beside.
func NewHLSManager(attacher HLSAttacher, ffmpeg, baseDir string, grace time.Duration, log *slog.Logger,
	observers ...*diagnostics.ProcessManager,
) (*HLSManager, error) {
	if grace <= 0 {
		grace = DefaultGrace
	}
	// A configured base must exist and be writable; MkdirTemp under it both creates our own
	// isolated subdir AND proves the base is usable, failing loudly at boot rather than at the
	// first viewer if the operator pointed it somewhere unwritable. Empty baseDir → the OS temp
	// dir (MkdirTemp's own default), which is the documented default.
	if baseDir != "" {
		// Ensure the operator's chosen directory exists — a fresh install or a tmpfs path may
		// not yet. Best-effort: MkdirTemp below is the real check and reports the true error.
		_ = os.MkdirAll(baseDir, 0o755)
	}
	root, err := os.MkdirTemp(baseDir, "loomarr-hls-")
	if err != nil {
		return nil, fmt.Errorf("hls: segment root under %q: %w", baseDir, err)
	}
	spawn := hlsSpawner(startHLSFFmpeg)
	if len(observers) > 0 && observers[0] != nil {
		manager := observers[0]
		spawn = func(ctx context.Context, bin, dir string, plan EncodePlan, log *slog.Logger) (*hlsProcess, error) {
			return startHLSFFmpegObserved(ctx, bin, dir, plan, log, manager)
		}
	}
	return &HLSManager{
		attacher: attacher, ffmpeg: ffmpeg, grace: grace, log: log,
		spawn:   spawn,
		root:    root,
		remuxes: map[remuxKey]*hlsRemux{},
	}, nil
}

// Playlist returns the absolute path to a channel's live `.m3u8`, starting the remux if it is not
// already running, and a detach func the caller MUST invoke when the viewer leaves — it is what
// decrements the refcount that keeps the remux (and, through it, the session) alive.
//
// The find-or-create is under the manager lock for the same reason Attach's is (session.go): two
// viewers tuning the same channel together must find one remux, not race two into existence with
// one orphaned.
func (m *HLSManager) Playlist(channelID string, plan EncodePlan) (playlistPath string, detach func(), err error) {
	key := remuxKey{channel: channelID, plan: plan}
	m.mu.Lock()
	if r := m.remuxes[key]; r != nil {
		if p, d, ok := r.addViewer(); ok {
			m.mu.Unlock()
			// A shared remux may still be starting. The first caller waits below, but a later HLS
			// poll can join before ffmpeg has written its first segment. Every caller must cross the
			// same readiness gate; returning the path here races Origin's read and produces an
			// immediate 502 even though the remux becomes healthy moments later.
			if werr := r.awaitPlaylist(m.readyWait()); werr != nil {
				d()
				return "", nil, werr
			}
			return p, d, nil
		}
		// Mid-teardown: drop and start fresh below rather than joining something dying.
		delete(m.remuxes, key)
	}
	r, err := m.start(channelID, plan)
	if err != nil {
		m.mu.Unlock()
		return "", nil, err
	}
	m.remuxes[key] = r
	p, d, _ := r.addViewer() // fresh remux: cannot be closed
	m.mu.Unlock()

	// Wait BRIEFLY for the first playlist to exist, then serve it. This is the INDUSTRY-STANDARD
	// live-HLS origin behaviour — hold the first manifest request until a media segment exists,
	// rather than serving an empty playlist or a 404. Apple's HLS spec requires a live playlist to
	// list playable media, and every live packager (ffmpeg, Shaka, Wowza, MediaLive, Mux) and media
	// server (Emby/Jellyfin) either holds the first request until ready or answers 503+Retry-After;
	// none hand the client a 404. This is the reconciliation of two things that seemed to conflict:
	//
	//   - "plays right away" — because the wait is SHORT (~ the time to buffer one segment), not the
	//     15s-then-502 an earlier version used, which is the version that was actually broken.
	//   - "never a 404 race" — because the alternative (return immediately) hands the client a URL
	//     that 404s for the few seconds until ffmpeg writes the first segment. Real clients handle
	//     that poorly: hls.js exhausts its manifest retries against the 404 and gives up before the
	//     file appears. Waiting here means the client's FIRST fetch gets a valid playlist with
	//     segments, exactly as a live-HLS origin is supposed to behave.
	//
	// Copy-repackage produces the first segment in about one keyframe interval, but the SESSION
	// upstream can take a few seconds to warm up (encoder spin-up, seek), so the wait is generous
	// enough to cover that and still bounded so a genuinely stuck channel fails cleanly rather than
	// hanging the request forever.
	if werr := r.awaitPlaylist(m.readyWait()); werr != nil {
		d() // release the refcount we just took; teardown fires if we were the only viewer
		return "", nil, werr
	}
	return p, d, nil
}

// AssetPath resolves an HLS segment (`seg-N.ts`, referenced by the live playlist) to its on-disk
// path for a channel, if that remux is live. Returns ok=false when the channel has no running
// remux (so the handler 404s rather than serving from a stale directory) OR when the cleaned
// relative path would escape the channel's dir — a defence in depth behind the handler's own name
// validation.
//
// `rel` is the file UNDER the channel dir (e.g. "seg-3.ts"). It is joined beneath the channel dir
// and the result is verified to stay inside.
func (m *HLSManager) AssetPath(channelID string, plan EncodePlan, rel string) (string, bool) {
	m.mu.Lock()
	r := m.remuxes[remuxKey{channel: channelID, plan: plan}]
	m.mu.Unlock()
	if r == nil {
		return "", false
	}
	// Clean and confirm containment: a joined path that does not have the channel dir as a
	// prefix is a traversal attempt, refused regardless of what the handler checked.
	full := filepath.Join(r.dir, filepath.Clean("/"+rel))
	if full != r.dir && !strings.HasPrefix(full, r.dir+string(os.PathSeparator)) {
		return "", false
	}
	return full, true
}

// start launches a remux for a channel at one EncodePlan. Caller holds m.mu.
func (m *HLSManager) start(channelID string, plan EncodePlan) (*hlsRemux, error) {
	dir, err := os.MkdirTemp(m.root, "ch-")
	if err != nil {
		return nil, fmt.Errorf("hls: channel dir: %w", err)
	}
	relay := newHLSRelay()

	// context.Background, NOT a viewer's request — the remux outlives whoever started it, like
	// the session it wraps. Teardown cancels this explicitly.
	ctx, cancel := context.WithCancel(context.Background())

	if m.log != nil {
		m.log.Info("hls: starting remux", "channel", channelID, "plan", plan.String(), "dir", dir, "ffmpeg", m.ffmpeg)
	}
	// Attach to the session for THIS client's resolved plan (§9.1 V48). PlanBaseline transcodes
	// HEVC/AC3 to the h264/aac a `<video>` decodes; PlanHEVC8/10 copies HEVC for a client that
	// advertised it. For an h264 channel EVERY plan is just `-c copy` (same bytes the tuner gets), so
	// this costs no extra encode there — only genuinely incompatible content pays, and only while
	// watched. The remux ffmpeg is `-c copy` regardless of plan — it re-containers whatever the
	// session hands it (HEVC-in-TS included; hls.js ≥1.6 transmuxes that client-side).
	sessLease, err := m.attacher.AttachSink(ctx, channelID, plan, relay)
	if err != nil {
		cancel()
		relay.abort(err)
		_ = os.RemoveAll(dir)
		if m.log != nil {
			m.log.Warn("hls: attach failed", "channel", channelID, "plan", plan.String(), "err", err)
		}
		return nil, err // ErrAtCapacity flows straight through — the caller renders 503.
	}
	if m.log != nil {
		m.log.Info("hls: attached to session", "channel", channelID, "plan", plan.String())
	}

	key := remuxKey{channel: channelID, plan: plan}
	r := &hlsRemux{
		channelID: channelID, dir: dir,
		playlist: filepath.Join(dir, hlsPlaylistName),
		cancel:   cancel, sessDetach: sessLease.Release,
		setSessionActive: sessLease.SetActive,
		relay:            relay,
		grace:            m.grace, log: m.log,
		onClosed: func() { m.remove(key) },
	}

	// Direct-play: the remux stream-copies the session's bytes into HLS. No renditions, no
	// transcode — one encode per channel is the SESSION's; the remux only re-containers it.
	parentRunID := ""
	if correlator, ok := m.attacher.(hlsProcessCorrelator); ok {
		parentRunID = correlator.ProcessRunID(channelID, plan)
	}
	ctx = diagnostics.WithProcessSpec(ctx, diagnostics.ProcessSpec{
		Purpose: "hls_remux", ParentRunID: parentRunID, ChannelID: channelID, Target: plan.String(),
	})
	proc, err := m.spawn(ctx, m.ffmpeg, dir, plan, m.log)
	if err != nil {
		cancel()
		sessLease.Release()
		relay.abort(err)
		_ = os.RemoveAll(dir)
		if m.log != nil {
			m.log.Warn("hls: spawn ffmpeg failed", "channel", channelID, "err", err)
		}
		return nil, err
	}
	r.proc = proc
	if m.log != nil {
		m.log.Info("hls: remux ffmpeg spawned", "channel", channelID)
	}

	// Feed the registered relay into ffmpeg. It ends when the session closes the sink (encoder
	// gone), ffmpeg exits, or teardown cancels the shared lifetime.
	go r.writeRelay()
	return r, nil
}

// remove drops a (channel, plan) remux from the map after its own teardown. Called by the remux's
// onClosed once the grace period has expired and it has released the session.
func (m *HLSManager) remove(key remuxKey) {
	m.mu.Lock()
	delete(m.remuxes, key)
	m.mu.Unlock()
}

// StopChannel tears down every live HLS remux for channelID, across every codec plan. Removing
// the entries before teardown makes follow-up asset requests fail closed immediately; teardown
// then cancels ffmpeg and releases each remux's underlying MPEG-TS session reference.
func (m *HLSManager) StopChannel(channelID string) {
	m.mu.Lock()
	remuxes := make([]*hlsRemux, 0, 2)
	for key, r := range m.remuxes {
		if key.channel == channelID {
			remuxes = append(remuxes, r)
			delete(m.remuxes, key)
		}
	}
	m.mu.Unlock()

	for _, r := range remuxes {
		r.teardown()
	}
}

// StopAll tears down every live remux but leaves the scratch root reusable. It is the fail-closed
// lifecycle path while a Postgres replica re-establishes its durable invalidation listener.
func (m *HLSManager) StopAll() {
	m.mu.Lock()
	remuxes := make([]*hlsRemux, 0, len(m.remuxes))
	for _, r := range m.remuxes {
		remuxes = append(remuxes, r)
	}
	m.remuxes = map[remuxKey]*hlsRemux{}
	m.mu.Unlock()

	for _, r := range remuxes {
		r.teardown()
	}
}

// Stop tears down every remux and removes the temp root. Called on shutdown.
func (m *HLSManager) Stop() {
	m.StopAll()
	_ = os.RemoveAll(m.root)
}

// hlsPlaylistName is the live media playlist ffmpeg writes and clients load — a rolling window of
// the direct-play segments beside it. Named to match the public route (/playout/hls/{id}/…) and
// the play-url the client is handed, so the on-disk file, the URL, and the mux pattern are one
// name with no mismatch.
const hlsPlaylistName = "master.m3u8"

// readyWait is the first-segment timeout, defaulting to the production bound.
func (m *HLSManager) readyWait() time.Duration {
	if m.readyTimeout > 0 {
		return m.readyTimeout
	}
	return hlsFirstSegmentTimeout
}

// hlsRemux is one channel's HLS repackaging: a `-c copy -f hls` ffmpeg fed by the session's
// bytes, writing segments a browser reads. Its viewer refcount mirrors Session's.
type hlsRemux struct {
	channelID string
	dir       string
	playlist  string

	cancel     context.CancelFunc
	sessDetach func() // release the session refcount — MUST run exactly once on teardown
	// setSessionActive distinguishes a live HLS client from a remux retained only for warmth.
	setSessionActive func(bool) bool
	proc             *hlsProcess
	relay            *hlsRelay
	log              *slog.Logger
	grace            time.Duration
	onClosed         func()

	mu             sync.Mutex
	viewers        int
	closed         bool
	graceStop      *time.Timer
	idleSince      time.Time
	idleGeneration uint64
}

// addViewer increments the refcount, returning the playlist path + a detach func. ok=false when
// the remux is already tearing down, so the caller starts a fresh one instead of joining a
// corpse (same guard as Session.addViewer).
func (r *hlsRemux) addViewer() (string, func(), bool) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return "", nil, false
	}
	if r.setSessionActive != nil && !r.setSessionActive(true) {
		r.mu.Unlock()
		return "", nil, false
	}
	// A newcomer cancels a pending grace teardown: the channel is wanted again.
	if r.graceStop != nil {
		r.graceStop.Stop()
		r.graceStop = nil
	}
	r.viewers++
	r.idleSince = time.Time{}
	r.idleGeneration++
	var once sync.Once
	detach := func() { once.Do(r.removeViewer) }
	r.mu.Unlock()
	return r.playlist, detach, true
}

// removeViewer decrements the refcount and arms the grace timer when it hits zero. The remux
// keeps running through the grace window so a viewer reloading the page (or hls.js re-fetching
// after a stall) does not pay a fresh ffmpeg start.
func (r *hlsRemux) removeViewer() {
	r.mu.Lock()
	if r.closed || r.viewers == 0 {
		r.mu.Unlock()
		return
	}
	r.viewers--
	if r.viewers > 0 {
		r.mu.Unlock()
		return
	}
	r.idleSince = time.Now()
	r.idleGeneration++
	idleGeneration := r.idleGeneration
	r.graceStop = time.AfterFunc(r.grace, func() {
		r.teardownIfIdle(idleGeneration)
	})
	if r.setSessionActive != nil {
		r.setSessionActive(false)
	}
	r.mu.Unlock()
}

// teardown stops ffmpeg, releases the session refcount, removes the segment dir, and notifies the
// manager. Idempotent: closed is set under the lock so a manager Stop racing a grace timer runs
// the body once.
func (r *hlsRemux) teardown() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	if r.graceStop != nil {
		r.graceStop.Stop()
		r.graceStop = nil
	}
	r.mu.Unlock()
	r.finishTeardown()
}

func (r *hlsRemux) teardownIfIdle(expectedGeneration uint64) bool {
	r.mu.Lock()
	if r.closed || r.viewers != 0 || r.idleSince.IsZero() || r.idleGeneration != expectedGeneration {
		r.mu.Unlock()
		return false
	}
	r.closed = true
	if r.graceStop != nil {
		r.graceStop.Stop()
		r.graceStop = nil
	}
	r.mu.Unlock()
	r.finishTeardown()
	return true
}

func (r *hlsRemux) finishTeardown() {
	r.relay.abort(context.Canceled) // wakes either side of the queue before process teardown
	r.cancel()                      // stops the ffmpeg (context-bound, like every playout process)
	r.sessDetach()                  // decrement the SESSION refcount — the channel's encoder can now idle out
	_ = r.proc.wait()               // reap before removing the dir so nothing writes into it after
	_ = os.RemoveAll(r.dir)
	if r.onClosed != nil {
		r.onClosed()
	}
}

// The relay decouples the shared MPEG-TS producer from ffmpeg's stdin so the session never sees the
// remux "fall behind" during its finite startup burst.
//
// ⚠ THE BUG THIS FIXES (found live, and it cost real time): the pump used to read a chunk from the
// session channel and SYNCHRONOUSLY write it to ffmpeg's stdin. But the remux ffmpeg's stdin pipe
// backs up during startup (it is initialising the HLS muxer), so the write blocks, the pump stops
// reading the session channel, the session's small per-viewer buffer (viewerBuffer × 64KB ≈ 512KB)
// fills in a burst, and the session — whose policy is "drop a viewer that falls behind", correct
// for a genuinely stalled TV — DROPS THE REMUX. The channel then closes with no segment written:
// a black frame. The remux is not a slow client; it is a pipe with normal backpressure, and it
// must not be dropped for it.
//
// writeRelay drains the startup relay into ffmpeg at the process's pace. A closed relay means the
// upstream session ended; a write error means ffmpeg ended. Either way this remux is terminal.
func (r *hlsRemux) writeRelay() {
	defer func() { _ = r.proc.closeStdin() }()
	for {
		chunk, err := r.relay.next()
		if err != nil {
			go r.teardown()
			return
		}
		if _, err := r.proc.stdin.Write(chunk); err != nil {
			r.relay.abort(err)
			go r.teardown() // ffmpeg went away
			return
		}
	}
}

// hlsRelay is a private, bounded lossless queue between the session fan-out and ffmpeg's stdin. A warm
// session can release several seconds of media before the child has finished starting; buffering
// that burst in a fixed-size Go channel either blocks Manager (which drops the viewer) or discards
// MPEG-TS packets (which corrupts the remux). offer only appends a slice under a mutex, so Manager
// installs and feeds this sink synchronously without a scheduler-sized mailbox race.
type hlsRelay struct {
	mu   sync.Mutex
	cond *sync.Cond

	queue    [][]byte
	bytes    int64
	closed   bool
	err      error
	maxBytes int64
}

func newHLSRelay() *hlsRelay {
	relay := &hlsRelay{maxBytes: hlsRelayMaxBytes}
	relay.cond = sync.NewCond(&relay.mu)
	return relay
}

func (q *hlsRelay) offer(chunk []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	if int64(len(chunk)) > q.maxBytes-q.bytes {
		q.abortLocked(fmt.Errorf("hls: startup queue exceeded %d bytes", q.maxBytes))
		return false
	}
	q.queue = append(q.queue, chunk)
	q.bytes += int64(len(chunk))
	q.cond.Signal()
	return true
}

func (q *hlsRelay) next() ([]byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.queue) == 0 && !q.closed {
		q.cond.Wait()
	}
	if q.err != nil {
		return nil, q.terminalError()
	}
	if len(q.queue) == 0 {
		return nil, io.EOF
	}
	chunk := q.queue[0]
	q.queue[0] = nil
	q.queue = q.queue[1:]
	q.bytes -= int64(len(chunk))
	if len(q.queue) == 0 {
		q.queue = nil
	}
	return chunk, nil
}

func (q *hlsRelay) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	q.cond.Broadcast()
}

func (q *hlsRelay) abort(err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.abortLocked(err)
}

func (q *hlsRelay) abortLocked(err error) {
	q.closed = true
	q.err = err
	q.queue = nil
	q.bytes = 0
	q.cond.Broadcast()
}

func (q *hlsRelay) terminalError() error {
	if q.err != nil {
		return q.err
	}
	return io.EOF
}

// awaitPlaylist blocks until the media playlist lists at least one SEGMENT, or the timeout elapses.
//
// ⚠ Waiting for the file to merely EXIST is not enough, and getting this wrong was a real bug:
// ffmpeg writes the `.m3u8` HEADER first and appends `#EXTINF`/segment lines only once the first
// segment is flushed. A playlist served in that window has no playable media — hls.js parses it,
// finds nothing to fetch, and stalls (or exhausts its retries). Apple's HLS spec requires a live
// playlist to list playable media, so the readiness signal is "a segment is referenced", detected
// here by the presence of a segment line, not the file's existence.
//
// ⚠ **The predicate must not name a CONTAINER.** It was `bytes.Contains(body, ".ts")` from the day
// the readiness gate was written until 2026-08-09, which silently stopped working the moment
// hlsArgs learned to emit fMP4: PlanHEVC8/HEVC10/Full write `seg-%d.m4s` with an `init.mp4`, so
// their playlist contains no `.ts` anywhere — and segment URIs are basenames, so the directory path
// cannot smuggle one in either. Every HEVC-capable client (Safari, native apps — exactly the
// audience V48 added the plan for) therefore waited the full 45s and got a 502.
//
// Every OTHER fMP4 layer had been thought through: the asset handler branches on `.m4s`/`.mp4`, and
// rewritePlaylistAuth special-cases `#EXT-X-MAP:URI=` with its own test. Only this line was missed,
// and no unit test could see it — hls_refcount_test.go writes a `.ts` stub "so awaitPlaylist
// succeeds" regardless of plan.
//
// `#EXTINF` is the container-independent answer, and it is the RIGHT answer rather than a wider
// net: the spec defines it as the tag that precedes a media segment URI, so it is present exactly
// when a playable segment is listed. Deliberately NOT `#EXT-X-MAP`, which fMP4 writes with the init
// segment BEFORE any media exists — matching it would reintroduce the header-only stall this
// function was written to prevent, on the very path that just broke.
func (r *hlsRemux) awaitPlaylist(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if body, err := os.ReadFile(r.playlist); err == nil && bytes.Contains(body, []byte("#EXTINF")) {
			return nil
		}
		if r.proc.done != nil {
			select {
			case <-r.proc.done:
				// Remove the dead remux before returning so the client's retry starts a fresh process
				// instead of rejoining this corpse for the remainder of the grace window.
				r.teardown()
				if last := r.proc.LastError(); last != "" {
					return fmt.Errorf("hls: channel %s remux exited before producing a stream: %s", r.channelID, last)
				}
				return fmt.Errorf("hls: channel %s remux exited before producing a stream: %v", r.channelID, r.proc.waitErr)
			default:
			}
		}
		if time.Now().After(deadline) {
			// Include ffmpeg's last stderr line — the WHY behind "no stream". Without it this
			// error is a bare timeout that sends diagnosis in the wrong direction.
			if last := r.proc.LastError(); last != "" {
				return fmt.Errorf("hls: channel %s produced no stream within %s: %s", r.channelID, timeout, last)
			}
			return fmt.Errorf("hls: channel %s produced no stream within %s", r.channelID, timeout)
		}
		time.Sleep(firstSegmentPoll)
	}
}

// hlsProcess is a minimal ffmpeg wrapper for the remux: it reads MPEG-TS on stdin and writes
// segments to disk, so unlike playout.Process it exposes stdin rather than stdout. Kept here
// rather than generalizing Process, whose whole contract is "Stdout is the caller's to read".
type hlsProcess struct {
	stdin io.WriteCloser
	log   *slog.Logger
	proc  *proctree.Supervisor
	run   *diagnostics.ProcessHandle
	done  chan struct{}

	mu       sync.Mutex
	lastErr  string
	waitOnce sync.Once
	waitErr  error
}

func (p *hlsProcess) closeStdin() error { return p.stdin.Close() }
func (p *hlsProcess) wait() error {
	if p.done == nil { // test-injected process wrappers predate the asynchronous constructor.
		p.waitOnce.Do(func() { p.waitErr = p.proc.Wait(); p.finishRun(p.waitErr) })
		return p.waitErr
	}
	<-p.done
	return p.waitErr
}

func newHLSProcess(proc *proctree.Supervisor, stdin io.WriteCloser, log *slog.Logger) *hlsProcess {
	return newObservedHLSProcess(proc, stdin, log, nil, nil)
}

func newObservedHLSProcess(proc *proctree.Supervisor, stdin io.WriteCloser, log *slog.Logger,
	run *diagnostics.ProcessHandle, stderr io.Reader,
) *hlsProcess {
	p := &hlsProcess{proc: proc, stdin: stdin, log: log, done: make(chan struct{}), run: run}
	stderrDone := make(chan struct{})
	if stderr == nil {
		close(stderrDone)
	} else {
		go func() { defer close(stderrDone); p.readStderr(stderr) }()
	}
	go func() {
		err := p.proc.Wait()
		<-stderrDone
		p.waitOnce.Do(func() {
			p.waitErr = err
			p.finishRun(err)
		})
		close(p.done)
	}()
	return p
}

func (p *hlsProcess) finishRun(err error) {
	if p.run != nil {
		p.run.Finish(diagnostics.ProcessResult{
			Err: err, Cancelled: p.proc.Stopped(), TerminationReason: hlsTerminationReason(p.proc.Stopped()),
		})
	}
}

// LastError returns ffmpeg's most recent stderr line — the actionable reason a remux produced no
// stream ("Device creation failed", a filter parse error). Retained like Process.LastError does,
// because a repackage that fails silently is the exact class of bug that makes a channel sit black.
func (p *hlsProcess) LastError() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErr
}

// readStderr drains ffmpeg's stderr, doing two things with each line: it EMITS it to our debug
// logs (so the remux is observable during normal operation, tagged so it can be grepped from the
// session encoder's lines), and it retains the last line for the failure message. Same shape as
// Process.readStderr — ffmpeg is chatty and long-lived, so all of it at Debug and only the tail
// kept in memory.
func (p *hlsProcess) readStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		p.mu.Lock()
		p.lastErr = line
		p.mu.Unlock()
		if p.run != nil {
			p.run.RecordOutput(line)
		}
		if p.log != nil {
			p.log.Debug("hls ffmpeg", "line", line)
		}
	}
}

// startHLSFFmpeg launches the DIRECT-PLAY repackager: one ffmpeg reading the shared MPEG-TS on
// stdin and stream-COPYING it into a live HLS playlist (hlsArgs). Under the same platform
// process-tree owner as Process, so cancellation cannot orphan a helper.
func startHLSFFmpeg(ctx context.Context, bin, dir string, plan EncodePlan, log *slog.Logger) (*hlsProcess, error) {
	return startHLSFFmpegObserved(ctx, bin, dir, plan, log, nil)
}

func startHLSFFmpegObserved(ctx context.Context, bin, dir string, plan EncodePlan, log *slog.Logger,
	manager *diagnostics.ProcessManager,
) (*hlsProcess, error) {
	args := hlsArgs(dir, plan)
	cmd := exec.Command(bin, args...) //nolint:gosec // args built here, never user text

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("hls: stdin pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("hls: stderr pipe: %w", err)
	}

	supervised, err := proctree.Start(ctx, cmd)
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("hls: start ffmpeg: %w", err)
	}
	var run *diagnostics.ProcessHandle
	if manager != nil {
		spec, _ := diagnostics.ProcessSpecFromContext(ctx)
		spec.Executable, spec.Args = bin, args
		run = manager.Begin(spec)
	}
	p := newObservedHLSProcess(supervised, stdin, log, run, stderr)
	return p, nil
}

func hlsTerminationReason(stopped bool) string {
	if stopped {
		return "process tree stopped"
	}
	return ""
}

// hlsArgs builds the DIRECT-PLAY repackage command: `-c copy -f hls`, a single live playlist. Split
// out so a test can assert the flags without running ffmpeg.
//
// DIRECT PLAY, no transcode. The session already produces the channel's normalized H.264 MPEG-TS
// (§9.1), so the remux only re-containers it into HLS segments a browser can fetch — near-zero CPU,
// and the first segment lands in ~one keyframe interval, so a live channel plays right away. There
// is deliberately NO ABR ladder here: transcoding lower renditions needs a cold encoder spin-up
// that delayed first paint for many seconds, which for a LIVE channel is the wrong trade. (ABR can
// return later as an enhancement that never blocks first paint; for now, live = copy.)
//
// LIVE, NOT VOD: `delete_segments` prunes old files so a day-long channel does not fill the disk;
// `omit_endlist` keeps players from thinking the stream ended; `-hls_allow_cache 0` stops segments
// being cached like VOD.
func hlsArgs(dir string, plan EncodePlan) []string {
	base := []string{
		"-hide_banner", "-loglevel", "error",
		// Input is the raw MPEG-TS on stdin. `-f mpegts` names it explicitly rather than making
		// ffmpeg probe a pipe it cannot seek.
		"-f", "mpegts", "-i", "pipe:0",
		"-c", "copy",
		"-f", "hls",
		"-hls_time", strconv.Itoa(hlsSegmentDuration),
		"-hls_list_size", strconv.Itoa(hlsWindow),
		"-hls_delete_threshold", "1",
		"-hls_allow_cache", "0",
	}

	// ⚠ HEVC HLS MUST be fMP4, not MPEG-TS (Apple's HLS authoring spec, and how Emby/Jellyfin do
	// HEVC direct-play). A `<video>`/MSE will NOT play HEVC muxed in an MPEG-TS segment — it black-
	// screens even on a browser that decodes HEVC. So the HEVC plans emit fragmented-MP4 segments
	// (`.m4s`) with an init segment (`init.mp4`, referenced by #EXT-X-MAP that ffmpeg writes), and
	// ffmpeg's fMP4 muxer signals the codec as `hvc1` in the playlist CODECS so the browser knows to
	// use its HEVC decoder. `-c copy` still copies the HEVC bitstream untouched — this is a REMUX
	// (container swap TS→fMP4), not a transcode.
	//
	// The BASELINE plan stays MPEG-TS: it is h264, which plays in TS everywhere, and TS is what has
	// shipped and is proven for the first-paint path (§9.1 V47). Only the HEVC plans pay the fMP4
	// path, and only while an HEVC-capable client is watching.
	if plan == PlanHEVC8 || plan == PlanHEVC10 || plan == PlanFull {
		return append(base,
			// ⚠ Two bitstream fixes REQUIRED for a TS→fMP4 remux, or the mux fails and the stream
			// black-screens (both verified by feeding a real HEVC clip through this exact command):
			//
			//  - `-bsf:a aac_adtstoasc`: AAC in MPEG-TS is ADTS-framed; the MP4/fMP4 muxer needs it as
			//    raw AAC with an ASC in the sample description. Without this the muxer REJECTS every
			//    audio packet ("Operation not permitted: Error muxing a packet") and the whole mux
			//    dies with exit 255 — no segments, black screen. This is the classic TS→MP4 gotcha.
			//  - `-tag:v hvc1`: ffmpeg tags copied HEVC as `hev1` (codec config in-band) by default,
			//    but MSE/Safari only reliably decode `hvc1` (config in the sample description). Forcing
			//    the tag is what makes the browser use its HEVC decoder instead of showing black.
			"-bsf:a", "aac_adtstoasc",
			"-tag:v", "hvc1",
			"-hls_flags", "delete_segments+independent_segments+omit_endlist+program_date_time",
			"-hls_segment_type", "fmp4",
			"-hls_fmp4_init_filename", "init.mp4",
			"-hls_segment_filename", filepath.Join(dir, "seg-%d.m4s"),
			filepath.Join(dir, hlsPlaylistName),
		)
	}
	return append(base,
		"-hls_flags", "delete_segments+independent_segments+omit_endlist+program_date_time",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(dir, "seg-%d.ts"),
		filepath.Join(dir, hlsPlaylistName),
	)
}
