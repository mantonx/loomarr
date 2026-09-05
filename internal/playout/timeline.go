package playout

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/schedule"
)

// "What is airing right now?" — the question the block supervisor asks at each finite EOF (§9.1).
// The wall-clock answer, including its stable identity and start time, is what advances the
// long-lived mux; process timing is never treated as scheduling truth.
//
// Deliberately NOT a new scheduler. `schedule.ComputeDesiredAt` already answers "what does
// this channel air at instant T", honouring curation rules, seasonality, ordering,
// separation and the relaxation ladder — it is what reconcile pushes to Tunarr and what the
// cycle preview shows. Playout asks the SAME function, so what plays cannot drift from what
// the preview promised. Building a second scheduler for playout would be the §10
// shared-assembler mistake in a new place.

// Airing is what a channel should be playing at a given instant.
type Airing struct {
	// StartedAt is the authoritative wall-clock boundary for this airing. Unlike request time it is
	// stable across retries and mid-program tune-ins, so downstream playout can distinguish a real
	// transition from reopening the same item.
	StartedAt time.Time
	// Identity is the stable content identity within StartedAt: a provisioning key for programmes,
	// a clip hash for commercials, and the slot kind for an unplayable scheduled card.
	Identity string
	// ScheduleBlockID identifies the scheduled block that contains this Airing. A filler break keeps
	// one value across its several clip Airings so client transitions and child Process runs join to
	// the same schedule truth.
	ScheduleBlockID string
	// Kind mirrors the scheduler's slot kind, so a caller can distinguish "play this
	// program" from "play a filler clip" from "there is nothing".
	Kind schedule.SlotKind
	// LibraryItemID is the media-server item to stream, for a program slot. Empty for
	// filler (which resolves to a clip) and for the nothing-to-play case.
	LibraryItemID string
	// Key is the provisioning key of what is airing — the identity airing history is
	// recorded under (§5, programming-design §3.1).
	//
	// LibraryItemID would be the wrong key for that: it changes when a file is re-encoded or
	// replaced, and a recency signal must survive an item being swapped underneath it. Empty
	// for filler/flex, which have no provisioned title behind them.
	Key provision.Key
	// Source is a direct ffmpeg input for an item that is NOT a library title — currently a
	// commercial clip resolved to a local file under FILLER_DIR (§10).
	//
	// Separate from LibraryItemID rather than overloading it: the two are resolved by different
	// code (one via the media server's stream endpoint, one via a path join with a containment
	// check) and conflating them would let a filler path reach the library resolver, or a
	// library id reach the filesystem.
	Source string
	// Title is for logs and the guide, never for identity.
	Title string
	// Offset is how far INTO the item playout should start.
	//
	// This is what makes a mid-program tune-in land at the right place rather than
	// restarting the show for whoever joins. A channel is a wall clock, not a playlist
	// that begins when someone watches.
	Offset time.Duration
	// Remaining is how much of the item is left. The child encode is bounded by it, so the
	// process exits at the item boundary and the block supervisor resolves again.
	Remaining time.Duration
}

// Playable reports whether there is something to encode.
//
// Two ways to be playable, because playout has two kinds of input (§9.1, §10):
//
//   - A library PROGRAM, identified by LibraryItemID, which the resolver turns into a media
//     server stream URL.
//   - A commercial CLIP, a local file under FILLER_DIR, which has no library id at all — the
//     resolver returns its path directly.
//
// Source is what distinguishes them: it is set for a resolved filler clip and empty for a
// library program (whose input is derived from LibraryItemID instead). An earlier version
// required LibraryItemID unconditionally, which made every resolved commercial fall through to
// the offline card — the ad was picked correctly and then silently never played.
func (a Airing) Playable() bool {
	if a.Kind == schedule.SlotFiller {
		return a.Source != ""
	}
	return a.Kind == schedule.SlotProgram && a.LibraryItemID != ""
}

// AiringAt walks a computed lineup against the wall clock and returns what is on.
//
// The cycle repeats: a channel with 90 minutes of programming plays it again, which is what
// makes a channel continuous without an infinitely long lineup. `epoch` anchors the cycle so
// the answer is STABLE — two callers asking at the same instant get the same item at the same
// offset, which is required for the shared-encoder model (one encode, N viewers) to be
// coherent.
//
// Slots with unknown duration are skipped rather than guessed at: a pending acquisition has
// DurationMs 0, and treating that as instantaneous would make the cycle drift, while treating
// it as some default would air silence for a made-up length.
func AiringAt(slots []schedule.Slot, epoch, now time.Time) Airing {
	total := cycleDuration(slots)
	if total <= 0 {
		// Nothing with a known duration — the honest answer is "nothing", which the caller
		// renders as the offline card rather than as dead air.
		return Airing{Kind: schedule.SlotFlex}
	}

	// Where in the cycle are we? Modulo keeps this correct for any elapsed time, including
	// a channel that has been running for weeks, and handles a clock that moved backwards
	// (NTP, DST) by wrapping rather than going negative.
	elapsed := now.Sub(epoch)
	if elapsed < 0 {
		elapsed = 0
	}
	into := elapsed % total

	for _, s := range slots {
		d := slotDuration(s)
		if d <= 0 {
			continue // unknown duration — not airable, see above
		}
		if into < d {
			identity := string(s.Key)
			if identity == "" {
				identity = s.LibraryItemID
			}
			if identity == "" {
				identity = string(s.Kind)
			}
			return Airing{
				StartedAt:     now.Add(-into),
				Identity:      identity,
				Kind:          s.Kind,
				LibraryItemID: s.LibraryItemID,
				// Key identifies WHAT aired independently of which library item served it —
				// the identity airing history is recorded under (§5, programming-design §3.1).
				// LibraryItemID alone would not do: a re-encoded or replaced file changes it,
				// and the recency signal must survive that.
				Key:       s.Key,
				Title:     s.Title,
				Offset:    into,
				Remaining: d - into,
			}
		}
		into -= d
	}
	// Unreachable while total > 0 and the loop uses the same durations, but returning flex
	// beats an index panic if those ever diverge.
	return Airing{Kind: schedule.SlotFlex}
}

// cycleDuration is the summed length of everything airable in the lineup.
func cycleDuration(slots []schedule.Slot) time.Duration {
	var total time.Duration
	for _, s := range slots {
		total += slotDuration(s)
	}
	return total
}

// fillerSlotDuration is how long a break gap lasts when the scheduler did not say.
//
// The scheduler emits break gaps as elastic flex for Tunarr to fill from a filler-list —
// Tunarr decides the length. Internal playout has no such negotiator, so a gap with no
// duration needs one, and it must be the SAME every time or the cycle length changes between
// calls and two viewers of one channel see different programs.
const fillerSlotDuration = 30 * time.Second

// slotDuration returns how long a slot occupies the timeline.
func slotDuration(s schedule.Slot) time.Duration {
	if s.DurationMs > 0 {
		return time.Duration(s.DurationMs) * time.Millisecond
	}
	// A filler/flex gap with no stated duration gets the fixed fallback; anything else
	// (notably a pending acquisition) has genuinely unknown length and is not airable.
	if s.Kind == schedule.SlotFiller || s.Kind == schedule.SlotFlex {
		return fillerSlotDuration
	}
	return 0
}

// Broadcast is one programme on the timeline, with the wall-clock times it occupies.
//
// Distinct from Airing, which answers "what is on RIGHT NOW" for the encoder. A guide needs the
// opposite shape: a sequence with absolute start/stop times, spanning hours. Same arithmetic
// underneath, so the guide cannot advertise something different from what plays.
type Broadcast struct {
	Kind schedule.SlotKind
	// Title is the EPISODE's name for a series, or the film's name for a movie.
	Title string
	// SeriesTitle is the show's name ("The Simpsons"); "" for a movie. XMLTV splits the two —
	// `<title>` is the series, `<sub-title>` the episode — and a guide that omits the series
	// lists every episode as an unrelated programme.
	SeriesTitle   string
	LibraryItemID string
	// Key is the provisioning key ("movie:tmdb:603", "series:tvdb:71663"), "" for
	// filler/flex. It is how a block joins back to its acquisition record — the only way a
	// PENDING block can say anything about itself, since it has no library item yet.
	Key     provision.Key
	Season  int
	Episode int
	// Display metadata for the guide, filled in by the caller from the media server (§9.1).
	// Deliberately NOT carried on a schedule.Slot: the schedule is about what plays when, and
	// per-item display data would bloat every lineup row and every policy_json blob for
	// something the media server already holds. The guide fetches it in one bulk call instead.
	Description string
	Genres      []string
	Year        int
	Rating      string
	// RuntimeMs is the item's OWN runtime, distinct from Stop-Start (how long the slot
	// occupies the timeline). They normally agree; where they differ the difference is the
	// point — a 22m episode in a 30m slot is how padding becomes visible.
	RuntimeMs int64
	// Provenance is the one-line answer to "why is this here, and is it real yet?" —
	// "in library · 1080p", "acquiring · 62%", "requested · 41h left". Rendered by the grid's
	// hover card (v2 mock), which is the surface where a pending slot stops being a mystery.
	//
	// A STRING rather than structured fields because it is display text assembled from
	// several sources (provisioning state, deadline, download progress); a client that had
	// to reassemble that sentence would be duplicating a decision the server already made.
	Provenance string
	// Nominal marks a block whose times are a DISPLAY ESTIMATE, not real airtime — today only
	// pending acquisitions, which have no known duration (see NominalPendingDuration). A
	// consumer that treats times as authoritative must skip these: the XMLTV guide does, since
	// advertising an invented start to a media server would be a listing that never happens.
	Nominal bool
	// Start and Stop are absolute wall-clock. Stop is exclusive.
	Start, Stop time.Time
}

// ScheduleBlockID returns the same opaque identity the live Airing path assigns. It is deterministic
// across Guide reads and restarts but reveals none of the content identity used to derive it.
func (b Broadcast) ScheduleBlockID(channelID string) string {
	return ScheduledBlockID(channelID, b.Start, b.Kind, broadcastIdentity(b))
}

// ContentIdentity is the same scheduler-owned identity used by live Airings and ScheduleBlockID.
// Prepared raw delivery carries it into the existing block supervisor without re-deriving it in
// the composition layer.
func (b Broadcast) ContentIdentity() string { return broadcastIdentity(b) }

// ScheduledBlockID is the one schedule-to-diagnostics correlation function. Callers provide the
// scheduler-owned facts they already have; no diagnostic layer performs a lookup to invent them.
func ScheduledBlockID(channelID string, startedAt time.Time, kind schedule.SlotKind, contentIdentity string) string {
	canonical := strings.Join([]string{
		strings.TrimSpace(channelID), startedAt.UTC().Format(time.RFC3339Nano),
		string(kind), strings.TrimSpace(contentIdentity),
	}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return "block_" + hex.EncodeToString(sum[:12])
}

func broadcastIdentity(b Broadcast) string {
	if b.Key != "" {
		return string(b.Key)
	}
	if b.LibraryItemID != "" {
		return b.LibraryItemID
	}
	return string(b.Kind)
}

// Duration is how long this programme occupies the schedule.
func (b Broadcast) Duration() time.Duration { return b.Stop.Sub(b.Start) }

// BroadcastsBetween walks the cycle and returns every programme overlapping [from, to).
//
// The guide's counterpart to AiringAt, and deliberately built on the SAME cycle arithmetic: a
// guide that computed its own timeline would eventually disagree with the encoder, and the
// symptom — "it says Heat but Predator is playing" — is the kind of bug nobody can reproduce on
// demand.
//
// Programmes are CLIPPED to the window, so a film already in progress at `from` reports its real
// start time rather than being truncated or dropped. That matters for an EPG: a media server
// draws the currently-airing programme from its actual start, and a clipped start renders as a
// programme that appears to begin the moment you opened the guide.
//
// Filler and flex are INCLUDED here — the caller decides whether to advertise them. §10 and
// decision #12 say the XMLTV guide must not, but the same walk serves Loomarr's own time-grid
// (V13b), which shows breaks explicitly. Filtering here would make this function useless to one
// of its two callers.
func BroadcastsBetween(slots []schedule.Slot, epoch, from, to time.Time) []Broadcast {
	total := cycleDuration(slots)
	if total <= 0 || !to.After(from) {
		return nil
	}

	// Rewind to the start of the programme covering `from`, so a film already in progress
	// reports its true start. Without this the first entry would begin at `from`.
	elapsed := from.Sub(epoch)
	if elapsed < 0 {
		elapsed = 0
	}
	into := elapsed % total
	cursor := from.Add(-into) // wall-clock time at which the current cycle pass began

	// Advance to the slot containing `from`, keeping cursor aligned to slot boundaries.
	idx := 0
	for _, s := range slots {
		d := slotDuration(s)
		if d <= 0 {
			idx++
			continue
		}
		if into < d {
			break
		}
		into -= d
		cursor = cursor.Add(d)
		idx++
	}

	var out []Broadcast
	// A hard cap on iterations, not on output: a lineup of very short items over a long window
	// is legitimate, but an unbounded loop over a corrupt cycle would hang the request. 10k
	// programmes is far past any real guide (Tunarr's own 14-hour output is 98).
	const maxIterations = 10000
	for i := 0; i < maxIterations && cursor.Before(to); i++ {
		if idx >= len(slots) {
			idx = 0 // the cycle repeats — that is what makes a channel continuous
		}
		s := slots[idx]
		d := slotDuration(s)
		if d <= 0 {
			idx++
			continue // unairable (a pending acquisition has no known duration)
		}
		stop := cursor.Add(d)
		if stop.After(from) {
			out = append(out, Broadcast{
				Kind: s.Kind, Title: s.Title, SeriesTitle: s.SeriesTitle,
				LibraryItemID: s.LibraryItemID, Key: s.Key,
				Season: s.Season, Episode: s.Episode,
				Start: cursor, Stop: stop,
			})
		}
		cursor = stop
		idx++
	}
	return out
}

// NominalPendingDuration is the width a pending acquisition is DRAWN at in Loomarr's own
// time-grid (V13b). It is a display figure and nothing else.
//
// ⚠ It must never reach slotDuration. A pending slot has genuinely unknown length, and giving
// it one inside the shared walk would grow cycleDuration — so every programme after it would
// shift, the encoder would air silence for a made-up length, and the guide would be "right"
// while playout was wrong. That is the shared-source invariant failing in its worst direction.
// The grid needs a width because a zero-width block is invisible; the encoder needs the slot
// skipped. Both are satisfied by keeping this in the projection, never in the timeline.
const NominalPendingDuration = 30 * time.Minute

// BroadcastsWithPending is BroadcastsBetween plus the pending acquisitions, positioned.
//
// The two differ ONLY in what they include, never in where anything sits: airable programmes
// come from the same walk at the same wall-clock times, so the grid and the encoder cannot
// disagree about what is on at 21:00.
//
// A pending slot is drawn immediately BEFORE the airable programme that follows it in the
// lineup, at NominalPendingDuration wide, and is marked Nominal so the UI can render it as a
// placeholder ("coming soon") rather than as a scheduled programme. It overlaps its neighbour
// deliberately: it occupies no real airtime, so there is no gap to place it in, and the honest
// rendering is a marker anchored to where the content WILL land once it arrives (§9 stable
// placement backfills in place — that is the position it will actually take).
func BroadcastsWithPending(slots []schedule.Slot, epoch, from, to time.Time) []Broadcast {
	airable := BroadcastsBetween(slots, epoch, from, to)
	if len(airable) == 0 {
		return airable
	}

	// Which lineup index does each pending slot precede? Walk the lineup once and remember, for
	// every pending slot, the airable slot that follows it (wrapping, since the cycle repeats).
	pendingBefore := map[int][]schedule.Slot{}
	var run []schedule.Slot
	for i := 0; i < len(slots)*2; i++ { // twice around: a pending tail precedes the NEXT pass
		s := slots[i%len(slots)]
		if s.Kind == schedule.SlotPending {
			if i < len(slots) { // only collect on the first pass; the second only resolves the tail
				run = append(run, s)
			}
			continue
		}
		if slotDuration(s) <= 0 {
			continue
		}
		if len(run) > 0 {
			pendingBefore[i%len(slots)] = append(pendingBefore[i%len(slots)], run...)
			run = nil
		}
		if i >= len(slots) {
			break // the tail has been attached to the first airable slot of the next pass
		}
	}
	if len(pendingBefore) == 0 {
		return airable
	}

	// Re-walk the lineup in step with the airable broadcasts so each one knows its lineup index,
	// then emit any pending slots that precede it. BroadcastsBetween emits airable slots in
	// lineup order starting from the slot covering `from`, so tracking the index means finding
	// where that walk began and advancing in lockstep.
	idx := startIndex(slots, epoch, from)
	out := make([]Broadcast, 0, len(airable)+len(pendingBefore))
	for _, b := range airable {
		for _, p := range pendingBefore[idx] {
			out = append(out, Broadcast{
				Kind: schedule.SlotPending, Title: p.Title, SeriesTitle: p.SeriesTitle,
				Key:    p.Key,
				Season: p.Season, Episode: p.Episode,
				Nominal: true,
				// Anchored to where the content will land, extending backwards so the marker
				// sits alongside the programme it precedes rather than displacing it.
				Start: b.Start.Add(-NominalPendingDuration), Stop: b.Start,
			})
		}
		out = append(out, b)
		idx = nextAirable(slots, idx)
	}
	return out
}

// startIndex is the lineup index of the slot airing at `from` — the slot BroadcastsBetween
// begins its walk on. Shared with that walk's own advance loop so the two cannot drift.
func startIndex(slots []schedule.Slot, epoch, from time.Time) int {
	total := cycleDuration(slots)
	if total <= 0 {
		return 0
	}
	elapsed := from.Sub(epoch)
	if elapsed < 0 {
		elapsed = 0
	}
	into := elapsed % total
	for i, s := range slots {
		d := slotDuration(s)
		if d <= 0 {
			continue
		}
		if into < d {
			return i
		}
		into -= d
	}
	return 0
}

// nextAirable is the index of the next slot that occupies real time, wrapping at the end.
func nextAirable(slots []schedule.Slot, idx int) int {
	for i := 1; i <= len(slots); i++ {
		n := (idx + i) % len(slots)
		if slotDuration(slots[n]) > 0 {
			return n
		}
	}
	return idx
}
