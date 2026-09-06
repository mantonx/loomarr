package filler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/taxonomy"
)

// The auto-confirm gate has had NO data source since V51g removed the text classifier, so
// `filler.autosplit.enabled` has been default-ON and structurally unable to fire — measured on the
// maintainer's catalog as 45 compilations parked at `split`, none ever auto-confirmed. These tests
// pin the property that matters: grounding a segment from its own frames makes the REAL gate pass.
//
// ⚠ Deliberately asserted through `AutoConfirmable` rather than by inspecting the stamped fields.
// "ground() set Category" is a claim about a function; "the gate now accepts the reel" is the claim
// the feature makes to an operator, and only the second one can fail if the gate changes.

// spanTools records the spans it was asked to frame, so a test can prove the grounder scoped its
// extraction to each SEGMENT rather than re-framing the whole compilation N times.
type spanTools struct {
	spans  [][2]int64
	frames [][]byte
	err    error
}

func (s *spanTools) KeyframesIn(_ context.Context, _ string, startMs, endMs int64, _ int) ([][]byte, error) {
	s.spans = append(s.spans, [2]int64{startMs, endMs})
	return s.frames, s.err
}
func (s *spanTools) Keyframes(context.Context, string, int) ([][]byte, error) { return s.frames, s.err }
func (s *spanTools) Chapters(context.Context, string) ([]Chapter, error)      { return nil, nil }
func (s *spanTools) Boundaries(context.Context, string, int64, int64) ([]Interval, []Interval, error) {
	return nil, nil, nil
}
func (s *spanTools) Transcribe(context.Context, string, int64, int64) ([]TranscriptSegment, error) {
	return nil, nil
}
func (s *spanTools) GrayFrames(context.Context, string, int64, int64) ([][]byte, error) {
	return nil, nil
}
func (s *spanTools) Cut(context.Context, string, int64, int64, string) error { return nil }

type fixedVision struct {
	answer      string
	err         error
	calls       int
	attribution llm.Attribution
	prompts     []string
}

type scriptedRoleEscalator struct {
	calls int
	spans [][2]int64
	err   error
}

func (s *scriptedRoleEscalator) EscalateRole(_ context.Context, source SplitSourceAsset, _ string, segment SplitSegment, assessedAt time.Time) (*StructureRoleEvidence, error) {
	s.calls++
	s.spans = append(s.spans, [2]int64{segment.StartMs, segment.EndMs})
	if s.err != nil {
		return nil, s.err
	}
	evidence, err := NewStructureRoleEvidence(StructureRoleEvidenceInput{
		Source: source, StartMs: segment.StartMs, EndMs: segment.EndMs,
		Role: SegmentRoleCommercial, Reason: "the complete sequence contains a product offer and call to action",
		Video: []byte("bounded-video"), PromptVersion: "video-role-v1", Prompt: "classify bounded video", Response: `{"role":"commercial"}`,
		RequestedProvider: "openrouter", ResolvedProvider: "openrouter", RequestedModel: "video", ResolvedModel: "video",
		Modalities: []string{"text", "video"}, Tokens: StructureRoleTokenUsage{Video: 1}, Attempts: 1, AssessedAt: assessedAt,
	})
	if err != nil {
		return nil, err
	}
	return &evidence, nil
}

func (f *fixedVision) AskAboutImages(_ context.Context, prompt string, _ [][]byte) (llm.Response, error) {
	f.calls++
	f.prompts = append(f.prompts, prompt)
	if f.err != nil {
		return llm.Response{}, f.err
	}
	return llm.Response{Content: f.answer, Attribution: f.attribution}, nil
}

type seedTaxa struct{}

func (seedTaxa) ListTaxa(context.Context) ([]taxonomy.Taxon, error) {
	return taxonomy.SeedForest(), nil
}

type proposalQueue struct{ proposals []SplitProposal }

func (p proposalQueue) ListSplitProposals(context.Context) ([]SplitProposal, error) {
	return p.proposals, nil
}

// gatePolicy is auto-confirm ON at a mid threshold — below MaxAutoFileConfidence, so a grounded
// era is not additionally required and Category alone decides.
func gatePolicy() *AutoSplitPolicy {
	return &AutoSplitPolicy{
		Enabled:       func() bool { return true },
		MinConfidence: func() int { return 70 },
		MaxDuration:   func() time.Duration { return 10 * time.Minute },
	}
}

// twoSegments is a pair of WELL-DETECTED cuts: reel edges at the outside, a boundary both
// detectors agreed on in the middle.
//
// ⚠ The provenance is not decoration. Since V54 the gate's last check is boundary confidence, so a
// fixture with no evidence is held back on "not sure this was cut in the right place" — which is
// correct behaviour, and would make every test here assert the wrong thing. These tests are about
// GROUNDING (tags), so their boundaries must be beyond reproach for the tag half to be what the
// gate is deciding on.
func twoSegments() []SplitSegment {
	segs := []SplitSegment{
		{Index: 0, StartMs: 0, EndMs: 30_000, Name: "reel part 1",
			startSrc: srcReelEdge, endSrc: srcBlack | srcSilence},
		{Index: 1, StartMs: 30_000, EndMs: 61_000, Name: "reel part 2",
			startSrc: srcBlack | srcSilence, endSrc: srcReelEdge},
	}
	scoreBoundaries(segs)
	return segs
}

// The baseline this feature exists to change: ungrounded segments are refused, every time.
func TestSegmentVision_WithoutGrounding_TheGateRefuses(t *testing.T) {
	segs := twoSegments()
	if got := AutoConfirmable(SplitProposal{Segments: segs}, gatePolicy(), 0).Verdict(); got != RejectUntagged {
		t.Fatalf("ungrounded segments = %q, want %q — this is the state 45 live reels are in",
			got, RejectUntagged)
	}
}

func TestSegmentVision_GroundsFromFramesSoTheGateCanFire(t *testing.T) {
	tools := &spanTools{frames: [][]byte{[]byte("\xff\xd8jpeg\xff\xd9")}}
	// A grounded product category the seed forest resolves. Era is left absent on purpose: below
	// the confidence ceiling the gate asks only for tags, and an era this model did not read must
	// not be invented.
	model := &fixedVision{answer: `{"category":"toys","visibleText":"TOYS R US MEGA SALE"}`}
	s := NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools: tools, Provider: model, Taxa: seedTaxa{}, ClipDir: "/clips",
		Budget: func() int { return 10 },
	})

	segs := twoSegments()
	s.ground(context.Background(), StoreClip{Clip: Clip{Path: "reel.mp4"}}, segs)

	// THE assertion: the same gate that refused above now accepts.
	if got := AutoConfirmable(SplitProposal{Segments: segs}, gatePolicy(), 0).Verdict(); got != AutoSplitOK {
		t.Fatalf("grounded segments = %q, want AutoSplitOK", got)
	}
	// ⚠ Each segment framed over ITS OWN span. Without this the grounder would re-frame the whole
	// compilation per segment — the exact unbounded-per-segment cost §10 V51g forbids — and every
	// segment would be classified from the same frames.
	want := [][2]int64{{0, 30_000}, {30_000, 61_000}}
	if len(tools.spans) != 2 || tools.spans[0] != want[0] || tools.spans[1] != want[1] {
		t.Errorf("framed spans = %v, want %v", tools.spans, want)
	}
	// ⚠ SuggestedEra must stay empty. The gate refuses a guessed era at EVERY threshold, so a
	// grounder that emitted one would guarantee the rejection it exists to lift.
	for _, sg := range segs {
		if sg.SuggestedEra != 0 {
			t.Errorf("segment %d carries SuggestedEra %d — that is an automatic gate refusal",
				sg.Index, sg.SuggestedEra)
		}
	}
}

func TestSegmentVisionRetainsExactPerSpanRoleEvidence(t *testing.T) {
	tools := &spanTools{frames: [][]byte{[]byte("opening"), []byte("closing")}}
	escalator := &scriptedRoleEscalator{}
	model := &fixedVision{
		answer: `{"visibleText":"ACME","brand":"ACME","tags":["toys"],"role":"commercial","roleReason":"a product offer and closing brand card"}`,
		attribution: llm.Attribution{
			RequestedProvider: "ollama", ResolvedProvider: "ollama", RequestedModel: "vision", ResolvedModel: "vision@sha256:abc",
			Modalities: []string{"text", "image"}, Tokens: llm.TokenUsage{Prompt: 20, Completion: 5, Image: 2},
			Latency: 100 * time.Millisecond, Attempts: 1,
		},
	}
	s := NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools: tools, Provider: model, RoleEscalator: escalator, Taxa: seedTaxa{}, Budget: func() int { return 10 },
	})
	source := structureSource(61_000)
	segments := twoSegments()
	pass := s.groundAt(context.Background(), StoreClip{}, "source.mp4", source, segments)
	if pass.Looked != 2 || pass.Pending != 0 {
		t.Fatalf("ground pass = %+v", pass)
	}
	for index, segment := range segments {
		if segment.RoleEvidence == nil || segment.RoleEvidence.Role != SegmentRoleCommercial || segment.RoleEvidence.Source != source || segment.RoleEvidence.StartMs != segment.StartMs || segment.RoleEvidence.EndMs != segment.EndMs || segment.RoleEvidence.SHA256 == "" {
			t.Fatalf("segment %d role evidence = %+v", index, segment.RoleEvidence)
		}
	}
	if len(model.prompts) != 2 || !strings.Contains(model.prompts[0], "programme_fragment") || !strings.Contains(model.prompts[0], "never infer role from the generated filename or duration") {
		t.Fatalf("role prompt = %q", model.prompts[0])
	}
	if escalator.calls != 0 {
		t.Fatalf("resolved frame roles triggered %d video escalations", escalator.calls)
	}

	base := assessStructure(t, source.DurationMs, []StructureObservation{
		structureObservation("black", ObservationBlackInterval, ObservationProposesBoundary, 29_900, 30_100),
		structureObservation("silence", ObservationSilenceInterval, ObservationProposesBoundary, 29_900, 30_100),
	}, nil)
	assessment, err := reassessProposalStructure(SplitProposal{Source: source, Structure: &base, Segments: segments}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Kind != StructureCompilationBreak || len(assessment.Plan) != 2 || assessment.Plan[0].Role != SegmentRoleCommercial || assessment.Plan[1].Role != SegmentRoleCommercial {
		t.Fatalf("role-enriched assessment = %+v", assessment)
	}
	// Partial confirmation removes published cuts from the live proposal. Regrounding a held cut
	// must not erase the already-published interval's role from the complete source assessment.
	remaining, err := reassessProposalStructure(SplitProposal{
		Source: source, Structure: &assessment, Segments: segments[1:],
	}, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if remaining.Kind != StructureCompilationBreak || remaining.Plan[0].Role != SegmentRoleCommercial || remaining.Plan[1].Role != SegmentRoleCommercial {
		t.Fatalf("partial proposal erased prior role evidence: %+v", remaining)
	}
}

func TestSegmentVisionMissingAttributionCannotEstablishRole(t *testing.T) {
	model := &fixedVision{answer: `{"tags":["toys"],"role":"commercial","roleReason":"a product offer"}`}
	s := NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools: &spanTools{frames: [][]byte{[]byte("frame")}}, Provider: model, Taxa: seedTaxa{}, Budget: func() int { return 1 },
	})
	segments := twoSegments()[:1]
	s.groundAt(context.Background(), StoreClip{}, "source.mp4", structureSource(61_000), segments)
	if segments[0].RoleEvidence != nil {
		t.Fatalf("unattributed model output established a role: %+v", segments[0].RoleEvidence)
	}
	if segments[0].Category == "" {
		t.Fatal("invalid role attribution discarded independently valid taxonomy grounding")
	}
}

func TestSegmentVisionEscalatesOnlyUnresolvedRolesToBoundedVideo(t *testing.T) {
	escalator := &scriptedRoleEscalator{}
	model := &fixedVision{answer: `{"category":"toys","visibleText":"TOYS R US","role":"","roleReason":""}`}
	s := NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools: &spanTools{frames: [][]byte{[]byte("frame")}}, Provider: model, RoleEscalator: escalator,
		Taxa: seedTaxa{}, Budget: func() int { return 2 },
	})
	segments := twoSegments()
	s.groundAt(context.Background(), StoreClip{}, "source.mp4", structureSource(61_000), segments)
	if escalator.calls != 2 || len(escalator.spans) != 2 || escalator.spans[0] != [2]int64{0, 30_000} || escalator.spans[1] != [2]int64{30_000, 61_000} {
		t.Fatalf("escalation calls=%d spans=%v", escalator.calls, escalator.spans)
	}
	for index, segment := range segments {
		if segment.RoleEvidence == nil || segment.RoleEvidence.VideoSHA256 == "" || len(segment.RoleEvidence.FrameSHA256) != 0 || segment.Category == "" {
			t.Fatalf("segment %d did not preserve taxonomy plus video role evidence: %+v", index, segment)
		}
	}
}

func TestSegmentVisionCanEscalateWhenSparseFramesAreUnavailable(t *testing.T) {
	escalator := &scriptedRoleEscalator{}
	s := NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools: &spanTools{}, Provider: &fixedVision{}, RoleEscalator: escalator, Taxa: seedTaxa{}, Budget: func() int { return 1 },
	})
	segments := twoSegments()[:1]
	pass := s.groundAt(context.Background(), StoreClip{}, "source.mp4", structureSource(61_000), segments)
	if pass.Looked != 1 || pass.Pending != 0 || escalator.calls != 1 || segments[0].RoleEvidence == nil || segments[0].RoleEvidence.VideoSHA256 == "" {
		t.Fatalf("pass=%+v escalation=%d segment=%+v", pass, escalator.calls, segments[0])
	}
}

func TestSegmentVisionDirectVideoCanResolveAnIndependentFrameProviderFailure(t *testing.T) {
	escalator := &scriptedRoleEscalator{}
	s := NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools: &spanTools{frames: [][]byte{[]byte("frame")}}, Provider: &fixedVision{err: errors.New("frame provider unavailable")},
		RoleEscalator: escalator, Taxa: seedTaxa{}, Budget: func() int { return 1 },
	})
	segments := twoSegments()[:1]
	pass := s.groundAt(context.Background(), StoreClip{}, "source.mp4", structureSource(61_000), segments)
	if pass.Looked != 1 || pass.Pending != 0 || escalator.calls != 1 || segments[0].RoleEvidence == nil {
		t.Fatalf("pass=%+v escalation=%d segment=%+v", pass, escalator.calls, segments[0])
	}

	failing := &scriptedRoleEscalator{err: errors.New("video provider unavailable")}
	s = NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools: &spanTools{frames: [][]byte{[]byte("frame")}}, Provider: &fixedVision{err: errors.New("frame provider unavailable")},
		RoleEscalator: failing, Taxa: seedTaxa{}, Budget: func() int { return 1 },
	})
	segments = twoSegments()[:1]
	pass = s.groundAt(context.Background(), StoreClip{}, "source.mp4", structureSource(61_000), segments)
	if pass.Looked != 0 || pass.Pending != 1 || failing.calls != 1 || segments[0].Looked {
		t.Fatalf("dual failure burned retry: pass=%+v escalation=%d segment=%+v", pass, failing.calls, segments[0])
	}
}

// ⚠ **THE resume property (§10 V54): the budget is a RATE, not a ceiling.**
//
// Before this, `ground` indexed the budget absolutely (`if i >= budget { return }`), so a reel
// larger than `filler.pipeline.max_split_vision` ground its first N segments on every pass and
// never advanced past them. Live reels run 82–303 segments against a default budget of 60, so the
// budget silently meant "reels this big can never auto-confirm".
func TestSegmentVision_ResumesWhereThePreviousPassStopped(t *testing.T) {
	tools := &spanTools{frames: [][]byte{[]byte("\xff\xd8jpeg\xff\xd9")}}
	model := &fixedVision{answer: `{"category":"toys","visibleText":"TOYS R US MEGA SALE"}`}
	s := NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools: tools, Provider: model, Taxa: seedTaxa{}, ClipDir: "/clips",
		Budget: func() int { return 1 }, // one segment per pass — two passes for two segments
	})
	clip := StoreClip{Clip: Clip{Path: "reel.mp4"}}
	segs := twoSegments()

	first := s.ground(context.Background(), clip, segs)
	if first.Looked != 1 || first.Pending != 1 {
		t.Fatalf("pass 1 = %+v, want {Looked:1 Pending:1}", first)
	}
	if got := AutoConfirmable(SplitProposal{Segments: segs}, gatePolicy(), 0).Verdict(); got == AutoSplitOK {
		t.Fatal("a half-grounded reel must not pass the gate — all-or-nothing is unchanged")
	}

	second := s.ground(context.Background(), clip, segs)
	if second.Looked != 1 || second.Pending != 0 {
		t.Fatalf("pass 2 = %+v, want {Looked:1 Pending:0} — the second pass must advance, not repeat", second)
	}

	// ⚠ The load-bearing assertion: the SECOND span was framed, not the first one twice. An
	// absolute-index budget passes every count-based check here and still fails this one.
	want := [][2]int64{{0, 30_000}, {30_000, 61_000}}
	if len(tools.spans) != 2 || tools.spans[0] != want[0] || tools.spans[1] != want[1] {
		t.Fatalf("framed spans = %v, want %v — pass 2 re-framed the segment pass 1 already did",
			tools.spans, want)
	}
	if got := AutoConfirmable(SplitProposal{Segments: segs}, gatePolicy(), 0).Verdict(); got != AutoSplitOK {
		t.Errorf("fully grounded over two passes = %q, want AutoSplitOK", got)
	}
}

// ⚠ Termination: a pass that looks at NOTHING must report `Looked == 0`, because that is the
// signal `Run` uses to stop deferring and let the gate park the reel. Without it a reel whose
// provider is down would defer forever, every two minutes, silently.
func TestSegmentVision_APassThatAchievesNothingReportsNoProgress(t *testing.T) {
	segs := twoSegments()
	s := NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools:    &spanTools{frames: [][]byte{[]byte("\xff\xd8jpeg\xff\xd9")}},
		Provider: &fixedVision{answer: `{"category":"toys"}`}, Taxa: seedTaxa{}, ClipDir: "/clips",
		Budget: func() int { return 0 }, // vision switched off
	})

	got := s.ground(context.Background(), StoreClip{Clip: Clip{Path: "reel.mp4"}}, segs)
	if got.Looked != 0 || got.Pending != 2 {
		t.Fatalf("zero-budget pass = %+v, want {Looked:0 Pending:2} — Looked>0 would defer forever", got)
	}
}

// The budget is a cost bound, and exceeding it must fail SAFE: an ungrounded tail leaves the reel
// in review rather than confirming a reel only partly looked at.
func TestSegmentVision_BudgetLeavesTheRestUngroundedAndTheReelInReview(t *testing.T) {
	tools := &spanTools{frames: [][]byte{[]byte("\xff\xd8jpeg\xff\xd9")}}
	model := &fixedVision{answer: `{"category":"toys","visibleText":"TOYS R US"}`}
	s := NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools: tools, Provider: model, Taxa: seedTaxa{}, ClipDir: "/clips",
		Budget: func() int { return 1 }, // one segment only
	})

	segs := twoSegments()
	s.ground(context.Background(), StoreClip{Clip: Clip{Path: "reel.mp4"}}, segs)

	if model.calls != 1 {
		t.Errorf("model called %d times, want 1 — the budget is not bounding the pass", model.calls)
	}
	if got := AutoConfirmable(SplitProposal{Segments: segs}, gatePolicy(), 0).Verdict(); got != RejectUntagged {
		t.Fatalf("partly-grounded reel = %q, want %q — a reel nobody finished judging must wait",
			got, RejectUntagged)
	}
}

// A model failure must not confirm anything. Every error path degrades toward review.
func TestSegmentVision_ModelFailureLeavesTheReelInReview(t *testing.T) {
	tools := &spanTools{frames: [][]byte{[]byte("\xff\xd8jpeg\xff\xd9")}}
	model := &fixedVision{err: context.DeadlineExceeded}
	s := NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools: tools, Provider: model, Taxa: seedTaxa{}, ClipDir: "/clips",
		Budget: func() int { return 10 },
	})

	segs := twoSegments()
	pass := s.ground(context.Background(), StoreClip{Clip: Clip{Path: "reel.mp4"}}, segs)
	if pass.Looked != 0 || pass.Pending != 2 {
		t.Fatalf("provider failure pass = %+v, want no consumed segments", pass)
	}

	if got := AutoConfirmable(SplitProposal{Segments: segs}, gatePolicy(), 0).Verdict(); got != RejectUntagged {
		t.Fatalf("after a model failure = %q, want %q", got, RejectUntagged)
	}
}

// Vision switched off ⇒ budget 0 ⇒ the model is never asked, and nothing auto-confirms. An
// operator who turned vision off did not ask for it back on a different rung.
func TestSegmentVision_ZeroBudgetAsksNothing(t *testing.T) {
	tools := &spanTools{frames: [][]byte{[]byte("\xff\xd8jpeg\xff\xd9")}}
	model := &fixedVision{answer: `{"category":"toys","visibleText":"TOYS R US"}`}
	s := NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools: tools, Provider: model, Taxa: seedTaxa{}, ClipDir: "/clips",
		Budget: func() int { return 0 },
	})

	segs := twoSegments()
	s.ground(context.Background(), StoreClip{Clip: Clip{Path: "reel.mp4"}}, segs)

	if model.calls != 0 {
		t.Errorf("model called %d times with vision off, want 0", model.calls)
	}
	if got := AutoConfirmable(SplitProposal{Segments: segs}, gatePolicy(), 0).Verdict(); got != RejectUntagged {
		t.Fatalf("with vision off = %q, want %q", got, RejectUntagged)
	}
}

func TestSegmentVision_ResumesAfterTheCheckedBatch(t *testing.T) {
	tools := &spanTools{frames: [][]byte{[]byte("\xff\xd8jpeg\xff\xd9")}}
	model := &fixedVision{answer: `{"category":"toys","visibleText":"TOYS R US"}`}
	s := NewSplitStage(nil, nil).WithSegmentVision(&SegmentVision{
		Tools: tools, Provider: model, Taxa: seedTaxa{}, ClipDir: "/clips",
		Budget: func() int { return 1 },
	})
	segs := twoSegments()

	first := s.ground(context.Background(), StoreClip{Clip: Clip{Path: "reel.mp4"}}, segs)
	if first.Looked != 1 || first.Pending != 1 || !segs[0].Looked || segs[1].Looked {
		t.Fatalf("first batch = %+v segments %+v", first, segs)
	}

	second := s.ground(context.Background(), StoreClip{Clip: Clip{Path: "reel.mp4"}}, segs)
	if second.Looked != 1 || second.Pending != 0 || model.calls != 2 {
		t.Fatalf("second batch = %+v calls %d", second, model.calls)
	}
	want := [][2]int64{{0, 30_000}, {30_000, 61_000}}
	if len(tools.spans) != 2 || tools.spans[0] != want[0] || tools.spans[1] != want[1] {
		t.Fatalf("resumed spans = %v, want %v", tools.spans, want)
	}
}

func TestDiscardDeterministic_RemovesDuplicatesAndBelowFloorOnly(t *testing.T) {
	segs := []SplitSegment{
		{Index: 0, StartMs: 0, EndMs: 30_000, DupOf: "existing.mp4"},
		{Index: 1, StartMs: 30_000, EndMs: 34_000},
		{Index: 2, StartMs: 34_000, EndMs: 80_000, Unsplittable: true},
	}

	kept, discarded := discardDeterministic(segs, 10*time.Second)
	if discarded.duplicates != 1 || discarded.short != 1 {
		t.Fatalf("discarded = %+v", discarded)
	}
	if len(kept) != 1 || kept[0].Index != 0 || !kept[0].Unsplittable {
		t.Fatalf("kept = %+v; an ambiguous span must remain for review", kept)
	}
}

func TestSplitStage_ResumesOnlyReviewsItCanImprove(t *testing.T) {
	queue := proposalQueue{proposals: []SplitProposal{
		{ClipHash: "duplicate", Segments: []SplitSegment{{StartMs: 0, EndMs: 30_000, DupOf: "old.mp4"}}},
		{ClipHash: "short", Segments: []SplitSegment{{StartMs: 0, EndMs: 4_000}}},
		{ClipHash: "unchecked", Segments: []SplitSegment{{StartMs: 0, EndMs: 30_000}}},
		{ClipHash: "already-ambiguous", Segments: []SplitSegment{{StartMs: 0, EndMs: 30_000, Looked: true}}},
	}}
	s := NewSplitStage(nil, queue).
		WithAutoConfirm(*gatePolicy(), func() time.Duration { return 10 * time.Second }).
		WithSegmentVision(&SegmentVision{
			Tools: &spanTools{}, Provider: &fixedVision{}, Budget: func() int { return 1 },
		})

	hashes, err := s.resumableReviewHashes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{"duplicate", "short", "unchecked"} {
		if _, ok := hashes[hash]; !ok {
			t.Errorf("%q was not selected for automatic refinement", hash)
		}
	}
	if _, ok := hashes["already-ambiguous"]; ok {
		t.Error("a segment the model already examined was requeued; it genuinely needs review")
	}
}

type pendingStructureShadow struct {
	pending map[string]bool
}

func (s pendingStructureShadow) NeedsStructureSplitObservation(_ context.Context, proposal SplitProposal) (bool, error) {
	return s.pending[proposal.ClipHash], nil
}

func (pendingStructureShadow) ObserveStructureSplit(context.Context, SplitProposal, SplitPartition) error {
	return nil
}

func TestSplitStageResumesExistingReviewForOneMissingShadowObservation(t *testing.T) {
	queue := proposalQueue{proposals: []SplitProposal{
		{ClipHash: "unobserved", Segments: []SplitSegment{{StartMs: 0, EndMs: 30_000, Looked: true}}},
		{ClipHash: "observed", Segments: []SplitSegment{{StartMs: 0, EndMs: 30_000, Looked: true}}},
	}}
	stage := NewSplitStage(nil, queue).WithStructureShadow(pendingStructureShadow{
		pending: map[string]bool{"unobserved": true},
	})

	hashes, err := stage.resumableReviewHashes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hashes["unobserved"]; !ok {
		t.Fatal("unobserved existing proposal was not requeued")
	}
	if _, ok := hashes["observed"]; ok {
		t.Fatal("observed proposal was requeued again")
	}
}
