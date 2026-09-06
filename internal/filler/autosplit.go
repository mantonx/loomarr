package filler

import (
	"fmt"
	"time"
)

// Auto-confirm for split proposals (§10 V43) — the gate behind
// `filler.autosplit.enabled`.
//
// ⚠ **RETIRED IN V54: the whole reel no longer qualifies or fails together.** The paragraph below
// is the rule as it stood, kept because its reasoning is still half-right and the half that
// changed is worth naming. It assumed there was no per-segment evidence, so confirming a subset
// would have been an ARBITRARY split of one decision. Boundary confidence (§10 V34) supplies that
// evidence, so holding five cuts back out of 52 is routing rather than splitting. What the old
// rule cost was total: one doubtful segment sent all 52 back, and ~50 reels sat parked with none
// ever auto-confirmed.
//
// ⚠ **The whole reel qualifies or none of it does.** Confirming the good segments and surfacing
// the rest would split one reel's decision across two places and hand the operator fragments to
// judge without the picture. It also matches how the 69% case actually fails: a badly-split reel
// is not uniformly slightly-wrong, it has obvious tells — one 6-minute block the detector could
// not resolve, sitting beside perfectly good 30-second cuts. One doubtful segment is evidence
// about the REEL, not just about itself.

// AutoSplitPolicy is what the gate reads, resolved per call so a settings change takes effect on
// the next run rather than at the next restart (the same hot-apply contract `AutoFilePolicy` uses).
type AutoSplitPolicy struct {
	// Enabled reports whether auto-confirm is on at all.
	//
	// ⚠ Must be backed by a FAIL-CLOSED read (`boolv`, not `boolOn`), like AutoFilePolicy. When
	// the settings service cannot answer, the safe answer is "don't cut" — failing open would
	// consume compilations unattended precisely when the install is degraded.
	Enabled func() bool
	// MinConfidence is the score every segment must reach. Bounded 50-95 by the registry.
	MinConfidence func() int
	// MaxDuration is the longest a segment may be and still be advert-shaped.
	MaxDuration func() time.Duration
}

// AutoSplitReject names why a proposal was not auto-confirmed. Returned rather than logged so
// the caller can report it — an unattended decision that leaves no trace is not one an appliance
// gets to make (§10), and "it just didn't" is unactionable for an operator tuning the threshold.
type AutoSplitReject string

const (
	AutoSplitOK AutoSplitReject = ""
	// RejectDisabled is the ordinary state: the operator has not opted in.
	RejectDisabled   AutoSplitReject = "auto-split is off"
	RejectNoSegments AutoSplitReject = "the proposal has no segments"
	// RejectUnsplittable is the signal this gate exists for. The detector already knows it
	// failed on this span; auto-confirming around that admission is the 69% failure.
	RejectUnsplittable AutoSplitReject = "a segment could not be split and needs a human"
	RejectTooLong      AutoSplitReject = "a segment is longer than one advert"
	RejectTooShort     AutoSplitReject = "a segment is shorter than the clip floor"
	// RejectDuplicate is defense in depth for callers that invoke the gate without the split
	// stage's deterministic curation. The automated path discards the match before this point.
	RejectDuplicate AutoSplitReject = "a segment already exists in the catalog"
	// RejectUngrounded is the grounding rule, reused rather than re-derived.
	RejectUngrounded AutoSplitReject = "a segment's era is a guess rather than something Loomarr read"
	RejectUntagged   AutoSplitReject = "a segment could not be classified"
	// RejectBoundaryUncertain is the CONFIDENCE outcome, and the only one of these that is a
	// threshold rather than a refusal (§10 V34).
	//
	// ⚠ It is checked LAST, over segments that already passed every refusal above. Confidence can
	// hold a qualifying cut back; it can never admit a refused one. Keeping the two kinds of
	// "no" separate — and in that order — is what stops a score standing in for a refusal.
	RejectBoundaryUncertain AutoSplitReject = "Loomarr is not sure this was cut in the right place"
)

// minConfidence resolves the operator's threshold, defaulting to the registry's 85 when the
// policy carries no reader. Clamped to the same ceiling the tagger uses: `filler.Score` caps an
// ungrounded era strictly below it, and a value above would silently break that guarantee.
func minConfidence(pol *AutoSplitPolicy) int {
	if pol == nil || pol.MinConfidence == nil {
		return 85
	}
	n := pol.MinConfidence()
	if n <= 0 || n > MaxAutoFileConfidence {
		return MaxAutoFileConfidence
	}
	return n
}

// ⚠ **This comment used to say "there is no per-segment confidence NUMBER to read, and this is
// better than one", and it was RIGHT about the thing it was defending.** Its argument: a grounded
// era lands in `Era`, an ungrounded guess lands in `SuggestedEra`, and inventing a score to compare
// against would launder that refusal into a number — the exact move the era rule exists to prevent.
//
// V54 adds a number, and the argument survives intact because the number answers a DIFFERENT
// question. `BoundaryConfidence` is about WHERE a cut was made, not what the clip is:
// `boundaryScore` cannot see `SuggestedEra`, `Era`, `Looked`, `Category` or `Tags`, so no tag fact
// can move it. And the ordering below enforces the rest — every refusal is checked first and holds
// at any score. A guessed era still disqualifies at 100. Nothing is laundered; a second, narrower
// question was added beside the first.
//
// ⚠ `MinConfidence` now gates two things, and both are honest uses of one dial: the ERA at the
// ceiling (95 requires a grounded year, and lowering it does NOT admit guesses — `SuggestedEra`
// disqualifies at any setting), and the BOUNDARY score. At its default of 85 the second means both
// of a segment's edges must be corroborated.

// segmentVerdict is why ONE segment may or may not be cut unattended.
//
// ⚠ **Refusals are absolute and are checked FIRST; the confidence threshold is checked LAST.** A
// refused segment is refused at any score — that ordering is what keeps `boundaryScore` from
// laundering a refusal into a number, which is the objection recorded above. Confidence can only
// hold back a segment that already passed everything.
func segmentVerdict(s SplitSegment, pol *AutoSplitPolicy, minClipDuration, maxDur time.Duration) AutoSplitReject {
	if s.Unsplittable {
		return RejectUnsplittable
	}
	if why := segmentContentVerdict(s, pol, minClipDuration, maxDur); why != AutoSplitOK {
		return why
	}
	// ⚠ **LAST, and only over segments that already passed every refusal above** (§10 V34).
	// At the default 85 this means BOTH of the segment's boundaries must be corroborated —
	// chapter, reel edge, or black+silence agreed (90) clears it; a single-detector edge (65)
	// does not. That conservatism is doing real work: a confirmed segment is airable, with no
	// second human gate after this one.
	if s.BoundaryConfidence < minConfidence(pol) {
		return RejectBoundaryUncertain
	}
	return AutoSplitOK
}

// segmentContentVerdict owns refusals that remain relevant after an independently certified
// complete-timeline decision. It deliberately knows nothing about detector success or confidence.
func segmentContentVerdict(s SplitSegment, pol *AutoSplitPolicy, minClipDuration, maxDur time.Duration) AutoSplitReject {
	if s.DupOf != "" {
		return RejectDuplicate
	}
	span := time.Duration(s.EndMs-s.StartMs) * time.Millisecond
	if span > maxDur {
		return RejectTooLong
	}
	// ⚠ The floor is the SAME `filler.min_duration` the scan boundary rejects on (V40), not
	// a second number. A segment this gate would confirm and the scan would then reject is
	// a clip cut out of a compilation and immediately thrown away — work done to produce
	// nothing, and a source file consumed for it.
	if minClipDuration > 0 && span < minClipDuration {
		return RejectTooShort
	}
	// The grounding outcome, not a score. An era Loomarr GUESSED is exactly the case a human
	// should see, and it is already marked.
	if s.SuggestedEra > 0 {
		return RejectUngrounded
	}
	// ⚠ Audience and category are what pod assembly MATCHES on (§10). A segment with
	// neither is a clip that can only ever be a fallback-ladder pick, which is not something
	// to create unattended out of a file the operator still had.
	if s.Audience == "" && s.Category == "" {
		return RejectUntagged
	}
	// ⚠ At the ceiling (95) a grounded ERA is required too, not merely tags. That is what
	// makes the threshold do real work rather than sit decorative: an operator who wants
	// only unambiguous reels cut sets it to the top and gets segments Loomarr READ a year
	// for. Below the ceiling, tags alone suffice — but a guessed era still disqualifies
	// above, because `SuggestedEra` is checked at every setting.
	if minConfidence(pol) >= MaxAutoFileConfidence && s.Era == 0 {
		return RejectUngrounded
	}
	return AutoSplitOK
}

// AutoConfirmable partitions a proposal into the segments that may be cut unattended and the ones
// that stay behind for a human (§10 V54).
//
// ⚠ **This was ALL-OR-NOTHING until V54** — one doubtful segment sent the whole reel to review,
// which meant the operator's work never shrank and ~50 reels sat parked with none ever confirmed.
// The old rule was right that splitting a decision ARBITRARILY is worse than making it once; what
// changed is that there is now per-segment evidence, so keeping five back out of 52 is routing by
// evidence rather than an arbitrary split.
//
// A whole-proposal refusal (disabled, no segments) returns in `Reject` with nothing confirmable.
func AutoConfirmable(p SplitProposal, pol *AutoSplitPolicy, minClipDuration time.Duration) SplitPartition {
	if pol == nil || pol.Enabled == nil || !pol.Enabled() {
		return SplitPartition{Reject: RejectDisabled, Hold: p.Segments}
	}
	if len(p.Segments) == 0 {
		return SplitPartition{Reject: RejectNoSegments}
	}

	maxDur := 120 * time.Second
	if pol.MaxDuration != nil {
		if d := pol.MaxDuration(); d > 0 {
			maxDur = d
		}
	}

	var out SplitPartition
	for _, s := range p.Segments {
		if why := segmentVerdict(s, pol, minClipDuration, maxDur); why != AutoSplitOK {
			s.HoldReason = string(why)
			out.Hold = append(out.Hold, s)
			continue
		}
		out.Confirm = append(out.Confirm, s)
	}
	return out
}

// SplitPartition is what the gate decided, segment by segment.
type SplitPartition struct {
	// Confirm may be cut into held child work items unattended.
	Confirm []SplitSegment
	// Hold stays in the proposal, each carrying the reason it was kept back.
	Hold []SplitSegment
	// Discard is complete-plan material intentionally omitted with retained structure authority.
	// It is neither publishable nor an unresolved hold.
	Discard []SplitSegment
	// Reject is set only for a WHOLE-proposal refusal (auto-split switched off, nothing detected).
	// A per-segment refusal is on the segment, not here.
	Reject AutoSplitReject
}

// String is the operator-facing text of a reject reason.
func (r AutoSplitReject) String() string { return string(r) }

// pluralizeCuts renders "1 cut" / "5 cuts" for the ladder note.
func pluralizeCuts(n int) string {
	if n == 1 {
		return "1 cut"
	}
	return fmt.Sprintf("%d cuts", n)
}

// Verdict reduces a partition to ONE reel-level answer: `AutoSplitOK` when every segment cleared,
// otherwise the reason the MOST held-back segments give.
//
// ⚠ A summary for a ladder note or a log line, never the decision itself. Reading this instead of
// `Confirm`/`Hold` reintroduces exactly the all-or-nothing behaviour V54 removed — one doubtful
// segment would once again speak for 51 good ones.
//
// ⚠ **This returned `Hold[0].HoldReason` — the FIRST held segment's reason — and on a real reel it
// reported the minority.** Measured 2026-08-13: 36 of 37 segments were refused as untagged and 1
// as boundary-uncertain, but the tagged-yet-doubtful one sorted first, so the ladder told the
// operator *"Loomarr is not sure this was cut in the right place"*. Acting on that means lowering
// the confidence threshold, which would have changed nothing at all — the 36 die two checks
// earlier. A summary that can name the reason for one segment out of thirty-seven is worse than no
// summary, because it is actionable and wrong.
func (sp SplitPartition) Verdict() AutoSplitReject {
	if sp.Reject != AutoSplitOK {
		return sp.Reject
	}
	reason, _, _ := sp.HoldSummary()
	return reason
}

// HoldSummary reports the reason the most held-back segments share, how many share it, and how
// many are held in total — so a caller can say "36 of 37" rather than implying unanimity.
//
// Ties break on the reason text, so one reel always produces one note.
func (sp SplitPartition) HoldSummary() (reason AutoSplitReject, shared, total int) {
	if len(sp.Hold) == 0 {
		return AutoSplitOK, 0, 0
	}
	counts := map[AutoSplitReject]int{}
	for _, seg := range sp.Hold {
		counts[AutoSplitReject(seg.HoldReason)]++
	}
	for r, n := range counts {
		if n > shared || (n == shared && r < reason) {
			reason, shared = r, n
		}
	}
	return reason, shared, len(sp.Hold)
}

// holdNote renders the ladder note for a reel where nothing cleared the gate.
//
// ⚠ It states the SHARE, not just the reason. "a segment could not be classified" reads as a
// property of the reel; "36 of 37 cuts: …" tells the operator both what to fix and how much of the
// reel it accounts for — and, when the reasons are mixed, that fixing this one will not release
// everything.
func holdNote(sp SplitPartition) string {
	reason, shared, total := sp.HoldSummary()
	switch {
	case total == 0:
		return string(AutoSplitOK)
	case shared == total:
		return fmt.Sprintf("%s: %s", pluralizeCuts(total), reason)
	default:
		return fmt.Sprintf("%d of %s: %s", shared, pluralizeCuts(total), reason)
	}
}
