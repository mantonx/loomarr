package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/filleradmission"
	"github.com/loomarr/loomarr/internal/fillerdecision"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/taxonomy"
)

// Filler (§10): clips, their lifecycle, the source registry, pulls and split proposals.
//
// ⚠ These were spread across FOUR non-contiguous regions of the old single file — clip tests
// at four separate offsets with sources, pulls and splits interleaved between them. The domain
// was always coherent; only the file ordering (historical, by when each test was written) was
// not.

// testFillerSources covers the persisted REMOTE source registry (§10, V33) on BOTH backends.
//
// The interesting assertions are the two that protect against silent data loss: a re-register
// must not reset "last fetched", and deleting a source must not take its clips with it.
// clipAt builds a catalog clip whose identity (`Hash`) is DISTINCT from its location (`Path`).
//
// ⚠ Hash and Path must never be equal here, and this is the whole point of the helper. They were
// the same string until V41, and that single fact hid two production defects for two releases:
// `DeleteClipsNotIn` wiped the entire catalog on every sync (see the post-mortem on
// `store.SetClipsRemoved`), and `UpdateClipClassification`/`RecordClipPlay` were called with a path
// against a `WHERE hash = ?` — so the AI tagger aborted on its first clip and play counters never
// moved. Every assertion passed throughout, because a fixture where the two keys are equal cannot
// tell them apart. A key-confusion bug is invisible by construction against such a fixture.
//
// ⚠ Real 64-hex hashes are still deliberately NOT used. The store does not care what a hash looks
// like — only `filler.ClipPath` validates the shape, and that is tested where it belongs
// (`filler/clippath_test.go`). A wall of hex would cost readability without buying coverage. The
// property that matters is that the two fields are DISTINGUISHABLE, not that the hash is realistic,
// so the readable path gets a short readable hash beside it.
func clipAt(path, name string, kind filler.Kind, durationMs int64) filler.Clip {
	return filler.Clip{Hash: clipHashFor(path), Path: path, Name: name, Kind: kind, DurationMs: durationMs}
}

// clipHashFor derives a fixture clip's identity from its path, and clipPathFor derives a fixture
// clip's location from its identity. The two builders in this file start from opposite ends —
// `clipAt` is given a readable path, `sampleClip` a readable id — so each needs the other half.
//
// Both are deterministic (the suite asserts exact values and runs against two backends), readable
// in failure output, and — the load-bearing property — never equal to the value they derive from.
func clipHashFor(path string) string { return "h:" + path }

func clipPathFor(hash string) string { return "p/" + hash + ".mp4" }

func sampleClip(id, name string, kind filler.Kind, era int, aud filler.Audience, cat string) Clip {
	c := Clip{}
	// ⚠ Identity is the HASH since V38c, not the path (§10).
	//
	// These tests use the READABLE id as the hash — "c1", not 64 hex characters — so assertions
	// stay legible (`GetClip(ctx, "c1")`) and a failure names a clip a human recognises. The
	// store does not care what a hash looks like; only `filler.ClipPath` validates the shape, and
	// that is covered where it belongs, in `filler/clippath_test.go`. Using real hashes here
	// would make every assertion a wall of hex and would test nothing extra.
	//
	// ⚠ But `Path` must NOT be the id as well. It was until V41, and hash-keyed and path-keyed
	// store methods became indistinguishable to this suite: `UpdateClipClassification` (`WHERE hash = ?`)
	// was being called with a path in production and every test still passed. See `clipAt` for
	// the full post-mortem. Keep the two fields distinct in every fixture, always.
	c.Hash = id
	c.Path = clipPathFor(id)
	c.TunarrProgramID = "tun-" + id
	c.Name = name
	c.Kind = kind
	c.Era = era
	c.Audience = aud
	c.Category = cat
	c.DurationMs = 30000
	c.Source = "archive"
	c.UpdatedAt = time.Unix(1_700_000_000, 0).UTC()
	return c
}

func cachedClipFingerprint(ctx context.Context, s Store, clipHash, algorithm string) ([]uint64, bool, error) {
	all, err := s.ListClipFingerprints(ctx, algorithm)
	if err != nil {
		return nil, false, err
	}
	frames, found := all[clipHash]
	return frames, found, nil
}

func ids2(clips []Clip) []string {
	out := make([]string, len(clips))
	for i, c := range clips {
		out[i] = c.Path
	}
	return out
}

// findSource returns one source by id, and whether it is still listed.
//
// ⚠ By id, not by position. V37's migration seeds two singleton rows (`folder`, `library`) so a
// fresh store can still express "not configured", which means `ListFillerSources(ctx)[0]` is no
// longer the row a test just added — it is whichever seeded row sorts first. Every positional
// read in this suite was rewritten through here after exactly that broke.
func findSource(t *testing.T, s Store, id string) (FillerSource, bool) {
	t.Helper()
	all, err := s.ListFillerSources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range all {
		if f.ID == id {
			return f, true
		}
	}
	return FillerSource{}, false
}

// src1 is the source this suite registers first, re-read. Fatals if it has gone missing, so a
// caller can assert on its fields without a nil check at every use.
func src1(t *testing.T, s Store) FillerSource {
	t.Helper()
	f, ok := findSource(t, s, "src-1")
	if !ok {
		t.Fatal("src-1 is not listed")
	}
	return f
}

func testClipFilters(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	c1 := sampleClip("c1", "Frosted Flakes", filler.Commercial, 1992, filler.Kids, "cereal")
	c1.GeographicScope, c1.Country, c1.Market = filler.GeographicLocal, "US", "New York"
	c1.Network, c1.Station, c1.AirDate, c1.GeoEvidence = "Fox", "WNYW", "1992-04-03", "filename: WNYW 1992-04-03"
	_ = s.UpsertClip(ctx, c1)
	_ = s.UpsertClip(ctx, sampleClip("c2", "TMNT figures", filler.Commercial, 1994, filler.Kids, "toys"))
	_ = s.UpsertClip(ctx, sampleClip("b1", "Bumper", filler.Bumper, 1992, filler.General, ""))
	_ = s.UpsertClip(ctx, sampleClip("u1", "untagged.mp4", filler.Commercial, 0, "", "")) // untagged

	// Round-trip.
	got, err := s.GetClip(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Frosted Flakes" || got.Kind != filler.Commercial || got.Era != 1992 || got.Audience != filler.Kids || got.Category != "cereal" {
		t.Errorf("clip round-trip mismatch: %+v", got.Clip)
	}
	if got.DurationMs != 30000 {
		t.Errorf("duration lost: %d", got.DurationMs)
	}
	if got.GeographicScope != filler.GeographicLocal || got.Country != "US" || got.Market != "New York" ||
		got.Network != "Fox" || got.Station != "WNYW" || got.AirDate != "1992-04-03" || got.GeoEvidence == "" {
		t.Errorf("geography round-trip mismatch: %+v", got.Clip)
	}
	if _, err := s.GetClip(ctx, "nope"); err != ErrNotFound {
		t.Errorf("GetClip(missing) = %v, want ErrNotFound", err)
	}

	// Filter by kind.
	comms, _ := s.ListClips(ctx, ClipFilter{Kind: filler.Commercial})
	if len(comms) != 3 {
		t.Errorf("kind=commercial = %d, want 3", len(comms))
	}
	// Filter by audience + era.
	// ⚠ Assert on Hash, not Path: identity is the hash (§10 V38c). Asserting the path here read
	// as correct only while the fixture made the two equal.
	kids92, _ := s.ListClips(ctx, ClipFilter{Audience: filler.Kids, Era: 1992})
	if len(kids92) != 1 || kids92[0].Hash != "c1" {
		t.Errorf("kids+1992 = %+v, want just c1", ids2(kids92))
	}
	ny, _ := s.ListClips(ctx, ClipFilter{GeographicScope: filler.GeographicLocal, Country: "us", Market: "new york"})
	if len(ny) != 1 || ny[0].Hash != "c1" {
		t.Errorf("US/New York geography = %+v, want just c1", ids2(ny))
	}
	if err := s.UpdateClipGeography(ctx, "c1", "national", "CA", "", "CBC", "", "", "operator", time.Now()); err != nil {
		t.Fatal(err)
	}
	updated, _ := s.GetClip(ctx, "c1")
	if updated.GeographicScope != filler.GeographicNational || updated.Country != "CA" || updated.GeoEvidence != "operator" {
		t.Errorf("updated geography = %+v", updated.Clip)
	}
	// Untagged only.
	untagged, _ := s.ListClips(ctx, ClipFilter{UntaggedOnly: true})
	if len(untagged) != 1 || untagged[0].Hash != "u1" {
		t.Errorf("untagged = %+v, want just u1", ids2(untagged))
	}
	// Empty filter = all.
	all, _ := s.ListClips(ctx, ClipFilter{})
	if len(all) != 4 {
		t.Errorf("no filter = %d, want 4", len(all))
	}
}

func testClipTagsAndPrune(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	_ = s.UpsertClip(ctx, sampleClip("u1", "untagged.mp4", filler.Commercial, 0, "", ""))
	_ = s.UpsertClip(ctx, sampleClip("keep", "keep.mp4", filler.Bumper, 1992, filler.General, ""))

	// Tag the untagged clip (the AI-tagging job path).
	if err := s.SetClipTags(ctx, "u1", []string{"cereal"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateClipClassification(ctx, "u1", 1994, "kids", 0, true, now); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetClip(ctx, "u1")
	if got.Era != 1994 || got.Audience != filler.Kids || got.Category != "cereal" || !got.AITagged {
		t.Errorf("tag update didn't persist: %+v", got.Clip)
	}
	if !got.Tagged() {
		t.Error("clip should be Tagged() after update")
	}
	// Tagging a missing clip → ErrNotFound.
	if err := s.UpdateClipClassification(ctx, "gone", 1990, "kids", 0, false, now); err != ErrNotFound {
		t.Errorf("UpdateClipClassification(missing) = %v, want ErrNotFound", err)
	}

	// Era suggestions (§10 V34) — the conditional suggested_era write:
	//  **record** an ungrounded suggestion on an era-less clip,
	//  **keep** it across a tag edit that carries neither era nor suggestion,
	//  **clear** it in the same write that sets era (the operator confirming).
	if err := s.UpdateClipClassification(ctx, "keep", 0, "family", 1985, false, now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, "keep")
	if got.SuggestedEra != 1985 || got.Era != 0 {
		t.Errorf("suggestion not recorded: era=%d suggestedEra=%d, want 0/1985", got.Era, got.SuggestedEra)
	}
	if err := s.UpdateClipClassification(ctx, "keep", 0, "general", 0, false, now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, "keep")
	if got.SuggestedEra != 1985 {
		t.Errorf("era-less tag edit wiped the suggestion: suggestedEra=%d, want 1985", got.SuggestedEra)
	}
	if err := s.UpdateClipClassification(ctx, "keep", 1985, "", 0, false, now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, "keep")
	if got.Era != 1985 || got.SuggestedEra != 0 {
		t.Errorf("confirming era did not clear the suggestion: era=%d suggestedEra=%d, want 1985/0", got.Era, got.SuggestedEra)
	}
	// A suggestion survives a sync upsert (sync.go merges it like the other tags).
	keep, _ := s.GetClip(ctx, "keep")
	keep.SuggestedEra = 1990
	if err := s.UpsertClip(ctx, keep); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, "keep")
	if got.SuggestedEra != 1990 {
		t.Errorf("suggested_era did not round-trip through UpsertClip: %d, want 1990", got.SuggestedEra)
	}

	// Prune: keep only "keep" — u1 is removed (it left the media server's library).
	n, err := s.DeleteClipsNotIn(ctx, []string{"keep"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("prune removed %d, want 1", n)
	}
	if _, err := s.GetClip(ctx, "u1"); err != ErrNotFound {
		t.Error("pruned clip still present")
	}
	if _, err := s.GetClip(ctx, "keep"); err != nil {
		t.Error("kept clip was wrongly pruned")
	}
	// Prune with empty keep set deletes all.
	n, _ = s.DeleteClipsNotIn(ctx, nil)
	if n != 1 {
		t.Errorf("prune-all removed %d, want 1", n)
	}
}

// testClipNameSearch covers the §7.2 `name LIKE` clip search. It is in the shared suite
// because the two dialects disagree by default: SQLite's LIKE folds ASCII case while
// Postgres's does not, so a naive implementation would make search case-sensitive on
// exactly one backend — the dialect fork the store rules forbid.
func testClipNameSearch(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	_ = s.UpsertClip(ctx, sampleClip("c1", "Frosted Flakes", filler.Commercial, 1992, filler.Kids, "cereal"))
	_ = s.UpsertClip(ctx, sampleClip("c2", "TMNT figures", filler.Commercial, 1994, filler.Kids, "toys"))
	_ = s.UpsertClip(ctx, sampleClip("c3", "100% Juice", filler.Commercial, 1993, filler.Kids, "drinks"))

	names := func(f ClipFilter) []string {
		t.Helper()
		got, err := s.ListClips(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(got))
		for _, c := range got {
			out = append(out, c.Name)
		}
		return out
	}

	// Substring, and case-insensitive on BOTH backends.
	if got := names(ClipFilter{Query: "flakes"}); len(got) != 1 || got[0] != "Frosted Flakes" {
		t.Errorf("Query=flakes → %v, want [Frosted Flakes] (case-insensitive on both dialects)", got)
	}
	if got := names(ClipFilter{Query: "FROSTED"}); len(got) != 1 {
		t.Errorf("Query=FROSTED → %v, want 1 match", got)
	}

	// A literal % must not act as a wildcard. Without escaping this returns everything,
	// which reads as "search is broken" and scans the whole table.
	if got := names(ClipFilter{Query: "%"}); len(got) != 1 || got[0] != "100% Juice" {
		t.Errorf("Query=%% → %v, want only [100%% Juice] — %% must be literal, not a wildcard", got)
	}
	// Likewise _, which would otherwise match any single character.
	if got := names(ClipFilter{Query: "_"}); len(got) != 0 {
		t.Errorf("Query=_ → %v, want none — _ must be literal, not a single-char wildcard", got)
	}

	// Search composes with the other filters rather than replacing them.
	if got := names(ClipFilter{Query: "e", Category: "toys"}); len(got) != 1 || got[0] != "TMNT figures" {
		t.Errorf("Query+Category → %v, want [TMNT figures]", got)
	}
}

// V28: play counters are written ONLY by RecordClipPlay, and a re-sync must not reset them.
//
// The reset is the bug worth pinning: UpsertClip lists most columns in its ON CONFLICT DO
// UPDATE, so adding play_count there would zero every counter on each sync pass — silently,
// and visible only as "usage never goes up".
func testClipPlayCounters(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	mustSaveChannel(t, s, sampleChannel("channel-1", 1, time.Time{}))

	c := sampleClip("1994/toys.mp4", "TMNT toys", filler.Commercial, 1994, filler.Kids, "toys")
	c.Thumbnail = "1994/toys.jpg"
	// ⚠ The animated preview is a SEPARATE column, not derived from the still (V39, 00035). Both
	// are asserted here because a column added to the INSERT and forgotten in the SELECT (or
	// vice versa) is a silent data loss the type system cannot see — the two lists are
	// hand-maintained and positional.
	c.Preview = "1994/toys.webp"
	// The detected language (V40, 00036) — a third hand-maintained position in the same lists.
	c.Language = "en"
	if err := s.UpsertClip(ctx, c); err != nil {
		t.Fatalf("seed clip: %v", err)
	}

	// ⚠ Read by HASH, not path. `GetClip`, `RecordClipPlay` and `UpdateClipClassification` are all keyed
	// `WHERE hash = ?`; passing a path returns ErrNotFound. This test used `c.Path` throughout
	// while the fixture made the two equal, so it could not distinguish the two keys — and the
	// production callers that passed a path went undetected for two releases (see `clipAt`).
	got, err := s.GetClip(ctx, c.Hash)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Thumbnail != "1994/toys.jpg" {
		t.Errorf("thumbnail = %q, want it round-tripped", got.Thumbnail)
	}
	if got.Preview != "1994/toys.webp" {
		t.Errorf("preview = %q, want it round-tripped — a preview that vanishes on read means "+
			"every card silently falls back to its still", got.Preview)
	}
	// ⚠ A language that vanishes on read reads as NOT YET CHECKED, so the detection job would
	// re-run forever — and on the local backend that is ~341s of QEMU per clip, every cycle.
	if got.Language != "en" {
		t.Errorf("language = %q, want it round-tripped", got.Language)
	}
	if got.PlayCount != 0 || !got.LastPlayedAt.IsZero() {
		t.Errorf("a fresh clip must start unplayed, got count=%d at=%v", got.PlayCount, got.LastPlayedAt)
	}

	aired := time.Unix(1_800_000_000, 0).UTC()
	for i := 0; i < 3; i++ {
		if _, err := s.RecordClipPlay(ctx, "channel-1", c.Hash, aired.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("record play %d: %v", i, err)
		}
	}
	lastAired := aired.Add(2 * time.Minute)
	got, _ = s.GetClip(ctx, c.Hash)
	if got.PlayCount != 3 {
		t.Errorf("play count = %d, want 3", got.PlayCount)
	}
	if !got.LastPlayedAt.Equal(lastAired) {
		t.Errorf("last played = %v, want %v", got.LastPlayedAt, lastAired)
	}

	// A re-sync (what the periodic scan does) must leave the counters alone. Everything else
	// about the row is legitimately refreshed.
	c.Name = "TMNT toys (renamed)"
	if err := s.UpsertClip(ctx, c); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	got, _ = s.GetClip(ctx, c.Hash)
	if got.Name != "TMNT toys (renamed)" {
		t.Errorf("a re-sync must refresh the name, got %q", got.Name)
	}
	if got.PlayCount != 3 {
		t.Errorf("a re-sync RESET the play count to %d — play_count must not be in the DO UPDATE list", got.PlayCount)
	}
	if !got.LastPlayedAt.Equal(lastAired) {
		t.Errorf("a re-sync reset last_played_at to %v", got.LastPlayedAt)
	}

	// Playout may resolve a clip the catalog has since pruned; that is telemetry missing a
	// row, not a playback error.
	if _, err := s.RecordClipPlay(ctx, "channel-1", "gone/missing.mp4", aired); err != nil {
		t.Errorf("recording a play for a pruned clip must not error, got %v", err)
	}
}

// V58: rotation history is channel-scoped, durable when a catalog row disappears, and queried
// through a strict break-start cutoff so a clip beginning inside a break cannot reshuffle its tail.
func testClipExposureRotation(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	mustSaveChannel(t, s, sampleChannel("channel-a", 1, time.Time{}))
	mustSaveChannel(t, s, sampleChannel("channel-b", 2, time.Time{}))
	clip := sampleClip("rotation-hash", "rotation.mp4", filler.Commercial, 1994, filler.Kids, "toys")
	if err := s.UpsertClip(ctx, clip); err != nil {
		t.Fatal(err)
	}
	first := time.UnixMilli(1_800_000_000_123).UTC()
	second := first.Add(2 * time.Second)
	if recorded, err := s.RecordClipPlay(ctx, "channel-a", clip.Hash, first); err != nil {
		t.Fatal(err)
	} else if !recorded {
		t.Fatal("first scheduled start was not reported as a new airing")
	}
	// A resolver can ask about the active clip more than once. The scheduled start is stable,
	// so a repeated write for that start is one airing, not another viewer or another play.
	if recorded, err := s.RecordClipPlay(ctx, "channel-a", clip.Hash, first); err != nil {
		t.Fatal(err)
	} else if recorded {
		t.Fatal("duplicate scheduled start was reported as another airing")
	}
	if recorded, err := s.RecordClipPlay(ctx, "channel-b", clip.Hash, second); err != nil {
		t.Fatal(err)
	} else if !recorded {
		t.Fatal("same clip on another channel was not reported as an independent airing")
	}
	if recorded, err := s.RecordClipPlay(ctx, "channel-a", clip.Hash, second); err != nil {
		t.Fatal(err)
	} else if !recorded {
		t.Fatal("later scheduled start was not reported as a new airing")
	}

	a, err := s.FillerExposuresByChannel(ctx, "channel-a", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := a[clip.Hash]; got.PlayCount != 2 || !got.LastPlayedAt.Equal(second) {
		t.Fatalf("channel-a exposure = %+v, want count 2 at %v", got, second)
	}
	b, err := s.FillerExposuresByChannel(ctx, "channel-b", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := b[clip.Hash]; got.PlayCount != 1 || !got.LastPlayedAt.Equal(second) {
		t.Fatalf("channel-b exposure = %+v, want count 1 at %v", got, second)
	}
	storedClip, err := s.GetClip(ctx, clip.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if storedClip.PlayCount != 3 {
		t.Fatalf("global clip count = %d, want the three distinct channel/start airings", storedClip.PlayCount)
	}

	beforeBreak, err := s.FillerExposuresByChannel(ctx, "channel-a", second)
	if err != nil {
		t.Fatal(err)
	}
	if got := beforeBreak[clip.Hash]; got.PlayCount != 1 || !got.LastPlayedAt.Equal(first) {
		t.Fatalf("break snapshot = %+v, want the history immediately before its cutoff", got)
	}

	// Duplicate callbacks for one scheduled start are idempotent, including when two replicas
	// race. This pins the upsert arithmetic on both SQLite and Postgres rather than relying on
	// process-local memory.
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.RecordClipPlay(ctx, "channel-a", clip.Hash, second)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent record: %v", err)
		}
	}
	a, err = s.FillerExposuresByChannel(ctx, "channel-a", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := a[clip.Hash]; got.PlayCount != 2 || !got.LastPlayedAt.Equal(second) {
		t.Fatalf("concurrent exposure = %+v, want two distinct scheduled starts", got)
	}

	// A missing catalog row still records actual airing truth. Re-admission must retain it.
	missing := "removed-and-readmitted"
	if _, err := s.RecordClipPlay(ctx, "channel-a", missing, second); err != nil {
		t.Fatal(err)
	}
	a, err = s.FillerExposuresByChannel(ctx, "channel-a", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := a[missing]; got.PlayCount != 1 || !got.LastPlayedAt.Equal(second) {
		t.Fatalf("pruned clip exposure = %+v, want durable actual airing", got)
	}
	restored := sampleClip(missing, "restored.mp4", filler.Commercial, 1994, filler.Kids, "toys")
	if err := s.UpsertClip(ctx, restored); err != nil {
		t.Fatal(err)
	}
	a, err = s.FillerExposuresByChannel(ctx, "channel-a", time.Time{})
	if err != nil || a[missing].PlayCount != 1 {
		t.Fatalf("re-admission lost exposure: %+v (err=%v)", a[missing], err)
	}
	channel, err := s.GetChannel(ctx, "channel-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteChannel(ctx, channel.ID, channel.Revision); err != nil {
		t.Fatal(err)
	}
	a, err = s.FillerExposuresByChannel(ctx, "channel-a", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 0 {
		t.Fatalf("deleted channel retained exposure rows: %+v", a)
	}
}

// testClipKeyIsHashNotPath pins WHICH key the clip writers take (§10 V38c).
//
// ⚠ This test exists because the absence of it cost two releases. `Hash` (identity) and `Path`
// (location) are both strings, so passing the wrong one is invisible to the compiler — and every
// fixture in this suite set them to the same value, so it was invisible to the tests too. Two
// production callers passed a path into a hash-keyed UPDATE: the AI tagger aborted on its first
// clip (ErrNotFound is fatal there) and play counters silently never moved.
//
// The assertions are deliberately negative — "a path must NOT work" — because the positive case
// passes just as happily against a store that accepts either.
func testClipKeyIsHashNotPath(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	mustSaveChannel(t, s, sampleChannel("channel-1", 1, time.Time{}))

	c := sampleClip("k1", "keyed.mp4", filler.Commercial, 0, "", "")
	if c.Hash == c.Path {
		t.Fatalf("fixture bug: hash and path are equal (%q) — this test cannot distinguish the "+
			"two keys and neither can any other test in this file", c.Hash)
	}
	if err := s.UpsertClip(ctx, c); err != nil {
		t.Fatal(err)
	}

	// GetClip is hash-keyed.
	if _, err := s.GetClip(ctx, c.Path); err != ErrNotFound {
		t.Errorf("GetClip(path) = %v, want ErrNotFound — GetClip is keyed WHERE hash = ?", err)
	}
	if _, err := s.GetClip(ctx, c.Hash); err != nil {
		t.Errorf("GetClip(hash) = %v, want it to resolve", err)
	}

	// UpdateClipClassification is hash-keyed, and its ErrNotFound is fatal to the tagging job.
	if err := s.UpdateClipClassification(ctx, c.Path, 1994, "kids", 0, true, now); err != ErrNotFound {
		t.Errorf("UpdateClipClassification(path) = %v, want ErrNotFound — the tagger must pass the hash", err)
	}
	if err := s.UpdateClipClassification(ctx, c.Hash, 1994, "kids", 0, true, now); err != nil {
		t.Errorf("UpdateClipClassification(hash) = %v, want it to apply", err)
	}

	// RecordClipPlay is hash-keyed and deliberately silent on a miss, so assert the COUNTER
	// rather than the error — a path that no-ops returns nil either way.
	if _, err := s.RecordClipPlay(ctx, "channel-1", c.Path, now); err != nil {
		t.Errorf("RecordClipPlay(path) = %v, want a silent no-op", err)
	}
	got, _ := s.GetClip(ctx, c.Hash)
	if got.PlayCount != 0 {
		t.Errorf("play count = %d after recording against a PATH, want 0", got.PlayCount)
	}
	if _, err := s.RecordClipPlay(ctx, "channel-1", c.Hash, now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, c.Hash)
	if got.PlayCount != 1 {
		t.Errorf("play count = %d after recording against the HASH, want 1", got.PlayCount)
	}

	// SetClipLanguage is the other half of the split: path-keyed, by design (the language job
	// walks the filesystem). Pinned so a well-meaning "consistency" change has to be deliberate.
	if err := s.SetClipLanguage(ctx, c.Path, "en", now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, c.Hash)
	if got.Language != "en" {
		t.Errorf("language = %q, want %q — SetClipLanguage is keyed WHERE path = ?", got.Language, "en")
	}

	// SetClipTranscript joins the path-keyed job writers (§10 V44). Same split, same reason.
	if err := s.SetClipTranscript(ctx, c.Path, "buy our cereal today", now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, c.Hash)
	if got.Transcript != "buy our cereal today" {
		t.Errorf("transcript = %q, want the recorded text — SetClipTranscript is keyed WHERE path = ?", got.Transcript)
	}

	// SetClipBrand is the TEXT tagger's grounded brand writer (§10 V44) — path-keyed like the
	// transcript writer, and it must write `brand` WITHOUT stamping `vision_tagged`: a brand grounded
	// in the transcript is not a brand a vision pass read off a frame, and conflating the two would
	// make a re-run skip the vision it never actually ran.
	if err := s.SetClipBrand(ctx, c.Path, "Post", now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, c.Hash)
	if got.Brand != "Post" {
		t.Errorf("brand = %q, want %q — SetClipBrand is keyed WHERE path = ?", got.Brand, "Post")
	}
	if got.VisionTagged {
		t.Errorf("vision_tagged set by a TEXT brand write — SetClipBrand must not masquerade as a vision pass")
	}

	// ApplyClipVision records the on-screen text, a grounded brand, and (here) a taxonomy tag. era is
	// passed 0, so the era set by UpdateClipClassification above (1994) must SURVIVE — the CASE guard, not a
	// blanket overwrite. It overwrites the text-grounded brand above, which is fine — both are
	// grounded writers of the same column.
	if err := s.ApplyClipVision(ctx, c.Hash, c.Path, "Kellogg's", "KELLOGG'S FROSTED FLAKES", 0, 0, []string{"cereal"}, now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, c.Hash)
	if got.Brand != "Kellogg's" || got.VisibleText != "KELLOGG'S FROSTED FLAKES" || !got.VisionTagged {
		t.Errorf("vision tags = {brand:%q visible:%q tagged:%v}, want them recorded", got.Brand, got.VisibleText, got.VisionTagged)
	}
	if got.Era != 1994 {
		t.Errorf("era = %d after a vision write with era=0, want 1994 preserved — the CASE guard must not blank an existing era", got.Era)
	}
	if got.Category != "cereal" || len(got.AssertedTags) != 1 || got.AssertedTags[0] != "cereal" {
		t.Errorf("vision taxonomy = category %q / asserted %v, want cereal / [cereal]", got.Category, got.AssertedTags)
	}
	if err := s.ApplyClipVision(ctx, c.Hash, c.Path, "Uncommitted", "SHOULD ROLL BACK", 0, 0, []string{"not-a-live-taxon"}, now); !errors.Is(err, ErrTaxonConflict) {
		t.Fatalf("invalid vision taxonomy error = %v, want ErrTaxonConflict", err)
	}
	got, _ = s.GetClip(ctx, c.Hash)
	if got.Brand != "Kellogg's" || got.VisibleText != "KELLOGG'S FROSTED FLAKES" {
		t.Errorf("invalid vision taxonomy partially committed facts: brand=%q text=%q", got.Brand, got.VisibleText)
	}
	// ⚠ A grounded era SUPPRESSES the frame-heuristic suggestion: this clip already has era 1994, so
	// passing suggestedEra=1960 must NOT set suggested_era — a clip with a known era has no question
	// to ask. This pins the "era = 0" precondition in the store's CASE guard.
	if err := s.ApplyClipVision(ctx, c.Hash, c.Path, "Kellogg's", "KELLOGG'S FROSTED FLAKES", 0, 1960, []string{"cereal"}, now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, c.Hash)
	if got.SuggestedEra != 0 {
		t.Errorf("suggested_era = %d, want 0 — a frame hint must not seed a suggestion when the clip already has a grounded era", got.SuggestedEra)
	}

	// The frame-heuristic path on a clip with NO era: a fresh clip, vision grounds no era, the hint
	// seeds a suggestion. This is the one write that turns a monochrome-4:3 measurement into the
	// operator-confirms field.
	hintClip := sampleClip("vhint", "wordless.mp4", filler.Commercial, 0, "", "")
	if err := s.UpsertClip(ctx, hintClip); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyClipVision(ctx, hintClip.Hash, hintClip.Path, "", "", 0, 1960, nil, now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, hintClip.Hash)
	if got.SuggestedEra != 1960 {
		t.Errorf("suggested_era = %d, want 1960 — a frame hint must seed the suggestion when the clip has no grounded era", got.SuggestedEra)
	}
	if got.Era != 0 {
		t.Errorf("era = %d, want 0 — a frame hint is a SUGGESTION, never a grounded era", got.Era)
	}

	// ⚠ The load-bearing property: a re-sync (UpsertClip) must NOT clobber the job-written columns.
	// The scan re-upserts every file it finds carrying a ZERO transcript/brand — if any of these
	// rode UpsertClip's DO UPDATE list, this upsert would wipe the work above and re-trigger Whisper
	// / a paid vision call on the next pass. This is the same discipline the language/held/counter
	// columns already pin; without it a green suite would hide the exact regression 00038 guards.
	resync := c
	resync.Transcript, resync.Brand, resync.VisibleText, resync.VisionTagged = "", "", "", false
	if err := s.UpsertClip(ctx, resync); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, c.Hash)
	if got.Transcript != "buy our cereal today" {
		t.Errorf("transcript = %q after a re-sync, want it PRESERVED — UpsertClip must omit transcript from DO UPDATE", got.Transcript)
	}
	if got.Brand != "Kellogg's" || got.VisibleText != "KELLOGG'S FROSTED FLAKES" || !got.VisionTagged {
		t.Errorf("vision tags lost on re-sync {brand:%q visible:%q tagged:%v} — UpsertClip must omit them from DO UPDATE", got.Brand, got.VisibleText, got.VisionTagged)
	}
}

// An internal transform changes the content hash without changing what the clip means to the
// operator. Every durable reference must follow in one transaction; otherwise a rescan produces
// a fresh hash-titled row while tags, lineage, pipeline progress and channel overrides stay on an
// orphan identity.
func testClipIdentityReplacement(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_900_000_000, 0).UTC()

	old := sampleClip("old-content", "McDonald's 1993", filler.Commercial, 1993, filler.Kids, "fast_food")
	old.ParentHash = "parent-reel"
	old.Held = true
	old.Confidence = 91
	old.Transcript = "two all beef patties"
	if err := s.UpsertClip(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertClipFingerprint(ctx, old.Hash, "dhash-v1", []uint64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	child := sampleClip("child", "Child", filler.Commercial, 1993, filler.Kids, "toys")
	child.ParentHash = old.Hash
	if err := s.UpsertClip(ctx, child); err != nil {
		t.Fatal(err)
	}

	if err := s.SetClipTags(ctx, old.Hash, []string{"cereal"}); err != nil {
		t.Fatal(err)
	}
	pipeline := filler.ClipPipeline{
		ClipHash: old.Hash, Stage: filler.StageTranscode, Status: filler.StatusRunning,
		Disposition: filler.DispositionRunning, EnrolledAt: now, UpdatedAt: now,
	}
	if err := s.UpsertClipPipeline(ctx, pipeline); err != nil {
		t.Fatal(err)
	}
	proposal := filler.SplitProposal{ID: "proposal", ClipHash: old.Hash, CreatedAt: now}
	if err := s.UpsertSplitProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	channel := sampleChannel("clip-ref", 711, now)
	channel.Policy.Filler = &schedule.FillerSelection{
		Pinned: []string{"keep", old.Hash}, Excluded: []string{old.Hash, "other"},
	}
	channel, err := s.SaveChannel(ctx, channel)
	if err != nil {
		t.Fatal(err)
	}

	replacement := old
	replacement.Hash = "new-content"
	replacement.Path = "ne/wc/new-content.mp4"
	replacement.DurationMs = 30_033
	replacement.Quality = "480p"
	replacement.UpdatedAt = now.Add(time.Minute)
	if err := s.ReplaceClipIdentity(ctx, old.Hash, replacement); err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetClip(ctx, old.Hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old identity still resolves: %v", err)
	}
	got, err := s.GetClip(ctx, replacement.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != old.Name || got.Source != old.Source || got.ParentHash != old.ParentHash ||
		got.Transcript != old.Transcript || !got.Held || got.Confidence != old.Confidence {
		t.Errorf("metadata did not follow identity replacement: %+v", got)
	}
	if got.Path != replacement.Path || got.DurationMs != replacement.DurationMs || got.Quality != replacement.Quality {
		t.Errorf("transformed facts were not installed: %+v", got)
	}
	if len(got.Tags) == 0 {
		t.Error("taxonomy tags stayed behind on the old identity")
	}
	if row, found, err := s.GetClipPipeline(ctx, replacement.Hash); err != nil || !found || row.Stage != filler.StageTranscode {
		t.Errorf("pipeline did not follow replacement: %+v, %v, %v", row, found, err)
	}
	if _, found, err := s.GetClipPipeline(ctx, old.Hash); err != nil || found {
		t.Errorf("old pipeline row survived: found=%v err=%v", found, err)
	}
	proposals, err := s.ListSplitProposals(ctx)
	if err != nil || len(proposals) != 1 || proposals[0].ClipHash != replacement.Hash {
		t.Errorf("split proposal did not follow replacement: %+v (%v)", proposals, err)
	}
	gotChild, err := s.GetClip(ctx, child.Hash)
	if err != nil || gotChild.ParentHash != replacement.Hash {
		t.Errorf("child lineage did not follow replacement: %+v (%v)", gotChild, err)
	}
	gotChannel, err := s.GetChannel(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotChannel.Policy.Filler == nil || gotChannel.Policy.Filler.Pinned[1] != replacement.Hash ||
		gotChannel.Policy.Filler.Excluded[0] != replacement.Hash {
		t.Errorf("channel overrides did not follow replacement: %+v", gotChannel.Policy.Filler)
	}
	if gotChannel.Revision != channel.Revision+1 {
		t.Errorf("channel policy rekey revision = %d, want %d", gotChannel.Revision, channel.Revision+1)
	}
	if _, found, err := cachedClipFingerprint(ctx, s, old.Hash, "dhash-v1"); err != nil || found {
		t.Errorf("old-byte fingerprint survived identity replacement: found=%v err=%v", found, err)
	}
	if _, found, err := cachedClipFingerprint(ctx, s, replacement.Hash, "dhash-v1"); err != nil || found {
		t.Errorf("old-byte fingerprint was re-keyed onto replacement bytes: found=%v err=%v", found, err)
	}
}

// A pending conditioned target may be reconstructed by Sync before the source row is re-keyed.
// The store adopts that exact held row and moves the source-owned graph in one transaction; the
// same operation then recognizes the target-only post-rekey state after a process restart.
func testConditioningPublicationCommit(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_900_634_000, 0).UTC()

	source := sampleClip("conditioning-source", "Reviewed child", filler.Commercial, 1993, filler.Kids, "food")
	source.ParentHash = "retained-parent"
	source.Confidence = 91
	source.Transcript = "source-owned transcript"
	source.UpdatedAt = now
	if err := s.UpsertClip(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertClipPipeline(ctx, filler.ClipPipeline{
		ClipHash: source.Hash, Stage: filler.StageTranscode, Status: filler.StatusRunning,
		Disposition: filler.DispositionRunning, EnrolledAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	target := source
	target.Hash = "conditioning-target"
	target.Path = clipPathFor(target.Hash)
	target.DurationMs = 30_033
	target.Quality = "480p"
	target.TunarrProgramID = ""
	reconstructed := target
	reconstructed.Name = "reconstructed from sidecar"
	reconstructed.Held = true
	reconstructed.Confidence = 0
	reconstructed.Transcript = ""
	if err := s.UpsertClip(ctx, reconstructed); err != nil {
		t.Fatal(err)
	}
	publication := filler.ConditioningPublication{
		State: "pending", Owner: "0123456789abcdef0123456789abcdef",
		SourceHash: source.Hash, TargetHash: target.Hash,
	}
	if err := s.CommitConditioningPublication(ctx, publication, target); err != nil {
		t.Fatalf("adopt reconstructed target: %v", err)
	}
	if _, err := s.GetClip(ctx, source.Hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source identity survived adoption: %v", err)
	}
	got, err := s.GetClip(ctx, target.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != source.Name || got.Transcript != source.Transcript || got.Confidence != source.Confidence || got.Held != source.Held {
		t.Fatalf("adopted target did not inherit source ownership: %+v", got)
	}
	if got.Path != target.Path || got.DurationMs != target.DurationMs || got.Quality != target.Quality {
		t.Fatalf("adopted target lost transformed facts: %+v", got)
	}
	if pipeline, found, err := s.GetClipPipeline(ctx, target.Hash); err != nil || !found || pipeline.Stage != filler.StageTranscode {
		t.Fatalf("adopted target pipeline = %+v, found=%v err=%v", pipeline, found, err)
	}
	if err := s.CommitConditioningPublication(ctx, publication, got); err != nil {
		t.Fatalf("recognize post-rekey target: %v", err)
	}
}

// A sidecar's pending publication owns exactly one source/target pair. Any malformed marker or
// catalog shape outside the three recovery states must leave both the source graph and any
// reconstructed target untouched for review; guessing which row to retire would lose evidence.
func testConditioningPublicationFailsClosed(t *testing.T, newStore NewStoreFunc) {
	for _, tc := range []struct {
		name          string
		mutateMarker  func(*filler.ConditioningPublication, *Clip)
		mutateSource  func(*Clip)
		mutateTarget  func(*Clip)
		withTargetRow bool
		heldTarget    bool
	}{
		{
			name: "owner is absent",
			mutateMarker: func(publication *filler.ConditioningPublication, _ *Clip) {
				publication.Owner = ""
			},
		},
		{
			name: "marker target differs from staged target",
			mutateMarker: func(publication *filler.ConditioningPublication, _ *Clip) {
				publication.TargetHash = "other-conditioned-target"
			},
		},
		{
			name: "marker collapses source and target ownership",
			mutateMarker: func(publication *filler.ConditioningPublication, _ *Clip) {
				publication.TargetHash = publication.SourceHash
			},
		},
		{
			name: "source no longer matches staged lineage",
			mutateSource: func(source *Clip) {
				source.ParentHash = "different-retained-parent"
			},
		},
		{
			name:          "target exists without Sync quarantine",
			withTargetRow: true,
		},
		{
			name:          "held target does not match pending evidence",
			withTargetRow: true,
			heldTarget:    true,
			mutateTarget: func(target *Clip) {
				target.Path = "wrong/conditioned-target.mp4"
			},
		},
		{
			name:          "target-only state is not exact",
			withTargetRow: true,
			heldTarget:    true,
			mutateSource: func(source *Clip) {
				source.Hash = "absent-conditioning-source"
			},
			mutateTarget: func(target *Clip) {
				target.Held = true
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			ctx := context.Background()
			now := time.Unix(1_900_634_100, 0).UTC()

			source := sampleClip("conditioning-ambiguous-source", "Reviewed child", filler.Commercial, 1993, filler.Kids, "food")
			source.ParentHash = "retained-parent"
			source.UpdatedAt = now
			target := source
			target.Hash = "conditioning-ambiguous-target"
			target.Path = clipPathFor(target.Hash)
			target.DurationMs = 30_033
			target.Quality = "480p"
			publication := filler.ConditioningPublication{
				State: "pending", Owner: "0123456789abcdef0123456789abcdef",
				SourceHash: source.Hash, TargetHash: target.Hash,
			}
			if tc.mutateMarker != nil {
				tc.mutateMarker(&publication, &target)
			}
			if tc.mutateSource != nil {
				tc.mutateSource(&source)
			}
			if source.Hash != publication.SourceHash {
				// The target-only state deliberately does not install a source catalog row.
			} else if err := s.UpsertClip(ctx, source); err != nil {
				t.Fatal(err)
			}
			if source.Hash == publication.SourceHash {
				if err := s.UpsertClipPipeline(ctx, filler.ClipPipeline{
					ClipHash: publication.SourceHash, Stage: filler.StageTranscode, Status: filler.StatusRunning,
					Disposition: filler.DispositionRunning, EnrolledAt: now, UpdatedAt: now,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.withTargetRow {
				reconstructed := target
				reconstructed.Held = tc.heldTarget
				if tc.mutateTarget != nil {
					tc.mutateTarget(&reconstructed)
				}
				if err := s.UpsertClip(ctx, reconstructed); err != nil {
					t.Fatal(err)
				}
			}

			if err := s.CommitConditioningPublication(ctx, publication, target); !errors.Is(err, ErrConditioningPublicationMismatch) {
				t.Fatalf("CommitConditioningPublication error = %v, want ErrConditioningPublicationMismatch", err)
			}
			if source.Hash == publication.SourceHash {
				got, err := s.GetClip(ctx, publication.SourceHash)
				if err != nil || got.Hash != source.Hash || got.ParentHash != source.ParentHash || got.Path != source.Path {
					t.Fatalf("ambiguous publication changed source evidence: %+v, %v", got, err)
				}
				if pipeline, found, err := s.GetClipPipeline(ctx, publication.SourceHash); err != nil || !found || pipeline.Stage != filler.StageTranscode {
					t.Fatalf("ambiguous publication lost source reference: %+v, found=%v, err=%v", pipeline, found, err)
				}
			}
			if tc.withTargetRow {
				got, err := s.GetClip(ctx, target.Hash)
				if err != nil || got.Held != tc.heldTarget {
					t.Fatalf("ambiguous publication changed reconstructed target: %+v, %v", got, err)
				}
			}
		})
	}
}

// A transform may legitimately preserve the content hash: remuxing an already-normalized clip
// can produce byte-for-byte identical output. That is an update to transformed facts, not a
// re-key, and must not run self-referential updates through unique sibling keys.
func testClipIdentityReplacementSameHash(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_900_000_000, 0).UTC()

	clip := sampleClip("unchanged-content", "Already normalized", filler.Commercial, 1995, filler.General, "")
	clip.Path = "un/ch/unchanged-content.mkv"
	clip.TunarrProgramID = "old-program"
	if err := s.UpsertClip(ctx, clip); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertClipFingerprint(ctx, clip.Hash, "dhash-v1", []uint64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	pipeline := filler.ClipPipeline{
		ClipHash: clip.Hash, Stage: filler.StageTranscode, Status: filler.StatusRunning,
		Disposition: filler.DispositionRunning, EnrolledAt: now, UpdatedAt: now,
	}
	if err := s.UpsertClipPipeline(ctx, pipeline); err != nil {
		t.Fatal(err)
	}

	updated := clip
	updated.Path = "un/ch/unchanged-content.mp4"
	updated.DurationMs = 30_033
	updated.Quality = "480p"
	updated.UpdatedAt = now.Add(time.Minute)
	if err := s.ReplaceClipIdentity(ctx, clip.Hash, updated); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetClip(ctx, clip.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != updated.Path || got.DurationMs != updated.DurationMs || got.Quality != updated.Quality {
		t.Errorf("same-identity transformed facts = %+v, want path %q duration %d quality %q",
			got, updated.Path, updated.DurationMs, updated.Quality)
	}
	if got.TunarrProgramID != "" {
		t.Errorf("Tunarr program id = %q, want cleared after path change", got.TunarrProgramID)
	}
	// Once Tunarr knows the canonical path, an idempotent retry at that same path must not
	// manufacture registration work. This also pins the CASE expression to the pre-update path
	// semantics shared by SQLite and Postgres.
	got.TunarrProgramID = "canonical-program"
	if err := s.UpsertClip(ctx, got); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceClipIdentity(ctx, got.Hash, got); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetClip(ctx, clip.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.TunarrProgramID != "canonical-program" {
		t.Errorf("same-path retry cleared Tunarr program id: %q", got.TunarrProgramID)
	}
	if row, found, err := s.GetClipPipeline(ctx, clip.Hash); err != nil || !found || row.Stage != filler.StageTranscode {
		t.Errorf("same-identity pipeline changed: %+v, found=%v err=%v", row, found, err)
	}
	if cached, found, err := cachedClipFingerprint(ctx, s, clip.Hash, "dhash-v1"); err != nil || !found || len(cached) != 3 {
		t.Errorf("fingerprint for unchanged bytes = (%v, %v, %v), want preserved", cached, found, err)
	}
}

func testClipPipelineOverview(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_900_000_000, 0).UTC()
	rows := []filler.ClipPipeline{
		{ClipHash: "due", Stage: filler.StageProbe, Status: filler.StatusQueued,
			Disposition: filler.DispositionRunning, NextRun: now.Add(-time.Minute)},
		{ClipHash: "active", Stage: filler.StageTranscode, Status: filler.StatusRunning,
			Disposition: filler.DispositionRunning},
		{ClipHash: "backoff", Stage: filler.StageVision, Status: filler.StatusFailed,
			Disposition: filler.DispositionRunning, NextRun: now.Add(time.Hour)},
		{ClipHash: "deferred", Stage: filler.StageSplit, Status: filler.StatusQueued,
			Disposition: filler.DispositionRunning, NextRun: now.Add(time.Hour)},
		{ClipHash: "review", Stage: filler.StageScore, Status: filler.StatusDone,
			Disposition: filler.DispositionReview},
		{ClipHash: "filed", Stage: filler.StageScore, Status: filler.StatusDone,
			Disposition: filler.DispositionFiled},
		{ClipHash: "rejected", Stage: filler.StageTranscode, Status: filler.StatusFailed,
			Disposition: filler.DispositionRejected, RejectReason: filler.ReasonUnplayable},
		{ClipHash: "dismissed", Stage: filler.StageTag, Status: filler.StatusDone,
			Disposition: filler.DispositionDismissed},
	}
	for i := range rows {
		rows[i].EnrolledAt, rows[i].UpdatedAt = now, now
		if err := s.UpsertClipPipeline(ctx, rows[i]); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.PipelineOverview(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	want := filler.PipelineOverview{
		Runnable: 1, InProgress: 1, Scheduled: 2, NeedsDecision: 1,
		Admitted: 1, Rejected: 1, Dismissed: 1, Recoverable: 1,
	}
	if got != want {
		t.Fatalf("PipelineOverview() = %+v, want %+v", got, want)
	}
}

func testFillerAcquisitionRuns(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_900_000_000, 0).UTC()
	older := filler.AcquisitionRun{
		ID: "acq-old", Trigger: filler.AcquisitionScheduled, SourceID: "archive:classic",
		Status: filler.AcquisitionRunning, Requested: 3, StartedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	newer := filler.AcquisitionRun{
		ID: "acq-new", Trigger: filler.AcquisitionPull, SourceID: "archive:vault", PullID: "pull-7",
		Status: filler.AcquisitionSuccess, Requested: 4, Fetched: 2, Skipped: 1, Failed: 1,
		StartedAt: now.Add(-time.Minute), CompletedAt: now, UpdatedAt: now,
	}
	for _, run := range []filler.AcquisitionRun{older, newer} {
		if err := s.UpsertAcquisitionRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	queued := filler.AcquisitionRun{
		ID: "acq-queued", Trigger: filler.AcquisitionManual, Status: filler.AcquisitionQueued,
		Requested: 1, StartedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour),
	}
	if err := s.UpsertAcquisitionRun(ctx, queued); err != nil {
		t.Fatal(err)
	}
	pipelines := []filler.ClipPipeline{
		{ClipHash: "preparing", AcquisitionID: newer.ID, Stage: filler.StageTag, Status: filler.StatusRunning,
			Disposition: filler.DispositionRunning, EnrolledAt: now, UpdatedAt: now},
		{ClipHash: "review", AcquisitionID: newer.ID, Stage: filler.StageScore, Status: filler.StatusDone,
			Disposition: filler.DispositionReview, EnrolledAt: now, UpdatedAt: now},
		{ClipHash: "admitted", AcquisitionID: newer.ID, Stage: filler.StageScore, Status: filler.StatusDone,
			Disposition: filler.DispositionFiled, EnrolledAt: now, UpdatedAt: now},
		{ClipHash: "rejected", AcquisitionID: newer.ID, Stage: filler.StageProbe, Status: filler.StatusFailed,
			Disposition: filler.DispositionRejected, EnrolledAt: now, UpdatedAt: now},
	}
	for _, row := range pipelines {
		if err := s.UpsertClipPipeline(ctx, row); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := s.ListAcquisitionRuns(ctx, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != newer.ID {
		t.Fatalf("latest acquisition = %+v, want only %s", runs, newer.ID)
	}
	wantOutcome := filler.AcquisitionOutcome{Enrolled: 4, Preparing: 1, NeedsDecision: 1, Admitted: 1, Rejected: 1}
	if runs[0].Outcome != wantOutcome {
		t.Fatalf("outcome = %+v, want %+v", runs[0].Outcome, wantOutcome)
	}
	if runs[0].PullID != "pull-7" || runs[0].Fetched != 2 || !runs[0].CompletedAt.Equal(now) {
		t.Fatalf("run facts did not round-trip: %+v", runs[0])
	}

	newer.Status, newer.Error, newer.UpdatedAt = filler.AcquisitionError, "catalogue failed", now.Add(time.Minute)
	if err := s.UpsertAcquisitionRun(ctx, newer); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAcquisitionRun(ctx, newer.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != filler.AcquisitionError || got.Error != "catalogue failed" || got.Outcome != wantOutcome {
		t.Fatalf("updated acquisition = %+v", got)
	}
	if _, err := s.GetAcquisitionRun(ctx, "missing", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing acquisition = %v, want ErrNotFound", err)
	}

	recoveredAt := now.Add(2 * time.Minute)
	if n, err := s.RecoverInterruptedAcquisitionRuns(ctx, recoveredAt); err != nil || n != 2 {
		t.Fatalf("recover interrupted = %d, %v; want two queued/running runs", n, err)
	}
	for _, id := range []string{older.ID, queued.ID} {
		run, err := s.GetAcquisitionRun(ctx, id, recoveredAt)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != filler.AcquisitionError || run.Error == "" || !run.CompletedAt.Equal(recoveredAt) {
			t.Fatalf("recovered %s = %+v", id, run)
		}
	}
	terminal, err := s.GetAcquisitionRun(ctx, newer.ID, recoveredAt)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.UpdatedAt.Equal(recoveredAt) {
		t.Fatal("startup recovery rewrote an already-terminal acquisition")
	}
}

func testInteractiveOperations(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_900_100_000, 0).UTC()

	split := InteractiveOperation{
		ID: "split-1", Kind: InteractiveOperationFillerSplit, Subject: "clip-hash",
		Status: InteractiveOperationRunning, StartedAt: now, UpdatedAt: now,
	}
	if err := s.UpsertInteractiveOperation(ctx, split); err != nil {
		t.Fatal(err)
	}
	split.Status = InteractiveOperationSuccess
	split.ResultID = "sp-1"
	split.CompletedAt, split.UpdatedAt = now.Add(time.Minute), now.Add(time.Minute)
	if err := s.UpsertInteractiveOperation(ctx, split); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetInteractiveOperation(ctx, split.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != split {
		t.Fatalf("split operation = %+v, want %+v", got, split)
	}

	pull := InteractiveOperation{
		ID: "pull-1", Kind: InteractiveOperationLLMPull, Subject: "qwen3:8b",
		Status: InteractiveOperationRunning, Percent: 42, Completed: 420, Total: 1000,
		StartedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	}
	if err := s.UpsertInteractiveOperation(ctx, pull); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetInteractiveOperation(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing operation = %v, want ErrNotFound", err)
	}

	recoveredAt := now.Add(2 * time.Minute)
	if n, err := s.RecoverInterruptedInteractiveOperations(ctx, recoveredAt); err != nil || n != 1 {
		t.Fatalf("recover operations = %d, %v; want one running pull", n, err)
	}
	recovered, err := s.GetInteractiveOperation(ctx, pull.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != InteractiveOperationError || recovered.Error == "" || !recovered.CompletedAt.Equal(recoveredAt) {
		t.Fatalf("recovered pull = %+v", recovered)
	}
	terminal, err := s.GetInteractiveOperation(ctx, split.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal != split {
		t.Fatalf("recovery rewrote terminal split = %+v, want %+v", terminal, split)
	}
}

func testClipPipelineRetry(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_900_000_000, 0).UTC()
	clip := clipAt("retry/failed.mp4", "Failed encode", filler.Commercial, 30_000)
	if err := s.UpsertClip(ctx, Clip{Clip: clip, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetClipsHeld(ctx, []string{clip.Path}, false, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetClipsRemoved(ctx, []string{clip.Path}, now); err != nil {
		t.Fatal(err)
	}
	failed := filler.ClipPipeline{
		ClipHash: clip.Hash, Stage: filler.StageTranscode, Status: filler.StatusFailed,
		Disposition: filler.DispositionRejected, RejectReason: filler.ReasonUnplayable,
		Attempts: filler.MaxAttempts, EnrolledAt: now, UpdatedAt: now,
	}
	if err := s.UpsertClipPipeline(ctx, failed); err != nil {
		t.Fatal(err)
	}
	retry := failed
	retry.Status, retry.Disposition, retry.Attempts = filler.StatusQueued, filler.DispositionRunning, 0
	retry.RejectReason, retry.ForceRun, retry.UpdatedAt = "", true, now.Add(time.Minute)
	if err := s.RetryClipPipeline(ctx, failed, retry, true); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryClipPipeline(ctx, failed, retry, true); !errors.Is(err, filler.ErrPipelineNotRetryable) {
		t.Fatalf("stale retry = %v, want ErrPipelineNotRetryable", err)
	}
	got, err := s.GetClip(ctx, clip.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if !got.RemovedAt.IsZero() || !got.Held || got.AutoFiled {
		t.Fatalf("restored clip = removed:%v held:%v auto:%v, want present and held", got.RemovedAt, got.Held, got.AutoFiled)
	}
	row, found, err := s.GetClipPipeline(ctx, clip.Hash)
	if err != nil || !found || row.Status != filler.StatusQueued || row.Disposition != filler.DispositionRunning || !row.ForceRun {
		t.Fatalf("retry row = %+v found=%v err=%v", row, found, err)
	}
}

// testClipFingerprintCache pins the cache's two correctness properties on both backends: only an
// exact content+algorithm key hits, and catalog pruning removes sibling-table orphans.
func testClipFingerprintCache(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	one := sampleClip("fingerprint-one", "One", filler.Commercial, 1993, filler.General, "")
	two := sampleClip("fingerprint-two", "Two", filler.Commercial, 1994, filler.General, "")
	if err := s.UpsertClip(ctx, one); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertClip(ctx, two); err != nil {
		t.Fatal(err)
	}

	if err := s.UpsertClipFingerprint(ctx, one.Hash, "dhash-v1", nil); err == nil {
		t.Fatal("empty fingerprint was persisted")
	}
	want := []uint64{0, 1, ^uint64(0)}
	if err := s.UpsertClipFingerprint(ctx, one.Hash, "dhash-v1", want); err != nil {
		t.Fatal(err)
	}
	got, found, err := cachedClipFingerprint(ctx, s, one.Hash, "dhash-v1")
	if err != nil || !found || len(got) != len(want) || got[2] != want[2] {
		t.Fatalf("fingerprint round-trip = (%v, %v, %v), want %v", got, found, err, want)
	}
	if _, found, err := cachedClipFingerprint(ctx, s, one.Hash, "dhash-v2"); err != nil || found {
		t.Errorf("different algorithm hit the cache: found=%v err=%v", found, err)
	}
	if err := s.UpsertClipFingerprint(ctx, one.Hash, "dhash-v1", []uint64{9}); err != nil {
		t.Fatal(err)
	}
	got, found, err = cachedClipFingerprint(ctx, s, one.Hash, "dhash-v1")
	if err != nil || !found || len(got) != 1 || got[0] != 9 {
		t.Errorf("idempotent upsert = (%v, %v, %v), want [9]", got, found, err)
	}
	if err := s.UpsertClipFingerprint(ctx, two.Hash, "dhash-v1", []uint64{7}); err != nil {
		t.Fatal(err)
	}

	// Keep only clip two. The cache has no FK by design, so DeleteClipsNotIn owns this cleanup.
	if _, err := s.DeleteClipsNotIn(ctx, []string{two.Hash}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := cachedClipFingerprint(ctx, s, one.Hash, "dhash-v1"); err != nil || found {
		t.Errorf("pruned clip left an orphan fingerprint: found=%v err=%v", found, err)
	}
	if got, found, err := cachedClipFingerprint(ctx, s, two.Hash, "dhash-v1"); err != nil || !found || len(got) != 1 || got[0] != 7 {
		t.Errorf("kept clip lost its fingerprint: got=%v found=%v err=%v", got, found, err)
	}
}

// testCompositeLineage pins the V45 composite/lineage invariants (§10): a composite is excluded from
// the default listing (the pod-assembly path), its segments link back via parent_hash, and neither
// is_composite nor parent_hash is clobbered by a re-sync.
func testCompositeLineage(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()

	// A composite (the recorded break) and two segments split out of it.
	comp := sampleClip("break1", "kcpq-1996.mp4", filler.Commercial, 1996, filler.General, "")
	comp.DurationMs = 971_000
	if err := s.UpsertClip(ctx, comp); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClipComposite(ctx, comp.Hash, true, now); err != nil {
		t.Fatal(err)
	}
	seg1 := sampleClip("seg1", "aw-root-beer.mp4", filler.Commercial, 1996, filler.General, "fast_food")
	seg1.ParentHash = comp.Hash
	seg2 := sampleClip("seg2", "kfc.mp4", filler.Commercial, 1996, filler.General, "fast_food")
	seg2.ParentHash = comp.Hash
	if err := s.UpsertClip(ctx, seg1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertClip(ctx, seg2); err != nil {
		t.Fatal(err)
	}

	// ⚠ THE load-bearing exclusion: a composite must NOT appear in the default (pod-assembly) listing.
	// Airing a 16-minute break as one "commercial" is the bug the flag removes.
	def, _ := s.ListClips(ctx, ClipFilter{})
	for _, c := range def {
		if c.Hash == comp.Hash {
			t.Errorf("composite %q appeared in the default listing — pod assembly would air a 16-min block as one commercial", comp.Hash)
		}
	}
	// The two segments ARE airable and present by default.
	if !containsHash(def, seg1.Hash) || !containsHash(def, seg2.Hash) {
		t.Errorf("segments missing from the default listing — they are the airable clips")
	}

	// Opt-in surfaces the composite (the catalog/lineage view).
	withComp, _ := s.ListClips(ctx, ClipFilter{IncludeComposites: true})
	if !containsHash(withComp, comp.Hash) {
		t.Errorf("IncludeComposites did not surface the composite")
	}
	only, _ := s.ListClips(ctx, ClipFilter{CompositesOnly: true})
	if len(only) != 1 || only[0].Hash != comp.Hash {
		t.Errorf("CompositesOnly = %d clips, want just the composite", len(only))
	}

	// Lineage: the segments of one composite, by parent_hash.
	kids, _ := s.ListClips(ctx, ClipFilter{ParentHash: comp.Hash})
	if len(kids) != 2 {
		t.Errorf("parent_hash query returned %d segments, want 2", len(kids))
	}
	for _, k := range kids {
		if k.ParentHash != comp.Hash {
			t.Errorf("segment %q parent_hash = %q, want %q", k.Hash, k.ParentHash, comp.Hash)
		}
	}

	// ⚠ A re-sync (the folder scan finding the original file) must NOT flip the composite back to
	// airable, nor blank a segment's lineage — is_composite/parent_hash are omitted from DO UPDATE.
	resync := comp
	resync.IsComposite = false // the scan-built Clip knows nothing of the composite mark
	if err := s.UpsertClip(ctx, resync); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetClip(ctx, comp.Hash)
	if !got.IsComposite {
		t.Errorf("is_composite lost on re-sync — UpsertClip must omit it from DO UPDATE, else a confirmed break re-airs")
	}
	resyncSeg := seg1
	resyncSeg.ParentHash = "" // the scan does not know whose segment this is
	if err := s.UpsertClip(ctx, resyncSeg); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetClip(ctx, seg1.Hash)
	if got.ParentHash != comp.Hash {
		t.Errorf("parent_hash = %q after re-sync, want it PRESERVED — UpsertClip must omit it from DO UPDATE", got.ParentHash)
	}

	// A completed re-split replaces the old generation atomically. seg2 is explicitly pinned and
	// therefore survives even though the new detector did not reproduce it; seg1 is retired but
	// remains recoverable as a tombstoned row. A newly accepted hash is restored if an earlier
	// generation had already tombstoned it.
	seg3 := sampleClip("seg3", "better-cut.mp4", filler.Commercial, 1996, filler.General, "drinks")
	seg3.ParentHash = comp.Hash
	if err := s.UpsertClip(ctx, seg3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetClipsRemoved(ctx, []string{seg3.Path}, now); err != nil {
		t.Fatal(err)
	}
	ch := sampleChannel("pinned-split-child", 809, time.Time{})
	ch.Policy.Filler = &schedule.FillerSelection{Pinned: []string{seg2.Hash}}
	if _, err := s.SaveChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	retired, err := s.ReplaceSplitChildren(ctx, comp.Hash, []string{seg3.Hash}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if retired != 1 {
		t.Errorf("retired = %d, want only superseded unpinned seg1", retired)
	}
	active, err := s.ListClips(ctx, ClipFilter{ParentHash: comp.Hash})
	if err != nil {
		t.Fatal(err)
	}
	if containsHash(active, seg1.Hash) || !containsHash(active, seg2.Hash) || !containsHash(active, seg3.Hash) {
		t.Errorf("active replacement generation = %+v, want pinned seg2 + new seg3", active)
	}
	all, err := s.ListClips(ctx, ClipFilter{ParentHash: comp.Hash, IncludeRemoved: true})
	if err != nil {
		t.Fatal(err)
	}
	if !containsHash(all, seg1.Hash) {
		t.Error("superseded seg1 row was deleted instead of tombstoned")
	}
}

func testSplitConfirmationAtomic(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_900_000_000, 0).UTC()
	parent := sampleClip("atomic-parent", "atomic-parent.mp4", filler.Commercial, 1994, filler.General, "")
	parent.Held = true
	old := sampleClip("atomic-old", "atomic-old.mp4", filler.Commercial, 1994, filler.General, "")
	old.ParentHash = parent.Hash
	first := sampleClip("atomic-first", "atomic-first.mp4", filler.Commercial, 1994, filler.General, "")
	first.ParentHash = parent.Hash
	second := sampleClip("atomic-second", "atomic-second.mp4", filler.Commercial, 1994, filler.General, "")
	second.ParentHash = parent.Hash
	for _, clip := range []Clip{parent, old, first, second} {
		if err := s.UpsertClip(ctx, clip); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SetClipsRemoved(ctx, []string{first.Path, second.Path}, now); err != nil {
		t.Fatal(err)
	}
	parentPipeline := filler.ClipPipeline{
		ClipHash: parent.Hash, Stage: filler.StageSplit, Status: filler.StatusQueued,
		Disposition: filler.DispositionReview, NextRun: now.Add(time.Hour),
		EnrolledAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute),
	}
	firstPipeline := filler.ClipPipeline{
		ClipHash: first.Hash, Stage: filler.StageProbe, Status: filler.StatusQueued,
		Disposition: filler.DispositionReview, EnrolledAt: now, UpdatedAt: now,
	}
	if err := s.UpsertClipPipeline(ctx, parentPipeline); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertClipPipeline(ctx, firstPipeline); err != nil {
		t.Fatal(err)
	}
	proposal := filler.SplitProposal{
		ID: "atomic-proposal", ClipHash: parent.Hash, CreatedAt: now,
		Segments: []filler.SplitSegment{{StartMs: 0, EndMs: 10_000, Name: "first"}},
	}
	if err := s.UpsertSplitProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	completion := filler.SplitCompletion{
		ProposalID: proposal.ID, ClaimToken: "atomic-owner", ParentHash: parent.Hash,
		ChildHashes: []string{first.Hash, second.Hash}, ActivateHashes: []string{first.Hash, second.Hash}, At: now,
	}
	if _, err := s.AcquireSplitProposalClaim(ctx, proposal.ID, completion.ClaimToken, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	wrongToken := completion
	wrongToken.ClaimToken = "stale-owner"
	if _, err := s.CompleteSplitConfirmation(ctx, wrongToken); !errors.Is(err, filler.ErrProposalClaimed) {
		t.Fatalf("stale full completion = %v, want ErrProposalClaimed", err)
	}

	// The missing second pipeline fails after the transaction has updated the parent and first
	// child. Every one of those statements must roll back with proposal consumption and selection.
	if _, err := s.CompleteSplitConfirmation(ctx, completion); err == nil {
		t.Fatal("completion succeeded without every staged child pipeline")
	}
	if got, err := s.GetSplitProposal(ctx, proposal.ID); err != nil || got.ClipHash != parent.Hash {
		t.Fatalf("failed completion consumed proposal: got %+v, err %v", got, err)
	}
	gotParent, err := s.GetClip(ctx, parent.Hash)
	if err != nil || gotParent.IsComposite || !gotParent.Held {
		t.Fatalf("failed completion changed parent: %+v, %v", gotParent, err)
	}
	gotParentPipeline, found, err := s.GetClipPipeline(ctx, parent.Hash)
	if err != nil || !found || gotParentPipeline.Disposition != filler.DispositionReview {
		t.Fatalf("failed completion changed parent pipeline: %+v, found=%v err=%v", gotParentPipeline, found, err)
	}
	gotFirstPipeline, found, err := s.GetClipPipeline(ctx, first.Hash)
	if err != nil || !found || gotFirstPipeline.Disposition != filler.DispositionReview {
		t.Fatalf("failed completion activated first child: %+v, found=%v err=%v", gotFirstPipeline, found, err)
	}
	active, err := s.ListClips(ctx, ClipFilter{ParentHash: parent.Hash})
	if err != nil || !containsHash(active, old.Hash) || containsHash(active, first.Hash) || containsHash(active, second.Hash) {
		t.Fatalf("failed completion changed selected generation: %+v, %v", active, err)
	}

	secondPipeline := firstPipeline
	secondPipeline.ClipHash = second.Hash
	if err := s.UpsertClipPipeline(ctx, secondPipeline); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteSplitConfirmation(ctx, completion); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSplitProposal(ctx, proposal.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("committed completion retained proposal: %v", err)
	}
	gotParent, err = s.GetClip(ctx, parent.Hash)
	if err != nil || !gotParent.IsComposite || gotParent.Held {
		t.Fatalf("committed completion parent = %+v, %v", gotParent, err)
	}
	active, err = s.ListClips(ctx, ClipFilter{ParentHash: parent.Hash})
	if err != nil || containsHash(active, old.Hash) || !containsHash(active, first.Hash) || !containsHash(active, second.Hash) {
		t.Fatalf("committed selected generation = %+v, %v", active, err)
	}
	for _, hash := range []string{first.Hash, second.Hash} {
		pipeline, found, err := s.GetClipPipeline(ctx, hash)
		if err != nil || !found || pipeline.Disposition != filler.DispositionRunning {
			t.Errorf("committed child pipeline %s = %+v, found=%v err=%v", hash, pipeline, found, err)
		}
	}
}

func testSplitConfirmationRequiresReviewParent(t *testing.T, newStore NewStoreFunc) {
	for _, tc := range []struct {
		name              string
		parentDisposition *filler.Disposition
	}{
		{name: "missing parent pipeline"},
		{name: "parent already filed", parentDisposition: func() *filler.Disposition {
			disposition := filler.DispositionFiled
			return &disposition
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			ctx := context.Background()
			now := time.Unix(1_900_100_000, 0).UTC()
			parent := sampleClip("review-parent", "review-parent.mp4", filler.Commercial, 1994, filler.General, "")
			parent.Held = true
			old := sampleClip("review-old", "review-old.mp4", filler.Commercial, 1994, filler.General, "")
			old.ParentHash = parent.Hash
			child := sampleClip("review-child", "review-child.mp4", filler.Commercial, 1994, filler.General, "")
			child.ParentHash = parent.Hash
			for _, clip := range []Clip{parent, old, child} {
				if err := s.UpsertClip(ctx, clip); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.SetClipsRemoved(ctx, []string{child.Path}, now); err != nil {
				t.Fatal(err)
			}
			childPipeline := filler.ClipPipeline{
				ClipHash: child.Hash, Stage: filler.StageProbe, Status: filler.StatusQueued,
				Disposition: filler.DispositionReview, EnrolledAt: now, UpdatedAt: now,
			}
			if err := s.UpsertClipPipeline(ctx, childPipeline); err != nil {
				t.Fatal(err)
			}
			if tc.parentDisposition != nil {
				if err := s.UpsertClipPipeline(ctx, filler.ClipPipeline{
					ClipHash: parent.Hash, Stage: filler.StageSplit, Status: filler.StatusQueued,
					Disposition: *tc.parentDisposition, EnrolledAt: now, UpdatedAt: now,
				}); err != nil {
					t.Fatal(err)
				}
			}
			proposal := filler.SplitProposal{
				ID: "review-proposal", ClipHash: parent.Hash, CreatedAt: now,
				Segments: []filler.SplitSegment{{StartMs: 0, EndMs: 10_000, Name: "child"}},
			}
			if err := s.UpsertSplitProposal(ctx, proposal); err != nil {
				t.Fatal(err)
			}
			const claimToken = "review-owner"
			if _, err := s.AcquireSplitProposalClaim(ctx, proposal.ID, claimToken, now, now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}

			_, err := s.CompleteSplitConfirmation(ctx, filler.SplitCompletion{
				ProposalID: proposal.ID, ClaimToken: claimToken, ParentHash: parent.Hash,
				ChildHashes: []string{child.Hash}, ActivateHashes: []string{child.Hash}, At: now,
			})
			if err == nil {
				t.Fatal("completion succeeded without exactly one review parent pipeline transition")
			}
			if got, err := s.GetSplitProposal(ctx, proposal.ID); err != nil || got.ClipHash != parent.Hash {
				t.Fatalf("failed completion consumed proposal: got %+v, err %v", got, err)
			}
			gotParent, err := s.GetClip(ctx, parent.Hash)
			if err != nil || gotParent.IsComposite || !gotParent.Held {
				t.Fatalf("failed completion changed held parent: %+v, %v", gotParent, err)
			}
			gotChildPipeline, found, err := s.GetClipPipeline(ctx, child.Hash)
			if err != nil || !found || gotChildPipeline.Disposition != filler.DispositionReview {
				t.Fatalf("failed completion activated child: %+v, found=%v err=%v", gotChildPipeline, found, err)
			}
			active, err := s.ListClips(ctx, ClipFilter{ParentHash: parent.Hash})
			if err != nil || !containsHash(active, old.Hash) || containsHash(active, child.Hash) {
				t.Fatalf("failed completion changed selected generation: %+v, %v", active, err)
			}
		})
	}
}

func testSplitProposalClaimFencesConfirmers(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_901_000_000, 0).UTC()
	proposal := filler.SplitProposal{
		ID: "claim-proposal", ClipHash: "claim-parent", CreatedAt: now,
		Segments: []filler.SplitSegment{{StartMs: 0, EndMs: 10_000, Name: "one"}},
	}
	if err := s.UpsertSplitProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.AcquireSplitProposalClaim(ctx, proposal.ID, "owner-a", now, now.Add(time.Minute))
	if err != nil || claimed.ID != proposal.ID {
		t.Fatalf("first claim = %+v, %v", claimed, err)
	}
	if _, err := s.AcquireSplitProposalClaim(ctx, proposal.ID, "owner-b", now.Add(time.Second), now.Add(2*time.Minute)); !errors.Is(err, filler.ErrProposalClaimed) {
		t.Fatalf("concurrent claim = %v, want ErrProposalClaimed", err)
	}

	changed := proposal
	changed.Segments = []filler.SplitSegment{{StartMs: 10_000, EndMs: 20_000, Name: "two"}}
	changed.Spawned = []string{"claim-child"}
	childPipeline := filler.ClipPipeline{
		ClipHash: "claim-child", Stage: filler.StageProbe, Status: filler.StatusQueued,
		Disposition: filler.DispositionReview, EnrolledAt: now, UpdatedAt: now,
	}
	if err := s.UpsertClipPipeline(ctx, childPipeline); err != nil {
		t.Fatal(err)
	}
	partial := filler.SplitPartialCompletion{
		Proposal: changed, ClaimToken: "owner-b", ActivateHashes: []string{"claim-child"}, At: now,
	}
	if err := s.CompletePartialSplitConfirmation(ctx, partial); !errors.Is(err, filler.ErrProposalClaimed) {
		t.Fatalf("losing partial completion = %v, want ErrProposalClaimed", err)
	}
	if got, err := s.GetSplitProposal(ctx, proposal.ID); err != nil || !reflect.DeepEqual(got.Segments, proposal.Segments) {
		t.Fatalf("losing token mutated proposal: %+v, %v", got, err)
	}

	// Expiry is the crash-recovery path. The new fencing token takes ownership; the stale owner can
	// neither release nor complete after that transition.
	claimed, err = s.AcquireSplitProposalClaim(ctx, proposal.ID, "owner-b", now.Add(time.Minute), now.Add(2*time.Minute))
	if err != nil || claimed.ID != proposal.ID {
		t.Fatalf("expired claim recovery = %+v, %v", claimed, err)
	}
	if err := s.ReleaseSplitProposalClaim(ctx, proposal.ID, "owner-a"); !errors.Is(err, filler.ErrProposalClaimed) {
		t.Fatalf("stale release = %v, want ErrProposalClaimed", err)
	}
	partial.ClaimToken = "owner-a"
	if err := s.CompletePartialSplitConfirmation(ctx, partial); !errors.Is(err, filler.ErrProposalClaimed) {
		t.Fatalf("stale completion = %v, want ErrProposalClaimed", err)
	}
	partial.ClaimToken = "owner-b"
	partial.ActivateHashes = []string{"claim-child", "missing-child"}
	if err := s.CompletePartialSplitConfirmation(ctx, partial); err == nil {
		t.Fatal("partial completion succeeded without every staged child pipeline")
	}
	if pipeline, found, err := s.GetClipPipeline(ctx, "claim-child"); err != nil || !found || pipeline.Disposition != filler.DispositionReview {
		t.Fatalf("failed partial completion activated first child: %+v, found=%v err=%v", pipeline, found, err)
	}
	if got, err := s.GetSplitProposal(ctx, proposal.ID); err != nil || !reflect.DeepEqual(got.Segments, proposal.Segments) {
		t.Fatalf("failed child activation mutated proposal: %+v, %v", got, err)
	}
	partial.ActivateHashes = []string{"claim-child"}
	if err := s.CompletePartialSplitConfirmation(ctx, partial); err != nil {
		t.Fatalf("winning partial completion: %v", err)
	}
	got, err := s.GetSplitProposal(ctx, proposal.ID)
	if err != nil || !reflect.DeepEqual(got.Segments, changed.Segments) {
		t.Fatalf("winning partial completion = %+v, %v", got, err)
	}
	if pipeline, found, err := s.GetClipPipeline(ctx, "claim-child"); err != nil || !found || pipeline.Disposition != filler.DispositionRunning {
		t.Fatalf("winning partial completion pipeline: %+v, found=%v err=%v", pipeline, found, err)
	}
	if _, err := s.AcquireSplitProposalClaim(ctx, proposal.ID, "owner-c", now.Add(2*time.Minute), now.Add(3*time.Minute)); err != nil {
		t.Fatalf("partial completion did not release claim: %v", err)
	}
	if err := s.ReleaseSplitProposalClaim(ctx, proposal.ID, "owner-c"); err != nil {
		t.Fatalf("release current claim: %v", err)
	}
}

func containsHash(clips []Clip, hash string) bool {
	for _, c := range clips {
		if c.Hash == hash {
			return true
		}
	}
	return false
}

// testTaxonomy pins the V45a taxonomy store: the default forest is seeded on open, tagging a clip
// expands to denormalised rollups, and re-tagging REPLACES (never accumulates) leaves while rollups
// track the current graph.
func testTaxonomy(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()

	// ⚠ Seeded on open: a fresh store already has the default forest (the boot seeder ran in Open).
	taxa, err := s.ListTaxa(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(taxa) < 40 {
		t.Fatalf("taxonomy not seeded on open: %d taxa, want the default forest (~55)", len(taxa))
	}
	forest := taxonomy.New(taxa)
	if _, ok := forest.Get("beer"); !ok {
		t.Fatal("seeded forest missing 'beer' — the seed did not load from SeedForest")
	}
	for _, hash := range []string{"clipA", "clipB"} {
		c := sampleClip(hash, hash+".mp4", filler.Commercial, 0, "", "")
		c.Held = true
		if err := s.UpsertClip(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	// Tag a clip `beer` → the denormalised set must be beer(leaf) + alcohol + drinks (rollups).
	if err := s.SetClipTags(ctx, "clipA", []string{"beer"}); err != nil {
		t.Fatal(err)
	}
	full, _ := s.GetClipTags(ctx, "clipA", false)
	assertSet(t, "clipA full tags", full, []string{"alcohol", "beer", "drinks"})
	leaves, _ := s.GetClipTags(ctx, "clipA", true)
	assertSet(t, "clipA leaves", leaves, []string{"beer"})

	// Two leaves sharing an ancestor: beer + spirits → alcohol/drinks stored ONCE (as rollups).
	if err := s.SetClipTags(ctx, "clipB", []string{"beer", "spirits", "beer"}); err != nil {
		t.Fatal(err)
	}
	full, _ = s.GetClipTags(ctx, "clipB", false)
	assertSet(t, "clipB full tags", full, []string{"alcohol", "beer", "drinks", "spirits"})

	// ⚠ The READ PATH loads Tags (§10 V45a): a real clip row, tagged, must come back from GetClip and
	// ListClips with its full leaf+rollup set in Clip.Tags — the batched attachTags load. Without this
	// the whole pod/DTO layer would read empty tags off every clip. Seed an actual clip (the tag rows
	// above are bare taxon rows with no clip); tag it; read it back both ways.
	realClip := Clip{UpdatedAt: now}
	realClip.Hash = "clipReal"
	realClip.Path = "cr/clipReal.mp4"
	realClip.Name = "Real"
	realClip.Kind = filler.Commercial
	realClip.DurationMs = 30000
	if err := s.UpsertClip(ctx, realClip); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClipTags(ctx, "clipReal", []string{"beer"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetClip(ctx, "clipReal")
	if err != nil {
		t.Fatal(err)
	}
	assertSet(t, "GetClip loads Tags (full rollup set)", got.Tags, []string{"alcohol", "beer", "drinks"})
	assertSet(t, "GetClip loads only authored AssertedTags", got.AssertedTags, []string{"beer"})
	listed, err := s.ListClips(ctx, ClipFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range listed {
		if c.Hash == "clipReal" {
			found = true
			assertSet(t, "ListClips loads Tags (full rollup set)", c.Tags, []string{"alcohol", "beer", "drinks"})
			assertSet(t, "ListClips loads only authored AssertedTags", c.AssertedTags, []string{"beer"})
		}
	}
	if !found {
		t.Error("clipReal missing from ListClips")
	}

	// ⚠ Re-tag REPLACES, never accumulates: clipA re-tagged `cereal` must lose beer/alcohol/drinks and
	// gain cereal/food — not keep the old alcohol lineage.
	if err := s.SetClipTags(ctx, "clipA", []string{"cereal"}); err != nil {
		t.Fatal(err)
	}
	full, _ = s.GetClipTags(ctx, "clipA", false)
	assertSet(t, "clipA after re-tag", full, []string{"cereal", "food"})
	if err := s.SetClipTags(ctx, "clipA", []string{"taxonomy-changed-under-editor"}); !errors.Is(err, ErrTaxonConflict) {
		t.Fatalf("retag with missing current taxon = %v, want ErrTaxonConflict", err)
	}
	full, _ = s.GetClipTags(ctx, "clipA", false)
	assertSet(t, "rejected retag preserves prior generation", full, []string{"cereal", "food"})
	clipA, err := s.GetClip(ctx, "clipA")
	if err != nil || clipA.Category != "cereal" {
		t.Fatalf("rejected retag category = %q, err=%v; want cereal", clipA.Category, err)
	}

	// Operator edit: add a taxon; it must appear in ListTaxa (the CRUD path).
	if err := s.ApplyTaxonomyEdit(ctx, TaxonomyEdit{Create: true, Taxon: taxonomy.Taxon{Slug: "energy-drink", Label: "Energy drink", Parent: "drinks", Axis: taxonomy.AxisProduct}}, now); err != nil {
		t.Fatal(err)
	}
	taxa2, _ := s.ListTaxa(ctx)
	if _, ok := taxonomy.New(taxa2).Get("energy-drink"); !ok {
		t.Error("ApplyTaxonomyEdit did not persist the new taxon")
	}

	// Invalid graph edits are rejected atomically: a cross-axis parent must change neither the row
	// nor the closure/rollups the catalog is matching against.
	bad := taxonomy.Taxon{Slug: "energy-drink", Label: "Energy drink", Parent: "promo", Axis: taxonomy.AxisProduct}
	if err := s.ApplyTaxonomyEdit(ctx, TaxonomyEdit{Slug: bad.Slug, Taxon: bad}, now); !errors.Is(err, taxonomy.ErrInvalidForest) {
		t.Fatalf("cross-axis edit = %v, want ErrInvalidForest", err)
	}
	if got, _ := taxonomy.New(mustList(t, s, ctx)).Get("energy-drink"); got.Parent != "drinks" {
		t.Errorf("rejected edit changed parent to %q, want drinks", got.Parent)
	}

	// Concurrent editors must serialize around the WHOLE graph generation. Postgres otherwise lets
	// both requests validate the same snapshot, then interleave their closure replacement so one
	// newly-added node has no ancestry. SQLite serializes writes itself; the same test pins both.
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for _, slug := range []string{"sports-drink", "sparkling-water"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- s.ApplyTaxonomyEdit(ctx, TaxonomyEdit{Create: true, Taxon: taxonomy.Taxon{
				Slug: slug, Label: slug, Parent: "drinks", Axis: taxonomy.AxisProduct,
			}}, now)
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent taxonomy edit: %v", err)
		}
	}
	concurrentForest := taxonomy.New(mustList(t, s, ctx))
	sqls, ok := s.(*sqlStore)
	if !ok {
		t.Fatalf("taxonomy conformance store = %T, want *sqlStore", s)
	}
	for _, slug := range []string{"sports-drink", "sparkling-water"} {
		if got := concurrentForest.Ancestors(slug); len(got) != 1 || got[0] != "drinks" {
			t.Errorf("concurrent taxon %q ancestors = %v, want [drinks]", slug, got)
		}
		var closureRows int
		if err := sqls.db.QueryRowContext(ctx, sqls.ph(
			`SELECT COUNT(*) FROM taxa_closure WHERE ancestor = ? AND descendant = ?`), "drinks", slug).Scan(&closureRows); err != nil {
			t.Fatal(err)
		}
		if closureRows != 1 {
			t.Errorf("concurrent taxon %q closure rows = %d, want drinks ancestor committed atomically", slug, closureRows)
		}
	}

	// A used asserted taxon cannot be deleted: vocabulary cleanup may not erase library knowledge.
	if err := s.ApplyTaxonomyEdit(ctx, TaxonomyEdit{Delete: true, Slug: "beer"}, now); !errors.Is(err, ErrTaxonConflict) {
		t.Fatalf("delete asserted beer = %v, want ErrTaxonConflict", err)
	}
	usage, err := s.TaxonomyUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage.TotalClips != 1 || usage.TaggedClips != 1 {
		t.Errorf("taxonomy coverage = %d total/%d tagged, want 1/1", usage.TotalClips, usage.TaggedClips)
	}
	if got := usage.ByTaxon["beer"]; got.Asserted != 1 || got.Matched != 1 {
		t.Errorf("beer usage = %+v, want asserted=1 matched=1", got)
	}
	if got := usage.ByTaxon["beer"].Stored; got != 2 {
		t.Errorf("beer stored assignments = %d, want 2 including the non-playable fixture", got)
	}
	if got := usage.ByTaxon["drinks"]; got.Asserted != 0 || got.Matched != 1 {
		t.Errorf("drinks usage = %+v, want asserted=0 matched=1", got)
	}

	// Preview uses the same prospective-graph validation as commit but does not mutate it. It counts
	// distinct stored assertions across the affected subtree, separately identifies playable clips
	// that may change channel fit, and explains resolver behavior changes.
	beer, ok := taxonomy.New(mustList(t, s, ctx)).Get("beer")
	if !ok {
		t.Fatal("seed taxonomy has no beer taxon")
	}
	beer.Synonyms = append(beer.Synonyms, "pint")
	impact, err := s.PreviewTaxonomyEdit(ctx, TaxonomyEdit{Slug: beer.Slug, Taxon: beer})
	if err != nil {
		t.Fatal(err)
	}
	if impact.DirectStoredClips != 2 || impact.DescendantStoredClips != 0 || impact.AffectedStoredClips != 2 {
		t.Errorf("beer preview stored impact = direct %d/descendant %d/affected %d, want 2/0/2", impact.DirectStoredClips, impact.DescendantStoredClips, impact.AffectedStoredClips)
	}
	if len(impact.PlayableClipHashes) != 0 {
		t.Errorf("resolver-only preview playable hashes = %v, want none because eligibility is unchanged", impact.PlayableClipHashes)
	}
	assertSet(t, "beer preview added resolver terms", impact.ResolverTermsAdded, []string{"pint"})
	if len(impact.ResolverTermsRemoved) != 0 {
		t.Errorf("beer preview removed resolver terms = %v, want none", impact.ResolverTermsRemoved)
	}
	if liveBeer, _ := taxonomy.New(mustList(t, s, ctx)).Get("beer"); len(liveBeer.Synonyms) == len(beer.Synonyms) {
		t.Error("PreviewTaxonomyEdit mutated the live graph")
	}

	alcoholImpact, err := s.PreviewTaxonomyEdit(ctx, TaxonomyEdit{Delete: true, Slug: "alcohol"})
	if err != nil {
		t.Fatal(err)
	}
	if alcoholImpact.DirectStoredClips != 0 || alcoholImpact.DescendantStoredClips != 2 || alcoholImpact.AffectedStoredClips != 2 {
		t.Errorf("alcohol delete impact = direct %d/descendant %d/affected %d, want 0/2/2", alcoholImpact.DirectStoredClips, alcoholImpact.DescendantStoredClips, alcoholImpact.AffectedStoredClips)
	}
	gotDescendants := make([]string, 0, len(alcoholImpact.Descendants))
	for _, descendant := range alcoholImpact.Descendants {
		gotDescendants = append(gotDescendants, descendant.Slug)
	}
	assertSet(t, "alcohol descendants", gotDescendants, []string{"beer", "spirits"})
	if _, err := s.PreviewTaxonomyEdit(ctx, TaxonomyEdit{Slug: "beer", Taxon: taxonomy.Taxon{Slug: "beer", Label: "Beer", Parent: "promo", Axis: taxonomy.AxisProduct}}); !errors.Is(err, taxonomy.ErrInvalidForest) {
		t.Fatalf("invalid taxonomy preview = %v, want ErrInvalidForest", err)
	}
	if usage.ByAxis[taxonomy.AxisProduct] != 1 || usage.ByAxis[taxonomy.AxisFormat] != 0 {
		t.Errorf("axis coverage product=%d format=%d, want 1/0 unique playable clips", usage.ByAxis[taxonomy.AxisProduct], usage.ByAxis[taxonomy.AxisFormat])
	}

	// `category` is a compatibility shadow consumed by pod matching. A graph edit that moves an
	// asserted node off the product axis must clear it in the SAME transaction as the graph and
	// rollups, or the catalog and scheduler disagree about what the clip is.
	shadowTaxon := taxonomy.Taxon{Slug: "shadow-product", Label: "Shadow product", Axis: taxonomy.AxisProduct}
	if err := s.ApplyTaxonomyEdit(ctx, TaxonomyEdit{Create: true, Taxon: shadowTaxon}, now); err != nil {
		t.Fatal(err)
	}
	shadowClip := Clip{Clip: clipAt("shadow.mp4", "Shadow", filler.Commercial, 30_000), UpdatedAt: now}
	if err := s.UpsertClip(ctx, shadowClip); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClipTags(ctx, shadowClip.Hash, []string{shadowTaxon.Slug}); err != nil {
		t.Fatal(err)
	}
	beforeShadow, err := s.GetClip(ctx, shadowClip.Hash)
	if err != nil || beforeShadow.Category != shadowTaxon.Slug {
		t.Fatalf("per-clip taxonomy write category = %q, err=%v; want %q", beforeShadow.Category, err, shadowTaxon.Slug)
	}
	shadowTaxon.Axis = taxonomy.AxisFormat
	if err := s.ApplyTaxonomyEdit(ctx, TaxonomyEdit{Slug: shadowTaxon.Slug, Taxon: shadowTaxon}, now); err != nil {
		t.Fatal(err)
	}
	gotShadow, err := s.GetClip(ctx, shadowClip.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if gotShadow.Category != "" || len(gotShadow.AssertedTags) != 1 || gotShadow.AssertedTags[0] != shadowTaxon.Slug {
		t.Errorf("axis edit left category/assertions = %q / %v, want empty / [%s]", gotShadow.Category, gotShadow.AssertedTags, shadowTaxon.Slug)
	}

	testAtomicTaxonomyEdit(t, s, ctx, now)
}

// testAtomicTaxonomyEdit pins the V55 contract: changing the graph re-derives every affected
// clip's rollups before the semantic edit returns. There is no second public rebuild operation or
// scheduled repair job that can expose two vocabulary generations.
func testAtomicTaxonomyEdit(t *testing.T, s Store, ctx context.Context, now time.Time) {
	// Insert an intermediate parent above cereal. clipA already asserts cereal, so the graph edit
	// itself must add breakfast to that existing clip while preserving its direct assertion.
	if err := s.ApplyTaxonomyEdit(ctx, TaxonomyEdit{Create: true, Taxon: taxonomy.Taxon{
		Slug: "breakfast", Label: "Breakfast", Parent: "food", Axis: taxonomy.AxisProduct,
	}}, now); err != nil {
		t.Fatal(err)
	}
	cereal, ok := taxonomy.New(mustList(t, s, ctx)).Get("cereal")
	if !ok {
		t.Fatal("seed taxonomy has no cereal taxon")
	}
	cereal.Parent = "breakfast"
	if err := s.ApplyTaxonomyEdit(ctx, TaxonomyEdit{Slug: cereal.Slug, Taxon: cereal}, now); err != nil {
		t.Fatal(err)
	}
	fullA, _ := s.GetClipTags(ctx, "clipA", false)
	assertSet(t, "graph edit atomically reindexes existing clip", fullA, []string{"cereal", "breakfast", "food"})
	leavesA, _ := s.GetClipTags(ctx, "clipA", true)
	assertSet(t, "graph edit preserves asserted leaves", leavesA, []string{"cereal"})

	// ⚠ A graph edit updates existing clip lineages. Insert a taxon BETWEEN an existing leaf and its
	// parent, so the rollup set genuinely changes. `pilsner` under a new `ale-family` under `beer`.
	if err := s.ApplyTaxonomyEdit(ctx, TaxonomyEdit{Create: true, Taxon: taxonomy.Taxon{Slug: "ale-family", Label: "Ale family", Parent: "beer", Axis: taxonomy.AxisProduct}}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyTaxonomyEdit(ctx, TaxonomyEdit{Create: true, Taxon: taxonomy.Taxon{Slug: "pilsner", Label: "Pilsner", Parent: "ale-family", Axis: taxonomy.AxisProduct}}, now); err != nil {
		t.Fatal(err)
	}
	clipC := sampleClip("clipC", "clipC.mp4", filler.Commercial, 0, "", "")
	clipC.Held = true
	if err := s.UpsertClip(ctx, clipC); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClipTags(ctx, "clipC", []string{"pilsner"}); err != nil {
		t.Fatal(err)
	}
	// With the ale-family hop present: pilsner → ale-family → beer → alcohol → drinks.
	afterC, _ := s.GetClipTags(ctx, "clipC", false)
	assertSet(t, "clipC rollups with ale-family hop", afterC, []string{"pilsner", "ale-family", "beer", "alcohol", "drinks"})

	// ⚠ DELETE the MIDDLE taxon: DeleteTaxon REPARENTS lager to the grandparent (beer), so the lineage
	// survives minus the removed level — it does NOT orphan lager or leave a phantom 'ale-family'
	// rollup. This is the "remove a middle category" behaviour, and the direction a stale closure OR a
	// dangling-parent Ancestors would get wrong.
	if err := s.ApplyTaxonomyEdit(ctx, TaxonomyEdit{Delete: true, Slug: "ale-family"}, now); err != nil {
		t.Fatal(err)
	}
	// Confirm the reparent landed at the graph level.
	if lg, ok := taxonomy.New(mustList(t, s, ctx)).Get("pilsner"); !ok || lg.Parent != "beer" {
		t.Fatalf("ApplyTaxonomyEdit did not reparent pilsner to beer: got parent %q (ok=%v)", lg.Parent, ok)
	}
	afterC, _ = s.GetClipTags(ctx, "clipC", false)
	// ale-family is GONE (reparented away, not a phantom ancestor); the rest of the lineage survives.
	assertSet(t, "clipC rollups after middle-taxon delete", afterC, []string{"pilsner", "beer", "alcohol", "drinks"})
	// pilsner itself (the asserted leaf) survives the graph edit.
	leavesC, _ := s.GetClipTags(ctx, "clipC", true)
	assertSet(t, "clipC leaf survives ancestor deletion", leavesC, []string{"pilsner"})
}

// mustList fetches the whole taxonomy or fails the test — a small helper for the reindex assertions
// that rebuild a forest from the live graph.
func mustList(t *testing.T, s Store, ctx context.Context) []taxonomy.Taxon {
	t.Helper()
	taxa, err := s.ListTaxa(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return taxa
}

// assertSet compares two string slices as SETS (order-independent), for the tag assertions above.
func assertSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	gm := map[string]bool{}
	for _, g := range got {
		gm[g] = true
	}
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v (set)", what, got, want)
		return
	}
	for _, w := range want {
		if !gm[w] {
			t.Errorf("%s = %v, missing %q (want %v)", what, got, w, want)
		}
	}
}

// testClipCounts pins the count queries against the listing they replaced.
//
// ⚠ Every assertion compares COUNT(*) against len(ListClips(sameFilter)) rather than a literal.
// That is the whole point: the counts exist to avoid materialising rows, so the only property
// worth pinning is that they still answer the SAME question as the listing. A hard-coded number
// would keep passing if the two predicates drifted apart, which is the one failure mode here.
func testClipCounts(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()

	seed := func(id, source string, held, autoFiled bool, era int) {
		c := sampleClip(id, id+".mp4", filler.Commercial, era, filler.Kids, "toys")
		c.Source = source
		c.Held = held
		c.AutoFiled = autoFiled
		if err := s.UpsertClip(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	seed("a1", "youtube", false, false, 1990)
	seed("a2", "youtube", false, true, 1991)
	seed("a3", "archive", false, false, 0) // untagged: era 0
	seed("a4", "archive", true, false, 1992)
	seed("a5", "", false, false, 1993)

	filters := map[string]ClipFilter{
		"catalog":   {},
		"held":      {HeldOnly: true},
		"untagged":  {UntaggedOnly: true},
		"autofiled": {AutoFiledOnly: true},
		"by-kind":   {Kind: filler.Commercial},
	}
	for name, f := range filters {
		listed, err := s.ListClips(ctx, f)
		if err != nil {
			t.Fatalf("%s list: %v", name, err)
		}
		got, err := s.CountClips(ctx, f)
		if err != nil {
			t.Fatalf("%s count: %v", name, err)
		}
		if got != len(listed) {
			t.Errorf("CountClips(%s) = %d, but ListClips returned %d — the two predicates have drifted",
				name, got, len(listed))
		}
	}

	// AutoFiledOnly must actually narrow, or the assertion above passes vacuously against a
	// filter the WHERE builder ignores.
	if n, _ := s.CountClips(ctx, ClipFilter{AutoFiledOnly: true}); n != 1 {
		t.Errorf("auto-filed count = %d, want exactly the 1 seeded auto-filed clip", n)
	}

	// The per-source rollup must agree with the catalog total, or the Sources page's "N sources ·
	// M clips" line contradicts itself.
	bySource, err := s.CountClipsBySource(ctx, ClipFilter{})
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, n := range bySource {
		sum += n
	}
	total, _ := s.CountClips(ctx, ClipFilter{})
	if sum != total {
		t.Errorf("per-source counts sum to %d but the catalog holds %d — a clip vanished from the rollup", sum, total)
	}
	if bySource["youtube"] != 2 {
		t.Errorf("youtube = %d, want 2", bySource["youtube"])
	}
	// ⚠ The empty source must survive as its own bucket. `source` is free text, so an unknown or
	// blank value is possible, and dropping it would silently lose clips from a page whose whole
	// job is accounting for where they came from.
	if bySource[""] != 1 {
		t.Errorf("blank source = %d, want the 1 seeded clip — an unattributed clip must not vanish", bySource[""])
	}
	// Held is excluded by default, exactly as in the listing.
	if bySource["archive"] != 1 {
		t.Errorf("archive = %d, want 1 (the held clip is not in the catalog)", bySource["archive"])
	}
}

// testClipLicense pins that a clip's declared licence round-trips on BOTH backends, and that an
// absent one stays absent. ⚠ Empty means UNKNOWN, never "public domain" — ~92% of archive.org
// items declare none, so the empty case is the common one and must not acquire a default.
func testClipLicense(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	licensed := Clip{Clip: filler.Clip{
		Hash: "licensed.mp4",
		Path: "licensed.mp4", Name: "Licensed", Kind: filler.Commercial, DurationMs: 30000,
		License: "https://creativecommons.org/publicdomain/zero/1.0/",
	}, UpdatedAt: now}
	unknown := Clip{Clip: filler.Clip{
		Hash: "unknown.mp4",
		Path: "unknown.mp4", Name: "Unknown", Kind: filler.Commercial, DurationMs: 30000,
	}, UpdatedAt: now}
	for _, c := range []Clip{licensed, unknown} {
		if err := s.UpsertClip(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.GetClip(ctx, "licensed.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if got.License != licensed.License {
		t.Errorf("licence = %q, want %q", got.License, licensed.License)
	}
	if got, err = s.GetClip(ctx, "unknown.mp4"); err != nil {
		t.Fatal(err)
	}
	if got.License != "" {
		t.Errorf("a clip with no declared licence has %q, want empty", got.License)
	}
}

// testClipHeld covers the V38 clip lifecycle on BOTH backends: a held clip is recorded but is not
// in the playable catalog, and only SetClipsHeld moves it.
//
// ⚠ The first assertion is the property the whole lifecycle rests on. Pod assembly, coverage, the
// filler-list builder and the catalog listing all read through ListClips with a zero filter, so if
// held clips were not excluded THERE, every untagged unreviewed download would air.
func testClipHeld(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	at := time.Now().UTC().Truncate(time.Second)

	filed := Clip{Clip: filler.Clip{
		Hash: "filed.mp4",
		Path: "filed.mp4", Name: "Filed", Kind: filler.Commercial, DurationMs: 30000,
		Era: 1990, Audience: filler.Kids, Category: "toys",
	}, UpdatedAt: at}
	held := Clip{Clip: filler.Clip{
		Hash: "held.mp4",
		Path: "held.mp4", Name: "Held", Kind: filler.Commercial, DurationMs: 30000,
		Era: 1990, Audience: filler.Kids, Category: "toys", Held: true, Confidence: 40,
	}, UpdatedAt: at}
	for _, c := range []Clip{filed, held} {
		if err := s.UpsertClip(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	// ⚠ A ZERO filter is what pod assembly passes. A held clip must not be in this answer.
	got, err := s.ListClips(ctx, ClipFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.Path == "held.mp4" {
			t.Fatal("a HELD clip came back from a zero-filter ListClips — pod assembly reads " +
				"exactly this, so an unreviewed clip would air")
		}
	}
	if len(got) != 1 {
		t.Fatalf("catalog has %d clips, want 1 (the filed one)", len(got))
	}

	// The review queue is the inverse, and it is how Incoming finds its work.
	queue, err := s.ListClips(ctx, ClipFilter{HeldOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 1 || queue[0].Path != "held.mp4" {
		t.Fatalf("HeldOnly returned %d clips, want just held.mp4", len(queue))
	}
	if queue[0].Confidence != 40 {
		t.Errorf("confidence = %d, want 40 — the score must round-trip", queue[0].Confidence)
	}

	// ⚠ THE trap this lifecycle has to survive: `clips` is a synced CACHE, so the folder scan
	// re-upserts every file it finds with held=false. If `held` rode along in UpsertClip's DO
	// UPDATE list, one scan pass would file every held clip — emptying the review queue into live
	// channels with no operator action and nothing in the logs.
	rescan := held
	rescan.Held = false
	rescan.Confidence = 0 // a scan knows nothing about tagging
	if err := s.UpsertClip(ctx, rescan); err != nil {
		t.Fatal(err)
	}
	after, err := s.ListClips(ctx, ClipFilter{HeldOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatal("a re-scan FILED a held clip — UpsertClip must omit `held` from its DO UPDATE " +
			"list, exactly as it omits the removal tombstone")
	}
	if after[0].Confidence != 40 {
		t.Errorf("a re-scan blanked the confidence score (%d) — it must be omitted too, or a "+
			"trusted clip starts asking again for no reason", after[0].Confidence)
	}

	// ⚠ **`SetClipConfidence` is the score's WRITER, and the assertions above cannot see whether
	// one exists.** They seed the value through `UpsertClip`'s INSERT and prove it round-trips and
	// survives a re-scan — all true, and all true for two phases while NOTHING in the application
	// ever wrote a score. `confidence` was 0 in every real catalog. So exercise the writer itself:
	// it is path-keyed like its `SetClipLanguage`/`SetClipTranscript` neighbours, and its value has
	// to outlive the folder scan the same way the seeded one does.
	if err := s.SetClipConfidence(ctx, "held.mp4", 92, at); err != nil {
		t.Fatal(err)
	}
	scored, err := s.ListClips(ctx, ClipFilter{HeldOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(scored) != 1 || scored[0].Confidence != 92 {
		t.Fatalf("SetClipConfidence did not write the score: %+v", scored)
	}
	if err := s.UpsertClip(ctx, rescan); err != nil {
		t.Fatal(err)
	}
	rescored, err := s.ListClips(ctx, ClipFilter{HeldOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rescored) != 1 || rescored[0].Confidence != 92 {
		t.Errorf("a re-scan blanked a WRITTEN score (%+v) — `confidence` must stay out of "+
			"UpsertClip's DO UPDATE list for the writer's value, not just the seeded one", rescored)
	}

	// Filing is the only way out, and it records that nobody looked.
	if _, err := s.SetClipsHeld(ctx, []string{"held.mp4"}, false, true, at); err != nil {
		t.Fatal(err)
	}
	catalog, err := s.ListClips(ctx, ClipFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 2 {
		t.Fatalf("after filing, catalog has %d clips, want 2", len(catalog))
	}
	var flag bool
	for _, c := range catalog {
		if c.Path == "held.mp4" {
			flag = c.AutoFiled
		}
	}
	if !flag {
		t.Error("auto_filed did not survive — it is the only thing that can answer " +
			"'which of these did I never see?'")
	}
}

func testFillerSources(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)

	// ⚠ A FRESH install ships with fetchable sources (§10 V38c.8, migration 00034). Asserted here,
	// on BOTH backends, because a seed that lands on sqlite and not on postgres is exactly the
	// dialect drift this one-suite-two-backends rule exists to catch — and it would show up as
	// "filler mysteriously does nothing" on one deployment only.
	for _, want := range []struct{ id, label, uri string }{
		{"archive:classic_tv_commercials", "Classic TV Commercials", "classic_tv_commercials"},
		{"archive:vhscommercials", "Commercials From The Vault", "vhscommercials"},
		{"archive:tv_ads", "TV Ads", "tv_ads"},
	} {
		got, ok := findSource(t, s, want.id)
		if !ok {
			t.Fatalf("a fresh store is missing the seeded source %q — a new install cannot fetch", want.id)
		}
		// ⚠ The LABEL is human-readable. `vhscommercials` is not a name an operator recognises,
		// and the row renders the label above the target.
		if got.Label != want.label {
			t.Errorf("%s label = %q, want %q", want.id, got.Label, want.label)
		}
		if got.URI != want.uri {
			t.Errorf("%s uri = %q, want %q", want.id, got.URI, want.uri)
		}
		if !got.Enabled {
			t.Errorf("%s seeded switched OFF — it would sit in the UI doing nothing", want.id)
		}
		if !got.AutoAdmit {
			t.Errorf("%s seeded with auto-admission OFF — upgrade must preserve the grounded workflow", want.id)
		}
		// ⚠ Fetchable, which is the whole point: `folder` and `library` are SCANNED, so before
		// this seed a fresh install had no source it could download from at all.
		if !got.Fetchable() {
			t.Errorf("%s is not fetchable — the seed exists so a new install CAN fetch", want.id)
		}
		// ⚠ EMPTY licence, and that is correct rather than missing data. All three declare none,
		// and §10 defines empty as UNKNOWN — never "public domain". A reassuring default here
		// would have Loomarr asserting a legal fact nobody checked.
		if got.License != "" {
			t.Errorf("%s licence = %q, want empty (unknown, NOT public domain)", want.id, got.License)
		}
	}

	// ⚠ YouTube seeds PRESENT BUT EMPTY. §10: Loomarr never recommends YouTube content itself, so
	// the operator brings the playlist — a seeded target would be that recommendation. The empty
	// uri also fails `Fetchable()`, which keeps the row out of every pull plan until it is filled
	// in; without that, approval would hand `Ingest` an empty string.
	if yt, ok := findSource(t, s, "youtube"); !ok {
		t.Error("a fresh store is missing the YouTube row — the mock draws it, unconfigured")
	} else {
		if yt.URI != "" {
			t.Errorf("youtube seeded with uri %q — Loomarr must not recommend a playlist", yt.URI)
		}
		if yt.Fetchable() {
			t.Error("an unconfigured youtube row is fetchable — a pull would ingest an empty string")
		}
	}

	created := time.Now().UTC().Truncate(time.Second)
	src := FillerSource{
		// Enabled explicitly: a Go bool zero-values to false, so a literal that omits it
		// describes a source that is switched OFF. Real add paths go through
		// NewFillerSource for exactly that reason.
		Enabled: true, AutoAdmit: true,
		// ⚠ NOT `classic_tv_commercials` — that is a SEEDED row now (00034), and 00032's unique
		// index on (kind, uri) correctly refuses a second row pointing at the same collection.
		// The fixture needs its own target; the index is doing its job.
		ID: "src-1", Kind: "archive", URI: "conformance_fixture_collection",
		Label:     "Classic TV commercials",
		License:   "https://creativecommons.org/licenses/by-nc-sa/4.0/",
		Geography: filler.Geography{Country: "US", Market: "New York"},
		// ⚠ Only ~8% of archive items declare a licence, so the empty case below is the
		// common one — both are covered.
		CreatedAt: created,
	}
	if err := s.UpsertFillerSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	unlicensed := FillerSource{ID: "src-2", Kind: "archive", URI: "vintage_ads", CreatedAt: created.Add(time.Second)}
	if err := s.UpsertFillerSource(ctx, unlicensed); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListFillerSources(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// ⚠ V37: the list is no longer empty on a fresh store. Migration 00029 materialises the two
	// CONFIG-BACKED singletons (`folder`, `library`) so the flat list can still say "you could
	// set up a drop-folder but have not" — §10's own answer to "why is my catalog empty?", which
	// a table of things-that-exist otherwise cannot express. So this suite asserts on the rows it
	// added, BY ID, rather than by position in the whole list.
	byID := map[string]FillerSource{}
	for _, f := range all {
		byID[f.ID] = f
	}
	for _, want := range []string{"folder", "library"} {
		if _, ok := byID[want]; !ok {
			t.Fatalf("singleton row %q missing — a fresh store must be able to say 'not configured'", want)
		}
	}
	if byID["folder"].URI != "" {
		t.Errorf("seeded folder URI = %q, want empty (= not configured, never a guessed path)", byID["folder"].URI)
	}

	// Ordering is still oldest-first and still explicit — an unordered list reshuffles between
	// reads on Postgres and the Sources tab's rows would move under the pointer. Checked over the
	// two rows this test added rather than the whole list, whose head is now seeded.
	//
	// ⚠ Filtered by ID, not by KIND. `Kind == "archive"` was unambiguous while every archive row
	// came from this test; migration 00034 seeds three of them, so the kind filter started
	// collecting the seed too and the count assertion below failed for the right reason.
	wanted := map[string]bool{"src-1": true, "src-2": true}
	var added []FillerSource
	for _, f := range all {
		if wanted[f.ID] {
			added = append(added, f)
		}
	}
	if len(added) != 2 {
		t.Fatalf("listed %d of this test's own sources, want 2", len(added))
	}
	if added[0].ID != "src-1" || added[1].ID != "src-2" {
		t.Errorf("order = %s,%s; want src-1,src-2 (oldest first)", added[0].ID, added[1].ID)
	}
	if added[0].License != src.License {
		t.Errorf("licence = %q, want %q", added[0].License, src.License)
	}
	if added[0].Geography != src.Geography {
		t.Errorf("source geography = %+v, want %+v", added[0].Geography, src.Geography)
	}
	if err := s.SetFillerSourceGeography(ctx, "src-1", filler.Geography{Country: "ca", Market: " Toronto "}); err != nil {
		t.Fatal(err)
	}
	if got, _ := findSource(t, s, "src-1"); got.Geography != (filler.Geography{Country: "CA", Market: "Toronto"}) {
		t.Errorf("updated source geography = %+v", got.Geography)
	}
	if added[1].License != "" {
		t.Errorf("unlicensed source has licence %q, want empty (= unknown)", added[1].License)
	}
	if !added[0].LastFetchedAt.IsZero() {
		t.Errorf("a never-fetched source has LastFetchedAt %v, want zero", added[0].LastFetchedAt)
	}
	if err := s.SetFillerSourceAutoAdmit(ctx, "src-1", false); err != nil {
		t.Fatal(err)
	}
	if src1(t, s).AutoAdmit {
		t.Error("source still auto-admits after its admission policy was switched off")
	}

	// ⚠ THE invariant the flat model has to carry itself (§10), MOVED in V38c from the kind to
	// the TARGET. 00029 allowed exactly one folder row; 00032 allows many, because commercials
	// living in two places is ordinary and V37 gave it no expression.
	//
	// What must still be impossible is ONE folder appearing as TWO rows — a stale row disagreeing
	// with another about the same directory, which is the precedence question 00023 refused to
	// have. So a DISTINCT path is accepted and a DUPLICATE path is refused, by the database
	// rather than by a Go guard the next caller forgets.
	second := FillerSource{ID: "folder-2", Kind: "folder", URI: "/other", Enabled: true, CreatedAt: created}
	if err := s.UpsertFillerSource(ctx, second); err != nil {
		t.Errorf("a second DISTINCT folder was refused (%v) — V38c allows many watched folders", err)
	}
	dup := FillerSource{ID: "folder-3", Kind: "folder", URI: "/other", Enabled: true, CreatedAt: created}
	if err := s.UpsertFillerSource(ctx, dup); err == nil {
		t.Error("a DUPLICATE folder path was accepted — one directory must not appear as two rows")
	}

	// ⚠ THE three-state encoding (§10 V38c). `nil` = inherit the global, `0` = never fetch this
	// source, `N` = every N seconds. They cannot collapse: `filler.fetch.every = 0` already means
	// "off", so a non-nullable column would make "unset" and "never" the same value and read as
	// "every existing source is switched off" on upgrade — 00026's mistake exactly.
	never, every900 := 0, 900
	if err := s.SetFillerSourceFetchPolicy(ctx, "src-2", &never, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFillerSourceFetchPolicy(ctx, "folder-2", &every900, &every900); err != nil {
		t.Fatal(err)
	}
	policies, err := s.ListFillerSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byPolicyID := map[string]FillerSource{}
	for _, f := range policies {
		byPolicyID[f.ID] = f
	}
	// src-1 was never given a policy: nil, and it must resolve to the global.
	if got := byPolicyID["src-1"]; got.FetchEverySeconds != nil {
		t.Errorf("src-1 override = %v, want nil (inherit)", *got.FetchEverySeconds)
	} else if d, ok := got.FetchEvery(time.Hour); !ok || d != time.Hour {
		t.Errorf("an un-overridden source resolved to (%v, %v), want the global hour", d, ok)
	}
	// src-2 was set to 0 = NEVER. ⚠ It must NOT read as "inherit" — that is the collapse.
	if got := byPolicyID["src-2"]; got.FetchEverySeconds == nil {
		t.Error("a 0 override round-tripped as NULL — 'never' collapsed into 'inherit'")
	} else if _, ok := got.FetchEvery(time.Hour); ok {
		t.Error("a 0 override resolved to a poll interval — 0 must mean NEVER")
	}
	if got := byPolicyID["folder-2"]; got.FetchEverySeconds == nil || *got.FetchEverySeconds != 900 {
		t.Errorf("folder-2 override did not round-trip: %v", got.FetchEverySeconds)
	} else if d, _ := got.FetchEvery(time.Hour); d != 900*time.Second {
		t.Errorf("resolved to %v, want 15m from the override rather than the global", d)
	}
	// Clearing goes back to inherit — a real operator action ("stop treating this specially").
	if err := s.SetFillerSourceFetchPolicy(ctx, "src-2", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := findSource(t, s, "src-2"); got.FetchEverySeconds != nil {
		t.Error("clearing an override did not return the source to inherit")
	}

	// ⚠ Two BLANK-uri rows must both survive. A seeded-but-unconfigured row carries no target —
	// that is how "you could set up a drop-folder but have not" is expressed (§10) — and a plain
	// unique index rather than a partial one would allow only ONE blank row across the table,
	// so a fresh install could not have both an unconfigured folder and an unconfigured library.
	for _, blank := range []FillerSource{
		{ID: "blank-a", Kind: "folder", URI: "", Enabled: true, CreatedAt: created},
		{ID: "blank-b", Kind: "library", URI: "", Enabled: true, CreatedAt: created},
	} {
		if err := s.UpsertFillerSource(ctx, blank); err != nil {
			t.Errorf("an unconfigured %s row was refused (%v) — 'not configured' must stay expressible",
				blank.Kind, err)
		}
	}

	// Fetch stamps.
	fetched := created.Add(time.Hour)
	if err := s.MarkFillerSourceFetched(ctx, "src-1", fetched); err != nil {
		t.Fatal(err)
	}
	if !src1(t, s).LastFetchedAt.Equal(fetched) {
		t.Errorf("LastFetchedAt = %v, want %v", src1(t, s).LastFetchedAt, fetched)
	}

	// ⚠ THE assertion this table's ON CONFLICT clause exists for. Re-registering a source
	// (an operator fixing its label) knows nothing about fetches; if last_fetched_at joined
	// the DO UPDATE list, a working source would silently look like it had never run.
	relabelled := src
	relabelled.Label = "Renamed"
	if err := s.UpsertFillerSource(ctx, relabelled); err != nil {
		t.Fatal(err)
	}
	if src1(t, s).Label != "Renamed" {
		t.Errorf("label = %q, want Renamed", src1(t, s).Label)
	}
	if !src1(t, s).LastFetchedAt.Equal(fetched) {
		t.Errorf("re-registering reset LastFetchedAt to %v — it must survive an upsert", src1(t, s).LastFetchedAt)
	}

	// The Sources tab's on/off switch (V35). Two properties, each a claim the switch's own
	// copy makes to the operator.
	if !src1(t, s).Enabled {
		t.Error("source is not enabled — a registered source must be on until switched off")
	}
	if err := s.SetFillerSourceEnabled(ctx, "src-1", false); err != nil {
		t.Fatal(err)
	}
	if src1(t, s).Enabled {
		t.Error("source still enabled after being switched off")
	}

	// 1. ⚠ Disabling is NOT deleting. The row keeps its licence and its fetch history, which
	//    is what makes switching it back on restore what was there instead of starting over.
	if src1(t, s).License != src.License {
		t.Errorf("licence lost on disable: %q", src1(t, s).License)
	}
	if !src1(t, s).LastFetchedAt.Equal(fetched) {
		t.Error("fetch history lost on disable — the row was rewritten rather than updated")
	}

	// 2. ⚠ A re-register must not flip the switch back. `UpsertFillerSource` deliberately omits
	//    `enabled` from its DO UPDATE list, for the same reason last_fetched_at is omitted: a
	//    caller fixing a label knows nothing about the switch, and a Go bool zero-values to
	//    FALSE, so writing it would silently disable a source behind the operator's back. The
	//    first draft of V35 had exactly that bug.
	reRegistered := src
	reRegistered.Label = "Renamed again"
	reRegistered.Enabled = true // what a caller who does not know about the switch would send
	if err := s.UpsertFillerSource(ctx, reRegistered); err != nil {
		t.Fatal(err)
	}
	if src1(t, s).Enabled {
		t.Error("re-registering re-enabled a disabled source — the switch is not the upsert's business")
	}
	if src1(t, s).Label != "Renamed again" {
		t.Errorf("label = %q, want the re-registered one", src1(t, s).Label)
	}

	// Put it back on, so the delete assertions below run against the normal state.
	if err := s.SetFillerSourceEnabled(ctx, "src-1", true); err != nil {
		t.Fatal(err)
	}

	// ⚠ Deleting a source must NOT delete its clips: they are real files already tagged and
	// possibly pinned into a channel, and forgetting where something came from is not a
	// reason to throw it away.
	if err := s.UpsertClip(ctx, Clip{Clip: filler.Clip{
		Hash: "from-src-1.mp4",
		Path: "from-src-1.mp4", Name: "From src 1", Kind: filler.Commercial, DurationMs: 30000,
		Source: "archive", License: src.License,
	}, UpdatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFillerSource(ctx, "src-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetClip(ctx, "from-src-1.mp4"); err != nil {
		t.Errorf("deleting a source removed its clip: %v", err)
	}
	if _, ok := findSource(t, s, "src-1"); ok {
		t.Error("after delete, src-1 is still listed")
	}

	// An unknown id is ErrNotFound on both write paths, so a caller cannot believe it
	// recorded something.
	if err := s.DeleteFillerSource(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete unknown = %v, want ErrNotFound", err)
	}
	if err := s.MarkFillerSourceFetched(ctx, "nope", fetched); !errors.Is(err, ErrNotFound) {
		t.Errorf("mark unknown fetched = %v, want ErrNotFound", err)
	}
	if err := s.SetFillerSourceEnabled(ctx, "nope", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("set enabled on unknown = %v, want ErrNotFound", err)
	}
	if err := s.SetFillerSourceAutoAdmit(ctx, "nope", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("set auto-admit on unknown = %v, want ErrNotFound", err)
	}
}

// testSeededDefaultSources covers what migration 00034 puts in a FRESH store, on BOTH backends
// (§10 V38c.8).
//
// ⚠ **This is the ONLY test that may depend on the seeded set.** Every other suite clears it —
// `newFillerServer` in internal/api does so explicitly — because eleven tests phrased as absolute
// counts ("want 1", "unconfigured") went red the moment the migration landed, none of them wrong
// about the behaviour they described. Concentrating the dependency here means the next change to
// the seed breaks exactly one test, and it breaks the one whose job is to notice.
//
// ⚠ It runs on both dialects because the seed is HAND-DUPLICATED per backend — two nearly
// identical SQL files, differing in `unixepoch()` vs its Postgres spelling. That is precisely the
// shape that drifts: a row added to one file and forgotten in the other produces a Postgres
// install that silently ships fewer sources than a SQLite one, and no single-dialect test can see
// it.
func testSeededDefaultSources(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)

	all, err := s.ListFillerSources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]FillerSource{}
	for _, f := range all {
		byID[f.ID] = f
	}

	// The three VERIFIED archive collections (checked against the live API 2026-08-03 — five
	// plausible-looking identifiers returned zero items, which is why this list is short).
	for _, want := range []struct{ id, label string }{
		{"archive:classic_tv_commercials", "Classic TV Commercials"},
		{"archive:vhscommercials", "Commercials From The Vault"},
		{"archive:tv_ads", "TV Ads"},
	} {
		got, ok := byID[want.id]
		if !ok {
			t.Errorf("%s is missing — a fresh install must have something it can fetch", want.id)
			continue
		}
		if !got.Enabled {
			t.Errorf("%s is disabled; the seeded defaults are on so filler works on day one", want.id)
		}
		if !got.Fetchable() {
			t.Errorf("%s is not fetchable — a scanned-only default would leave the install stuck", want.id)
		}
		// ⚠ A human-readable name, not the identifier. `vhscommercials` is not something an
		// operator recognises in the Sources list.
		if got.Label != want.label {
			t.Errorf("%s label = %q, want %q", want.id, got.Label, want.label)
		}
		// ⚠ EMPTY licence, deliberately. ~92% of archive items declare none and §10 defines empty
		// as UNKNOWN, never "public domain" — a reassuring default here would have Loomarr
		// asserting a legal fact nobody checked.
		if got.License != "" {
			t.Errorf("%s license = %q, want empty (unknown) — absence carries no permission",
				want.id, got.License)
		}
	}

	// ⚠ The YouTube row is present but has NO target, and that is the design rather than an
	// oversight. §10 says Loomarr never recommends YouTube content itself; the operator brings
	// their own playlist. An empty uri also keeps the row out of every pull plan on its own,
	// because Fetchable() requires one.
	yt, ok := byID["youtube"]
	if !ok {
		t.Fatal("the youtube row is missing — the mock draws it as a present, empty prompt")
	}
	if yt.URI != "" {
		t.Errorf("youtube uri = %q, want empty — seeding a target IS the recommendation §10 forbids",
			yt.URI)
	}
	if yt.Fetchable() {
		t.Error("the empty youtube row is fetchable; it must not reach the ingest job until someone fills it in")
	}
}

// testFillerPulls covers the filler approval gate (§10 V35) on BOTH backends.
//
// The assertions that matter are the ones protecting the AUDIT: a decided pull is kept, and a
// dropped plan row is retained with its flag rather than removed. "We approved this" is only
// meaningful next to what was proposed.
func testFillerPulls(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	created := time.Now().UTC().Truncate(time.Second)

	p := filler.Pull{
		ID: "pull_1", Title: "Top up the 1990s", Reason: "Saturday Mornings falls back to bumpers.",
		ProposedBy: "admin-1", Status: filler.PullPending, CreatedAt: created,
		Plan: []filler.PullPlanRow{
			{SourceID: "classic", Tag: "1990s", Name: "Classic TV commercials", Why: "Era match", EstimateClips: 40},
			{SourceID: "psa", Tag: "psa", Name: "Public service", Why: "Filler variety", EstimateClips: 12},
		},
	}
	if err := s.UpsertPull(ctx, p); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetPull(ctx, "pull_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Plan) != 2 || got.Plan[0].SourceID != "classic" || got.Plan[0].EstimateClips != 40 {
		t.Errorf("plan did not round-trip: %+v", got.Plan)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}
	// Pending means undecided, and that must be legible as a ZERO time rather than an epoch
	// date nobody meant.
	if !got.DecidedAt.IsZero() {
		t.Errorf("a pending pull has DecidedAt %v, want zero", got.DecidedAt)
	}
	if got.EstimatedClips() != 52 {
		t.Errorf("EstimatedClips = %d, want 52", got.EstimatedClips())
	}

	// Approve with one row dropped.
	decided := created.Add(time.Hour)
	got.Plan[1].Dropped = true
	got.Status = filler.PullApproved
	got.Note = "no local dealers"
	got.DecidedAt = decided
	got.DecidedBy = "admin-2"
	if err := s.UpsertPull(ctx, got); err != nil {
		t.Fatal(err)
	}

	after, err := s.GetPull(ctx, "pull_1")
	if err != nil {
		t.Fatal(err)
	}
	// ⚠ The dropped row is STILL THERE, flagged. Removing it would leave a record of what was
	// fetched with no record of what was declined, which is the half a reviewer needs.
	if len(after.Plan) != 2 {
		t.Fatalf("plan has %d rows after approval, want 2 — a dropped row must be retained", len(after.Plan))
	}
	if !after.Plan[1].Dropped {
		t.Error("the dropped flag did not persist")
	}
	if n := len(after.Committed()); n != 1 {
		t.Errorf("Committed() = %d rows, want 1", n)
	}
	if after.EstimatedClips() != 40 {
		t.Errorf("EstimatedClips after drop = %d, want 40", after.EstimatedClips())
	}
	if !after.DecidedAt.Equal(decided) || after.DecidedBy != "admin-2" || after.Note != "no local dealers" {
		t.Errorf("decision not recorded: %+v", after)
	}

	// Status filtering, and the fact that a decided pull is KEPT rather than deleted.
	if pending, err := s.ListPulls(ctx, filler.PullPending); err != nil || len(pending) != 0 {
		t.Errorf("pending = %d (%v), want 0", len(pending), err)
	}
	approved, err := s.ListPulls(ctx, filler.PullApproved)
	if err != nil || len(approved) != 1 {
		t.Fatalf("approved = %d (%v), want 1 — the history must survive the decision", len(approved), err)
	}
	if all, err := s.ListPulls(ctx, ""); err != nil || len(all) != 1 {
		t.Errorf("all = %d (%v), want 1", len(all), err)
	}

	if _, err := s.GetPull(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPull unknown = %v, want ErrNotFound", err)
	}
}

// testSplitProposals covers the persisted split proposal (§10, V34) on BOTH backends: the
// segments JSON round-trip, ONE proposal per clip (re-detection replaces, and the new id
// wins), delete, and DeleteClip (the confirm path's drop of the compilation row).
func testSplitProposals(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	p := filler.SplitProposal{
		ID: "sp_1", ClipHash: clipHashFor("comps/1987.mp4"), CreatedAt: now,
		Segments: []filler.SplitSegment{
			{Index: 0, StartMs: 0, EndMs: 30000, Name: "comps/1987 part 1", Era: 1987, Audience: filler.Kids, Category: "toys"},
			{Index: 1, StartMs: 30000, EndMs: 61000, Name: "unknown", SuggestedEra: 1985, DupOf: "old/ad.mp4", Looked: true},
			{Index: 2, StartMs: 61000, EndMs: 149000, Name: "comps/1987 part 3", Unsplittable: true, Transcript: "[00:00] …"},
		},
	}
	if err := s.UpsertSplitProposal(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSplitProposal(ctx, "sp_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ClipHash != p.ClipHash || len(got.Segments) != 3 || !got.CreatedAt.Equal(now) {
		t.Fatalf("proposal round-trip = %+v", got)
	}
	// Every segment field survives the JSON round-trip — including the V34-specific
	// suggestion, dedup flag, and unsplittable marker the review renders.
	s1 := got.Segments[1]
	if s1.SuggestedEra != 1985 || s1.DupOf != "old/ad.mp4" || s1.Era != 0 || !s1.Looked {
		t.Errorf("segment suggestion/dedup fields lost: %+v", s1)
	}
	if !got.Segments[2].Unsplittable || got.Segments[2].Transcript == "" {
		t.Errorf("unsplittable marker/transcript lost: %+v", got.Segments[2])
	}

	draft := filler.SplitProposal{
		ID: "sp_draft", ClipHash: clipHashFor("comps/long.mp4"), CreatedAt: now.Add(time.Minute),
		Detection: &filler.SplitDetectionProgress{
			ScannedThroughMs: 600_000,
			Black:            []filler.Interval{{StartMs: 29_900, EndMs: 30_100}},
		},
	}
	if err := s.UpsertSplitProposal(ctx, draft); err != nil {
		t.Fatal(err)
	}
	gotDraft, err := s.GetSplitProposal(ctx, draft.ID)
	if err != nil || gotDraft.Ready() || gotDraft.Detection.ScannedThroughMs != 600_000 || len(gotDraft.Detection.Black) != 1 {
		t.Fatalf("detector checkpoint round-trip = (%+v, %v)", gotDraft, err)
	}
	if err := s.DeleteSplitProposal(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}

	// ⚠ Re-detection REPLACES the pending proposal for the same clip — two competing
	// cut-lists for one file is a review bug, not a choice. The NEW id answers the old
	// one's GET with ErrNotFound.
	p2 := filler.SplitProposal{ID: "sp_2", ClipHash: p.ClipHash, CreatedAt: now.Add(time.Hour),
		Segments: []filler.SplitSegment{{Index: 0, StartMs: 0, EndMs: 149000, Name: "whole"}}}
	if err := s.UpsertSplitProposal(ctx, p2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSplitProposal(ctx, "sp_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale proposal after re-detection = %v, want ErrNotFound", err)
	}
	got2, err := s.GetSplitProposal(ctx, "sp_2")
	if err != nil || len(got2.Segments) != 1 {
		t.Fatalf("replacement proposal = (%+v, %v)", got2, err)
	}

	// DeleteClip (confirm drops the compilation row) + proposal cleanup.
	if err := s.UpsertClip(ctx, Clip{Clip: clipAt("comps/1987.mp4", "1987", filler.Commercial, 149000), UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteClip(ctx, "comps/1987.mp4"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetClip(ctx, "comps/1987.mp4"); !errors.Is(err, ErrNotFound) {
		t.Errorf("compilation row survived DeleteClip: %v", err)
	}
	if err := s.DeleteClip(ctx, "comps/1987.mp4"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteClip twice = %v, want ErrNotFound", err)
	}
	if err := s.DeleteSplitProposal(ctx, "sp_2"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSplitProposal(ctx, "sp_2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteSplitProposal twice = %v, want ErrNotFound", err)
	}

	// --- UpdateSplitProposal: grounding and partial output accumulate across passes (§10 V54) ---
	//
	// Split-time grounding is a read-modify-write spanning MINUTES of vision calls, so the write
	// races `Confirm`. Two properties are pinned here, and the second is the load-bearing one.
	p3 := filler.SplitProposal{
		ID: "sp_3", ClipHash: "h:comps/1991.mp4", CreatedAt: now,
		Segments: []filler.SplitSegment{
			{Index: 0, StartMs: 0, EndMs: 30_000, Name: "one"},
			{Index: 1, StartMs: 30_000, EndMs: 61_000, Name: "two"},
		},
	}
	if err := s.UpsertSplitProposal(ctx, p3); err != nil {
		t.Fatal(err)
	}

	// 1. The grounding fields round-trip — including `Looked`, which is what distinguishes
	// "looked at and found nothing" from "not reached yet". Inferring it from Category/Era would
	// make a resumable budget retry the ungroundable segments forever.
	grounded := []filler.SplitSegment{
		{Index: 0, StartMs: 0, EndMs: 30_000, Name: "one", Looked: true, Category: "toys", Era: 1991},
		{Index: 1, StartMs: 30_000, EndMs: 61_000, Name: "two", Looked: true},
	}
	p3.Segments = grounded
	p3.Spawned = []string{"new-child"}
	if err := s.UpdateSplitProposal(ctx, p3); err != nil {
		t.Fatal(err)
	}
	got3, err := s.GetSplitProposal(ctx, "sp_3")
	if err != nil {
		t.Fatal(err)
	}
	if len(got3.Segments) != 2 {
		t.Fatalf("segments after update = %+v, want 2", got3.Segments)
	}
	if !got3.Segments[0].Looked || got3.Segments[0].Category != "toys" || got3.Segments[0].Era != 1991 {
		t.Errorf("grounding lost in round-trip: %+v", got3.Segments[0])
	}
	if !got3.Segments[1].Looked || got3.Segments[1].Category != "" {
		t.Errorf("segment 1 = %+v, want Looked with NO category — 'looked and found nothing'", got3.Segments[1])
	}
	if len(got3.Spawned) != 1 || got3.Spawned[0] != "new-child" {
		t.Errorf("partial-confirm output lost in round-trip: %+v", got3.Spawned)
	}
	// ⚠ `created_at` must be untouched: ListSplitProposals orders by it, so writing it would let a
	// reel jump the review queue merely for having been grounded.
	if !got3.CreatedAt.Equal(p3.CreatedAt) {
		t.Errorf("created_at moved on a grounding write: %v, want %v", got3.CreatedAt, p3.CreatedAt)
	}

	// 2. ⚠ **THE safety property: the update must NEVER insert.** If it were an upsert, a grounding
	// write landing after `Confirm` consumed the proposal would RESURRECT it — a pending review for
	// a reel already cut, pointing at a composite whose segments are in the catalog.
	if err := s.DeleteSplitProposal(ctx, "sp_3"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSplitProposal(ctx, p3); !errors.Is(err, ErrNotFound) {
		t.Errorf("update after delete = %v, want ErrNotFound", err)
	}
	if _, err := s.GetSplitProposal(ctx, "sp_3"); !errors.Is(err, ErrNotFound) {
		t.Error("a grounding write RESURRECTED a confirmed proposal — the reel would be reviewed twice")
	}
	p3.ID = "sp_never_existed"
	if err := s.UpdateSplitProposal(ctx, p3); !errors.Is(err, ErrNotFound) {
		t.Errorf("update of an unknown id = %v, want ErrNotFound", err)
	}

	// --- The other side of the no-foreign-key independence: the PRUNE takes proposals too ---
	//
	// ⚠ `filler_split_proposals` is a sibling of `clips` with no FK, so nothing cleaned it up.
	// Measured 2026-08-11: deleting every clip file and running filler-sync pruned `clips` to 0
	// and left **48** proposals behind, which Incoming rendered as 48 "compilations to review"
	// titled with raw content hashes, each opening a review of a file that was gone.
	keeper := clipAt("comps/keeper.mp4", "Keeper", filler.Commercial, 149_000)
	orphan := clipAt("comps/orphan.mp4", "Orphan", filler.Commercial, 149_000)
	for _, c := range []filler.Clip{keeper, orphan} {
		if err := s.UpsertClip(ctx, Clip{Clip: c, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	seg := []filler.SplitSegment{{Index: 0, StartMs: 0, EndMs: 30_000}}
	for id, hash := range map[string]string{"sp_keep": keeper.Hash, "sp_orphan": orphan.Hash} {
		if err := s.UpsertSplitProposal(ctx, filler.SplitProposal{
			ID: id, ClipHash: hash, CreatedAt: now, Segments: seg,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// ⚠ A KEEPER is enrolled first on purpose, so the assertion distinguishes "pruned the orphan"
	// from "emptied the table" — a prune with a broken predicate passes the orphan check alone.
	if _, err := s.DeleteClipsNotIn(ctx, []string{keeper.Hash}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSplitProposal(ctx, "sp_orphan"); !errors.Is(err, ErrNotFound) {
		t.Errorf("orphan proposal survived the prune = %v; Incoming would render it as a "+
			"hash-titled reel pointing at a deleted compilation", err)
	}
	if _, err := s.GetSplitProposal(ctx, "sp_keep"); err != nil {
		t.Errorf("the prune took a LIVE proposal with it: %v", err)
	}
	// ⚠ Asserted on the LIST too, because that is the surface the defect was seen on.
	remaining, err := s.ListSplitProposals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != "sp_keep" {
		t.Errorf("ListSplitProposals = %+v, want only sp_keep", remaining)
	}

	// --- the sweep's tombstone: a REAPED composite survives the prune (§10 V54) ---
	//
	// ⚠ **The cascade this prevents.** The split sweep deletes a spent recording on purpose, so the
	// next scan legitimately does not see it. Without the exemption `DeleteClipsNotIn` removes the
	// row — and every clip cut out of that reel carries `parent_hash` pointing at it, so one sweep
	// would dangle all of its children and take V45's lineage with it.
	reaped := clipAt("comps/reaped.mp4", "Reaped", filler.Commercial, 149_000)
	child := clipAt("cuts/child.mp4", "Child", filler.Commercial, 30_000)
	child.ParentHash = reaped.Hash
	for _, c := range []filler.Clip{reaped, child} {
		if err := s.UpsertClip(ctx, Clip{Clip: c, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkClipReaped(ctx, reaped.Hash, now); err != nil {
		t.Fatal(err)
	}

	// The scan now reports only the child — the reel's bytes are gone, which is the point.
	if _, err := s.DeleteClipsNotIn(ctx, []string{child.Hash}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetClip(ctx, reaped.Hash); err != nil {
		t.Errorf("a reaped composite was pruned (%v) — every clip cut from it now has a dangling "+
			"parent_hash", err)
	}

	// ⚠ …and the EMPTY-scan branch takes the same exemption. An unreadable drop folder is exactly
	// when a swept reel looks most like a deleted one.
	if _, err := s.DeleteClipsNotIn(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetClip(ctx, reaped.Hash); err != nil {
		t.Errorf("an empty scan pruned the reaped composite: %v", err)
	}
}

// testClipPipeline covers the per-clip ingest pipeline's state (§10 V51b) on BOTH backends: the
// ladder round-trip, the work-list's due/terminal filtering and total order, lazy enrolment, and —
// the property the whole table exists for — that a folder re-scan cannot touch it.
func testClipPipeline(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	clip := clipAt("a/b/one.mp4", "One", filler.Commercial, 30_000)
	if err := s.UpsertClip(ctx, Clip{Clip: clip, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	// --- Lazy enrolment: a catalogued clip with no row is work waiting to be picked up. ---
	missing, err := s.ListClipsWithoutPipeline(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].Hash != clip.Hash {
		t.Fatalf("ListClipsWithoutPipeline = %+v, want just %s", missing, clip.Hash)
	}

	p := filler.ClipPipeline{
		ClipHash: clip.Hash, Stage: filler.StageTag, Status: filler.StatusRunning,
		Progress: 40, Disposition: filler.DispositionRunning,
		Attempts: 1, ForceRun: true, NextRun: now, EnrolledAt: now, UpdatedAt: now,
		Stages: []filler.StageRecord{
			{Stage: filler.StageProbe, Status: filler.StatusDone, At: now},
			{Stage: filler.StageTranscribe, Status: filler.StatusSkipped, Note: "the description already says enough", At: now},
		},
	}
	if err := s.UpsertClipPipeline(ctx, p); err != nil {
		t.Fatal(err)
	}

	// Enrolled clips drop out of the enrolment list — otherwise every pass would re-enrol
	// everything and reset the catalog to the start of the pipeline.
	if again, aerr := s.ListClipsWithoutPipeline(ctx, 10); aerr != nil || len(again) != 0 {
		t.Fatalf("an enrolled clip is still listed as missing: %+v (%v)", again, aerr)
	}

	got, found, err := s.GetClipPipeline(ctx, clip.Hash)
	if err != nil || !found {
		t.Fatalf("GetClipPipeline = (%+v, %v, %v)", got, found, err)
	}
	if got.Stage != filler.StageTag || got.Status != filler.StatusRunning || got.Progress != 40 || got.Attempts != 1 {
		t.Errorf("header round-trip lost fields: %+v", got)
	}
	if !got.ForceRun {
		t.Error("pipeline round-trip lost the explicit rerun marker")
	}
	// The LADDER is what the Incoming tab renders as history — including WHY a stage was skipped.
	// A skip with no note reads as "nothing happened", which is a different and false claim.
	if len(got.Stages) != 2 || got.Stages[1].Status != filler.StatusSkipped ||
		got.Stages[1].Note != "the description already says enough" {
		t.Errorf("ladder round-trip = %+v", got.Stages)
	}

	// --- An absent row is ordinary, not an error. An un-enrolled clip is the common case. ---
	if _, ok, gerr := s.GetClipPipeline(ctx, "no-such-clip"); gerr != nil || ok {
		t.Errorf("GetClipPipeline(missing) = (%v, %v), want (false, nil)", ok, gerr)
	}

	// --- The work list: due, non-terminal, oldest first. ---
	work, err := s.ListPipelineWork(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(work) != 1 || work[0].ClipHash != clip.Hash {
		t.Fatalf("work list = %+v, want the running clip", work)
	}
	// ⚠ A row backing off is NOT due. Without this the retry schedule is decorative: a failing
	// stage would be re-run on the very next pass, which is the "retried at full cost forever"
	// behaviour the backoff was added to stop.
	future := p
	future.NextRun = now.Add(time.Hour)
	if err := s.UpsertClipPipeline(ctx, future); err != nil {
		t.Fatal(err)
	}
	if backed, berr := s.ListPipelineWork(ctx, now, 10); berr != nil || len(backed) != 0 {
		t.Errorf("a backing-off row was returned as due: %+v (%v)", backed, berr)
	}

	// ⚠ A TERMINAL row is never work again, whatever its schedule says. `review` counts as
	// terminal here: the pipeline has done all it can and is waiting on a person, so re-running
	// the ladder would burn Whisper and vision calls on a clip whose only missing input is a
	// human decision.
	for _, d := range []filler.Disposition{filler.DispositionReview, filler.DispositionFiled, filler.DispositionRejected} {
		term := p
		term.Disposition = d
		term.NextRun = now.Add(-time.Hour) // overdue on purpose
		if err := s.UpsertClipPipeline(ctx, term); err != nil {
			t.Fatal(err)
		}
		if w, werr := s.ListPipelineWork(ctx, now, 10); werr != nil || len(w) != 0 {
			t.Errorf("disposition %q was returned as work: %+v (%v)", d, w, werr)
		}
	}

	// --- The rejected read model carries the CODE and the measured detail. ---
	rej := p
	rej.Disposition = filler.DispositionRejected
	rej.RejectReason = filler.ReasonTooShort
	rej.RejectDetail = "8.2s; floor is 10s"
	if err := s.UpsertClipPipeline(ctx, rej); err != nil {
		t.Fatal(err)
	}
	rejected, err := s.ListClipPipelines(ctx, filler.PipelineFilter{RejectedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 || rejected[0].RejectReason != filler.ReasonTooShort ||
		rejected[0].RejectDetail != "8.2s; floor is 10s" {
		t.Fatalf("rejected read model = %+v — the code AND the measured fact must both survive", rejected)
	}

	// --- ⚠ THE property this table exists for. ---
	//
	// `clips` is a synced CACHE: the folder scan re-upserts every file it finds. Pipeline state
	// records that ~341s of Whisper and a paid vision call have ALREADY been spent. If a re-scan
	// could touch these rows, one sync would re-run the whole catalog through the whole pipeline
	// and re-spend the money — which is precisely the class of failure `UpsertClip`'s DO UPDATE
	// omission list defends against by hand, one table over. Here it is structural: the scan does
	// not know this table exists.
	rescan := Clip{Clip: clip, UpdatedAt: now.Add(time.Minute)}
	if err := s.UpsertClip(ctx, rescan); err != nil {
		t.Fatal(err)
	}
	after, found, err := s.GetClipPipeline(ctx, clip.Hash)
	if err != nil || !found {
		t.Fatalf("a re-scan DELETED the pipeline row (%v, %v)", found, err)
	}
	if after.Stage != rej.Stage || after.Disposition != rej.Disposition ||
		after.RejectReason != rej.RejectReason || len(after.Stages) != len(rej.Stages) {
		t.Errorf("a re-scan altered pipeline state: %+v, want %+v", after, rej)
	}

	// --- ⚠ The other side of that independence: the PRUNE must take the rows with it. ---
	//
	// The sibling table has no foreign key, deliberately, so it survives a `clips` rebuild. The
	// price is that nothing else will ever clean it up — and an orphan row is not inert. It stays
	// in the work list, `advance` cannot find its clip, and it is re-tombstoned as "no longer in
	// the catalog" on every pass, forever. `DeleteClipsNotIn` is the one place clips disappear in
	// bulk, so it is the one place that has to prune them.
	//
	// A second clip is enrolled first, so the assertion distinguishes "pruned the orphan" from
	// "emptied the table".
	keeper := clipAt("c/d/two.mp4", "Two", filler.Commercial, 30_000)
	if err := s.UpsertClip(ctx, Clip{Clip: keeper, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	kept := filler.ClipPipeline{
		ClipHash: keeper.Hash, Stage: filler.StageProbe, Status: filler.StatusQueued,
		Disposition: filler.DispositionRunning, EnrolledAt: now, UpdatedAt: now,
	}
	if err := s.UpsertClipPipeline(ctx, kept); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteClipsNotIn(ctx, []string{keeper.Hash}); err != nil {
		t.Fatal(err)
	}
	if _, orphan, oerr := s.GetClipPipeline(ctx, clip.Hash); oerr != nil || orphan {
		t.Errorf("the pruned clip's pipeline row survived (found=%v, %v) — it would be worked forever", orphan, oerr)
	}
	if _, stillThere, kerr := s.GetClipPipeline(ctx, keeper.Hash); kerr != nil || !stillThere {
		t.Errorf("the prune took a LIVE clip's pipeline row with it (found=%v, %v)", stillThere, kerr)
	}
}

// clipHashes is the identity projection the paging property compares on. ⚠ Hashes, not paths:
// `ids2` returns paths and the tie-break sorts on `hash`, so comparing paths would be comparing a
// column the ORDER BY never mentions.
func clipHashes(clips []Clip) []string {
	out := make([]string, len(clips))
	for i, c := range clips {
		out[i] = c.Hash
	}
	return out
}

func sameHashes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// testClipPaging covers the catalog's paging, sorting and widened search (§10 V51d) on BOTH
// backends.
//
// ⚠ **The centrepiece is the CONCATENATION PROPERTY**: for every sort key, both directions, at
// several page sizes, all pages concatenated must equal the unpaginated list exactly. One
// assertion catches three distinct bugs that are individually easy to miss — a missing tie-break
// (a row appears on two pages and another vanishes), an off-by-one offset, and a per-dialect
// collation difference — and it catches them on whichever backend has them.
//
// ⚠ **The fixture is load-bearing in two specific ways.** It contains deliberate TIES on every
// tieable column, because a total order is untestable against distinct values; and it contains
// CASE-MIXED names, because SQLite's BINARY collation puts 'Z' before 'a' while Postgres's locale
// collation does not — so a missing `LOWER()` fails on exactly one backend, which is the class
// `make test-pg` exists to catch.
func testClipPaging(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()

	// ⚠ Names deliberately straddle the case boundary. Under SQLite's BINARY collation every
	// capital sorts before every lowercase ('Z' = 90 < 'a' = 97); under Postgres's locale
	// collation they interleave. `LOWER(name)` is what makes the two agree, and these rows are
	// what prove it: an implementation without it orders "Zeppelin Ad" and "apple juice"
	// differently on the two backends while every other assertion here still passes.
	type row struct {
		id       string
		name     string
		duration int64
		plays    int64
		conf     int
		created  int64
	}
	rows := []row{
		{"p1", "apple juice", 30000, 5, 70, 1_700_000_100},
		{"p2", "Zeppelin Ad", 30000, 5, 70, 1_700_000_200}, // ties p1 on duration/plays/confidence
		{"p3", "banana split", 15000, 0, 10, 1_700_000_300},
		{"p4", "Banana bread", 15000, 0, 10, 1_700_000_300}, // ties p3 on EVERYTHING, created included
		{"p5", "cereal, morning", 60000, 12, 95, 1_700_000_400},
		{"p6", "Cereal, evening", 45000, 12, 95, 1_700_000_500},
		{"p7", "zzz last", 5000, 1, 0, 1_700_000_600},
		{"p8", "AAA first", 90000, 99, 100, 1_700_000_700},
		{"p9", "middle of the road", 30000, 5, 50, 1_700_000_800},
	}
	for _, r := range rows {
		c := sampleClip(r.id, r.name, filler.Commercial, 1990, filler.Kids, "toys")
		c.DurationMs = r.duration
		c.PlayCount = r.plays
		c.Confidence = r.conf
		c.CreatedAt = time.Unix(r.created, 0).UTC()
		if err := s.UpsertClip(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	sorts := []ClipSort{"", ClipSortName, ClipSortDuration, ClipSortAdded, ClipSortPlays, ClipSortConfidence}

	t.Run("PagesConcatenateToTheWholeList", func(t *testing.T) {
		for _, sort := range sorts {
			for _, desc := range []bool{false, true} {
				full, err := s.ListClips(ctx, ClipFilter{Sort: sort, Desc: desc})
				if err != nil {
					t.Fatalf("sort %q desc=%v: %v", sort, desc, err)
				}
				want := clipHashes(full)
				for _, size := range []int{1, 2, 3, 4, 7, 100} {
					var got []string
					for offset := 0; ; offset += size {
						page, err := s.ListClips(ctx, ClipFilter{Sort: sort, Desc: desc, Limit: size, Offset: offset})
						if err != nil {
							t.Fatalf("sort %q desc=%v page@%d: %v", sort, desc, offset, err)
						}
						if len(page) == 0 {
							break
						}
						if len(page) > size {
							t.Fatalf("sort %q: page of %d rows exceeds limit %d", sort, len(page), size)
						}
						got = append(got, clipHashes(page)...)
						if offset > len(rows)*2 { // a paranoid stop, so a broken LIMIT cannot loop forever
							t.Fatalf("sort %q: paging did not terminate", sort)
						}
					}
					if !sameHashes(got, want) {
						t.Errorf("sort %q desc=%v size %d: pages concatenate to\n  %v\nbut the unpaginated list is\n  %v\n"+
							"— a duplicated or dropped row means the ORDER BY is not a TOTAL order",
							sort, desc, size, got, want)
					}
				}
			}
		}
	})

	t.Run("DescendingIsTheExactReverse", func(t *testing.T) {
		for _, sort := range sorts {
			asc, err := s.ListClips(ctx, ClipFilter{Sort: sort})
			if err != nil {
				t.Fatal(err)
			}
			desc, err := s.ListClips(ctx, ClipFilter{Sort: sort, Desc: true})
			if err != nil {
				t.Fatal(err)
			}
			a, d := clipHashes(asc), clipHashes(desc)
			for i := range a {
				if a[i] != d[len(d)-1-i] {
					t.Errorf("sort %q: descending is not the reverse of ascending (%v vs %v) — the tie-break "+
						"does not flip with the sort", sort, a, d)
					break
				}
			}
		}
	})

	// ⚠ THE dialect assertion. Without `LOWER(name)` this passes on one backend and fails on the
	// other, which is the entire reason the store conformance suite runs twice.
	t.Run("NameSortsCaseInsensitivelyOnBothBackends", func(t *testing.T) {
		got, err := s.ListClips(ctx, ClipFilter{Sort: ClipSortName})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"p8", "p1", "p4", "p3", "p6", "p5", "p9", "p2", "p7"}
		if !sameHashes(clipHashes(got), want) {
			t.Errorf("name order = %v, want %v — 'Zeppelin Ad' after 'middle of the road' and "+
				"'AAA first' before 'apple juice' only hold when the column is LOWER()ed",
				clipHashes(got), want)
		}
	})

	t.Run("UnknownSortIsAnErrorNotAFallback", func(t *testing.T) {
		if _, err := s.ListClips(ctx, ClipFilter{Sort: "; DROP TABLE clips"}); !errors.Is(err, ErrUnknownClipSort) {
			t.Errorf("unknown sort returned %v, want ErrUnknownClipSort — a silent fall-back to the "+
				"default order makes a broken sort control look like a UI glitch", err)
		}
	})

	t.Run("CountIgnoresLimitOffsetAndSort", func(t *testing.T) {
		total, err := s.CountClips(ctx, ClipFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if total != len(rows) {
			t.Fatalf("catalog total = %d, want %d", total, len(rows))
		}
		paged, err := s.CountClips(ctx, ClipFilter{Limit: 2, Offset: 4, Sort: ClipSortPlays, Desc: true})
		if err != nil {
			t.Fatal(err)
		}
		if paged != total {
			t.Errorf("CountClips with a page = %d, want %d — the total is 'how many match', not "+
				"'how many are on this page', or the pager reports 'showing 1-2 of 2'", paged, total)
		}
	})

	t.Run("OffsetPastTheEndIsEmpty", func(t *testing.T) {
		got, err := s.ListClips(ctx, ClipFilter{Sort: ClipSortName, Limit: 5, Offset: 500})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("offset past the end returned %d rows, want none", len(got))
		}
	})

	// ⚠ Offset with no limit must be IGNORED rather than emulated: sqlite rejects a bare OFFSET,
	// so an implementation that renders one errors on one backend only.
	t.Run("OffsetWithoutLimitIsIgnored", func(t *testing.T) {
		got, err := s.ListClips(ctx, ClipFilter{Offset: 3})
		if err != nil {
			t.Fatalf("offset with no limit: %v", err)
		}
		if len(got) != len(rows) {
			t.Errorf("got %d rows, want the whole catalog (%d) — a page with no size is not a page", len(got), len(rows))
		}
	})

	t.Run("HashesReadsExactlyTheAskedForClips", func(t *testing.T) {
		got, err := s.ListClips(ctx, ClipFilter{Hashes: []string{"p3", "p8", "not-a-clip"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d clips, want 2 (the unknown hash is simply absent, never an error)", len(got))
		}
		n, err := s.CountClips(ctx, ClipFilter{Hashes: []string{"p3", "p8", "not-a-clip"}})
		if err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Errorf("CountClips over the same hashes = %d, want 2 — the predicate is shared, so it cannot differ", n)
		}
	})
}

// testClipSearchWidened covers the V51d search across name | brand | visible_text | tags, and the
// opt-in transcript (§10 V51d).
//
// ⚠ Every case asserts CountClips alongside ListClips. They share `clipWhere` precisely so a
// search's total cannot disagree with its rows, and an EXISTS that was written as a JOIN would
// break exactly that — a clip with three matching tags counting three times.
func testClipSearchWidened(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()

	named := sampleClip("s1", "Crunchy Flakes 1994", filler.Commercial, 1994, filler.Kids, "cereal")
	branded := sampleClip("s2", "unnamed spot", filler.Commercial, 1994, filler.Kids, "cereal")
	branded.Brand = "Kellogg's"
	seen := sampleClip("s3", "silent visual", filler.Commercial, 1994, filler.Kids, "cereal")
	seen.VisibleText = "FORD TOUGH"
	spoken := sampleClip("s4", "chatty spot", filler.Commercial, 1994, filler.Kids, "cereal")
	spoken.Transcript = "you can afford a Ford this weekend"
	for _, c := range []Clip{named, branded, seen, spoken} {
		if err := s.UpsertClip(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	// ⚠ A REAL slug from the seeded forest, tagged through the real writer — a hand-inserted
	// clip_tags row would not carry the rollups, and the search must match those too.
	if err := s.SetClipTags(ctx, "s1", []string{"cereal"}); err != nil {
		t.Fatal(err)
	}

	hits := func(t *testing.T, f ClipFilter) []string {
		t.Helper()
		got, err := s.ListClips(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		n, err := s.CountClips(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		if n != len(got) {
			t.Errorf("CountClips = %d but ListClips returned %d for %+v — the shared predicate has drifted", n, len(got), f)
		}
		return clipHashes(got)
	}

	t.Run("MatchesName", func(t *testing.T) {
		if got := hits(t, ClipFilter{Query: "crunchy"}); !sameHashes(got, []string{"s1"}) {
			t.Errorf("name search = %v, want [s1]", got)
		}
	})
	t.Run("MatchesBrand", func(t *testing.T) {
		if got := hits(t, ClipFilter{Query: "kellogg"}); !sameHashes(got, []string{"s2"}) {
			t.Errorf("brand search = %v, want [s2] — a catalog that cannot find a clip by its "+
				"advertiser is a search box that looks broken", got)
		}
	})
	t.Run("MatchesVisibleText", func(t *testing.T) {
		if got := hits(t, ClipFilter{Query: "ford tough"}); !sameHashes(got, []string{"s3"}) {
			t.Errorf("visible-text search = %v, want [s3]", got)
		}
	})
	t.Run("MatchesTags", func(t *testing.T) {
		if got := hits(t, ClipFilter{Query: "cereal"}); !sameHashes(got, []string{"s1"}) {
			t.Errorf("tag search = %v, want [s1] exactly ONCE — a JOIN here would return the clip "+
				"per matching tag and make the total disagree with the rows", got)
		}
	})
	// ⚠ The transcript is opt-in, and this pair is what proves the flag does something. "afford"
	// contains "ford", which is also the noise argument for keeping it opt-in.
	t.Run("TranscriptOnlyWhenAskedFor", func(t *testing.T) {
		if got := hits(t, ClipFilter{Query: "weekend"}); len(got) != 0 {
			t.Errorf("default search matched the transcript (%v) — it is kilobytes per clip and the "+
				"noisiest column; it must be opt-in", got)
		}
		if got := hits(t, ClipFilter{Query: "weekend", QueryTranscript: true}); !sameHashes(got, []string{"s4"}) {
			t.Errorf("transcript search = %v, want [s4]", got)
		}
	})
}

// testClipTopLevelOnly pins the composite-as-container listing (§10 V51d).
//
// ⚠ The load-bearing half is the SECOND assertion: the ZERO filter must still return segments.
// TopLevelOnly is opt-in because pod assembly reads through that zero filter, and segments are
// the airable clips — an opt-out would delete every split-out advert from every channel's breaks.
func testClipTopLevelOnly(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()

	brk := sampleClip("brk", "KCPQ break 1996", filler.Commercial, 1996, filler.General, "")
	brk.IsComposite = true
	standalone := sampleClip("solo", "a single advert", filler.Commercial, 1996, filler.General, "toys")
	segA := sampleClip("seg-a", "advert 1", filler.Commercial, 1996, filler.General, "toys")
	segA.ParentHash = "brk"
	segB := sampleClip("seg-b", "advert 2", filler.Commercial, 1996, filler.General, "toys")
	segB.ParentHash = "brk"
	for _, c := range []Clip{brk, standalone, segA, segB} {
		if err := s.UpsertClip(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	zero, err := s.ListClips(ctx, ClipFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := clipHashes(zero); !sameHashes(got, []string{"seg-a", "seg-b", "solo"}) {
		t.Errorf("zero filter = %v, want the two SEGMENTS and the standalone clip (the composite is "+
			"excluded, its segments are what air)", got)
	}

	top, err := s.ListClips(ctx, ClipFilter{TopLevelOnly: true, IncludeComposites: true, Sort: ClipSortName})
	if err != nil {
		t.Fatal(err)
	}
	if got := clipHashes(top); !sameHashes(got, []string{"solo", "brk"}) {
		t.Errorf("top-level listing = %v, want [solo brk] — a break paginates as ONE container row", got)
	}

	// Expanding a break loads its segments, and TopLevelOnly must not silently empty that.
	seg, err := s.ListClips(ctx, ClipFilter{ParentHash: "brk", TopLevelOnly: true, Sort: ClipSortName})
	if err != nil {
		t.Fatal(err)
	}
	if got := clipHashes(seg); !sameHashes(got, []string{"seg-a", "seg-b"}) {
		t.Errorf("lineage read = %v, want both segments — ParentHash wins over TopLevelOnly, or "+
			"expanding a break shows nothing on a break with twenty adverts in it", got)
	}
}

// testClipCreatedAt pins the "recently added" column's single-writer rule (§10 V51d, 00045).
//
// ⚠ The whole point of the column is that a RE-SYNC must not move it. If `created_at` rode
// UpsertClip's DO UPDATE list, every clip would read as "just added" after each folder scan and
// the sort would be worthless — a failure with no error and no log line.
func testClipCreatedAt(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()

	arrived := time.Unix(1_700_000_000, 0).UTC()
	c := sampleClip("ca1", "arrived once", filler.Commercial, 1994, filler.Kids, "toys")
	c.CreatedAt = arrived
	c.UpdatedAt = arrived
	if err := s.UpsertClip(ctx, c); err != nil {
		t.Fatal(err)
	}

	// A re-sync: same clip, a later UpdatedAt, and — as the folder scan does — no CreatedAt at all.
	rescanned := sampleClip("ca1", "arrived once", filler.Commercial, 1994, filler.Kids, "toys")
	rescanned.UpdatedAt = arrived.Add(48 * time.Hour)
	if err := s.UpsertClip(ctx, rescanned); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetClip(ctx, "ca1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.CreatedAt.Equal(arrived) {
		t.Errorf("created_at = %v after a re-sync, want %v — the scan supplies a fresh timestamp "+
			"every pass, so this column must be absent from the DO UPDATE list", got.CreatedAt, arrived)
	}
	if !got.UpdatedAt.Equal(arrived.Add(48 * time.Hour)) {
		t.Errorf("updated_at = %v, want the re-sync's timestamp — it is the column that DOES ride", got.UpdatedAt)
	}

	// A writer that never heard of the column (every pre-V51d call site) still gets an honest
	// value rather than a 0 that sorts to the far end of "recently added" forever.
	fresh := sampleClip("ca2", "no created_at supplied", filler.Commercial, 1994, filler.Kids, "toys")
	fresh.UpdatedAt = arrived
	if err := s.UpsertClip(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	got2, err := s.GetClip(ctx, "ca2")
	if err != nil {
		t.Fatal(err)
	}
	if !got2.CreatedAt.Equal(arrived) {
		t.Errorf("created_at = %v with none supplied, want the UpdatedAt fallback (%v)", got2.CreatedAt, arrived)
	}
}

// testIncomingConveyorCount keeps the bounded Incoming total on the same readiness rule as the
// list it describes. A split detection checkpoint is private pipeline state, so its composite
// remains on the conveyor; only a completed proposal claims that composite into the reels list.
// This runs unchanged against SQLite and Postgres because a backend-specific JSON predicate here
// would be an easy source of a count that disagrees on one install type.
func testIncomingConveyorCount(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	counters, ok := s.(interface {
		CountIncomingConveyor(context.Context) (int, error)
	})
	if !ok {
		t.Fatal("store does not expose the Incoming conveyor counter")
	}

	for _, c := range []Clip{
		{Clip: filler.Clip{Hash: "draft-reel", Path: "reels/draft.mp4", Name: "Draft reel",
			Kind: filler.Commercial, DurationMs: 1_180_000, IsComposite: true}},
		{Clip: filler.Clip{Hash: "ready-reel", Path: "reels/ready.mp4", Name: "Ready reel",
			Kind: filler.Commercial, DurationMs: 1_180_000, IsComposite: true}},
	} {
		if err := s.UpsertClip(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertClipPipeline(ctx, filler.ClipPipeline{
			ClipHash: c.Hash, Stage: filler.StageSplit, Status: filler.StatusQueued,
			Disposition: filler.DispositionRunning, UpdatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertSplitProposal(ctx, filler.SplitProposal{
		ID: "sp-draft", ClipHash: "draft-reel", CreatedAt: time.Now().UTC(),
		Detection: &filler.SplitDetectionProgress{ScannedThroughMs: 600_000},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSplitProposal(ctx, filler.SplitProposal{
		ID: "sp-ready", ClipHash: "ready-reel", CreatedAt: time.Now().UTC(),
		Segments: []filler.SplitSegment{{Index: 0, StartMs: 0, EndMs: 30_000}},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := counters.CountIncomingConveyor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("Incoming conveyor count = %d, want 1 draft reel; the ready reel has its own row", got)
	}
}

func testFillerInferenceAccountingAndBudgets(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	at := time.Date(2026, 8, 24, 15, 0, 0, 0, time.UTC)
	versions := InferenceVersions{
		Evidence: "e1", Extractor: "x1", Prompt: "p1", Schema: "s1", Taxonomy: "t1",
		AdmissionPolicy: "a1", RolePolicy: "r1", CapabilitySnapshot: "c1",
	}
	reserve := func(id, clip string, nano int64) InferenceEvaluation {
		e, err := s.ReserveInferenceEvaluation(ctx, InferenceEvaluation{
			ID: id, ClipHash: clip, RunID: "cert-1", Role: "filler_frames", Rung: "frames",
			RequestedProvider: "openrouter", RequestedModel: "openai/gpt-5-mini",
			Modalities: []string{"text", "image"}, DerivativeBytes: 4096, DerivativePixels: 2_073_600,
			ReservedNanoUSD: nano, Versions: versions, CreatedAt: at,
		}, InferenceBudget{PerClipNanoUSD: 100, PerDayNanoUSD: 1000, PerRunNanoUSD: 1000})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}

	first := reserve("eval-1", "clip-a", 60)
	if first.State != InferenceReserved {
		t.Fatalf("first reservation = %+v", first)
	}
	denied := reserve("eval-2", "clip-a", 50)
	if denied.State != InferenceHeldBudget || denied.ReservedNanoUSD != 0 || denied.FailureReason == "" {
		t.Fatalf("over-budget reservation = %+v", denied)
	}

	settled, err := s.SettleInferenceEvaluation(ctx, first.ID, InferenceSettlement{
		State: InferenceCompleted, ResolvedProvider: "OpenAI", ResolvedModel: "openai/gpt-5-mini-2026-08-07",
		Tokens:        InferenceTokens{Prompt: 194, Completion: 12, Reasoning: 5, Cached: 31, Image: 80},
		ChargedAmount: "0.000000040", ChargedCurrency: "USD", ChargedNanoUSD: 40,
		EstimatedNanoUSD: 42, PriceSnapshot: "prices-1", LatencyMS: 812, Attempts: 1,
		GenerationID: "gen-1", Outcome: "admit", UpdatedAt: at.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if settled.State != InferenceCompleted || settled.ReservedNanoUSD != 40 || settled.ChargedAmount != "0.000000040" || settled.Tokens.Image != 80 {
		t.Fatalf("settled evaluation = %+v", settled)
	}

	third := reserve("eval-3", "clip-a", 50)
	if third.State != InferenceReserved {
		t.Fatalf("unused reservation was not released: %+v", third)
	}
	over, err := s.SettleInferenceEvaluation(ctx, third.ID, InferenceSettlement{
		State: InferenceCompleted, ChargedAmount: "0.000000055", ChargedCurrency: "USD",
		ChargedNanoUSD: 55, UpdatedAt: at.Add(2 * time.Minute),
	})
	if !errors.Is(err, ErrInferenceBudgetExceeded) || over.State != InferenceHeldBudget || over.ChargedNanoUSD != 55 {
		t.Fatalf("provider overspend = %+v, %v", over, err)
	}
	if _, err := s.SettleInferenceEvaluation(ctx, third.ID, InferenceSettlement{UpdatedAt: at.Add(3 * time.Minute)}); !errors.Is(err, ErrInferenceNotReserved) {
		t.Fatalf("second settlement = %v", err)
	}

	dayDenied := reserve("eval-4", "clip-b", 906)
	if dayDenied.State != InferenceHeldBudget {
		t.Fatalf("daily/run ceiling did not hold request: %+v", dayDenied)
	}
	rows, err := s.ListInferenceEvaluations(ctx, InferenceEvaluationFilter{RunID: "cert-1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0].ID != "eval-4" {
		t.Fatalf("evaluation history = %+v", rows)
	}
	got, err := s.GetInferenceEvaluation(ctx, "eval-1")
	if err != nil || got.GenerationID != "gen-1" || got.Versions.RolePolicy != "r1" || len(got.Modalities) != 2 || got.CacheKey == "" {
		t.Fatalf("inspect evaluation = %+v, %v", got, err)
	}
}

func testFillerAdmissionDecisionAudit(t *testing.T, newStore NewStoreFunc) {
	t.Helper()
	s := newStore(t)
	ctx := context.Background()
	at := time.Date(2026, 8, 25, 5, 0, 0, 0, time.UTC)
	record := func(id string, result filleradmission.Result) fillerdecision.Record {
		return fillerdecision.Record{
			ID: id, ClipHash: "clip-" + id, EvidenceHash: "evidence-" + id,
			EvidenceVersion: "e1", SchemaVersion: filleradmission.SchemaVersion,
			PolicyVersion: "policy-1", TaxonomyVersion: "taxonomy-1",
			ApplicationMode: fillerdecision.ApplicationModeShadow, Result: result, CreatedAt: at,
		}
	}
	semantic := func(verdict filleradmission.Verdict) filleradmission.Result {
		decision := &filleradmission.Decision{
			Verdict: verdict, ReasonCodes: []filleradmission.ReasonCode{filleradmission.ReasonEvidenceSatisfied},
			EvidenceRefs: []string{"evidence-a"},
		}
		if verdict == filleradmission.VerdictReview {
			decision.ReviewQuestion = "What product is this clip advertising?"
		}
		return filleradmission.Result{Decision: decision}
	}

	for _, row := range []fillerdecision.Record{
		record("d-admit", semantic(filleradmission.VerdictAdmit)),
		record("d-reject", semantic(filleradmission.VerdictReject)),
		record("d-review", semantic(filleradmission.VerdictReview)),
		record("d-hold", filleradmission.Result{Hold: &filleradmission.Hold{
			Code: filleradmission.HoldProviderUnavailable, Detail: "provider response is deliberately not projected", Retryable: true,
		}}),
	} {
		if err := s.PutFillerDecision(ctx, row); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.GetFillerDecision(ctx, "d-review")
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(record("d-review", semantic(filleradmission.VerdictReview)).Result)
	gotJSON, _ := json.Marshal(got.Result)
	if string(gotJSON) != string(wantJSON) || got.CreatedAt != at || got.ApplicationMode != fillerdecision.ApplicationModeShadow {
		t.Fatalf("decision round trip = %+v (%s), want canonical %s", got, gotJSON, wantJSON)
	}
	second := openSecondConformanceStore(t, s)
	fromSecond, err := second.GetFillerDecision(ctx, "d-review")
	if err != nil || fromSecond.Result.Decision == nil || fromSecond.Result.Decision.ReviewQuestion == "" ||
		fromSecond.ApplicationMode != fillerdecision.ApplicationModeShadow {
		_ = second.Close()
		t.Fatalf("decision did not survive a fresh store pool: %+v, %v", fromSecond, err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.PutFillerDecision(ctx, record("d-review", semantic(filleradmission.VerdictReview))); err != nil {
		t.Fatalf("idempotent insert: %v", err)
	}
	unknownMode := record("d-unknown-mode", semantic(filleradmission.VerdictAdmit))
	unknownMode.ApplicationMode = "automatic"
	if err := s.PutFillerDecision(ctx, unknownMode); !errors.Is(err, fillerdecision.ErrInvalid) {
		t.Fatalf("unknown application mode = %v, want ErrInvalid", err)
	}
	conflict := record("d-review", semantic(filleradmission.VerdictReject))
	if err := s.PutFillerDecision(ctx, conflict); !errors.Is(err, fillerdecision.ErrConflict) {
		t.Fatalf("conflicting immutable insert = %v", err)
	}
	missingReference := record("d-missing-inference", semantic(filleradmission.VerdictAdmit))
	missingReference.Result.Decision.Attribution = []filleradmission.Attribution{{EvaluationID: "eval-missing"}}
	if err := s.PutFillerDecision(ctx, missingReference); err == nil {
		t.Fatal("decision with a missing inference evaluation was persisted")
	}
	if _, err := s.GetFillerDecision(ctx, missingReference.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed decision transaction left a row behind: %v", err)
	}

	page, err := s.ListFillerDecisions(ctx, fillerdecision.DecisionFilter{
		Kind: fillerdecision.OutcomeSemantic, Cursor: fillerdecision.Cursor{}, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Rows) != 2 || page.Rows[0].ID != "d-review" || page.Rows[1].ID != "d-reject" {
		t.Fatalf("first keyset page = %+v", page)
	}
	next, err := s.ListFillerDecisions(ctx, fillerdecision.DecisionFilter{
		Kind:   fillerdecision.OutcomeSemantic,
		Cursor: fillerdecision.Cursor{BeforeCreatedAt: page.Rows[1].CreatedAt, BeforeID: page.Rows[1].ID}, Limit: 2,
	})
	if err != nil || next.Total != 3 || len(next.Rows) != 1 || next.Rows[0].ID != "d-admit" {
		t.Fatalf("second keyset page = %+v, %v", next, err)
	}

	counts, err := s.FillerDecisionCounts(ctx)
	if err != nil || counts.Admitted != 1 || counts.Rejected != 1 || counts.Reviews != 1 ||
		counts.Operational != 1 || counts.Retryable != 1 || counts.UnresolvedReviews != 1 {
		t.Fatalf("decision counts = %+v, %v", counts, err)
	}
	if err := s.CommitFillerDecisionAction(ctx, fillerdecision.Action{
		ID: "action-on-current-hold", DecisionID: "d-hold", Kind: fillerdecision.ActionAdmit,
		ActorID: "admin-1", CreatedAt: at.Add(250 * time.Millisecond),
	}); !errors.Is(err, fillerdecision.ErrActionNotAllowed) {
		t.Fatalf("current operational hold accepted a semantic action: %v", err)
	}
	// A recovered clip gets a new immutable decision. Current health and diagnostics follow that
	// latest outcome, while the earlier hold remains durable history for Activity and audit reads.
	recovered := record("d-recovered", semantic(filleradmission.VerdictAdmit))
	recovered.ClipHash = "clip-d-hold"
	recovered.CreatedAt = at.Add(500 * time.Millisecond)
	if err := s.PutFillerDecision(ctx, recovered); err != nil {
		t.Fatal(err)
	}
	currentHolds, err := s.ListFillerDecisions(ctx, fillerdecision.DecisionFilter{
		Kind: fillerdecision.OutcomeOperational, CurrentOnly: true, Limit: 10,
	})
	if err != nil || currentHolds.Total != 0 || len(currentHolds.Rows) != 0 {
		t.Fatalf("recovered hold remained in current diagnostics = %+v, %v", currentHolds, err)
	}
	historicalHolds, err := s.ListFillerDecisions(ctx, fillerdecision.DecisionFilter{
		Kind: fillerdecision.OutcomeOperational, Limit: 10,
	})
	if err != nil || historicalHolds.Total != 1 || len(historicalHolds.Rows) != 1 {
		t.Fatalf("recovered hold disappeared from history = %+v, %v", historicalHolds, err)
	}
	counts, err = s.FillerDecisionCounts(ctx)
	if err != nil || counts.Admitted != 2 || counts.Operational != 0 || counts.Retryable != 0 || counts.UnresolvedReviews != 1 {
		t.Fatalf("recovered current counts = %+v, %v", counts, err)
	}

	abandon := fillerdecision.Action{
		ID: "action-abandon", DecisionID: "d-review", Kind: fillerdecision.ActionAbandon,
		ActorID: "admin-1", Reason: "skip for now", CreatedAt: at.Add(750 * time.Millisecond),
	}
	if err := s.CommitFillerDecisionAction(ctx, abandon); err != nil {
		t.Fatal(err)
	}
	stillUnresolved, err := s.ListFillerDecisions(ctx, fillerdecision.DecisionFilter{
		Kind: fillerdecision.OutcomeSemantic, Verdict: filleradmission.VerdictReview,
		UnresolvedOnly: true, Limit: 10,
	})
	if err != nil || stillUnresolved.Total != 1 {
		t.Fatalf("abandoned review was treated as resolved = %+v, %v", stillUnresolved, err)
	}

	resolution := fillerdecision.Action{
		ID: "action-admit", DecisionID: "d-review", Kind: fillerdecision.ActionAdmit,
		ActorID: "admin-1", Reason: "closing card proves product", CreatedAt: at.Add(time.Second),
	}
	if err := s.CommitFillerDecisionAction(ctx, resolution); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitFillerDecisionAction(ctx, resolution); err != nil {
		t.Fatalf("idempotent action: %v", err)
	}
	changed := resolution
	changed.Reason = "different payload"
	if err := s.CommitFillerDecisionAction(ctx, changed); !errors.Is(err, fillerdecision.ErrConflict) {
		t.Fatalf("conflicting action = %v", err)
	}
	if err := s.CommitFillerDecisionAction(ctx, fillerdecision.Action{
		ID: "action-reject", DecisionID: "d-review", Kind: fillerdecision.ActionReject,
		ActorID: "admin-1", SupersedesID: resolution.ID, CreatedAt: at.Add(2 * time.Second),
	}); !errors.Is(err, fillerdecision.ErrActionNotAllowed) {
		t.Fatalf("invalid transition = %v", err)
	}
	reversal := fillerdecision.Action{
		ID: "action-z-reverse", DecisionID: "d-review", Kind: fillerdecision.ActionReverse,
		ActorID: "admin-1", Reason: "review answer was later disproven",
		SupersedesID: resolution.ID, CreatedAt: at.Add(3*time.Second + 100*time.Nanosecond),
	}
	if err := s.CommitFillerDecisionAction(ctx, reversal); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitFillerDecisionAction(ctx, fillerdecision.Action{
		ID: "action-a-restore", DecisionID: "d-review", Kind: fillerdecision.ActionRestore,
		ActorID: "admin-1", Reason: "new evidence restored the reviewed answer",
		SupersedesID: reversal.ID, CreatedAt: at.Add(3*time.Second + 200*time.Nanosecond),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutFillerDecision(ctx, record("d-correct", semantic(filleradmission.VerdictReview))); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitFillerDecisionAction(ctx, fillerdecision.Action{
		ID: "action-correct", DecisionID: "d-correct", Kind: fillerdecision.ActionCorrect,
		ActorID: "admin-1", Answer: "The closing card identifies soda.",
		CorrectedVerdict: filleradmission.VerdictAdmit, CreatedAt: at.Add(5 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitFillerDecisionAction(ctx, fillerdecision.Action{
		ID: "action-on-hold", DecisionID: "d-hold", Kind: fillerdecision.ActionAdmit,
		ActorID: "admin-1", CreatedAt: at.Add(6 * time.Second),
	}); !errors.Is(err, fillerdecision.ErrActionStale) {
		t.Fatalf("superseded operational hold accepted a stale semantic action: %v", err)
	}

	reviews, err := s.ListFillerDecisions(ctx, fillerdecision.DecisionFilter{
		Kind: fillerdecision.OutcomeSemantic, Verdict: filleradmission.VerdictReview,
		UnresolvedOnly: true, Limit: 10,
	})
	if err != nil || reviews.Total != 0 || len(reviews.Rows) != 0 {
		t.Fatalf("resolved review remained actionable = %+v, %v", reviews, err)
	}
	actions, err := s.ListFillerDecisionActions(ctx, fillerdecision.ActionFilter{DecisionID: "d-review", Limit: 10})
	if err != nil || actions.Total != 4 || len(actions.Rows) != 4 || actions.Rows[0].ID != "action-a-restore" {
		t.Fatalf("action history = %+v, %v", actions, err)
	}
	activity, err := s.ListFillerDecisionActivity(ctx, fillerdecision.Cursor{}, 10)
	if err != nil || activity.Total != 10 || len(activity.Rows) != 10 || activity.Rows[0].Kind != fillerdecision.ActivityCorrection {
		t.Fatalf("activity projection = %+v, %v", activity, err)
	}
	kinds := make(map[fillerdecision.ActivityKind]int)
	for _, item := range activity.Rows {
		if item.ApplicationMode != fillerdecision.ApplicationModeShadow {
			t.Fatalf("activity %q application mode = %q, want shadow", item.ID, item.ApplicationMode)
		}
		kinds[item.Kind]++
	}
	for kind, want := range map[fillerdecision.ActivityKind]int{
		fillerdecision.ActivityAutomaticAdmit: 2, fillerdecision.ActivityAutomaticReject: 1,
		fillerdecision.ActivityReviewRequested: 2, fillerdecision.ActivityActionAdmit: 1,
		fillerdecision.ActivityCorrection: 1, fillerdecision.ActivityRestore: 1,
		fillerdecision.ActivityReversal: 1, fillerdecision.ActivityReviewAbandoned: 1,
	} {
		if kinds[kind] != want {
			t.Fatalf("activity kind %q = %d, want %d: %+v", kind, kinds[kind], want, activity.Rows)
		}
	}
	counts, err = s.FillerDecisionCounts(ctx)
	if err != nil || counts.UnresolvedReviews != 0 {
		t.Fatalf("resolved counts = %+v, %v", counts, err)
	}
}

// openSecondConformanceStore proves committed decision state through a fresh database pool while
// leaving the suite-owned pool alive for its cleanup. It runs inside the one shared conformance
// assertion, so SQLite and Postgres prove the same restart/reconnect property.
func openSecondConformanceStore(t *testing.T, s Store) Store {
	t.Helper()
	impl := s.(*sqlStore)
	var (
		second *sqlStore
		err    error
	)
	if impl.dialect == DialectPostgres {
		second, err = openPostgres(t.Context(), impl.dsn)
	} else {
		var opened Store
		opened, err = Open(t.Context(), "sqlite://"+impl.path, true)
		if err == nil {
			return opened
		}
	}
	if err != nil {
		t.Fatalf("open second conformance store: %v", err)
	}
	return second
}
