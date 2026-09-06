package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/channels"
	"github.com/loomarr/loomarr/internal/clipfetch"
	"github.com/loomarr/loomarr/internal/events"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/mediatools"
	"github.com/loomarr/loomarr/internal/programmer"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/taxonomy"
)

// fillerSourceAdapter bridges the optional Tunarr annotation slice used by
// filler.DirSource (§10): it registers FILLER_DIR and joins Tunarr program uuids
// onto Loomarr's locally-scanned, content-hash-identified clips.
type fillerSourceAdapter struct {
	prog       tunarrFillerClient
	configured func() bool
}

// tunarrFillerClient is the exact programmer slice needed to annotate Loomarr's local
// filler catalog with optional Tunarr program ids. Keeping the seam narrow lets the adapter's
// live-enable behavior be tested without starting a network service.
type tunarrFillerClient interface {
	EnsureLocalFillerSource(ctx context.Context, dir string) (programmer.EnsureLocalSourceResult, error)
	ListLocalFillerClipsAll(ctx context.Context) ([]programmer.LocalClip, error)
}

// available is resolved per call so an internal-only process can gain Tunarr filler
// annotation after the connection is saved, without turning an empty Tunarr URL into a
// warning on every ordinary local scan. A nil resolver preserves the adapter's historical
// always-on behavior for direct construction in tests.
func (a fillerSourceAdapter) available() bool {
	return a.configured == nil || a.configured()
}

func (a fillerSourceAdapter) EnsureLocalSource(ctx context.Context, dir string) error {
	if !a.available() {
		return nil
	}
	_, err := a.prog.EnsureLocalFillerSource(ctx, dir)
	return err
}

// LocalClipIDsByName maps a clip's file name to the Tunarr program uuid Tunarr assigned it.
//
// Name is the only join key Tunarr's scan reports back, so it is what we have. Imperfect: two
// clips sharing a basename in different subfolders collide and one may take the other's uuid.
// That is tolerable ONLY because the uuid stopped being identity (§9.1) — a wrong uuid degrades
// a Tunarr filler-list, it cannot corrupt the catalog or misdirect internal playout, both of
// which key on the path.
func (a fillerSourceAdapter) LocalClipIDsByName(ctx context.Context) (map[string]string, error) {
	if !a.available() {
		return map[string]string{}, nil
	}
	clips, err := a.prog.ListLocalFillerClipsAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(clips))
	for _, c := range clips {
		// Tunarr reports the display name; the scanner derives its Name the same way (base
		// filename without extension), so strip an extension here if Tunarr kept one.
		name := strings.TrimSuffix(c.Name, filepath.Ext(c.Name))
		out[name] = c.ProgramID
	}
	return out, nil
}

// fillerStoreAdapter bridges the store's clip methods → filler.Store (the sync).
type fillerStoreAdapter struct{ st store.Store }

func (a fillerStoreAdapter) UpsertClip(ctx context.Context, c filler.StoreClip) error {
	return a.st.UpsertClip(ctx, store.Clip{Clip: c.Clip, UpdatedAt: c.UpdatedAt})
}
func (a fillerStoreAdapter) GetClip(ctx context.Context, id string) (filler.StoreClip, bool, error) {
	c, err := a.st.GetClip(ctx, id)
	if err == store.ErrNotFound {
		return filler.StoreClip{}, false, nil
	}
	if err != nil {
		return filler.StoreClip{}, false, err
	}
	return filler.StoreClip{Clip: c.Clip, UpdatedAt: c.UpdatedAt}, true, nil
}
func (a fillerStoreAdapter) DeleteClipsNotIn(ctx context.Context, keep []string) (int, error) {
	return a.st.DeleteClipsNotIn(ctx, keep)
}

// fillerPipelineClipAdapter bridges the store → the CLIP-facing seams the ingest pipeline needs
// (§10 V51b): `filler.ClipStore` for the runner, plus the probe and transcode rungs' writers.
//
// ⚠ The PIPELINE table itself needs no adapter — `store.Store` satisfies `filler.PipelineStore`
// directly, because those five methods already speak `filler.ClipPipeline`. This adapter exists
// only for the clip-row translation (`store.Clip` ⇄ `filler.StoreClip`) every other filler
// adapter here performs.
type fillerPipelineClipAdapter struct{ st store.Store }

func (a fillerPipelineClipAdapter) GetClip(ctx context.Context, id string) (filler.StoreClip, bool, error) {
	return fillerStoreAdapter(a).GetClip(ctx, id)
}
func (a fillerPipelineClipAdapter) UpsertClip(ctx context.Context, c filler.StoreClip) error {
	return fillerStoreAdapter(a).UpsertClip(ctx, c)
}
func (a fillerPipelineClipAdapter) ReplaceClipIdentity(ctx context.Context, oldHash string, c filler.StoreClip) error {
	return a.st.ReplaceClipIdentity(ctx, oldHash, store.Clip{Clip: c.Clip, UpdatedAt: c.UpdatedAt})
}
func (a fillerPipelineClipAdapter) CommitConditioningPublication(ctx context.Context, publication filler.ConditioningPublication, c filler.StoreClip) error {
	return a.st.CommitConditioningPublication(ctx, publication, store.Clip{Clip: c.Clip, UpdatedAt: c.UpdatedAt})
}
func (a fillerPipelineClipAdapter) SetClipsRemoved(ctx context.Context, paths []string, at time.Time) (int, error) {
	return a.st.SetClipsRemoved(ctx, paths, at)
}
func (a fillerPipelineClipAdapter) SetClipsHeld(ctx context.Context, paths []string, held, autoFiled bool, at time.Time) (int, error) {
	return a.st.SetClipsHeld(ctx, paths, held, autoFiled, at)
}
func (a fillerPipelineClipAdapter) SetClipComposite(ctx context.Context, hash string, composite bool, at time.Time) error {
	return a.st.SetClipComposite(ctx, hash, composite, at)
}

// fillerRewindAdapter bridges the store → filler.RewindStore — the invalidation seam behind
// re-running a stage (§10 V51b).
//
// ⚠ Every method here is an EXISTING single writer, called with the "not yet" value it already
// defines. Rewind introduces no new writer of any clip column, which is what keeps the
// single-writer story the store conformance suite pins.
type fillerRewindAdapter struct{ st store.Store }

func (a fillerRewindAdapter) SetClipLanguage(ctx context.Context, path, language string, at time.Time) error {
	return a.st.SetClipLanguage(ctx, path, language, at)
}
func (a fillerRewindAdapter) SetClipTranscript(ctx context.Context, path, transcript string, at time.Time) error {
	return a.st.SetClipTranscript(ctx, path, transcript, at)
}
func (a fillerRewindAdapter) ClearClipVisionTags(ctx context.Context, path string, at time.Time) error {
	return a.st.ClearClipVisionTags(ctx, path, at)
}
func (a fillerRewindAdapter) ListSplitProposals(ctx context.Context) ([]filler.SplitProposal, error) {
	return a.st.ListSplitProposals(ctx)
}
func (a fillerRewindAdapter) DeleteSplitProposal(ctx context.Context, id string) error {
	return a.st.DeleteSplitProposal(ctx, id)
}

// fillerSweepStoreAdapter bridges the store → filler.SweepStore (§10 V54).
//
// ⚠ It exists for ONE translation: `store.SweepableProposal` → `filler.SweepableProposal`. The
// domain must not import the store (Tier 3), and the store's row type is a query result rather
// than a domain concept, so the two are deliberately separate structs with the same shape.
type fillerSweepStoreAdapter struct{ st store.Store }

func (a fillerSweepStoreAdapter) ListSweepableSplitProposals(ctx context.Context, before time.Time) ([]filler.SweepableProposal, error) {
	rows, err := a.st.ListSweepableSplitProposals(ctx, before)
	if err != nil {
		return nil, err
	}
	out := make([]filler.SweepableProposal, len(rows))
	for i, r := range rows {
		out[i] = filler.SweepableProposal{
			ProposalID: r.ProposalID, ClipHash: r.ClipHash, ClipPath: r.ClipPath, Segments: r.Segments,
		}
	}
	return out, nil
}
func (a fillerSweepStoreAdapter) DeleteSplitProposal(ctx context.Context, id string) error {
	return a.st.DeleteSplitProposal(ctx, id)
}
func (a fillerSweepStoreAdapter) MarkClipReaped(ctx context.Context, hash string, at time.Time) error {
	return a.st.MarkClipReaped(ctx, hash, at)
}
func (a fillerSweepStoreAdapter) MarkPipelineFiled(ctx context.Context, hash string, at time.Time) error {
	return a.st.MarkPipelineFiled(ctx, hash, at)
}

// fillerScanSourceAdapter bridges the store → filler.ScanSourceStore (§10 V38c).
//
// ⚠ The `Enabled && Scannable()` filter lives HERE rather than in the syncer, matching where the
// fetch path puts its `Enabled && Fetchable()` check. Both predicates are the store's domain
// knowledge about a row; re-deriving them inside the job is how the two start disagreeing about
// what a source is for.
type fillerScanSourceAdapter struct{ st store.Store }

func (a fillerScanSourceAdapter) ListScanSources(ctx context.Context) ([]filler.ScanSource, error) {
	srcs, err := a.st.ListFillerSources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]filler.ScanSource, 0, len(srcs))
	for _, s := range srcs {
		if s.Enabled && s.Scannable() {
			out = append(out, filler.ScanSource{ID: s.ID, Kind: s.Kind, URI: s.URI})
		}
	}
	return out, nil
}

// fillerLibraryAdapter bridges the media-server client → filler.LibraryLister (§10 V38c).
//
// ⚠ It resolves the operator's library NAME to the item id the API needs. The operator types
// "Commercials" because that is what their media server shows them; `ListFillerClips` needs the
// library's id. Making the operator find an id would be asking them to do a lookup Loomarr can do.
//
// ⚠ An unknown name is an EMPTY result and no error. The operator named a library their server
// does not have, which is "found nothing" — a scan that reports zero clips against a named
// library, not a failure that stops the other sources.
type fillerLibraryAdapter struct{ lib *library.Client }

func (a fillerLibraryAdapter) ListLibraryClips(ctx context.Context, name string) ([]filler.LibraryClip, error) {
	if a.lib == nil || name == "" {
		return nil, nil
	}
	lib := a.lib.Snapshot()
	id, err := lib.LibraryIDByName(ctx, name)
	if errors.Is(err, library.ErrConnectionRequired) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, nil // no such library on this server
	}
	clips, err := lib.ListFillerClips(ctx, id)
	if errors.Is(err, library.ErrConnectionRequired) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]filler.LibraryClip, 0, len(clips))
	for _, c := range clips {
		out = append(out, filler.LibraryClip{Name: c.Name, Path: c.Path})
	}
	return out, nil
}

// fillerChannelWake is the shared post-commit latency path for every non-HTTP filing operation.
// It deliberately depends on only Reconcile: pipeline code does not need the API's wider channel
// management surface merely to announce that pod eligibility changed.
type fillerChannelWake struct {
	st       store.Store
	channels interface {
		Reconcile(context.Context, string) error
	}
	log *slog.Logger
}

func (w *fillerChannelWake) Run(ctx context.Context, snapshots []filler.Clip) {
	if w == nil || w.st == nil || w.channels == nil {
		return
	}
	if targeted, ok := w.channels.(interface {
		ReconcileFillerChange(context.Context, []filler.Clip) error
	}); ok {
		if err := targeted.ReconcileFillerChange(ctx, snapshots); err != nil && w.log != nil {
			w.log.Warn("filler catalog changed but affected-channel reconcile failed; sweep will retry", "err", err)
		}
		return
	}
	all, err := w.st.ListChannels(ctx)
	if err != nil {
		if w.log != nil {
			w.log.Warn("filler catalog changed but active channels could not be listed; sweep will retry", "err", err)
		}
		return
	}
	for _, ch := range all {
		if !ch.Status.Reconcilable() {
			continue
		}
		if err := w.channels.Reconcile(ctx, ch.ID); err != nil && w.log != nil {
			w.log.Warn("filler catalog changed but channel reconcile failed; sweep will retry",
				"channel", ch.ID, "err", err)
		}
	}
}

// fillerTagStoreAdapter bridges the store → filler.TagStore (the AI-tagging job).
type fillerTagStoreAdapter struct {
	st   store.Store
	wake *fillerChannelWake
}

func (a fillerTagStoreAdapter) ListUntaggedCommercials(ctx context.Context) ([]filler.StoreClip, error) {
	clips, err := a.st.ListUntaggedCommercials(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]filler.StoreClip, len(clips))
	for i, c := range clips {
		out[i] = filler.StoreClip{Clip: c.Clip, UpdatedAt: c.UpdatedAt}
	}
	return out, nil
}
func (a fillerTagStoreAdapter) UpdateClipClassification(ctx context.Context, id string, era int, audience string, suggestedEra int, aiTagged bool, updatedAt time.Time) error {
	return a.st.UpdateClipClassification(ctx, id, era, audience, suggestedEra, aiTagged, updatedAt)
}

func (a fillerTagStoreAdapter) SetClipBrand(ctx context.Context, path, brand string, at time.Time) error {
	return a.st.SetClipBrand(ctx, path, brand, at)
}

func (a fillerTagStoreAdapter) SetClipConfidence(ctx context.Context, path string, confidence int, at time.Time) error {
	return a.st.SetClipConfidence(ctx, path, confidence, at)
}

func (a fillerTagStoreAdapter) SetClipsHeld(ctx context.Context, paths []string, held, autoFiled bool, at time.Time) (int, error) {
	snapshots := fillerClipsByPath(ctx, a.st, paths)
	updated, err := a.st.SetClipsHeld(ctx, paths, held, autoFiled, at)
	if err == nil && updated > 0 {
		a.wake.Run(ctx, snapshots)
	}
	return updated, err
}

// The taxonomy path (§10 V45a): the tagger serves the vocabulary, grounds against it, and persists
// the grounded leaf set. All three forward straight to the store — the taxonomy is store-owned.
func (a fillerTagStoreAdapter) ListTaxa(ctx context.Context) ([]taxonomy.Taxon, error) {
	return a.st.ListTaxa(ctx)
}
func (a fillerTagStoreAdapter) GetClipTags(ctx context.Context, clipHash string, leavesOnly bool) ([]string, error) {
	return a.st.GetClipTags(ctx, clipHash, leavesOnly)
}
func (a fillerTagStoreAdapter) SetClipTags(ctx context.Context, clipHash string, leaves []string) error {
	return a.st.SetClipTags(ctx, clipHash, leaves)
}

// fillerLanguageStoreAdapter bridges the store → filler.LanguageStore (the language gate, V40).
type fillerLanguageStoreAdapter struct{ st store.Store }

// ListClips is the job's work list.
//
// ⚠ `IncludeHeld` is honoured but `IncludeRemoved` is deliberately NOT set. A clip the gate already
// tombstoned must not come back round as work — it would be re-detected forever, and on the local
// backend that is ~341s of QEMU per clip per pass, spent to re-learn an answer already recorded.
func (a fillerLanguageStoreAdapter) ListClips(ctx context.Context, f filler.ClipQuery) ([]filler.StoreClip, error) {
	clips, err := a.st.ListClips(ctx, store.ClipFilter{IncludeHeld: f.IncludeHeld})
	if err != nil {
		return nil, err
	}
	out := make([]filler.StoreClip, len(clips))
	for i, c := range clips {
		out[i] = filler.StoreClip{Clip: c.Clip, UpdatedAt: c.UpdatedAt}
	}
	return out, nil
}

func (a fillerLanguageStoreAdapter) SetClipLanguage(ctx context.Context, path, language string, at time.Time) error {
	return a.st.SetClipLanguage(ctx, path, language, at)
}

func (a fillerLanguageStoreAdapter) SetClipsRemoved(ctx context.Context, paths []string, at time.Time) (int, error) {
	return a.st.SetClipsRemoved(ctx, paths, at)
}

// fillerTranscribeStoreAdapter bridges the store → filler.TranscribeStore (§10 V44).
//
// ⚠ Like the language adapter above, it lists WITH held (a held clip is a fine candidate — knowing
// what it says before a human files it is strictly more useful) but does NOT include removed clips:
// a tombstoned clip must never come back round as work to be re-transcribed at ~341s a time.
type fillerTranscribeStoreAdapter struct{ st store.Store }

func (a fillerTranscribeStoreAdapter) ListClips(ctx context.Context, f filler.ClipQuery) ([]filler.StoreClip, error) {
	clips, err := a.st.ListClips(ctx, store.ClipFilter{IncludeHeld: f.IncludeHeld})
	if err != nil {
		return nil, err
	}
	out := make([]filler.StoreClip, len(clips))
	for i, c := range clips {
		out[i] = filler.StoreClip{Clip: c.Clip, UpdatedAt: c.UpdatedAt}
	}
	return out, nil
}

func (a fillerTranscribeStoreAdapter) SetClipTranscript(ctx context.Context, path, transcript string, at time.Time) error {
	return a.st.SetClipTranscript(ctx, path, transcript, at)
}

// fillerVisionStoreAdapter bridges the store → filler.VisionStore (§10 V44). Same list polarity as
// the transcribe adapter — held candidates in, removed clips out.
type fillerVisionStoreAdapter struct{ st store.Store }

func (a fillerVisionStoreAdapter) ListClips(ctx context.Context, f filler.ClipQuery) ([]filler.StoreClip, error) {
	clips, err := a.st.ListClips(ctx, store.ClipFilter{IncludeHeld: f.IncludeHeld})
	if err != nil {
		return nil, err
	}
	out := make([]filler.StoreClip, len(clips))
	for i, c := range clips {
		out[i] = filler.StoreClip{Clip: c.Clip, UpdatedAt: c.UpdatedAt}
	}
	return out, nil
}

func (a fillerVisionStoreAdapter) ApplyClipVision(ctx context.Context, hash, path, brand, visibleText string, era, suggestedEra int, leaves []string, at time.Time) error {
	return a.st.ApplyClipVision(ctx, hash, path, brand, visibleText, era, suggestedEra, leaves, at)
}

// ListTaxa: the vision tier grounds its category against the taxonomy graph (§10 V45a).
func (a fillerVisionStoreAdapter) ListTaxa(ctx context.Context) ([]taxonomy.Taxon, error) {
	return a.st.ListTaxa(ctx)
}

// fetchStoreAdapter bridges the store → filler.FetchStore (auto-fetch, §10 V38b).
type fetchStoreAdapter struct {
	st store.Store
	// fetchEvery is the GLOBAL poll interval a source's override resolves against (§10 V38c).
	// A closure so it hot-applies — an operator changing it expects the next pass to honour it.
	fetchEvery func() time.Duration
	home       func() filler.Geography
}

func (a fetchStoreAdapter) ListFetchSources(ctx context.Context) ([]filler.FetchSource, error) {
	srcs, err := a.st.ListFillerSources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]filler.FetchSource, 0, len(srcs))
	for _, s := range srcs {
		if a.home != nil && !s.GeographicallyEligible(a.home()) {
			continue
		}
		// ⚠ The three-state override is resolved HERE, by the store's own method, and handed to
		// the fetcher already decided (§10 V38c). `FetchEvery` is the single implementation of
		// nil-vs-0-vs-N; re-deriving it in the fetcher is how the two would drift, and the way
		// they would drift is toward treating "never" as "inherit" — i.e. fetching from a source
		// the operator opted out of.
		//
		// The interval itself is not passed on: the JOB's cron decides when a pass happens, so
		// what the fetcher needs from a per-source interval is only whether it is zero.
		_, pollable := s.FetchEvery(a.fetchEvery())
		out = append(out, filler.FetchSource{
			ID: s.ID, Kind: s.Kind, URI: s.URI, Enabled: s.Enabled,
			NeverFetch: !pollable,
			MaxPerRun:  s.MaxPerRun(0),
		})
	}
	return out, nil
}

// CatalogPaths returns every clip path.
//
// ⚠ `IncludeHeld` is REQUIRED. A held clip is not in the catalog by the default filter, but it is
// very much already on disk — omitting it would make auto-fetch re-download everything sitting in
// the review queue, on every pass, forever.
func (a fetchStoreAdapter) CatalogPaths(ctx context.Context) ([]string, error) {
	clips, err := a.st.ListClips(ctx, store.ClipFilter{IncludeHeld: true, IncludeRemoved: true})
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(clips))
	for i, c := range clips {
		paths[i] = c.Path
	}
	return paths, nil
}

func (a fetchStoreAdapter) ListAcquisitionRemoteStates(ctx context.Context) (map[string]filler.ExistingRemoteState, error) {
	return a.st.ListAcquisitionRemoteStates(ctx)
}

func (a fetchStoreAdapter) MarkFetched(ctx context.Context, id string, at time.Time) error {
	return a.st.MarkFillerSourceFetched(ctx, id, at)
}

// registeredSourceEnumerator dispatches only by the registered row's explicit provider kind.
// A returned item's host is not provider policy and must never select this lane.
type registeredSourceEnumerator struct{ youtube *clipfetch.YouTubeEnumerator }

func (e registeredSourceEnumerator) Enumerate(ctx context.Context, source filler.FetchSource, limit int) ([]filler.DiscoveredRef, int, error) {
	switch source.Kind {
	case "archive":
		res, err := clipfetch.NewArchiveDownloader(false).EnumerateCollection(ctx, source.URI, limit)
		if err != nil {
			return nil, 0, err
		}
		out := make([]filler.DiscoveredRef, 0, len(res.Items))
		for _, it := range res.Items {
			out = append(out, filler.DiscoveredRef{
				ID: it.ID, URL: "https://archive.org/details/" + it.ID,
				Title: it.Title, License: it.License, ObservedYear: it.Year,
				PublishedAt: it.Date, DurationMS: it.DurationMS, Height: it.Height,
			})
		}
		return out, res.Total, nil
	case "youtube":
		if e.youtube == nil {
			return nil, 0, fmt.Errorf("youtube enumerator is unavailable")
		}
		items, total, err := e.youtube.Enumerate(ctx, source.URI, limit)
		if err != nil {
			return nil, 0, err
		}
		out := make([]filler.DiscoveredRef, len(items))
		for i, item := range items {
			out[i] = filler.DiscoveredRef{
				ID: item.ID, URL: item.URL, Title: item.Title, License: item.License,
				ObservedYear: item.ReleaseYear, PublishedAt: item.PublishedAt,
				DurationMS: item.DurationMS, Height: item.Height,
			}
		}
		return out, total, nil
	default:
		return nil, 0, fmt.Errorf("unsupported registered filler source kind %q", source.Kind)
	}
}

// clipCatalogAdapter bridges the store → filler.CatalogReader (pod assembly).
type clipCatalogAdapter struct{ st store.Store }

// clipExposureAdapter is deliberately separate from the catalog reader: pod planning can read
// actual-airing history but cannot write it. Playout owns the sole write boundary.
type clipExposureAdapter struct{ st store.Store }

func (a clipExposureAdapter) FillerExposuresByChannel(ctx context.Context, channelID string, before time.Time) (map[string]filler.Exposure, error) {
	return a.st.FillerExposuresByChannel(ctx, channelID, before)
}

// ⚠ A ZERO filter, and that is what keeps HELD clips out of every pod (§10 V38). Pod assembly,
// coverage and the filler-list builder all read through here, so the exclusion living in
// ListClips means none of them can forget it. Adding IncludeHeld to this call would put every
// unreviewed download into live channels.
func (a clipCatalogAdapter) AllClips(ctx context.Context) ([]filler.Clip, error) {
	clips, err := a.st.ListClips(ctx, store.ClipFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]filler.Clip, len(clips))
	for i, c := range clips {
		out[i] = c.Clip
	}
	return out, nil
}

// fillerSplitStoreAdapter bridges the store → filler.SplitStore (V34). The
// proposal methods pass filler.SplitProposal straight through — the store
// persists exactly that type, so there is nothing to translate.
type fillerSplitStoreAdapter struct {
	st   store.Store
	wake *fillerChannelWake
}

func (a fillerSplitStoreAdapter) GetClip(ctx context.Context, id string) (filler.StoreClip, bool, error) {
	return fillerStoreAdapter{st: a.st}.GetClip(ctx, id)
}
func (a fillerSplitStoreAdapter) ListClips(ctx context.Context) ([]filler.StoreClip, error) {
	clips, err := a.st.ListClips(ctx, store.ClipFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]filler.StoreClip, len(clips))
	for i, c := range clips {
		out[i] = filler.StoreClip{Clip: c.Clip, UpdatedAt: c.UpdatedAt}
	}
	return out, nil
}
func (a fillerSplitStoreAdapter) ListClipFingerprints(ctx context.Context, algorithm string) (map[string][]uint64, error) {
	return a.st.ListClipFingerprints(ctx, algorithm)
}
func (a fillerSplitStoreAdapter) UpsertClipFingerprint(ctx context.Context, clipHash, algorithm string, frames []uint64) error {
	return a.st.UpsertClipFingerprint(ctx, clipHash, algorithm, frames)
}
func (a fillerSplitStoreAdapter) UpsertClip(ctx context.Context, c filler.StoreClip) error {
	return a.st.UpsertClip(ctx, store.Clip{Clip: c.Clip, UpdatedAt: c.UpdatedAt})
}
func (a fillerSplitStoreAdapter) GetClipTags(ctx context.Context, clipHash string, leavesOnly bool) ([]string, error) {
	return a.st.GetClipTags(ctx, clipHash, leavesOnly)
}
func (a fillerSplitStoreAdapter) SetClipTags(ctx context.Context, clipHash string, leaves []string) error {
	return a.st.SetClipTags(ctx, clipHash, leaves)
}
func (a fillerSplitStoreAdapter) UpsertClipPipeline(ctx context.Context, row filler.ClipPipeline) error {
	return a.st.UpsertClipPipeline(ctx, row)
}
func (a fillerSplitStoreAdapter) ReplaceSplitChildren(ctx context.Context, parentHash string, keepHashes []string, at time.Time) (int, error) {
	return a.st.ReplaceSplitChildren(ctx, parentHash, keepHashes, at)
}
func (a fillerSplitStoreAdapter) DeleteClip(ctx context.Context, id string) error {
	return a.st.DeleteClip(ctx, id)
}
func (a fillerSplitStoreAdapter) SetClipComposite(ctx context.Context, hash string, composite bool, at time.Time) error {
	return a.st.SetClipComposite(ctx, hash, composite, at)
}
func (a fillerSplitStoreAdapter) SetClipsHeld(ctx context.Context, paths []string, held, autoFiled bool, at time.Time) (int, error) {
	snapshots := fillerClipsByPath(ctx, a.st, paths)
	updated, err := a.st.SetClipsHeld(ctx, paths, held, autoFiled, at)
	if err == nil && updated > 0 {
		a.wake.Run(ctx, snapshots)
	}
	return updated, err
}

func fillerClipsByPath(ctx context.Context, st store.Store, paths []string) []filler.Clip {
	out := make([]filler.Clip, 0, len(paths))
	for _, path := range paths {
		if clip, err := st.GetClipByPath(ctx, path); err == nil {
			out = append(out, clip.Clip)
		}
	}
	return out
}

// ListTaxa: split-segment classification serves + grounds against the taxonomy graph (§10 V45a).
func (a fillerSplitStoreAdapter) ListTaxa(ctx context.Context) ([]taxonomy.Taxon, error) {
	return a.st.ListTaxa(ctx)
}
func (a fillerSplitStoreAdapter) UpsertSplitProposal(ctx context.Context, p filler.SplitProposal) error {
	return a.st.UpsertSplitProposal(ctx, p)
}
func (a fillerSplitStoreAdapter) GetSplitProposal(ctx context.Context, id string) (filler.SplitProposal, error) {
	return a.st.GetSplitProposal(ctx, id)
}
func (a fillerSplitStoreAdapter) AcquireSplitProposalClaim(ctx context.Context, id, token string, at, expiresAt time.Time) (filler.SplitProposal, error) {
	return a.st.AcquireSplitProposalClaim(ctx, id, token, at, expiresAt)
}
func (a fillerSplitStoreAdapter) RenewSplitProposalClaim(ctx context.Context, id, token string, expiresAt time.Time) error {
	return a.st.RenewSplitProposalClaim(ctx, id, token, expiresAt)
}
func (a fillerSplitStoreAdapter) ReleaseSplitProposalClaim(ctx context.Context, id, token string) error {
	return a.st.ReleaseSplitProposalClaim(ctx, id, token)
}
func (a fillerSplitStoreAdapter) DeleteSplitProposal(ctx context.Context, id string) error {
	return a.st.DeleteSplitProposal(ctx, id)
}
func (a fillerSplitStoreAdapter) MarkPipelineFiled(ctx context.Context, hash string, at time.Time) error {
	return a.st.MarkPipelineFiled(ctx, hash, at)
}
func (a fillerSplitStoreAdapter) CompleteSplitConfirmation(ctx context.Context, completion filler.SplitCompletion) (int, error) {
	return a.st.CompleteSplitConfirmation(ctx, completion)
}
func (a fillerSplitStoreAdapter) CompletePartialSplitConfirmation(ctx context.Context, completion filler.SplitPartialCompletion) error {
	return a.st.CompletePartialSplitConfirmation(ctx, completion)
}

// ⚠ Translates the store's ErrNotFound into the DOMAIN's ErrProposalGone. `internal/filler` does
// not import `internal/store` (Tier 3), and the distinction is load-bearing rather than cosmetic:
// the split rung must tell "the proposal was confirmed under me" apart from a real write failure,
// because the first is a normal outcome and the second must fail the pass.
func (a fillerSplitStoreAdapter) UpdateSplitProposal(ctx context.Context, p filler.SplitProposal) error {
	err := a.st.UpdateSplitProposal(ctx, p)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: %s", filler.ErrProposalGone, p.ID)
	}
	return err
}

// ListSplitProposals is the split RUNG's "is one already waiting?" read (§10 V51b) — one read of
// the pending queue rather than an existence check per candidate. Re-detecting over a proposal an
// operator is halfway through editing is what it prevents.
func (a fillerSplitStoreAdapter) ListSplitProposals(ctx context.Context) ([]filler.SplitProposal, error) {
	return a.st.ListSplitProposals(ctx)
}

// fillerServiceAdapter bridges filler.Syncer/Tagger → api.FillerService.
type fillerServiceAdapter struct {
	syncer *filler.Syncer
	tagger *filler.Tagger
	// fetcher is nil unless the running image carries the ingest tooling (the single image
	// — §16). nil is the normal state on loomarr:latest, not a misconfiguration.
	fetcher interface {
		Run(context.Context, []clipfetch.Source) clipfetch.Result
	}
	// afterIngest closes the watch-folder → catalog → pipeline loop before a successful ingest
	// event is published (§10 V56). Optional only in narrow unit tests.
	afterIngest func(context.Context) error
	bus         *events.Bus
	log         *slog.Logger
	newID       func() string
	timeout     time.Duration
	start       interactiveOperationLauncher
	operations  interactiveOperationWriter
	// sources registers what an operator added, so the Sources tab can show where clips
	// came from. Narrow interface, not the whole store: this adapter has no other reason
	// to reach persistence.
	sources fillerSourceRegistry
	// pullPlanning is the read side of candidate-level pull composition. It is separate from
	// sources because approval history is evidence for "already queued/declined" selection.
	pullPlanning fillerPullPlanningStore
	sourceEnum   filler.SourceEnumerator
	home         func() filler.Geography
	// acquisitions is the reconnect truth for background downloads. nil is allowed only in
	// narrow tests; production always supplies the store before any job can be accepted.
	acquisitions fillerAcquisitionWriter
	readiness    fillerReadinessStore
	pool         func(context.Context) (filler.PoolReport, error)
	now          func() time.Time
	// splitter / splitClips back compilation splitting (§10, V34). nil splitter ⇒
	// no drop-folder configured ⇒ Split/ConfirmSplit answer ErrSplitUnavailable.
	splitter   *filler.Splitter
	splitClips fillerSplitStoreAdapter
	// pipeline is the ingest pipeline (§10 V51b), so the operator-triggered paths reach the same
	// machinery the cron driver does: a confirmed split enrols its segments, and an ingest nudges
	// the runner instead of leaving a fresh download until the next tick. nil on an install with
	// no drop-folder, where there is nothing to ingest.
	pipeline *filler.Pipeline
	// autoFetch supplies the live limit status rendered by /v1/filler/watch. It is the same
	// Fetcher the scheduler runs, so reporting and enforcement cannot drift.
	autoFetch *filler.Fetcher
}

var _ api.FillerRewinder = fillerServiceAdapter{}

// fillerSourceRegistry is the acquisition-side source slice. Admission policy is deliberately
// absent: this adapter registers and fetches sources but is not allowed to change their trust.
type fillerSourceRegistry interface {
	ListFillerSources(context.Context) ([]store.FillerSource, error)
	UpsertFillerSource(context.Context, store.FillerSource) error
}

type fillerAcquisitionWriter interface {
	UpsertAcquisitionRun(context.Context, filler.AcquisitionRun) error
}

type interactiveOperationWriter interface {
	UpsertInteractiveOperation(context.Context, store.InteractiveOperation) error
}

type fillerReadinessStore interface {
	PipelineOverview(context.Context, time.Time) (filler.PipelineOverview, error)
	ListAcquisitionRuns(context.Context, int, time.Time) ([]filler.AcquisitionRun, error)
	AcquisitionRepairSummary(context.Context) (filler.AcquisitionRepairSummary, error)
}

// Readiness composes existing authoritative projections and delegates prioritisation to the
// filler domain. It is intentionally available before the HTTP route so API wiring cannot tempt
// the client to rebuild the decision from lower-level endpoints.
func (a fillerServiceAdapter) Readiness(ctx context.Context) (filler.Readiness, error) {
	if a.readiness == nil || a.pool == nil {
		return filler.Readiness{}, errors.New("filler readiness is not configured")
	}
	now := time.Now().UTC()
	if a.now != nil {
		now = a.now().UTC()
	}
	fetch, err := a.FetchStatus(ctx)
	if err != nil {
		return filler.Readiness{}, err
	}
	pipeline, err := a.readiness.PipelineOverview(ctx, now)
	if err != nil {
		return filler.Readiness{}, err
	}
	pool, err := a.pool(ctx)
	if err != nil {
		return filler.Readiness{}, err
	}
	runs, err := a.readiness.ListAcquisitionRuns(ctx, 20, now)
	if err != nil {
		return filler.Readiness{}, err
	}
	repairs, err := a.readiness.AcquisitionRepairSummary(ctx)
	if err != nil {
		return filler.Readiness{}, err
	}
	return filler.ProjectReadiness(filler.ReadinessInput{
		Fetch: fetch, Pipeline: pipeline, Pool: pool, Runs: runs, Repairs: repairs,
	}), nil
}

func (a fillerServiceAdapter) FetchStatus(ctx context.Context) (filler.FetchStatus, error) {
	if a.autoFetch == nil {
		return filler.FetchStatus{}, nil
	}
	return a.autoFetch.Status(ctx)
}

func (a fillerServiceAdapter) Fetch(ctx context.Context, sourceID string) (filler.FetchResult, error) {
	if a.autoFetch == nil {
		return filler.FetchResult{}, api.ErrIngestUnavailable
	}
	if sourceID != "" {
		return a.autoFetch.RunSource(ctx, sourceID)
	}
	return a.autoFetch.Run(ctx)
}

func (a fillerServiceAdapter) Rewind(ctx context.Context, hash string, from filler.StageID, force bool) error {
	if a.pipeline == nil {
		return errors.New("filler pipeline is not configured")
	}
	return a.pipeline.Rewind(ctx, hash, from, force)
}

func (a fillerServiceAdapter) RetryFailure(ctx context.Context, hash string) error {
	if a.pipeline == nil {
		return errors.New("filler pipeline is not configured")
	}
	return a.pipeline.RetryFailure(ctx, hash)
}

func (a fillerServiceAdapter) Sync(ctx context.Context) (int, int, int, int, error) {
	res, err := a.syncer.Sync(ctx)
	return res.Total, res.Added, res.Updated, res.Pruned, err
}
func (a fillerServiceAdapter) Tag(ctx context.Context) (int, int, int, int, error) {
	if a.tagger == nil {
		return 0, 0, 0, 0, nil // AI tagging disabled (FILLER_AI_TAGGING=false)
	}
	res, err := a.tagger.Run(ctx)
	return res.Considered, res.Tagged, res.Partial, res.Skipped, err
}

// Ingest downloads clips into the drop-folder (§10). It returns a job id immediately and
// does the work in the background: a playlist can take minutes to hours, so holding the
// HTTP request open would guarantee a gateway timeout on exactly the useful cases.
// Progress rides the SSE bus as `filler_ingest` frames — the same shape the §8.1 model
// pull uses, because it is the same problem (long external process, no request to hold).
// ⚠ Downloads only — it does NOT register a source. See `rememberSources`.
func (a fillerServiceAdapter) Ingest(ctx context.Context, urls []string) (string, error) {
	return a.ingest(ctx, filler.AcquisitionManual, "", acquisitionTargets("", "", urls))
}

// IngestSource is the unattended registered-source path. It preserves source attribution through
// the downloader sidecar so the catalog can apply and audit the correct admission policy.
func (a fillerServiceAdapter) IngestSource(ctx context.Context, sourceID, sourceKind string, urls []string) (string, error) {
	return a.ingest(ctx, filler.AcquisitionSource, "", acquisitionTargets(sourceID, sourceKind, urls))
}

func (a fillerServiceAdapter) IngestSourceItems(ctx context.Context, sourceID, sourceKind string, items []filler.DiscoveredRef) (string, error) {
	targets := make([]filler.AcquisitionTarget, 0, len(items))
	for _, item := range items {
		targets = append(targets, filler.AcquisitionTarget{
			SourceID: sourceID, RemoteID: item.ID, Kind: sourceKind, URL: item.URL,
		})
	}
	return a.ingest(ctx, filler.AcquisitionSource, "", targets)
}

// IngestAsked downloads AND remembers the target, for the one path where an operator named it.
func (a fillerServiceAdapter) IngestAsked(ctx context.Context, urls []string) (string, error) {
	a.rememberSources(ctx, urls)
	return a.ingest(ctx, filler.AcquisitionManual, "", acquisitionTargets("", "", urls))
}

// IngestPull preserves one approval identity across a plan that may contain several registered
// sources. The approval route will use this seam once the shared API surface is available.
func (a fillerServiceAdapter) IngestPull(
	ctx context.Context,
	pullID string,
	targets []filler.AcquisitionTarget,
) (string, error) {
	return a.ingest(ctx, filler.AcquisitionPull, pullID, targets)
}

func acquisitionTargets(sourceID, sourceKind string, urls []string) []filler.AcquisitionTarget {
	targets := make([]filler.AcquisitionTarget, 0, len(urls))
	for _, url := range urls {
		targets = append(targets, filler.AcquisitionTarget{SourceID: sourceID, Kind: sourceKind, URL: url})
	}
	return targets
}

func (a fillerServiceAdapter) ingest(
	ctx context.Context,
	trigger filler.AcquisitionTrigger,
	pullID string,
	targets []filler.AcquisitionTarget,
) (string, error) {
	if a.fetcher == nil {
		return "", api.ErrIngestUnavailable
	}
	if len(targets) == 0 {
		return "", errors.New("filler acquisition requires at least one target")
	}
	if a.start == nil {
		return "", errors.New("filler acquisition lifecycle is not configured")
	}
	jobID := a.newID()
	sources := make([]clipfetch.Source, 0, len(targets))
	for _, target := range targets {
		kind := clipfetch.Kind(target.Kind)
		if kind == "" {
			// Only the deliberate one-off admin path lacks registered source policy.
			kind = clipfetch.KindForURL(target.URL)
		} else if kind != clipfetch.Archive && kind != clipfetch.YouTube {
			return "", fmt.Errorf("unsupported registered filler source kind %q", target.Kind)
		}
		sources = append(sources, clipfetch.Source{
			ID: target.SourceID, AcquisitionID: jobID,
			Kind: kind, URL: target.URL, RemoteID: target.RemoteID,
		})
	}
	sourceID := commonAcquisitionSource(targets)

	now := time.Now
	if a.now != nil {
		now = a.now
	}
	run := filler.AcquisitionRun{
		ID: jobID, Trigger: trigger, SourceID: sourceID, PullID: pullID,
		Status: filler.AcquisitionQueued, Requested: len(sources),
		StartedAt: now().UTC(), UpdatedAt: now().UTC(),
	}
	if a.acquisitions != nil {
		if err := a.acquisitions.UpsertAcquisitionRun(ctx, run); err != nil {
			return "", fmt.Errorf("queue filler acquisition: %w", err)
		}
	}
	var res clipfetch.Result
	operationTimeout := a.timeout * time.Duration(len(sources))
	err := a.start(operationTimeout, func(operationCtx context.Context) error {
		run.Status = filler.AcquisitionRunning
		run.UpdatedAt = now().UTC()
		a.persistAcquisition(operationCtx, run)
		a.publishIngest(jobID, "starting", clipfetch.Result{}, "")
		res = a.fetcher.Run(operationCtx, sources)
		run.Fetched, run.Skipped = res.Fetched, res.Skipped
		run.Failed, run.Empty = res.Failed, res.Empty
		if err := operationCtx.Err(); err != nil {
			return err
		}
		if res.Failed > 0 && res.Fetched == 0 {
			return errors.New("every source failed; check the URLs and the yt-dlp version")
		}
		if a.afterIngest != nil {
			if err := a.afterIngest(operationCtx); err != nil {
				return fmt.Errorf("downloaded files could not be catalogued; the scheduled sync will retry: %w", err)
			}
		}
		return nil
	}, func(completionCtx context.Context, runErr error) {
		if runErr != nil {
			run.Status = filler.AcquisitionError
			run.Error = runErr.Error()
		} else {
			run.Status = filler.AcquisitionSuccess
		}
		run.CompletedAt, run.UpdatedAt = now().UTC(), now().UTC()
		a.persistAcquisition(completionCtx, run)
		a.publishIngest(jobID, string(run.Status), res, run.Error)
	})
	if err != nil {
		run.Status = filler.AcquisitionError
		run.Error = err.Error()
		run.CompletedAt, run.UpdatedAt = now().UTC(), now().UTC()
		a.persistAcquisition(ctx, run)
		return "", err
	}
	return jobID, nil
}

func commonAcquisitionSource(targets []filler.AcquisitionTarget) string {
	if len(targets) == 0 {
		return ""
	}
	sourceID := targets[0].SourceID
	for _, target := range targets[1:] {
		if target.SourceID != sourceID {
			return ""
		}
	}
	return sourceID
}

func (a fillerServiceAdapter) persistAcquisition(ctx context.Context, run filler.AcquisitionRun) {
	if a.acquisitions == nil {
		return
	}
	// The run was durably queued before the goroutine started. Later snapshots are best-effort:
	// a transient store failure must not cancel downloaded bytes or bypass pipeline reconciliation.
	if err := a.acquisitions.UpsertAcquisitionRun(ctx, run); err != nil && a.log != nil {
		a.log.Error("could not persist filler acquisition state",
			"acquisition", run.ID, "status", run.Status, "err", err)
	}
}

// publishIngest emits one ingest-progress frame (§7, type=filler_ingest). Empty counts
// sources that yielded nothing without erroring — a typo'd Archive id returns 200 with no
// items, so without surfacing it the operator sees "fetched:0 failed:0" and no reason.
func (a fillerServiceAdapter) publishIngest(jobID, status string, res clipfetch.Result, errMsg string) {
	if a.bus == nil {
		return
	}
	a.bus.Publish(events.Event{
		Type: "filler_ingest",
		Payload: api.FillerIngestEvent{
			JobID: jobID, Status: status,
			Fetched: res.Fetched, Skipped: res.Skipped,
			Failed: res.Failed, Empty: res.Empty, Error: errMsg,
		},
	})
}

// Split starts compilation detection on one clip (§10, V34). It returns a job id
// immediately — a full-decode detection pass runs minutes per file — and reports
// over the SSE bus as `filler_split` frames, the same shape as Ingest above. The
// terminal frame carries the proposal id the review UI then reads back.
//
// The clip's existence is checked SYNCHRONOUSLY: "that clip is gone" is an
// answer the caller should get as a 404, not as an SSE error frame seconds later.
func (a fillerServiceAdapter) Split(ctx context.Context, clipID string) (string, error) {
	if a.splitter == nil {
		return "", api.ErrSplitUnavailable
	}
	if a.start == nil {
		return "", errors.New("filler split lifecycle is not configured")
	}
	if a.operations == nil {
		return "", errors.New("filler split operation store is not configured")
	}
	if _, found, err := a.splitClips.GetClip(ctx, clipID); err != nil {
		return "", err
	} else if !found {
		return "", store.ErrNotFound
	}
	jobID := a.newID()
	now := time.Now
	if a.now != nil {
		now = a.now
	}
	operation := store.InteractiveOperation{
		ID: jobID, Kind: store.InteractiveOperationFillerSplit, Subject: clipID,
		Status: store.InteractiveOperationQueued, StartedAt: now().UTC(), UpdatedAt: now().UTC(),
	}
	if err := a.operations.UpsertInteractiveOperation(ctx, operation); err != nil {
		return "", fmt.Errorf("queue filler split: %w", err)
	}
	var proposal *filler.SplitProposal
	err := a.start(a.timeout, func(operationCtx context.Context) error {
		operation.Status = store.InteractiveOperationRunning
		operation.UpdatedAt = now().UTC()
		a.persistInteractiveOperation(operationCtx, operation)
		a.publishSplit(jobID, clipID, "running", "", 0, "")
		p, err := a.splitter.Propose(operationCtx, clipID)
		if err != nil {
			return err
		}
		proposal = p
		// ⚠ Un-park the reel, for the same reason ConfirmSplit enrols its cuts (§10 V54a): the
		// operator-triggered path must leave the clip where the unattended one would. A reel parked
		// at `split`/`review` is claimed by nothing — `ListPipelineWork` takes `running` only — so
		// without this a re-detect writes a freshly scored proposal that no rung will ever read,
		// and the remedy §10 names for a stale proposal silently does half its job.
		//
		// ⚠ STRICTLY AFTER `Propose`. Un-parking first makes the row claimable while detection is
		// still running, and the rung would then ground the outgoing segment list or re-`Propose`
		// the same reel concurrently. A failed detection (above) returns before reaching here, so
		// the row keeps its original state.
		//
		// ⚠ The error is DROPPED, per this adapter's convention (see `registerSources` below): it
		// has no logger and reports through the event bus, whose `filler_split` frames describe
		// DETECTION — publishing "error" here would report a detection that in fact succeeded and
		// whose proposal is saved. `Requeue` logs its own outcome, and the failure mode is the
		// state the reel was already in: parked, reviewable by hand, nothing lost.
		if a.pipeline != nil {
			_, _ = a.pipeline.Requeue(operationCtx, clipID)
		}
		return nil
	}, func(completionCtx context.Context, runErr error) {
		operation.CompletedAt, operation.UpdatedAt = now().UTC(), now().UTC()
		if runErr != nil {
			operation.Status = store.InteractiveOperationError
			operation.Error = runErr.Error()
			a.persistInteractiveOperation(completionCtx, operation)
			a.publishSplit(jobID, clipID, "error", "", 0, runErr.Error())
			return
		}
		operation.Status = store.InteractiveOperationSuccess
		operation.ResultID = proposal.ID
		a.persistInteractiveOperation(completionCtx, operation)
		a.publishSplit(jobID, clipID, "success", proposal.ID, len(proposal.Segments), "")
	})
	if err != nil {
		operation.Status = store.InteractiveOperationError
		operation.Error = err.Error()
		operation.CompletedAt, operation.UpdatedAt = now().UTC(), now().UTC()
		a.persistInteractiveOperation(ctx, operation)
		return "", err
	}
	return jobID, nil
}

func (a fillerServiceAdapter) persistInteractiveOperation(ctx context.Context, operation store.InteractiveOperation) {
	if err := a.operations.UpsertInteractiveOperation(ctx, operation); err != nil && a.log != nil {
		a.log.Error("could not persist interactive operation state",
			"operation", operation.ID, "kind", operation.Kind, "status", operation.Status, "err", err)
	}
}

// ConfirmSplit commits the operator's reviewed cut list (§10, V34) — straight
// through to the splitter, whose validation errors (filler.ErrSplitValidation)
// and missing-proposal (store.ErrNotFound) the API maps to 422/404.
func (a fillerServiceAdapter) ConfirmSplit(ctx context.Context, proposalID string, segments []filler.SplitSegment) error {
	if a.splitter == nil {
		return api.ErrSplitUnavailable
	}
	// Confirm owns the complete parent/child durable batch, including terminal parent pipeline
	// filing. The adapter must not add a fallible write after the generation commits: reporting an
	// error then would invite an operator retry of an operation that already succeeded.
	_, err := a.splitter.Confirm(ctx, proposalID, segments)
	return err
}

// publishSplit emits one split-detection frame (§7, type=filler_split). The
// terminal "success" frame is what hands the review UI its proposal id.
// ⚠ The clip field is a HASH and is now named like one. Every caller already passed `clipID`;
// only the parameter and the wire key said "path", which is the same naming drift that let the
// proposal store a path in a field the lookup needed a hash for (§10 V51a).
func (a fillerServiceAdapter) publishSplit(jobID, clipHash, status, proposalID string, segments int, errMsg string) {
	if a.bus == nil {
		return
	}
	a.bus.Publish(events.Event{
		Type: "filler_split",
		Payload: api.FillerSplitEvent{
			JobID: jobID, ClipHash: clipHash, Status: status,
			ProposalID: proposalID, Segments: segments, Error: errMsg,
		},
	})
}

// podPreviewAdapter bridges filler.PodAdapter → api.PodPreviewer (§12). It exists to
// derive the selection + seed from the channel using the SAME exported helpers the
// reconciler calls, so a preview and the next reconcile of that channel produce the
// identical pool. The API stays out of that derivation entirely.
type podPreviewAdapter struct {
	store store.Store
	pods  *filler.PodAdapter
}

// Preview assembles the pool for the channel's SAVED filler selection (the GET …/pods
// path).
func (a podPreviewAdapter) Preview(ctx context.Context, channelID string) (filler.Pod, error) {
	ch, err := a.store.GetChannel(ctx, channelID)
	if err != nil {
		return filler.Pod{}, err
	}
	return a.pods.Preview(ctx, ch.ID, channels.PodSeed(ch.ID), channels.SelectionForChannel(ch))
}

// PreviewAt assembles the pod for the break starting at a specific instant — what internal
// playout airs there, and what the guide's hover card promises.
//
// The SAME call both consumers make, for the §10 one-assembler reason: if the guide computed
// a break independently, the hover card would eventually list clips that are not the ones
// playing, and nobody would be able to reproduce the discrepancy on demand.
func (a podPreviewAdapter) PreviewAt(ctx context.Context, channelID string, breakStartMs int64) (filler.Pod, error) {
	ch, err := a.store.GetChannel(ctx, channelID)
	if err != nil {
		return filler.Pod{}, err
	}
	return a.pods.PreviewAt(ctx, ch.ID, channels.PodSeedAt(ch.ID, breakStartMs), channels.SelectionForChannel(ch), time.UnixMilli(breakStartMs).UTC())
}

// Coverage reports which ladder rung this channel's breaks would draw from (V29b-api).
//
// Resolved through `SelectionForChannel`, exactly like the previews above — coverage that
// derived a channel's selection its own way could report on a different window than the pods
// it claims to describe, which is the "lying meter" the whole phase exists to prevent.
func (a podPreviewAdapter) Coverage(ctx context.Context, channelID string) (filler.CoverageReport, error) {
	ch, err := a.store.GetChannel(ctx, channelID)
	if err != nil {
		return filler.CoverageReport{}, err
	}
	return a.pods.CoverageFor(ctx, ch.ID, channels.SelectionForChannel(ch))
}

// CoverageDraft is Coverage for an UNSAVED selection (§10 V51f).
//
// ⚠ It takes the selection already resolved by the caller — `channels.SelectionFrom` at the API
// boundary — for the same reason `PreviewDraft` stopped re-applying the scope era: the era's
// inherit-vs-explicitly-any decision has exactly one writer, and a second application here could
// not tell an operator who chose "any era" from one who left the field alone.
func (a podPreviewAdapter) CoverageDraft(ctx context.Context, channelID string, sel filler.Selection) (filler.CoverageReport, error) {
	ch, err := a.store.GetChannel(ctx, channelID)
	if err != nil {
		return filler.CoverageReport{}, err
	}
	return a.pods.CoverageFor(ctx, ch.ID, sel)
}

// ClipFit reports how one clip relates to every channel's selection (§10 V35 item 1.7).
//
// ⚠ Resolved through `SelectionForChannel` per channel, exactly like Coverage above — the note
// beside a checkbox is a claim about what will play, and deriving the selection any other way
// is how a picker comes to disagree with the pods it is about to change.
//
// ⚠ EVERY channel, including paused and detached ones. Coverage and Pool exclude them because
// they report on breaks that are currently airing; this is a picker, and an operator pinning a
// clip to a paused channel is making a decision that takes effect when it resumes. Hiding the
// row would look like the channel had been deleted.
func (a podPreviewAdapter) ClipFit(ctx context.Context, clipPath string) (map[string]filler.Fit, error) {
	clip, err := a.store.GetClip(ctx, clipPath)
	if err != nil {
		return nil, err
	}
	chans, err := a.store.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]filler.Fit, len(chans))
	for _, ch := range chans {
		// `store.Clip` embeds `filler.Clip`, so this is the same value the catalog load hands
		// the assembler — not a re-derivation.
		out[ch.ID] = a.pods.FitForChannel(ch.ID, channels.SelectionForChannel(ch), clip.Clip)
	}
	return out, nil
}

// Pool reports catalog-wide filler health for the Filler page's pool strip (§10 V35).
//
// ⚠ **Every per-channel number here comes from the SAME `Coverage` call the channel page makes**,
// once per live channel. That is the whole design: an aggregate that computed its own answer
// would be a second opinion about the ladder, and the two would eventually contradict each other
// on the two pages an operator compares when something looks wrong. Aggregating the real answer
// costs one catalog load per channel and cannot drift.
//
// Paused and detached channels are excluded: their breaks are not airing, so counting them as
// "uncovered" would report a problem the operator deliberately created.
func (a podPreviewAdapter) Pool(ctx context.Context) (filler.PoolReport, error) {
	report, err := a.pods.PoolCounts(ctx)
	if err != nil {
		return filler.PoolReport{}, err
	}

	// Counted in SQL rather than recomputed here — "untagged" is the AI-tagging job's own
	// predicate (store/clips.go), and a second Go definition of the word would be free to
	// disagree with the job that acts on it.
	//
	// ⚠ **The predicate is shared; the SCOPE is not.** `ListUntaggedCommercials` sets
	// `IncludeHeld: true` on purpose, because held clips are exactly what the tagger must tag.
	// Reusing it here inherited that as a silent side effect, and every OTHER number in this
	// report counts the catalog alone — so an install with 1 filed clip and 12 held ones rendered
	// "CLIPS 1 / 12 clips still need tagging", a headline its own subtext contradicts.
	//
	// Counting held clips here is not merely inconsistent, it is unactionable: the strip's advice
	// is to go and tag them, and they are not in the Catalog to tag. Incoming carries its own
	// count for that queue.
	// Counted in SQL, not materialised: this loaded every column of every untagged row purely
	// to take len() of the slice.
	report.Untagged, err = a.store.CountClips(ctx, store.ClipFilter{UntaggedOnly: true})
	if err != nil {
		return filler.PoolReport{}, err
	}

	chans, err := a.store.ListChannels(ctx)
	if err != nil {
		return filler.PoolReport{}, err
	}
	// ⚠ ONE catalog load for every channel's coverage, not one each. `CoverageFor` loads the
	// catalog per call, so asking it per channel made this request O(channels) full-table reads —
	// 20 channels meant 20 `AllClips` on top of the two counts above. `CoverageFrom` runs the
	// identical derivation over a catalog passed in, so the numbers are still the SAME `Coverage`
	// the channel page shows (the invariant this function is built on); only the redundant reads
	// are gone.
	clips, err := a.pods.Catalog(ctx)
	if err != nil {
		return filler.PoolReport{}, err
	}
	for _, ch := range chans {
		// The same two-state DENY-LIST `recurate.eligible` uses, for the same reason: every
		// managed state qualifies (live, building, and drifted), because drifted is a transient
		// "the guide is catching up" state, not a channel that stopped airing. Only paused
		// (deliberately off the sweep) and detached (soft-deleted) are excluded.
		if ch.Status == schedule.StatusPaused || ch.Status == schedule.StatusDetached {
			continue
		}
		cov := a.pods.CoverageFrom(clips, ch.ID, channels.SelectionForChannel(ch))
		report.Channels = append(report.Channels, filler.ChannelCoverage{
			ChannelID: ch.ID, Name: ch.Name, Number: ch.Number, Report: cov,
		})
	}

	// Worst first, so a caller naming "the channel to fix" needs no sort of its own. Ties
	// break on channel number for a stable order — an unstable list would make the strip's
	// diagnosis line flicker between equally-bad channels on every poll.
	sort.SliceStable(report.Channels, func(i, j int) bool {
		li, lj := report.Channels[i].Report.Level, report.Channels[j].Report.Level
		if li != lj {
			return filler.LevelWorseThan(li, lj)
		}
		return report.Channels[i].Number < report.Channels[j].Number
	})
	return report, nil
}

// PreviewDraft assembles the pool for a DRAFT selection (the POST …/pods/preview
// sandbox) — the same seed as the saved preview (so only the selection differs), but the
// caller's unsaved selection in place of the persisted one.
//
// ⚠ **It no longer re-applies the scope era, and removing that is a correctness fix rather than
// tidying (V51f).** The rule lived in THREE places: `channels.SelectionForChannel` (applied it),
// `api.fillerSelectionToDomain` (did not), and here (applied it) — so the API's omission was
// silently rescued one layer down, which is why nothing looked broken. With the era a real range
// and "explicitly any" a reachable state, this copy becomes actively wrong: it keyed off
// `sel.Era == 0`, which cannot tell *unset* from an operator who chose ANY, so it would re-inherit
// the channel's era over the top of an explicit choice — the exact bug §10 V51f exists to fix.
// `channels.SelectionFrom` is now the single writer, applied at the API boundary where the draft
// policy (and therefore the DRAFT's scope, not the saved channel's) is known.
func (a podPreviewAdapter) PreviewDraft(ctx context.Context, channelID string, sel filler.Selection) (filler.Pod, error) {
	ch, err := a.store.GetChannel(ctx, channelID)
	if err != nil {
		return filler.Pod{}, err
	}
	return a.pods.Preview(ctx, ch.ID, channels.PodSeed(ch.ID), sel)
}

// Discover searches archive.org for clips the operator could add (§10, V33).
//
// ⚠ SYNCHRONOUS, unlike Ingest above — and the asymmetry is the point. Ingest starts a
// download that runs for minutes to hours, so it returns a job id and reports on the SSE bus.
// This is one HTTP GET to a public JSON API that answers in well under a second, so a job id
// would be ceremony the caller has to poll for a result it could already have had.
//
// ⚠ Available WITHOUT the ingest tooling. Searching is plain net/http (§10 chose it precisely
// because Archive needs no key or binary), so an operator on the default image can browse and
// see what exists — they simply cannot fetch it. Refusing the search too would hide the reason
// the fetch is unavailable behind a second, unrelated-looking wall.
func (a fillerServiceAdapter) Discover(ctx context.Context, query string, limit int) ([]api.DiscoveredClip, int, error) {
	res, err := clipfetch.NewArchiveDownloader(false).Search(ctx, query, limit)
	if err != nil {
		return nil, 0, err
	}
	items, total := discoveredClips(res)
	return items, total, nil
}

// DiscoverCollection lists one collection (§10, V17d — the starter pack). Same shape as
// Discover: listing only, no ingest tooling needed, results mapped identically. The ONLY
// difference is which archive.org query gets asked, which is why both live here rather than
// the pack growing an acquisition path of its own.
//
// ⚠ This gives clipfetch.DiscoverCollection its first non-test caller. It shipped with V33
// complete and conformant and reachable by nothing — the same built-but-unimported shape as
// `filler_sources` (V33) and the eight instances before it. Worth naming, because the
// function looking finished is exactly what made it easy to leave unwired.
func (a fillerServiceAdapter) DiscoverCollection(ctx context.Context, ref, query string, limit int) ([]api.DiscoveredClip, int, error) {
	discoverer := clipfetch.NewArchiveDownloader(false)
	var (
		res clipfetch.DiscoveryResult
		err error
	)
	if strings.TrimSpace(query) == "" {
		res, err = discoverer.DiscoverCollection(ctx, ref, limit)
	} else {
		res, err = discoverer.SearchCollection(ctx, ref, query, limit)
	}
	if err != nil {
		return nil, 0, err
	}
	items, total := discoveredClips(res)
	return items, total, nil
}

// EnrichDiscovered fills in duration + quality for specific results (§10, V35).
//
// ⚠ Only ids archive.org actually answered for appear in the map. An item it never probed is
// OMITTED rather than returned as zero — 0 renders as "0:00", which claims a clip is empty,
// and absence is what lets a client render "—" instead.
func (a fillerServiceAdapter) EnrichDiscovered(ctx context.Context, ids []string) (map[string]api.DiscoveredClipStats, error) {
	items := make([]clipfetch.DiscoveredItem, len(ids))
	for i, id := range ids {
		items[i].ID = id
	}
	clipfetch.NewArchiveDownloader(false).Enrich(ctx, items)
	return discoveredStats(items), nil
}

// discoveredStats maps enriched items to the DTO map.
//
// ⚠ Split out from EnrichDiscovered so the omit-vs-zero rule is TESTABLE: the caller builds its
// own ArchiveDownloader (there is no seam to stub), so a test through the adapter would have to
// reach archive.org. Without this the rule was covered only by the API's fake, which implements
// the rule itself — a test that proves the fake works, not the code.
func discoveredStats(items []clipfetch.DiscoveredItem) map[string]api.DiscoveredClipStats {
	out := make(map[string]api.DiscoveredClipStats, len(items))
	for _, it := range items {
		if it.DurationMS == 0 && it.Height == 0 {
			continue // learned nothing — absent, never zeroed (see DiscoveredClipStats)
		}
		out[it.ID] = api.DiscoveredClipStats{DurationMS: it.DurationMS, Height: it.Height}
	}
	return out
}

// discoveredClips maps a clipfetch result to the DTO. Shared by both discovery modes so a
// field added for one cannot silently go missing from the other.
func discoveredClips(res clipfetch.DiscoveryResult) ([]api.DiscoveredClip, int) {
	out := make([]api.DiscoveredClip, 0, len(res.Items))
	for _, it := range res.Items {
		out = append(out, api.DiscoveredClip{
			ID:         it.ID,
			Title:      it.Title,
			Year:       it.Year,
			Date:       it.Date,
			DurationMS: it.DurationMS,
			Height:     it.Height,
			// The item's own page, so an operator can look before adding. Built here
			// rather than in clipfetch because it is a presentation concern.
			URL: "https://archive.org/details/" + it.ID,
			// Archive's own thumbnail service: a stable URL pattern, no API call and no
			// per-item cost, which is why the row gets an image for free while duration and
			// quality had to be fetched.
			ThumbnailURL: "https://archive.org/services/img/" + it.ID,
		})
	}
	return out, res.Total
}

// rememberSources records an archive.org COLLECTION an operator asked for (§10, V33).
//
// ⚠ **Only from `IngestAsked`** — never auto-fetch or an approved pull, which carry the URLs of
// individual ITEMS inside a collection that is already registered. Calling it from those turned
// 5 source rows into 35, one per downloaded clip.
//
// ⚠ Best-effort: it runs before the download and a failure is dropped. Bookkeeping must never be
// able to stop a clip being fetched.
//
// ⚠ Archive only. A YouTube URL is a video, not a source with state worth remembering.
func (a fillerServiceAdapter) rememberSources(ctx context.Context, urls []string) {
	if a.sources == nil {
		return
	}
	for _, u := range urls {
		if clipfetch.KindForURL(u) != clipfetch.Archive {
			continue
		}
		id := archiveIDFrom(u)
		if id == "" {
			continue
		}
		now := time.Now
		if a.now != nil {
			now = a.now
		}
		// Upsert by id: re-adding a source an operator already has is not an error, and
		// UpsertFillerSource deliberately leaves last_fetched_at alone (see the store).
		// ⚠ The error is deliberately DROPPED, not logged: this adapter has no logger (it
		// reports through the event bus, which is for the download itself), and inventing a
		// second channel for bookkeeping noise would be worse than the silence. The download
		// proceeds either way, which is the property that matters.
		// NewFillerSource, not a struct literal: `Enabled` is a bool, so a literal that omits
		// it registers the source SWITCHED OFF (see the store).
		_ = a.sources.UpsertFillerSource(ctx, store.NewFillerSource(id, "archive", u, id, now()))
	}
}

// archiveIDFrom pulls the identifier out of an archive.org URL. Returns "" for anything that
// is not one, which the caller skips.
func archiveIDFrom(raw string) string {
	const marker = "archive.org/details/"
	i := strings.Index(raw, marker)
	if i < 0 {
		return ""
	}
	id := raw[i+len(marker):]
	if j := strings.IndexAny(id, "/?#"); j >= 0 {
		id = id[:j]
	}
	return id
}

// resolveTool turns a configured tool path into a usable one, falling back to a PATH lookup.
//
// ⚠ **The fallback is what makes §15's documented behaviour true.** §15 has always described the
// ingest binaries as "defaulted to the vendored binaries", but the registry defaults are the empty
// string and only the Docker image sets them — so a source build had ingest off even with the
// tools installed. `settings.toolRunnable` does the same lookup for the FEATURE GATE; this is the
// wiring side of the same rule, and the two disagreeing is what let the UI offer a "Fetch now"
// button that always failed.
//
// Returns "" when the tool cannot be found, which the caller reads as "this downloader is
// unavailable" rather than as an error — an install without yt-dlp is a supported configuration.
func resolveTool(configured, name string) string {
	if configured != "" {
		return configured
	}
	found, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return found
}

// orNone renders an empty tool path for a log line. "none" says the tool is absent; an empty
// string in structured output reads as a logging bug.
func orNone(path string) string {
	if path == "" {
		return "none"
	}
	return path
}

// audioAskerAdapter bridges llm.OpenAI → filler.AudioAsker (the hosted language detector, V40).
//
// ⚠ It exists only to map two identical structs across a package boundary, and that duplication is
// deliberate: `internal/filler` declares its own `AudioAsk` rather than importing `internal/llm`,
// the same dependency inversion `MediaTools` uses. The domain describes what it needs; the
// composition root supplies something that satisfies it.
type audioAskerAdapter struct{ oa *llm.OpenAI }

func (a audioAskerAdapter) AskAboutAudio(ctx context.Context, req filler.AudioAsk) (string, error) {
	resp, err := a.oa.AskAboutAudio(ctx, llm.AudioRequest{
		Model: req.Model, Prompt: req.Prompt, Audio: req.Audio,
		Format: req.Format, MaxTokens: req.MaxTokens,
	})
	return resp.Content, err
}

// hostedSTTAdapter maps the OpenAI-compatible transcription wire into mediatools' timed segment
// seam. OpenRouter uses the same base URL and bearer key as the rest of Loomarr's hosted AI work;
// only the capability-specific model differs.
type hostedSTTAdapter struct{ oa *llm.OpenAI }

func (a hostedSTTAdapter) TranscribeAudio(ctx context.Context, model, format, language string, audio []byte) ([]mediatools.TranscriptSegment, error) {
	result, err := a.oa.TranscribeAudio(ctx, llm.TranscriptionRequest{
		Model: model, Audio: audio, Format: format, Language: language,
	})
	if err != nil {
		return nil, err
	}
	out := make([]mediatools.TranscriptSegment, 0, len(result.Segments))
	for _, seg := range result.Segments {
		out = append(out, mediatools.TranscriptSegment{
			StartMs: seg.StartMs, EndMs: seg.EndMs, Text: seg.Text,
		})
	}
	return out, nil
}

// (`fillerSplitRunStoreAdapter` bridged the scheduled split job's store until V51b retired it. Its
// candidate-list half is gone with the sweep — the split RUNG acts on the `is_composite` mark the
// probe rung sets, so nothing lists the catalog looking for long files any more. Its
// pending-proposal read moved onto `fillerSplitStoreAdapter`, which the rung already used.)
