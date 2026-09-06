package filler_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
)

// --- doubles -----------------------------------------------------------------

// pipeMemStore is an in-memory PipelineStore + ClipStore.
//
// ⚠ Keyed by HASH on both halves, exactly as the real store is. The split fixtures keyed clips by
// PATH and hid two shipped bugs for two phases (§10 V51a); a double that indexes differently from
// the thing it stands in for cannot see key confusion by construction.
type pipeMemStore struct {
	clips     map[string]filler.StoreClip
	rows      map[string]filler.ClipPipeline
	proposals []filler.SplitProposal
	removed   []string
	held      []string
	holdErr   error
}

func (m *pipeMemStore) SetClipsHeld(_ context.Context, paths []string, held, autoFiled bool, _ time.Time) (int, error) {
	if m.holdErr != nil {
		return 0, m.holdErr
	}
	n := 0
	for _, p := range paths {
		for hash, c := range m.clips {
			if c.Path == p {
				c.Held, c.AutoFiled = held, autoFiled
				m.clips[hash] = c
				n++
			}
		}
	}
	m.held = append(m.held, paths...)
	return n, nil
}

func (m *pipeMemStore) SetClipLanguage(context.Context, string, string, time.Time) error {
	return nil
}
func (m *pipeMemStore) SetClipTranscript(context.Context, string, string, time.Time) error {
	return nil
}
func (m *pipeMemStore) ClearClipVisionTags(context.Context, string, time.Time) error {
	return nil
}
func (m *pipeMemStore) ListSplitProposals(context.Context) ([]filler.SplitProposal, error) {
	return append([]filler.SplitProposal(nil), m.proposals...), nil
}
func (m *pipeMemStore) DeleteSplitProposal(context.Context, string) error { return nil }

func newPipeMemStore() *pipeMemStore {
	return &pipeMemStore{clips: map[string]filler.StoreClip{}, rows: map[string]filler.ClipPipeline{}}
}

func (m *pipeMemStore) put(c filler.StoreClip) { m.clips[c.Hash] = c }

// SetClipsRemoved records the tombstone the RUNNER writes on a reject (§10 V51b).
//
// ⚠ Keyed by PATH while the clip map is keyed by HASH, exactly as the real store is — and the
// fixture deliberately derives one from the other rather than setting both to the same string.
// A fixture that equated them is what hid the V38c identity split for two releases.
func (m *pipeMemStore) SetClipsRemoved(_ context.Context, paths []string, at time.Time) (int, error) {
	n := 0
	for _, p := range paths {
		for hash, c := range m.clips {
			if c.Path == p {
				c.RemovedAt = at
				m.clips[hash] = c
				n++
			}
		}
	}
	m.removed = append(m.removed, paths...)
	return n, nil
}

func (m *pipeMemStore) GetClip(_ context.Context, id string) (filler.StoreClip, bool, error) {
	c, ok := m.clips[id]
	return c, ok, nil
}

func (m *pipeMemStore) ListPipelineWork(_ context.Context, now time.Time, limit int) ([]filler.ClipPipeline, error) {
	var out []filler.ClipPipeline
	for _, r := range m.rows {
		if r.Disposition != filler.DispositionRunning {
			continue
		}
		if r.NextRun.After(now) {
			continue
		}
		out = append(out, r)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *pipeMemStore) PipelineOverview(_ context.Context, at time.Time) (filler.PipelineOverview, error) {
	rows := make([]filler.ClipPipeline, 0, len(m.rows))
	for _, row := range m.rows {
		rows = append(rows, row)
	}
	return filler.SummarizePipelines(rows, at), nil
}

func (m *pipeMemStore) ListClipsWithoutPipeline(_ context.Context, limit int) ([]filler.StoreClip, error) {
	var out []filler.StoreClip
	for h, c := range m.clips {
		if _, ok := m.rows[h]; ok {
			continue
		}
		out = append(out, c)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *pipeMemStore) GetClipPipeline(_ context.Context, hash string) (filler.ClipPipeline, bool, error) {
	r, ok := m.rows[hash]
	return r, ok, nil
}

func (m *pipeMemStore) ListClipPipelines(_ context.Context, f filler.PipelineFilter) ([]filler.ClipPipeline, error) {
	var out []filler.ClipPipeline
	for _, r := range m.rows {
		if f.ConveyorOnly && r.Disposition != filler.DispositionRunning && r.Disposition != filler.DispositionReview {
			continue
		}
		if f.RejectedOnly && r.Disposition != filler.DispositionRejected {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// ⚠ **It HONOURS cancellation, and that is the point** (§10 V51g). A fake that ignored `ctx` is
// why the original bug was invisible: `onFailure` computed the failure record, the backoff and the
// `MaxAttempts` resolution, then wrote them through the very context whose expiry caused the
// failure — and against a real store every one of those writes was silently discarded. With a fake
// that accepts anything, that code path looks perfect. Rejecting a dead context here is what makes
// the detached-write fix provable rather than asserted.
func (m *pipeMemStore) UpsertClipPipeline(ctx context.Context, p filler.ClipPipeline) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.rows[p.ClipHash] = p
	return nil
}

func (m *pipeMemStore) RetryClipPipeline(ctx context.Context, _ filler.ClipPipeline, p filler.ClipPipeline, restore bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.rows[p.ClipHash] = p
	if restore {
		c := m.clips[p.ClipHash]
		c.RemovedAt, c.Held, c.AutoFiled = time.Time{}, true, false
		m.clips[p.ClipHash] = c
	}
	return nil
}

// fakeStage is a scriptable rung.
type fakeStage struct {
	id      filler.StageID
	cost    filler.StageCost
	applies bool
	note    string
	result  filler.StageResult
	err     error
	panicV  any
	runs    int
	killCtx func()
}

func (f *fakeStage) ID() filler.StageID     { return f.id }
func (f *fakeStage) Cost() filler.StageCost { return f.cost }
func (f *fakeStage) Applies(context.Context, filler.StoreClip) (bool, string) {
	return f.applies, f.note
}
func (f *fakeStage) Run(ctx context.Context, _ filler.StoreClip) (filler.StageResult, error) {
	f.runs++
	if f.panicV != nil {
		panic(f.panicV)
	}
	// killCtx models the rung whose work outlives the pass: the deadline lands mid-exec, the
	// child process dies, and the stage reports the context error — exactly what ffmpeg, whisper
	// and an LLM turn all do when the budget ends underneath them.
	if f.killCtx != nil {
		f.killCtx()
		return filler.StageResult{}, ctx.Err()
	}
	return f.result, f.err
}

// A rung panic must fail ONE clip, not abort the scheduled pass and leave that row permanently
// `running`. The scheduler has its own last-resort recovery, but it is too far out to persist the
// clip's retry/backoff state or let work behind it advance.
func TestPipeline_StagePanicBecomesARecoverableClipFailure(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()
	stages[filler.StageTag].panicV = "classifier exploded"

	p := newPipe(st, asSlice(stages), filler.DefaultBudget())
	if err := p.Advance(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}

	row := st.rows["c1"]
	if row.Stage != filler.StageTag || row.Status != filler.StatusFailed || row.Attempts != 1 {
		t.Fatalf("panic state = %q/%q attempts=%d, want tag/failed attempts=1", row.Stage, row.Status, row.Attempts)
	}
	if row.NextRun.IsZero() {
		t.Error("panic did not receive ordinary failure backoff")
	}
	if got := row.Stages[len(row.Stages)-1].Note; got != "stage tag panicked: classifier exploded" {
		t.Fatalf("panic note = %q", got)
	}
}

func TestPipeline_AuthorityFailureNeverFallsThroughToLegacyFiling(t *testing.T) {
	for _, blockedStage := range []filler.StageID{filler.StageScreen, filler.StageAdmission} {
		t.Run(string(blockedStage), func(t *testing.T) {
			st := newPipeMemStore()
			seedEnrolled(st, "c1")
			stages := allStages()
			stages[blockedStage].err = errors.New("authority store unavailable")

			p := newPipe(st, asSlice(stages), filler.DefaultBudget())
			for range filler.MaxAttempts + 1 {
				if err := p.Advance(context.Background(), "c1"); err != nil {
					t.Fatal(err)
				}
			}

			row := st.rows["c1"]
			if row.Stage != blockedStage || row.Status != filler.StatusFailed ||
				row.Disposition != filler.DispositionRunning || row.Attempts != filler.MaxAttempts {
				t.Fatalf("authority failure = %q/%q disposition=%q attempts=%d",
					row.Stage, row.Status, row.Disposition, row.Attempts)
			}
			if stages[filler.StageScore].runs != 0 {
				t.Fatalf("legacy score ran %d times without stage %q authority", stages[filler.StageScore].runs, blockedStage)
			}
		})
	}
}

func stage(id filler.StageID) *fakeStage {
	return &fakeStage{id: id, applies: true, result: filler.StageResult{Verdict: filler.VerdictContinue}}
}

// allStages returns a passing stage for every rung, so a test can replace just the one it cares
// about and still exercise a complete ladder.
func allStages() map[filler.StageID]*fakeStage {
	out := map[filler.StageID]*fakeStage{}
	for _, id := range filler.StageOrder {
		out[id] = stage(id)
	}
	return out
}

func asSlice(m map[filler.StageID]*fakeStage) []filler.Stage {
	var out []filler.Stage
	for _, id := range filler.StageOrder {
		out = append(out, m[id])
	}
	return out
}

func seedEnrolled(st *pipeMemStore, hash string) {
	st.put(filler.StoreClip{Clip: filler.Clip{Hash: hash, Path: "a/b/" + hash + ".mp4", Name: hash}})
	st.rows[hash] = filler.ClipPipeline{
		ClipHash: hash, Stage: filler.StageProbe, Status: filler.StatusQueued,
		Disposition: filler.DispositionRunning,
	}
}

func newPipe(st *pipeMemStore, stages []filler.Stage, b filler.Budget) *filler.Pipeline {
	at := time.Unix(1_800_000_000, 0).UTC()
	return filler.NewPipeline(st, st, stages, b, nil, func() time.Time { return at }, nil)
}

// --- tests -------------------------------------------------------------------

// A clip that clears every rung ends FILED, having visited each stage exactly once.
func TestPipeline_WalksEveryStageAndFiles(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()

	if _, err := newPipe(st, asSlice(stages), filler.DefaultBudget()).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	row := st.rows["c1"]
	if row.Disposition != filler.DispositionFiled {
		t.Fatalf("disposition = %q, want filed", row.Disposition)
	}
	if len(row.Stages) != len(filler.StageOrder) {
		t.Errorf("ladder has %d rungs, want %d — every stage must be recorded, including the boring ones",
			len(row.Stages), len(filler.StageOrder))
	}
	for id, s := range stages {
		if s.runs != 1 {
			t.Errorf("stage %s ran %d times, want 1", id, s.runs)
		}
	}
}

func TestPipeline_RewindResetsOnlyTheRequestedSuffix(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "stuck")
	row := st.rows["stuck"]
	row.Stage = filler.StageScore
	row.Status = filler.StatusDone
	row.Attempts = 3
	row.Disposition = filler.DispositionReview
	for _, id := range filler.StageOrder {
		row.Stages = append(row.Stages, filler.StageRecord{Stage: id, Status: filler.StatusDone})
	}
	st.rows["stuck"] = row

	p := newPipe(st, nil, filler.DefaultBudget()).WithRewind(st, "")
	if err := p.Rewind(context.Background(), "stuck", filler.StageTag, false); err != nil {
		t.Fatal(err)
	}
	got := st.rows["stuck"]
	if got.Stage != filler.StageTag || got.Status != filler.StatusQueued || got.Attempts != 0 || got.Disposition != filler.DispositionRunning {
		t.Fatalf("rewound row = %+v, want tag/queued/running with fresh attempts", got)
	}
	if !got.ForceRun {
		t.Fatal("rewind did not persist the explicit rerun instruction")
	}
	if len(got.Stages) != filler.StageIndex(filler.StageTag) {
		t.Fatalf("kept %d stages, want only %d stages before tag", len(got.Stages), filler.StageIndex(filler.StageTag))
	}
}

func TestPipeline_RewindRunsAStageThatWouldNormallySkip(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "already-tagged")
	row := st.rows["already-tagged"]
	row.Stage = filler.StageScore
	row.Status = filler.StatusDone
	row.Disposition = filler.DispositionReview
	st.rows["already-tagged"] = row

	tag := stage(filler.StageTag)
	tag.applies = false
	tag.note = "already fully tagged"
	p := newPipe(st, []filler.Stage{tag}, filler.DefaultBudget()).WithRewind(st, "")
	if err := p.Rewind(context.Background(), "already-tagged", filler.StageTag, false); err != nil {
		t.Fatal(err)
	}
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tag.runs != 1 {
		t.Fatalf("tag runs = %d, want 1 after an explicit rewind", tag.runs)
	}
	if st.rows["already-tagged"].ForceRun {
		t.Fatal("forced rerun marker survived the requested stage")
	}
}

func TestPipeline_RewindTranscodeRequiresExplicitForce(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "encoded")
	p := newPipe(st, nil, filler.DefaultBudget()).WithRewind(st, "")
	before := st.rows["encoded"]
	if err := p.Rewind(context.Background(), "encoded", filler.StageTranscode, false); !errors.Is(err, filler.ErrTranscodeNeedsForce) {
		t.Fatalf("rewind transcode = %v, want ErrTranscodeNeedsForce", err)
	}
	if got := st.rows["encoded"]; got.Stage != before.Stage || got.Status != before.Status {
		t.Errorf("refused rewind mutated row: before=%+v after=%+v", before, got)
	}
}

func TestPipeline_RetryFailureRestoresTerminalExecutionFailureHeld(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "broken-encode")
	c := st.clips["broken-encode"]
	c.RemovedAt, c.Held, c.AutoFiled = time.Now().UTC(), false, true
	st.clips[c.Hash] = c
	row := st.rows[c.Hash]
	row.Stage, row.Status = filler.StageTranscode, filler.StatusFailed
	row.Attempts, row.Disposition = filler.MaxAttempts, filler.DispositionRejected
	row.RejectReason, row.RejectDetail = filler.ReasonUnplayable, "ffmpeg exited 1"
	row.Stages = []filler.StageRecord{
		{Stage: filler.StageProbe, Status: filler.StatusDone},
		{Stage: filler.StageTranscode, Status: filler.StatusFailed},
	}
	st.rows[c.Hash] = row

	p := newPipe(st, nil, filler.DefaultBudget()).WithRewind(st, "")
	if err := p.RetryFailure(context.Background(), c.Hash); err != nil {
		t.Fatal(err)
	}
	got := st.rows[c.Hash]
	if got.Stage != filler.StageTranscode || got.Status != filler.StatusQueued ||
		got.Disposition != filler.DispositionRunning || got.Attempts != 0 || !got.ForceRun {
		t.Fatalf("retried row = %+v, want transcode/queued/running with force", got)
	}
	if len(got.Stages) != 1 || got.Stages[0].Stage != filler.StageProbe {
		t.Fatalf("retry discarded completed upstream work: %+v", got.Stages)
	}
	clip := st.clips[c.Hash]
	if !clip.RemovedAt.IsZero() || !clip.Held || clip.AutoFiled {
		t.Fatalf("restored clip = removed:%v held:%v auto:%v, want present and held", clip.RemovedAt, clip.Held, clip.AutoFiled)
	}
}

func TestPipeline_RetryFailureRefusesContentDecision(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "silent")
	row := st.rows["silent"]
	row.Stage, row.Status = filler.StageTranscode, filler.StatusDone
	row.Disposition, row.RejectReason = filler.DispositionRejected, filler.ReasonSilentContent
	st.rows[row.ClipHash] = row

	err := newPipe(st, nil, filler.DefaultBudget()).WithRewind(st, "").RetryFailure(context.Background(), row.ClipHash)
	if !errors.Is(err, filler.ErrPipelineNotRetryable) {
		t.Fatalf("retry content decision = %v, want ErrPipelineNotRetryable", err)
	}
	if got := st.rows[row.ClipHash]; got.Disposition != filler.DispositionRejected {
		t.Fatalf("refused retry mutated row: %+v", got)
	}
}

func TestPipeline_RequeuesFiledLegacyMezzanineForQualityWithoutSpendingPastBudget(t *testing.T) {
	dir := t.TempDir()
	st := newPipeMemStore()
	hash := "legacy-quality"
	path := "aa/bb/legacy.mp4"
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("mezzanine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filler.WriteSidecarTags(full, filler.SidecarTags{
		Mezzanine: "h264-crf20-aac192",
	}, false); err != nil {
		t.Fatal(err)
	}
	st.put(filler.StoreClip{Clip: filler.Clip{Hash: hash, Path: path, Name: "Legacy advert"}})
	st.rows[hash] = filler.ClipPipeline{
		ClipHash: hash, Stage: filler.StageScore, Status: filler.StatusDone,
		Disposition: filler.DispositionFiled,
		Stages:      []filler.StageRecord{{Stage: filler.StageProbe, Status: filler.StatusDone}},
	}
	transcode := stage(filler.StageTranscode)
	transcode.cost = filler.CostTranscode
	p := newPipe(st, []filler.Stage{transcode}, filler.Budget{
		MaxTranscodes: func() int { return 0 },
	}).WithRewind(st, dir)

	res, err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	row := st.rows[hash]
	if res.Requeued != 1 || row.Stage != filler.StageTranscode || row.Disposition != filler.DispositionRunning {
		t.Fatalf("result/row = %+v / %+v, want queued quality backfill", res, row)
	}
	if transcode.runs != 0 {
		t.Error("quality backfill ignored the transcode budget")
	}
}

func TestPipeline_RepairsFiledCompositeHeldByOlderConfirm(t *testing.T) {
	st := newPipeMemStore()
	const hash = "legacy-confirmed-reel"
	st.put(filler.StoreClip{Clip: filler.Clip{
		Hash: hash, Path: "reels/legacy-confirmed.mp4", Name: "Legacy confirmed reel",
		IsComposite: true, Held: true,
	}})
	st.rows[hash] = filler.ClipPipeline{
		ClipHash: hash, Stage: filler.StageSplit, Status: filler.StatusDone,
		Disposition: filler.DispositionFiled,
	}

	res, err := newPipe(st, nil, filler.DefaultBudget()).WithRewind(st, "").RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Repaired != 1 {
		t.Fatalf("pipeline reported %d repaired composites, want 1", res.Repaired)
	}
	if got := st.clips[hash]; got.Held {
		t.Fatalf("filed composite remains held after compatibility pass: %+v", got)
	}
}

func TestPipeline_DoesNotFileCompositeWithSplitProposalStillWaiting(t *testing.T) {
	st := newPipeMemStore()
	const hash = "partly-confirmed-reel"
	st.put(filler.StoreClip{Clip: filler.Clip{
		Hash: hash, Path: "reels/partly-confirmed.mp4", Name: "Partly confirmed reel",
		IsComposite: true, Held: true,
	}})
	st.rows[hash] = filler.ClipPipeline{
		ClipHash: hash, Stage: filler.StageSplit, Status: filler.StatusDone,
		Disposition: filler.DispositionFiled,
	}
	st.proposals = []filler.SplitProposal{{
		ID: "proposal-with-leftovers", ClipHash: hash,
		Segments: []filler.SplitSegment{{Index: 0, StartMs: 0, EndMs: 30_000}},
	}}

	if _, err := newPipe(st, nil, filler.DefaultBudget()).WithRewind(st, "").RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := st.clips[hash]; !got.Held {
		t.Fatalf("composite with a pending split proposal was filed: %+v", got)
	}
}

func TestPipeline_DoesNotFileCompositeStillWaitingForDecision(t *testing.T) {
	st := newPipeMemStore()
	const hash = "review-reel"
	st.put(filler.StoreClip{Clip: filler.Clip{
		Hash: hash, Path: "reels/review.mp4", Name: "Review reel",
		IsComposite: true, Held: true,
	}})
	st.rows[hash] = filler.ClipPipeline{
		ClipHash: hash, Stage: filler.StageSplit, Status: filler.StatusDone,
		Disposition: filler.DispositionReview,
	}

	if _, err := newPipe(st, nil, filler.DefaultBudget()).WithRewind(st, "").RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := st.clips[hash]; !got.Held {
		t.Fatalf("composite still waiting for a decision was filed: %+v", got)
	}
}

func TestPipeline_ReviewHoldsAPreviouslyFiledClipOutOfRotation(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "quality-review")
	row := st.rows["quality-review"]
	row.Stage = filler.StageTranscode
	st.rows["quality-review"] = row
	quality := stage(filler.StageTranscode)
	quality.result = filler.StageResult{
		Verdict: filler.VerdictReview,
		Note:    "The picture is unchanged for 12.0s; this may be intentional.",
	}

	if _, err := newPipe(st, []filler.Stage{quality}, filler.DefaultBudget()).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	clip := st.clips["quality-review"]
	if !clip.Held || len(st.held) != 1 {
		t.Fatalf("reviewed clip remained airable: clip=%+v held writes=%v", clip, st.held)
	}
}

func TestPipeline_ReviewIsRetriedWhenTheAirabilityHoldFails(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "quality-review-retry")
	row := st.rows["quality-review-retry"]
	row.Stage = filler.StageTranscode
	st.rows["quality-review-retry"] = row
	quality := stage(filler.StageTranscode)
	quality.result = filler.StageResult{Verdict: filler.VerdictReview, Note: "picture unchanged"}
	pipe := newPipe(st, []filler.Stage{quality}, filler.DefaultBudget())

	st.holdErr = errors.New("store unavailable")
	res, err := pipe.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || st.rows["quality-review-retry"].Disposition != filler.DispositionRunning {
		t.Fatalf("failed hold became terminal: result=%+v row=%+v", res, st.rows["quality-review-retry"])
	}

	st.holdErr = nil
	res, err = pipe.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Completed != 1 || st.rows["quality-review-retry"].Disposition != filler.DispositionReview {
		t.Fatalf("review did not recover: result=%+v row=%+v", res, st.rows["quality-review-retry"])
	}
	if !st.clips["quality-review-retry"].Held || quality.runs != 2 {
		t.Fatalf("hold was not retried: clip=%+v runs=%d", st.clips["quality-review-retry"], quality.runs)
	}
}

// A stage that does not apply is recorded as SKIPPED with its reason, and the clip advances.
//
// ⚠ The reason is the point. §10 makes transcription deliberately selective, so a stage that
// silently does not happen reads as broken — the same rule that makes a reached limit reported
// rather than silent.
func TestPipeline_SkipRecordsWhyAndAdvances(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()
	stages[filler.StageTranscribe].applies = false
	stages[filler.StageTranscribe].note = "the description already says enough"

	if _, err := newPipe(st, asSlice(stages), filler.DefaultBudget()).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if stages[filler.StageTranscribe].runs != 0 {
		t.Error("a stage that does not apply was executed anyway")
	}
	row := st.rows["c1"]
	if row.Disposition != filler.DispositionFiled {
		t.Errorf("a skipped stage stranded the clip: %q", row.Disposition)
	}
	var found bool
	for _, r := range row.Stages {
		if r.Stage == filler.StageTranscribe {
			found = true
			if r.Status != filler.StatusSkipped || r.Note != "the description already says enough" {
				t.Errorf("skip recorded as %+v — the reason must survive", r)
			}
		}
	}
	if !found {
		t.Error("the skipped stage is missing from the ladder entirely")
	}
}

// A rejecting stage stops the clip with its CODE and its measured detail, and nothing downstream runs.
func TestPipeline_RejectStopsTheClipWithItsReason(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()
	stages[filler.StageProbe].result = filler.StageResult{
		Verdict: filler.VerdictReject, Reason: filler.ReasonTooShort, Detail: "8.2s; the floor is 10.0s",
	}

	if _, err := newPipe(st, asSlice(stages), filler.DefaultBudget()).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	row := st.rows["c1"]
	if row.Disposition != filler.DispositionRejected {
		t.Fatalf("disposition = %q, want rejected", row.Disposition)
	}
	if row.RejectReason != filler.ReasonTooShort || row.RejectDetail != "8.2s; the floor is 10.0s" {
		t.Errorf("reject lost its reason/detail: %q / %q", row.RejectReason, row.RejectDetail)
	}
	if stages[filler.StageScore].runs != 0 {
		t.Error("a rejected clip carried on through the rest of the pipeline")
	}
	assertRowAgreesWithItsLadder(t, row)
}

// assertRowAgreesWithItsLadder checks the one invariant that ties `Status` to the ladder: the row's
// Status IS whatever its current rung recorded.
//
// ⚠ **This is asserted rather than the literal value `done`, because the value is not the point.**
// A verdict path that records `failed` and a verdict path that records `done` are both correct; a
// row whose Status says `running` while its own entry for the SAME rung says otherwise is not, and
// that disagreement is what shipped. Checking the relationship catches every future verdict path,
// including ones with a status neither of these tests names.
func assertRowAgreesWithItsLadder(t *testing.T, row filler.ClipPipeline) {
	t.Helper()
	for _, rung := range row.Stages {
		if rung.Stage != row.Stage {
			continue
		}
		if row.Status != rung.Status {
			t.Errorf("row says %s/%q but its own ladder entry for %s says %q — one row disagreeing with itself",
				row.Stage, row.Status, rung.Stage, rung.Status)
		}
		return
	}
	t.Errorf("the current rung %q has no ladder entry at all", row.Stage)
}

// ⚠ A clip handed to a PERSON must stop looking like work in progress.
//
// `Disposition` is the clip's outcome and `Status` is the current rung's — and the verdict paths
// set the first, recorded the rung, and left the second at the `running` written on entry. Nothing
// functional broke (every store predicate keys on `disposition`), so the suite stayed green; what
// broke was the picture. `ClipPipeline.resolve` on the frontend prefers `row.stage`/`row.status`
// over the visited ladder — correctly, since a rung mid-run has no entry yet — so the pip pulsed
// "in progress" forever and the rung's note was never rendered. Measured live (§10 V51g): eight
// reels, WAGA-5 among them, every one finished within seconds and every one drawn as still working.
func TestPipeline_ReviewStopsLookingLikeWorkInProgress(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()
	stages[filler.StageProbe].result = filler.StageResult{
		Verdict: filler.VerdictReview, Note: "a segment could not be classified",
	}

	if _, err := newPipe(st, asSlice(stages), filler.DefaultBudget()).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	row := st.rows["c1"]
	if row.Disposition != filler.DispositionReview {
		t.Fatalf("disposition = %q, want review", row.Disposition)
	}
	if row.Status == filler.StatusRunning {
		t.Error("a clip waiting on a person still reports status=running — the belt draws it busy forever")
	}
	assertRowAgreesWithItsLadder(t, row)
}

// ⚠ A backend failure says nothing about the CLIP, so a non-fatal stage that exhausts its retries
// SKIPS and the clip advances. A missing transcript must never strand a commercial.
func TestPipeline_NonFatalFailureSkipsRatherThanStranding(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()
	stages[filler.StageTranscribe].err = fmt.Errorf("no whisper on this machine")

	p := newPipe(st, asSlice(stages), filler.DefaultBudget())
	// Each pass burns one attempt; the clock is frozen, so drive it explicitly.
	for i := 0; i < filler.MaxAttempts; i++ {
		if err := p.Advance(context.Background(), "c1"); err != nil {
			t.Fatal(err)
		}
		st.rows["c1"] = resetSchedule(st.rows["c1"])
	}

	row := st.rows["c1"]
	if row.Disposition == filler.DispositionRejected {
		t.Fatal("a missing backend REJECTED the clip — that is a fact about the machine, not the file")
	}
	if row.Disposition != filler.DispositionFiled {
		t.Errorf("disposition = %q, want the clip to have carried on", row.Disposition)
	}
}

// ⚠ …but probe and transcode ARE facts about the file, so exhausting their retries rejects.
func TestPipeline_FatalFailureRejects(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()
	stages[filler.StageProbe].err = fmt.Errorf("moov atom not found")

	p := newPipe(st, asSlice(stages), filler.DefaultBudget())
	for i := 0; i < filler.MaxAttempts; i++ {
		if err := p.Advance(context.Background(), "c1"); err != nil {
			t.Fatal(err)
		}
		st.rows["c1"] = resetSchedule(st.rows["c1"])
	}

	row := st.rows["c1"]
	if row.Disposition != filler.DispositionRejected || row.RejectReason != filler.ReasonUnprobeable {
		t.Fatalf("unprobeable clip = %q / %q, want rejected/unprobeable", row.Disposition, row.RejectReason)
	}
	// ⚠ The fatal branch had the SAME omission as the verdict paths, one function away: it records
	// the rung `failed` and sets the disposition, and left Status at `running`.
	assertRowAgreesWithItsLadder(t, row)
}

// A failure short of the retry limit BACKS OFF rather than resolving — it stays on the same stage,
// scheduled into the future.
func TestPipeline_FailureBacksOffBeforeGivingUp(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()
	stages[filler.StageProbe].err = fmt.Errorf("transient")

	p := newPipe(st, asSlice(stages), filler.DefaultBudget())
	if err := p.Advance(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}

	row := st.rows["c1"]
	if row.Stage != filler.StageProbe || row.Status != filler.StatusFailed {
		t.Fatalf("after one failure = %q/%q, want it still on probe and failed", row.Stage, row.Status)
	}
	if row.NextRun.IsZero() {
		t.Error("no backoff was scheduled — the clip would be retried immediately, at full cost, forever")
	}
	if row.Disposition != filler.DispositionRunning {
		t.Errorf("one failure resolved the clip to %q; it has attempts left", row.Disposition)
	}
}

// ⚠ **THE bug this phase exists for, and it had no test at all** (§10 V51g). A rung whose work
// outlives the pass looped forever: `attempts` was incremented and saved BEFORE the work, the
// failure bookkeeping was written through the context that had just expired and was therefore
// discarded, and the row stayed `running` at whatever attempt count it had reached. Measured live:
// one 16m47s reel at **12 attempts against a MaxAttempts of 3**, re-doing the same first third
// every two minutes while the UI showed it advancing.
//
// Out of TIME is not failing. The clip goes back to `queued`, spends no attempt, takes no backoff,
// and resumes next pass.
func TestPipeline_ADeadlineDefersInsteadOfBurningAnAttempt(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stages[filler.StageProbe].killCtx = cancel

	err := newPipe(st, asSlice(stages), filler.DefaultBudget()).Advance(ctx, "c1")

	if !errors.Is(err, filler.ErrDeferred) {
		t.Fatalf("Advance = %v, want ErrDeferred — a deadline is not a failure", err)
	}
	row := st.rows["c1"]
	// ⚠ The write LANDED despite the dead context. That is the whole fix: before it, this row
	// still said `running` at attempt 1 because the save was made through the cancelled context.
	if row.Status != filler.StatusQueued {
		t.Errorf("status = %q, want queued — the row must resume, not sit in `running` forever", row.Status)
	}
	if row.Attempts != 0 {
		t.Errorf("attempts = %d, want 0 — the clip did nothing wrong; the pass ran out of time", row.Attempts)
	}
	// ⚠ It YIELDS. Found by watching the fix starve the queue: the work list is oldest-first, so a
	// clip that cannot fit a pass took the whole budget again on the next one, and 84 other clips
	// were never reached. Not a penalty — no attempt was spent — but it goes behind work that can
	// finish. A ZERO NextRun here is the starvation bug, which is why this asserts the opposite of
	// what a failure-backoff test would.
	if !row.NextRun.After(row.UpdatedAt) {
		t.Error("a deferred clip is due immediately; it will take the next pass too, and the queue behind it starves")
	}
	if row.Disposition != filler.DispositionRunning {
		t.Errorf("disposition = %q, want running", row.Disposition)
	}
}

func TestPipeline_AStageCanYieldAPersistedBatchWithoutFailing(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()
	stages[filler.StageProbe].err = filler.ErrDeferred

	err := newPipe(st, asSlice(stages), filler.DefaultBudget()).Advance(context.Background(), "c1")
	if !errors.Is(err, filler.ErrDeferred) {
		t.Fatalf("Advance = %v, want ErrDeferred", err)
	}
	row := st.rows["c1"]
	if row.Stage != filler.StageProbe || row.Status != filler.StatusQueued {
		t.Fatalf("yielded row = %q/%q, want probe/queued", row.Stage, row.Status)
	}
	if row.Attempts != 0 {
		t.Errorf("attempts = %d, want 0 — a completed batch is progress, not a failure", row.Attempts)
	}
	if !row.NextRun.After(row.UpdatedAt) {
		t.Error("a yielded batch did not give the rest of the queue a turn")
	}
}

// …and the run summary says DEFERRED, not failed. A reel too slow for one pass produced an
// identical WARN every two minutes forever, and the count blamed the clip for the budget.
func TestPipeline_ADeferralIsNotCountedAsAFailure(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stages[filler.StageProbe].killCtx = cancel

	res, err := newPipe(st, asSlice(stages), filler.DefaultBudget()).RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if res.Failed != 0 {
		t.Errorf("failed = %d, want 0 — running out of time is not the clip failing", res.Failed)
	}
	if res.Deferred != 1 {
		t.Errorf("deferred = %d, want 1", res.Deferred)
	}
}

// Budget exhaustion DEFERS: the clip stays queued on the stage it could not afford, so the next
// pass resumes exactly there rather than skipping the expensive rung.
func TestPipeline_BudgetExhaustionDefersRatherThanSkips(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()
	stages[filler.StageTranscribe].cost = filler.CostWhisper

	b := filler.DefaultBudget()
	// ⚠ ZERO, not nil. A nil closure means "use the default"; zero means "none may be spent", which
	// is how an operator turns an expensive stage off on a busy box.
	b.MaxWhisper = func() int { return 0 }
	if _, err := newPipe(st, asSlice(stages), b).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	row := st.rows["c1"]
	if stages[filler.StageTranscribe].runs != 0 {
		t.Error("an unaffordable stage ran anyway")
	}
	if row.Stage != filler.StageTranscribe || row.Status != filler.StatusQueued {
		t.Fatalf("deferred clip = %q/%q, want it queued on transcribe", row.Stage, row.Status)
	}
	if row.Disposition != filler.DispositionRunning {
		t.Errorf("running out of budget resolved the clip to %q — it is not finished, it is waiting",
			row.Disposition)
	}
}

// Clips a stage CREATES (split segments) are enrolled at the start of the pipeline, so each one
// runs the whole ladder itself rather than inheriting its parent's progress.
func TestPipeline_SpawnedClipsAreEnrolledFromTheStart(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "reel")
	st.put(filler.StoreClip{Clip: filler.Clip{Hash: "seg1", Path: "a/b/seg1.mp4"}})
	stages := allStages()
	stages[filler.StageSplit].result = filler.StageResult{
		Verdict: filler.VerdictContinue, Spawned: []string{"seg1"},
	}

	if err := newPipe(st, asSlice(stages), filler.DefaultBudget()).Advance(context.Background(), "reel"); err != nil {
		t.Fatal(err)
	}

	seg, ok := st.rows["seg1"]
	if !ok {
		t.Fatal("a spawned segment was never enrolled — it would sit in the catalog untagged forever")
	}
	if seg.Stage != filler.StageProbe || seg.Disposition != filler.DispositionRunning {
		t.Errorf("spawned segment = %q/%q, want probe/running", seg.Stage, seg.Disposition)
	}
}

// A byte-changing stage returns the replacement identity after atomically re-keying storage.
// The runner must follow it: persisting the old hash recreates an orphan pipeline row, and the
// next pass rejects it as a clip that vanished.
func TestPipeline_FollowsAStageIdentityReplacement(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "old-hash")
	stages := allStages()
	replacement := st.clips["old-hash"]
	replacement.Hash = "new-hash"
	replacement.Path = "aa/bb/new-hash.mp4"
	stages[filler.StageTranscode].result = filler.StageResult{
		Clip: replacement, Verdict: filler.VerdictContinue,
	}

	if err := newPipe(st, asSlice(stages), filler.DefaultBudget()).Advance(context.Background(), "old-hash"); err != nil {
		t.Fatal(err)
	}
	row, ok := st.rows["new-hash"]
	if !ok {
		t.Fatal("pipeline persisted the transformed clip under its old content identity")
	}
	if row.ClipHash != "new-hash" || row.Disposition != filler.DispositionFiled {
		t.Errorf("replacement pipeline row = %+v", row)
	}
}

// A terminal clip is not picked up again — otherwise every pass would re-run the whole ladder over
// a catalog that is already done.
func TestPipeline_TerminalClipsAreNotReWorked(t *testing.T) {
	st := newPipeMemStore()
	seedEnrolled(st, "c1")
	stages := allStages()
	p := newPipe(st, asSlice(stages), filler.DefaultBudget())

	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := stages[filler.StageScore].runs
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stages[filler.StageScore].runs != before {
		t.Errorf("a finished clip was worked again (%d → %d)", before, stages[filler.StageScore].runs)
	}
}

// Enrolment is lazy: a catalogued clip with no row gets one, at the start.
func TestPipeline_EnrolsCataloguedClips(t *testing.T) {
	st := newPipeMemStore()
	st.put(filler.StoreClip{Clip: filler.Clip{Hash: "c1", Path: "a/b/c1.mp4"}})

	res, err := newPipe(st, asSlice(allStages()), filler.DefaultBudget()).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Enrolled != 1 {
		t.Fatalf("enrolled %d, want 1", res.Enrolled)
	}
	if _, ok := st.rows["c1"]; !ok {
		t.Error("the clip was counted as enrolled but has no row")
	}
}

func TestPipeline_EnrolmentCarriesAcquisitionFromSidecar(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join("ab", "clip.mp4")
	media := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(media), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(media, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filler.WriteSidecarTags(media, filler.SidecarTags{AcquisitionID: "acq-17"}, false); err != nil {
		t.Fatal(err)
	}
	st := newPipeMemStore()
	st.put(filler.StoreClip{Clip: filler.Clip{Hash: "c1", Path: rel}})

	if _, err := newPipe(st, nil, filler.DefaultBudget()).WithRewind(st, dir).EnrolMissing(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := st.rows["c1"].AcquisitionID; got != "acq-17" {
		t.Fatalf("pipeline acquisition = %q, want acq-17", got)
	}
}

// Download completion needs the cheap half of RunOnce: make the clip visible on the durable
// conveyor without unexpectedly spending a transcode/Whisper budget on an unrelated backlog.
func TestPipeline_EnrolMissingDoesNotRunAStage(t *testing.T) {
	st := newPipeMemStore()
	st.put(filler.StoreClip{Clip: filler.Clip{Hash: "c1", Path: "a/b/c1.mp4"}})
	stages := allStages()

	n, err := newPipe(st, asSlice(stages), filler.DefaultBudget()).EnrolMissing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("enrolled %d, want 1", n)
	}
	if stages[filler.StageProbe].runs != 0 {
		t.Fatalf("probe ran %d times; enrol-only nudge must not execute stages", stages[filler.StageProbe].runs)
	}
	if row := st.rows["c1"]; row.Stage != filler.StageProbe || row.Status != filler.StatusQueued {
		t.Fatalf("pipeline row = %+v, want probe/queued", row)
	}
}

// resetSchedule clears the backoff so a test can drive consecutive attempts against a frozen clock.
func resetSchedule(r filler.ClipPipeline) filler.ClipPipeline {
	r.NextRun = time.Time{}
	return r
}
