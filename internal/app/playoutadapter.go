package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/diagnostics"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/inventory"
	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/playout"
	"github.com/loomarr/loomarr/internal/prepared"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
)

// The adapter that makes internal playout work (§9.1) — where three separately-verified pieces
// finally compose:
//
//	store.Channel.Desired  → the accepted cycle reconciliation committed for broadcast
//	playout.AiringAt       → which program that puts on right now, and at what offset
//	library.StreamURL      → the URL ffmpeg can actually read
//
// It lives in internal/app rather than internal/playout on purpose. playout is the mechanism
// (encoders, args, sessions) and must not know about stores, media servers, or settings; this is
// the wiring, and wiring belongs in the composition root.

// cyclePreviewer is the scheduling surface the resolver needs — satisfied by *channels.Engine.
//
// Narrowed to the one method deliberately: the resolver must not be able to reconcile, push to
// Tunarr, or mutate anything. Playout is a READ of the schedule, and a narrow interface makes
// that structural rather than a rule someone has to remember.
type cyclePreviewer interface {
	CyclePreview(ctx context.Context, channelID string, at time.Time) (
		resolvedAt time.Time, slots []schedule.Slot, active schedule.ActiveRuleAttribution,
		window time.Duration, err error)
}

// titleReader is the one store method provenance needs.
//
// Narrowed to a single method deliberately, the same way cyclePreviewer is: the guide READS
// acquisition state and must not be able to mutate it. A structural guarantee beats a rule
// someone has to remember.
type titleReader interface {
	GetTitle(ctx context.Context, key provision.Key) (provision.Record, error)
}

// playoutResolver answers "what is airing now, and where does ffmpeg read it from".
// clipPlayRecorder is the one-method slice of the store the resolver needs to count an
// airing. `at` is the scheduled clip start, which makes repeated resolves of one airing
// idempotent in the store. Narrow deliberately — the resolver has no other business writing.
type clipPlayRecorder interface {
	RecordClipPlay(ctx context.Context, channelID, clipHash string, at time.Time) (bool, error)
}

// airingRecorder stamps that a PROGRAMME aired (§5, programming-design §3.1) — the recency
// signal placement ranks on. Narrowed to the one write, like every other store slice here.
type airingRecorder interface {
	RecordAiring(ctx context.Context, channelID string, key provision.Key, libraryItemID string, at time.Time) error
}

// channelReader is the one store method both broadcast and guide need. Broadcast reads Desired,
// the accepted cycle reconciliation committed; guide also fingerprints Lineup and Policy when it
// forecasts a future rolling window (see cyclecache.go). Narrowed like the readers above — both
// are reads, and neither path may mutate the channel it observes.
type channelReader interface {
	GetChannel(ctx context.Context, id string) (store.Channel, error)
}

// codecWriter persists a channel's computed broadcast codec (§9.1 V50). Narrowed to the one
// targeted column write — the resolver samples + probes the lineup, but has no other business
// mutating a channel row (the binder owns the lineup, loomarr-one-lineup-writer).
type codecWriter interface {
	SetChannelBroadcastCodec(ctx context.Context, id string, expectedRevision int64, codec string) (int64, error)
}

type playoutResolver struct {
	engine  cyclePreviewer
	lib     *library.Client
	now     func() time.Time
	metrics *metrics.Recorder
	// titles resolves a block's acquisition record for the grid's provenance line. Nil ⇒ the
	// line is simply absent, which is the right degradation: a guide without provenance is
	// still a guide.
	titles titleReader
	// clipPlays counts a filler clip having aired (V28). Nil ⇒ no counting, which is the
	// correct degradation: usage is telemetry and a channel must still play without it.
	clipPlays clipPlayRecorder
	// airings records that a PROGRAMME aired, feeding recency-aware placement
	// (programming-design §3.1). Nil ⇒ no history, and placement degrades to the positional
	// rotation it used before the signal existed.
	airings airingRecorder

	// tier / encoder / capacity are read live so an operator's Settings change applies to the
	// NEXT program rather than requiring a restart. Each program is a fresh child process, so
	// "the next program" is at most one program away — which makes hot-apply genuinely cheap
	// here in a way it would not be for one long-lived encode.
	tier     func() string
	encoder  func() string
	capacity func() int
	// activeChannels is how many channels are encoding right now, for the load-aware quality
	// ladder. A FUNC because the session manager and this resolver need each other: the manager
	// spawns encodes that ask the resolver for a profile, and the profile depends on how many
	// the manager is running. A func breaks the cycle that a struct field could not.
	activeChannels func() int

	// pods assembles the channel's commercial break (§10). The SAME PodPreviewer the API and
	// the reconciler use, so the ad that plays is the one the channel page previewed — §10's
	// one-assembler rule. Nil ⇒ breaks fall back to the offline card.
	pods api.PodPreviewer
	// fillerDir resolves a clip's relative id to a file on disk. It is immutable for the
	// generation: catalog paths, scan and playout must never interpret one row under different
	// roots while a saved layout change is waiting on restart.
	fillerDir string

	// pathMap resolves the parsed `library.path_map` (§15, V47) live, so a mapping edit applies
	// without a restart. Empty ⇒ no mapping ⇒ ResolveInput uses the media server's HTTP stream.
	pathMap func() library.PathMap
	// probeFormat probes a source's codec/format for the direct-play copy decision (probe.go).
	// Nil ⇒ treat as transcode-required (safe: correctness over speed when we cannot probe).
	probeFormat playout.FormatProber

	// ffmpegPath is the binary the capability probe executes.
	ffmpegPath func() string
	// capabilityRoot is the persistent prepared root that carries bounded, regenerable host
	// capability evidence across restarts. Nil/empty keeps ordinary full detection.
	capabilityRoot func() string
	// gpuName reports the primary GPU's name so the capability probe can prefer that vendor's
	// native encoder (Detect's gpuVendor arg). Nil or "" ⇒ unknown GPU ⇒ the cross-vendor default
	// order, so an install without the wiring still detects an encoder, just without the native hint.
	gpuName func() string
	// audioLanguage is the operator's preferred audio track language (§9.1, `eng` by default),
	// read live like every other setting. Empty ⇒ the file's first track, which is what playout
	// did before this existed — and is how a channel played a film in Russian.
	audioLanguage func() string
	// probeSource returns the shared ffprobe superset so the audio choice and durable technical
	// observation come from one process. Nil ⇒ track 0, preserving best-effort playout.
	probeSource playout.SourceProber
	// inventory is Loomarr's durable provider-neutral source observation (§5 V66). Audio selection
	// reads it before Library or ffprobe I/O; nil preserves the safe direct-probe fallback.
	inventory inventory.Service
	// probeTracks lists a source's audio AND subtitle tracks for the Watch pickers (§9.1, V46) —
	// the options come from the airing media, not a list. Nil ⇒ empty tracks, so the pickers show
	// only the current channel default rather than blocking.
	probeTracks        playout.TrackProber
	processDiagnostics *diagnostics.ProcessManager
	log                *slog.Logger
	// channels reads the channel the arranged-cycle cache fingerprints, and cycles is that
	// cache (cyclecache.go). Both nil ⇒ the guide computes every cycle live, which is exactly
	// the pre-cache behaviour — so tests and any install that skips the wiring still get correct
	// (merely slower) listings.
	channels channelReader
	// codecs persists the computed broadcast codec after a bind samples the lineup (§9.1 V50).
	// Nil ⇒ ComputeChannelCodec still returns the measured codec but skips the write — so a
	// caller that only wants the value (or an install without the wiring) degrades to "compute,
	// don't store", never to a failed bind.
	codecs codecWriter
	cycles *cycleCache
	// detectOnce / detected cache the measured encoder choice (detectedEncoder). The first live
	// demand synchronously checks only the persisted evidence fingerprint, then detectStart validates
	// a match (or performs the full benchmark on a miss) without making playback wait.
	//
	// Process-cached because Detect trial-encodes every candidate at ~5s apiece — fine once, far
	// too slow on the per-program path. A verified hardware result can also survive restart through
	// capabilityRoot when its cheap host fingerprint still matches; a bounded real validation then
	// runs asynchronously.
	detectOnce     sync.Once
	detectStart    sync.Once
	detectEvidence sync.Once
	inputsOnce     sync.Once
	detectReady    atomic.Bool
	detectedMu     sync.RWMutex
	detectContext  context.Context
	detected       playout.Encoder
	maxChannels    atomic.Int64
	capabilityBin  string
	capabilityGPU  string
	capabilityPath string
	// Test seam for the cheap persisted-evidence identity check. Nil fingerprints the real
	// FFmpeg/GPU/profile and reads the evidence; it starts no encoder trial or full benchmark.
	loadCapabilityEvidence func(context.Context) (playout.Capacity, bool)
}

// AiringNow resolves the channel's current program and its ffmpeg input URL.
//
// It reads the persisted Desired cycle that reconciliation ACCEPTED. CyclePreview is an authoring
// and forecasting surface: it includes mutable availability and airing-history inputs, so invoking
// it here would let the deck change at a programme EOF. Desired is also what makes the answer
// survive a process restart; the deterministic channel epoch supplies the matching wall-clock
// position.
func (r *playoutResolver) AiringNow(ctx context.Context, channelID string) (playout.Airing, string, error) {
	now := r.now()

	slots, epoch, err := r.acceptedCycle(ctx, channelID)
	if err != nil {
		return playout.Airing{}, "", err
	}

	airing := playout.AiringAt(slots, epoch, now)
	airing.ScheduleBlockID = playout.ScheduledBlockID(channelID, airing.StartedAt, airing.Kind, airing.Identity)

	// A BREAK GAP resolves to a real commercial (§10). Tunarr used to do this: the scheduler
	// leaves flex, and Tunarr played clips from a filler-list into it. Internal playout has no
	// such negotiator, so it must pick the clip itself — otherwise every break is dead air.
	if airing.Kind == schedule.SlotFiller {
		return r.airingFiller(ctx, channelID, airing, now)
	}

	if !airing.Playable() {
		// Not an error: an empty lineup, or one where nothing has landed yet, is a real state.
		// The handler renders it as the offline card.
		return airing, "", nil
	}

	// Record that this programme aired (§5, programming-design §3.1) — the memory the
	// scheduler lacked, and the input recency-aware placement ranks on.
	//
	// ⚠ Stamped with the programme's START (`now - Offset`), not with `now`, and written on
	// EVERY resolve rather than only at the start.
	//
	// The filler counter next door guards on `into == 0` because it is a COUNTER — counting a
	// mid-clip re-resolve would inflate it. This is not a counter: the row is an upsert holding
	// the last airing, so re-writing it is idempotent. Guarding on `Offset == 0` here was a
	// real bug (caught only by tuning in live): a viewer arrives mid-programme, so Offset is
	// however far the wall clock happens to be into the film — measured at 2075s on the first
	// real tune-in. The guard would have fired only within ~1ms of a programme boundary, so
	// history stayed empty and the whole recency signal silently did nothing.
	//
	// Using the START also makes the value mean what §3.1 needs: "when did this programme
	// begin airing", which is stable no matter when anyone tuned in or how often ffmpeg
	// re-requested the segment.
	//
	// ⚠ Telemetry, never correctness. A failed write is logged and the programme still airs —
	// the same posture as RecordClipPlay, for the same reason: a channel must never go dark
	// because a history table was unavailable.
	if r.airings != nil && airing.Key != "" {
		startedAt := now.Add(-airing.Offset)
		if aerr := r.airings.RecordAiring(ctx, channelID, airing.Key, airing.LibraryItemID, startedAt); aerr != nil && r.log != nil {
			r.log.Debug("playout: airing not recorded", "channel", channelID, "key", airing.Key, "err", aerr)
		}
	}

	// Resolve the input ffmpeg reads: the FILE directly (direct play, V47) when a path mapping
	// resolves a readable local file, else the media server's HTTP stream (fallback). This is where
	// direct play begins — reading the file lets the encoder `-c copy` it (playout.PlanCopy).
	var pm library.PathMap
	if r.pathMap != nil {
		pm = r.pathMap()
	}
	src := r.lib.ResolveInput(ctx, airing.LibraryItemID, pm, library.StatReadableFile)
	if src.URL == "" {
		// The item id is real but the media server is unconfigured, so there is nothing to
		// read. Reporting it as "nothing airing" rather than an error means the channel shows
		// the offline card instead of failing to tune — the same outcome the viewer would get
		// from an error, minus the retry storm.
		return playout.Airing{Kind: schedule.SlotFlex}, "", nil
	}
	return airing, src.URL, nil
}

// acceptedCycle is the broadcast commit boundary. Reconciliation owns the write; the encoder and
// current guide own reads. Keeping the seam here makes it impossible for a programme boundary to
// accidentally call the mutable authoring preview again.
func (r *playoutResolver) acceptedCycle(ctx context.Context, channelID string) ([]schedule.Slot, time.Time, error) {
	if r.channels == nil {
		return nil, time.Time{}, errors.New("read accepted channel schedule: channel reader is not configured")
	}
	ch, err := r.channels.GetChannel(ctx, channelID)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("read accepted channel schedule %s: %w", channelID, err)
	}
	epoch, err := effectivePlayoutAnchor(ch)
	if err != nil {
		return nil, time.Time{}, err
	}
	return ch.Desired, epoch, nil
}

// ComputeChannelCodec derives the channel's uniform BROADCAST CODEC (§9.1 V50) from its library
// content: it samples the channel's program slots, probes each file's video codec, and takes the
// MAJORITY (Q2). The result is what the timeline normalizes to — the show that already matches
// copies, everything else (minority-codec titles, all filler) transcodes to match — which is what
// keeps the stream single-codec and therefore HEVC-fMP4-legal.
//
// Called at CURATION (after a bind writes the lineup), not on the play hot path: the codec is
// stored (Q1) so the session start reads a column, not a probe. It reuses the SAME resolve+probe
// path AiringNow uses (CyclePreview → ResolveInput → probeFormat), so the codec we store is
// measured from the very files we broadcast — it cannot drift from what actually plays.
//
// Returns BroadcastCodecH264 as the safe answer whenever it cannot measure (no probe wired, no
// resolvable files, an empty/all-pending lineup): h264/TS plays on every client, so an
// un-measurable channel stays maximally compatible rather than guessing HEVC. Ties break to h264
// for the same reason — a 50/50 channel is not clearly HEVC-dominant.
//
// If r.codecs is wired the measured codec is persisted; the measured value is returned either way.
func (r *playoutResolver) ComputeChannelCodec(ctx context.Context, channelID string) (string, error) {
	if r.codecs == nil {
		return r.measureChannelCodec(ctx, channelID), nil
	}
	if r.channels == nil {
		return store.BroadcastCodecH264, errors.New("persist broadcast codec: channel reader is not configured")
	}

	// Measuring spans cycle construction, library resolution, and several probes. A lineup can
	// change during that window, so the derived codec is only valid when the channel revision is
	// unchanged on both sides of the measurement. The targeted write uses that same revision and
	// advances it, preventing either a stale probe or a full-row writer from overwriting the other.
	const maxCodecWriteAttempts = 4
	codec := store.BroadcastCodecH264
	for attempt := 0; attempt < maxCodecWriteAttempts; attempt++ {
		before, err := r.channels.GetChannel(ctx, channelID)
		if err != nil {
			return codec, fmt.Errorf("read channel %s before codec measurement: %w", channelID, err)
		}
		codec = r.measureChannelCodec(ctx, channelID)
		after, err := r.channels.GetChannel(ctx, channelID)
		if err != nil {
			return codec, fmt.Errorf("read channel %s after codec measurement: %w", channelID, err)
		}
		if before.Revision != after.Revision {
			continue
		}
		if _, err := r.codecs.SetChannelBroadcastCodec(ctx, channelID, after.Revision, codec); err == nil {
			return codec, nil
		} else if !errors.Is(err, store.ErrChannelStale) {
			return codec, fmt.Errorf("persist broadcast codec for %s: %w", channelID, err)
		}
		if err := ctx.Err(); err != nil {
			return codec, err
		}
	}
	return codec, fmt.Errorf("persist broadcast codec for %s: channel kept changing", channelID)
}

// ChannelCodec reads the channel's STORED broadcast codec (§9.1 V50) — the hot-path read the play
// URL and program routing use to decide the served container + the transcode target. It is a plain
// column read (set at curation by ComputeChannelCodec / backfilled at boot), NOT a probe: the whole
// point of storing it (Q1) is that a session start costs a row read, not N ffprobes.
//
// Defaults to BroadcastCodecH264 on any miss (no channel reader wired, channel not found, or an
// empty column on an un-backfilled row) — h264/TS is the maximally-compatible fallback, so a
// lookup failure degrades a channel to "plays everywhere", never to a black screen.
func (r *playoutResolver) ChannelCodec(ctx context.Context, channelID string) string {
	if r.channels == nil {
		return store.BroadcastCodecH264
	}
	ch, err := r.channels.GetChannel(ctx, channelID)
	if err != nil {
		if r.log != nil {
			r.log.Debug("codec: channel read failed; defaulting h264", "channel", channelID, "err", err)
		}
		return store.BroadcastCodecH264
	}
	if ch.BroadcastCodec == "" {
		return store.BroadcastCodecH264
	}
	return ch.BroadcastCodec
}

// maxCodecProbes bounds how many program files ComputeChannelCodec probes to decide the majority.
//
// Curation is a human-triggered action, not a hot loop, but a household lineup can still be scores
// of titles and each probe is an ffprobe exec (~tens–hundreds of ms). Sampling the first N program
// slots is enough to establish the majority codec reliably — a channel's library is overwhelmingly
// one codec in practice, and a genuinely mixed one is decided by the same N-sample majority the
// full scan would reach. Past N, the tail would only confirm the lead. Bounds the approve latency.
const maxCodecProbes = 12

// measureChannelCodec is the pure-ish measurement half of ComputeChannelCodec: sample → probe →
// majority, no persistence. Split out so the storing wrapper stays trivial and the sampling policy
// (which slots, how many, tiebreak) lives in one place.
func (r *playoutResolver) measureChannelCodec(ctx context.Context, channelID string) string {
	if r.probeFormat == nil {
		return store.BroadcastCodecH264 // cannot measure → safe default
	}
	_, slots, _, _, err := r.engine.CyclePreview(ctx, channelID, r.now())
	if err != nil {
		if r.log != nil {
			r.log.Debug("codec: cycle preview failed; defaulting h264", "channel", channelID, "err", err)
		}
		return store.BroadcastCodecH264
	}

	var pm library.PathMap
	if r.pathMap != nil {
		pm = r.pathMap()
	}
	lib := r.lib.Snapshot()

	var codecs []string
	probed := 0
	for _, sl := range slots {
		if probed >= maxCodecProbes {
			break
		}
		// Only real, landed PROGRAMS carry a file to probe. Pending acquisitions have no
		// LibraryItemID yet, and filler/flex are normalized TO the channel codec, not sources of it.
		if sl.Kind != schedule.SlotProgram || sl.LibraryItemID == "" {
			continue
		}
		src := lib.ResolveInput(ctx, sl.LibraryItemID, pm, library.StatReadableFile)
		if src.URL == "" {
			continue
		}
		fmtInfo, perr := r.probeFormat(ctx, src.URL)
		if perr != nil {
			if r.log != nil {
				r.log.Debug("codec: probe failed; skipping title", "channel", channelID, "item", sl.LibraryItemID, "err", perr)
			}
			continue
		}
		probed++
		codecs = append(codecs, fmtInfo.VideoCodec)
	}
	return majorityBroadcastCodec(codecs)
}

// majorityBroadcastCodec turns a sample of probed video-codec strings into the one broadcast codec
// the channel normalizes to (§9.1 V50 Q2: MAJORITY WINS). Everything non-HEVC counts as h264 —
// h264/mpeg2/vp9/… all normalize DOWN to h264 — so the vote is strictly HEVC vs. not-HEVC.
//
// An even split (including the empty sample: no probes, or an all-pending/filler lineup) breaks to
// h264, the maximally-compatible choice: h264/TS needs no fMP4 and no HEVC decoder, so a channel
// that isn't clearly HEVC-dominant stays playable everywhere. Pure + total, so the sampling policy
// above and this decision are testable in isolation.
func majorityBroadcastCodec(codecs []string) string {
	var hevc, other int
	for _, c := range codecs {
		if playout.IsHEVCCodec(c) {
			hevc++
		} else {
			other++
		}
	}
	if hevc > other {
		return store.BroadcastCodecHEVC
	}
	return store.BroadcastCodecH264
}

// BroadcastsBetween resolves a channel's programme timeline for the XMLTV guide (§9.1, V6b).
//
// Deliberately on the SAME type as AiringNow. The rolling window containing now reads the SAME
// persisted Desired cycle as the encoder; only other windows use CyclePreview as a forecast.
//
// `at` is the window's START, not `now`. CyclePreview evaluates curation rules at an instant —
// a rule that switches the channel to horror at 21:00 changes the lineup — and a guide built at
// `now` would advertise the current rule's lineup for the whole window. Using `from` means the
// listings reflect what the rules said when the window opened; a window spanning a rule
// boundary is a known limitation rather than a silent wrong answer (the mid-window portion
// shows the earlier rule's programmes).
// cycleAt is CyclePreview with the arranged cycle memoised (cyclecache.go).
//
// GUIDE PATHS ONLY. AiringNow deliberately does not use this: it is what ffmpeg streams, and
// §9.1's one-source rule is worth more on the broadcast path than the milliseconds a cache would
// save there (one channel, once per programme, versus every channel on every grid poll).
//
// Degrades to the live computation whenever the cache cannot be trusted: no store, no cache, or
// a channel that will not load or fingerprint. Every one of those returns the same answer
// CyclePreview would — only slower — so a cache failure is a performance regression, never a
// wrong lineup.
// Returns the arranged slots AND the resolved rolling-window horizon, which segmentedBroadcasts
// needs to know where the deck rotates. The window is part of the same CyclePreview answer, so
// carrying it costs nothing and saves the caller re-deriving the rule > channel > default
// precedence (and getting it subtly different).
func (r *playoutResolver) cycleAt(
	ctx context.Context, channelID string, at time.Time,
) ([]schedule.Slot, time.Duration, error) {
	if r.cycles == nil || r.channels == nil {
		_, slots, _, window, err := r.engine.CyclePreview(ctx, channelID, at)
		if err != nil || r.channels == nil {
			return slots, window, err
		}
		ch, cerr := r.channels.GetChannel(ctx, channelID)
		if cerr == nil && ch.Desired != nil && sameRollingWindow(at, r.now(), window) {
			return ch.Desired, window, nil
		}
		return slots, window, nil
	}

	ch, err := r.channels.GetChannel(ctx, channelID)
	if err != nil {
		// The engine loads the channel itself and will surface the real error (or succeed, if
		// this was a transient read) — so fall through rather than failing the row here.
		_, slots, _, window, cerr := r.engine.CyclePreview(ctx, channelID, at)
		return slots, window, cerr
	}

	// The bucket is what lets two requests a few seconds apart share an arrangement: the guide's
	// window start moves with every poll, so an exact-instant key would never hit. See
	// cycleBucket on why one minute cannot straddle a rule boundary by more than itself.
	bucket := at.Truncate(cycleBucket).Unix()
	key, ok := fingerprintChannel(channelID, ch.Lineup, ch.Policy, bucket)
	if !ok {
		_, slots, _, window, cerr := r.engine.CyclePreview(ctx, channelID, at)
		return slots, window, cerr
	}

	if slots, window, hit := r.cycles.get(key); hit {
		if ch.Desired != nil && sameRollingWindow(at, r.now(), window) {
			return ch.Desired, window, nil
		}
		return slots, window, nil
	}

	_, slots, _, window, err := r.engine.CyclePreview(ctx, channelID, at)
	if err != nil {
		return nil, 0, err
	}
	r.cycles.put(key, slots, window)
	if ch.Desired != nil && sameRollingWindow(at, r.now(), window) {
		return ch.Desired, window, nil
	}
	return slots, window, nil
}

// sameRollingWindow identifies the one forecast segment that is observed state: the segment that
// contains the resolver's current wall clock. An unbounded cycle has only one window. Window
// boundaries use Unix time, matching schedule.windowIndex and segmentedBroadcasts.
func sameRollingWindow(a, b time.Time, window time.Duration) bool {
	if window <= 0 {
		return true
	}
	seconds := int64(window / time.Second)
	if seconds <= 0 {
		return true
	}
	return a.Unix()/seconds == b.Unix()/seconds
}

// maxGuideSegments bounds how many rolling windows one guide request will re-resolve.
//
// The FE offers a 7-day forward span (RETENTION_DAYS + FORWARD_DAYS) against a 24h default
// window, so 8 covers the whole picker with a segment to spare. The cap is a backstop against a
// pathological window (a channel configured to a one-minute horizon must not turn one guide
// request into thousands of arrangements), not an expected limit: past it the tail of the span
// keeps the last segment's cycle, which is exactly today's behaviour for the whole span.
const maxGuideSegments = 8

// segmentedBroadcasts walks [from, to) one ROLLING WINDOW at a time, re-resolving the channel's
// cycle at each boundary, and concatenates the results.
//
// # Why this exists
//
// The scheduler ROTATES: windowSlice advances its start by the window index, so day 0 airs one
// slice of the deck and day 1 continues where it left off (§6.5, "over a full cycle every program
// airs"). Reconciliation commits that rotation into Desired at the new window; AiringNow reads the
// accepted value. A multi-day guide still has to forecast the later windows: resolving ONE cycle
// at `from` and looping it across the whole requested span would advertise a rotation that will
// never happen.
//
// Measured on a 27-title channel at the same instant 24h out: the guide advertised RoboCop / The
// Running Man / The Empire Strikes Back while the scheduler would arrange Lethal Weapon / Lethal
// Weapon 2 / Akira. Not merely under-reporting variety — a wrong EPG.
//
// (The XMLTV document escaped this: it publishes a 24h window, which is one rotation step, so it
// was right by construction. Loomarr's own grid queries up to 7 days, which is where it showed.)
//
// # Why here and not inside playout.BroadcastsBetween
//
// That walk is SHARED with the encoder and is deliberately a pure function of one slot list — it
// is what guarantees the grid and the encoder cannot disagree about what is on at 21:00. Teaching
// it to re-resolve would give it a dependency on the engine and put the invariant at risk. The
// segmentation belongs to the caller that spans multiple windows; each segment still goes through
// the identical shared walk.
//
// A window of 0 (WindowFull / unbounded) means the deck never rotates, so one segment is the
// whole answer — the pre-existing behaviour, unchanged.
func (r *playoutResolver) segmentedBroadcasts(
	ctx context.Context, channelID string, from, to time.Time,
	project func(slots []schedule.Slot, epoch, segFrom, segTo time.Time) []playout.Broadcast,
) ([]playout.Broadcast, error) {
	if r.channels == nil {
		return nil, errors.New("read channel playout anchor: channel reader is not configured")
	}
	ch, err := r.channels.GetChannel(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("read channel playout anchor %s: %w", channelID, err)
	}
	epoch, err := effectivePlayoutAnchor(ch)
	if err != nil {
		return nil, err
	}

	slots, window, err := r.cycleAt(ctx, channelID, from)
	if err != nil {
		return nil, err
	}
	// Unbounded window ⇒ no rotation ⇒ nothing to segment.
	if window <= 0 {
		return project(slots, epoch, from, to), nil
	}

	out := make([]playout.Broadcast, 0, 32)
	segFrom := from
	for i := 0; i < maxGuideSegments && segFrom.Before(to); i++ {
		// The end of the rolling window `segFrom` falls in — the instant the deck rotates.
		// Truncate on the window grid so segments land on the SAME boundaries windowIndex uses,
		// rather than on offsets from an arbitrary request time.
		segTo := segFrom.Truncate(window).Add(window)
		if !segTo.After(segFrom) { // Truncate is a no-op on a boundary; step a whole window
			segTo = segFrom.Add(window)
		}
		if segTo.After(to) {
			segTo = to
		}

		// The first segment's cycle is already resolved; later ones re-resolve AT THE SEGMENT,
		// which is what makes the rotation visible.
		segSlots := slots
		if i > 0 {
			segSlots, _, err = r.cycleAt(ctx, channelID, segFrom)
			if err != nil {
				// One segment failing must not empty the guide — keep what we have. The grid
				// renders a shorter forecast rather than nothing, the same posture the
				// per-channel failure takes in api.channelGuide.
				break
			}
		}
		out = append(out, project(segSlots, epoch, segFrom, segTo)...)
		segFrom = segTo
	}
	return out, nil
}

func (r *playoutResolver) BroadcastsBetween(
	ctx context.Context, channelID string, from, to time.Time,
) ([]playout.Broadcast, error) {
	bs, err := r.segmentedBroadcasts(ctx, channelID, from, to, playout.BroadcastsBetween)
	if err != nil {
		return nil, err
	}
	r.attachMetadata(ctx, bs)
	return bs, nil
}

// BroadcastsWithPending is BroadcastsBetween plus pending acquisitions, for the time grid
// (V13b). Same CyclePreview, same epoch, same arithmetic — only the projection differs, so the
// grid cannot disagree with the encoder about what airs when.
func (r *playoutResolver) BroadcastsWithPending(
	ctx context.Context, channelID string, from, to time.Time,
) ([]playout.Broadcast, error) {
	bs, err := r.segmentedBroadcasts(ctx, channelID, from, to, playout.BroadcastsWithPending)
	if err != nil {
		return nil, err
	}
	r.attachMetadata(ctx, bs)
	r.attachProvenance(ctx, bs)
	return bs, nil
}

// attachProvenance fills in each block's one-line "why is this here" (§12 hover card).
//
// Only for the GRID, never the XMLTV guide: an EPG lists what is on, and "acquiring · 62%" is
// an operator's answer, not a viewer's listing. That split is why this is a separate pass
// rather than part of attachMetadata.
//
// BEST-EFFORT, like the metadata pass: a store hiccup leaves the blocks exactly as they were.
// A hover card missing one line is far better than a guide that fails to load.
func (r *playoutResolver) attachProvenance(ctx context.Context, bs []playout.Broadcast) {
	if r.titles == nil || len(bs) == 0 {
		return
	}
	now := r.now()
	// One lookup per DISTINCT key: a channel airing six episodes of one series shares a
	// single acquisition record, so keying the cache on the provisioning key collapses those
	// to one read.
	cache := map[provision.Key]string{}
	for i := range bs {
		k := bs[i].Key
		if k == "" {
			continue // filler/flex have no acquisition to describe
		}
		if p, ok := cache[k]; ok {
			bs[i].Provenance = p
			continue
		}
		rec, err := r.titles.GetTitle(ctx, k)
		if err != nil {
			cache[k] = "" // remember the miss too, so one bad key is not re-read per block
			continue
		}
		p := provenanceOf(rec, now)
		cache[k] = p
		bs[i].Provenance = p
	}
}

// attachMetadata fills in descriptions, genres, years and ratings from the media server.
//
// ONE BULK CALL for the whole channel, which is the only reason this is affordable on a request
// a media server polls: Emby's `/Items?Ids=` takes a comma-separated list, and 120 episodes came
// back in 24ms on the dev stack. Per-item lookups would have been a round trip per programme and
// would have forced a cache.
//
// BEST-EFFORT BY DESIGN. A failure leaves the broadcasts exactly as they were — titles and times
// intact — because a guide with thin entries is far better than no guide. The media server is an
// external dependency on a path that must keep working when it is slow or briefly down.
func (r *playoutResolver) attachMetadata(ctx context.Context, bs []playout.Broadcast) {
	if r.lib == nil || len(bs) == 0 {
		return
	}
	ids := make([]string, 0, len(bs))
	for _, b := range bs {
		if b.LibraryItemID != "" {
			ids = append(ids, b.LibraryItemID)
		}
	}
	if len(ids) == 0 {
		return
	}

	meta, err := r.lib.ItemMetadataByID(ctx, ids)
	if err != nil && r.log != nil {
		// Logged, not returned: the partial map is still applied below. ItemMetadataByID
		// returns what it gathered alongside the error precisely so a slow page of a large
		// guide does not cost the whole document its descriptions.
		r.log.Debug("playout: guide metadata partially unavailable",
			"err", err, "resolved", len(meta), "wanted", len(ids))
	}
	for i := range bs {
		m, ok := meta[bs[i].LibraryItemID]
		if !ok {
			continue // removed from the library since the lineup was built; keep the title
		}
		bs[i].Description = m.Overview
		bs[i].Genres = m.Genres
		bs[i].Year = m.Year
		bs[i].Rating = m.OfficialRating
		bs[i].RuntimeMs = m.RuntimeMs
	}
}

// airingFiller resolves a break gap to ONE specific commercial file.
//
// The gap is a single slot on the timeline, but a pod is a SEQUENCE (bumper → ads → bumper),
// so this walks the pod by the offset already computed into the break and returns whichever
// clip covers that instant. Same shape as AiringAt one level down — and it must be, for the
// same reason: two viewers asking mid-break have to get the same clip at the same position.
//
// The pod comes from PodPreviewer, which is the SAME assembler and the SAME seed the reconciler
// and the UI preview use (§10's one-assembler rule). So the commercial that plays is the one the
// channel page promised, not a second opinion.
func (r *playoutResolver) airingFiller(
	ctx context.Context, channelID string, gap playout.Airing, now time.Time,
) (playout.Airing, string, error) {
	if r.pods == nil || r.fillerDir == "" {
		// Filler unconfigured: the break becomes a bounded break card rather than an error. Keep
		// the gap itself so the handler knows this is scheduled airtime and where it ends.
		return gap, "", nil
	}

	// PreviewAt, not Preview: the pod is seeded from THIS break's start, so consecutive
	// breaks play different adverts — and the guide's hover card, which calls the same
	// method with the same start, lists exactly what will air here.
	//
	// gap.Offset is how far into the break we are, so the break began that long ago.
	breakStart := now.Add(-gap.Offset).UnixMilli()
	pod, err := r.pods.PreviewAt(ctx, channelID, breakStart)
	if err != nil || len(pod.Entries) == 0 {
		// No pool, or the assembler could not fill this break. Not an error: §10's ladder
		// bottoms out at "nothing matched", and a channel must not fail to tune because it has
		// no ads.
		return gap, "", nil
	}

	// Walk the pod to the instant we are at INSIDE the break. gap.Offset is how far into the
	// break the wall clock has reached, which AiringAt already computed.
	into := gap.Offset
	for _, e := range pod.Entries {
		d := time.Duration(e.DurationMs) * time.Millisecond
		if d <= 0 {
			continue // a clip with no duration cannot occupy time; skip rather than divide by it
		}
		if into < d {
			// The embedded fallback bumper card has no file — it is generated, not played.
			if e.Path == "" {
				return gap, "", nil
			}
			// ⚠ ClipPath is the containment check, not a join: the id comes from the database
			// and a crafted `../` would otherwise stream an arbitrary file off the host.
			full, perr := filler.ClipPath(r.fillerDir, e.Path, "")
			if perr != nil {
				return gap, "", nil
			}
			// Count the airing (V28). THIS is the honest write point, and the two
			// tempting alternatives are both wrong:
			//   - pod ASSEMBLY re-runs on every 10m reconcile sweep, so it counts what was
			//     scheduled, over and over, not what aired;
			//   - counting every resolve as a new play would count viewer reconnects and child
			//     retries rather than airings.
			// Here the resolver passes the clip's SCHEDULED start on every resolve. The store
			// makes that stable identity idempotent. Requiring `into == 0` instead lost every
			// real transition: a finite encoder child asks for its successor milliseconds after
			// the boundary, not at the exact nanosecond.
			//
			// ⚠ Internal playout only. A Tunarr-backed channel airs its filler through Tunarr,
			// which never reports back, so those clips stay at zero — "not counted here", not
			// "never played". The DTO says which.
			//
			// ⚠ Keyed by Hash, not Path. `RecordClipPlay` is `WHERE hash = ?`; this passed
			// `e.Path` from V38c until V41 and every counter stayed at zero. The error below is
			// swallowed by design, so the miss was silent — the guard is `PodEntry.Hash` now
			// existing at all, plus the store's ClipKeyIsHashNotPath conformance test.
			if e.Hash != "" && r.clipPlays != nil {
				startedAt := now.Add(-into)
				recorded, err := r.clipPlays.RecordClipPlay(ctx, channelID, e.Hash, startedAt)
				if err != nil {
					// Telemetry, never correctness: a failed count must not stop a break from
					// airing. Logged at debug because a pruned clip is an ordinary race.
					_ = err
				} else if recorded && r.metrics != nil {
					r.metrics.FillerRotationAired(e.RecentRepeat, e.RecentRepeat && !e.RotationPinned, e.RotationPinned)
				}
			}
			remaining := d - into
			if gap.Remaining > 0 && gap.Remaining < remaining {
				remaining = gap.Remaining
			}
			return playout.Airing{
				StartedAt:       now.Add(-into),
				Identity:        e.Hash,
				ScheduleBlockID: gap.ScheduleBlockID,
				Kind:            schedule.SlotFiller,
				// Source, not LibraryItemID: this is a local file, not a media-server item.
				// Playable() checks Source for exactly this case.
				Source:    full,
				Title:     e.Name,
				Offset:    into,
				Remaining: remaining,
			}, full, nil
		}
		into -= d
	}

	// The pod is shorter than the break gap. Real: a 30s break with 20s of clips. The remainder
	// is the offline card rather than a repeat, because repeating would mean the same ad twice
	// in one break — worse than a moment of card.
	return gap, "", nil
}

// Profile is the encode profile for the next program, resolved against live load.
//
// Called once per program (each child is a fresh process), so the ladder adapts as channels come
// and go: the first channel on an idle box gets the top rung, and a fifth channel starting up
// steps everyone down as their next program begins. That is the "best picture the hardware
// sustains, then adapt" policy §9.1 states, and it is only implementable because the child
// processes are short-lived.
// AudioTrackFor resolves which audio track a source should play (§9.1).
//
// Returns an index among AUDIO streams — the `N` in `-map 0:a:N`. Zero means "the file's first
// track", which is both the historical behaviour and the safe answer for every failure below.
//
// ⚠ BEST-EFFORT, NEVER FATAL, and every branch here degrades the same way for the same reason:
// a probe that fails must cost the viewer the language preference, never the programme. No
// preference set, no prober wired, an unreachable media server, an ffprobe that is missing or
// slow — all of them fall through to track 0 and the channel keeps playing. The alternative is a
// channel that goes dark because a metadata read timed out, which trades a small annoyance for a
// total outage.
//
// One probe per PROGRAMME, not per request: the block supervisor asks for a new program at each
// boundary, so this runs about as often as a film is long. That is what makes an exec on the
// broadcast path affordable — and why it must not become per-segment.
const inventoryAudioFreshness = 24 * time.Hour

func (r *playoutResolver) AudioTrackFor(ctx context.Context, channelID, libraryItemID, streamURL string) int {
	if r.audioLanguage == nil || streamURL == "" {
		return 0
	}
	prefer := r.preferredAudioLanguage(ctx, channelID)
	if strings.TrimSpace(prefer) == "" {
		// Explicitly cleared ⇒ the operator wants ffmpeg's original behaviour. Skip the probe
		// entirely rather than paying for an answer that cannot change the outcome.
		return 0
	}
	if tracks, ok := r.inventoryAudioTracks(ctx, libraryItemID, streamURL); ok {
		return playout.PickAudioTrack(tracks, prefer)
	}
	if r.probeSource == nil {
		return 0
	}
	observed, err := r.probeSource(ctx, streamURL)
	if err != nil {
		if r.log != nil {
			r.log.Debug("playout: audio tracks not probed, using the first",
				"err", err)
		}
		return 0
	}
	tracks := observed.AudioTracks()
	r.recordSourceMeasurement(ctx, libraryItemID, streamURL, observed)
	return playout.PickAudioTrack(tracks, prefer)
}

// AudioTrackFromInventory applies the channel's audio policy using only Loomarr-owned durable
// metadata. A missing observation is explicit so the planner can spend its bounded refresh budget;
// this path never contacts the Library server and never invokes ffprobe.
func (r *playoutResolver) AudioTrackFromInventory(
	ctx context.Context, channelID, libraryItemID string,
) (int, bool) {
	if r.audioLanguage == nil {
		return 0, true
	}
	prefer := r.preferredAudioLanguage(ctx, channelID)
	if strings.TrimSpace(prefer) == "" {
		return 0, true
	}
	if r.inventory == nil || r.lib == nil || strings.TrimSpace(libraryItemID) == "" {
		return 0, false
	}
	origin, err := r.lib.InventoryOrigin(libraryItemID)
	if err != nil {
		return 0, false
	}
	source, ok, err := r.inventory.ResolveSource(ctx, inventory.SourceRequest{
		Item: inventory.ItemRef{Origin: &origin}, Kinds: []inventory.SourceKind{inventory.SourceLibraryOriginal},
		RequiredCoverage: []string{"audioStreams"},
	})
	if err != nil || !ok {
		return 0, false
	}
	return playout.PickAudioTrack(playoutAudioTracks(source.Observation.Facts.Streams), prefer), true
}

func (r *playoutResolver) preferredAudioLanguage(ctx context.Context, channelID string) string {
	prefer := r.audioLanguage()
	// A channel override wins over the instance default. Failure to reload the channel is
	// deliberately non-fatal: AiringNow already resolved enough state to play, so falling back
	// to the global preference is better than taking the programme off air for an optional track.
	if r.channels != nil && channelID != "" {
		if ch, err := r.channels.GetChannel(ctx, channelID); err == nil {
			prefer = schedule.ResolveAudioLanguage(ch.Policy, prefer)
		}
	}
	return prefer
}

func (r *playoutResolver) inventoryAudioTracks(
	ctx context.Context,
	libraryItemID, streamURL string,
) ([]playout.AudioTrack, bool) {
	if r.inventory == nil {
		return nil, false
	}
	if localOrigin, ok := r.ensureLocalInventorySource(ctx, streamURL); ok {
		source, found, err := r.inventory.ResolveSource(ctx, inventory.SourceRequest{
			Item: inventory.ItemRef{Origin: &localOrigin}, Now: r.inventoryNow(),
			Kinds: []inventory.SourceKind{inventory.SourceLocalFile}, RequiredCoverage: []string{"audioStreams"},
		})
		if err == nil && found {
			return playoutAudioTracks(source.Observation.Facts.Streams), true
		}
		// A local file's stat revision is the authoritative freshness boundary. Library metadata
		// cannot validate changed local bytes, so a miss falls directly through to one probe.
		return nil, false
	}
	if r.lib == nil || strings.TrimSpace(libraryItemID) == "" {
		return nil, false
	}
	origin, err := r.lib.InventoryOrigin(libraryItemID)
	if err != nil {
		return nil, false
	}
	request := inventory.SourceRequest{
		Item: inventory.ItemRef{Origin: &origin}, Now: r.inventoryNow(), MaxAge: inventoryAudioFreshness,
		RequiredCoverage: []string{"audioStreams"},
	}
	if source, ok, resolveErr := r.inventory.ResolveSource(ctx, request); resolveErr == nil && ok {
		return playoutAudioTracks(source.Observation.Facts.Streams), true
	}

	snapshot, present, importErr := r.lib.InventorySnapshot(ctx, libraryItemID)
	if importErr == nil && present {
		if _, applyErr := r.inventory.ApplySnapshot(ctx, snapshot); applyErr == nil {
			if source, ok, resolveErr := r.inventory.ResolveSource(ctx, request); resolveErr == nil && ok {
				return playoutAudioTracks(source.Observation.Facts.Streams), true
			}
		} else if r.log != nil {
			r.log.Debug("playout: library media observation not persisted", "err", applyErr)
		}
	} else if importErr != nil && r.log != nil {
		r.log.Debug("playout: library media observation unavailable", "err", importErr)
	}
	return nil, false
}

func (r *playoutResolver) recordSourceMeasurement(
	ctx context.Context,
	libraryItemID, streamURL string,
	observed playout.SourceObservation,
) {
	if r.inventory == nil {
		return
	}
	var ref inventory.ItemRef
	if localOrigin, ok := r.ensureLocalInventorySource(ctx, streamURL); ok {
		ref = inventory.ItemRef{Origin: &localOrigin}
	} else {
		if r.lib == nil || strings.TrimSpace(libraryItemID) == "" {
			return
		}
		origin, err := r.lib.InventoryOrigin(libraryItemID)
		if err != nil {
			return
		}
		ref = inventory.ItemRef{Origin: &origin}
		if _, ok, itemErr := r.inventory.Item(ctx, ref); itemErr == nil && !ok {
			_ = r.ensureRemoteInventorySource(ctx, origin)
		}
	}
	source, ok, err := r.inventory.ResolveSource(ctx, inventory.SourceRequest{Item: ref})
	if err != nil || !ok {
		return
	}
	facts := inventoryFactsOf(observed)
	audioCoverage := inventory.CoverageEmpty
	for _, stream := range facts.Streams {
		if stream.Kind == inventory.StreamAudio {
			audioCoverage = inventory.CoveragePresent
			break
		}
	}
	measurement := inventory.Measurement{
		SourceID: source.ID, Revision: source.Revision,
		Observation: inventory.Observation[inventory.SourceFacts]{
			SchemaVersion: 1, ObservedAt: r.inventoryNow(),
			Coverage: map[string]inventory.Coverage{
				"streams": inventory.CoveragePresent, "audioStreams": audioCoverage,
			},
			Facts: facts,
		},
	}
	if err := r.inventory.RecordMeasurement(ctx, measurement); err != nil && r.log != nil {
		r.log.Debug("playout: measured audio observation not persisted", "err", err)
	}
}

// ResolvePreparedSource performs the bounded control-plane half of prepared playout. It keeps
// direct disk first, imports the exact source into Loomarr's Inventory, and returns only durable
// identity plus a transient input hint for preferred-audio selection.
func (r *playoutResolver) ResolvePreparedSource(
	ctx context.Context, libraryItemID string, pathMap library.PathMap,
) (prepared.Source, string, bool) {
	if r == nil || r.lib == nil || r.inventory == nil || strings.TrimSpace(libraryItemID) == "" {
		return prepared.Source{}, "", false
	}
	lib := r.lib.Snapshot()
	input := lib.ResolveInput(ctx, libraryItemID, pathMap, library.StatReadableFile)
	if input.URL == "" {
		return prepared.Source{}, "", false
	}
	request := inventory.SourceRequest{Now: r.inventoryNow()}
	if input.Kind == library.InputFile {
		origin, ok := r.ensureLocalInventorySource(ctx, input.URL)
		if !ok {
			return prepared.Source{}, "", false
		}
		request.Item = inventory.ItemRef{Origin: &origin}
		request.Kinds = []inventory.SourceKind{inventory.SourceLocalFile}
	} else {
		origin, err := lib.InventoryOrigin(libraryItemID)
		if err != nil {
			return prepared.Source{}, "", false
		}
		if snapshot, present, snapshotErr := lib.InventorySnapshot(ctx, libraryItemID); snapshotErr == nil && present {
			if _, applyErr := r.inventory.ApplySnapshot(ctx, snapshot); applyErr != nil {
				return prepared.Source{}, "", false
			}
		}
		request.Item = inventory.ItemRef{Origin: &origin}
		request.Kinds = []inventory.SourceKind{inventory.SourceLibraryOriginal}
	}
	resolved, ok, err := r.inventory.ResolveSource(ctx, request)
	if err != nil || !ok {
		return prepared.Source{}, "", false
	}
	if input.Kind == library.InputHTTP && resolved.Locator.ExternalSourceID != "" {
		input.URL = lib.StreamURLForSource(resolved.Locator.ExternalItemID, resolved.Locator.ExternalSourceID)
	}
	if input.URL == "" {
		return prepared.Source{}, "", false
	}
	return prepared.Source{
		ItemID: string(resolved.ItemID), SourceID: string(resolved.ID), Revision: resolved.Revision,
	}, input.URL, true
}

// ResolvePreparedSourceFromInventory resolves an already imported Library original without a
// media-server request. When a path map exists it deliberately misses: the refresh path must first
// attempt the operator's direct-disk mapping instead of silently rebinding the item to HTTP.
func (r *playoutResolver) ResolvePreparedSourceFromInventory(
	ctx context.Context, libraryItemID string, pathMap library.PathMap,
) (prepared.Source, string, bool) {
	if r == nil || r.lib == nil || r.inventory == nil || len(pathMap) > 0 ||
		strings.TrimSpace(libraryItemID) == "" {
		return prepared.Source{}, "", false
	}
	lib := r.lib.Snapshot()
	origin, err := lib.InventoryOrigin(libraryItemID)
	if err != nil {
		return prepared.Source{}, "", false
	}
	resolved, ok, err := r.inventory.ResolveSource(ctx, inventory.SourceRequest{
		Item: inventory.ItemRef{Origin: &origin}, Kinds: []inventory.SourceKind{inventory.SourceLibraryOriginal},
	})
	if err != nil || !ok {
		return prepared.Source{}, "", false
	}
	input := lib.StreamURLForSource(resolved.Locator.ExternalItemID, resolved.Locator.ExternalSourceID)
	if input == "" {
		return prepared.Source{}, "", false
	}
	return prepared.Source{
		ItemID: string(resolved.ItemID), SourceID: string(resolved.ID), Revision: resolved.Revision,
	}, input, true
}

// PreparedSourceCurrent compares a durable binding with Loomarr's latest Inventory observation.
// It performs no source or Library I/O and is called only by the readiness control plane, never tune.
func (r *playoutResolver) PreparedSourceCurrent(ctx context.Context, selected prepared.Source) bool {
	if r == nil || r.inventory == nil || strings.TrimSpace(selected.ItemID) == "" {
		return false
	}
	item, ok, err := r.inventory.Item(ctx, inventory.ItemRef{ID: inventory.ItemID(selected.ItemID)})
	if err != nil || !ok {
		return false
	}
	_, _, ok = preparedInventoryOrigin(item, selected)
	return ok
}

// OpenInput is Source Access for background preparation. It verifies the exact Inventory revision,
// then exposes a protected local path or freshly minted authenticated original-file URL only to
// FFmpeg. Tune never calls this method.
func (r *playoutResolver) OpenInput(ctx context.Context, selected prepared.Source) (prepared.Input, error) {
	if r == nil || r.inventory == nil || strings.TrimSpace(selected.ItemID) == "" ||
		strings.TrimSpace(selected.SourceID) == "" || strings.TrimSpace(selected.Revision) == "" {
		return prepared.Input{}, prepared.ErrInvalidSource
	}
	item, ok, err := r.inventory.Item(ctx, inventory.ItemRef{ID: inventory.ItemID(selected.ItemID)})
	if err != nil || !ok {
		return prepared.Input{}, prepared.ErrSourceChanged
	}
	source, origin, ok := preparedInventoryOrigin(item, selected)
	if !ok {
		return prepared.Input{}, prepared.ErrSourceChanged
	}
	switch source.Kind {
	case inventory.SourceLocalFile:
		info, statErr := os.Stat(origin.Locator.Path)
		if statErr != nil || !info.Mode().IsRegular() || localInventoryRevision(info) != selected.Revision ||
			!library.StatReadableFile(origin.Locator.Path) {
			return prepared.Input{}, prepared.ErrSourceChanged
		}
		return prepared.LocalInput(origin.Locator.Path), nil
	case inventory.SourceLibraryOriginal:
		if r.lib == nil {
			return prepared.Input{}, prepared.ErrSourceChanged
		}
		lib := r.lib.Snapshot()
		current, originErr := lib.InventoryOrigin(origin.Locator.ExternalItemID)
		if originErr != nil || current.Authority != origin.Locator.Authority {
			return prepared.Input{}, prepared.ErrSourceChanged
		}
		snapshot, present, refreshErr := lib.InventorySnapshot(ctx, origin.Locator.ExternalItemID)
		if refreshErr != nil || !present {
			return prepared.Input{}, prepared.ErrSourceChanged
		}
		if _, applyErr := r.inventory.ApplySnapshot(ctx, snapshot); applyErr != nil {
			return prepared.Input{}, prepared.ErrSourceChanged
		}
		item, ok, itemErr := r.inventory.Item(ctx, inventory.ItemRef{ID: inventory.ItemID(selected.ItemID)})
		if itemErr != nil || !ok {
			return prepared.Input{}, prepared.ErrSourceChanged
		}
		_, origin, ok = preparedInventoryOrigin(item, selected)
		if !ok {
			return prepared.Input{}, prepared.ErrSourceChanged
		}
		input := lib.StreamURLForSource(origin.Locator.ExternalItemID, origin.Locator.ExternalSourceID)
		if input == "" {
			return prepared.Input{}, prepared.ErrSourceChanged
		}
		return prepared.HTTPInput(input), nil
	}
	return prepared.Input{}, prepared.ErrSourceChanged
}

func preparedInventoryOrigin(
	item inventory.Item, selected prepared.Source,
) (inventory.Source, inventory.SourceOrigin, bool) {
	for _, source := range item.Sources {
		if string(source.ID) != selected.SourceID || source.Revision != selected.Revision {
			continue
		}
		for _, origin := range source.Origins {
			if origin.MissingAt.IsZero() {
				return source, origin, true
			}
		}
	}
	return inventory.Source{}, inventory.SourceOrigin{}, false
}

func (r *playoutResolver) ensureLocalInventorySource(ctx context.Context, input string) (inventory.OriginKey, bool) {
	info, err := os.Stat(input)
	if err != nil || info.IsDir() || !info.Mode().IsRegular() {
		return inventory.OriginKey{}, false
	}
	digest := sha256.Sum256([]byte(input))
	origin := inventory.OriginKey{
		Authority: "local-playout:v1", ExternalItemID: hex.EncodeToString(digest[:16]),
	}
	revision := localInventoryRevision(info)
	if item, ok, itemErr := r.inventory.Item(ctx, inventory.ItemRef{Origin: &origin}); itemErr == nil && ok {
		for _, source := range item.Sources {
			if source.Kind == inventory.SourceLocalFile && source.Revision == revision {
				return origin, true
			}
		}
	}
	at := r.inventoryNow()
	_, err = r.inventory.ApplySnapshot(ctx, inventory.Snapshot{
		Origin: origin, Kind: "unknown",
		Observation: inventory.Observation[inventory.ItemFacts]{
			SchemaVersion: 1, ObservedAt: at,
			Coverage: map[string]inventory.Coverage{"sources": inventory.CoveragePresent},
		},
		Sources: []inventory.SourceSnapshot{{
			ExternalSourceID: "file", Kind: inventory.SourceLocalFile,
			Revision: revision,
			Locator:  inventory.Locator{Path: input},
			Observation: inventory.Observation[inventory.SourceFacts]{
				SchemaVersion: 1, ObservedAt: at,
				Coverage: map[string]inventory.Coverage{"sourceStat": inventory.CoveragePresent},
				Facts: inventory.SourceFacts{
					SizeBytes: info.Size(), ModifiedUnixNano: info.ModTime().UnixNano(),
				},
			},
		}},
	})
	return origin, err == nil
}

func localInventoryRevision(info os.FileInfo) string {
	return fmt.Sprintf("stat:%d:%d", info.Size(), info.ModTime().UnixNano())
}

func (r *playoutResolver) ensureRemoteInventorySource(ctx context.Context, origin inventory.OriginKey) error {
	at := r.inventoryNow()
	_, err := r.inventory.ApplySnapshot(ctx, inventory.Snapshot{
		Origin: origin, Kind: "unknown",
		Observation: inventory.Observation[inventory.ItemFacts]{
			SchemaVersion: 1, ObservedAt: at,
			Coverage: map[string]inventory.Coverage{"sources": inventory.CoveragePresent},
		},
		Sources: []inventory.SourceSnapshot{{
			ExternalSourceID: "default", Kind: inventory.SourceLibraryOriginal, Revision: "unversioned",
			Locator: inventory.Locator{
				Authority: origin.Authority, ExternalItemID: origin.ExternalItemID, ExternalSourceID: "default",
			},
			Observation: inventory.Observation[inventory.SourceFacts]{SchemaVersion: 1, ObservedAt: at},
		}},
	})
	return err
}

func inventoryFactsOf(observed playout.SourceObservation) inventory.SourceFacts {
	facts := inventory.SourceFacts{
		Container: observed.Container, DurationMillis: observed.DurationMillis, Bitrate: observed.Bitrate,
		UnsafePreroll: observed.UnsafePreroll,
	}
	for _, stream := range observed.Streams {
		facts.Streams = append(facts.Streams, inventory.Stream{
			Index: stream.Index, Kind: inventory.StreamKind(stream.Kind), Codec: stream.Codec,
			Profile: stream.Profile, Level: stream.Level, Language: stream.Language, Title: stream.Title,
			Disposition: inventory.Disposition{Default: stream.Default, Forced: stream.Forced},
			Channels:    stream.Channels, ChannelLayout: stream.ChannelLayout, SampleRate: stream.SampleRate,
			Width: stream.Width, Height: stream.Height, FrameRate: stream.FrameRate,
			PixelFormat: stream.PixelFormat, ColorSpace: stream.ColorSpace,
			ColorTransfer: stream.ColorTransfer, ColorPrimaries: stream.ColorPrimaries,
			HDR: stream.HDR, Interlaced: stream.Interlaced,
			SubtitleFormat: func() string {
				if stream.Kind == string(inventory.StreamSubtitle) {
					return stream.Codec
				}
				return ""
			}(),
		})
	}
	return facts
}

func playoutFormatOf(facts inventory.SourceFacts) playout.MediaFormat {
	format := playout.MediaFormat{
		Container:    strings.ToLower(strings.TrimSpace(facts.Container)),
		Duration:     float64(facts.DurationMillis) / float64(time.Second/time.Millisecond),
		Bitrate:      facts.Bitrate,
		VideoPreroll: facts.UnsafePreroll,
	}
	for _, stream := range facts.Streams {
		switch stream.Kind {
		case inventory.StreamVideo:
			if format.VideoCodec != "" {
				continue
			}
			format.VideoCodec = strings.ToLower(strings.TrimSpace(stream.Codec))
			format.Width = stream.Width
			format.Height = stream.Height
			format.FrameRate = parseInventoryRational(stream.FrameRate)
			format.PixelFormat = strings.ToLower(strings.TrimSpace(stream.PixelFormat))
			format.ColorTransfer = strings.ToLower(strings.TrimSpace(stream.ColorTransfer))
		case inventory.StreamAudio:
			if format.AudioCodec != "" {
				continue
			}
			format.AudioCodec = strings.ToLower(strings.TrimSpace(stream.Codec))
			format.AudioChannels = stream.Channels
			format.AudioSampleRate = stream.SampleRate
		}
	}
	return format
}

func parseInventoryRational(value string) float64 {
	numerator, denominator, found := strings.Cut(strings.TrimSpace(value), "/")
	if !found {
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return parsed
	}
	n, numeratorErr := strconv.ParseFloat(strings.TrimSpace(numerator), 64)
	d, denominatorErr := strconv.ParseFloat(strings.TrimSpace(denominator), 64)
	if numeratorErr != nil || denominatorErr != nil || d == 0 {
		return 0
	}
	return n / d
}

func (r *playoutResolver) inventoryNow() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func playoutAudioTracks(streams []inventory.Stream) []playout.AudioTrack {
	tracks := make([]playout.AudioTrack, 0)
	for _, stream := range streams {
		if stream.Kind == inventory.StreamAudio {
			tracks = append(tracks, playout.AudioTrack{SourceIndex: stream.Index, Language: stream.Language})
		}
	}
	return tracks
}

// Tracks probes the audio + subtitle tracks of what's airing on a channel right now (§9.1 Watch,
// V46). It resolves the current program's source URL exactly as the broadcast path does, then
// probes it.
//
// BEST-EFFORT, like AudioTrackFor: nothing airing, no prober wired, or a failed probe all return
// empty tracks rather than an error, so the Watch pickers degrade to "just the channel default"
// instead of erroring. The one thing it does NOT swallow is a genuine store/resolve error (a
// missing channel), which the handler needs to render a 404 rather than an empty list.
func (r *playoutResolver) Tracks(ctx context.Context, channelID string) (playout.MediaTracks, error) {
	airing, streamURL, err := r.AiringNow(ctx, channelID)
	if err != nil {
		return playout.MediaTracks{}, err
	}
	if r.probeTracks == nil || !airing.Playable() || streamURL == "" {
		return playout.MediaTracks{}, nil
	}
	tracks, err := r.probeTracks(ctx, streamURL)
	if err != nil {
		if r.log != nil {
			r.log.Debug("playout: tracks not probed for Watch pickers", "channel", channelID, "err", err)
		}
		return playout.MediaTracks{}, nil // degrade, don't fail the UI request
	}
	return tracks, nil
}

// PlanFor decides the copy/transcode plan for an input against a target (§9.1 direct play, V47) —
// probe the source, then PlanCopy. This is what makes a program direct-play: an h264 file to the
// browser copies the video and transcodes at most the audio.
//
// BEST-EFFORT, and it fails SAFE toward TRANSCODE: no prober wired, no input, or a probe error all
// return the zero CopyPlan (copy nothing → transcode both). A copy of an unprobed source could ship
// a codec the target cannot play — a black frame — whereas an unnecessary transcode is merely slow.
// So when we cannot know, we transcode.
//
// It returns the PROBE ALONGSIDE THE PLAN, and that is the point of the second return value. This
// function used to reduce a fully-parsed MediaFormat — geometry, framerate, pixel format, colour
// transfer, duration, bitrate — to two booleans and discard the rest, which is why copyplan.go's
// "Probe once, keep it all… so later features need no second ffprobe" was a promise nothing
// collected on, and why its HDR() had no production caller. The zero MediaFormat travels with the
// zero CopyPlan on every failure path, so an unprobed source reads as SDR/unknown rather than as
// anything asserted.
func (r *playoutResolver) PlanFor(
	ctx context.Context, input string, target playout.EncodePlan,
) (playout.CopyPlan, playout.MediaFormat) {
	if input == "" {
		return playout.CopyPlan{}, playout.MediaFormat{} // transcode both
	}
	if r.inventory != nil {
		if origin, ok := r.ensureLocalInventorySource(ctx, input); ok {
			source, found, err := r.inventory.ResolveSource(ctx, inventory.SourceRequest{
				Item: inventory.ItemRef{Origin: &origin}, Now: r.inventoryNow(),
				Kinds: []inventory.SourceKind{inventory.SourceLocalFile}, RequiredCoverage: []string{"streams"},
			})
			if err == nil && found {
				format := playoutFormatOf(source.Observation.Facts)
				return playout.PlanCopy(format, target), format
			}
		}
	}
	if r.probeFormat == nil {
		return playout.CopyPlan{}, playout.MediaFormat{} // transcode both
	}
	f, err := r.probeFormat(ctx, input)
	if err != nil {
		if r.log != nil {
			r.log.Debug("playout: format not probed, transcoding", "input", library.RedactStreamURL(input), "err", err)
		}
		return playout.CopyPlan{}, playout.MediaFormat{}
	}
	return playout.PlanCopy(f, target), f
}

func (r *playoutResolver) Profile(ctx context.Context) playout.Profile {
	enc := playout.Encoder(r.encoder())
	if enc == "" {
		// A matching persisted result is available to this first tune after only its host fingerprint
		// matches. Its real encoder validation runs asynchronously; a miss likewise starts the full
		// benchmark in the background and uses software until a safe result is ready.
		//
		// This used to fall straight through to libx264 with a comment claiming the
		// capability prober's choice "was stored at wizard time" — but nothing ever stored
		// it, so the fallback was unconditional and a box with a working GPU silently
		// encoded in software forever. Detect trial-encodes each candidate, so its answer is
		// measured rather than inferred from `ffmpeg -encoders` (which lists encoders the
		// hardware cannot actually run — the exact trap that took a live channel down: the
		// host listed h264_vulkan, the container had no /dev/dri).
		r.reuseEncoderEvidence(ctx)
		enc = playout.EncoderSoftware
		if r.detectReady.Load() {
			enc = r.publishedEncoder()
		}
		r.warmEncoderDetection(ctx)
	}
	return playout.Resolve(playout.TierFor(r.tier()), enc, r.capacity(), r.activeChannels())
}

func (r *playoutResolver) reuseEncoderEvidence(ctx context.Context) {
	r.detectEvidence.Do(func() {
		var capacity playout.Capacity
		var ok bool
		if r.loadCapabilityEvidence != nil {
			capacity, ok = r.loadCapabilityEvidence(ctx)
		} else {
			bin, gpu, root := r.capabilityInputs()
			capacity, ok = playout.LoadMatchingObservedCapabilityEvidence(
				ctx, bin, playout.DefaultProfile(), gpu, root,
			)
		}
		if ok {
			r.publishDetectedCapacity(capacity, true)
		}
	})
}

// warmEncoderDetection starts the expensive host probe once without putting it on a viewer's
// critical path. The composition root's lifetime wins over an individual request context so one
// TV disconnecting cannot discard the result every later tune needs.
func (r *playoutResolver) warmEncoderDetection(fallback context.Context) {
	r.detectStart.Do(func() {
		ctx := r.detectContext
		if ctx == nil {
			ctx = context.WithoutCancel(fallback)
		}
		go r.detectedEncoder(ctx)
	})
}

// detectedEncoder returns the best encoder that actually WORKS here, probing once. Control-plane
// callers may wait for it; Profile deliberately uses warmEncoderDetection + detectReady instead.
//
// Lazily and exactly once per process, which is the only workable timing: a full Detect trials
// every candidate and then measures the winner warm. The first demand starts it asynchronously;
// everything after completion reads the result. A matching persisted hardware result substitutes
// one short real TS encode, while an FFmpeg/GPU/profile mismatch or failed validation performs the
// complete measurement and atomically replaces that evidence.
func (r *playoutResolver) detectedEncoder(ctx context.Context) playout.Encoder {
	r.detectOnce.Do(func() {
		bin, gpu, root := r.capabilityInputs()
		cap, reused := playout.DetectObservedWithEvidence(
			ctx, bin, playout.DefaultProfile(), gpu, root, r.processDiagnostics,
		)
		r.publishDetectedCapacity(cap, reused)
	})
	return r.publishedEncoder()
}

func (r *playoutResolver) capabilityInputs() (bin, gpu, root string) {
	r.inputsOnce.Do(func() {
		if r.ffmpegPath != nil {
			r.capabilityBin = r.ffmpegPath()
		}
		if r.capabilityBin == "" {
			r.capabilityBin = "ffmpeg"
		}
		if r.gpuName != nil {
			r.capabilityGPU = r.gpuName()
		}
		if r.capabilityRoot != nil {
			r.capabilityPath = r.capabilityRoot()
		}
	})
	return r.capabilityBin, r.capabilityGPU, r.capabilityPath
}

func (r *playoutResolver) publishDetectedCapacity(cap playout.Capacity, reused bool) {
	r.detectedMu.Lock()
	r.detected = cap.Chosen
	r.detectedMu.Unlock()
	r.maxChannels.Store(int64(cap.MaxChannels))
	if r.log != nil {
		skipped := make([]string, 0, len(cap.All))
		for _, c := range cap.All {
			if !c.Works {
				skipped = append(skipped, string(c.Encoder)+": "+c.Err)
			}
		}
		r.log.Info("playout: encoder probed",
			"chosen", cap.Chosen, "measured_max_channels", cap.MaxChannels,
			"reused_verified_evidence", reused, "skipped", skipped)
	}
	r.detectReady.Store(true)
}

func (r *playoutResolver) publishedEncoder() playout.Encoder {
	r.detectedMu.RLock()
	defer r.detectedMu.RUnlock()
	return r.detected
}

// HWEncodeSlots is how many concurrent HARDWARE encodes this box sustains — the capability probe's
// measured_max_channels. It drives the playout admission gate: a transcode that cannot get a slot
// goes straight to software rather than piling onto a saturated GPU and stalling. Runs the same
// memoised probe as detectedEncoder (first call pays; the rest read the cache). Returns 0 when the
// box has no working hardware encoder (software-only) — the gate treats 0 as "never admit hardware",
// which is correct: there is no hardware to admit.
func (r *playoutResolver) HWEncodeSlots(ctx context.Context) int {
	if r.detectedEncoder(ctx) == playout.EncoderSoftware {
		return 0
	}
	return int(r.maxChannels.Load())
}

// effectivePlayoutAnchor enforces the persisted timeline origin. A live channel without
// one is corrupt control-plane state; guessing would let the encoder and guide diverge.
func effectivePlayoutAnchor(ch store.Channel) (time.Time, error) {
	if ch.PlayoutAnchor.IsZero() {
		return time.Time{}, fmt.Errorf("channel %s has no playout anchor", ch.ID)
	}
	return ch.PlayoutAnchor, nil
}

// playoutSpawner builds the SESSION encoder for a channel: a block-aware Go supervisor feeds one
// long-lived `-c copy` mux. Each finite HTTP response is an explicit Airing boundary.
//
// This is the parent, not a program child. It never re-encodes — all the encoding happens in the
// per-program children the supervisor requests — which is what makes one channel cost one
// encode regardless of how many programs it plays.
func playoutSpawner(
	ffmpegBin string, publicURL func() string, token func() string, log *slog.Logger,
	processDiagnostics *diagnostics.ProcessManager, preparedSource func() playout.BlockSource,
) playout.Spawner {
	return func(ctx context.Context, channelID string, target playout.EncodePlan) (*playout.Process, error) {
		base := publicURL()
		if base == "" {
			return nil, fmt.Errorf("playout: server.public_url is not set, so the session cannot open blocks")
		}
		var prepared playout.BlockSource
		if preparedSource != nil {
			prepared = preparedSource()
		}
		source := playoutBlockSource(base, token, http.DefaultClient, prepared)
		return playout.BlockSpawner(ffmpegBin, source, log, processDiagnostics)(ctx, channelID, target)
	}
}

// playoutBlockSource owns the internal HTTP hop and the session-scoped broadcast token. The first
// child chooses a format from the load ladder; every later child must acknowledge that exact format
// before its bytes can enter the long-lived mux.
func playoutBlockSource(
	base string, token func() string, client *http.Client, preparedSource playout.BlockSource,
) playout.BlockSource {
	var broadcast string
	return func(blockCtx context.Context, blockChannel string, blockPlan playout.EncodePlan) (playout.Block, error) {
		if preparedSource != nil {
			block, err := preparedSource(blockCtx, blockChannel, blockPlan)
			if err == nil && block.Content != nil {
				format, valid := playout.ParseBroadcastFormat(block.Format.String())
				canonical := format.String()
				if valid && (broadcast == "" || canonical == broadcast) {
					broadcast = canonical
					block.Format = format
					return block, nil
				}
				_ = block.Content.Close()
			}
			if blockCtx.Err() != nil {
				return playout.Block{}, blockCtx.Err()
			}
		}
		query := url.Values{
			"token": []string{token()},
			"plan":  []string{blockPlan.String()},
		}
		if broadcast != "" {
			query.Set(api.PlayoutBroadcastFormatQuery, broadcast)
		}
		programURL := fmt.Sprintf("%s/v1/playout/program/%s?%s",
			strings.TrimRight(base, "/"), url.PathEscape(blockChannel), query.Encode())
		req, err := http.NewRequestWithContext(blockCtx, http.MethodGet, programURL, nil)
		if err != nil {
			return playout.Block{}, err
		}
		if parent, ok := diagnostics.ProcessSpecFromContext(blockCtx); ok && parent.ParentRunID != "" {
			req.Header.Set(api.PlayoutParentProcessRunHeader, parent.ParentRunID)
		}
		resp, err := client.Do(req)
		if err != nil {
			return playout.Block{}, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			return playout.Block{}, fmt.Errorf("playout: block endpoint returned %s", resp.Status)
		}
		format, ok := playout.ParseBroadcastFormat(resp.Header.Get(api.PlayoutBroadcastFormatHeader))
		if !ok {
			_ = resp.Body.Close()
			return playout.Block{}, fmt.Errorf("playout: block endpoint returned no valid broadcast format")
		}
		canonical := format.String()
		if broadcast == "" {
			broadcast = canonical
		} else if canonical != broadcast {
			_ = resp.Body.Close()
			return playout.Block{}, fmt.Errorf("playout: block format changed from %s to %s", broadcast, canonical)
		}
		identity, ok := api.ParsePlayoutAiringIdentity(resp.Header)
		if !ok {
			_ = resp.Body.Close()
			return playout.Block{}, fmt.Errorf("playout: block endpoint returned no valid airing identity")
		}
		return playout.Block{Content: resp.Body, Identity: identity, Format: format}, nil
	}
}
