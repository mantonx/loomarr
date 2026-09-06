package filler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/taxonomy"
)

// The SPLIT stage (§10 V51b): a compilation is cut into the adverts it holds.
//
// V43's `SplitRunner` logic, minus the sweep. Detection is `Splitter.Propose` and the gate is
// `AutoConfirmable`, both unchanged — what moves is who decides a clip is a candidate (the probe
// rung, which now sets `is_composite`) and what happens to the pieces (they are SPAWNED, so each
// segment runs the whole ladder for itself instead of waiting for six cron jobs to notice it).
//
// ⚠ **`filler.autosplit.enabled` now defaults ON** (maintainer decision, V51b). The gate it turns
// on is strict: `SuggestedEra > 0` disqualifies at every threshold and `MaxAutoFileConfidence`
// remains the ceiling. Segments it holds back keep their proposal and appear in Incoming with
// `AutoSplitReject`'s reason recorded, so "why did this not auto-cut?" is answerable without
// reading the log.
//
// ⚠ **The gate is PER SEGMENT since V54 — "the whole reel qualifies or none of it does" is
// retired.** The confident cuts are filed and the doubtful ones stay behind in a shrunken
// proposal, so a reel of 52 becomes 47 clips and 5 cuts to review. The old rule made the
// operator's work never shrink: one doubtful segment sent all 52 back, and ~50 reels sat parked
// with none ever confirmed.

// SplitStageStore is the slice of the store the split stage reads.
//
// ⚠ One read of the pending queue rather than a per-clip existence check — the queue is a review
// backlog a human is expected to clear, so it is small by construction, and re-detecting over a
// proposal an operator is halfway through editing is the thing being prevented.
type SplitStageStore interface {
	ListSplitProposals(ctx context.Context) ([]SplitProposal, error)
}

// SplitStage proposes (and where allowed, confirms) the cuts inside a compilation.
type SplitStage struct {
	splitter *Splitter
	store    SplitStageStore
	// autoConfirm is the opt-in gate. nil ⇒ propose only, every reel waits for a human.
	autoConfirm *AutoSplitPolicy
	// minClipDuration is `filler.min_duration`, the SAME floor the scan boundary rejects on.
	minClipDuration func() time.Duration
	// vision grounds proposed segments from their own frames so the gate has data (§10 V54).
	// nil ⇒ propose only.
	vision *SegmentVision
	// structureShadow durably records the compatibility comparison beside the complete-plan gate.
	// It is diagnostic only and never authorizes child materialization.
	structureShadow StructureSplitShadowObserver
	// structureDecisioner independently assesses the complete retained source once. nil leaves
	// detector structure in place and makes no provider request.
	structureDecisioner CompleteTimelineStructureDecisioner
	// structureMaterialization verifies the only gate that can materialize held children. A nil
	// policy holds the proposal; compatibility is never application authority.
	structureMaterialization *StructureMaterializationPolicy
	// log reports what a grounding pass actually did (§10 V54b). nil is tolerated everywhere.
	log *slog.Logger
}

// NewSplitStage builds the stage. Without `WithAutoConfirm` it PROPOSES ONLY, which is the safe
// default: proposing writes no clips and consumes no file, so the waiting is removed without the
// deciding.
func NewSplitStage(splitter *Splitter, store SplitStageStore) *SplitStage {
	return &SplitStage{splitter: splitter, store: store}
}

// WithLogger attaches the logger the grounding pass reports through (§10 V54b).
//
// ⚠ **Grounding had SEVEN distinct silent outcomes and no way to tell them apart.** Not wired,
// zero budget, unreadable taxonomy, no keyframes, a provider that refused, an unparseable answer,
// and — the one that actually bit — a provider that answered every time and yielded nothing
// usable. All seven produced the same observable: segments left ungrounded, the gate refusing with
// "a segment could not be classified", and not one line anywhere saying which.
func (s *SplitStage) WithLogger(log *slog.Logger) *SplitStage {
	s.log = log
	return s
}

// WithAutoConfirm attaches the auto-confirm policy and the clip floor it must respect.
func (s *SplitStage) WithAutoConfirm(pol AutoSplitPolicy, minClipDuration func() time.Duration) *SplitStage {
	s.autoConfirm = &pol
	s.minClipDuration = minClipDuration
	return s
}

// WithSegmentVision attaches the split-time grounder that answers the auto-confirm gate (§10 V54).
//
// ⚠ **Without this, `filler.autosplit.enabled` is default-ON and cannot fire.** `AutoConfirmable`
// refuses any segment with neither `Audience` nor `Category`; the text `classify` that used to fill
// those fields was removed in V51g (it cost 3× the pass budget AND classified nothing but a
// generated name), and nothing replaced it — so the gate has had no data source at all. Measured on
// the maintainer's catalog: 45 compilations parked at `split`, none ever auto-confirmed.
//
// ⚠ **It grounds from PIXELS, not from the segment's name, and that is the whole difference.** The
// removed classifier read `"… part 7"` and grounded nothing, which is why speeding it up would not
// have helped. Frames from the segment's own span exist at split time and carry a real signal — the
// same signal the `vision` rung reads, through the same prompt and the same `groundVisionTags`, so
// the gate is fed by the vocabulary that judges it.
//
// nil ⇒ propose only, exactly as a nil `autoConfirm` does. A missing grounder must never mean
// "confirm without one".
func (s *SplitStage) WithSegmentVision(v *SegmentVision) *SplitStage {
	s.vision = v
	return s
}

// WithStructureShadow attaches the durable dual-evaluation module. A recording failure is an
// error rather than a log-only omission: unattended materialization must not erase the disagreement
// evidence by consuming its proposal.
func (s *SplitStage) WithStructureShadow(observer StructureSplitShadowObserver) *SplitStage {
	s.structureShadow = observer
	return s
}

// WithCompleteTimelineStructureAssessment attaches the independently reduced whole-source
// assessment module. Merely attaching it cannot authorize child materialization; the complete-plan
// gate still requires an immutable structure authority.
func (s *SplitStage) WithCompleteTimelineStructureAssessment(decisioner CompleteTimelineStructureDecisioner) *SplitStage {
	s.structureDecisioner = decisioner
	return s
}

// WithStructureMaterialization attaches the certified complete-plan gate. The policy itself still
// verifies explicit release authority; a nil policy deliberately leaves every proposal held.
func (s *SplitStage) WithStructureMaterialization(policy *StructureMaterializationPolicy) *SplitStage {
	s.structureMaterialization = policy
	return s
}

// SegmentVision grounds proposed segments from their own frames so the auto-confirm gate has
// something to judge.
type SegmentVision struct {
	Tools    MediaTools
	Provider llm.VisionProvider
	// RoleEscalator inspects an exact bounded video only when frame evidence cannot establish
	// a role. nil leaves the span unresolved; it never weakens the publication gate.
	RoleEscalator SegmentRoleEscalator
	Taxa          TaxaLister
	ClipDir       string
	// Budget caps how many segments ONE pass will look at (`filler.pipeline.max_split_vision`).
	//
	// ⚠ It is a per-pass cost bound, and §10 V51g is why it is not optional: a rung's cost must
	// stay inside its pass budget.
	//
	// ⚠ **A RATE, not a ceiling (V54).** This used to say a reel with more segments than the
	// budget "simply does not auto-confirm" — true when the budget was indexed absolutely, and it
	// meant reels of 82–303 segments could never be confirmed at all against a default of 60.
	// Grounding now persists and resumes, so a large reel is judged over several passes; a segment
	// still ungrounded when the gate runs is held back on its own rather than sinking the reel.
	Budget func() int
}

// TaxaLister reads the taxonomy the grounder resolves categories against — the SAME read the
// `vision` and `tag` rungs make, so all three ground against one vocabulary.
type TaxaLister interface {
	ListTaxa(ctx context.Context) ([]taxonomy.Taxon, error)
}

// ground stamps Category/Era onto each segment it can see, in place.
//
// Best-effort throughout: a frame-extraction or model failure leaves that segment ungrounded,
// which the gate then refuses — a reel that could not be judged goes to review rather than being
// confirmed on missing data. That direction is the safety property, so every error path here
// degrades toward review and never toward confirm.
func (s *SplitStage) ground(ctx context.Context, c StoreClip, segs []SplitSegment) groundPass {
	file := ""
	if s.vision != nil {
		file = filepath.Join(s.vision.ClipDir, filepath.FromSlash(c.Path))
	}
	return s.groundAt(ctx, c, file, SplitSourceAsset{}, segs)
}

func (s *SplitStage) groundFromSource(ctx context.Context, c StoreClip, source SplitSourceAsset, segs []SplitSegment) groundPass {
	if s.vision == nil {
		return s.groundAt(ctx, c, "", source, segs)
	}
	_, file, err := resolveSplitSource(ctx, s.vision.ClipDir, c, source)
	if err != nil {
		s.groundSkipped(ctx, c, "the proposal's evidence derivative is unavailable", "err", err)
		return groundPass{Pending: countPendingGrounding(segs)}
	}
	return s.groundAt(ctx, c, file, source, segs)
}

func countPendingGrounding(segs []SplitSegment) int {
	n := 0
	for i := range segs {
		if !segs[i].Looked {
			n++
		}
	}
	return n
}

func (s *SplitStage) groundAt(ctx context.Context, c StoreClip, file string, source SplitSourceAsset, segs []SplitSegment) groundPass {
	pending := func() int {
		return countPendingGrounding(segs)
	}
	if s.vision == nil || s.vision.Provider == nil || s.vision.Tools == nil {
		s.groundSkipped(ctx, c, "vision is not wired on this install")
		return groundPass{Pending: pending()}
	}
	// ⚠ The budget bounds ATTEMPTS THIS PASS, not an index into the segment list. It was
	// `if i >= budget { return }`, which is absolute — with resume that stops at the same segment
	// on every pass forever, so a 142-segment reel would ground its first 60 and then loop.
	budget := len(segs)
	if s.vision.Budget != nil {
		if b := s.vision.Budget(); b >= 0 && b < budget {
			budget = b
		}
	}
	if budget == 0 {
		s.groundSkipped(ctx, c, "the per-pass vision budget is zero (filler.pipeline.max_split_vision)")
		return groundPass{Pending: pending()}
	}
	var forest *taxonomy.Forest
	if s.vision.Taxa != nil {
		taxa, err := s.vision.Taxa.ListTaxa(ctx)
		if err != nil {
			// No vocabulary to ground against ⇒ ground nothing, and the gate refuses. Reported as
			// zero looked, which is what stops the caller deferring on a pass that achieved nothing.
			s.groundSkipped(ctx, c, "the taxonomy could not be read", "err", err)
			return groundPass{Pending: pending()}
		}
		forest = taxonomy.New(taxa)
	}
	looked := 0
	// The pass tally. Counted rather than logged per segment: 60 lines a pass is noise, and the
	// question an operator has is about the pass, not any one cut.
	var noFrames, unreadable, learned, roles, unresolvedRoles, videoAttempts, videoRoles, videoErrors int
	var providerErr error
	for i := range segs {
		if looked >= budget {
			break
		}
		if segs[i].Looked {
			continue // ⚠ THE RESUME: an earlier pass already spent a look on this one.
		}
		frames, err := s.vision.Tools.KeyframesIn(ctx, file, segs[i].StartMs, segs[i].EndMs, VisionKeyframes)
		if err != nil || len(frames) == 0 {
			segs[i].Looked, looked = true, looked+1
			noFrames++
			if evidence, attempted, escalationErr := s.escalateSegmentRole(ctx, source, file, segs[i]); attempted {
				videoAttempts++
				if escalationErr != nil {
					videoErrors++
					unresolvedRoles++
				} else if evidence != nil {
					segs[i].RoleEvidence = evidence
					roles, videoRoles, learned = roles+1, videoRoles+1, learned+1
				} else {
					unresolvedRoles++
				}
			} else {
				unresolvedRoles++
			}
			continue
		}
		prompt := visionPrompt(forest)
		resp, err := s.vision.Provider.AskAboutImages(ctx, prompt, frames)
		if err != nil {
			// A separately routed direct-video model may still settle the temporal role. Only a
			// successful escalation marks the span looked; if both routes fail, the old retry
			// behavior remains and this pass stops without burning the rest of the reel.
			if evidence, attempted, escalationErr := s.escalateSegmentRole(ctx, source, file, segs[i]); attempted {
				videoAttempts++
				if escalationErr != nil {
					videoErrors++
					providerErr = errors.Join(err, escalationErr)
					break
				}
				segs[i].Looked, looked = true, looked+1
				if evidence != nil {
					segs[i].RoleEvidence = evidence
					roles, videoRoles, learned = roles+1, videoRoles+1, learned+1
				} else {
					unresolvedRoles++
				}
				continue
			}
			providerErr = err
			break
		}
		segs[i].Looked, looked = true, looked+1
		var out visionOutput
		parsed := json.Unmarshal([]byte(llm.ExtractJSONObject(resp.Content)), &out) == nil
		if !parsed {
			unreadable++
		}
		v := groundVisionTags(out, forest)
		var roleEvidence *StructureRoleEvidence
		var roleErr error
		if parsed {
			roleEvidence, roleErr = structureRoleEvidenceFromVision(source, segs[i], prompt, frames, resp, out, s.structureAssessedAt())
		}
		if roleEvidence == nil {
			if evidence, attempted, escalationErr := s.escalateSegmentRole(ctx, source, file, segs[i]); attempted {
				videoAttempts++
				if escalationErr != nil {
					videoErrors++
				} else if evidence != nil {
					roleEvidence = evidence
					roleErr = nil
					videoRoles++
				}
			}
		}
		if roleErr == nil && roleEvidence != nil {
			segs[i].RoleEvidence = roleEvidence
			roles++
		} else {
			unresolvedRoles++
		}
		if len(v.Tags) > 0 || v.Era > 0 || roleEvidence != nil {
			learned++
		}
		segs[i].Tags = unionLeaves(segs[i].Tags, v.Tags)
		segs[i].Category = v.Category
		segs[i].Era = v.Era
		// ⚠ `SuggestedEra` is deliberately NOT stamped here, though the vision RUNG does stamp it.
		// The gate treats a suggested era as an automatic refusal at every threshold — "an era
		// Loomarr GUESSED is exactly the case a human should see" — so writing one would make this
		// grounder guarantee the rejection it exists to lift. The child clip's own vision pass
		// still raises the suggestion afterwards, where an operator can act on it.
	}
	s.groundReport(ctx, c, groundTally{
		looked: looked, pending: pending(), learned: learned,
		noFrames: noFrames, unreadable: unreadable, roles: roles, unresolvedRoles: unresolvedRoles,
		videoAttempts: videoAttempts, videoRoles: videoRoles, videoErrors: videoErrors,
		providerErr: providerErr,
	})
	return groundPass{Looked: looked, Pending: pending()}
}

// groundTally is one pass's outcome, counted so the pass can be reported in a single line.
type groundTally struct {
	looked, pending, learned, noFrames, unreadable, roles, unresolvedRoles int
	videoAttempts, videoRoles, videoErrors                                 int
	providerErr                                                            error
}

func (s *SplitStage) escalateSegmentRole(ctx context.Context, source SplitSourceAsset, file string, segment SplitSegment) (*StructureRoleEvidence, bool, error) {
	if s.vision == nil || s.vision.RoleEscalator == nil || source.validate() != nil {
		return nil, false, nil
	}
	evidence, err := s.vision.RoleEscalator.EscalateRole(ctx, source, file, segment, s.structureAssessedAt())
	return evidence, true, err
}

func (s *SplitStage) structureAssessedAt() time.Time {
	if s.splitter != nil && s.splitter.now != nil {
		return s.splitter.now().UTC()
	}
	return time.Now().UTC()
}

// groundSkipped reports a pass that never began. Each caller passes a DIFFERENT reason, which is
// the entire point: these were four indistinguishable early returns.
func (s *SplitStage) groundSkipped(ctx context.Context, c StoreClip, why string, args ...any) {
	if s.log == nil {
		return
	}
	s.log.WarnContext(ctx, "split grounding did not run: "+why, append([]any{"clip", c.Hash}, args...)...)
}

// groundReport reports what a pass achieved.
//
// ⚠ **The `learned == 0` case is the one this exists for, and it is not an error anywhere.** A
// model can answer every single call, parse cleanly, and still ground nothing — `llava:7b`
// (measured 2026-08-13) echoed the prompt's own placeholder list back as its answer, so
// `"category":"toys|cereal|cars|..."` arrived as well-formed JSON and `groundVisionTags` correctly
// dropped every field. Thirty-seven segments, thirty-seven successful calls, one usable category,
// and the reel refused as untagged. Every counter along that path says "fine": no provider error,
// no parse error, `Looked` incremented 37 times. Only `learned` tells the truth, and only by being
// compared against `looked`.
func (s *SplitStage) groundReport(ctx context.Context, c StoreClip, t groundTally) {
	if s.log == nil || (t.looked == 0 && t.providerErr == nil) {
		return
	}
	args := []any{
		"clip", c.Hash, "looked", t.looked, "learned", t.learned,
		"pending", t.pending, "noFrames", t.noFrames, "unreadable", t.unreadable,
		"roles", t.roles, "unresolvedRoles", t.unresolvedRoles,
		"videoAttempts", t.videoAttempts, "videoRoles", t.videoRoles, "videoErrors", t.videoErrors,
	}
	switch {
	case t.providerErr != nil:
		s.log.WarnContext(ctx, "split grounding stopped: the vision provider refused",
			append(args, "err", t.providerErr)...)
	case t.looked > 0 && t.learned == 0:
		s.log.WarnContext(ctx, "split grounding read every cut and learned nothing — check filler.vision.model can follow a JSON prompt", args...)
	default:
		s.log.InfoContext(ctx, "split grounding pass", args...)
	}
}

// pendingFor returns the proposal already waiting for this clip, or nil.
//
// ⚠ One read of the whole pending list rather than a per-clip lookup, unchanged from V51b: the
// queue is a review backlog a human is expected to clear, so it is small by construction.
func (s *SplitStage) pendingFor(ctx context.Context, hash string) (*SplitProposal, error) {
	pending, err := s.store.ListSplitProposals(ctx)
	if err != nil {
		return nil, err
	}
	for i := range pending {
		if pending[i].ClipHash == hash {
			return &pending[i], nil
		}
	}
	return nil, nil
}

// countPending counts segments the grounder has never looked at.
func countPending(segs []SplitSegment) int {
	n := 0
	for i := range segs {
		if !segs[i].Looked {
			n++
		}
	}
	return n
}

// groundPass is what ONE grounding pass achieved: how many segments it looked at, and how many
// have still never been looked at.
//
// ⚠ `Looked == 0 && Pending > 0` is the termination signal, and the difference matters: it means
// the pass could not make progress (vision off, no vocabulary, the provider down at the first
// segment), so the caller must NOT defer — it falls through to the gate and the reel parks with a
// reason. Deferring on a pass that achieved nothing is an infinite loop.
type groundPass struct {
	Looked  int
	Pending int
}

type splitDiscards struct {
	duplicates int
	short      int
}

// discardDeterministic removes outcomes that cannot become useful new clips. Ambiguous or
// unsplittable spans remain for review; known duplicates and below-floor fragments do not.
func discardDeterministic(segs []SplitSegment, minClipDuration time.Duration) ([]SplitSegment, splitDiscards) {
	kept := make([]SplitSegment, 0, len(segs))
	var discarded splitDiscards
	for _, seg := range segs {
		if seg.DupOf != "" {
			discarded.duplicates++
			continue
		}
		span := time.Duration(seg.EndMs-seg.StartMs) * time.Millisecond
		if minClipDuration > 0 && span < minClipDuration {
			discarded.short++
			continue
		}
		seg.Index = len(kept)
		kept = append(kept, seg)
	}
	return kept, discarded
}

func discardNote(d splitDiscards) string {
	switch {
	case d.duplicates > 0 && d.short > 0:
		return fmt.Sprintf("discarded %d duplicate(s) and %d short fragment(s)", d.duplicates, d.short)
	case d.duplicates > 0:
		return fmt.Sprintf("discarded %d duplicate(s)", d.duplicates)
	case d.short > 0:
		return fmt.Sprintf("discarded %d short fragment(s)", d.short)
	default:
		return ""
	}
}

func (s *SplitStage) ID() StageID     { return StageSplit }
func (s *SplitStage) Cost() StageCost { return CostSplit }

// Applies to a composite, and nothing else.
//
// ⚠ **The candidate decision is the probe rung's, not this one's.** Probe measures the file and
// sets `is_composite`; this rung acts on that mark. Splitting the question that way is what lets
// the expensive boundary scan be gated on duration in ONE place, and it is why a 16-minute
// recording is excluded from pods the moment it is measured rather than only once someone splits
// it — the exact bug §10 V45 describes.
func (s *SplitStage) Applies(_ context.Context, c StoreClip) (bool, string) {
	if s.splitter == nil {
		return false, "splitting is not available on this install"
	}
	if !c.IsComposite {
		return false, "not a compilation"
	}
	return true, ""
}

// Run detects the cuts and either makes them or leaves them for a human.
func (s *SplitStage) Run(ctx context.Context, c StoreClip) (StageResult, error) {
	// Already review-pending: re-detecting would replace a proposal an operator may be halfway
	// through editing. ⚠ Keyed by HASH on both sides — the identity `Propose` takes.
	// ⚠ An existing proposal is RE-GROUNDED, never re-detected. Until V54 this returned outright,
	// which looked like "leave the operator's cut list alone" and was in fact a dead end: the row
	// went to `review`, `ListPipelineWork` only claims `running`, and nothing ever reached that
	// reel again. Every compilation detected before the grounder existed was therefore permanently
	// ungroundable — measured as ~50 reels parked at `split`, none auto-confirmed, ever.
	//
	// Re-detection stays the operator's call (`POST /v1/filler/split`): a rung must not redraw a
	// cut list a human may have open. Grounding is additive and touches no boundary.
	p, err := s.pendingFor(ctx, c.Hash)
	if err != nil {
		return StageResult{}, err
	}
	if p == nil || !p.Ready() {
		var complete bool
		if p, complete, err = s.splitter.advanceProposal(ctx, c.Hash, p); err != nil {
			return StageResult{}, err
		}
		if !complete {
			return StageResult{}, ErrDeferred
		}
	}
	if p.StructureDecision == nil && s.structureDecisioner != nil {
		assessed, assessErr := s.splitter.AssessProposalStructure(ctx, *p, s.structureDecisioner)
		if errors.Is(assessErr, ErrProposalGone) {
			return StageResult{Verdict: VerdictContinue, Note: "already resolved"}, nil
		}
		if assessErr != nil {
			// An assessment outage or invalid runtime result says nothing about the source or
			// its proposed boundaries. Keep both at split review rather than letting ordinary
			// retry exhaustion skip the rung and file a compilation without an assessment.
			// Context cancellation is control flow, though: the pipeline resumes it without
			// turning an incomplete attempt into review evidence.
			if errors.Is(assessErr, context.Canceled) || errors.Is(assessErr, context.DeadlineExceeded) {
				return StageResult{}, assessErr
			}
			return StageResult{
				Verdict: VerdictReview,
				Note:    "the complete-timeline assessment could not be completed; review the split proposal",
			}, nil
		}
		p = &assessed
	}
	// Deterministic outcomes are curation, not decisions. Persist them before any model work so a
	// restart and the review UI see the same smaller reel.
	kept, discarded := discardDeterministic(p.Segments, s.minClipFloor())
	if len(kept) != len(p.Segments) {
		p.Segments = kept
		if err := s.splitter.saveProposal(ctx, *p); err != nil {
			return StageResult{}, err
		}
	}
	if len(p.Segments) == 0 {
		if err := s.splitter.resolveEmpty(ctx, p.ID); err != nil {
			return StageResult{}, err
		}
		note := discardNote(discarded)
		if note == "" {
			note = "no usable adverts remained"
		}
		return StageResult{Verdict: VerdictContinue, Note: note}, nil
	}

	// Ground the segments from their own frames BEFORE the gate reads them (§10 V54).
	pass := s.groundFromSource(ctx, c, p.Source, p.Segments)

	// Persist what this pass learned, so the next one resumes instead of starting over.
	if pass.Looked > 0 {
		merged, rerr := s.splitter.Reground(ctx, p.ID, p.Segments)
		switch {
		case errors.Is(rerr, ErrProposalGone):
			// Confirmed or rejected under us while we were grounding. Nothing to write and nothing
			// to decide — the clip's disposition is already someone else's answer.
			return StageResult{Verdict: VerdictContinue, Note: "already resolved"}, nil
		case rerr != nil:
			return StageResult{}, rerr
		}
		p = &merged
		pass = groundPass{Looked: pass.Looked, Pending: countPending(p.Segments)}
	}

	// ⚠ Partly grounded is UNFINISHED WORK, not a refusal. `filler.pipeline.max_split_vision`
	// bounds ONE PASS; a 142-segment reel is judged over several. Deferring requires that this
	// pass achieved something (`Looked > 0`), which is what guarantees termination: every look
	// marks a segment, so `Pending` strictly decreases and the reel reaches a verdict.
	if pass.Pending > 0 && pass.Looked > 0 {
		return StageResult{Verdict: VerdictDefer, Note: fmt.Sprintf(
			"looked at %d of %d cuts so far", len(p.Segments)-pass.Pending, len(p.Segments))}, nil
	}

	// ⚠ Auto-confirm is decided HERE, not inside `Propose`. The manual path runs the same Propose
	// from a button an operator just pressed — a human is already present and about to review, so
	// confirming under them would take the decision they came to make.
	//
	// ⚠ **PER SEGMENT since V54.** This used to be one verdict for the reel, so one doubtful cut
	// in 52 sent all 52 back and the operator's work never shrank.
	legacy, part := s.splitPartitions(*p)
	if s.structureShadow != nil {
		if err := s.structureShadow.ObserveStructureSplit(ctx, *p, legacy); err != nil {
			return StageResult{}, err
		}
	}
	if part.Reject != AutoSplitOK {
		// A whole-proposal refusal — disabled splitting, an incomplete certified plan, or nothing
		// detected. Certified holds must be persisted too: a reviewable proposal without its
		// attributable reason would turn missing authority into an opaque failure.
		if len(part.Hold) > 0 {
			if err := s.persistHeldReasons(ctx, p.ID, part.Hold); err != nil {
				if errors.Is(err, ErrProposalGone) {
					return StageResult{Verdict: VerdictContinue, Note: "already resolved"}, nil
				}
				return StageResult{}, err
			}
		}
		note := string(part.Reject)
		if d := discardNote(discarded); d != "" {
			note += "; " + d
		}
		return StageResult{Verdict: VerdictReview, Note: note}, nil
	}
	if len(part.Confirm) == 0 {
		// Nothing cleared the gate. The reason is RECORDED on the ladder, not just logged: "it
		// didn't auto-confirm" is unactionable for an operator deciding whether to lower a
		// threshold.
		//
		// ⚠ **With the COUNT, because the bare reason was actively misleading.** The note used to
		// be one segment's reason presented as the reel's, and on a real reel that was the reason
		// for 1 cut out of 37 — sending the operator to a threshold that was never consulted.
		note := holdNote(part)
		if d := discardNote(discarded); d != "" {
			note += "; " + d
		}
		// Persist the per-segment reasons too. The ladder note explains the reel in aggregate, but
		// the review screen has to tell the operator which cut needs classification, a grounded
		// era, or a closer look at its boundary. Partial confirmation persists these through
		// ConfirmSome; the all-held path previously returned before writing them anywhere.
		if err := s.persistHeldReasons(ctx, p.ID, part.Hold); err != nil {
			if errors.Is(err, ErrProposalGone) {
				return StageResult{Verdict: VerdictContinue, Note: "already resolved"}, nil
			}
			return StageResult{}, err
		}
		return StageResult{Verdict: VerdictReview, Note: note}, nil
	}

	// ⚠ `ConfirmSome`, not `Confirm`: the held segments must SURVIVE in the proposal. Calling the
	// manual path here would delete the reel's remaining cuts along with the confirmed ones — the
	// five a human still needs to see would simply vanish.
	spawned, err := s.splitter.ConfirmSome(ctx, p.ID, part.Confirm, part.Hold)
	if err != nil {
		// The proposal survives, so the operator can still review it by hand — a failed
		// auto-confirm degrades to the manual path rather than losing the detection work. Returned
		// as an error so the runner retries with backoff.
		//
		// ⚠ This used to claim that "exhausting the retries leaves the reel in review". It does
		// not: `split` is not in `fatalStages`, so `onFailure` resolves and the row STEPS ONWARD,
		// through rungs the composite guard skips, landing at `filed`. The reel is still reviewable
		// — its proposal is intact and Incoming lists it — but the pipeline row says finished, not
		// waiting. Corrected rather than deleted because the wrong version is the kind of comment
		// someone reasons from when deciding whether a reel can be recovered.
		return StageResult{}, err
	}
	reportProgress(ctx, StageSplit, 100)

	// ⚠ The segments are SPAWNED, so each one is enrolled at `probe` and runs the whole ladder for
	// itself — transcoded, heard, transcribed, tagged and scored like any other arrival. Before
	// this, a freshly cut segment had to wait for whichever of six sweeps happened to reach it.
	note := fmt.Sprintf("cut into %d adverts", len(spawned))
	if len(spawned) == 1 {
		note = "cut into one advert"
	}
	if d := discardNote(discarded); d != "" {
		note += "; " + d
	}

	// ⚠ **A PARTIAL confirm keeps the reel in review, with only the doubtful cuts left.** The
	// proposal has already been shrunk to `part.Hold` by `Confirm`; the row must follow it there,
	// or a reel with five unresolved cuts would report itself finished and the operator would
	// never be told the five exist.
	if len(part.Hold) > 0 {
		return StageResult{
			Verdict: VerdictReview,
			Spawned: spawned,
			Note: fmt.Sprintf("%s; %s to review (%s)", note,
				pluralizeCuts(len(part.Hold)), part.Verdict()),
		}, nil
	}
	return StageResult{Verdict: VerdictContinue, Spawned: spawned, Note: note}, nil
}

func (s *SplitStage) persistHeldReasons(ctx context.Context, proposalID string, held []SplitSegment) error {
	_, err := s.splitter.Reground(ctx, proposalID, held)
	return err
}

func (s *SplitStage) splitPartitions(proposal SplitProposal) (SplitPartition, SplitPartition) {
	compatibility := AutoConfirmable(proposal, s.autoConfirm, s.minClipFloor())
	application := CertifiedStructureMaterializable(proposal, s.autoConfirm, s.structureMaterialization, s.minClipFloor())
	return compatibility, application
}

// minClipFloor resolves the clip-duration floor, defaulting to zero (no floor check).
func (s *SplitStage) minClipFloor() time.Duration {
	if s.minClipDuration == nil {
		return 0
	}
	return s.minClipDuration()
}

// resumableReviewHashes finds older review rows that the current stage can improve without a
// person: incomplete detection, deterministic discards, or unlooked segments with runnable vision.
func (s *SplitStage) resumableReviewHashes(ctx context.Context) (map[string]struct{}, error) {
	proposals, err := s.store.ListSplitProposals(ctx)
	if err != nil {
		return nil, err
	}
	canGround := s.autoConfirm != nil && s.autoConfirm.Enabled != nil && s.autoConfirm.Enabled() &&
		s.vision != nil && s.vision.Provider != nil && s.vision.Tools != nil
	if canGround && s.vision.Budget != nil && s.vision.Budget() <= 0 {
		canGround = false
	}
	out := make(map[string]struct{})
	for _, p := range proposals {
		if !p.Ready() {
			out[p.ClipHash] = struct{}{}
			continue
		}
		if s.structureDecisioner != nil && p.StructureDecision == nil {
			out[p.ClipHash] = struct{}{}
			continue
		}
		if s.structureShadow != nil {
			pending, shadowErr := s.structureShadow.NeedsStructureSplitObservation(ctx, p)
			if shadowErr != nil {
				return nil, shadowErr
			}
			if pending {
				out[p.ClipHash] = struct{}{}
				continue
			}
		}
		kept, discarded := discardDeterministic(p.Segments, s.minClipFloor())
		if len(kept) != len(p.Segments) || discarded.duplicates > 0 || discarded.short > 0 {
			out[p.ClipHash] = struct{}{}
			continue
		}
		if canGround {
			for _, seg := range p.Segments {
				if !seg.Looked {
					out[p.ClipHash] = struct{}{}
					break
				}
			}
		}
	}
	return out, nil
}
