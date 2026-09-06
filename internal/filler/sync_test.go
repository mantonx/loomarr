package filler_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/mediatools"
)

// fakeSource is a Tunarr local filler source returning a fixed set of raw clips.
// ensures counts EnsureLocalSource calls so the idempotent-setup path is covered.
type fakeSource struct {
	clips     []filler.RawClip
	ensures   int
	ensureDir string
	listErr   error
}

func (f *fakeSource) EnsureLocalSource(_ context.Context, dir string) error {
	f.ensures++
	f.ensureDir = dir
	return nil
}
func (f *fakeSource) ListLocalClips(_ context.Context) ([]filler.RawClip, error) {
	return f.clips, f.listErr
}

// memStore is an in-memory filler.Store for sync tests.
//
// ⚠ Keyed on the clip's ID (its content hash), exactly as the real store is since V38c. It used
// to key on `c.Path`, which quietly made every sync test agree with a Syncer that also keyed on
// the path — so the re-key left `Sync` reading the wrong field and nothing went red. A double
// that models identity differently from the real thing tests the double.
type memStore struct{ clips map[string]filler.StoreClip }

func newMemStore() *memStore { return &memStore{clips: map[string]filler.StoreClip{}} }

func (m *memStore) UpsertClip(_ context.Context, c filler.StoreClip) error {
	m.clips[c.ID()] = c
	return nil
}

type manifestAuthority struct {
	artifact filler.AcquisitionArtifact
	found    bool
}

func (m *manifestAuthority) AcquisitionArtifactForClip(_ context.Context, _, clipHash string) (filler.AcquisitionArtifact, bool, error) {
	return m.artifact, m.found && clipHash == m.artifact.ClipHash, nil
}

func (m *manifestAuthority) UpsertAcquisitionArtifacts(_ context.Context, artifacts []filler.AcquisitionArtifact) error {
	if len(artifacts) == 1 {
		m.artifact = artifacts[0]
		m.found = true
	}
	return nil
}
func (m *memStore) GetClip(_ context.Context, id string) (filler.StoreClip, bool, error) {
	c, ok := m.clips[id]
	return c, ok, nil
}
func (m *memStore) DeleteClipsNotIn(_ context.Context, keep []string) (int, error) {
	keepSet := map[string]bool{}
	for _, id := range keep {
		keepSet[id] = true
	}
	n := 0
	for id := range m.clips {
		if !keepSet[id] {
			delete(m.clips, id)
			n++
		}
	}
	return n, nil
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testLayout(root string, watch ...string) filler.Layout {
	configuredWatch := ""
	if len(watch) > 0 {
		configuredWatch = watch[0]
	}
	layout, err := filler.NewLayout(root, configuredWatch)
	if err != nil {
		panic(err)
	}
	return layout
}

func TestSync_RegistersTheConfiguredSharedVolumePath(t *testing.T) {
	realRoot := t.TempDir()
	aliasRoot := filepath.Join(t.TempDir(), "shared-clips")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	source := &fakeSource{}
	syncer := filler.NewSyncer(source, newMemStore(), testLayout(aliasRoot), time.Now, discardLog())

	if _, err := syncer.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if source.ensureDir != aliasRoot {
		t.Errorf("registered source = %q, want configured shared-volume path %q", source.ensureDir, aliasRoot)
	}
}

// raw builds a scanned clip. ⚠ ID and Path are set SEPARATELY — id is the content hash, path is
// where the file sits. The fixtures below pass the same string for both where the distinction
// does not matter; the tests that turn on it (see the two-folder case) pass different ones.
func raw(id, name string, kind filler.Kind, dur int64, era int) filler.RawClip {
	return filler.RawClip{ID: id, Path: id, Name: name, Kind: kind, DurationMs: dur, Era: era}
}

func newSyncer(source *fakeSource, st *memStore) *filler.Syncer {
	return filler.NewSyncer(source, st, testLayout("/drop"),
		func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }, discardLog())
}

func TestSync_AddsClips_DurationFromServer(t *testing.T) {
	source := &fakeSource{clips: []filler.RawClip{
		raw("c1", "Frosted Flakes 1992", filler.Commercial, 30000, 1992),
		raw("b1", "Bumper", filler.Bumper, 5000, 0),
	}}
	st := newMemStore()
	res, err := newSyncer(source, st).Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 || res.Total != 2 {
		t.Fatalf("sync result = %+v, want added 2 / total 2", res)
	}
	c1 := st.clips["c1"]
	if c1.DurationMs != 30000 {
		t.Errorf("duration not from server: %d", c1.DurationMs)
	}
	if c1.Era != 1992 {
		t.Errorf("initial era from filename lost: %d", c1.Era)
	}
}

// THE KEY PROPERTY (§10): a re-sync PRESERVES loomarr-owned tags. A clip tagged
// (by AI or by hand) keeps its era/audience/category when the media server
// re-lists it — the server only owns id/name/duration.
func TestSync_PreservesTagsOnResync(t *testing.T) {
	source := &fakeSource{clips: []filler.RawClip{raw("c1", "clip", filler.Commercial, 30000, 0)}}
	st := newMemStore()
	s := newSyncer(source, st)

	// First sync creates the clip untagged.
	_, _ = s.Sync(context.Background())

	// A human/AI tags it.
	tagged := st.clips["c1"]
	tagged.Era = 1994
	tagged.Audience = filler.Kids
	tagged.Category = "cereal"
	tagged.AITagged = true
	st.clips["c1"] = tagged

	// The media server re-lists the same clip (maybe with a corrected name).
	source.clips = []filler.RawClip{raw("c1", "clip (renamed)", filler.Commercial, 30000, 0)}
	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	after := st.clips["c1"]
	// Tags survive.
	if after.Era != 1994 || after.Audience != filler.Kids || after.Category != "cereal" || !after.AITagged {
		t.Fatalf("re-sync clobbered loomarr-owned tags: %+v", after.Clip)
	}
	// Server-owned name updated.
	if after.Name != "clip (renamed)" {
		t.Errorf("server name not updated: %q", after.Name)
	}
	if res.Updated != 1 {
		t.Errorf("renamed clip should count as updated, got %+v", res)
	}
}

// Idempotent: a no-change re-sync makes no updates.
func TestSync_IdempotentNoChange(t *testing.T) {
	source := &fakeSource{clips: []filler.RawClip{raw("c1", "clip", filler.Commercial, 30000, 1992)}}
	st := newMemStore()
	s := newSyncer(source, st)
	_, _ = s.Sync(context.Background())
	res, _ := s.Sync(context.Background())
	if res.Added != 0 || res.Updated != 0 {
		t.Errorf("no-change re-sync should be a no-op, got %+v", res)
	}
}

// ⚠ **Artwork that appears on a LATER pass must still reach the database.**
//
// `serverFieldsUnchanged` gates the write, so a scan-owned field it does not compare can never be
// persisted for an existing clip: the merge assigns it and the sync then skips the row as
// unchanged. This is not hypothetical — it is exactly what happened live in V39. All 13 clips had
// their previews rendered to disk, `merged.Preview = rc.Preview` ran, and every `preview` column
// stayed empty, because Name/DurationMs/Kind were identical.
//
// That path is the NORMAL one on upgrade: the whole existing catalog gains previews on a re-scan
// of clips whose rows already exist. `Thumbnail` carried the same latent bug and had simply never
// been exercised, because a still was always generated on the pass that first inserted the clip.
func TestSync_LateArtworkIsPersistedOnAReScan(t *testing.T) {
	// First pass: a clip whose artwork has not been rendered yet (the pre-V39 catalog).
	bare := raw("c1", "clip", filler.Commercial, 30000, 1992)
	source := &fakeSource{clips: []filler.RawClip{bare}}
	st := newMemStore()
	s := newSyncer(source, st)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Second pass: the artwork now exists, and NOTHING else about the clip has changed.
	withArt := bare
	withArt.Thumbnail = "c1.jpg"
	withArt.Preview = "c1.webp"
	source.clips = []filler.RawClip{withArt}

	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 {
		t.Errorf("updated = %d, want 1 — newly rendered artwork is a REAL change, and skipping "+
			"the write means it can never be stored", res.Updated)
	}

	got, ok, err := st.GetClip(context.Background(), "c1")
	if err != nil || !ok {
		t.Fatalf("get c1: ok=%v err=%v", ok, err)
	}
	if got.Preview != "c1.webp" {
		t.Errorf("preview = %q, want it persisted — every hover on this install renders nothing "+
			"until this column is written", got.Preview)
	}
	if got.Thumbnail != "c1.jpg" {
		t.Errorf("thumbnail = %q, want it persisted", got.Thumbnail)
	}
}

// Prune: a clip removed from the media server's library is removed from the catalog.
func TestSync_PrunesRemovedClips(t *testing.T) {
	source := &fakeSource{clips: []filler.RawClip{
		raw("c1", "keep", filler.Commercial, 30000, 1992),
		raw("c2", "goes away", filler.Commercial, 30000, 1993),
	}}
	st := newMemStore()
	s := newSyncer(source, st)
	_, _ = s.Sync(context.Background())

	// c2 disappears from the media server.
	source.clips = []filler.RawClip{raw("c1", "keep", filler.Commercial, 30000, 1992)}
	res, _ := s.Sync(context.Background())
	if res.Pruned != 1 {
		t.Errorf("pruned = %d, want 1", res.Pruned)
	}
	if _, ok := st.clips["c2"]; ok {
		t.Error("removed clip still in catalog")
	}
	if _, ok := st.clips["c1"]; !ok {
		t.Error("kept clip wrongly pruned")
	}
}

func TestSync_FailedListingPreservesLastKnownCatalog(t *testing.T) {
	source := &fakeSource{clips: []filler.RawClip{
		raw("c1", "known clip", filler.Commercial, 30000, 1992),
	}}
	st := newMemStore()
	syncer := newSyncer(source, st)
	if _, err := syncer.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	// A broken or unavailable replacement layout must not look like a successful empty scan.
	// The error is surfaced and pruning is never reached, so the last known catalog survives.
	source.clips = nil
	source.listErr = fs.ErrNotExist
	if _, err := syncer.Sync(context.Background()); err == nil {
		t.Fatal("failed listing reported success")
	}
	if _, ok := st.clips["c1"]; !ok {
		t.Fatal("failed listing pruned the last known catalog")
	}
}

// --- V38: the lifecycle fork ---

// ⚠ THE mechanism the whole review queue depends on. Ingest downloads into the same folder the
// scan watches, so at catalogue time a fetched clip and a hand-copied one are both just files.
// The `.info.json` sidecar `clipfetch` writes is what tells them apart.
//
// If this fork were wrong in the "no sidecar ⇒ hold" direction, an operator's own files would sit
// invisible until approved. Wrong the other way, every download would go straight to air.
func TestSync_HoldsDownloadedClipsAndFilesHandCopiedOnes(t *testing.T) {
	dir := t.TempDir()
	// A downloaded clip. ⚠ The signal is the `fetchedBy` FIELD, not the sidecar's existence
	// (V38c): Loomarr writes sidecars for hand-dropped clips too now, so a bare `{"title":"x"}`
	// no longer means "downloaded" — it means "tagged". This fixture failed on exactly that,
	// which is the change working.
	if err := os.WriteFile(filepath.Join(dir, "fetched.info.json"),
		[]byte(`{"title":"x","loomarr":{"fetchedBy":"loomarr"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	source := &fakeSource{clips: []filler.RawClip{
		raw("fetched.mp4", "Fetched ad", filler.Commercial, 30000, 0),
		raw("copied.mp4", "Copied ad", filler.Commercial, 30000, 0),
	}}
	st := newMemStore()
	sync := filler.NewSyncer(source, st, testLayout(dir),
		func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }, discardLog())

	if _, err := sync.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	fetched, _, _ := st.GetClip(context.Background(), "fetched.mp4")
	if !fetched.Held {
		t.Error("a DOWNLOADED clip (sidecar present) was filed on sight — it must wait for review")
	}
	copied, _, _ := st.GetClip(context.Background(), "copied.mp4")
	if copied.Held {
		t.Error("a HAND-COPIED clip (no sidecar) was held — a file the operator placed themselves " +
			"would sit invisible until approved")
	}
}

func TestSync_DurableManifestHoldsAndRepairsDownloadedClipWithoutSidecar(t *testing.T) {
	dir := t.TempDir()
	temporary := filepath.Join(t.TempDir(), "download.mp4")
	if err := os.WriteFile(temporary, []byte("downloaded video bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	clipHash, err := filler.ClipID(temporary)
	if err != nil {
		t.Fatal(err)
	}
	media, err := filler.ClipPath(dir, clipHash, ".mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(media), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, media); err != nil {
		t.Fatal(err)
	}
	digest, size, err := filler.FileSHA256(media)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(dir, media)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	authority := &manifestAuthority{found: true, artifact: filler.AcquisitionArtifact{
		ID: "artifact-1", AcquisitionID: "acq-1", SourceID: "youtube:classic",
		Provider: "youtube", SourceURL: "https://youtube.com/watch?v=one",
		StagingPath: ".loomarr-acquisitions/acq-1/download.mp4", MediaPath: "download.mp4",
		SidecarPath: "download.info.json", MediaSHA256: digest, MediaBytes: size,
		ClipHash: clipHash, State: filler.ArtifactPublished, CompletedAt: now, UpdatedAt: now,
	}}
	source := &fakeSource{clips: []filler.RawClip{
		raw(clipHash, "Downloaded ad", filler.Commercial, 30_000, 0),
	}}
	source.clips[0].Path = filepath.ToSlash(relative)
	st := newMemStore()
	syncer := filler.NewSyncer(source, st, testLayout(dir), func() time.Time { return now }, discardLog()).
		WithAcquisitionAuthority(authority)

	if _, err := syncer.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	clip, ok := st.clips[clipHash]
	if !ok || !clip.Held || clip.Source != "youtube:classic" {
		t.Fatalf("manifested clip = %+v, %v; want held and source-attributed", clip, ok)
	}
	tags, ok := filler.ReadSidecarTags(media)
	if !ok || tags.AcquisitionID != "acq-1" || tags.SourceID != "youtube:classic" || !filler.SidecarFetchedByUs(media) {
		t.Fatalf("repaired portable provenance = %+v, %v", tags, ok)
	}
	if authority.artifact.State != filler.ArtifactConsumed || authority.artifact.MediaPath != filepath.ToSlash(relative) {
		t.Fatalf("consumed manifest = %+v", authority.artifact)
	}
}

func TestSync_RefusesManifestDigestSubstitution(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, strings.Repeat("a", 64)+".mp4")
	if err := os.WriteFile(media, []byte("substituted bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	clipHash, err := filler.ClipID(media)
	if err != nil {
		t.Fatal(err)
	}
	_, size, err := filler.FileSHA256(media)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	authority := &manifestAuthority{found: true, artifact: filler.AcquisitionArtifact{
		ID: "artifact-1", AcquisitionID: "acq-1", Provider: "youtube",
		SourceURL: "https://youtube.com/watch?v=one", MediaPath: filepath.Base(media),
		MediaSHA256: strings.Repeat("f", 64), MediaBytes: size, ClipHash: clipHash,
		State: filler.ArtifactPublished, CompletedAt: now, UpdatedAt: now,
	}}
	source := &fakeSource{clips: []filler.RawClip{raw(clipHash, "Substituted", filler.Commercial, 30_000, 0)}}
	source.clips[0].Path = filepath.Base(media)
	st := newMemStore()
	syncer := filler.NewSyncer(source, st, testLayout(dir), func() time.Time { return now }, discardLog()).
		WithAcquisitionAuthority(authority)

	if _, err := syncer.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.clips[clipHash]; ok {
		t.Fatal("digest-substituted acquisition became catalog content")
	}
	if authority.artifact.State != filler.ArtifactRepair || authority.artifact.RepairReason == "" {
		t.Fatalf("artifact = %+v, want durable repair reason", authority.artifact)
	}
}

// ⚠ A re-scan must never re-hold a clip a human already filed. The scan sees the same sidecar on
// every pass, so without the preserve in the merge, filing a clip would last exactly until the
// next sync — and the operator would find it back in the queue with no explanation.
func TestSync_ReScanDoesNotReHoldAFiledClip(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fetched.info.json"),
		[]byte(`{"title":"x","loomarr":{"fetchedBy":"loomarr"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	source := &fakeSource{clips: []filler.RawClip{
		raw("fetched.mp4", "Fetched ad", filler.Commercial, 30000, 0),
	}}
	st := newMemStore()
	sync := filler.NewSyncer(source, st, testLayout(dir),
		func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }, discardLog())

	if _, err := sync.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The operator files it from Incoming.
	c, _, _ := st.GetClip(context.Background(), "fetched.mp4")
	c.Held = false
	if err := st.UpsertClip(context.Background(), c); err != nil {
		t.Fatal(err)
	}

	// ⚠ The re-scan must actually WRITE, or this proves nothing. `serverFieldsUnchanged` skips
	// an unchanged clip before any write, so a naive second Sync() passes whatever the merge
	// does with `Held` — a sabotage that recomputed it from the sidecar still went green. The
	// duration change below is what forces the update path this test exists to cover.
	source.clips[0].DurationMs = 31000
	if _, err := sync.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _, _ := st.GetClip(context.Background(), "fetched.mp4")
	if after.DurationMs != 31000 {
		t.Fatal("the re-scan did not write — this test would pass vacuously")
	}
	if after.Held {
		t.Error("a re-scan RE-HELD a clip the operator had filed — the sidecar is still there on " +
			"every pass, so `Held` must be preserved for a clip we already know")
	}
}

// Two watched folders each holding `ads/coke.mp4` — different adverts that happen to share a
// relative path. THE case V38c moved identity off the path for (§10).
//
// ⚠ This is the test the re-key was missing. `Sync` kept keying on `rc.Path` after identity
// became `rc.ID`, and nothing caught it because every fixture set the two to the same string and
// the in-memory double keyed on the path as well. With the path as identity the second clip
// overwrites the first, `keep` carries one entry where two are live, and the prune then deletes a
// clip that is sitting right there on disk.
func TestSync_TwoFoldersSharingAPathAreTwoClips(t *testing.T) {
	source := &fakeSource{clips: []filler.RawClip{
		{ID: "hash-coke-1985", Path: "ads/coke.mp4", Name: "Coke 1985",
			Kind: filler.Commercial, DurationMs: 30000},
		{ID: "hash-coke-1992", Path: "ads/coke.mp4", Name: "Coke 1992",
			Kind: filler.Commercial, DurationMs: 15000},
	}}
	st := newMemStore()

	res, err := newSyncer(source, st).Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 {
		t.Errorf("Added = %d, want 2 — one clip overwrote the other, so identity is still the path",
			res.Added)
	}
	if len(st.clips) != 2 {
		t.Fatalf("store holds %d clips, want 2", len(st.clips))
	}
	// And neither was pruned: `keep` must carry both identities, not one path twice.
	if _, ok, _ := st.GetClip(context.Background(), "hash-coke-1985"); !ok {
		t.Error("the 1985 advert was pruned — `keep` is collecting paths, so a live clip was deleted")
	}
	if _, ok, _ := st.GetClip(context.Background(), "hash-coke-1992"); !ok {
		t.Error("the 1992 advert is missing from the catalog")
	}
	// The location still travels with each row — identity moved, the path did not disappear.
	if got := st.clips["hash-coke-1985"].Path; got != "ads/coke.mp4" {
		t.Errorf("Path = %q, want the on-disk location", got)
	}
}

// --- V38c: intake runs inside the sync ---

// realScanSource wires the ACTUAL DirSource to a temp folder, so this test exercises the real
// intake → scan → catalog path rather than a double's idea of it. The property under test is an
// ORDERING one, and a fake source returning fixed clips cannot express it.
type realScanSource struct{ dir string }

func (r realScanSource) EnsureLocalSource(context.Context, string) error { return nil }
func (r realScanSource) ListLocalClips(ctx context.Context) ([]filler.RawClip, error) {
	clips, _, err := filler.ScanDir(ctx, r.dir, func(context.Context, string) (filler.Probed, error) {
		return filler.Probed{DurationMs: 30_000}, nil
	})
	return clips, err
}

func TestSync_CatalogRebuildRestoresConditioningParentFromSidecar(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "reviewed-child.mp4")
	if err := os.WriteFile(media, []byte("reviewed child bytes that survive a catalog rebuild"), 0o600); err != nil {
		t.Fatal(err)
	}
	childHash, err := filler.ClipID(media)
	if err != nil {
		t.Fatal(err)
	}
	const parentHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := filler.WriteSidecarTags(media, filler.SidecarTags{
		OriginalName: "Reviewed child",
		ConditioningLineage: &filler.ConditioningLineage{
			ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 12_000, IntendedEndMs: 42_000,
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	st := newMemStore()
	sync := filler.NewSyncer(realScanSource{dir}, st, testLayout(dir), time.Now, discardLog())
	if _, err := sync.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.clips) != 1 {
		t.Fatalf("rebuilt catalog = %+v, want one child", st.clips)
	}
	for _, child := range st.clips {
		if child.ParentHash != parentHash {
			t.Fatalf("rebuilt child parent = %q, want sidecar identity %q", child.ParentHash, parentHash)
		}
	}
}

func TestSync_EmptyCatalogRebuildMarksConditioningParentCompositeInEitherScanOrder(t *testing.T) {
	for _, tc := range []struct {
		name       string
		parentPath string
		childPath  string
	}{
		{name: "child scanned first", parentPath: "z-parent.mp4", childPath: "a-child.mp4"},
		{name: "parent scanned first", parentPath: "a-parent.mp4", childPath: "z-child.mp4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			parentFull := filepath.Join(dir, tc.parentPath)
			childFull := filepath.Join(dir, tc.childPath)
			if err := os.WriteFile(parentFull, []byte("retained compilation bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(childFull, []byte("reviewed child bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			parentHash, err := filler.ClipID(parentFull)
			if err != nil {
				t.Fatal(err)
			}
			childHash, err := filler.ClipID(childFull)
			if err != nil {
				t.Fatal(err)
			}
			if err := filler.WriteSidecarTags(childFull, filler.SidecarTags{ConditioningLineage: &filler.ConditioningLineage{
				ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
			}}, false); err != nil {
				t.Fatal(err)
			}
			st := newMemStore()
			syncer := filler.NewSyncer(realScanSource{dir}, st, testLayout(dir), time.Now, discardLog())
			if _, err := syncer.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			parent, found, err := st.GetClip(context.Background(), parentHash)
			if err != nil || !found || !parent.IsComposite {
				t.Fatalf("rebuilt parent = %+v, found=%v, err=%v; want retained composite", parent, found, err)
			}
			child, found, err := st.GetClip(context.Background(), childHash)
			if err != nil || !found || child.ParentHash != parentHash {
				t.Fatalf("rebuilt child = %+v, found=%v, err=%v", child, found, err)
			}
		})
	}
}

func TestSync_ConditionedChildClearsHoldOnlyWhenRetainedParentResolves(t *testing.T) {
	for _, tc := range []struct {
		name       string
		parentPath string
		childPath  string
		withParent bool
		parentMode string
		selfParent bool
		wantHeld   bool
	}{
		{name: "child scanned before parent", parentPath: "z-parent.mp4", childPath: "a-child.mp4", withParent: true},
		{name: "child scanned after parent", parentPath: "a-parent.mp4", childPath: "z-child.mp4", withParent: true},
		{name: "parent missing", parentPath: "missing-parent.mp4", childPath: "child.mp4", wantHeld: true},
		{name: "parent sidecar unreadable", parentPath: "parent.mp4", childPath: "child.mp4", withParent: true, parentMode: "unreadable", wantHeld: true},
		{name: "parent is itself conditioned lineage", parentPath: "parent.mp4", childPath: "child.mp4", withParent: true, parentMode: "conditioned", wantHeld: true},
		{name: "child names itself as parent", parentPath: "parent.mp4", childPath: "child.mp4", withParent: true, selfParent: true, wantHeld: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			parentFull := filepath.Join(dir, tc.parentPath)
			if err := os.WriteFile(parentFull, []byte("retained compilation bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			parentHash, err := filler.ClipID(parentFull)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.withParent {
				if err := os.Remove(parentFull); err != nil {
					t.Fatal(err)
				}
			}
			childFull := filepath.Join(dir, tc.childPath)
			if err := os.WriteFile(childFull, []byte("conditioned child bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			childHash, err := filler.ClipID(childFull)
			if err != nil {
				t.Fatal(err)
			}
			if tc.parentMode == "unreadable" {
				parentSidecar := strings.TrimSuffix(parentFull, filepath.Ext(parentFull)) + ".info.json"
				if err := os.WriteFile(parentSidecar, []byte(`{"loomarr":`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.parentMode == "conditioned" {
				if err := filler.WriteSidecarTags(parentFull, filler.SidecarTags{ConditioningLineage: &filler.ConditioningLineage{
					ChildHash: parentHash, ParentHash: strings.Repeat("b", 64), IntendedStartMs: 1_000, IntendedEndMs: 31_000,
				}}, false); err != nil {
					t.Fatal(err)
				}
			}
			lineageParentHash := parentHash
			if tc.selfParent {
				lineageParentHash = childHash
			}
			const reviewedHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			before := syncConditioningMeasurement(true)
			after := syncConditioningMeasurement(false)
			if err := filler.WriteSidecarTags(childFull, filler.SidecarTags{
				Mezzanine: mediatools.DefaultMezzanine().ID(),
				ConditioningLineage: &filler.ConditioningLineage{
					ChildHash: reviewedHash, ParentHash: lineageParentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
				},
				Conditioning: &filler.ConditioningEvidence{
					BeforeRewriteHash: reviewedHash, AfterRewriteHash: childHash,
					BeforeRewrite: before, AfterRewrite: after,
					DerivedParentEdgesAfterRewrite: before.Cuts[0],
				},
				MediaQuality: &after.Quality,
			}, false); err != nil {
				t.Fatal(err)
			}

			st := newMemStore()
			if _, err := filler.NewSyncer(realScanSource{dir}, st, testLayout(dir), time.Now, discardLog()).Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			child, found, err := st.GetClip(context.Background(), childHash)
			if err != nil || !found || child.Held != tc.wantHeld || child.ParentHash != lineageParentHash {
				t.Fatalf("rebuilt child = %+v, found=%v, err=%v; held want %v", child, found, err, tc.wantHeld)
			}
			if tc.withParent && !tc.wantHeld {
				parent, found, err := st.GetClip(context.Background(), parentHash)
				if err != nil || !found || !parent.IsComposite {
					t.Fatalf("rebuilt parent = %+v, found=%v, err=%v; want retained composite", parent, found, err)
				}
			} else if tc.withParent {
				parent, found, err := st.GetClip(context.Background(), parentHash)
				if err != nil || !found || parent.IsComposite {
					t.Fatalf("invalid authority parent = %+v, found=%v, err=%v; must not be promoted", parent, found, err)
				}
			}
		})
	}
}

func syncConditioningMeasurement(directEdges bool) mediatools.ConditioningMeasurement {
	available := func(ms int64) mediatools.OptionalMilliseconds {
		return mediatools.OptionalMilliseconds{Milliseconds: ms, Available: true}
	}
	edge := func(kind mediatools.StreamKind, index int) mediatools.ConditioningCutStream {
		out := mediatools.ConditioningCutStream{Kind: kind, Index: index}
		if directEdges {
			out.StartError, out.EndError = available(0), available(0)
		}
		return out
	}
	return mediatools.ConditioningMeasurement{
		ContainerDurationMs: 30_000,
		Streams: []mediatools.ConditioningStream{
			{Kind: mediatools.StreamVideo, Index: 0, Start: available(0), Duration: available(30_000), Cadence: &mediatools.Rational{Numerator: 30_000, Denominator: 1001}},
			{Kind: mediatools.StreamAudio, Index: 1, Start: available(120), Duration: available(29_880)},
		},
		AVSkew: mediatools.ConditioningSkew{Start: available(120), End: available(0)},
		Loudness: mediatools.ConditioningLoudness{IntegratedLUFS: -23, Available: true,
			TruePeak: mediatools.ConditioningTruePeak{State: mediatools.TruePeakFinite, DBTP: -2}},
		Quality: mediatools.MediaQuality{EvidenceVersion: mediatools.MediaQualityEvidenceV1,
			Provenance: mediatools.MediaQualityProvenanceFFmpegDetectors, DurationMs: 30_000},
		Cuts: []mediatools.ConditioningCutMeasurement{{Intended: mediatools.Interval{StartMs: 1_000, EndMs: 31_000}, Streams: []mediatools.ConditioningCutStream{
			edge(mediatools.StreamVideo, 0), edge(mediatools.StreamAudio, 1),
		}}},
	}
}

func TestSync_MalformedConditioningLineageCannotBecomeAnAirableTopLevelClip(t *testing.T) {
	dir := t.TempDir()
	childFull := filepath.Join(dir, "damaged-child.mp4")
	if err := os.WriteFile(childFull, []byte("damaged lineage child"), 0o600); err != nil {
		t.Fatal(err)
	}
	childHash, err := filler.ClipID(childFull)
	if err != nil {
		t.Fatal(err)
	}
	if err := filler.WriteSidecarTags(childFull, filler.SidecarTags{ConditioningLineage: &filler.ConditioningLineage{
		ParentHash: "retained-parent", IntendedStartMs: 12_000, IntendedEndMs: 12_000,
	}}, false); err != nil {
		t.Fatal(err)
	}
	st := newMemStore()
	if _, err := filler.NewSyncer(realScanSource{dir}, st, testLayout(dir), time.Now, discardLog()).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	child, found, err := st.GetClip(context.Background(), childHash)
	if err != nil || !found || !child.Held || child.ParentHash != "retained-parent" {
		t.Fatalf("malformed-lineage child = %+v, found=%v, err=%v; want held with parent identity", child, found, err)
	}
}

func TestSync_BlankConditioningLineageCannotBecomeAnAirableTopLevelClip(t *testing.T) {
	dir := t.TempDir()
	childFull := filepath.Join(dir, "blank-lineage-child.mp4")
	if err := os.WriteFile(childFull, []byte("blank lineage child"), 0o600); err != nil {
		t.Fatal(err)
	}
	childHash, err := filler.ClipID(childFull)
	if err != nil {
		t.Fatal(err)
	}
	if err := filler.WriteSidecarTags(childFull, filler.SidecarTags{ConditioningLineage: &filler.ConditioningLineage{
		ChildHash: childHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
	}}, false); err != nil {
		t.Fatal(err)
	}
	st := newMemStore()
	if _, err := filler.NewSyncer(realScanSource{dir}, st, testLayout(dir), time.Now, discardLog()).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	child, found, err := st.GetClip(context.Background(), childHash)
	if err != nil || !found || !child.Held {
		t.Fatalf("blank-lineage child = %+v, found=%v, err=%v; want held", child, found, err)
	}
}

func TestSync_IncompleteConditioningEvidenceRemainsHeldAfterCatalogRebuild(t *testing.T) {
	dir := t.TempDir()
	childFull := filepath.Join(dir, "incomplete-restart.mp4")
	if err := os.WriteFile(childFull, []byte("transformed child without conditioning facts"), 0o600); err != nil {
		t.Fatal(err)
	}
	childHash, err := filler.ClipID(childFull)
	if err != nil {
		t.Fatal(err)
	}
	if err := filler.WriteSidecarTags(childFull, filler.SidecarTags{
		Mezzanine: mediatools.DefaultMezzanine().ID(),
		MediaQuality: &filler.MediaQuality{
			EvidenceVersion: mediatools.MediaQualityEvidenceV1,
			Provenance:      mediatools.MediaQualityProvenanceFFmpegDetectors,
			DurationMs:      30_000,
		},
		ConditioningLineage: &filler.ConditioningLineage{
			ChildHash: "reviewed-source-hash", ParentHash: "retained-parent",
			IntendedStartMs: 1_000, IntendedEndMs: 31_000,
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	st := newMemStore()
	if _, err := filler.NewSyncer(realScanSource{dir}, st, testLayout(dir), time.Now, discardLog()).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	child, found, err := st.GetClip(context.Background(), childHash)
	if err != nil || !found || !child.Held || child.ParentHash != "retained-parent" {
		t.Fatalf("incomplete rebuilt child = %+v, found=%v, err=%v; want held", child, found, err)
	}
}

func TestSync_ConditioningStateWithoutLineageCannotBecomeAnAirableTopLevelClip(t *testing.T) {
	for _, tc := range []struct {
		name        string
		conditioned bool
		wantHeld    bool
	}{
		{name: "conditioning evidence without lineage", conditioned: true, wantHeld: true},
		{name: "ordinary top-level mezzanine", conditioned: false, wantHeld: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			media := filepath.Join(dir, "clip.mp4")
			if err := os.WriteFile(media, []byte("clip bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			tags := filler.SidecarTags{Mezzanine: mediatools.DefaultMezzanine().ID()}
			if tc.conditioned {
				tags.Conditioning = &filler.ConditioningEvidence{}
			}
			if err := filler.WriteSidecarTags(media, tags, false); err != nil {
				t.Fatal(err)
			}
			_, sidecarState := filler.ReadSidecarTagsState(media)
			if tc.conditioned && sidecarState != filler.SidecarInvalid {
				t.Fatalf("conditioning without lineage state = %v, want invalid", sidecarState)
			}
			if !tc.conditioned && sidecarState != filler.SidecarValid {
				t.Fatalf("ordinary top-level mezzanine state = %v, want valid", sidecarState)
			}
			id, err := filler.ClipID(media)
			if err != nil {
				t.Fatal(err)
			}
			st := newMemStore()
			if _, err := filler.NewSyncer(realScanSource{dir}, st, testLayout(dir), time.Now, discardLog()).Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			clip, found, err := st.GetClip(context.Background(), id)
			if err != nil || !found || clip.Held != tc.wantHeld {
				t.Fatalf("rebuilt clip = %+v, found=%v, err=%v; held want %v", clip, found, err, tc.wantHeld)
			}
		})
	}
}

func TestSync_UnreadableOrWrongTypedConditioningStateRemainsHeld(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sidecar   string
		wantHeld  bool
		wantState filler.SidecarReadState
	}{
		{name: "corrupt JSON", sidecar: `{"loomarr":`, wantHeld: true, wantState: filler.SidecarInvalid},
		{name: "conditioned mezzanine omitted lineage", sidecar: `{"loomarr":{"mezzanine":"h264-crf20-aac192","conditioning":{"beforeRewriteHash":"before","afterRewriteHash":"after"}}}`, wantHeld: true, wantState: filler.SidecarInvalid},
		{name: "conditioned mezzanine null lineage", sidecar: `{"loomarr":{"mezzanine":"h264-crf20-aac192","conditioningLineage":null,"conditioning":{"beforeRewriteHash":"before","afterRewriteHash":"after"}}}`, wantHeld: true, wantState: filler.SidecarInvalid},
		{name: "conditioned mezzanine blank lineage", sidecar: `{"loomarr":{"mezzanine":"h264-crf20-aac192","conditioningLineage":{},"conditioning":{"beforeRewriteHash":"before","afterRewriteHash":"after"}}}`, wantHeld: true, wantState: filler.SidecarInvalid},
		{name: "conditioned mezzanine malformed lineage", sidecar: `{"loomarr":{"mezzanine":"h264-crf20-aac192","conditioningLineage":"not-lineage","conditioning":{"beforeRewriteHash":"before","afterRewriteHash":"after"}}}`, wantHeld: true, wantState: filler.SidecarInvalid},
		{name: "wrong typed lineage", sidecar: `{"loomarr":{"conditioningLineage":"not-lineage"}}`, wantHeld: true, wantState: filler.SidecarInvalid},
		{name: "wrong typed conditioning evidence", sidecar: `{"loomarr":{"conditioning":[]}}`, wantHeld: true, wantState: filler.SidecarInvalid},
		{name: "valid ordinary top-level tags", sidecar: `{"loomarr":{"originalName":"ordinary.mp4"}}`, wantHeld: false, wantState: filler.SidecarValid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			media := filepath.Join(dir, "clip.mp4")
			if err := os.WriteFile(media, []byte("ordinary or damaged conditioned bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			sidecar := strings.TrimSuffix(media, filepath.Ext(media)) + ".info.json"
			if err := os.WriteFile(sidecar, []byte(tc.sidecar), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, state := filler.ReadSidecarTagsState(media); state != tc.wantState {
				t.Fatalf("sidecar state = %v, want %v", state, tc.wantState)
			}
			id, err := filler.ClipID(media)
			if err != nil {
				t.Fatal(err)
			}
			st := newMemStore()
			if _, err := filler.NewSyncer(realScanSource{dir}, st, testLayout(dir), time.Now, discardLog()).Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			clip, found, err := st.GetClip(context.Background(), id)
			if err != nil || !found || clip.Held != tc.wantHeld {
				t.Fatalf("rebuilt clip = %+v, found=%v, err=%v; held want %v", clip, found, err, tc.wantHeld)
			}
		})
	}
}

func TestSync_OmittedConditioningLineageMemberIsInvalidAndHeld(t *testing.T) {
	for _, omitted := range []string{"childHash", "parentHash", "intendedStartMs", "intendedEndMs"} {
		t.Run(omitted, func(t *testing.T) {
			dir := t.TempDir()
			media := filepath.Join(dir, "conditioned.mp4")
			if err := os.WriteFile(media, []byte("conditioned bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			lineage := map[string]any{
				"childHash": "reviewed-child", "parentHash": "retained-parent",
				"intendedStartMs": int64(0), "intendedEndMs": int64(30_000),
			}
			delete(lineage, omitted)
			doc := map[string]any{"loomarr": map[string]any{
				"mezzanine": mediatools.DefaultMezzanine().ID(), "conditioningLineage": lineage,
			}}
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			sidecar := strings.TrimSuffix(media, filepath.Ext(media)) + ".info.json"
			if err := os.WriteFile(sidecar, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, state := filler.ReadSidecarTagsState(media); state != filler.SidecarInvalid {
				t.Fatalf("sidecar state = %v, want invalid when %s is omitted", state, omitted)
			}
			id, err := filler.ClipID(media)
			if err != nil {
				t.Fatal(err)
			}
			st := newMemStore()
			if _, err := filler.NewSyncer(realScanSource{dir}, st, testLayout(dir), time.Now, discardLog()).Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			clip, found, err := st.GetClip(context.Background(), id)
			if err != nil || !found || !clip.Held {
				t.Fatalf("rebuilt clip = %+v, found=%v, err=%v; want held", clip, found, err)
			}
		})
	}

	t.Run("explicit zero start remains valid", func(t *testing.T) {
		dir := t.TempDir()
		media := filepath.Join(dir, "explicit-zero.mp4")
		if err := os.WriteFile(media, []byte("conditioned bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := filler.WriteSidecarTags(media, filler.SidecarTags{ConditioningLineage: &filler.ConditioningLineage{
			ChildHash: "reviewed-child", ParentHash: "retained-parent", IntendedStartMs: 0, IntendedEndMs: 30_000,
		}}, false); err != nil {
			t.Fatal(err)
		}
		if _, state := filler.ReadSidecarTagsState(media); state != filler.SidecarValid {
			t.Fatalf("sidecar state = %v, want valid for explicit zero start", state)
		}
	})
}

func TestReadSidecarTagsState_ConditioningLineageRequiresExactPrimitiveTypes(t *testing.T) {
	for _, field := range []string{"childHash", "parentHash", "intendedStartMs", "intendedEndMs"} {
		for _, mutation := range []struct {
			name  string
			value any
		}{
			{name: "null", value: nil},
			{name: "wrong scalar type", value: map[string]any{
				"childHash": 7, "parentHash": 7, "intendedStartMs": "0", "intendedEndMs": "30000",
			}[field]},
		} {
			t.Run(field+"/"+mutation.name, func(t *testing.T) {
				dir := t.TempDir()
				media := filepath.Join(dir, "conditioned.mp4")
				if err := os.WriteFile(media, []byte("conditioned bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
				lineage := map[string]any{
					"childHash": "reviewed-child", "parentHash": "retained-parent",
					"intendedStartMs": int64(0), "intendedEndMs": int64(30_000),
				}
				lineage[field] = mutation.value
				raw, err := json.Marshal(map[string]any{"loomarr": map[string]any{"conditioningLineage": lineage}})
				if err != nil {
					t.Fatal(err)
				}
				sidecar := strings.TrimSuffix(media, filepath.Ext(media)) + ".info.json"
				if err := os.WriteFile(sidecar, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, state := filler.ReadSidecarTagsState(media); state != filler.SidecarInvalid {
					t.Fatalf("sidecar state = %v, want invalid for %s %s", state, field, mutation.name)
				}
			})
		}
	}

	for _, field := range []string{"intendedStartMs", "intendedEndMs"} {
		t.Run(field+"/explicit zero is structurally present", func(t *testing.T) {
			dir := t.TempDir()
			media := filepath.Join(dir, "conditioned.mp4")
			if err := os.WriteFile(media, []byte("conditioned bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			lineage := map[string]any{
				"childHash": "reviewed-child", "parentHash": "retained-parent",
				"intendedStartMs": int64(1), "intendedEndMs": int64(30_000),
			}
			lineage[field] = int64(0)
			raw, err := json.Marshal(map[string]any{"loomarr": map[string]any{"conditioningLineage": lineage}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(strings.TrimSuffix(media, filepath.Ext(media))+".info.json", raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, state := filler.ReadSidecarTagsState(media); state != filler.SidecarValid {
				t.Fatalf("sidecar state = %v, want structurally valid explicit zero", state)
			}
		})
	}
}

func TestReadSidecarTagsState_ConditioningEvidenceHashesRequireExactStrings(t *testing.T) {
	for _, field := range []string{"beforeRewriteHash", "afterRewriteHash"} {
		for _, mutation := range []struct {
			name  string
			value any
		}{
			{name: "null", value: nil},
			{name: "wrong scalar type", value: 7},
		} {
			t.Run(field+"/"+mutation.name, func(t *testing.T) {
				dir := t.TempDir()
				media := filepath.Join(dir, "conditioned.mp4")
				if err := os.WriteFile(media, []byte("conditioned bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
				conditioning := map[string]any{
					"beforeRewriteHash": "before", "afterRewriteHash": "after",
					"beforeRewrite": map[string]any{}, "afterRewrite": map[string]any{},
					"derivedParentEdgesAfterRewrite": map[string]any{},
				}
				conditioning[field] = mutation.value
				raw, err := json.Marshal(map[string]any{"loomarr": map[string]any{
					"conditioningLineage": map[string]any{
						"childHash": "reviewed-child", "parentHash": "retained-parent",
						"intendedStartMs": int64(0), "intendedEndMs": int64(30_000),
					},
					"conditioning": conditioning,
				}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(strings.TrimSuffix(media, filepath.Ext(media))+".info.json", raw, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, state := filler.ReadSidecarTagsState(media); state != filler.SidecarInvalid {
					t.Fatalf("sidecar state = %v, want invalid for %s %s", state, field, mutation.name)
				}
			})
		}
	}
}

// ⚠ THE ordering property. Intake runs BEFORE the listing, so a file dropped in the watch folder
// is catalogued by the SAME pass that files it. Draining afterwards would leave every arrival
// waiting a full sync interval — 15 minutes by default — which reads to an operator as "I dropped
// a file in and nothing happened".
func TestSync_FilesAndCatalogsAWatchFolderArrivalInOnePass(t *testing.T) {
	clipDir := t.TempDir()
	watchDir := filepath.Join(clipDir, filler.WatchDirName)
	if err := os.MkdirAll(watchDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// A file big enough to hash, with a year in its name — the era signal that must survive the
	// rename by way of the sidecar.
	body := make([]byte, 4096)
	for i := range body {
		body[i] = byte(i % 251)
	}
	if err := os.WriteFile(filepath.Join(watchDir, "Frosted Flakes 1993.mp4"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	st := newMemStore()
	sync := filler.NewSyncer(realScanSource{clipDir}, st, testLayout(clipDir),
		func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }, discardLog())

	res, err := sync.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 {
		t.Fatalf("Added = %d, want 1 — the arrival was not catalogued by the pass that filed it", res.Added)
	}

	var got filler.StoreClip
	for _, c := range st.clips {
		got = c
	}
	// Filed under its hash, in the sharded layout, NOT at its arrival path.
	if !strings.HasSuffix(got.Path, ".mp4") || strings.Contains(got.Path, filler.WatchDirName) {
		t.Errorf("Path = %q, want a sharded hash path outside the watch folder", got.Path)
	}
	if got.ID() != got.Hash || got.Hash == "" {
		t.Errorf("identity = %q / hash = %q, want a content hash", got.ID(), got.Hash)
	}
	// ⚠ The era survived the rename. Once the file is `a3f9….mp4` the only place the year still
	// exists is the sidecar's originalName — which is exactly why intake captures it.
	if got.Era != 1993 {
		t.Errorf("Era = %d, want 1993 — the filename's era signal was lost in the rename", got.Era)
	}
	if got.Name != "Frosted Flakes 1993" {
		t.Errorf("Name = %q, want the original filename, not the hash", got.Name)
	}
	// The watch folder drained.
	left, _ := os.ReadDir(watchDir)
	for _, e := range left {
		if !e.IsDir() {
			t.Errorf("watch folder still holds %q", e.Name())
		}
	}
}

func TestSync_RepairsLegacyStaleHashPathAndPreservesTitle(t *testing.T) {
	dir := t.TempDir()
	body := make([]byte, 4096)
	for i := range body {
		body[i] = byte((i * 7) % 251)
	}
	probeFile := filepath.Join(dir, "probe.mp4")
	if err := os.WriteFile(probeFile, body, 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := filler.ClipID(probeFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(probeFile); err != nil {
		t.Fatal(err)
	}
	// The title reported in the live catalog was the former 40-character digest shape, not the
	// current 64-character content id. Both are machine names and both must migrate.
	stale := strings.Repeat("a", 40)
	oldRel := filepath.ToSlash(filler.ClipRelPath(stale, ".mp4"))
	oldFull := filepath.Join(dir, filepath.FromSlash(oldRel))
	if err := os.MkdirAll(filepath.Dir(oldFull), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldFull, body, 0o600); err != nil {
		t.Fatal(err)
	}

	source := &fakeSource{clips: []filler.RawClip{{
		ID: actual, Path: oldRel, Name: stale, Kind: filler.Commercial, DurationMs: 30_000,
	}}}
	st := newMemStore()
	st.clips[actual] = filler.StoreClip{Clip: filler.Clip{
		Hash: actual, Path: oldRel, Name: stale, Kind: filler.Commercial,
		Brand: "Coca-Cola", Era: 1992, Audience: filler.Family,
	}}
	sync := filler.NewSyncer(source, st, testLayout(dir), time.Now, discardLog())
	res, err := sync.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Repaired != 1 || res.Updated != 1 {
		t.Fatalf("sync = %+v, want one repaired catalog path", res)
	}
	wantRel := filepath.ToSlash(filler.ClipRelPath(actual, ".mp4"))
	got := st.clips[actual]
	if got.Path != wantRel || got.Name != "Coca-Cola — 1992" {
		t.Errorf("repaired row = path %q name %q, want %q and preserved title", got.Path, got.Name, wantRel)
	}
	if _, err := os.Stat(oldFull); !os.IsNotExist(err) {
		t.Errorf("stale media still exists: %v", err)
	}
	newFull := filepath.Join(dir, filepath.FromSlash(wantRel))
	if id, err := filler.ClipID(newFull); err != nil || id != actual {
		t.Fatalf("canonical media identity = %q err=%v, want %q", id, err, actual)
	}
	tags, ok := filler.ReadSidecarTags(newFull)
	if !ok || tags.OriginalName != "Coca-Cola — 1992" {
		t.Errorf("repaired sidecar = %+v ok=%v, want preserved display title", tags, ok)
	}
}

func TestSync_RepairsOpaqueTitleAtCanonicalPathWithoutInventingMetadata(t *testing.T) {
	dir := t.TempDir()
	body := []byte("a canonical clip whose source name was lost")
	probeFile := filepath.Join(dir, "probe.mp4")
	if err := os.WriteFile(probeFile, body, 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := filler.ClipID(probeFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(probeFile); err != nil {
		t.Fatal(err)
	}
	rel := filepath.ToSlash(filler.ClipRelPath(actual, ".mp4"))
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, body, 0o600); err != nil {
		t.Fatal(err)
	}
	legacyTitle := "299bceca4e5635c05ea454255105152f0b999119"
	source := &fakeSource{clips: []filler.RawClip{{
		ID: actual, Path: rel, Name: legacyTitle, Kind: filler.Commercial, DurationMs: 30_000,
	}}}
	st := newMemStore()
	sync := filler.NewSyncer(source, st, testLayout(dir), time.Now, discardLog())
	res, err := sync.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Repaired != 1 || res.Added != 1 {
		t.Fatalf("sync = %+v, want one repaired new clip", res)
	}
	if got := st.clips[actual].Name; got != "Untitled commercial" {
		t.Errorf("name = %q, want neutral display name", got)
	}
	tags, ok := filler.ReadSidecarTags(full)
	if !ok || tags.OriginalName != "Untitled commercial" {
		t.Errorf("sidecar = %+v ok=%v, want durable neutral display name", tags, ok)
	}
}

// A hand-dropped clip is FILED, not held (§10 V38c). Intake writes no `fetchedBy` for it, so the
// sync's held/filed fork must let it straight into the catalog — holding a file the operator
// placed themselves would mean it sits invisible until approved.
func TestSync_AWatchFolderDropIsFiledNotHeld(t *testing.T) {
	clipDir := t.TempDir()
	watchDir := filepath.Join(clipDir, filler.WatchDirName)
	if err := os.MkdirAll(watchDir, 0o750); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 2048)
	for i := range body {
		body[i] = byte(i % 97)
	}
	if err := os.WriteFile(filepath.Join(watchDir, "dropped.mp4"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	st := newMemStore()
	sync := filler.NewSyncer(realScanSource{clipDir}, st, testLayout(clipDir),
		func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }, discardLog())
	if _, err := sync.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, c := range st.clips {
		if c.Held {
			t.Error("a hand-dropped clip was HELD — it would sit invisible until approved, " +
				"which is the ceremony §7 warns teaches people to click through gates")
		}
	}
}
