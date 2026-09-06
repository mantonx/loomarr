package filler

import (
	"sort"
	"strings"
	"time"
)

// Compilation splitting (§10, V34) — domain types and the PURE segment logic.
// The exec boundary (ffmpeg/ffprobe/whisper-cli) lives in mediatools.go behind
// MediaTools, and the LLM boundary rescue in splitrescue.go; everything here is
// deterministic and unit-tested without touching a binary.
//
// The pipeline (plan §6.4, designed from measurement on six real compilations):
// triage (chapters) → coarse split (blackdetect + silencedetect) → transcript
// rescue for over-long segments → dHash dedup → REVIEW (not optional —
// detection quality is a property of the source, measured 69–100%, so nothing
// enters the catalog unconfirmed).
//
// ⚠ "classify each segment" used to sit before dedup and is GONE (§10 V51g). It
// was one LLM turn per segment — 51 × 7.4s ≈ 377s on a 16m47s reel, against a
// 120s pass — so the rung could never finish and restarted every two minutes.
// **Split cuts; it does not describe.** Each segment is spawned as its own clip
// and reaches `tag` on its own ladder, after `transcribe`, with a real
// transcript instead of the string "… part 7".

const (
	// MinSegmentMs drops slivers: black/silence detection on real compilations
	// produces sub-second "segments" between an advert's own fade-out and fade-in.
	MinSegmentMs int64 = 3000
	// OverlongSegmentMs is the "far longer than a plausible advert" threshold that
	// sends a segment to transcript rescue. Breaks are 30–90s; past two minutes
	// either the A/V pass missed boundaries (the measured 149s three-advert block)
	// or it genuinely is one long advert (the measured 121s infomercial) — only the
	// transcript can say which.
	OverlongSegmentMs int64 = 120_000
)

// segmentFloor is the shortest span that may become a segment: the sliver floor and the CATALOG
// floor (`filler.min_duration`), whichever binds.
//
// ⚠ **`max()`, never replacement.** `filler.min_duration` is settable to `0s` ("accept anything
// with a readable duration"), and a 400ms fade artefact must still be dropped there — that is what
// `MinSegmentMs` is for, and it does not become configurable.
//
// ⚠ **Why the catalog floor belongs at DETECTION and not only in the gate (§10 V34, V54).** It was
// only in `AutoConfirmable`, which returns on the first failing segment — so one sub-floor fragment
// sank the whole reel. A real commercial compilation is *made of* such fragments: measured
// 2026-08-11 on an 82-segment archive.org reel, **39 were under the 10s floor**, shortest 3.1s
// (station IDs, inter-ad bumpers). Auto-split had therefore never fired on any real reel, and the
// V54 grounder below the floor check in that same loop could never be reached. Dropping them here
// costs nothing that was not already lost: `filler.min_duration` is a hard reject at the scan
// boundary, so such a segment would be cut, written, spawned and then thrown away.
type segmentFloor int64 // milliseconds

// newSegmentFloor composes the two floors. A zero or negative `minClip` means the operator turned
// the catalog floor off, which leaves `MinSegmentMs` holding the line.
func newSegmentFloor(minClip time.Duration) segmentFloor {
	return segmentFloor(max(MinSegmentMs, minClip.Milliseconds()))
}

// admits reports whether [startMs,endMs) is long enough to be a segment.
func (f segmentFloor) admits(startMs, endMs int64) bool { return endMs-startMs >= int64(f) }

// ms is the floor in milliseconds, for the error messages that quote the number.
func (f segmentFloor) ms() int64 { return int64(f) }

// boundarySource is which detector(s) put a cut where it is — a bitmask, because the merges below
// are naturally unions and because "black AND silence agreed" is the single most useful fact the
// detection pass produces (§10 V34, the confidence ladder).
type boundarySource uint8

const (
	srcBlack boundarySource = 1 << iota
	srcSilence
	srcChapter
	srcTranscript
	// srcReelEdge is the file's own start or end — declared by the container, not inferred.
	srcReelEdge
	// srcTruncated marks an edge an overlap resolution MOVED. The span is real; this particular
	// end of it was chosen by arithmetic rather than observed.
	srcTruncated
	// srcOperator is a boundary a human typed. It carries no score — see `boundaryScore`.
	srcOperator
)

// Evidence ceilings (§10 V34). Each is the most a boundary with that evidence may score.
//
// ⚠ **Measured, not chosen**, for the three that matter: 9 of 12 fades in the sampled reel were
// corroborated by both detectors (hence 90 for agreement, 65 for the remaining single-detector
// three), and the transcript rescue lands boundaries within ±2–3s (hence 50).
//
// ⚠ Black is deliberately NOT ranked above silence. Nothing measures that, and inventing an order
// would be exactly the unfounded number this ladder exists to avoid. Which one fired survives in
// the evidence token instead.
const (
	confDeclared     = 100 // a chapter marker, or the reel's own edge
	confCorroborated = 90  // black AND silence agreed
	confSingle       = 65  // one detector only
	confTranscript   = 50  // the rescue alone
	confTruncated    = 40  // an overlap moved this edge
	// Segment-level caps. These may only LOWER a score, never raise it.
	confOverlong     = 50
	confUnsplittable = 20
)

// edgeCeiling is the best a single boundary can score, given what found it.
func edgeCeiling(src boundarySource) int {
	switch {
	case src&srcOperator != 0:
		// ⚠ Zero, not 100. A human typing a timecode is not evidence Loomarr collected, and
		// scoring it would let the machine auto-confirm a cut on the strength of the operator's
		// own half-finished edit. An operator-touched segment goes back to them.
		return 0
	case src&srcChapter != 0, src&srcReelEdge != 0:
		return confDeclared
	case src&srcBlack != 0 && src&srcSilence != 0:
		return confCorroborated
	case src&srcTruncated != 0:
		return confTruncated
	case src&srcBlack != 0, src&srcSilence != 0:
		return confSingle
	case src&srcTranscript != 0:
		return confTranscript
	}
	return 0
}

// evidenceToken renders a boundary's evidence for the operator ("silence only", "black + silence").
//
// ⚠ A closed vocabulary on the wire, but typed as a plain string rather than an enum: adding a
// source later must not be a breaking codegen change for every client.
func evidenceToken(src boundarySource) string {
	switch {
	case src&srcOperator != 0:
		return "operator"
	case src&srcChapter != 0:
		return "chapter"
	case src&srcReelEdge != 0:
		return "reel edge"
	case src&srcBlack != 0 && src&srcSilence != 0:
		return "black + silence"
	case src&srcTruncated != 0:
		return "truncated"
	case src&srcBlack != 0:
		return "black only"
	case src&srcSilence != 0:
		return "silence only"
	case src&srcTranscript != 0:
		return "transcript"
	}
	return ""
}

// boundaryScore is how much Loomarr trusts WHERE this segment was cut — 0–100.
//
// ⚠ **It answers a different question from `Clip.Confidence`.** That one asks "do we know what this
// is?"; this asks "did we cut in the right place?". Different evidence, different field, and the
// gate reads this one and never writes that one.
//
// ⚠ **It cannot see a single tag field, and that is structural rather than incidental.** No
// `SuggestedEra`, `Era`, `Looked`, `Category` or `Tags` is in scope here, so a tag fact can never
// move this number — which is what keeps `AutoConfirmable`'s refusals absolute instead of being
// laundered into a score (the objection `autosplit.go` records).
//
// Within a boundary the best evidence wins; across the two the WORST does, because a cut is only as
// trustworthy as its weaker end. Segment facts then cap it.
func boundaryScore(s SplitSegment) int {
	score := min(edgeCeiling(s.startSrc), edgeCeiling(s.endSrc))
	// ⚠ The rescue's confirmation LIFTS this cap rather than adding points — the measured 121s
	// infomercial is genuinely one advert, and capping it for being long would refuse the very
	// case the rescue exists to settle.
	if s.overlong() && !s.rescueConfirmedWhole {
		score = min(score, confOverlong)
	}
	if s.Unsplittable {
		score = min(score, confUnsplittable)
	}
	return score
}

// scoreBoundaries stamps the confidence and its evidence onto every segment, in place.
func scoreBoundaries(segs []SplitSegment) {
	for i := range segs {
		segs[i].BoundaryConfidence = boundaryScore(segs[i])
		segs[i].StartEvidence = evidenceToken(segs[i].startSrc)
		segs[i].EndEvidence = evidenceToken(segs[i].endSrc)
	}
}

// dropTally is what the detection floor discarded, so `Propose` can say so on the ladder.
//
// ⚠ It exists because dropping is not free: the discarded span is NOT merged into a neighbour, so
// the recording goes with it (measured: 39 fragments ≈ 3 minutes on one 82-segment reel). A silent
// drop would leave an operator comparing a 20-minute source against 17 minutes of cuts with
// nothing to read.
type dropTally struct {
	Count int
	Ms    int64
}

func (d *dropTally) add(startMs, endMs int64) {
	d.Count++
	d.Ms += endMs - startMs
}

// SplitSegment is one proposed clip inside a compilation (§10 V34). The detector
// authors it; the reviewer edits it; only confirm writes it to the catalog.
type SplitSegment struct {
	Index   int   `json:"index"`
	StartMs int64 `json:"startMs"`
	EndMs   int64 `json:"endMs"`
	// Name is the proposed clip name (from the LLM's product label, or
	// "<compilation> part N"). It becomes the clip's filename on confirm.
	Name string `json:"name"`
	// Era/Audience/Tags come from the SAME Classify the tag job uses, over the
	// segment's transcript. Era is grounded (year in the text) or zero — an
	// ungrounded guess is carried ONLY as SuggestedEra (§10 era rule).
	Era          int      `json:"era,omitempty"`
	SuggestedEra int      `json:"suggestedEra,omitempty"`
	Audience     Audience `json:"audience,omitempty"`
	// Tags is the grounded taxonomy leaf set (§10 V45a); Category is its DERIVED product-leaf shadow,
	// carried so the review and the confirmed clip both have the cheap read-path value. Both come from
	// the same Classify the tag job uses.
	Tags     []string `json:"tags,omitempty"`
	Category string   `json:"category,omitempty"`
	// BoundaryConfidence is how much Loomarr trusts WHERE this was cut, 0–100 (§10 V34 ladder).
	// `0` means never scored — every proposal detected before V54 deserialises that way, and the
	// review surface must treat "unscored" as "no opinion", never as "doubtful".
	//
	// ⚠ **Not `Clip.Confidence`.** That is a TAG confidence ("do we know what this is?"); this is a
	// BOUNDARY confidence ("did we cut in the right place?"). Confirm must never copy one into the
	// other — doing so would corrupt the auto-file policy, which reads the tag one.
	BoundaryConfidence int `json:"boundaryConfidence,omitempty"`
	// StartEvidence/EndEvidence say WHY, in the operator's words ("black + silence", "silence only").
	//
	// ⚠ **Two fields because a segment has two boundaries**, and they routinely disagree — a cut
	// can start on a chapter marker and end on a lone silence. One field would have to pick a
	// winner, and the number already picks the worse of the two; the evidence is what tells the
	// operator WHICH end to look at, so collapsing it throws away the actionable half.
	StartEvidence string `json:"startEvidence,omitempty"`
	EndEvidence   string `json:"endEvidence,omitempty"`
	// HoldReason is why this segment was kept back when its reel was partly confirmed (§10 V54) —
	// the same vocabulary `AutoSplitReject` uses, so the review says "a segment's era is a guess"
	// rather than leaving the operator to work out why 5 of 52 are still here.
	HoldReason string `json:"holdReason,omitempty"`
	// startSrc/endSrc are the bitmask the two fields above are rendered from. Unexported: the wire
	// carries the rendered tokens and the score, never the bitmask, so adding a source is not a
	// breaking API change.
	startSrc boundarySource
	endSrc   boundarySource
	// rescueConfirmedWhole means the transcript rescue looked at this over-long span and reported
	// exactly ONE advert. It lifts the over-long cap — see `boundaryScore`.
	rescueConfirmedWhole bool
	// Looked records that the split-time grounder ALREADY examined this segment's frames —
	// whether or not it came back with anything (§10 V54).
	//
	// ⚠ **Inference cannot replace this flag.** `Category != "" || Era > 0` conflates *never
	// looked at* with *looked at and grounded nothing*, and treating those alike is exactly what
	// makes a resumable budget never converge: the ungroundable segments would be retried every
	// pass, forever, and the reel would never reach a verdict.
	//
	// ⚠ It is set even when the frame extraction or the JSON parse FAILED. A transient ffmpeg
	// error therefore retires that segment permanently for this proposal. That is deliberate — it
	// is what bounds the loop — and the remedy is a re-detect, which replaces the proposal. The
	// provider-error path does NOT set it, because a failing backend fails for every segment and
	// should be retried next pass rather than poisoning the reel.
	Looked bool `json:"looked,omitempty"`
	// DupOf is the path of an existing catalog clip this segment duplicates
	// (dHash, measured 25× separation). Propose records the detection fact; the automated split
	// stage then discards it before classification because it cannot add a new catalog clip.
	DupOf string `json:"dupOf,omitempty"`
	// Unsplittable means the segment is over-long AND the rescue could not run or
	// found nothing (no whisper, whisper/LLM failure). The review must say so —
	// the alternative is guessing, which is exactly what the era rule forbids in
	// tag form.
	Unsplittable bool `json:"unsplittable,omitempty"`
	// Transcript is the segment's whisper text, when the rescue ran. Carried on
	// the proposal so the reviewer can SEE why a boundary was proposed; not
	// persisted to the catalog on confirm.
	Transcript string `json:"transcript,omitempty"`
}

// SplitDetectionProgress is the private durable checkpoint for coarse boundary detection. It is
// stored with a proposal but never exposed through the review interface: until this becomes nil,
// the proposal is detector work rather than a cut list an operator can judge.
type SplitDetectionProgress struct {
	ScannedThroughMs int64      `json:"scannedThroughMs"`
	Black            []Interval `json:"black,omitempty"`
	Silence          []Interval `json:"silence,omitempty"`
	// Chapters says CoarseSegments came from container-authored chapter boundaries. It is private
	// checkpoint provenance, not review data, and lets a resume restore scoring evidence even when
	// a source supplied one untitled chapter (otherwise indistinguishable from a whole-reel
	// boundary fallback after JSON removed the private source bitmask).
	Chapters bool `json:"chapters,omitempty"`
	// CoarseSegments is set once chapter/boundary triage is complete. Persisting it before
	// transcript rescue means a timeout in the next phase never repeats the timeline scan.
	CoarseSegments []SplitSegment `json:"coarseSegments,omitempty"`
}

// SplitProposal is the persisted, operator-reviewable result of detecting cuts
// in a compilation clip (§10 V34). It is NOT a clip: nothing here is visible to
// pod matching, and the only way segments become clips is Confirm.
type SplitProposal struct {
	ID string `json:"id"`
	// Dropped is what the detection floor discarded on the pass that PRODUCED this proposal.
	//
	// ⚠ **In-memory only** (`json:"-"`): the row persists `segments_json` and nothing else, so a
	// proposal read back from the store reports a zero tally. That is honest rather than lossy —
	// the number describes a detection pass, and re-reading a stored proposal did not run one.
	// Persisting it would mean a schema change to carry a figure only the pass that produced it
	// can vouch for.
	Dropped dropTally `json:"-"`
	// ClipHash is the compilation's IDENTITY — its content hash (§10 V38c), the value
	// `GetClip` is keyed on.
	//
	// ⚠ **This field was `ClipPath` and held `clip.Path`, and that mismatch broke Confirm
	// outright.** `Propose` wrote the shard path (`a3/f9/<hash>.mp4`); `Confirm` handed the same
	// string to a HASH-keyed `GetClip`, which never matched — so every confirm returned
	// "compilation … no longer in the catalog" for a clip sitting in the catalog. An operator
	// could open a 41-segment reel, edit it, and never commit it. It survived because the
	// splitter's test store keys its map on `Path`, so the fixture answered a question
	// production's store does not (the same collapsed-key class `conformance_filler.go` records).
	//
	// ⚠ The CATALOG PATH still comes from the clip row rather than being reconstructed from this
	// identity. V66 additionally persists `Source` below: that is the exact evidence/playback asset
	// reviewed by detection, not a second competing catalog location.
	ClipHash  string         `json:"clipHash"`
	CreatedAt time.Time      `json:"createdAt"`
	Segments  []SplitSegment `json:"segments"`
	// Source binds detection and confirmation to one exact derivative. It is internal durable
	// state rather than review UI; zero is a pre-V66 proposal that resolves through legacy rules.
	Source SplitSourceAsset `json:"-"`
	// Spawned remembers children already produced by partial auto-confirm. It is private durable
	// state, not review UI: final confirmation needs the whole new generation so it can retire
	// superseded children without also retiring cuts produced on an earlier pass.
	Spawned []string `json:"-"`
	// Detection is persisted inside the store document, not served in OpenAPI. nil means the
	// proposal is complete and reviewable; non-nil means the pipeline must resume detection.
	Detection *SplitDetectionProgress `json:"-"`
}

// Ready reports whether detection has produced an operator-reviewable cut list.
func (p SplitProposal) Ready() bool { return p.Detection == nil }

// segmentsFromBoundaries builds segments by cutting the timeline at every
// detected gap (a black or silence interval), dropping slivers under
// MinSegmentMs. `gaps` may arrive unsorted and overlapping (blackdetect and
// silencedetect fire on the same boundary); they are merged first so a fade that
// is BOTH black and silent produces one cut, not two slivers.
//
// Boundaries are taken at the gap's MIDPOINT: blackdetect reports the whole
// fade interval, and cutting at its start would shave the advert's tail frame.
func segmentsFromBoundaries(durationMs int64, gaps []detectedGap, floor segmentFloor) ([]SplitSegment, dropTally) {
	cuts := boundaryCuts(gaps, durationMs)
	var out []SplitSegment
	var dropped dropTally
	start := int64(0)
	// ⚠ The first segment starts at the REEL'S OWN EDGE, which is declared by the container rather
	// than detected — the strongest evidence there is. Same for the last segment's end below.
	startSrc := srcReelEdge
	for _, cut := range cuts {
		if seg, ok := makeSegment(len(out), start, cut.Ms, floor, startSrc, cut.Src); ok {
			out = append(out, seg)
		} else {
			dropped.add(start, cut.Ms)
		}
		startSrc = cut.Src
		// ⚠ `start` advances even when the span was DROPPED, so the dropped time is discarded
		// rather than absorbed into either neighbour. Deliberate — merging a stinger into the
		// advert beside it would put someone else's audio in that clip's tail — but it means a
		// drop costs real recording, which is why the tally is reported (§10 V45).
		start = cut.Ms
	}
	if seg, ok := makeSegment(len(out), start, durationMs, floor, startSrc, srcReelEdge); ok {
		out = append(out, seg)
	} else {
		dropped.add(start, durationMs)
	}
	return out, dropped
}

// detectedGap is a gap that remembers WHICH detector found it.
//
// ⚠ It exists because that fact used to die one line before it was needed: `triage` did
// `append(blacks, silences...)`, and from there nothing could tell a boundary both detectors agreed
// on from one only a single detector saw. Corroboration is the strongest evidence a cut has — 9 of
// 12 fades in the measured reel were confirmed by both — so throwing it away is what left the
// confidence ladder (§10 V34) with nothing to read.
//
// ⚠ A filler-local type rather than a field on `Interval`: that alias belongs to `mediatools` and
// is used in ~110 places, none of which care who found a gap.
type detectedGap struct {
	Interval
	Src boundarySource
}

// boundaryCut is a cut position and the evidence that put it there.
type boundaryCut struct {
	Ms  int64
	Src boundarySource
}

// boundaryCuts merges overlapping/adjacent gaps and returns their midpoints as
// ordered cut positions, clamped inside (0, durationMs), each carrying the union
// of the detectors that produced it.
func boundaryCuts(gaps []detectedGap, durationMs int64) []boundaryCut {
	if len(gaps) == 0 {
		return nil
	}
	sorted := append([]detectedGap(nil), gaps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].StartMs < sorted[j].StartMs })
	var cuts []boundaryCut
	cur := sorted[0]
	for _, g := range sorted[1:] {
		if g.StartMs <= cur.EndMs {
			cur.EndMs = max(cur.EndMs, g.EndMs)
			// ⚠ **THE line that records agreement.** A fade that is both black and silent merges
			// into one cut here; unioning the sources is what lets the ladder tell that cut apart
			// from one a single detector saw.
			cur.Src |= g.Src
			continue
		}
		cuts = append(cuts, boundaryCut{Ms: midpoint(cur.Interval, durationMs), Src: cur.Src})
		cur = g
	}
	cuts = append(cuts, boundaryCut{Ms: midpoint(cur.Interval, durationMs), Src: cur.Src})
	// Midpoints of adjacent gaps can collide after clamping; dedupe + order.
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].Ms < cuts[j].Ms })
	out := cuts[:0]
	for i, c := range cuts {
		// ⚠ A collision is AGREEMENT too, and this is the non-obvious half. Two separate gaps whose
		// midpoints clamp onto the same millisecond used to have the second one silently dropped —
		// which is right for the position and wrong for the evidence, because it discarded a second
		// detector confirming that very spot.
		if i > 0 && c.Ms == cuts[i-1].Ms {
			out[len(out)-1].Src |= c.Src
			continue
		}
		out = append(out, c)
	}
	return out
}

func midpoint(g Interval, durationMs int64) int64 {
	m := (g.StartMs + g.EndMs) / 2
	if m < 1 {
		m = 1
	}
	if m > durationMs-1 {
		m = durationMs - 1
	}
	return m
}

func makeSegment(index int, start, end int64, floor segmentFloor, startSrc, endSrc boundarySource) (SplitSegment, bool) {
	if !floor.admits(start, end) {
		return SplitSegment{}, false
	}
	return SplitSegment{Index: index, StartMs: start, EndMs: end, startSrc: startSrc, endSrc: endSrc}, true
}

// segmentsFromChapters turns embedded chapters into segments (the triage step —
// a pre-chaptered source splits for free). Chapters that abut produce no gaps;
// slivers are dropped as usual. Chapters carry their own titles, which beat any
// generated name.
func segmentsFromChapters(chapters []Chapter, floor segmentFloor) ([]SplitSegment, dropTally) {
	var out []SplitSegment
	var dropped dropTally
	for _, ch := range chapters {
		// ⚠ Stamped HERE rather than by the caller: a chapter's boundaries are DECLARED by the
		// container, and that is a fact about where they came from, not a decision `triage` makes.
		seg, ok := makeSegment(len(out), ch.StartMs, ch.EndMs, floor, srcChapter, srcChapter)
		if !ok {
			dropped.add(ch.StartMs, ch.EndMs)
			continue
		}
		seg.Name = strings.TrimSpace(ch.Title)
		out = append(out, seg)
	}
	return out, dropped
}

// overlong reports whether a segment is far longer than a plausible advert and
// therefore needs the transcript rescue (§10 — boundaries that exist only in
// language).
func (s SplitSegment) overlong() bool { return s.EndMs-s.StartMs > OverlongSegmentMs }

// TranscriptText renders a transcript as timestamped lines for the LLM prompt
// ("[01:23] …"). mm:ss is what the model reads most reliably, and it is what the
// rescue asks it to answer in.
func TranscriptText(segs []TranscriptSegment) string {
	var b strings.Builder
	for _, s := range segs {
		text := strings.Join(strings.Fields(s.Text), " ")
		if text == "" {
			continue
		}
		b.WriteString("[" + formatMS(s.StartMs) + "] " + text + "\n")
	}
	return b.String()
}

// formatMS renders milliseconds as mm:ss for prompts and display.
func formatMS(ms int64) string {
	s := ms / 1000
	return pad2(s/60) + ":" + pad2(s%60)
}

func pad2(n int64) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
