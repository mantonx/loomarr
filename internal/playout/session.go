package playout

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// One encoder per channel, N viewers (§9.1; prior-art §6.3).
//
// The unit of work is a CHANNEL, not a viewer. A channel is a wall clock — everyone
// watching it at 20:15 sees the same frame — so encoding it once and fanning the bytes out
// is not an optimization, it is the only model that is even correct. Per-viewer encodes
// would drift apart, and three people watching one channel would cost three encoders.
//
// That inverts the usual VOD shape, where each viewer legitimately has their own position
// and their own transcode. viewra is a VOD transcoder, and its session manager EVICTED
// other sessions to make room when it hit its limit (prior-art, viewra §1) — behaviour
// which for playout means one person tuning in kills someone else's channel. Here active
// viewers are never eviction candidates: admission first retires the least-recently-viewed
// grace-idle transcode, then returns `AtCapacity` if every costly session is still wanted.
//
// Three failure modes get explicit machinery below, because each is easy to write wrong
// and none of them fails loudly:
//
//  1. The same-key start race — two viewers arriving together must not start two encoders.
//  2. Grace-period teardown — a channel-surfing TV must not pay for a fresh encoder start.
//  3. The ABA problem inside that grace timer — see stopAfterGrace.

// ErrAtCapacity is returned when a new transcode would exceed the effective measured budget.
//
// A distinct error because the API must render it as 503 with an actionable message, not as
// a generic failure: the operator's fixes (wait for a slot, relax a lowering-only safety cap,
// or choose a lower quality tier) are only discoverable if we say which wall was hit.
var ErrAtCapacity = errors.New("playout: at channel capacity")

// viewerBuffer is how many chunks a single slow viewer may fall behind before it is
// dropped.
//
// Sized in CHUNKS, not bytes, because that is the unit the fan-out moves. Deliberately
// small: a viewer this far behind is not going to recover, and a bigger buffer only delays
// the drop while holding more memory per stalled TV.
const viewerBuffer = 8

// warmIdleSessionLimit is the complete process-wide speculative/retained live hot set. The Watch
// controller warms only the previous and next Channels beside current, so retaining more than two
// proven-warm idle (Channel, EncodePlan) sessions buys no product latency while keeping their parent
// and HLS process/file-descriptor/scratch cost alive. Viewer-active sessions are outside this limit.
//
// This is deliberately an internal invariant rather than a setting or constructor argument: callers
// request playback and report demand; they do not arbitrate the Manager's lifecycle policy.
const warmIdleSessionLimit = 2

// Session is one channel's encoder plus its connected viewers.
type Session struct {
	ChannelID string
	// Plan is the EncodePlan (codec bucket) this session encodes for (§9.1 V47, V48) — part of the
	// session's identity, not a viewer attribute. Two sessions can share a ChannelID and differ only
	// here (a baseline one transcoding HEVC, an hevc8/full one copying it). Reported on SessionStat so
	// the dashboard shows which audience an encoder serves.
	Plan EncodePlan

	// cost is this session's contribution to the manager's committedCost — 1 if it TRANSCODES video,
	// 0 if it `-c copy`s (§9.1 V49 admission). It starts from the tune-time estimate and transitions
	// atomically to each real program's cost before that child starts; progress reports repeat the
	// transition idempotently for legacy callers.
	// Read/written under Manager.mu (the manager owns the budget accounting), not the session mu.
	cost int

	// cancel stops the encoder. The context IS the lifetime (process.go) — there is no
	// separate "stop the ffmpeg" path that could disagree with it. nil until the spawn completes
	// (see ready): a placeholder is in the map before its ffmpeg exists.
	cancel context.CancelFunc
	proc   *Process
	log    *slog.Logger

	// ready is closed once the encoder spawn resolves (success OR initErr). It lets the manager
	// insert a placeholder session under m.mu, release the lock, and spawn ffmpeg OUTSIDE it, while
	// concurrent same-key viewers wait here for the one spawn instead of starting a second encoder.
	// A nil ready means a fully-spawned session (the fake-spawner test path builds these directly).
	ready chan struct{}
	// initErr is the spawn failure, if any — read only after ready is closed. Set once by
	// spawnPlaceholder; never mutated afterwards, so no lock is needed for it.
	initErr error
	// grace is how long this session survives its last viewer. Copied from the manager
	// so onIdle does not need to reach back through a parent pointer (which would also
	// mean taking two locks in an order that has to stay consistent).
	grace time.Duration
	// onClosed tells the manager this session ended, so the dashboard learns a channel
	// stopped. Copied down for the same reason as `grace`: a parent pointer would mean
	// reaching back through the manager's lock from inside the session's.
	onClosed func()
	// onDemandChange publishes active↔idle viewer transitions after the session lock is released.
	onDemandChange func()
	// onBecameIdle lets the Manager enforce its process-wide warm-session invariant after the
	// session lock is released. Separate from onDemandChange because active transitions need
	// telemetry but cannot make the idle set exceed its limit.
	onBecameIdle func()

	// startedAt is when the channel came on air — the dashboard's uptime, and the denominator
	// for judging whether a low speed is a cold start or a sustained problem.
	startedAt time.Time
	// coldStartMs is the wall-clock from session start to the FIRST bytes the parent produced —
	// the "time to first frame" the viewer waits through as a black screen (§9.1 V47 doctor). The
	// one number that tracks the black-screen symptom directly; 0 until the first bytes arrive.
	// Written once in pump under mu, read in stat().
	coldStartMs int64
	// hasTransport distinguishes a genuinely warm session from a process that started but never
	// emitted a byte. coldStartMs cannot do that: a healthy first read may complete in under 1ms and
	// legitimately round to zero. Only proven transport earns the idle grace window.
	hasTransport bool
	// encoder is the hardware/software choice the CURRENT program resolved (§9.1). The single
	// most actionable telemetry an operator has: it is the difference between four concurrent
	// streams and one.
	//
	// ⚠ Reported from OUTSIDE, by the per-program child, not captured at session start. The
	// session's own ffmpeg is the `-c copy` mux fed by the block supervisor; it never encodes.
	// Its speed would measure remuxing throughput and its
	// "encoder" would be copy, so sourcing telemetry from it would put a confident,
	// meaningless number on the dashboard. Encoding happens in the per-program children the
	// supervisor requests. The session format is pinned, while the selected hardware engine may
	// legitimately change after a child falls back.
	encoder Encoder

	mu      sync.Mutex
	viewers map[int]sessionViewer
	// inactiveViewers are internal sinks retained only to keep a delivery adapter warm. They still
	// receive bytes, but do not represent current viewer demand and are eligible for idle eviction.
	inactiveViewers map[int]bool
	nextID          int
	// idleSince is nonzero only while this proven-warm session has no viewers. Admission uses it to
	// reclaim the least-recently-viewed idle transcode before refusing a foreground tune.
	idleSince      time.Time
	idleGeneration uint64
	// last is the most recent progress sample from the CURRENT program's encoder (see
	// `encoder` above for why it is not the session's own process). The parser has always run
	// and every caller passed `nil` for its callback, so each sample was parsed and discarded —
	// the telemetry the dashboard needs was already being produced and had nowhere to go.
	//
	// Guarded by the same mutex as `viewers`: both are read together to build a snapshot,
	// and a second lock would be one more ordering to keep consistent for no gain.
	last Progress
	// closed guards against a viewer attaching to a session that is already tearing
	// down. Without it, a viewer could join between "the grace timer fired" and "the map
	// entry was deleted" and then wait forever on a stream nobody is writing to.
	closed bool
}

// SessionStat is a snapshot of one live encoder, for the dashboard (§12, V16).
//
// A VALUE, not a pointer into the live session: the caller reads it without holding a lock
// and cannot accidentally mutate an encoder's state while it runs.
type SessionStat struct {
	ChannelID string `json:"channelId"`
	// Target names the codec audience this encoder serves — "browser" or "mediaserver" (§9.1 V47).
	// A channel can have one row per target; the dashboard shows both so an operator seeing two
	// encoders on one channel knows one is a browser transcode and one is a tuner copy.
	Target   string  `json:"target"`
	Viewers  int     `json:"viewers"`
	Encoder  string  `json:"encoder" doc:"Resolved encoder: hardware vendor, or 'software'"`
	Hardware bool    `json:"hardware" doc:"Whether the resolved encoder is hardware-accelerated"`
	Speed    float64 `json:"speed" doc:"Realtime multiple from ffmpeg; sustained <1.0 means the channel will stutter"`
	// BufferedMS is how far ahead of realtime the encoder has produced output — out_time
	// minus wall-clock elapsed. Negative means it is BEHIND, which is the same condition a
	// sub-1.0 speed reports, seen as accumulated deficit rather than an instantaneous rate.
	BufferedMS int64 `json:"bufferedMs"`
	UptimeMS   int64 `json:"uptimeMs"`
	// ColdStartMS is how long this channel took from session start to first frame — the black-screen
	// window a viewer waited through (§9.1 V47 doctor). 0 before the first bytes arrive.
	ColdStartMS int64 `json:"coldStartMs"`
	// TranscodeCost is this session's current video-transcode admission cost. Zero means the
	// programme copies video; one means it consumes one measured hardware/software encode slot.
	TranscodeCost int `json:"transcodeCost"`
}

// sessionKey identifies one live encoder. A channel does NOT have a single stream — it has one
// per codec AUDIENCE (§9.1 V47): a media-server tuner ingests HEVC, a browser needs h264, so the
// two get different copy/transcode plans and therefore different encoders. The key is the pair.
//
// A struct rather than a "channel|target" string: it is a comparable value Go maps accept
// directly, with no delimiter to escape and no parse to get wrong. For an h264 channel both
// targets' plans are `-c copy`, so the "second" session is a cheap remux, not a second encode —
// the split's cost is bounded by the copy plan, not the key (see §9.1).
type sessionKey struct {
	channel string
	plan    EncodePlan
}

// Manager owns the live sessions. One per process.
type Manager struct {
	// spawn starts an encoder. Injected so tests exercise the session lifecycle
	// (refcounting, grace, the race) without executing ffmpeg; the live test supplies
	// the real one.
	spawn Spawner

	log *slog.Logger

	// grace is how long a channel keeps encoding after its last viewer leaves.
	grace time.Duration
	// budget returns the CURRENT admission budget: how many concurrent VIDEO TRANSCODES this box can
	// sustain right now (§9.1 V49). A func, not a fixed int, because it is DYNAMIC — the measured
	// encoder capacity (Detect) shaded by live VRAM headroom (a resident LLM leaves room for fewer
	// hardware encodes) and capped by any operator override. Re-read on every admission so a settings
	// change or a model loading/unloading re-applies without a restart. <=0 ⇒ unmeasured, never block.
	budget func() int
	// estimateCost may prove a cold session will begin from an already prepared copy-only block.
	// It runs outside mu so independent Channel lookups remain parallel. Nil keeps the conservative
	// one-slot reservation until the first live program report.
	estimateCost func(context.Context, string, EncodePlan) int
	// committedCost is the summed admission cost of live sessions — the number of them currently
	// TRANSCODING video (a `-c copy` session costs 0). Compared against budget() to admit. Guarded by
	// mu; each session's contribution is tracked on the Session (cost) so a report/teardown adjusts it.
	committedCost int

	// onChange fires after the live-session set changes — a channel starting or stopping.
	// The composition root uses it to publish an SSE `playout` frame so the dashboard learns
	// about a stream within a frame rather than at the next poll (§8: SSE is the latency
	// path, the GET endpoint is truth).
	//
	// Fired WITHOUT the manager lock held. Publishing takes the bus's lock, and calling out
	// to arbitrary code while holding m.mu is how a deadlock gets built between two packages
	// that never mention each other.
	onChange func()
	observer SessionObserver

	mu       sync.Mutex
	sessions map[sessionKey]*Session
}

// SessionObserver receives bounded lifecycle facts without Channel or plan identity.
type SessionObserver interface {
	PlayoutSessionStarted(result string)
	PlayoutSessionActive(delta int)
	PlayoutProcessFailure(stage string)
}

// ProcessRunID returns the opaque diagnostic identity of one ready session, if observed.
func (m *Manager) ProcessRunID(channelID string, plan EncodePlan) string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	s := m.sessions[sessionKey{channel: channelID, plan: plan}]
	m.mu.Unlock()
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc == nil {
		return ""
	}
	return s.proc.ProcessRunID()
}

// OnChange registers the session-set change hook. Called once during composition, before any
// viewer can attach.
func (m *Manager) OnChange(fn func()) { m.onChange = fn }

// WithObserver binds one application generation's session lifecycle observer.
func (m *Manager) WithObserver(observer SessionObserver) *Manager {
	m.observer = observer
	return m
}

// WithCostEstimator installs a lookup-only cold-session cost proof. Only zero is a relaxation;
// every other result retains the conservative one-transcode reservation.
func (m *Manager) WithCostEstimator(estimate func(context.Context, string, EncodePlan) int) *Manager {
	m.estimateCost = estimate
	return m
}

// notifyChange fires the hook if one is registered. Never called with m.mu held.
func (m *Manager) notifyChange() {
	if m.onChange != nil {
		m.onChange()
	}
}

// Spawner starts an encoder for a channel and returns the supervised process. The
// implementation builds args from the resolved Airing and the load-aware Profile.
//
// It takes the plan as well as the channel (§9.1 V47), so every finite block is normalized for the
// same codec audience. Browser and tuner sessions can therefore serve the same channel differently
// without changing format inside either session.
type Spawner func(ctx context.Context, channelID string, plan EncodePlan) (*Process, error)

// NewManager builds a session manager.
//
// There is deliberately NO resolver here. The manager owns one long-lived copy mux per
// channel/plan; the block supervisor resolves "what is airing now" through /playout/program at
// each finite EOF. An earlier draft gave the manager a Resolver seam and nothing ever read it —
// dead surface whose only effect was a nil argument at the call site.
// budget returns the CURRENT admission budget (concurrent video transcodes this box can sustain);
// see the field doc. A nil budget means "unmeasured" — admission never blocks (Admit's budget<=0
// path), which is the safe default for a unit Manager built without capacity wiring.
func NewManager(spawn Spawner, budget func() int, grace time.Duration, log *slog.Logger) *Manager {
	if grace <= 0 {
		grace = DefaultGrace
	}
	if budget == nil {
		budget = func() int { return 0 }
	}
	return &Manager{
		spawn:  spawn,
		budget: budget, grace: grace, log: log,
		sessions: map[sessionKey]*Session{},
	}
}

// DefaultGrace is how long an encoder survives its last viewer.
//
// Long enough to absorb channel surfing and a client reconnecting after a network blip —
// both of which are common on a TV and both of which would otherwise pay the full encoder
// start cost (seek + ffmpeg init, seconds not milliseconds). Short enough that a genuinely
// abandoned channel stops burning a core promptly.
const DefaultGrace = 30 * time.Second

// Attach connects a viewer to a channel FOR A TARGET, starting the encoder if one is not already
// running for that (channel, target) pair (§9.1 V47). A tuner attaches as TargetMediaServer and a
// browser as TargetBrowser; they get separate sessions when the copy plan differs (HEVC) and
// separate-but-both-cheap `-c copy` sessions when it does not (h264).
//
// Returns a chunk channel and a detach func. The caller MUST call detach — it is what
// decrements the refcount, and a leaked viewer keeps a channel encoding forever.
func (m *Manager) Attach(ctx context.Context, channelID string, plan EncodePlan) (<-chan []byte, func(), error) {
	key := sessionKey{channel: channelID, plan: plan}
	for {
		s, err := m.acquire(ctx, key)
		if err != nil {
			return nil, nil, err
		}
		if ch, detach, ok := s.addViewer(); ok {
			return ch, detach, nil
		}
		m.discardClosed(key, s)
	}
}

// AttachSink registers an in-process consumer directly in the session fan-out. It exists for the
// HLS remux, whose input must absorb ffmpeg's finite readrate startup burst without crossing the
// eight-chunk mailbox intended for network viewers. The sink's offer is synchronous and must stay
// non-blocking; returning false drops only that sink, preserving the channel for every other viewer.
func (m *Manager) AttachSink(ctx context.Context, channelID string, plan EncodePlan, sink sessionSink) (sinkLease, error) {
	key := sessionKey{channel: channelID, plan: plan}
	for {
		s, err := m.acquire(ctx, key)
		if err != nil {
			return sinkLease{}, err
		}
		if lease, ok := s.addSink(sink); ok {
			return lease, nil
		}
		m.discardClosed(key, s)
	}
}

// sinkLease lets an in-process delivery adapter distinguish retained warmth from current viewer
// demand without exposing Session lifecycle machinery. A plain byte-stream viewer is always active.
type sinkLease struct {
	release   func()
	setActive func(bool) bool
}

func (l sinkLease) Release() {
	if l.release != nil {
		l.release()
	}
}

func (l sinkLease) SetActive(active bool) bool {
	if l.setActive == nil {
		return true
	}
	return l.setActive(active)
}

// acquire finds or starts the one session for a channel/plan. Viewer registration is deliberately
// outside this method so byte-channel viewers and the in-process HLS sink share all admission,
// spawn-race, grace, and failure handling rather than growing two subtly different managers.
func (m *Manager) acquire(ctx context.Context, key sessionKey) (*Session, error) {

	// ⚠ **The find-or-create is atomic, but the SPAWN is not held under m.mu.** The lock protects
	// only the map decision (reuse an existing session, or reserve a placeholder for a new one);
	// the ffmpeg spawn then runs OUTSIDE the lock. This is what lets four channels cold-start in
	// PARALLEL instead of queuing — the earlier design held m.mu across the whole spawn, so a
	// simultaneous four-channel tune-in serialized, each waiting on the previous encoder's init.
	//
	// The race the old lock prevented (two viewers of the SAME channel starting two encoders, one
	// orphaned) is still prevented: the placeholder is inserted atomically, so the second same-key
	// caller finds it and WAITS on the winner's spawn (session.ready) rather than starting its own.
	// Different keys never contend — each reserves its own placeholder and spawns concurrently.
	m.mu.Lock()
	if s := m.sessions[key]; s != nil {
		m.mu.Unlock()
		<-s.ready
		if s.initErr != nil {
			m.discardFailed(key, s)
			return m.acquire(ctx, key)
		}
		return s, nil
	}
	m.mu.Unlock()

	// A prepared lookup can prove this exact cold start is copy-only before admission. Keep it
	// outside the Manager lock: the lookup reads durable schedule/readiness state, and serializing
	// unrelated Channels behind that I/O would recreate the multi-Channel cold-start convoy.
	newCost := key.plan.EstimatedCost()
	if m.estimateCost != nil && m.estimateCost(ctx, key.channel, key.plan) == 0 {
		newCost = 0
	}

	// Another caller may have reserved this key while the estimate ran. Join its placeholder rather
	// than spawning a second process; the same-key atomicity contract still holds.
	m.mu.Lock()
	if s := m.sessions[key]; s != nil {
		m.mu.Unlock()
		<-s.ready
		if s.initErr != nil {
			m.discardFailed(key, s)
			return m.acquire(ctx, key)
		}
		return s, nil
	}
	// Cost-aware admission with idle reclamation (§9.1 V49). The bound counts concurrent VIDEO TRANSCODES,
	// not sessions: a `-c copy` session (an h264 channel, or HEVC to an HEVC-capable client) costs 0
	// and is always admitted, so a channel watched at two plans (baseline + hevc8) costs ONE (the
	// baseline transcode), not two — the plan-split no longer halves capacity. The incoming cost is
	// estimated here and atomically transitioned to the real cost before each program child starts.
	// Checked under the lock against the live committedCost so parallel starts cannot
	// overshoot the budget. A full budget may reclaim proven-warm work with zero viewer demand, but
	// never an actively watched session.
	if !Admit(m.budget(), m.committedCost, newCost) {
		candidates := make([]idleCandidate, 0, len(m.sessions))
		for candidateKey, candidateSession := range m.sessions {
			if candidateSession.cost > 0 {
				candidates = append(candidates, idleCandidate{key: candidateKey, session: candidateSession})
			}
		}
		m.mu.Unlock()
		if reclaimOldestIdle(candidates) {
			return m.acquire(ctx, key)
		}
		if m.observer != nil {
			m.observer.PlayoutSessionStarted("capacity")
		}
		return nil, ErrAtCapacity
	}
	// Reserve the slot with a not-yet-spawned placeholder, then spawn outside the lock.
	s := m.newPlaceholder(key.channel, key.plan)
	s.cost = newCost
	m.committedCost += newCost
	m.sessions[key] = s
	m.mu.Unlock()

	m.spawnPlaceholder(ctx, s)
	<-s.ready
	if s.initErr != nil {
		// Spawn failed: drop the placeholder so the next viewer starts fresh, and RELEASE its cost
		// reservation (§9.1 V49) — a session that never started must not hold a transcode slot.
		m.mu.Lock()
		if m.sessions[key] == s {
			delete(m.sessions, key)
			m.committedCost -= s.cost
			s.cost = 0
		}
		m.mu.Unlock()
		if m.observer != nil {
			result := "spawn_error"
			if errors.Is(s.initErr, context.Canceled) {
				result = "canceled"
			} else {
				m.observer.PlayoutProcessFailure("parent")
			}
			m.observer.PlayoutSessionStarted(result)
		}
		return nil, s.initErr
	}
	if m.observer != nil {
		m.observer.PlayoutSessionStarted("success")
		m.observer.PlayoutSessionActive(1)
	}
	m.notifyChange()
	return s, nil
}

type idleCandidate struct {
	key     sessionKey
	session *Session
}

// reclaimOldestIdle retires at most one proven-warm, zero-viewer transcode. Candidate sessions are
// snapshotted under Manager.mu, then inspected and closed without it: Session.close calls back into
// the manager to release cost, so holding the manager lock across close would deadlock.
func reclaimOldestIdle(candidates []idleCandidate) bool {
	_, oldest, oldestGeneration, ok := oldestIdle(candidates)
	if !ok {
		return false
	}
	return oldest.session.closeIfIdle(oldestGeneration)
}

// oldestIdle inspects one snapshot without holding Manager.mu. It returns the total currently-idle
// count as well as the oldest exact idle generation, so both transcode admission and the aggregate
// warm-session invariant share one LRU/ABA decision instead of growing subtly different policies.
func oldestIdle(candidates []idleCandidate) (int, idleCandidate, uint64, bool) {
	idleCount := 0
	var oldest idleCandidate
	var oldestSince time.Time
	var oldestGeneration uint64
	found := false
	for _, candidate := range candidates {
		candidate.session.mu.Lock()
		idleSince := candidate.session.idleSince
		idleGeneration := candidate.session.idleGeneration
		idle := !candidate.session.closed && candidate.session.activeViewerCountLocked() == 0 && !idleSince.IsZero()
		candidate.session.mu.Unlock()
		if !idle {
			continue
		}
		idleCount++
		if !found || idleSince.Before(oldestSince) ||
			(idleSince.Equal(oldestSince) && lessSessionKey(candidate.key, oldest.key)) {
			oldest = candidate
			oldestSince = idleSince
			oldestGeneration = idleGeneration
			found = true
		}
	}
	return idleCount, oldest, oldestGeneration, found
}

// enforceWarmIdleLimit closes the least-recently-viewed exact idle generation until at most the
// two useful adjacent sessions remain. Selection and close are deliberately outside Manager.mu:
// close calls back into forget, and holding the manager lock across it would deadlock. A failed
// close means a viewer or a newer idle generation won the ABA race, so resnapshot rather than
// evicting a now-active session or leaving a stale over-limit decision in force.
func (m *Manager) enforceWarmIdleLimit() {
	for {
		m.mu.Lock()
		candidates := make([]idleCandidate, 0, len(m.sessions))
		for key, session := range m.sessions {
			candidates = append(candidates, idleCandidate{key: key, session: session})
		}
		m.mu.Unlock()

		idleCount, oldest, idleGeneration, ok := oldestIdle(candidates)
		if idleCount <= warmIdleSessionLimit || !ok {
			return
		}
		_ = oldest.session.closeIfIdle(idleGeneration)
	}
}

func lessSessionKey(left, right sessionKey) bool {
	if left.channel != right.channel {
		return left.channel < right.channel
	}
	return left.plan < right.plan
}

func (m *Manager) discardFailed(key sessionKey, s *Session) {
	m.mu.Lock()
	if m.sessions[key] == s {
		delete(m.sessions, key)
		m.committedCost -= s.cost
		s.cost = 0
	}
	m.mu.Unlock()
}

func (m *Manager) discardClosed(key sessionKey, s *Session) {
	m.mu.Lock()
	if m.sessions[key] == s {
		delete(m.sessions, key)
		m.committedCost -= s.cost
		s.cost = 0
	}
	m.mu.Unlock()
}

// newPlaceholder builds a Session whose encoder is not spawned yet. `ready` gates every viewer until
// spawnPlaceholder resolves it (success or initErr), so concurrent same-key callers share one spawn.
func (m *Manager) newPlaceholder(channelID string, plan EncodePlan) *Session {
	key := sessionKey{channel: channelID, plan: plan}
	s := &Session{
		ChannelID: channelID,
		Plan:      plan,
		log:       m.log,
		grace:     m.grace,
		// On terminal close (grace teardown, encoder death, or Stop), the session must both leave the
		// map AND release its admission cost (§9.1 V49), then notify. close() is single-fire (guarded
		// by s.closed), so forget runs exactly once — no double-subtract. forget does the notify.
		onDemandChange:  m.notifyChange,
		onBecameIdle:    m.enforceWarmIdleLimit,
		startedAt:       time.Now(),
		viewers:         map[int]sessionViewer{},
		inactiveViewers: map[int]bool{},
		ready:           make(chan struct{}),
	}
	// Capture identity as well as key. An old close callback can race a foreground Attach that
	// has already replaced this closed session at the same key; it must never delete or release
	// the replacement's admission cost.
	s.onClosed = func() { m.forget(key, s) }
	return s
}

// spawnPlaceholder runs the ffmpeg spawn for a reserved placeholder OUTSIDE m.mu, then closes
// s.ready. On failure it records s.initErr; Attach/joinExisting remove the placeholder from the map.
func (m *Manager) spawnPlaceholder(ctx context.Context, s *Session) {
	// context.Background, NOT the attaching viewer's request context. The session outlives whoever
	// started it — binding its lifetime to the first viewer's request would kill the channel for
	// everybody the moment that one person's TV disconnected. ctx is accepted only for symmetry with
	// the spawner signature; the viewer's cancellation must not propagate here.
	_ = ctx
	sctx, cancel := context.WithCancel(context.Background())

	if m.log != nil {
		m.log.Info("playout: session.start spawning encoder", "channel", s.ChannelID, "target", s.Plan.String())
	}
	proc, err := m.spawn(sctx, s.ChannelID, s.Plan)
	if err != nil {
		cancel()
		if m.log != nil {
			m.log.Warn("playout: session.start spawn failed", "channel", s.ChannelID, "target", s.Plan.String(), "err", err)
		}
		s.initErr = err
		close(s.ready)
		return
	}
	// An operator can pause/detach the channel while its cold encoder is spawning. StopChannel
	// closes the placeholder immediately; if the process arrived afterwards, retire it here
	// before any waiter can attach. The HTTP eligibility check prevents a fresh request from
	// replacing it once the persisted lifecycle transition is visible.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		proc.Stop()
		s.initErr = context.Canceled
		close(s.ready)
		return
	}
	s.cancel = cancel
	s.proc = proc
	s.mu.Unlock()
	go s.pump()
	close(s.ready)
}

// pump reads the encoder's output and fans each chunk to every viewer.
//
// One reader, N writers. The chunk size is a plain read buffer rather than anything
// MPEG-TS-aware: this layer moves bytes and must not care where packet boundaries fall.
// Parsing the transport stream here would be a second muxer to keep correct, and the
// viewers' consumer (a media server) already handles arbitrary chunking.
func (s *Session) pump() {
	defer s.close()

	// 64 KiB: ~340 MPEG-TS packets, a few frames at playout bitrates. Large enough that
	// the per-chunk fan-out overhead is negligible, small enough that a viewer joining
	// mid-stream waits milliseconds for its first bytes.
	buf := make([]byte, 64*1024)
	var total int64
	for {
		n, err := s.proc.Stdout.Read(buf)
		if n > 0 {
			if total == 0 {
				// First bytes — the cold-start window (start → first frame) closes here.
				s.mu.Lock()
				s.coldStartMs = time.Since(s.startedAt).Milliseconds()
				s.hasTransport = true
				vc := s.activeViewerCountLocked()
				cold := s.coldStartMs
				s.mu.Unlock()
				if s.log != nil {
					s.log.Info("playout: session first bytes from parent",
						"channel", s.ChannelID, "n", n, "viewers", vc, "cold_start_ms", cold)
				}
			}
			total += int64(n)
			// Copy before broadcasting: buf is reused on the next iteration, and the
			// viewers hold their chunk in a buffered channel across it. Handing them the
			// shared slice would corrupt whatever they had not yet written — a data race
			// whose symptom is intermittently garbled video, which looks like an encoder
			// bug and is not.
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.broadcast(chunk)
		}
		if err != nil {
			if err != io.EOF && s.log != nil {
				s.log.Debug("playout: encoder read ended",
					"channel", s.ChannelID, "err", err, "ffmpeg", s.proc.LastError())
			}
			return
		}
	}
}

// broadcast sends a chunk to every viewer, dropping any that cannot keep up.
//
// The events bus (internal/events) has the same non-blocking shape with the OPPOSITE
// action, and the difference is worth stating. There, a full buffer drops the EVENT,
// because §8 makes SSE a latency optimization and the store is truth on reconnect. Here
// there is no re-read: a byte stream with a hole in the middle is corrupt, not stale. So
// the choice is between dropping the VIEWER and blocking the encoder — and blocking would
// let one stalled TV freeze the channel for everyone else watching it.
//
// Dropping the viewer is therefore the kind option: that one client reconnects (media
// servers do, promptly) and gets a clean stream from the current position, while nobody
// else notices.
func (s *Session) broadcast(chunk []byte) {
	s.mu.Lock()
	droppedActive := false
	for id, viewer := range s.viewers {
		if !viewer.offer(chunk) {
			if s.log != nil {
				s.log.Debug("playout: dropping viewer that fell behind",
					"channel", s.ChannelID, "viewer", id)
			}
			if !s.inactiveViewers[id] {
				droppedActive = true
			}
			delete(s.viewers, id)
			delete(s.inactiveViewers, id)
			viewer.close()
		}
	}
	becameIdle := droppedActive && s.activeViewerCountLocked() == 0 && !s.closed && s.hasTransport
	idleGeneration := s.idleGeneration
	if becameIdle {
		s.idleSince = time.Now()
		s.idleGeneration++
		idleGeneration = s.idleGeneration
	}
	s.mu.Unlock()

	if droppedActive && s.onDemandChange != nil {
		s.onDemandChange()
	}
	if becameIdle {
		s.onIdle(idleGeneration)
		if s.onBecameIdle != nil {
			s.onBecameIdle()
		}
	}
}

// sessionViewer is the fan-out's private delivery contract. A network viewer uses a deliberately
// tiny mailbox and returns false when stalled; the HLS relay implements the same interface with a
// bounded lossless startup queue. broadcast therefore knows only the policy outcome, not buffering.
type sessionViewer interface {
	offer([]byte) bool
	close()
}

type channelViewer struct{ chunks chan []byte }

func (v *channelViewer) offer(chunk []byte) bool {
	select {
	case v.chunks <- chunk:
		return true
	default:
		return false
	}
}

func (v *channelViewer) close() { close(v.chunks) }

type sessionSink interface {
	sessionViewer
}

// addViewer registers a viewer. Reports false if the session is already tearing down.
func (s *Session) addViewer() (<-chan []byte, func(), bool) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil, false
	}
	id := s.nextID
	s.nextID++
	ch := make(chan []byte, viewerBuffer)
	s.viewers[id] = &channelViewer{chunks: ch}
	s.idleSince = time.Time{}
	s.idleGeneration++

	var once sync.Once
	detach := func() { once.Do(func() { s.removeViewer(id) }) }
	s.mu.Unlock()
	if s.onDemandChange != nil {
		s.onDemandChange()
	}
	return ch, detach, true
}

func (s *Session) addSink(sink sessionSink) (sinkLease, bool) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return sinkLease{}, false
	}
	id := s.nextID
	s.nextID++
	s.viewers[id] = sink
	s.idleSince = time.Time{}
	s.idleGeneration++

	var once sync.Once
	detach := func() { once.Do(func() { s.removeViewer(id) }) }
	lease := sinkLease{
		release:   detach,
		setActive: func(active bool) bool { return s.setViewerActive(id, active) },
	}
	s.mu.Unlock()
	if s.onDemandChange != nil {
		s.onDemandChange()
	}
	return lease, true
}

func (s *Session) activeViewerCountLocked() int {
	return len(s.viewers) - len(s.inactiveViewers)
}

// setViewerActive changes only demand, not delivery. An idle HLS remux continues receiving bytes so
// it stays warm, while admission and telemetry see that no person is currently watching it.
func (s *Session) setViewerActive(id int, active bool) bool {
	s.mu.Lock()
	if s.closed || s.viewers[id] == nil {
		s.mu.Unlock()
		return false
	}
	wasActive := !s.inactiveViewers[id]
	if wasActive == active {
		s.mu.Unlock()
		return true
	}
	if active {
		delete(s.inactiveViewers, id)
		s.idleSince = time.Time{}
		s.idleGeneration++
		s.mu.Unlock()
		if s.onDemandChange != nil {
			s.onDemandChange()
		}
		return true
	}

	s.inactiveViewers[id] = true
	becameIdle := s.activeViewerCountLocked() == 0
	warm := s.hasTransport
	idleGeneration := s.idleGeneration
	if becameIdle && warm {
		s.idleSince = time.Now()
		s.idleGeneration++
		idleGeneration = s.idleGeneration
	}
	s.mu.Unlock()

	if s.onDemandChange != nil {
		s.onDemandChange()
	}
	if becameIdle {
		if !warm {
			s.close()
			return false
		}
		s.onIdle(idleGeneration)
		if s.onBecameIdle != nil {
			s.onBecameIdle()
		}
	}
	return true
}

// removeViewer drops a viewer and arms the grace timer if it was the last one.
func (s *Session) removeViewer(id int) {
	s.mu.Lock()
	viewer, ok := s.viewers[id]
	wasActive := ok && !s.inactiveViewers[id]
	if ok {
		delete(s.viewers, id)
		delete(s.inactiveViewers, id)
		viewer.close()
	}
	last := wasActive && s.activeViewerCountLocked() == 0 && !s.closed
	warm := s.hasTransport
	idleGeneration := s.idleGeneration
	if last && warm {
		s.idleSince = time.Now()
		s.idleGeneration++
		idleGeneration = s.idleGeneration
	}
	s.mu.Unlock()

	if wasActive && s.onDemandChange != nil {
		s.onDemandChange()
	}
	if last {
		if !warm {
			// A process that never produced transport is not a warm channel. Retaining it would
			// keep its conservative admission cost through the grace window and can block the
			// baseline retry that the waiting tuner is about to make.
			if s.log != nil {
				s.log.Debug("playout: stopping zero-byte session without idle grace",
					"channel", s.ChannelID, "plan", s.Plan.String())
			}
			s.close()
			return
		}
		s.onIdle(idleGeneration)
		if s.onBecameIdle != nil {
			s.onBecameIdle()
		}
	}
}

// closeIfIdle closes only if the same idle interval selected by admission is still current. A
// viewer can reattach between candidate selection and this method; in that case the foreground tune
// must lose the race rather than disconnect the active viewer.
func (s *Session) closeIfIdle(expectedGeneration uint64) bool {
	s.mu.Lock()
	if s.closed || s.activeViewerCountLocked() != 0 || s.idleSince.IsZero() || s.idleGeneration != expectedGeneration {
		s.mu.Unlock()
		return false
	}
	s.closed = true
	for id, viewer := range s.viewers {
		delete(s.viewers, id)
		delete(s.inactiveViewers, id)
		viewer.close()
	}
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if s.onClosed != nil {
		s.onClosed()
	}
	return true
}

// onIdle is called when the last viewer leaves. It does NOT tear down immediately — see
// DefaultGrace: a channel-surfing TV that comes back in two seconds should find the encoder
// still running rather than pay the start cost again.
//
// The obvious `time.AfterFunc(s.grace, s.close)` is wrong:
//
//	t=0   last viewer leaves      → timer armed for t=30s
//	t=2s  a viewer attaches       → the session is live again, 1 viewer
//	t=30s the timer fires         → closes a session that someone is watching
//
// The fix is to make firing conditional on BOTH zero viewers and the idle generation this timer
// belongs to. The viewer check protects an active reattach; the generation protects a later idle
// interval after an attach/detach ABA cycle. Cancelling a timer is only an optimization because
// `timer.Stop()` may race a callback already running, so the state check remains authoritative.
func (s *Session) onIdle(idleGeneration uint64) {
	time.AfterFunc(s.grace, func() {
		if !s.closeIfIdle(idleGeneration) {
			// Someone is watching (or pump already tore the session down when the encoder
			// died), or this callback belongs to an older idle generation. Either way this
			// timer has nothing to do.
			return
		}
		if s.log != nil {
			s.log.Debug("playout: stopping idle channel",
				"channel", s.ChannelID, "grace", s.grace)
		}
	})
}

// ViewerCount is for the API's telemetry and for tests.
func (s *Session) ViewerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeViewerCountLocked()
}

// close tears the session down and disconnects every remaining viewer.
//
// Closing the viewer channels is what unblocks their handlers: a tuner handler is parked
// on a channel receive, and a closed channel is how it learns the stream ended and can
// return instead of hanging until the client gives up.
func (s *Session) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for id, viewer := range s.viewers {
		delete(s.viewers, id)
		delete(s.inactiveViewers, id)
		viewer.close()
	}
	cancel := s.cancel
	s.mu.Unlock()

	// Stop can race a cold session's spawn. In that window there is no process context to
	// cancel yet; spawnPlaceholder observes closed after the spawn and tears the process down
	// before publishing readiness. A nil-safe cancel here keeps lifecycle changes from turning
	// that ordinary race into a panic.
	if cancel != nil {
		cancel() // kills the process group (process.go)
	}

	// Outside the lock, and after the cancel, so a subscriber that immediately re-reads the
	// telemetry sees the session already gone rather than mid-teardown.
	if s.onClosed != nil {
		s.onClosed()
	}
}

// Stop tears down the session immediately, regardless of viewers. For shutdown and for an
// operator stopping a channel.
func (s *Session) Stop() { s.close() }

// StopChannel tears down every live session for channelID, across every codec plan. It is the
// operator lifecycle path: pausing, detaching, or moving a channel away from internal playout
// must disconnect existing viewers immediately rather than merely refusing the next tune.
func (m *Manager) StopChannel(channelID string) {
	m.mu.Lock()
	sessions := make([]*Session, 0, 2)
	for key, s := range m.sessions {
		if key.channel == channelID {
			sessions = append(sessions, s)
		}
	}
	m.mu.Unlock()

	// Session.close calls forget, which owns map removal and admission-cost accounting. Do not
	// pre-delete here: doing so would make forget unable to release a transcode reservation.
	for _, s := range sessions {
		s.Stop()
	}
}

// Stop tears down every session. Called on shutdown — a live encoder never exits on its
// own, so without this they outlive the process that started them.
func (m *Manager) Stop() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()

	for _, s := range sessions {
		s.Stop()
	}
}

// forget removes a session from the map and releases its admission cost (§9.1 V49), then notifies.
// Called from a session's onClosed, which close() fires exactly once — so the cost is subtracted
// exactly once even if Stop races the grace timer. Identity is part of the guard: a foreground
// attach can replace a closed session at the same key before its callback arrives, and that stale
// callback must not delete the replacement or subtract its admission cost.
func (m *Manager) forget(key sessionKey, closing *Session) {
	m.mu.Lock()
	removed := false
	if m.sessions[key] == closing {
		m.committedCost -= closing.cost
		closing.cost = 0
		delete(m.sessions, key)
		removed = true
	}
	m.mu.Unlock()
	if removed && m.observer != nil {
		m.observer.PlayoutSessionActive(-1)
	}
	m.notifyChange()
}

// session returns a live session by (channel, target), or nil. For tests and telemetry.
func (m *Manager) session(channelID string, plan EncodePlan) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[sessionKey{channel: channelID, plan: plan}]
}

// ActiveCount reports how many channels are encoding. This is the `active` input to
// Resolve's load-aware quality decision, and what the operator sees as concurrent load.
func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// Capacity reports the current admission budget — how many concurrent VIDEO TRANSCODES the box can
// sustain right now (§9.1 V49), the denominator in the dashboard's "2 / 4" load line. It is the
// measured/live budget, not a static setting, so the dashboard shows real headroom (which shrinks
// when a model goes resident and grows when it unloads). Read outside m.mu — budget() is its own
// source of truth and takes no manager lock.
func (m *Manager) Capacity() int {
	return m.budget()
}

// Stats snapshots every live encoder for the dashboard (§12, V16).
//
// Sorted by channel id so a polling or re-rendering caller sees a stable order — an
// unsorted map walk would reshuffle the rows on every read, which reads as flicker rather
// than as data changing.
//
// Snapshots session pointers and their manager-owned admission cost under the manager lock, then
// reads each session after releasing it. Teardown callbacks need the manager lock, so holding it
// while taking every session lock would turn an observational endpoint into a lock-order hazard.
func (m *Manager) Stats(now time.Time) []SessionStat {
	type candidate struct {
		session       *Session
		transcodeCost int
	}
	m.mu.Lock()
	sessions := make([]candidate, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, candidate{session: s, transcodeCost: s.cost})
	}
	m.mu.Unlock()

	out := make([]SessionStat, 0, len(sessions))
	for _, candidate := range sessions {
		// ⚠ Skip CLOSED sessions. Teardown is lazy: close() marks the session and disconnects
		// its viewers, but the map entry survives until the next Attach on that channel
		// deletes it. An unfiltered snapshot would keep reporting a dead encoder as live —
		// indefinitely, on a channel nobody tunes again.
		if st, ok := candidate.session.statIfLive(now, candidate.transcodeCost); ok {
			out = append(out, st)
		}
	}
	// By channel, then target — a channel can now have two rows (browser + tuner), and a stable
	// tiebreak keeps them from reshuffling between polls.
	sort.Slice(out, func(i, j int) bool {
		if out[i].ChannelID != out[j].ChannelID {
			return out[i].ChannelID < out[j].ChannelID
		}
		return out[i].Target < out[j].Target
	})
	return out
}

// statIfLive snapshots one session, reporting false if it has already been torn down.
//
// Liveness is read under the SAME lock as the rest of the snapshot: checking `closed`
// separately would leave a window where a session closes between the check and the read, and
// the row would describe an encoder that no longer exists.
func (s *Session) statIfLive(now time.Time, transcodeCost int) (SessionStat, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return SessionStat{}, false
	}

	uptime := now.Sub(s.startedAt)
	// How far ahead of realtime the encoder has produced output. `out_time` is how much
	// media exists; uptime is how much wall-clock has passed. The difference is the cushion
	// absorbing a slow moment before a viewer sees a stall — and it goes NEGATIVE when the
	// encoder is losing, which is the same condition a sub-1.0 speed reports, expressed as
	// accumulated deficit rather than an instantaneous rate.
	buffered := s.last.OutTimeMS - uptime.Milliseconds()

	return SessionStat{
		ChannelID:     s.ChannelID,
		Target:        s.Plan.String(),
		Viewers:       s.activeViewerCountLocked(),
		Encoder:       string(s.encoder),
		Hardware:      s.encoder != "" && s.encoder != EncoderSoftware,
		Speed:         s.last.Speed,
		BufferedMS:    buffered,
		UptimeMS:      uptime.Milliseconds(),
		ColdStartMS:   s.coldStartMs,
		TranscodeCost: transcodeCost,
	}, true
}

// ReportProgram records telemetry for the program currently encoding on a (channel, target) (V16).
//
// The TARGET is part of the address (§9.1 V47): a channel can have two live sessions (a browser
// one and a tuner one), and a per-program child belongs to exactly one — the one whose parent
// requested it, which is why the child carries its target through the program URL. Reporting to the
// wrong session would put a browser transcode's speed on the tuner's dashboard row.
//
// Called by the per-program encode path, which is where the real encoder runs — see the
// `encoder` field for why the session's own process is the wrong source. A report for a channel
// with no live session is dropped: the child is bound to its request, so it can briefly outlive
// a session that was just torn down, and resurrecting a dead session to hold its telemetry
// would be worse than losing a sample.
//
// Progress samples are a LATENCY signal, never load-bearing (§8) — the same discipline the SSE
// bus documents. Dropping one costs a stale number for a second, nothing more.
func (m *Manager) ReportProgram(channelID string, plan EncodePlan, enc Encoder, transcoding bool, p Progress) {
	s := m.session(channelID, plan)
	if s == nil {
		return
	}
	s.mu.Lock()
	s.encoder = enc
	s.last = p
	s.mu.Unlock()
	// startChild admitted this real cost before spawning. Calling the same transition here keeps
	// legacy/report-only callers correct and is idempotent for the production progress path.
	_ = m.AdmitProgram(channelID, plan, transcoding)
}

// AdmitProgram atomically transitions an existing session between copy and video-transcode cost.
// A prepared session starts at zero, but a later prepared miss must earn capacity before its live
// child starts; otherwise many cheap sessions could all cross an Airing boundary and oversubscribe
// the measured encoder budget together.
func (m *Manager) AdmitProgram(channelID string, plan EncodePlan, transcoding bool) bool {
	key := sessionKey{channel: channelID, plan: plan}
	desiredCost := 0
	if transcoding {
		desiredCost = 1
	}
	for {
		m.mu.Lock()
		s := m.sessions[key]
		if s == nil {
			m.mu.Unlock()
			return false
		}
		if s.cost == desiredCost {
			m.mu.Unlock()
			return true
		}
		if desiredCost == 0 {
			m.committedCost -= s.cost
			s.cost = 0
			m.mu.Unlock()
			m.notifyChange()
			return true
		}
		incoming := desiredCost - s.cost
		if Admit(m.budget(), m.committedCost, incoming) {
			m.committedCost += incoming
			s.cost = desiredCost
			m.mu.Unlock()
			m.notifyChange()
			return true
		}
		candidates := make([]idleCandidate, 0, len(m.sessions))
		for candidateKey, candidateSession := range m.sessions {
			if candidateKey != key && candidateSession.cost > 0 {
				candidates = append(candidates, idleCandidate{key: candidateKey, session: candidateSession})
			}
		}
		m.mu.Unlock()
		if !reclaimOldestIdle(candidates) {
			return false
		}
	}
}
