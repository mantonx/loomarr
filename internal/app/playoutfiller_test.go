package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/playout"
	"github.com/loomarr/loomarr/internal/schedule"
)

// stubCycle returns a fixed lineup: one program then a break gap.
type stubCycle struct{ slots []schedule.Slot }

func (s stubCycle) CyclePreview(context.Context, string, time.Time) (
	time.Time, []schedule.Slot, schedule.ActiveRuleAttribution, time.Duration, error,
) {
	return time.Time{}, s.slots, schedule.ActiveRuleAttribution{}, 0, nil
}

type stubPods struct {
	pod filler.Pod
	err error
}

func (s stubPods) Preview(context.Context, string) (filler.Pod, error) { return s.pod, s.err }
func (s stubPods) PreviewDraft(context.Context, string, filler.Selection) (filler.Pod, error) {
	return s.pod, s.err
}

func (s stubPods) PreviewAt(context.Context, string, int64) (filler.Pod, error) {
	return s.pod, s.err
}

func (s stubPods) Coverage(context.Context, string) (filler.CoverageReport, error) {
	return filler.CoverageReport{}, s.err
}

func (s stubPods) CoverageDraft(ctx context.Context, _ string, _ filler.Selection) (filler.CoverageReport, error) {
	if err := ctx.Err(); err != nil {
		return filler.CoverageReport{}, err
	}
	return filler.CoverageReport{}, s.err
}

func (s stubPods) ClipFit(context.Context, string) (map[string]filler.Fit, error) {
	return nil, s.err
}

func (s stubPods) Pool(context.Context) (filler.PoolReport, error) {
	return filler.PoolReport{}, s.err
}

// clipAt builds the SHARD-relative location a clip row carries since V38c — `a3/f9/<hash>.mp4`.
//
// ⚠ These fixtures used bare names like `1994/toys.mp4`, which `ClipPath`'s allow-list now
// refuses. That refusal is not pedantry: playout hands a pod entry's path straight to ClipPath,
// so an unresolvable one makes the break fall back to flex and the channel plays SILENCE. These
// tests caught exactly that when identity changed, which is what they are for.
func clipAt(name string) string {
	sum := sha256.Sum256([]byte(name))
	return filler.ClipRelPath(hex.EncodeToString(sum[:]), ".mp4")
}

func fillerResolver(t *testing.T, dir string, pod filler.Pod) *playoutResolver {
	t.Helper()
	slots := []schedule.Slot{
		{Kind: schedule.SlotFiller},
		{Kind: schedule.SlotProgram, LibraryItemID: "x", DurationMs: 60000},
	}
	accepted := &stubChannels{}
	accepted.ch.Desired = slots
	return &playoutResolver{
		// A break gap FIRST, so a clock at the epoch lands inside it.
		engine:         stubCycle{slots: slots},
		channels:       accepted,
		now:            func() time.Time { return testPlayoutAnchor() },
		tier:           func() string { return "balanced" },
		encoder:        func() string { return "" },
		capacity:       func() int { return 4 },
		activeChannels: func() int { return 0 },
		pods:           stubPods{pod: pod},
		fillerDir:      dir,
	}
}

func testPlayoutAnchor() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// THE DEFECT THIS CLOSES: the finite ffmpeg child asks AiringNow again at every programme EOF.
// AiringNow used to rebuild the authoring preview on each request, after recording the outgoing
// programme in airing history. Recency then produced a different deck, so the same wall clock
// landed in the middle of an unrelated episode. The reconciler had already persisted the accepted
// deck; playout simply was not reading it.
func TestAiringNow_ReadsThePersistedAcceptedCycle(t *testing.T) {
	t.Parallel()
	preview := &countingCycle{slots: []schedule.Slot{{
		Kind: schedule.SlotProgram, Title: "mutable preview", LibraryItemID: "preview", DurationMs: 60000,
	}}}
	accepted := &stubChannels{}
	accepted.ch.Desired = []schedule.Slot{{
		Kind: schedule.SlotFlex, Title: "accepted broadcast", DurationMs: 60000,
	}}
	r := &playoutResolver{
		engine: preview, channels: accepted,
		now: func() time.Time { return testPlayoutAnchor() },
	}

	airing, _, err := r.AiringNow(context.Background(), "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if airing.Title != "accepted broadcast" {
		t.Fatalf("airing = %q, want persisted accepted broadcast", airing.Title)
	}
	if got := preview.count(); got != 0 {
		t.Fatalf("CyclePreview called %d times, want 0 on the broadcast path", got)
	}
}

func TestAiringNow_FreshChannelStartsAtPersistedActivation(t *testing.T) {
	t.Parallel()
	anchor := time.Unix(1_800_000_000, 0).UTC()
	accepted := &stubChannels{}
	accepted.ch.ID = "fresh"
	accepted.ch.PlayoutAnchor = anchor
	accepted.ch.Desired = []schedule.Slot{
		{Kind: schedule.SlotFlex, Title: "first", DurationMs: int64((30 * time.Minute) / time.Millisecond)},
		{Kind: schedule.SlotFlex, Title: "second", DurationMs: int64((30 * time.Minute) / time.Millisecond)},
	}
	r := &playoutResolver{channels: accepted, now: func() time.Time { return anchor.Add(time.Second) }}

	airing, _, err := r.AiringNow(context.Background(), "fresh")
	if err != nil {
		t.Fatal(err)
	}
	if airing.Title != "first" || airing.Offset != time.Second {
		t.Fatalf("first tune = %q at %v, want first at 1s", airing.Title, airing.Offset)
	}
}

// THE GAP THIS CLOSES: a break must resolve to a real commercial FILE. Tunarr used to do this
// via filler-lists; internal playout has no such negotiator, so without this every break was
// dead air (the offline card).
func TestAiringNow_BreakResolvesToACommercialFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pod := filler.Pod{Entries: []filler.PodEntry{
		{Path: clipAt("bumper.mp4"), Name: "bumper", DurationMs: 4000},
		{Path: clipAt("1994/toys.mp4"), Name: "toys", DurationMs: 8000},
	}}
	r := fillerResolver(t, dir, pod)

	airing, src, err := r.AiringNow(context.Background(), "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if !airing.Playable() {
		t.Fatalf("a break with a full pod was not playable: %+v", airing)
	}
	if src == "" {
		t.Fatal("no ffmpeg input for the commercial")
	}
	// The FIRST pod entry, since the clock is at the start of the break.
	if airing.Title != "bumper" {
		t.Errorf("title = %q, want the first pod entry", airing.Title)
	}
	if airing.Remaining != 4000*time.Millisecond {
		t.Errorf("remaining = %v, want the clip's own duration", airing.Remaining)
	}
	// Source is what makes it playable — a clip has no LibraryItemID.
	if airing.Source == "" {
		t.Error("Source is empty, so Playable() would be false for every commercial")
	}
	if airing.LibraryItemID != "" {
		t.Errorf("a local clip must not carry a library id: %q", airing.LibraryItemID)
	}
	if airing.Kind != schedule.SlotFiller {
		t.Errorf("kind = %q, want filler preserved through playout", airing.Kind)
	}
	if airing.ScheduleBlockID == "" {
		t.Fatal("commercial airing has no schedule block identity")
	}
}

// The pod is a SEQUENCE, so the resolver must pick whichever clip covers this instant —
// otherwise every viewer sees the bumper on loop and the ads never play.
func TestAiringNow_BreakWalksThePodByOffset(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pod := filler.Pod{Entries: []filler.PodEntry{
		{Path: clipAt("bumper.mp4"), Name: "bumper", DurationMs: 4000},
		{Path: clipAt("1994/toys.mp4"), Name: "toys", DurationMs: 8000},
		{Path: clipAt("1994/cereal.mp4"), Name: "cereal", DurationMs: 8000},
	}}
	r := fillerResolver(t, dir, pod)

	// 6s into the break: past the 4s bumper, 2s into the first ad.
	base := testPlayoutAnchor()
	r.now = func() time.Time { return base.Add(6 * time.Second) }

	airing, _, err := r.AiringNow(context.Background(), "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if airing.Title != "toys" {
		t.Errorf("title = %q, want the ad the clock is inside of", airing.Title)
	}
	if airing.Offset != 2*time.Second {
		t.Errorf("offset = %v, want 2s INTO the ad — a mid-break joiner must not restart it",
			airing.Offset)
	}
	if airing.StartedAt != base.Add(4*time.Second) {
		t.Errorf("started at = %v, want second clip boundary %v", airing.StartedAt, base.Add(4*time.Second))
	}
	firstBlockID := playout.ScheduledBlockID("ch1", base, schedule.SlotFiller, string(schedule.SlotFiller))
	if airing.ScheduleBlockID != firstBlockID {
		t.Errorf("second commercial block id = %q, want break identity %q", airing.ScheduleBlockID, firstBlockID)
	}
}

func TestAiringNow_FillerCannotCrossTheScheduledBreakBoundary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := fillerResolver(t, dir, filler.Pod{Entries: []filler.PodEntry{
		{Path: clipAt("long-ad.mp4"), Name: "long ad", DurationMs: 60_000},
	}})
	base := testPlayoutAnchor()
	r.now = func() time.Time { return base.Add(5 * time.Second) }

	airing, _, err := r.AiringNow(context.Background(), "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if airing.Offset != 5*time.Second || airing.Remaining != 25*time.Second {
		t.Fatalf("filler Airing = %+v, want offset 5s and the scheduled break's 25s remainder", airing)
	}
}

// No pool ⇒ the offline card, not an error. A channel with no commercials must still tune.
func TestAiringNow_BreakWithNoPodIsNotAnError(t *testing.T) {
	t.Parallel()
	r := fillerResolver(t, t.TempDir(), filler.Pod{})
	airing, src, err := r.AiringNow(context.Background(), "ch1")
	if err != nil {
		t.Fatalf("an empty pod was an error: %v", err)
	}
	if airing.Playable() || src != "" {
		t.Errorf("want the offline card, got %+v / %q", airing, src)
	}
	if airing.Kind != schedule.SlotFiller || airing.Remaining != 30*time.Second {
		t.Errorf("unfilled break lost its schedule identity/boundary: %+v", airing)
	}
}

// ⚠ A crafted clip id must NOT stream a file from outside FILLER_DIR. The id reaches here from
// the database, so containment is enforced at resolution, not assumed at write time.
func TestAiringNow_BreakRefusesAPathEscape(t *testing.T) {
	t.Parallel()
	pod := filler.Pod{Entries: []filler.PodEntry{
		// ⚠ The RAW traversal string, deliberately NOT run through clipAt — hashing it would
		// produce a perfectly valid id and test nothing. What must be refused is the crafted
		// value itself, as it would arrive from a tampered database row.
		{Path: "../../../../etc/passwd", Name: "evil", DurationMs: 4000},
	}}
	r := fillerResolver(t, t.TempDir(), pod)

	airing, src, err := r.AiringNow(context.Background(), "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if airing.Playable() || src != "" {
		t.Errorf("a traversal id resolved to %q — playout would stream it", src)
	}
}

// The embedded fallback bumper card is generated, not a file: it has no path to play.
func TestAiringNow_BreakWithOnlyTheFallbackCardPlaysNoFile(t *testing.T) {
	t.Parallel()
	pod := filler.Pod{Entries: []filler.PodEntry{
		// ⚠ An EMPTY path, not a hashed one: the fallback card is generated rather than played
		// from a file, and "" is how the resolver knows. Hashing it would give the card a path
		// that resolves to a file which does not exist.
		{Path: "", Name: "fallback card", DurationMs: 5000, IsFallbackCard: true},
	}}
	r := fillerResolver(t, t.TempDir(), pod)

	_, src, err := r.AiringNow(context.Background(), "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if src != "" {
		t.Errorf("the fallback card resolved to a file: %q", src)
	}
}

// --- Encoder selection (the silent-software-fallback bug) ---

// An operator override wins outright and must NOT trigger a probe: probing trial-encodes every
// candidate at ~5s apiece, which would be paid for nothing.
func TestProfile_OperatorOverrideSkipsTheProbe(t *testing.T) {
	t.Parallel()
	probed := false
	r := fillerResolver(t, t.TempDir(), filler.Pod{})
	r.encoder = func() string { return "h264_nvenc" }
	r.ffmpegPath = func() string { probed = true; return "/nonexistent/ffmpeg" }

	p := r.Profile(context.Background())
	if p.Encoder != "h264_nvenc" {
		t.Errorf("encoder = %q, want the operator's override", p.Encoder)
	}
	if probed {
		t.Error("probed despite an explicit override — that is ~20s of trial encodes for nothing")
	}
}

// With no override, the encoder is MEASURED rather than assumed. Previously this fell straight
// through to libx264 with a comment claiming the wizard had stored a choice — nothing did, so a
// box with a working GPU silently encoded in software forever.
//
// Detect never errors: "no hardware" is the expected outcome on most machines and software is a
// correct answer. So with a bogus ffmpeg path the probe finds nothing and yields libx264 — which
// is exactly the contract worth pinning.
func TestProfile_NoOverrideProbesAndFallsBackSafely(t *testing.T) {
	t.Parallel()
	r := fillerResolver(t, t.TempDir(), filler.Pod{})
	r.encoder = func() string { return "" }
	r.ffmpegPath = func() string { return "/nonexistent/ffmpeg" }

	_ = r.detectedEncoder(context.Background())
	p := r.Profile(context.Background())
	if p.Encoder != playout.EncoderSoftware {
		t.Errorf("encoder = %q, want the software fallback when nothing probes clean", p.Encoder)
	}
}

// A viewer pays only the persisted-evidence fingerprint check, never an encoder trial or the full
// machine benchmark. With no reusable evidence, software is the immediately-available fallback.
func TestProfile_NoOverrideDoesNotWaitForProbe(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	r := fillerResolver(t, t.TempDir(), filler.Pod{})
	r.encoder = func() string { return "" }
	r.loadCapabilityEvidence = func(context.Context) (playout.Capacity, bool) {
		return playout.Capacity{}, false
	}
	r.ffmpegPath = func() string {
		close(started)
		<-release
		return "/nonexistent/ffmpeg"
	}

	before := time.Now()
	p := r.Profile(context.Background())
	if elapsed := time.Since(before); elapsed > 100*time.Millisecond {
		t.Errorf("Profile waited %s for capability detection", elapsed)
	}
	if p.Encoder != playout.EncoderSoftware {
		t.Errorf("encoder = %q, want immediate software while capability detection warms", p.Encoder)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Profile did not start capability detection in the background")
	}
	close(release)
}

// The probe runs ONCE. It is called per program boundary, and trial-encoding every candidate on
// each one would make every program transition take ~20 seconds.
func TestProfile_ProbesOnlyOnce(t *testing.T) {
	t.Parallel()
	calls := 0
	r := fillerResolver(t, t.TempDir(), filler.Pod{})
	r.encoder = func() string { return "" }
	r.ffmpegPath = func() string { calls++; return "/nonexistent/ffmpeg" }

	for i := 0; i < 5; i++ {
		_ = r.detectedEncoder(context.Background())
	}
	if calls != 1 {
		t.Errorf("probed %d times across 5 programs, want 1 — each probe is ~20s", calls)
	}
}

// recordingPlays captures RecordClipPlay calls (V28).
type recordingPlays struct {
	calls    []string
	channels []string
	at       []time.Time
	err      error
}

func (r *recordingPlays) RecordClipPlay(_ context.Context, channelID, clipHash string, at time.Time) (bool, error) {
	r.calls = append(r.calls, clipHash)
	r.channels = append(r.channels, channelID)
	r.at = append(r.at, at)
	return r.err == nil, r.err
}

// V28: a clip that AIRS is counted. V41: the count identifies the clip by its HASH.
//
// ⚠ This test asserted the catalog PATH from V28 until V41, and it was right when written —
// identity was the path then. V38c moved identity to the hash and never revisited this
// assertion, so the test went on pinning the pre-V38c contract and kept `RecordClipPlay`'s
// path argument looking correct while every counter in production stayed at zero
// (`RecordClipPlay` is keyed `WHERE hash = ?`). A test that outlives the model it encodes
// stops being a guard and becomes a reason not to look.
func TestAiringNow_CountsTheClipThatAirs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pod := filler.Pod{Entries: []filler.PodEntry{
		{Path: clipAt("1994/toys.mp4"), Hash: "hash-toys", Name: "toys", DurationMs: 8000},
	}}
	r := fillerResolver(t, dir, pod)
	plays := &recordingPlays{}
	r.clipPlays = plays

	if _, _, err := r.AiringNow(context.Background(), "ch1"); err != nil {
		t.Fatal(err)
	}
	// ⚠ The HASH, not the path and not the absolute file. The counter keys on identity, so
	// anything else counts against a clip that does not exist — silently, because the caller
	// swallows the error as telemetry.
	if len(plays.calls) != 1 || plays.calls[0] != "hash-toys" {
		t.Fatalf("recorded %v, want one play of the clip HASH", plays.calls)
	}
	if len(plays.channels) != 1 || plays.channels[0] != "ch1" {
		t.Fatalf("recorded channels %v, want the airing channel", plays.channels)
	}
}

// A finite ffmpeg child asks for the next clip after the previous child exits. That request
// arrives slightly after the scheduled boundary (roughly 80-250ms on the live stack), never at
// the exact nanosecond. The exposure write must identify the scheduled clip start so later
// re-resolves can be made idempotent without requiring an impossible `into == 0` coincidence.
func TestAiringNow_RecordsClipStartWhenResolverArrivesLate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pod := filler.Pod{Entries: []filler.PodEntry{
		{Path: clipAt("1994/toys.mp4"), Hash: "hash-toys", Name: "toys", DurationMs: 8000},
	}}
	r := fillerResolver(t, dir, pod)
	plays := &recordingPlays{}
	r.clipPlays = plays

	base := testPlayoutAnchor()
	r.now = func() time.Time { return base.Add(100 * time.Millisecond) }

	if _, _, err := r.AiringNow(context.Background(), "ch1"); err != nil {
		t.Fatal(err)
	}
	if len(plays.calls) != 1 {
		t.Fatalf("recorded %d plays, want the clip that started 100ms ago recorded once", len(plays.calls))
	}
	if got := plays.at[0]; !got.Equal(base) {
		t.Fatalf("recorded at %v, want scheduled clip start %v", got, base)
	}
}

// ⚠ The bumper card has no catalog row, so it has no hash — and must not be counted. Without
// the `e.Hash != ""` guard this records a play against the empty string, which is not merely
// useless: it is an UPDATE with an attacker-independent but wrong key, and the swallowed error
// means it would never be noticed.
func TestAiringNow_DoesNotCountAnEntryWithNoHash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pod := filler.Pod{Entries: []filler.PodEntry{
		{Path: clipAt("1994/toys.mp4"), Name: "no-hash", DurationMs: 8000},
	}}
	r := fillerResolver(t, dir, pod)
	plays := &recordingPlays{}
	r.clipPlays = plays

	if _, _, err := r.AiringNow(context.Background(), "ch1"); err != nil {
		t.Fatal(err)
	}
	if len(plays.calls) != 0 {
		t.Errorf("recorded %v for an entry with no hash, want no play counted", plays.calls)
	}
}

// A mid-clip re-resolve reports the SAME scheduled start. The durable recorder owns
// idempotency, because process-local suppression would be lost on restart or another replica.
func TestAiringNow_MidClipResolveUsesTheScheduledStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pod := filler.Pod{Entries: []filler.PodEntry{
		{Path: clipAt("1994/toys.mp4"), Hash: "hash-toys", Name: "toys", DurationMs: 8000},
	}}
	r := fillerResolver(t, dir, pod)
	plays := &recordingPlays{}
	r.clipPlays = plays

	// Three seconds into the break: the clip is already rolling.
	base := testPlayoutAnchor()
	r.now = func() time.Time { return base.Add(3 * time.Second) }

	if _, _, err := r.AiringNow(context.Background(), "ch1"); err != nil {
		t.Fatal(err)
	}
	if len(plays.calls) != 1 {
		t.Fatalf("a mid-clip resolve recorded %v calls, want one idempotent observation", plays.calls)
	}
	if got := plays.at[0]; !got.Equal(base) {
		t.Errorf("mid-clip resolve recorded at %v, want scheduled start %v", got, base)
	}
}

// Counting is telemetry: a store failure must never stop a break from airing.
func TestAiringNow_ACountingFailureStillAirsTheBreak(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pod := filler.Pod{Entries: []filler.PodEntry{
		{Path: clipAt("1994/toys.mp4"), Name: "toys", DurationMs: 8000},
	}}
	r := fillerResolver(t, dir, pod)
	r.clipPlays = &recordingPlays{err: errors.New("database is gone")}

	airing, src, err := r.AiringNow(context.Background(), "ch1")
	if err != nil {
		t.Fatalf("a counting failure became a playout error: %v", err)
	}
	if !airing.Playable() || src == "" {
		t.Errorf("the break must still air: %+v src=%q", airing, src)
	}
}

// No recorder wired (Tunarr-backed, or telemetry off) is a supported configuration.
func TestAiringNow_NoRecorderIsFine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pod := filler.Pod{Entries: []filler.PodEntry{
		{Path: clipAt("1994/toys.mp4"), Name: "toys", DurationMs: 8000},
	}}
	r := fillerResolver(t, dir, pod) // clipPlays left nil
	if _, _, err := r.AiringNow(context.Background(), "ch1"); err != nil {
		t.Fatalf("a nil recorder must not break playout: %v", err)
	}
}
