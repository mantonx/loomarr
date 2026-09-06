package filler_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/mediatools"
	"github.com/loomarr/loomarr/internal/taxonomy"
	"github.com/loomarr/loomarr/internal/testkit"
)

// fakeTools scripts MediaTools per test (§19 — unit tests never exec a binary).
type fakeTools struct {
	chapters      []filler.Chapter
	blacks        []filler.Interval
	silences      []filler.Interval
	transcripts   map[string][]filler.TranscriptSegment // "start:end" → utterances
	transcribeErr error
	grayFrames    map[string][][]byte // "path|start|end" → frames
	keyframes     map[string][][]byte // basename → JPEG frames (V44 vision/heuristic input)

	chapterCalls     int
	blackSilenceCall int
	boundarySpans    [][2]int64
	boundaryFn       func(context.Context, int64, int64) ([]filler.Interval, error)
	cutCalls         []string
	grayCalls        []string
	grayHook         func(string, int64, int64)
	cutFn            func(string, int64, int64, string) error
}

func key3(path string, start, end int64) string { return fmt.Sprintf("%s|%d|%d", path, start, end) }

func (f *fakeTools) Chapters(context.Context, string) ([]filler.Chapter, error) {
	f.chapterCalls++
	return f.chapters, nil
}

func (f *fakeTools) Boundaries(ctx context.Context, _ string, startMs, endMs int64) ([]filler.Interval, []filler.Interval, error) {
	f.blackSilenceCall++
	f.boundarySpans = append(f.boundarySpans, [2]int64{startMs, endMs})
	if f.boundaryFn != nil {
		gaps, err := f.boundaryFn(ctx, startMs, endMs)
		return gaps, nil, err
	}
	return append([]filler.Interval(nil), f.blacks...), append([]filler.Interval(nil), f.silences...), nil
}

func (f *fakeTools) Transcribe(_ context.Context, _ string, start, end int64) ([]filler.TranscriptSegment, error) {
	if f.transcribeErr != nil {
		return nil, f.transcribeErr
	}
	return f.transcripts[fmt.Sprintf("%d:%d", start, end)], nil
}

func (f *fakeTools) GrayFrames(_ context.Context, path string, start, end int64) ([][]byte, error) {
	// The splitter passes drop-dir-joined paths; tests key on the basename.
	if f.grayHook != nil {
		f.grayHook(filepath.Base(path), start, end)
	}
	f.grayCalls = append(f.grayCalls, key3(filepath.Base(path), start, end))
	frames, ok := f.grayFrames[key3(filepath.Base(path), start, end)]
	if !ok {
		return nil, fmt.Errorf("no frames for %s", key3(path, start, end))
	}
	return frames, nil
}

func (f *fakeTools) KeyframesIn(ctx context.Context, path string, _, _ int64, n int) ([][]byte, error) {
	return f.Keyframes(ctx, path, n)
}

func (f *fakeTools) Keyframes(_ context.Context, path string, _ int) ([][]byte, error) {
	// Scripted per basename (the vision/heuristic tiers pass drop-dir-joined paths),
	// so a unit test never shells ffmpeg for real JPEGs.
	return f.keyframes[filepath.Base(path)], nil
}

// ⚠ The written bytes are DERIVED FROM THE SPAN, and that is load-bearing rather than decorative.
// A segment's identity is the hash of its contents (§10 V38c), so a fake that wrote the same bytes
// for every cut would make every segment of a reel hash identically — they would collapse into one
// catalog row, correctly, and the test could not tell that outcome apart from the empty-hash bug
// V51a fixes. Real cuts of different spans differ; the fake has to as well.
func (f *fakeTools) Cut(_ context.Context, in string, start, end int64, out string) error {
	f.cutCalls = append(f.cutCalls, fmt.Sprintf("%d-%d→%s", start, end, filepath.Base(out)))
	if f.cutFn != nil {
		return f.cutFn(in, start, end, out)
	}
	return os.WriteFile(out, []byte(fmt.Sprintf("cut %d-%d", start, end)), 0o644)
}

// splitMemStore is an in-memory SplitStore.
type splitMemStore struct {
	mu           sync.Mutex
	clips        map[string]filler.StoreClip
	proposals    map[string]filler.SplitProposal
	claims       map[string]string
	fingerprints map[string][]uint64
	// roundTripProposals makes the fake cross the same JSON durability boundary as the SQL store.
	// It is opt-in because most splitter tests exercise domain behavior, while checkpoint tests
	// specifically need private in-memory fields to disappear between passes like they do live.
	roundTripProposals bool
	// Captures whether cache population incorrectly reused an expired pipeline context.
	fingerprintWriteCtxErr error
	fingerprintReadErr     error
	fingerprintWriteErr    error
	pipelines              map[string]filler.ClipPipeline
}

type failingSplitStore struct {
	*splitMemStore
	operation string
	failOn    int
	calls     map[string]int
	before    func(string) error
}

type blockingGenerationStore struct {
	*splitMemStore
	reached chan struct{}
	release chan struct{}
}

type recoveringClaimStore struct {
	*splitMemStore
	firstAtPublication chan struct{}
	releaseFirst       chan struct{}
	recover            atomic.Bool
	firstRenewals      atomic.Int32
}

func (s *recoveringClaimStore) AcquireSplitProposalClaim(ctx context.Context, id, token string, at, expiresAt time.Time) (filler.SplitProposal, error) {
	if token == "owner-b" && s.recover.Load() {
		s.mu.Lock()
		defer s.mu.Unlock()
		p, ok := s.proposals[id]
		if !ok {
			return filler.SplitProposal{}, filler.ErrProposalGone
		}
		s.claims[id] = token
		return p, nil
	}
	return s.splitMemStore.AcquireSplitProposalClaim(ctx, id, token, at, expiresAt)
}

func (s *recoveringClaimStore) RenewSplitProposalClaim(ctx context.Context, id, token string, expiresAt time.Time) error {
	if token == "owner-a" && s.firstRenewals.Add(1) == 2 {
		close(s.firstAtPublication)
		select {
		case <-s.releaseFirst:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.splitMemStore.RenewSplitProposalClaim(ctx, id, token, expiresAt)
}

func (s *blockingGenerationStore) ReplaceSplitChildren(context.Context, string, []string, time.Time) (int, error) {
	close(s.reached)
	<-s.release
	return 0, errors.New("injected generation failure")
}

func (s *blockingGenerationStore) CompleteSplitConfirmation(context.Context, filler.SplitCompletion) (int, error) {
	close(s.reached)
	<-s.release
	return 0, errors.New("injected generation failure")
}

func (s *failingSplitStore) fail(operation string) error {
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[operation]++
	if s.before != nil {
		if err := s.before(operation); err != nil {
			return err
		}
	}
	failOn := s.failOn
	if failOn == 0 {
		failOn = 1
	}
	if s.operation == operation && s.calls[operation] == failOn {
		return fmt.Errorf("injected %s failure", operation)
	}
	return nil
}

func (s *failingSplitStore) UpsertClip(ctx context.Context, clip filler.StoreClip) error {
	if err := s.fail("catalog"); err != nil {
		return err
	}
	return s.splitMemStore.UpsertClip(ctx, clip)
}

func (s *failingSplitStore) SetClipTags(ctx context.Context, hash string, leaves []string) error {
	if err := s.fail("tag"); err != nil {
		return err
	}
	return s.splitMemStore.SetClipTags(ctx, hash, leaves)
}

func (s *failingSplitStore) UpsertClipPipeline(ctx context.Context, row filler.ClipPipeline) error {
	if err := s.fail("enrollment"); err != nil {
		return err
	}
	return s.splitMemStore.UpsertClipPipeline(ctx, row)
}

func (s *failingSplitStore) SetClipComposite(ctx context.Context, hash string, composite bool, at time.Time) error {
	if err := s.fail("composite"); err != nil {
		return err
	}
	return s.splitMemStore.SetClipComposite(ctx, hash, composite, at)
}

func (s *failingSplitStore) ReplaceSplitChildren(ctx context.Context, parentHash string, keep []string, at time.Time) (int, error) {
	if err := s.fail("generation"); err != nil {
		return 0, err
	}
	return s.splitMemStore.ReplaceSplitChildren(ctx, parentHash, keep, at)
}

func (s *failingSplitStore) SetClipsHeld(ctx context.Context, paths []string, held, autoFiled bool, at time.Time) (int, error) {
	if err := s.fail("parent filing"); err != nil {
		return 0, err
	}
	return s.splitMemStore.SetClipsHeld(ctx, paths, held, autoFiled, at)
}

func (s *failingSplitStore) MarkPipelineFiled(ctx context.Context, hash string, at time.Time) error {
	if err := s.fail("pipeline filing"); err != nil {
		return err
	}
	return s.splitMemStore.MarkPipelineFiled(ctx, hash, at)
}

func (s *failingSplitStore) DeleteSplitProposal(ctx context.Context, id string) error {
	if err := s.fail("proposal"); err != nil {
		return err
	}
	return s.splitMemStore.DeleteSplitProposal(ctx, id)
}

func (s *failingSplitStore) UpdateSplitProposal(ctx context.Context, proposal filler.SplitProposal) error {
	if err := s.fail("partial proposal"); err != nil {
		return err
	}
	return s.splitMemStore.UpdateSplitProposal(ctx, proposal)
}

func (s *failingSplitStore) CompletePartialSplitConfirmation(ctx context.Context, completion filler.SplitPartialCompletion) error {
	if err := s.fail("partial proposal"); err != nil {
		return err
	}
	return s.splitMemStore.CompletePartialSplitConfirmation(ctx, completion)
}

func (s *failingSplitStore) CompleteSplitConfirmation(ctx context.Context, completion filler.SplitCompletion) (int, error) {
	// These are deliberately checked before the in-memory commit. They model failures at each
	// statement inside the production transaction; none may leak an intermediate durable state.
	for _, operation := range []string{"composite", "pipeline filing", "parent filing", "proposal", "generation"} {
		if err := s.fail(operation); err != nil {
			return 0, err
		}
	}
	return s.splitMemStore.CompleteSplitConfirmation(ctx, completion)
}

func newSplitMemStore() *splitMemStore {
	return &splitMemStore{
		clips: map[string]filler.StoreClip{}, proposals: map[string]filler.SplitProposal{},
		claims: map[string]string{}, fingerprints: map[string][]uint64{}, pipelines: map[string]filler.ClipPipeline{},
	}
}

// ⚠ **Keyed by HASH, because `store.UpsertClip` is `ON CONFLICT(hash)` and `store.GetClip` is
// `WHERE hash = ?`.** This map was keyed on `Path`, and that single mismatch hid two shipped bugs
// at once: `Confirm` looked the compilation up by path (so no split could ever be committed), and
// every segment was upserted with an empty hash (so a 41-segment reel collapsed into one row).
// Both were invisible here because a path-keyed fixture answers a question production never asks.
// Keep this keyed exactly as the real store is — a fixture that indexes differently from the
// thing it stands in for cannot see key confusion by construction.
func (m *splitMemStore) GetClip(_ context.Context, id string) (filler.StoreClip, bool, error) {
	c, ok := m.clips[id]
	return c, ok, nil
}
func (m *splitMemStore) ListClips(context.Context) ([]filler.StoreClip, error) {
	var out []filler.StoreClip
	for _, c := range m.clips {
		out = append(out, c)
	}
	return out, nil
}
func (m *splitMemStore) ListClipFingerprints(_ context.Context, algorithm string) (map[string][]uint64, error) {
	if m.fingerprintReadErr != nil {
		return nil, m.fingerprintReadErr
	}
	out := make(map[string][]uint64)
	for key, frames := range m.fingerprints {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) == 2 && parts[1] == algorithm {
			out[parts[0]] = append([]uint64(nil), frames...)
		}
	}
	return out, nil
}
func (m *splitMemStore) UpsertClipFingerprint(ctx context.Context, clipHash, algorithm string, frames []uint64) error {
	m.fingerprintWriteCtxErr = ctx.Err()
	if m.fingerprintWriteErr != nil {
		return m.fingerprintWriteErr
	}
	m.fingerprints[clipHash+"|"+algorithm] = append([]uint64(nil), frames...)
	return nil
}
func (m *splitMemStore) UpsertClip(_ context.Context, c filler.StoreClip) error {
	if old, ok := m.clips[c.Hash]; ok {
		// Match production's lifecycle preservation on conflict.
		c.RemovedAt = old.RemovedAt
		c.ParentHash = old.ParentHash
	}
	m.clips[c.Hash] = c
	return nil
}
func (m *splitMemStore) GetClipTags(_ context.Context, hash string, leavesOnly bool) ([]string, error) {
	c, ok := m.clips[hash]
	if !ok {
		return nil, fmt.Errorf("clip not found: %s", hash)
	}
	if leavesOnly {
		return append([]string(nil), c.AssertedTags...), nil
	}
	return append([]string(nil), c.Tags...), nil
}
func (m *splitMemStore) SetClipTags(_ context.Context, hash string, leaves []string) error {
	c, ok := m.clips[hash]
	if !ok {
		return fmt.Errorf("clip not found: %s", hash)
	}
	forest := taxonomy.New(taxonomy.SeedForest())
	c.AssertedTags = unionStrings(c.AssertedTags, leaves)
	c.Tags = nil
	for _, leaf := range c.AssertedTags {
		c.Tags = unionStrings(c.Tags, []string{leaf})
		c.Tags = unionStrings(c.Tags, forest.Ancestors(leaf))
	}
	c.Category = forest.PrimaryProductLeaf(c.AssertedTags)
	m.clips[hash] = c
	return nil
}
func (m *splitMemStore) UpsertClipPipeline(_ context.Context, row filler.ClipPipeline) error {
	m.pipelines[row.ClipHash] = row
	return nil
}

func unionStrings(left, right []string) []string {
	out := append([]string(nil), left...)
	seen := make(map[string]bool, len(out)+len(right))
	for _, value := range out {
		seen[value] = true
	}
	for _, value := range right {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
func (m *splitMemStore) ReplaceSplitChildren(_ context.Context, parentHash string, keepHashes []string, at time.Time) (int, error) {
	keep := make(map[string]bool, len(keepHashes))
	for _, hash := range keepHashes {
		keep[hash] = true
	}
	retired := 0
	for hash, c := range m.clips {
		if c.ParentHash != parentHash {
			continue
		}
		if keep[hash] {
			c.RemovedAt = time.Time{}
		} else if c.RemovedAt.IsZero() {
			c.RemovedAt = at
			retired++
		}
		m.clips[hash] = c
	}
	return retired, nil
}
func (m *splitMemStore) CompleteSplitConfirmation(ctx context.Context, completion filler.SplitCompletion) (int, error) {
	if m.claims[completion.ProposalID] != completion.ClaimToken {
		return 0, filler.ErrProposalClaimed
	}
	parent, ok := m.clips[completion.ParentHash]
	if !ok || !parent.Held {
		return 0, errors.New("parent is not held for review")
	}
	if proposal, ok := m.proposals[completion.ProposalID]; !ok || proposal.ClipHash != completion.ParentHash {
		return 0, errors.New("proposal not found")
	}
	parentPipeline, ok := m.pipelines[completion.ParentHash]
	if !ok || parentPipeline.Disposition != filler.DispositionReview {
		return 0, errors.New("parent pipeline is not awaiting review")
	}
	for _, hash := range completion.ActivateHashes {
		pipeline, ok := m.pipelines[hash]
		if !ok || pipeline.Disposition != filler.DispositionReview {
			return 0, fmt.Errorf("child %s is not staged for review", hash)
		}
	}
	parent.IsComposite = true
	parent.Held = false
	m.clips[completion.ParentHash] = parent
	parentPipeline.Disposition = filler.DispositionFiled
	parentPipeline.UpdatedAt = completion.At
	m.pipelines[completion.ParentHash] = parentPipeline
	for _, hash := range completion.ActivateHashes {
		pipeline := m.pipelines[hash]
		pipeline.Disposition = filler.DispositionRunning
		pipeline.UpdatedAt = completion.At
		m.pipelines[hash] = pipeline
	}
	delete(m.proposals, completion.ProposalID)
	delete(m.claims, completion.ProposalID)
	return m.ReplaceSplitChildren(ctx, completion.ParentHash, completion.ChildHashes, completion.At)
}
func (m *splitMemStore) DeleteClip(_ context.Context, id string) error {
	delete(m.clips, id)
	return nil
}

// SetClipComposite marks the parent composite by HASH (§10 V45) — a direct lookup now that the
// map is keyed the way the real store is. It used to scan for a matching `Hash` because the key
// was the path; that workaround was the fixture quietly admitting it indexed clips differently
// from production.
func (m *splitMemStore) SetClipComposite(_ context.Context, hash string, composite bool, _ time.Time) error {
	c, ok := m.clips[hash]
	if !ok {
		return fmt.Errorf("composite target not found: %s", hash)
	}
	c.IsComposite = composite
	m.clips[hash] = c
	return nil
}
func (m *splitMemStore) SetClipsHeld(_ context.Context, paths []string, held, _ bool, _ time.Time) (int, error) {
	wanted := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		wanted[path] = struct{}{}
	}
	updated := 0
	for hash, c := range m.clips {
		if _, ok := wanted[c.Path]; !ok {
			continue
		}
		c.Held = held
		m.clips[hash] = c
		updated++
	}
	return updated, nil
}
func (m *splitMemStore) MarkPipelineFiled(_ context.Context, hash string, at time.Time) error {
	row, ok := m.pipelines[hash]
	if !ok {
		return nil
	}
	row.Disposition = filler.DispositionFiled
	row.UpdatedAt = at
	m.pipelines[hash] = row
	return nil
}
func (m *splitMemStore) UpsertSplitProposal(_ context.Context, p filler.SplitProposal) error {
	for id, existing := range m.proposals {
		if existing.ClipHash == p.ClipHash && id != p.ID {
			delete(m.proposals, id) // one proposal per clip, like the store's UNIQUE
		}
	}
	if m.roundTripProposals {
		p = durableProposalCopy(p)
	}
	m.proposals[p.ID] = p
	return nil
}

func durableProposalCopy(p filler.SplitProposal) filler.SplitProposal {
	copy := p
	if p.Segments != nil {
		raw, _ := json.Marshal(p.Segments)
		_ = json.Unmarshal(raw, &copy.Segments)
	}
	if p.Detection != nil {
		raw, _ := json.Marshal(p.Detection)
		var detection filler.SplitDetectionProgress
		_ = json.Unmarshal(raw, &detection)
		copy.Detection = &detection
	}
	return copy
}
func (m *splitMemStore) GetSplitProposal(_ context.Context, id string) (filler.SplitProposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.proposals[id]
	if !ok {
		return filler.SplitProposal{}, fmt.Errorf("not found")
	}
	return p, nil
}
func (m *splitMemStore) AcquireSplitProposalClaim(_ context.Context, id, token string, _, _ time.Time) (filler.SplitProposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.proposals[id]
	if !ok {
		return filler.SplitProposal{}, filler.ErrProposalGone
	}
	if owner := m.claims[id]; owner != "" && owner != token {
		return filler.SplitProposal{}, filler.ErrProposalClaimed
	}
	m.claims[id] = token
	return p, nil
}
func (m *splitMemStore) RenewSplitProposalClaim(_ context.Context, id, token string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.claims[id] != token {
		return filler.ErrProposalClaimed
	}
	return nil
}
func (m *splitMemStore) ReleaseSplitProposalClaim(_ context.Context, id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.claims[id] != token {
		return filler.ErrProposalClaimed
	}
	delete(m.claims, id)
	return nil
}
func (m *splitMemStore) DeleteSplitProposal(_ context.Context, id string) error {
	delete(m.proposals, id)
	return nil
}
func (m *splitMemStore) ListSplitProposals(context.Context) ([]filler.SplitProposal, error) {
	out := make([]filler.SplitProposal, 0, len(m.proposals))
	for _, p := range m.proposals {
		out = append(out, p)
	}
	return out, nil
}

// ⚠ REFUSES to insert, exactly as the real store does. A fake that happily created the row would
// hide the resurrection race this method exists to prevent — the class this repo has already been
// bitten by twice (a double that never refuses cannot catch a write-through-a-dead-handle bug).
func (m *splitMemStore) UpdateSplitProposal(_ context.Context, p filler.SplitProposal) error {
	_, ok := m.proposals[p.ID]
	if !ok {
		return fmt.Errorf("%w: %s", filler.ErrProposalGone, p.ID)
	}
	m.proposals[p.ID] = p
	return nil
}
func (m *splitMemStore) CompletePartialSplitConfirmation(_ context.Context, completion filler.SplitPartialCompletion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := completion.Proposal
	if m.claims[p.ID] != completion.ClaimToken {
		return filler.ErrProposalClaimed
	}
	if _, ok := m.proposals[p.ID]; !ok {
		return filler.ErrProposalGone
	}
	for _, hash := range completion.ActivateHashes {
		pipeline, ok := m.pipelines[hash]
		if !ok || pipeline.Disposition != filler.DispositionReview {
			return fmt.Errorf("child %s is not staged for review", hash)
		}
	}
	for _, hash := range completion.ActivateHashes {
		pipeline := m.pipelines[hash]
		pipeline.Disposition = filler.DispositionRunning
		pipeline.UpdatedAt = completion.At
		m.pipelines[hash] = pipeline
	}
	m.proposals[p.ID] = p
	delete(m.claims, p.ID)
	return nil
}

// ListTaxa serves the REAL seed forest (§10 V45a), like tagMemStore — the splitter grounds each
// segment's tags against it, so a segment tagged `toys` resolves and an off-vocabulary slug is dropped
// exactly as a directly-tagged clip is. An empty graph would ground nothing and prove nothing.
func (m *splitMemStore) ListTaxa(_ context.Context) ([]taxonomy.Taxon, error) {
	return taxonomy.SeedForest(), nil
}

// seedCompilation files a compilation and RETURNS ITS HASH — the identity every caller then hands
// to `Propose`.
//
// ⚠ Returning it is the point. Callers used to pass the PATH to `Propose`, which is an identity
// parameter, and a path-keyed fixture happily answered — so the suite asserted against a lookup
// production does not perform. Handing back the hash means no test re-derives the identity, and
// none can express "look this clip up by its location" even by accident.
func seedCompilation(st *splitMemStore, path string, durationMs int64) string {
	c := filler.StoreClip{}
	// ⚠ Hash is the IDENTITY (§10 V38c) SetClipComposite/ParentHash key on, and it must be NON-EMPTY
	// and DISTINCT from the path. Leaving it "" made SetClipComposite(clip.Hash=="") match whichever
	// empty-hash clip the map iteration reached first — the compilation OR a freshly-cut segment — an
	// intermittent "compilation not marked composite" flake ([[loomarr-fixture-collapsed-keys]]).
	c.Hash = "hash-of-" + path
	c.Path = path
	c.Name = filepath.Base(path)
	c.Kind = filler.Commercial
	c.DurationMs = durationMs
	c.Source = "archive"
	c.License = "https://creativecommons.org/licenses/by/4.0/"
	c.Quality = "480p"
	st.clips[c.Hash] = c
	return c.Hash
}

func bindCompilationIdentity(t *testing.T, st *splitMemStore, oldHash, fullPath string) string {
	t.Helper()
	hash, err := filler.ClipID(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	clip := st.clips[oldHash]
	delete(st.clips, oldHash)
	clip.Hash = hash
	st.clips[hash] = clip
	return hash
}

func stageParentForSplitReview(st *splitMemStore, hash string) {
	parent := st.clips[hash]
	parent.Held = true
	st.clips[hash] = parent
	st.pipelines[hash] = filler.ClipPipeline{
		ClipHash: hash, Stage: filler.StageSplit, Status: filler.StatusQueued,
		Disposition: filler.DispositionReview, EnrolledAt: time.Now(), UpdatedAt: time.Now(),
	}
}

func newSplitter(st filler.SplitStore, tools filler.MediaTools, provider *testkit.LLM, dropDir string) *filler.Splitter {
	var p llm.Provider
	if provider != nil {
		p = provider
	}
	n := 0
	// ⚠ The REAL default (10s), not 0. The suite should exercise the number production runs with:
	// a splitter built with no floor would pass tests that the live 10s floor then fails, which is
	// exactly how the sub-floor problem stayed invisible until it was measured on a real reel.
	return filler.NewSplitter(st, tools, p, dropDir,
		func() time.Duration { return 10 * time.Second },
		func() string { n++; return fmt.Sprintf("sp_%d", n) },
		func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }, nil).WithSplitSourceResolver(fixtureSplitSourceResolver)
}

func fixtureSplitSourceResolver(_ context.Context, root string, clip filler.StoreClip, bound filler.SplitSourceAsset) (filler.SplitSourceAsset, string, error) {
	path := filepath.Join(root, filepath.FromSlash(clip.Path))
	if bound.Role != "" {
		return bound, filepath.Join(root, filepath.FromSlash(bound.Path)), nil
	}
	digest := sha256.Sum256([]byte(clip.Hash))
	identity := fmt.Sprintf("%x", digest)
	size := int64(1)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		fullDigest, fullSize, digestErr := filler.FileSHA256(path)
		clipHash, clipErr := filler.ClipID(path)
		if digestErr != nil || clipErr != nil {
			return filler.SplitSourceAsset{}, "", errors.Join(digestErr, clipErr)
		}
		return filler.SplitSourceAsset{Role: filler.SplitSourceLegacyPlayback, SHA256: fullDigest, Bytes: fullSize, ClipHash: clipHash, Path: clip.Path, DurationMs: clip.DurationMs}, path, nil
	}
	return filler.SplitSourceAsset{Role: filler.SplitSourceLegacyPlayback, SHA256: fmt.Sprintf("%x", digest), Bytes: size, ClipHash: identity, Path: clip.Path, DurationMs: clip.DurationMs}, path, nil
}

func TestSplitStage_LongBoundaryScanResumesFromDurableChunkAfterRestartAndTimeout(t *testing.T) {
	st := newSplitMemStore()
	duration := int64((21 * time.Minute) / time.Millisecond)
	hash := seedCompilation(st, "comps/three-hour-shape.mp4", duration)
	tools := &fakeTools{boundaryFn: func(ctx context.Context, startMs, endMs int64) ([]filler.Interval, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var gaps []filler.Interval
		for cut := startMs + 30_000; cut < endMs; cut += 30_000 {
			gaps = append(gaps, filler.Interval{StartMs: cut - 100, EndMs: cut + 100})
		}
		return gaps, nil
	}}
	clip := st.clips[hash]
	clip.IsComposite = true
	st.clips[hash] = clip
	newStage := func() *filler.SplitStage {
		// A fresh splitter/stage on every call simulates an application restart. The only state
		// allowed to carry progress is the proposal in the store.
		return filler.NewSplitStage(newSplitter(st, tools, nil, t.TempDir()), st)
	}

	if _, err := newStage().Run(context.Background(), clip); !errors.Is(err, filler.ErrDeferred) {
		t.Fatalf("first chunk = %v, want deliberate deferral", err)
	}
	props, _ := st.ListSplitProposals(context.Background())
	if len(props) != 1 || props[0].Ready() || props[0].Detection.ScannedThroughMs != 600_000 {
		t.Fatalf("first checkpoint = %+v, want a private draft through 10:00", props)
	}
	proposalID := props[0].ID

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newStage().Run(expired, clip); !errors.Is(err, context.Canceled) {
		t.Fatalf("expired second chunk = %v, want cancellation", err)
	}
	props, _ = st.ListSplitProposals(context.Background())
	if props[0].Detection.ScannedThroughMs != 600_000 {
		t.Fatalf("timeout moved checkpoint to %d; an unfinished span must repeat", props[0].Detection.ScannedThroughMs)
	}

	for i := 0; i < 2; i++ {
		if _, err := newStage().Run(context.Background(), clip); !errors.Is(err, filler.ErrDeferred) {
			t.Fatalf("resume pass %d = %v, want deferral", i+1, err)
		}
	}
	out, err := newStage().Run(context.Background(), clip)
	if err != nil || out.Verdict != filler.VerdictReview {
		t.Fatalf("final pass = (%+v, %v), want completed proposal awaiting policy", out, err)
	}

	props, _ = st.ListSplitProposals(context.Background())
	if len(props) != 1 || !props[0].Ready() || props[0].ID != proposalID {
		t.Fatalf("completed proposal = %+v, want same durable id and a reviewable cut list", props)
	}
	wantSpans := [][2]int64{
		{0, 600_000},
		{600_000, 1_200_000}, // canceled attempt
		{600_000, 1_200_000}, // resumed after restart
		{1_200_000, duration},
	}
	if !reflect.DeepEqual(tools.boundarySpans, wantSpans) {
		t.Fatalf("boundary spans = %v, want %v", tools.boundarySpans, wantSpans)
	}
	if tools.chapterCalls != 1 {
		t.Errorf("chapters checked %d times; draft resume repeated triage", tools.chapterCalls)
	}
}

// A checkpoint crosses JSON before the next pass. The detector's private source bitmask cannot be
// the only copy of boundary evidence, or a restart turns corroborated black+silence cuts into
// confidence 0 and makes the default auto-split threshold impossible to clear.
func TestSplitStage_BoundaryConfidenceSurvivesDetectionCheckpoint(t *testing.T) {
	st := newSplitMemStore()
	st.roundTripProposals = true
	hash := seedCompilation(st, "comps/corroborated.mp4", 65_000)
	clip := st.clips[hash]
	clip.IsComposite = true
	st.clips[hash] = clip
	tools := &fakeTools{
		blacks: []filler.Interval{
			{StartMs: 20_000, EndMs: 21_000},
			{StartMs: 40_000, EndMs: 41_000},
		},
		silences: []filler.Interval{
			{StartMs: 20_000, EndMs: 21_000},
			{StartMs: 40_000, EndMs: 41_000},
		},
	}
	newStage := func() *filler.SplitStage {
		return filler.NewSplitStage(newSplitter(st, tools, nil, t.TempDir()), st)
	}

	if _, err := newStage().Run(context.Background(), clip); !errors.Is(err, filler.ErrDeferred) {
		t.Fatalf("checkpoint pass = %v, want deliberate deferral", err)
	}
	out, err := newStage().Run(context.Background(), clip)
	if err != nil || out.Verdict != filler.VerdictReview {
		t.Fatalf("resume pass = (%+v, %v), want completed proposal awaiting policy", out, err)
	}
	props, err := st.ListSplitProposals(context.Background())
	if err != nil || len(props) != 1 || !props[0].Ready() {
		t.Fatalf("completed proposals = %+v, %v", props, err)
	}
	for i, seg := range props[0].Segments {
		if seg.BoundaryConfidence != 90 || seg.StartEvidence == "" || seg.EndEvidence == "" {
			t.Errorf("segment %d boundary = %d (%q / %q), want persisted corroborated evidence at 90",
				i, seg.BoundaryConfidence, seg.StartEvidence, seg.EndEvidence)
		}
	}
}

func TestSplitStage_UntitledChapterEvidenceSurvivesDetectionCheckpoint(t *testing.T) {
	st := newSplitMemStore()
	st.roundTripProposals = true
	hash := seedCompilation(st, "comps/untitled-chapter.mp4", 60_000)
	clip := st.clips[hash]
	clip.IsComposite = true
	st.clips[hash] = clip
	tools := &fakeTools{chapters: []filler.Chapter{{StartMs: 0, EndMs: 60_000}}}
	newStage := func() *filler.SplitStage {
		return filler.NewSplitStage(newSplitter(st, tools, nil, t.TempDir()), st)
	}

	if _, err := newStage().Run(context.Background(), clip); !errors.Is(err, filler.ErrDeferred) {
		t.Fatalf("checkpoint pass = %v, want deliberate deferral", err)
	}
	if _, err := newStage().Run(context.Background(), clip); err != nil {
		t.Fatalf("resume pass: %v", err)
	}
	props, _ := st.ListSplitProposals(context.Background())
	if len(props) != 1 || len(props[0].Segments) != 1 {
		t.Fatalf("completed proposals = %+v", props)
	}
	seg := props[0].Segments[0]
	if seg.BoundaryConfidence != 100 || seg.StartEvidence != "chapter" || seg.EndEvidence != "chapter" {
		t.Errorf("chapter boundary = %d (%q / %q), want durable chapter evidence at 100",
			seg.BoundaryConfidence, seg.StartEvidence, seg.EndEvidence)
	}
}

// Chapters split for free: no black/silence pass runs, and titles become names.
func TestPropose_ChaptersShortCircuitDetection(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/1987.mp4", 61_000)
	tools := &fakeTools{chapters: []filler.Chapter{
		{StartMs: 0, EndMs: 30000, Title: "McDonald's"},
		{StartMs: 30000, EndMs: 61000, Title: "Lego"},
	}}
	llmMock := testkit.NewLLM(
		testkit.FinalResponse(`{"era":0,"audience":"kids","tags":["fast_food"]}`),
		testkit.FinalResponse(`{"era":0,"audience":"kids","tags":["toys"]}`),
	)
	drop := t.TempDir()
	sp := newSplitter(st, tools, llmMock, drop)

	p, err := sp.Propose(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if tools.blackSilenceCall != 0 {
		t.Error("chapters present, but the coarse detector still ran")
	}
	// ⚠ Names come from the CHAPTERS, not from a model. The `Category` assertion that stood here
	// was checking `classify`, which V51g removed from `Propose` — 51 LLM turns inside a 120s pass,
	// on segments whose only input was a generated name. Tagging happens on each spawned segment's
	// own `tag` rung now, and the grounding rule it must obey is tested at `Classify` itself
	// (`TestClassify_UngroundedEraBecomesSuggestion`, `TestClassify_EraGroundedBySourceText`).
	if len(p.Segments) != 2 || p.Segments[0].Name != "McDonald's" || p.Segments[1].Name != "Lego" {
		t.Errorf("proposal = %+v", p.Segments)
	}
	// Persisted — review happens later, possibly after a restart.
	if _, err := st.GetSplitProposal(context.Background(), p.ID); err != nil {
		t.Errorf("proposal not persisted: %v", err)
	}
	// The catalog is UNTOUCHED: propose never writes clips.
	if len(st.clips) != 1 {
		t.Errorf("propose wrote to the catalog: %+v", st.clips)
	}
}

// Coarse split: black/silence boundaries cut, slivers dropped, parts named.
func TestPropose_CoarseSplit(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/1987.mp4", 90_000)
	tools := &fakeTools{
		blacks:   []filler.Interval{{StartMs: 900, EndMs: 1100}, {StartMs: 29800, EndMs: 30200}},
		silences: []filler.Interval{{StartMs: 59900, EndMs: 60100}},
	}
	llmMock := testkit.NewLLM(
		testkit.FinalResponse(`{"era":1987,"audience":"general","tags":["cars"]}`),
		testkit.FinalResponse(`{"era":0,"audience":"general","tags":["tech"]}`),
		testkit.FinalResponse(`{"era":0,"audience":"general","tags":["cereal"]}`),
	)
	sp := newSplitter(st, tools, llmMock, t.TempDir())

	p, err := sp.Propose(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	// The 1s head sliver is dropped; three boundaries → three kept segments.
	if len(p.Segments) != 3 {
		t.Fatalf("segments = %+v, want 3", p.Segments)
	}
	// ⚠ Name only. The `Era != 1987` half asserted `classify`, which no longer runs here (V51g) —
	// the era is settled on the segment's own `tag` rung, after `transcribe`, where there is
	// actually text to ground it in. Here the name is derived from the parent, and that IS
	// `Propose`'s job.
	if p.Segments[0].Name == "" {
		t.Errorf("segment naming wrong: %+v", p.Segments[0])
	}
	// ⚠ And the proposal must carry NO tags at all — a half-classified segment would be worse
	// than an untagged one, because the review screen would present a guess as a finding.
	if p.Segments[0].Era != 0 || p.Segments[0].SuggestedEra != 0 || len(p.Segments[0].Tags) != 0 {
		t.Errorf("Propose classified a segment; that belongs to the tag rung: %+v", p.Segments[0])
	}
	if p.Segments[0].StartMs != 1000 || p.Segments[2].EndMs != 90_000 {
		t.Errorf("cut positions wrong: %+v", p.Segments)
	}
}

// ⚠ The rescue's reason to exist: a 149s block with NO A/V boundaries, holding
// three adverts that only the transcript can see (plan §6.4's measured case).
func TestPropose_RescueSplitsWhatDetectorsCouldNot(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/late-night.mp4", 149_000)
	tools := &fakeTools{ // no chapters, no black, no silence — the measured shape
		transcripts: map[string][]filler.TranscriptSegment{
			"0:149000": {
				{StartMs: 0, EndMs: 27000, Text: "The Swiffer sweeper picks up dust"},
				{StartMs: 27400, EndMs: 54000, Text: "Aqua Globes water your plants since 1987"},
				{StartMs: 54000, EndMs: 149000, Text: "Call now for the amazing knife set"},
			},
		},
	}
	llmMock := testkit.NewLLM(
		// 1: the rescue — three adverts, cut at 27.4s exactly (the measured cut).
		testkit.FinalResponse(`{"adverts":[
			{"start":"00:00","end":"00:27","product":"Swiffer"},
			{"start":"00:27","end":"00:54","product":"Aqua Globes"},
			{"start":"00:54","end":"02:29","product":"amazing knife set"}]}`),
		// 2-4: classify each rescued segment. The Aqua Globes era is grounded
		// ("since 1987" IS in the transcript); the knife era is INVENTED.
		testkit.FinalResponse(`{"era":0,"audience":"general","tags":["tech"]}`),
		testkit.FinalResponse(`{"era":1987,"audience":"general","tags":["tech"]}`),
		testkit.FinalResponse(`{"era":1950,"audience":"general","tags":["tech"]}`),
	)
	sp := newSplitter(st, tools, llmMock, t.TempDir())

	p, err := sp.Propose(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Segments) != 3 {
		t.Fatalf("rescue produced %+v, want 3 segments", p.Segments)
	}
	if p.Segments[1].Name != "Aqua Globes" || p.Segments[1].StartMs != 27_000 {
		t.Errorf("rescued boundary wrong: %+v", p.Segments[1])
	}
	// ⚠ The era-grounding assertions that stood here MOVED, they were not dropped (§8 is not
	// negotiable). They exercised `Classify` through `Propose`'s `classify` call, which V51g
	// removed; the rule itself is unchanged and is tested at its own seam —
	// `TestClassify_EraGroundedBySourceText` (a year present in the text becomes `Era`) and
	// `TestClassify_UngroundedEraBecomesSuggestion` (an invented one becomes `SuggestedEra`,
	// never a tag). Every spawned segment reaches that code on its own `tag` rung.
	//
	// What `Propose` owes is the CUT, and that is what is asserted above: three segments, the
	// rescued boundary at the right millisecond, named from the parent. Tags are the tag rung's.
	for i, seg := range p.Segments {
		if seg.Era != 0 || seg.SuggestedEra != 0 || len(seg.Tags) != 0 {
			t.Errorf("segment %d arrived classified; Propose cuts, it does not describe: %+v", i, seg)
		}
	}
}

// ⚠ The load-bearing single-advert case: a 121s infomercial for ONE product
// must come back as ONE segment — never manufactured into clips (§6.4).
func TestPropose_SingleLongAdvertIsNotManufactured(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/infomercial.mp4", 121_000)
	tools := &fakeTools{
		transcripts: map[string][]filler.TranscriptSegment{
			"0:121000": {{StartMs: 0, EndMs: 121000, Text: "The amazing knife slices, dices, and juliennes"}},
		},
	}
	llmMock := testkit.NewLLM(
		testkit.FinalResponse(`{"adverts":[{"start":"00:00","end":"02:01","product":"amazing knife"}]}`),
		testkit.FinalResponse(`{"era":0,"audience":"general","tags":["tech"]}`),
	)
	sp := newSplitter(st, tools, llmMock, t.TempDir())

	p, err := sp.Propose(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Segments) != 1 {
		t.Fatalf("single advert manufactured into %d clips: %+v", len(p.Segments), p.Segments)
	}
	if p.Segments[0].Unsplittable {
		t.Error("a successfully-rescued single advert is not 'unsplittable'")
	}
}

// No whisper (or a whisper failure) ⇒ Unsplittable, NEVER a guessed cut (§15).
func TestPropose_WhisperFailureMarksUnsplittable(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/late-night.mp4", 149_000)
	tools := &fakeTools{transcribeErr: fmt.Errorf("whisper not configured")}
	sp := newSplitter(st, tools, testkit.NewLLM(), t.TempDir())

	p, err := sp.Propose(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Segments) != 1 || !p.Segments[0].Unsplittable {
		t.Fatalf("whisper failure = %+v, want one Unsplittable segment", p.Segments)
	}
}

// No LLM at all: coarse split still works, over-long segments say Unsplittable,
// and nothing is classified — the honest degradation.
func TestPropose_NoLLMDegradesHonestly(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/1987.mp4", 160_000)
	tools := &fakeTools{blacks: []filler.Interval{{StartMs: 29800, EndMs: 30200}}}
	sp := newSplitter(st, tools, nil, t.TempDir())

	p, err := sp.Propose(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Segments) != 2 {
		t.Fatalf("segments = %+v", p.Segments)
	}
	if !p.Segments[1].Unsplittable {
		t.Errorf("the over-long segment must say Unsplittable with no LLM: %+v", p.Segments[1])
	}
	for _, s := range p.Segments {
		if s.Era != 0 || s.Category != "" || s.Audience != "" {
			t.Errorf("classified without a provider: %+v", s)
		}
	}
}

// Dedup flags a segment matching a catalog clip — and never matches the
// compilation itself.
func TestPropose_DedupFlagsExistingClips(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/1987.mp4", 60_000)
	existing := filler.StoreClip{}
	// ⚠ A distinct, non-empty hash. Left empty, this clip and any other unhashed fixture share
	// the map key "" — the collapsed-key trap that has now hidden four separate bugs in this file.
	existing.Hash = "hash-of-old/mcdonalds.mp4"
	existing.Path = "old/mcdonalds.mp4"
	existing.DurationMs = 30_000
	st.clips[existing.Hash] = existing

	dupFrame := make([]byte, 72)
	for i := range dupFrame {
		dupFrame[i] = byte(i) // a deterministic non-flat pattern
	}
	compFrame := make([]byte, 72)
	for i := range compFrame {
		compFrame[i] = byte(255 - i) // a DIFFERENT pattern — matches only the compilation
	}
	tools := &fakeTools{
		blacks: []filler.Interval{{StartMs: 29800, EndMs: 30200}},
		grayFrames: map[string][][]byte{
			key3("1987.mp4", 0, 30000):       {dupFrame},
			key3("1987.mp4", 30000, 60_000):  {compFrame}, // matches ONLY the compilation
			key3("1987.mp4", 0, 60_000):      {compFrame}, // the compilation, hashed whole
			key3("mcdonalds.mp4", 0, 30_000): {dupFrame},
		},
	}
	sp := newSplitter(st, tools, nil, t.TempDir())

	p, err := sp.Propose(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if p.Segments[0].DupOf != "old/mcdonalds.mp4" {
		t.Errorf("duplicate not flagged: %+v", p.Segments[0])
	}
	// ⚠ The compilation itself is EXCLUDED from the candidate set — otherwise
	// every segment would "duplicate" the file it was cut from, and the flag
	// would cry wolf on exactly the clips that are new.
	if p.Segments[1].DupOf != "" {
		t.Errorf("segment flagged against its OWN compilation: %+v", p.Segments[1])
	}
}

func TestPropose_CatalogFingerprintCacheSurvivesSplitterRestart(t *testing.T) {
	st := newSplitMemStore()
	firstHash := seedCompilation(st, "comps/first.mp4", 30_000)
	existing := filler.StoreClip{}
	existing.Hash = "hash-of-catalog-ad"
	existing.Path = "old/catalog-ad.mp4"
	existing.DurationMs = 30_000
	st.clips[existing.Hash] = existing

	pattern := make([]byte, 72)
	for i := range pattern {
		pattern[i] = byte(i)
	}
	tools := &fakeTools{
		chapters: []filler.Chapter{{StartMs: 0, EndMs: 30_000, Title: "advert"}},
		grayFrames: map[string][][]byte{
			key3("catalog-ad.mp4", 0, 30_000): {pattern},
			key3("first.mp4", 0, 30_000):      {pattern},
			key3("second.mp4", 0, 30_000):     {pattern},
		},
	}

	first := newSplitter(st, tools, nil, t.TempDir())
	p, err := first.Propose(context.Background(), firstHash)
	if err != nil {
		t.Fatal(err)
	}
	delete(st.proposals, p.ID)
	delete(st.clips, firstHash)

	secondHash := seedCompilation(st, "comps/second.mp4", 30_000)
	// A new Splitter represents a process restart: only the store is shared.
	second := newSplitter(st, tools, nil, t.TempDir())
	if _, err := second.Propose(context.Background(), secondHash); err != nil {
		t.Fatal(err)
	}

	catalogCall := key3("catalog-ad.mp4", 0, 30_000)
	calls := 0
	for _, call := range tools.grayCalls {
		if call == catalogCall {
			calls++
		}
	}
	if calls != 1 {
		t.Errorf("catalog media decoded %d times across two splitters, want one persisted-cache fill; calls=%v", calls, tools.grayCalls)
	}
}

func TestPropose_CatalogFingerprintCompletedAtDeadlineIsStillPersisted(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/deadline.mp4", 30_000)
	existing := filler.StoreClip{}
	existing.Hash = "hash-of-deadline-candidate"
	existing.Path = "old/deadline-candidate.mp4"
	existing.DurationMs = 30_000
	st.clips[existing.Hash] = existing

	pattern := make([]byte, 72)
	for i := range pattern {
		pattern[i] = byte(i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceled := false
	tools := &fakeTools{
		chapters: []filler.Chapter{{StartMs: 0, EndMs: 30_000, Title: "advert"}},
		grayFrames: map[string][][]byte{
			key3("deadline-candidate.mp4", 0, 30_000): {pattern},
			key3("deadline.mp4", 0, 30_000):           {pattern},
		},
		grayHook: func(path string, _, _ int64) {
			// Simulate the pass expiring after ffmpeg produced the catalog frames but before the
			// cache write. The write must detach from this context or the completed work is lost.
			if path == "deadline-candidate.mp4" && !canceled {
				canceled = true
				cancel()
			}
		},
	}
	defer cancel()

	if _, err := newSplitter(st, tools, nil, t.TempDir()).Propose(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if len(st.fingerprints) != 1 {
		t.Fatalf("completed catalog decode left %d cache rows, want 1", len(st.fingerprints))
	}
	if st.fingerprintWriteCtxErr != nil {
		t.Errorf("cache write inherited expired pass: %v", st.fingerprintWriteCtxErr)
	}
}

func TestPropose_CatalogFingerprintCacheFailureFallsBackToMedia(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/cache-failure.mp4", 30_000)
	existing := filler.StoreClip{}
	existing.Hash = "hash-of-cache-failure-candidate"
	existing.Path = "old/cache-failure-candidate.mp4"
	existing.DurationMs = 30_000
	st.clips[existing.Hash] = existing
	st.fingerprintReadErr = errors.New("cache read unavailable")
	st.fingerprintWriteErr = errors.New("cache write unavailable")

	pattern := make([]byte, 72)
	for i := range pattern {
		pattern[i] = byte(i)
	}
	tools := &fakeTools{
		chapters: []filler.Chapter{{StartMs: 0, EndMs: 30_000, Title: "advert"}},
		grayFrames: map[string][][]byte{
			key3("cache-failure-candidate.mp4", 0, 30_000): {pattern},
			key3("cache-failure.mp4", 0, 30_000):           {pattern},
		},
	}
	p, err := newSplitter(st, tools, nil, t.TempDir()).Propose(context.Background(), hash)
	if err != nil {
		t.Fatalf("derived-cache outage failed split detection: %v", err)
	}
	if len(p.Segments) != 1 || p.Segments[0].DupOf != existing.Path {
		t.Errorf("media fallback did not preserve duplicate detection: %+v", p.Segments)
	}
}

func TestSplitStage_DiscardsDuplicateAndShortCandidatesBeforeAutoConfirm(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/1987.mp4", 70_000)
	parent := st.clips[hash]
	parent.Held = true
	st.clips[hash] = parent
	drop := t.TempDir()
	if err := os.MkdirAll(filepath.Join(drop, "comps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drop, "comps/1987.mp4"), []byte("compilation"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash = bindCompilationIdentity(t, st, hash, filepath.Join(drop, "comps/1987.mp4"))
	stageParentForSplitReview(st, hash)
	p := filler.SplitProposal{
		ID: "sp_existing", ClipHash: hash, CreatedAt: time.Now(),
		Segments: []filler.SplitSegment{
			{Index: 0, StartMs: 0, EndMs: 30_000, Name: "already have it", DupOf: "old/ad.mp4"},
			{Index: 1, StartMs: 30_000, EndMs: 34_000, Name: "boundary fragment"},
			{Index: 2, StartMs: 34_000, EndMs: 70_000, Name: "new advert", Category: "toys", Looked: true,
				BoundaryConfidence: 100, StartEvidence: "reel edge", EndEvidence: "reel edge"},
		},
	}
	if err := st.UpsertSplitProposal(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	tools := &fakeTools{}
	stage := filler.NewSplitStage(newSplitter(st, tools, nil, drop), st).WithAutoConfirm(
		filler.AutoSplitPolicy{
			Enabled:       func() bool { return true },
			MinConfidence: func() int { return 85 },
			MaxDuration:   func() time.Duration { return 2 * time.Minute },
		},
		func() time.Duration { return 10 * time.Second },
	)

	out, err := stage.Run(context.Background(), st.clips[hash])
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Spawned) != 1 || len(tools.cutCalls) != 1 {
		t.Fatalf("spawned = %v, cuts = %v; deterministic rejects became clips", out.Spawned, tools.cutCalls)
	}
	if got := tools.cutCalls[0]; !strings.HasPrefix(got, "34000-70000→") {
		t.Errorf("cut = %q, want only the usable 34s-70s span", got)
	}
	if !st.clips[hash].IsComposite {
		t.Error("parent was not retained as a composite")
	}
	if st.clips[hash].Held {
		t.Error("fully auto-confirmed composite remained held and invisible in the catalog")
	}
	if _, ok := st.proposals[p.ID]; ok {
		t.Error("completed proposal still waits for approval")
	}
}

func TestSplitStage_AllDiscardedFinishesWithoutAnEmptyReview(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/repeats.mp4", 30_000)
	parent := st.clips[hash]
	parent.Held = true
	st.clips[hash] = parent
	p := filler.SplitProposal{
		ID: "sp_duplicates", ClipHash: hash, CreatedAt: time.Now(),
		Segments: []filler.SplitSegment{{StartMs: 0, EndMs: 30_000, DupOf: "old/ad.mp4"}},
	}
	if err := st.UpsertSplitProposal(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	stage := filler.NewSplitStage(newSplitter(st, &fakeTools{}, nil, t.TempDir()), st).
		WithAutoConfirm(filler.AutoSplitPolicy{Enabled: func() bool { return true }}, func() time.Duration { return 10 * time.Second })

	out, err := stage.Run(context.Background(), st.clips[hash])
	if err != nil {
		t.Fatal(err)
	}
	if out.Verdict != filler.VerdictContinue || len(out.Spawned) != 0 {
		t.Fatalf("result = %+v", out)
	}
	if !st.clips[hash].IsComposite {
		t.Error("all-duplicate parent can still air")
	}
	if st.clips[hash].Held {
		t.Error("resolved all-duplicate parent remained held and invisible in the catalog")
	}
	if _, ok := st.proposals[p.ID]; ok {
		t.Error("an empty proposal was left in the review queue")
	}
}

func TestSplitStage_PersistsWhyEverySegmentNeedsReview(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/unclassified.mp4", 30_000)
	p := filler.SplitProposal{
		ID: "sp_unclassified", ClipHash: hash, CreatedAt: time.Now(),
		Segments: []filler.SplitSegment{{
			Index: 0, StartMs: 0, EndMs: 30_000, Name: "unclassified part", Looked: true,
			BoundaryConfidence: 90, StartEvidence: "reel edge", EndEvidence: "black + silence",
		}},
	}
	if err := st.UpsertSplitProposal(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	stage := filler.NewSplitStage(newSplitter(st, &fakeTools{}, nil, t.TempDir()), st).WithAutoConfirm(
		filler.AutoSplitPolicy{
			Enabled:       func() bool { return true },
			MinConfidence: func() int { return 85 },
			MaxDuration:   func() time.Duration { return 2 * time.Minute },
		},
		func() time.Duration { return 10 * time.Second },
	)

	out, err := stage.Run(context.Background(), st.clips[hash])
	if err != nil {
		t.Fatal(err)
	}
	if out.Verdict != filler.VerdictReview {
		t.Fatalf("verdict = %v, want review", out.Verdict)
	}
	persisted := st.proposals[p.ID]
	if got := persisted.Segments[0].HoldReason; got != string(filler.RejectUntagged) {
		t.Fatalf("persisted hold reason = %q, want %q", got, filler.RejectUntagged)
	}
}

// Confirm cuts, catalogs, and consumes — and leaves the compilation behind.
func TestConfirm_WritesReviewedSegments(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/1987.mp4", 61_000)
	drop := t.TempDir()
	if err := os.MkdirAll(filepath.Join(drop, "comps"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(drop, "comps", "1987.mp4")
	if err := os.WriteFile(src, []byte("compilation"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash = bindCompilationIdentity(t, st, hash, src)
	stageParentForSplitReview(st, hash)
	tools := &fakeTools{}
	sp := newSplitter(st, tools, nil, drop)
	// ⚠ Capture the proposal id from Propose's return, NOT by ranging st.proposals — Go randomises map
	// order, so if the store ever holds >1 proposal the range picked an arbitrary one and Confirm ran
	// against the wrong id (an intermittent "compilation not marked composite" flake, now fixed
	// deterministically). See [[loomarr-splitjob-test-map-order-flake]].
	prop, err := sp.Propose(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	propID := prop.ID

	// The operator's EDITED list: era suggestion accepted on the second segment,
	// and a third segment they added by hand.
	edited := []filler.SplitSegment{
		{StartMs: 0, EndMs: 30000, Name: "McDonald's", Era: 1987, Audience: filler.Kids, Category: "fast_food"},
		{StartMs: 30000, EndMs: 61000, Name: "Lego", Era: 1987, Audience: filler.Kids, Category: "toys"},
	}
	if _, err := sp.Confirm(context.Background(), propID, edited); err != nil {
		t.Fatal(err)
	}

	if len(tools.cutCalls) != 2 {
		t.Fatalf("cuts = %v", tools.cutCalls)
	}
	// ⚠ V45: the compilation is KEPT and marked a composite (NOT deleted — the reversal of V34). Its
	// row survives, flagged is_composite so pod assembly excludes it, and its file stays for re-split.
	comp, found, _ := st.GetClip(context.Background(), hash)
	if !found {
		t.Fatal("compilation row was deleted on confirm — V45 keeps the parent as a composite")
	}
	if !comp.IsComposite {
		t.Error("compilation survived but was not marked is_composite — it would still be airable")
	}
	if _, err := os.Stat(src); err != nil {
		t.Error("compilation file was removed on confirm — V45 keeps it for re-splitting")
	}
	// …the proposal is consumed…
	if _, err := st.GetSplitProposal(context.Background(), propID); err == nil {
		t.Error("proposal survived confirm")
	}
	// …and the segments are real clips with tags, durations, provenance, AND lineage back to the
	// composite. Skip the kept composite itself when counting segments.
	var segments []filler.StoreClip
	for path, c := range st.clips {
		if c.IsComposite {
			continue // the kept parent, not a segment
		}
		if c.DurationMs <= 0 || c.Kind != filler.Commercial || c.License == "" || c.Source != "archive" {
			t.Errorf("clip %s missing duration/kind/provenance: %+v", path, c.Clip)
		}
		if c.ParentHash != comp.Hash {
			t.Errorf("segment %s parent_hash = %q, want the composite's hash %q — lineage is the point of V45", c.Name, c.ParentHash, comp.Hash)
		}
		segments = append(segments, c)
	}
	if len(segments) != 2 {
		t.Fatalf("segments = %+v", segments)
	}
	for _, seg := range segments {
		if len(seg.AssertedTags) != 1 || seg.AssertedTags[0] != seg.Category {
			t.Errorf("confirmed segment %q taxonomy = category %q / asserted %v", seg.Name, seg.Category, seg.AssertedTags)
		}
		tags, ok := filler.ReadSidecarTags(filepath.Join(drop, seg.Path))
		if !ok || tags.ConditioningLineage == nil || tags.ConditioningLineage.ChildHash != seg.Hash {
			t.Errorf("confirmed segment %q child identity binding = %+v, ok=%v", seg.Name, tags.ConditioningLineage, ok)
		} else if tags.ConditioningLineage.ParentAssetRole != string(filler.SplitSourceLegacyPlayback) ||
			tags.ConditioningLineage.ParentAssetSHA256 != prop.Source.SHA256 {
			t.Errorf("confirmed segment %q parent asset binding = %+v, want role %q digest %q",
				seg.Name, tags.ConditioningLineage, filler.SplitSourceLegacyPlayback, prop.Source.SHA256)
		}
	}
	// The cut files exist at cataloged paths (segments only; the composite keeps its own file).
	for _, seg := range segments {
		if _, err := os.Stat(filepath.Join(drop, seg.Path)); err != nil {
			t.Errorf("cataloged segment %s has no file: %v", seg.Path, err)
		}
	}
}

func TestConfirm_SidecarFailurePublishesNoChildMedia(t *testing.T) {
	st := newSplitMemStore()
	parentHash := seedCompilation(st, "comps/sidecar-failure.mp4", 60_000)
	drop := t.TempDir()
	parent := filepath.Join(drop, "comps", "sidecar-failure.mp4")
	if err := os.MkdirAll(filepath.Dir(parent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte("compilation"), 0o600); err != nil {
		t.Fatal(err)
	}
	parentHash = bindCompilationIdentity(t, st, parentHash, parent)
	tools := &fakeTools{chapters: []filler.Chapter{
		{StartMs: 0, EndMs: 30_000, Title: "First reviewed child"},
		{StartMs: 30_000, EndMs: 60_000, Title: "Second reviewed child"},
	}}
	sp := newSplitter(st, tools, nil, drop)
	proposal, err := sp.Propose(context.Background(), parentHash)
	if err != nil {
		t.Fatal(err)
	}

	children := make([]string, 0, 2)
	childHashes := make([]string, 0, 2)
	for _, body := range []string{"cut 0-30000", "cut 30000-60000"} {
		expected := filepath.Join(t.TempDir(), "expected.mp4")
		if err := os.WriteFile(expected, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		childHash, err := filler.ClipID(expected)
		if err != nil {
			t.Fatal(err)
		}
		child, err := filler.ClipPath(drop, childHash, ".mp4")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(child), 0o755); err != nil {
			t.Fatal(err)
		}
		children = append(children, child)
		childHashes = append(childHashes, childHash)
	}
	// A directory at the final sidecar name is an unrecoverable metadata publication failure.
	// Confirm must roll back the first prepared sidecar when the later child fails, before either
	// media name reaches the scan tree.
	if err := os.Mkdir(mediatools.SidecarPathFor(children[1]), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := sp.Confirm(context.Background(), proposal.ID, proposal.Segments); err == nil {
		t.Fatal("Confirm succeeded despite an unwritable final sidecar")
	}
	for i, child := range children {
		if _, err := os.Stat(child); !os.IsNotExist(err) {
			t.Fatalf("child %d media became scan-visible without a complete batch: %v", i, err)
		}
		if _, err := os.Stat(mediatools.SidecarPathFor(child)); i == 0 && !os.IsNotExist(err) {
			t.Fatalf("first child sidecar survived later sidecar failure: %v", err)
		}
		if _, found, err := st.GetClip(context.Background(), childHashes[i]); err != nil || found {
			t.Fatalf("child %d catalog row = found %v, err %v; want absent", i, found, err)
		}
	}
}

func TestConfirm_SourceReplacementAfterValidationPublishesNothing(t *testing.T) {
	st := newSplitMemStore()
	drop := t.TempDir()
	parentFull := filepath.Join(drop, "reviewed-parent.mp4")
	if err := os.WriteFile(parentFull, []byte("reviewed parent bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	parentHash, err := filler.ClipID(parentFull)
	if err != nil {
		t.Fatal(err)
	}
	st.clips[parentHash] = filler.StoreClip{Clip: filler.Clip{
		Hash: parentHash, Path: "reviewed-parent.mp4", Name: "Parent", DurationMs: 30_000,
	}}
	stageParentForSplitReview(st, parentHash)
	replaced := false
	tools := &fakeTools{chapters: []filler.Chapter{{StartMs: 0, EndMs: 30_000, Title: "Child"}}}
	tools.cutFn = func(in string, start, end int64, out string) error {
		if !replaced {
			replaced = true
			if err := os.WriteFile(parentFull, []byte("replacement parent bytes"), 0o600); err != nil {
				return err
			}
		}
		return os.WriteFile(out, []byte(fmt.Sprintf("cut %d-%d from %s", start, end, filepath.Base(in))), 0o600)
	}
	sp := newSplitter(st, tools, nil, drop)
	proposal, err := sp.Propose(context.Background(), parentHash)
	if err != nil {
		t.Fatal(err)
	}
	beforeCatalog := len(st.clips)

	if _, err := sp.Confirm(context.Background(), proposal.ID, proposal.Segments); err == nil {
		t.Fatal("Confirm accepted a parent pathname replaced during cutting")
	}
	if len(st.clips) != beforeCatalog {
		t.Fatalf("catalog changed after source replacement: %+v", st.clips)
	}
	if _, ok := st.proposals[proposal.ID]; !ok {
		t.Fatal("source replacement consumed the retryable proposal")
	}
	if err := filepath.WalkDir(drop, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && filepath.Ext(path) == ".mp4" && filepath.Clean(path) != filepath.Clean(parentFull) {
			t.Fatalf("source replacement published child media %q", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestConfirm_SparseIdentityCollisionCannotHideCompositeReplacement(t *testing.T) {
	original, replacement := testkit.SparseIdentityCollision()
	st := newSplitMemStore()
	drop := t.TempDir()
	parentFull := filepath.Join(drop, "reviewed-parent.mp4")
	if err := os.WriteFile(parentFull, original, 0o600); err != nil {
		t.Fatal(err)
	}
	parentHash, err := filler.ClipID(parentFull)
	if err != nil {
		t.Fatal(err)
	}
	check := filepath.Join(t.TempDir(), "replacement.mp4")
	if err := os.WriteFile(check, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if replacementHash, err := filler.ClipID(check); err != nil || replacementHash != parentHash {
		t.Fatalf("fixture does not collide under ClipID: %q, %v", replacementHash, err)
	}
	st.clips[parentHash] = filler.StoreClip{Clip: filler.Clip{
		Hash: parentHash, Path: "reviewed-parent.mp4", Name: "Parent", DurationMs: 30_000,
	}}
	tools := &fakeTools{chapters: []filler.Chapter{{StartMs: 0, EndMs: 30_000, Title: "Child"}}}
	tools.cutFn = func(_ string, _, _ int64, out string) error {
		if err := os.WriteFile(parentFull, replacement, 0o600); err != nil {
			return err
		}
		return os.WriteFile(out, []byte("reviewed child"), 0o600)
	}
	sp := newSplitter(st, tools, nil, drop)
	proposal, err := sp.Propose(context.Background(), parentHash)
	if err != nil {
		t.Fatal(err)
	}
	beforeCatalog := len(st.clips)
	if _, err := sp.Confirm(context.Background(), proposal.ID, proposal.Segments); err == nil {
		t.Fatal("Confirm accepted different composite bytes hidden by the sparse catalog identity")
	}
	if len(st.clips) != beforeCatalog {
		t.Fatalf("catalog changed after exact-byte mismatch: %+v", st.clips)
	}
	if _, ok := st.proposals[proposal.ID]; !ok {
		t.Fatal("exact-byte mismatch consumed the retryable proposal")
	}
}

func TestConfirm_ExistingChildReuseRejectsBytesThatDoNotMatchTheirAddress(t *testing.T) {
	st := newSplitMemStore()
	parentHash := seedCompilation(st, "parent.mp4", 30_000)
	drop := t.TempDir()
	parentFull := filepath.Join(drop, "parent.mp4")
	if err := os.WriteFile(parentFull, []byte("parent"), 0o600); err != nil {
		t.Fatal(err)
	}
	honestParentHash, err := filler.ClipID(parentFull)
	if err != nil {
		t.Fatal(err)
	}
	delete(st.clips, parentHash)
	parentHash = honestParentHash
	st.clips[parentHash] = filler.StoreClip{Clip: filler.Clip{
		Hash: parentHash, Path: "parent.mp4", Name: "Parent", DurationMs: 30_000,
	}}
	sp := newSplitter(st, &fakeTools{chapters: []filler.Chapter{{StartMs: 0, EndMs: 30_000, Title: "Child"}}}, nil, drop)
	proposal, err := sp.Propose(context.Background(), parentHash)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(t.TempDir(), "expected.mp4")
	if err := os.WriteFile(expected, []byte("cut 0-30000"), 0o600); err != nil {
		t.Fatal(err)
	}
	childHash, err := filler.ClipID(expected)
	if err != nil {
		t.Fatal(err)
	}
	childFull, err := filler.ClipPath(drop, childHash, ".mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(childFull), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childFull, []byte("corrupt bytes under the staged hash"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filler.WriteSidecarTags(childFull, filler.SidecarTags{ConditioningLineage: &filler.ConditioningLineage{
		ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 0, IntendedEndMs: 30_000,
	}}, false); err != nil {
		t.Fatal(err)
	}

	if _, err := sp.Confirm(context.Background(), proposal.ID, proposal.Segments); err == nil {
		t.Fatal("Confirm reused corrupt bytes at a content-addressed child path")
	}
	if _, ok := st.proposals[proposal.ID]; !ok {
		t.Fatal("corrupt child reuse consumed the retryable proposal")
	}
}

func TestConfirm_ExistingChildRequiresExactStagedBytesBeyondClipID(t *testing.T) {
	st := newSplitMemStore()
	drop := t.TempDir()
	parentFull := filepath.Join(drop, "parent.mp4")
	if err := os.WriteFile(parentFull, []byte("parent"), 0o600); err != nil {
		t.Fatal(err)
	}
	parentHash, err := filler.ClipID(parentFull)
	if err != nil {
		t.Fatal(err)
	}
	st.clips[parentHash] = filler.StoreClip{Clip: filler.Clip{
		Hash: parentHash, Path: "parent.mp4", Name: "Parent", DurationMs: 30_000,
	}}
	stagedBytes, existingBytes := testkit.SparseIdentityCollision()
	tools := &fakeTools{chapters: []filler.Chapter{{StartMs: 0, EndMs: 30_000, Title: "Child"}}}
	tools.cutFn = func(_ string, _, _ int64, out string) error {
		return os.WriteFile(out, stagedBytes, 0o600)
	}
	sp := newSplitter(st, tools, nil, drop)
	proposal, err := sp.Propose(context.Background(), parentHash)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(t.TempDir(), "staged.mp4")
	if err := os.WriteFile(expected, stagedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	childHash, err := filler.ClipID(expected)
	if err != nil {
		t.Fatal(err)
	}
	childFull, err := filler.ClipPath(drop, childHash, ".mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(childFull), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childFull, existingBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filler.WriteSidecarTags(childFull, filler.SidecarTags{ConditioningLineage: &filler.ConditioningLineage{
		ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 0, IntendedEndMs: 30_000,
	}}, false); err != nil {
		t.Fatal(err)
	}

	if _, err := sp.Confirm(context.Background(), proposal.ID, proposal.Segments); err == nil {
		t.Fatal("Confirm reused different child bytes hidden by the sparse catalog identity")
	}
	if _, ok := st.proposals[proposal.ID]; !ok {
		t.Fatal("exact child mismatch consumed the retryable proposal")
	}
}

func TestConfirm_ConcurrentScanNeverObservesAnUnboundChild(t *testing.T) {
	st := newSplitMemStore()
	drop := t.TempDir()
	parent := filepath.Join(drop, "parent-staging.mp4")
	if err := os.WriteFile(parent, []byte("compilation"), 0o600); err != nil {
		t.Fatal(err)
	}
	parentFileHash, err := filler.ClipID(parent)
	if err != nil {
		t.Fatal(err)
	}
	parentFinal, err := filler.ClipPath(drop, parentFileHash, ".mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(parentFinal), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent, parentFinal); err != nil {
		t.Fatal(err)
	}
	parentHash := parentFileHash
	st.clips[parentHash] = filler.StoreClip{Clip: filler.Clip{
		Hash: parentHash, Path: filepath.ToSlash(filler.ClipRelPath(parentHash, ".mp4")),
		Name: "Compilation", Kind: filler.Commercial, DurationMs: 30_000,
	}}
	stageParentForSplitReview(st, parentHash)
	// The large reviewed display name makes the historical media-first then sidecar-write window
	// deterministic enough for the scanner to enter; atomic sidecar-first publication has no such
	// state regardless of how long metadata encoding takes.
	name := "Reviewed child " + strings.Repeat("x", 4<<20)
	tools := &fakeTools{chapters: []filler.Chapter{{StartMs: 0, EndMs: 30_000, Title: name}}}
	sp := newSplitter(st, tools, nil, drop)
	proposal, err := sp.Propose(context.Background(), parentHash)
	if err != nil {
		t.Fatal(err)
	}

	var stop, sawUnbound atomic.Bool
	var scans atomic.Int64
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanStore := newMemStore()
		syncer := filler.NewSyncer(realScanSource{dir: drop}, scanStore, testLayout(drop), time.Now, discardLog())
		close(started)
		for !stop.Load() {
			if _, syncErr := syncer.Sync(context.Background()); syncErr != nil {
				continue
			}
			scans.Add(1)
			for _, clip := range scanStore.clips {
				if clip.Hash != parentFileHash && clip.ParentHash == "" {
					sawUnbound.Store(true)
				}
			}
		}
	}()
	<-started
	if _, err := sp.Confirm(context.Background(), proposal.ID, proposal.Segments); err != nil {
		t.Fatal(err)
	}
	stop.Store(true)
	<-done
	if scans.Load() == 0 {
		t.Fatal("concurrent scanner did not complete an observation")
	}
	if sawUnbound.Load() {
		t.Fatal("concurrent scan observed child media without durable lineage")
	}
}

func TestConfirm_DurableFailureCannotExposeAnAirablePartialGeneration(t *testing.T) {
	for _, operation := range []string{"catalog", "tag", "enrollment", "composite", "pipeline filing", "generation", "parent filing", "proposal", "partial proposal"} {
		t.Run(operation, func(t *testing.T) {
			base := newSplitMemStore()
			drop := t.TempDir()
			parentFull := filepath.Join(drop, "parent.mp4")
			if err := os.WriteFile(parentFull, []byte("honest parent bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			parentHash, err := filler.ClipID(parentFull)
			if err != nil {
				t.Fatal(err)
			}
			base.clips[parentHash] = filler.StoreClip{Clip: filler.Clip{
				Hash: parentHash, Path: "parent.mp4", Name: "Parent", DurationMs: 60_000, Held: true,
			}}
			failOn := 1
			if operation == "catalog" || operation == "tag" || operation == "enrollment" {
				failOn = 2
			}
			store := &failingSplitStore{splitMemStore: base, operation: operation, failOn: failOn}
			tools := &fakeTools{chapters: []filler.Chapter{
				{StartMs: 0, EndMs: 30_000, Title: "First"},
				{StartMs: 30_000, EndMs: 60_000, Title: "Second"},
			}}
			sp := filler.NewSplitter(store, tools, nil, drop, func() time.Duration { return 0 },
				func() string { return "generation-failure" }, time.Now, discardLog())
			proposal, err := sp.Propose(context.Background(), parentHash)
			if err != nil {
				t.Fatal(err)
			}
			segments := append([]filler.SplitSegment(nil), proposal.Segments...)
			for i := range segments {
				segments[i].Tags = []string{"toys"}
			}

			var confirmErr error
			if operation == "partial proposal" {
				_, confirmErr = sp.ConfirmSome(context.Background(), proposal.ID, segments[:1], segments[1:])
			} else {
				_, confirmErr = sp.Confirm(context.Background(), proposal.ID, segments)
			}
			if confirmErr == nil {
				t.Fatalf("Confirm succeeded despite injected %s failure", operation)
			}
			if operation == "partial proposal" {
				for hash, pipeline := range base.pipelines {
					if hash != parentHash && pipeline.Disposition == filler.DispositionRunning {
						t.Fatalf("failed partial confirmation left child %s runnable: %+v", hash, pipeline)
					}
				}
			}
			if _, statErr := os.Stat(parentFull); statErr != nil {
				t.Fatalf("parent source was lost: %v", statErr)
			}
			scanStore := newMemStore()
			syncer := filler.NewSyncer(realScanSource{dir: drop}, scanStore, testLayout(drop), time.Now, discardLog())
			if _, err := syncer.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			for hash, clip := range scanStore.clips {
				if hash != parentHash {
					t.Fatalf("%s failure left a scan-visible partial child %+v", operation, clip)
				}
			}
		})
	}
}

func TestConfirm_ConcurrentSyncDuringGenerationFailureSeesOnlyBoundHeldChildrenThenRollback(t *testing.T) {
	base := newSplitMemStore()
	store := &blockingGenerationStore{
		splitMemStore: base,
		reached:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	drop := t.TempDir()
	parentFull := filepath.Join(drop, "parent.mp4")
	if err := os.WriteFile(parentFull, []byte("honest parent bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	parentHash, err := filler.ClipID(parentFull)
	if err != nil {
		t.Fatal(err)
	}
	base.clips[parentHash] = filler.StoreClip{Clip: filler.Clip{
		Hash: parentHash, Path: "parent.mp4", Name: "Parent", DurationMs: 60_000, Held: true,
	}}
	tools := &fakeTools{chapters: []filler.Chapter{
		{StartMs: 0, EndMs: 30_000, Title: "First"},
		{StartMs: 30_000, EndMs: 60_000, Title: "Second"},
	}}
	sp := filler.NewSplitter(store, tools, nil, drop, func() time.Duration { return 0 },
		func() string { return "concurrent-generation-failure" }, time.Now, discardLog())
	proposal, err := sp.Propose(context.Background(), parentHash)
	if err != nil {
		t.Fatal(err)
	}

	confirmDone := make(chan error, 1)
	go func() {
		_, confirmErr := sp.Confirm(context.Background(), proposal.ID, proposal.Segments)
		confirmDone <- confirmErr
	}()
	<-store.reached
	released := false
	defer func() {
		if !released {
			close(store.release)
		}
	}()

	scanStore := newMemStore()
	syncer := filler.NewSyncer(realScanSource{dir: drop}, scanStore, testLayout(drop), time.Now, discardLog())
	if _, err := syncer.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	children := 0
	for hash, clip := range scanStore.clips {
		if hash == parentHash {
			continue
		}
		children++
		if clip.ParentHash != parentHash || !clip.Held {
			t.Fatalf("concurrent Sync observed unbound or airable pre-selection child: %+v", clip)
		}
	}
	if children != 2 {
		t.Fatalf("concurrent Sync observed %d bound held children, want 2", children)
	}
	close(store.release)
	released = true
	if err := <-confirmDone; err == nil {
		t.Fatal("Confirm succeeded despite generation failure")
	}
	if _, err := syncer.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	for hash, clip := range scanStore.clips {
		if hash != parentHash {
			t.Fatalf("failed generation left replacement media scan-visible after rollback: %+v", clip)
		}
	}
}

func TestConfirm_LaterCutFailurePublishesNoGeneration(t *testing.T) {
	base := newSplitMemStore()
	drop := t.TempDir()
	parentFull := filepath.Join(drop, "parent.mp4")
	if err := os.WriteFile(parentFull, []byte("honest parent bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	parentHash, err := filler.ClipID(parentFull)
	if err != nil {
		t.Fatal(err)
	}
	base.clips[parentHash] = filler.StoreClip{Clip: filler.Clip{
		Hash: parentHash, Path: "parent.mp4", Name: "Parent", DurationMs: 60_000, Held: true,
	}}
	cuts := 0
	tools := &fakeTools{chapters: []filler.Chapter{
		{StartMs: 0, EndMs: 30_000, Title: "First"},
		{StartMs: 30_000, EndMs: 60_000, Title: "Second"},
	}}
	tools.cutFn = func(_ string, start, end int64, out string) error {
		cuts++
		if cuts == 2 {
			return errors.New("later cut failed")
		}
		return os.WriteFile(out, []byte(fmt.Sprintf("cut %d-%d", start, end)), 0o600)
	}
	sp := newSplitter(base, tools, nil, drop)
	proposal, err := sp.Propose(context.Background(), parentHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Confirm(context.Background(), proposal.ID, proposal.Segments); err == nil {
		t.Fatal("Confirm succeeded despite later cut failure")
	}
	scanStore := newMemStore()
	if _, err := filler.NewSyncer(realScanSource{dir: drop}, scanStore, testLayout(drop), time.Now, discardLog()).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(scanStore.clips) != 1 {
		t.Fatalf("later cut failure exposed a partial generation: %+v", scanStore.clips)
	}
}

func TestConfirm_MediaDeleteFailureRetainsOwnerLineageAndRebuildsHeld(t *testing.T) {
	ctx := context.Background()
	base := newSplitMemStore()
	drop := t.TempDir()
	parentFull := filepath.Join(drop, "parent.mp4")
	if err := os.WriteFile(parentFull, []byte("honest parent bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	parentHash, err := filler.ClipID(parentFull)
	if err != nil {
		t.Fatal(err)
	}
	base.clips[parentHash] = filler.StoreClip{Clip: filler.Clip{
		Hash: parentHash, Path: "parent.mp4", Name: "Parent", DurationMs: 30_000, Held: true,
	}}
	stageParentForSplitReview(base, parentHash)
	proposal := filler.SplitProposal{
		ID: "delete-denied", ClipHash: parentHash, CreatedAt: time.Now(),
		Segments: []filler.SplitSegment{{StartMs: 0, EndMs: 30_000, Name: "one"}},
	}
	if err := base.UpsertSplitProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	expectedBody := []byte("cut 0-30000")
	expected := filepath.Join(t.TempDir(), "expected.mp4")
	if err := os.WriteFile(expected, expectedBody, 0o600); err != nil {
		t.Fatal(err)
	}
	childHash, err := filler.ClipID(expected)
	if err != nil {
		t.Fatal(err)
	}
	childFull, err := filler.ClipPath(drop, childHash, ".mp4")
	if err != nil {
		t.Fatal(err)
	}
	store := &failingSplitStore{splitMemStore: base, operation: "generation"}
	store.before = func(operation string) error {
		if operation != "generation" {
			return nil
		}
		if err := os.Remove(childFull); err != nil {
			return err
		}
		if err := os.Mkdir(childFull, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(childFull, "open-handle"), []byte("delete denied"), 0o600)
	}
	sp := filler.NewSplitter(store, &fakeTools{}, nil, drop, func() time.Duration { return 0 },
		func() string { return "delete-owner" }, time.Now, discardLog())
	if _, err := sp.Confirm(ctx, proposal.ID, proposal.Segments); err == nil {
		t.Fatal("Confirm succeeded despite generation failure")
	}
	tags, state := filler.ReadSidecarTagsState(childFull)
	if state != filler.SidecarValid || tags.ConditioningLineage == nil || tags.SplitPublicationToken != "delete-owner" {
		t.Fatalf("delete-denied rollback lost quarantine evidence: state=%v tags=%+v", state, tags)
	}

	// Model the Windows handle being released: the same surviving media bytes become readable
	// again, and the retained sidecar must reconstruct them as a held child rather than ordinary.
	if err := os.Remove(filepath.Join(childFull, "open-handle")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(childFull); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childFull, expectedBody, 0o600); err != nil {
		t.Fatal(err)
	}
	rebuilt := newMemStore()
	if _, err := filler.NewSyncer(realScanSource{dir: drop}, rebuilt, testLayout(drop), time.Now, discardLog()).Sync(ctx); err != nil {
		t.Fatal(err)
	}
	child, found := rebuilt.clips[childHash]
	if !found || !child.Held || child.ParentHash != parentHash {
		t.Fatalf("delete-denied survivor rebuilt as airable or ordinary: %+v, found=%v", child, found)
	}
}

func TestConfirm_ReSplitReplacesOldChildrenOnlyWhenComplete(t *testing.T) {
	st := newSplitMemStore()
	parentHash := seedCompilation(st, "comps/resplit.mp4", 60_000)
	drop := t.TempDir()
	if err := os.MkdirAll(filepath.Join(drop, "comps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drop, "comps", "resplit.mp4"), []byte("reel"), 0o644); err != nil {
		t.Fatal(err)
	}
	parentHash = bindCompilationIdentity(t, st, parentHash, filepath.Join(drop, "comps", "resplit.mp4"))
	stageParentForSplitReview(st, parentHash)
	sp := newSplitter(st, &fakeTools{}, nil, drop)

	oldProposal := filler.SplitProposal{
		ID: "old", ClipHash: parentHash, CreatedAt: time.Now(),
		Segments: []filler.SplitSegment{
			{StartMs: 0, EndMs: 30_000, Name: "old one"},
			{StartMs: 30_000, EndMs: 60_000, Name: "old two"},
		},
	}
	if err := st.UpsertSplitProposal(context.Background(), oldProposal); err != nil {
		t.Fatal(err)
	}
	oldHashes, err := sp.Confirm(context.Background(), oldProposal.ID, oldProposal.Segments)
	if err != nil {
		t.Fatal(err)
	}

	newProposal := filler.SplitProposal{
		ID: "new", ClipHash: parentHash, CreatedAt: time.Now().Add(time.Second),
		Segments: []filler.SplitSegment{
			{StartMs: 0, EndMs: 20_000, Name: "new one"},
			{StartMs: 20_000, EndMs: 40_000, Name: "new two"},
			{StartMs: 40_000, EndMs: 60_000, Name: "new three"},
		},
	}
	if err := st.UpsertSplitProposal(context.Background(), newProposal); err != nil {
		t.Fatal(err)
	}
	parent := st.clips[parentHash]
	parent.Held = true
	st.clips[parentHash] = parent
	stageParentForSplitReview(st, parentHash)
	first, err := sp.ConfirmSome(context.Background(), newProposal.ID, newProposal.Segments[:1], newProposal.Segments[1:])
	if err != nil {
		t.Fatal(err)
	}
	for _, hash := range first {
		pipeline, found := st.pipelines[hash]
		if !found || pipeline.Disposition != filler.DispositionRunning {
			t.Fatalf("committed partial child pipeline %s = %+v, found=%v; want running", hash, pipeline, found)
		}
	}
	if len(first) != 1 || st.clips[first[0]].RemovedAt.IsZero() {
		t.Fatalf("partial re-split child = %v / %+v, want a tombstone until the generation is complete", first, st.clips[first[0]])
	}
	if !st.clips[parentHash].Held {
		t.Error("partial confirmation filed the parent before its proposal was resolved")
	}
	for _, hash := range oldHashes {
		if !st.clips[hash].RemovedAt.IsZero() {
			t.Errorf("old generation %s retired before the replacement was complete", hash)
		}
	}
	persisted := st.proposals[newProposal.ID]
	if len(persisted.Spawned) != 1 || persisted.Spawned[0] != first[0] {
		t.Fatalf("proposal spawned state = %v, want first partial child", persisted.Spawned)
	}

	last, err := sp.Confirm(context.Background(), newProposal.ID, persisted.Segments)
	if err != nil {
		t.Fatal(err)
	}
	for _, hash := range oldHashes {
		if st.clips[hash].RemovedAt.IsZero() {
			t.Errorf("superseded old child %s remained airable after final confirm", hash)
		}
	}
	for _, hash := range append(first, last...) {
		if !st.clips[hash].RemovedAt.IsZero() {
			t.Errorf("replacement child %s stayed tombstoned after final confirm", hash)
		}
	}
	if _, ok := st.proposals[newProposal.ID]; ok {
		t.Error("completed re-split proposal survived final confirm")
	}
	if st.clips[parentHash].Held {
		t.Error("final re-split confirmation left the parent held")
	}
}

func TestConfirm_ReSplitFailureKeepsTheSelectedGenerationAndPublishesNoReplacement(t *testing.T) {
	for _, tc := range []struct {
		name             string
		operation        string
		failOn           int
		publicationIndex int
	}{
		{name: "second child catalog write", operation: "catalog", failOn: 2, publicationIndex: -1},
		{name: "second child tag write", operation: "tag", failOn: 2, publicationIndex: -1},
		{name: "second child pipeline enrollment", operation: "enrollment", failOn: 2, publicationIndex: -1},
		{name: "parent composite transition inside atomic completion", operation: "composite", publicationIndex: -1},
		{name: "parent pipeline filing inside atomic completion", operation: "pipeline filing", publicationIndex: -1},
		{name: "parent filing inside atomic completion", operation: "parent filing", publicationIndex: -1},
		{name: "proposal deletion inside atomic completion", operation: "proposal", publicationIndex: -1},
		{name: "first media publication", publicationIndex: 0},
		{name: "second media publication", publicationIndex: 1},
		{name: "final generation selection", operation: "generation", publicationIndex: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			base := newSplitMemStore()
			drop := t.TempDir()
			parentFull := filepath.Join(drop, "parent.mp4")
			if err := os.WriteFile(parentFull, []byte("retained compilation bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			parentHash, err := filler.ClipID(parentFull)
			if err != nil {
				t.Fatal(err)
			}
			base.clips[parentHash] = filler.StoreClip{Clip: filler.Clip{
				Hash: parentHash, Path: "parent.mp4", Name: "Parent", DurationMs: 60_000, Held: true,
			}}
			stageParentForSplitReview(base, parentHash)
			sp := newSplitter(base, &fakeTools{}, nil, drop)
			oldProposal := filler.SplitProposal{ID: "old", ClipHash: parentHash, CreatedAt: time.Now(), Segments: []filler.SplitSegment{
				{StartMs: 0, EndMs: 30_000, Name: "old one"},
				{StartMs: 30_000, EndMs: 60_000, Name: "old two"},
			}}
			if err := base.UpsertSplitProposal(ctx, oldProposal); err != nil {
				t.Fatal(err)
			}
			oldHashes, err := sp.Confirm(ctx, oldProposal.ID, oldProposal.Segments)
			if err != nil {
				t.Fatal(err)
			}

			newSegments := []filler.SplitSegment{
				{StartMs: 0, EndMs: 20_000, Name: "new one", Tags: []string{"toys"}},
				{StartMs: 20_000, EndMs: 60_000, Name: "new two", Tags: []string{"toys"}},
			}
			newProposal := filler.SplitProposal{ID: "new", ClipHash: parentHash, CreatedAt: time.Now().Add(time.Second), Segments: newSegments}
			if err := base.UpsertSplitProposal(ctx, newProposal); err != nil {
				t.Fatal(err)
			}
			parent := base.clips[parentHash]
			parent.Held = true
			base.clips[parentHash] = parent
			parentPipeline := filler.ClipPipeline{
				ClipHash: parentHash, Stage: filler.StageSplit, Status: filler.StatusQueued,
				Disposition: filler.DispositionReview, NextRun: time.Now().Add(time.Hour),
				EnrolledAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Minute),
			}
			base.pipelines[parentHash] = parentPipeline

			newHashes := make([]string, 0, len(newSegments))
			newPaths := make([]string, 0, len(newSegments))
			for _, segment := range newSegments {
				expected := filepath.Join(t.TempDir(), "expected.mp4")
				if err := os.WriteFile(expected, []byte(fmt.Sprintf("cut %d-%d", segment.StartMs, segment.EndMs)), 0o600); err != nil {
					t.Fatal(err)
				}
				hash, err := filler.ClipID(expected)
				if err != nil {
					t.Fatal(err)
				}
				full, err := filler.ClipPath(drop, hash, ".mp4")
				if err != nil {
					t.Fatal(err)
				}
				newHashes = append(newHashes, hash)
				newPaths = append(newPaths, full)
			}

			failing := &failingSplitStore{splitMemStore: base, operation: tc.operation, failOn: tc.failOn}
			if tc.publicationIndex >= 0 {
				if err := os.MkdirAll(filepath.Dir(newPaths[tc.publicationIndex]), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(newPaths[tc.publicationIndex], 0o700); err != nil {
					t.Fatal(err)
				}
			}
			sp = newSplitter(failing, &fakeTools{}, nil, drop)
			if _, err := sp.Confirm(ctx, newProposal.ID, newSegments); err == nil {
				t.Fatalf("Confirm succeeded despite injected %s failure", tc.name)
			}
			if got, err := base.GetSplitProposal(ctx, newProposal.ID); err != nil || !reflect.DeepEqual(got, newProposal) {
				t.Errorf("failed confirmation consumed or changed proposal: got %+v, err %v, want %+v", got, err, newProposal)
			}
			if got := base.clips[parentHash]; got.Held != true || got.IsComposite != true {
				// This is a re-split, so the retained parent was already composite before this attempt.
				t.Errorf("failed confirmation changed retained parent state: %+v", got)
			}
			if got := base.pipelines[parentHash]; !reflect.DeepEqual(got, parentPipeline) {
				t.Errorf("failed confirmation changed parent pipeline: got %+v, want %+v", got, parentPipeline)
			}

			for _, hash := range oldHashes {
				if !base.clips[hash].RemovedAt.IsZero() {
					t.Errorf("selected old child %s was retired", hash)
				}
			}
			for _, hash := range newHashes {
				if child, found := base.clips[hash]; found && child.RemovedAt.IsZero() {
					t.Errorf("replacement child %s became selected: %+v", hash, child)
				}
				if pipeline, found := base.pipelines[hash]; found && pipeline.Disposition == filler.DispositionRunning {
					t.Errorf("replacement child %s retained runnable pipeline after rollback: %+v", hash, pipeline)
				}
			}
			scanStore := newMemStore()
			if _, err := filler.NewSyncer(realScanSource{dir: drop}, scanStore, testLayout(drop), time.Now, discardLog()).Sync(ctx); err != nil {
				t.Fatal(err)
			}
			for _, hash := range newHashes {
				if child, found := scanStore.clips[hash]; found {
					t.Errorf("replacement media remained scan-visible: %+v", child)
				}
			}
		})
	}
}

type cancelAfterErrChecks struct {
	context.Context
	cancel  context.CancelFunc
	checks  int
	trigger int
}

func (c *cancelAfterErrChecks) Err() error {
	c.checks++
	if c.checks == c.trigger {
		c.cancel()
		return nil
	}
	return c.Context.Err()
}

func TestConfirm_CancellationAfterFinalExactComparisonPublishesAndWritesNothing(t *testing.T) {
	base := newSplitMemStore()
	drop := t.TempDir()
	parentFull := filepath.Join(drop, "parent.mp4")
	if err := os.WriteFile(parentFull, []byte("retained compilation bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	parentHash, err := filler.ClipID(parentFull)
	if err != nil {
		t.Fatal(err)
	}
	base.clips[parentHash] = filler.StoreClip{Clip: filler.Clip{
		Hash: parentHash, Path: "parent.mp4", Name: "Parent", DurationMs: 30_000, Held: true,
	}}
	proposal := filler.SplitProposal{ID: "cancel", ClipHash: parentHash, CreatedAt: time.Now(), Segments: []filler.SplitSegment{
		{StartMs: 0, EndMs: 30_000, Name: "child"},
	}}
	if err := base.UpsertSplitProposal(context.Background(), proposal); err != nil {
		t.Fatal(err)
	}
	counting := &failingSplitStore{splitMemStore: base}
	inner, cancel := context.WithCancel(context.Background())
	// Each complete-byte comparison checks before its one small-file read and again after the
	// loop. Trigger on the final comparison's post-loop check: initial pre/post are 1/2 and final
	// pre/post are 3/4. The next sidecar-preparation check must observe the real cancellation.
	ctx := &cancelAfterErrChecks{Context: inner, cancel: cancel, trigger: 4}
	_, err = newSplitter(counting, &fakeTools{}, nil, drop).Confirm(ctx, proposal.ID, proposal.Segments)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Confirm error = %v, want context cancellation", err)
	}
	for _, operation := range []string{"catalog", "tag", "enrollment", "composite", "generation", "parent filing", "proposal"} {
		if counting.calls[operation] != 0 {
			t.Errorf("%s writes after cancellation = %d, want zero", operation, counting.calls[operation])
		}
	}
	if _, err := base.GetSplitProposal(context.Background(), proposal.ID); err != nil {
		t.Fatalf("proposal consumed after cancellation: %v", err)
	}
	expected := filepath.Join(t.TempDir(), "expected.mp4")
	if err := os.WriteFile(expected, []byte("cut 0-30000"), 0o600); err != nil {
		t.Fatal(err)
	}
	childHash, err := filler.ClipID(expected)
	if err != nil {
		t.Fatal(err)
	}
	child, err := filler.ClipPath(drop, childHash, ".mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(child); !os.IsNotExist(err) {
		t.Fatalf("child media published after cancellation: %v", err)
	}
	if _, err := os.Stat(mediatools.SidecarPathFor(child)); !os.IsNotExist(err) {
		t.Fatalf("child sidecar published after cancellation: %v", err)
	}
}

func TestConfirm_StaleRollbackCannotDeleteRecoveredWinnersMedia(t *testing.T) {
	ctx := context.Background()
	base := newSplitMemStore()
	store := &recoveringClaimStore{
		splitMemStore: base, firstAtPublication: make(chan struct{}), releaseFirst: make(chan struct{}),
	}
	drop := t.TempDir()
	parentFull := filepath.Join(drop, "parent.mp4")
	if err := os.WriteFile(parentFull, []byte("retained compilation bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	parentHash, err := filler.ClipID(parentFull)
	if err != nil {
		t.Fatal(err)
	}
	base.clips[parentHash] = filler.StoreClip{Clip: filler.Clip{
		Hash: parentHash, Path: "parent.mp4", Name: "Parent", DurationMs: 30_000, Held: true,
	}}
	stageParentForSplitReview(base, parentHash)
	proposal := filler.SplitProposal{
		ID: "proposal-recovery", ClipHash: parentHash, CreatedAt: time.Now(),
		Segments: []filler.SplitSegment{{StartMs: 0, EndMs: 30_000, Name: "one"}},
	}
	if err := base.UpsertSplitProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	first := filler.NewSplitter(store, &fakeTools{}, nil, drop,
		func() time.Duration { return 10 * time.Second }, func() string { return "owner-a" }, time.Now, nil)
	second := filler.NewSplitter(store, &fakeTools{}, nil, drop,
		func() time.Duration { return 10 * time.Second }, func() string { return "owner-b" }, time.Now, nil)

	firstErr := make(chan error, 1)
	go func() {
		_, err := first.Confirm(ctx, proposal.ID, proposal.Segments)
		firstErr <- err
	}()
	select {
	case <-store.firstAtPublication:
	case <-time.After(5 * time.Second):
		t.Fatal("first confirmer did not reach the pre-publication fence")
	}
	store.recover.Store(true)
	winner, err := second.Confirm(ctx, proposal.ID, proposal.Segments)
	if err != nil || len(winner) != 1 {
		t.Fatalf("recovered confirmer = %v, %v", winner, err)
	}
	final, err := filler.ClipPath(drop, winner[0], ".mp4")
	if err != nil {
		t.Fatal(err)
	}
	close(store.releaseFirst)
	if err := <-firstErr; !errors.Is(err, filler.ErrProposalClaimed) && !errors.Is(err, filler.ErrProposalGone) {
		t.Fatalf("stale confirmer = %v, want fenced error", err)
	}
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("stale rollback removed recovered winner media: %v", err)
	}
	if tags, ok := filler.ReadSidecarTags(final); !ok || tags.ConditioningLineage == nil || tags.ConditioningLineage.ChildHash != winner[0] {
		t.Fatalf("winner sidecar was removed or changed: %+v, ok=%v", tags, ok)
	}
}

// Confirm REJECTS a gutted compilation and overlapping cuts (§10 — the write
// path is where the review gate's teeth are).
func TestConfirm_ValidatesTheEdit(t *testing.T) {
	st := newSplitMemStore()
	hash := seedCompilation(st, "comps/1987.mp4", 61_000)
	tools := &fakeTools{}
	sp := newSplitter(st, tools, nil, t.TempDir())
	if _, err := sp.Propose(context.Background(), hash); err != nil {
		t.Fatal(err)
	}
	var propID string
	for id := range st.proposals {
		propID = id
	}

	if _, err := sp.Confirm(context.Background(), propID, nil); err == nil {
		t.Error("zero-segment confirm accepted — the compilation would be gutted")
	}
	overlap := []filler.SplitSegment{
		{StartMs: 0, EndMs: 31000, Name: "a"},
		{StartMs: 30000, EndMs: 61000, Name: "b"},
	}
	if _, err := sp.Confirm(context.Background(), propID, overlap); err == nil {
		t.Error("overlapping confirm accepted")
	}
	// Nothing was written on the failures.
	if len(st.clips) != 1 || len(tools.cutCalls) != 0 {
		t.Errorf("a rejected confirm still wrote: clips=%+v cuts=%v", st.clips, tools.cutCalls)
	}
}
