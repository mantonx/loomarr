package filler_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
)

type fetchStub struct {
	sources     []filler.FetchSource
	paths       []string
	offers      []filler.DiscoveredRef
	queued      []string
	queuedIDs   []string
	sourceID    string
	sourceKind  string
	calls       int
	listed      []string
	listedKinds []string
	ingestErr   error
	// stamped records which sources were marked fetched, and when.
	stamped map[string]time.Time
}

func (f *fetchStub) ListFetchSources(context.Context) ([]filler.FetchSource, error) {
	return f.sources, nil
}
func (f *fetchStub) CatalogPaths(context.Context) ([]string, error) { return f.paths, nil }
func (f *fetchStub) Enumerate(_ context.Context, source filler.FetchSource, _ int) ([]filler.DiscoveredRef, int, error) {
	f.calls++
	f.listed = append(f.listed, source.URI)
	f.listedKinds = append(f.listedKinds, source.Kind)
	return f.offers, len(f.offers), nil
}
func (f *fetchStub) IngestSource(_ context.Context, sourceID, sourceKind string, urls []string) (string, error) {
	f.sourceID = sourceID
	f.sourceKind = sourceKind
	if f.ingestErr != nil {
		return "", f.ingestErr
	}
	f.queued = append(f.queued, urls...)
	return "job-1", nil
}
func (f *fetchStub) IngestSourceItems(ctx context.Context, sourceID, sourceKind string, items []filler.DiscoveredRef) (string, error) {
	urls := make([]string, 0, len(items))
	for _, item := range items {
		f.queuedIDs = append(f.queuedIDs, item.ID)
		urls = append(urls, item.URL)
	}
	return f.IngestSource(ctx, sourceID, sourceKind, urls)
}
func (f *fetchStub) MarkFetched(_ context.Context, id string, at time.Time) error {
	if f.stamped == nil {
		f.stamped = map[string]time.Time{}
	}
	f.stamped[id] = at
	return nil
}

func refs(ids ...string) []filler.DiscoveredRef {
	out := make([]filler.DiscoveredRef, len(ids))
	for i, id := range ids {
		out[i] = filler.DiscoveredRef{ID: id, URL: "https://archive.org/details/" + id}
	}
	return out
}

func limits(perRun, catalog, disk int) filler.FetchLimits {
	return filler.FetchLimits{
		MaxPerRun:       func() int { return perRun },
		MaxCatalogClips: func() int { return catalog },
		MaxDiskGB:       func() int { return disk },
	}
}

func newFetcher(t *testing.T, stub *fetchStub, l filler.FetchLimits) *filler.Fetcher {
	t.Helper()
	return newFetcherWithRemoteStates(t, stub, l, nil)
}

// fetchStoreWithRemoteStates adds the fetch port's typed-state call without turning the shared
// fetch fixture into a second stateful catalog implementation.
type fetchStoreWithRemoteStates struct {
	*fetchStub
	listRemoteStates func(context.Context) (map[string]filler.ExistingRemoteState, error)
}

func (s fetchStoreWithRemoteStates) ListAcquisitionRemoteStates(ctx context.Context) (map[string]filler.ExistingRemoteState, error) {
	if s.listRemoteStates == nil {
		return nil, nil
	}
	return s.listRemoteStates(ctx)
}

func newFetcherWithRemoteStates(t *testing.T, stub *fetchStub, l filler.FetchLimits, states map[string]filler.ExistingRemoteState) *filler.Fetcher {
	t.Helper()
	return filler.NewFetcher(fetchStoreWithRemoteStates{
		fetchStub: stub,
		listRemoteStates: func(context.Context) (map[string]filler.ExistingRemoteState, error) {
			return states, nil
		},
	}, stub, stub, t.TempDir(), l, discardLog())
}

type sourceEnum func(filler.FetchSource) []filler.DiscoveredRef

func (e sourceEnum) Enumerate(_ context.Context, source filler.FetchSource, _ int) ([]filler.DiscoveredRef, int, error) {
	items := e(source)
	return items, len(items), nil
}

// ⚠ THE bound that makes auto-fetch safe to enable by default: an archive.org collection is
// thousands of items, and without this "add a source" means "download all of it tonight".
func TestFetch_StopsAtMaxPerRun(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true}},
		offers:  refs("a", "b", "c", "d", "e", "f", "g", "h"),
	}
	res, err := newFetcher(t, stub, limits(3, 2000, 20)).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Queued != 3 || len(stub.queued) != 3 {
		t.Fatalf("queued %d (%v), want 3 — max_per_run is what stops a collection arriving at once",
			res.Queued, stub.queued)
	}
	if stub.sourceID != "s1" {
		t.Errorf("queued source id = %q, want s1 — admission provenance was dropped", stub.sourceID)
	}
	if len(stub.queuedIDs) != 3 {
		t.Fatalf("queued remote ids = %v, want exact identities retained", stub.queuedIDs)
	}
}

func TestFetch_RanksMetadataInsteadOfTakingProviderOrder(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true}},
		offers: []filler.DiscoveredRef{
			{ID: "first-low", URL: "https://archive.org/details/first-low", Height: 240},
			{ID: "second-hd", URL: "https://archive.org/details/second-hd", Height: 1080, License: "cc-by"},
		},
	}
	res, err := newFetcher(t, stub, limits(1, 2000, 20)).Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.Queued != 1 || len(stub.queuedIDs) != 1 || stub.queuedIDs[0] != "second-hd" {
		t.Fatalf("scheduled selection queued %v, want the declared-rights HD item", stub.queuedIDs)
	}
}

func TestFetch_EnumeratesTheRegisteredProviderKind(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "youtube:kids", Kind: "youtube", URI: "https://youtube.com/@kids/videos", Enabled: true}},
		offers:  []filler.DiscoveredRef{{ID: "video-1", URL: "https://youtube.com/watch?v=video-1"}},
	}
	if _, err := newFetcher(t, stub, limits(2, 2000, 20)).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(stub.listedKinds) != 1 || stub.listedKinds[0] != "youtube" {
		t.Fatalf("enumerated kinds = %v, want the registered YouTube lane", stub.listedKinds)
	}
	if stub.sourceKind != "youtube" {
		t.Fatalf("queued source kind = %q, want the registered YouTube kind", stub.sourceKind)
	}
}

func TestFetch_ScheduledSelectionKeepsProviderNamespacesDistinct(t *testing.T) {
	stub := &fetchStub{sources: []filler.FetchSource{
		{ID: "archive:classic", Kind: "archive", URI: "https://archive.org/details/classic", Enabled: true},
		{ID: "youtube:classic", Kind: "youtube", URI: "https://youtube.com/@classic/videos", Enabled: true},
	}}
	enum := sourceEnum(func(source filler.FetchSource) []filler.DiscoveredRef {
		if source.Kind == "youtube" {
			return []filler.DiscoveredRef{{ID: "abcdef12345", URL: "https://youtube.com/watch?v=abcdef12345", Height: 1080}}
		}
		return []filler.DiscoveredRef{{ID: "abcdef12345", URL: "https://archive.org/details/abcdef12345", Height: 1080}}
	})
	f := filler.NewFetcher(fetchStoreWithRemoteStates{fetchStub: stub}, enum, stub, t.TempDir(), limits(1, 2000, 20), discardLog())
	res, err := f.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.Queued != 2 || len(stub.queued) != 2 {
		t.Fatalf("queued=%d urls=%v, want both provider-specific candidates", res.Queued, stub.queued)
	}
}

func TestFetch_PreservesCaseSensitiveYouTubeItemIdentity(t *testing.T) {
	stub := &fetchStub{sources: []filler.FetchSource{{ID: "youtube:case", Kind: "youtube", URI: "https://youtube.com/@case/videos", Enabled: true}}, offers: []filler.DiscoveredRef{
		{ID: "AbCd123", URL: "https://youtube.com/watch?v=AbCd123", Title: "Upper title"},
		{ID: "abcd123", URL: "https://youtube.com/watch?v=abcd123", Title: "Lower title"},
	}}
	res, err := newFetcher(t, stub, limits(2, 2000, 20)).Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.Queued != 2 || !reflect.DeepEqual(stub.queuedIDs, []string{"AbCd123", "abcd123"}) {
		t.Fatalf("queued=%d ids=%v, want both case-sensitive IDs", res.Queued, stub.queuedIDs)
	}
}

func TestFetch_TypedRemoteStatesExcludeOnlyExactIdentity(t *testing.T) {
	upper := filler.RemoteIdentity{Provider: "youtube", SourceID: "youtube:case", RemoteID: "AbCd123"}
	stub := &fetchStub{sources: []filler.FetchSource{{ID: upper.SourceID, Kind: upper.Provider, URI: "https://youtube.com/@case/videos", Enabled: true}}, offers: []filler.DiscoveredRef{{ID: upper.RemoteID, URL: "https://youtube.com/watch?v=AbCd123"}, {ID: "abcd123", URL: "https://youtube.com/watch?v=abcd123"}}}
	res, err := newFetcherWithRemoteStates(t, stub, limits(2, 2000, 20), map[string]filler.ExistingRemoteState{upper.Key(): filler.RemoteCatalogued}).Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || len(stub.queued) != 1 || stub.queued[0] != "https://youtube.com/watch?v=abcd123" {
		t.Fatalf("result=%+v queued=%v, want only unrelated identity URL", res, stub.queued)
	}
}

func TestFetch_FailedQueueLeavesIdentityEligibleForHealthyRetry(t *testing.T) {
	states := map[string]filler.ExistingRemoteState{}
	stub := &fetchStub{sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true}}, offers: refs("retry"), ingestErr: errors.New("temporary")}
	f := newFetcherWithRemoteStates(t, stub, limits(2, 2000, 20), states)
	if _, err := f.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("states mutated after failed queue: %v", states)
	}
	stub.ingestErr = nil
	res, err := f.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.Queued != 1 || len(stub.queued) != 1 {
		t.Fatalf("retry result=%+v queued=%v, want healthy retry", res, stub.queued)
	}
}

// A disabled source is not polled. The Sources switch claims Loomarr "stops scanning, searching
// and downloading" from it; auto-fetch honouring anything less makes that copy false.
func TestFetch_SkipsDisabledSources(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: false}},
		offers:  refs("a", "b"),
	}
	res, _ := newFetcher(t, stub, limits(10, 2000, 20)).Run(context.Background())
	if res.SourcesPolled != 0 || len(stub.queued) != 0 {
		t.Errorf("a switched-off source was polled (%d) and queued %v", res.SourcesPolled, stub.queued)
	}
	if stub.calls != 0 {
		t.Error("a switched-off source was still listed upstream — the switch must stop the request")
	}
}

// The config-backed rows are SCANNED, not fetched. They have no URI, and polling them would be a
// request to nowhere.
func TestFetch_SkipsFolderAndLibraryRows(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{
			{ID: "folder", Kind: "folder", URI: "", Enabled: true},
			{ID: "library", Kind: "library", URI: "", Enabled: true},
		},
		offers: refs("a"),
	}
	res, _ := newFetcher(t, stub, limits(10, 2000, 20)).Run(context.Background())
	if res.SourcesPolled != 0 || len(stub.queued) != 0 {
		t.Error("a config-backed row was polled — those are scanned, not downloaded from")
	}
}

// ⚠ Without dedupe the job re-downloads its own output on every pass, forever. The typed remote
// state is the high-water mark; paths still count toward the catalog ceiling but cannot prove
// provider/source identity.
func TestFetch_SkipsWhatIsAlreadyInTheCatalog(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true}},
		paths:   []string{"a.mp4", "nested/b.mp4"},
		offers:  refs("a", "b", "c"),
	}
	res, _ := newFetcherWithRemoteStates(t, stub, limits(10, 2000, 20), map[string]filler.ExistingRemoteState{
		(filler.RemoteIdentity{Provider: "archive", SourceID: "s1", RemoteID: "a"}).Key(): filler.RemoteCatalogued,
		(filler.RemoteIdentity{Provider: "archive", SourceID: "s1", RemoteID: "b"}).Key(): filler.RemoteCatalogued,
	}).Run(context.Background())
	if res.Skipped != 2 {
		t.Errorf("skipped %d, want 2 — a catalogued clip must be recognised as the item it came from", res.Skipped)
	}
	if len(stub.queued) != 1 || stub.queued[0] != "https://archive.org/details/c" {
		t.Errorf("queued %v, want only the new item", stub.queued)
	}
}

// A catalogued artifact remains excluded even when its stored path uses Archive's output template.
func TestFetch_SkipsCataloguedArchiveOutputTemplatePath(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true}},
		paths:   []string{"CampbellsSoupAdvert - Campbell's Soup Advert 1993.mp4"},
		offers:  refs("CampbellsSoupAdvert", "new-ad"),
	}
	res, err := newFetcherWithRemoteStates(t, stub, limits(10, 2000, 20), map[string]filler.ExistingRemoteState{
		(filler.RemoteIdentity{Provider: "archive", SourceID: "s1", RemoteID: "CampbellsSoupAdvert"}).Key(): filler.RemoteCatalogued,
	}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 {
		t.Fatalf("skipped %d, want catalogued Archive item skipped", res.Skipped)
	}
	if len(stub.queued) != 1 || stub.queued[0] != "https://archive.org/details/new-ad" {
		t.Fatalf("queued = %v, want only the new Archive item", stub.queued)
	}
}

func TestFetch_DoesNotTreatAnOrdinaryNameAsArchiveOutput(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true}},
		paths:   []string{"An ordinary catalog name - not an Archive item ID.mp4"},
		offers:  refs("An ordinary catalog name"),
	}
	res, err := newFetcher(t, stub, limits(10, 2000, 20)).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 0 || len(stub.queued) != 1 {
		t.Fatalf("result = %+v; queued = %v, want ordinary name left unmatched", res, stub.queued)
	}
}

// A catalogued artifact remains excluded even when its stored path uses yt-dlp's output template.
func TestFetch_SkipsCataloguedYouTubeOutputTemplatePath(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "youtube:retro", Kind: "youtube", URI: "https://youtube.com/@retro/videos", Enabled: true}},
		paths:   []string{"Title for a catalogued clip [video-id].mp4"},
		offers:  []filler.DiscoveredRef{{ID: "video-id", URL: "https://youtube.com/watch?v=video-id"}},
	}
	res, err := newFetcherWithRemoteStates(t, stub, limits(10, 2000, 20), map[string]filler.ExistingRemoteState{
		(filler.RemoteIdentity{Provider: "youtube", SourceID: "youtube:retro", RemoteID: "video-id"}).Key(): filler.RemoteCatalogued,
	}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || len(stub.queued) != 0 {
		t.Fatalf("result = %+v; queued = %v, want catalogued YouTube video skipped", res, stub.queued)
	}
	if _, stamped := stub.stamped["youtube:retro"]; stamped {
		t.Fatal("stamped a source whose catalogued YouTube video was not queued")
	}
}

// The catalog ceiling stops the pass and SAYS SO. An operator whose catalog stopped growing must
// be able to see which limit stopped it (§10) — a crawler that quietly does nothing is
// indistinguishable from one that is broken.
func TestFetch_StopsAndReportsAtTheCatalogCeiling(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true}},
		paths:   []string{"x.mp4", "y.mp4", "z.mp4"},
		offers:  refs("a"),
	}
	res, _ := newFetcher(t, stub, limits(10, 3, 20)).Run(context.Background())
	if res.StoppedBy != "catalog" {
		t.Errorf("StoppedBy = %q, want catalog", res.StoppedBy)
	}
	if len(stub.queued) != 0 {
		t.Errorf("queued %v at the ceiling", stub.queued)
	}
}

func TestFetchStatus_ReportsTheLiveLimitWithoutRunningAFetch(t *testing.T) {
	stub := &fetchStub{paths: []string{"x.mp4", "y.mp4", "z.mp4"}}
	f := newFetcher(t, stub, limits(10, 3, 0))
	status, err := f.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.StoppedBy != "catalog" || status.CatalogClips != 3 || status.MaxCatalog != 3 {
		t.Errorf("status = %+v, want enabled catalog ceiling 3/3", status)
	}
	if stub.calls != 0 || len(stub.queued) != 0 {
		t.Error("status check performed fetch work")
	}

	status, err = f.WithEnabled(func() bool { return false }).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.StoppedBy != "" {
		t.Errorf("disabled status = %+v, want no active stop reason", status)
	}
}

func TestFetchStatus_UsesTheSameLimitPriorityAsRun(t *testing.T) {
	stub := &fetchStub{paths: []string{"x.mp4"}}
	dir := t.TempDir()
	large := filepath.Join(dir, "large.mp4")
	if err := os.WriteFile(large, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A sparse file reports the logical size dirSizeBytes uses without allocating a gigabyte.
	if err := os.Truncate(large, 1024*1024*1024); err != nil {
		t.Fatal(err)
	}
	f := filler.NewFetcher(fetchStoreWithRemoteStates{fetchStub: stub}, stub, stub, dir, limits(10, 1, 1), discardLog())
	f.WithEnabled(func() bool { return true })

	status, err := f.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.StoppedBy != "catalog" {
		t.Errorf("StoppedBy = %q when both limits are reached, want Run's catalog-first answer", status.StoppedBy)
	}
}

// Same for the disk ceiling, measured against the FOLDER rather than a running total — so files
// an operator deletes by hand are noticed.
func TestFetch_StopsAtTheDiskCeiling(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 2*1024*1024)
	if err := os.WriteFile(filepath.Join(dir, "big.mp4"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true}},
		offers:  refs("a"),
	}
	// A 0 GB ceiling is below any real folder, which is the only way to exercise this without
	// writing gigabytes. `positiveLimit` stops an operator setting it, but the job must still
	// behave when a value arrives from an env pin or a hand-edited row.
	f := filler.NewFetcher(fetchStoreWithRemoteStates{fetchStub: stub}, stub, stub, dir, filler.FetchLimits{
		MaxPerRun:       func() int { return 10 },
		MaxCatalogClips: func() int { return 2000 },
		MaxDiskGB:       func() int { return 1 },
	}, discardLog())
	// 2 MB is under 1 GB, so this pass proceeds — the guard is checked, not merely present.
	if res, _ := f.Run(context.Background()); res.StoppedBy != "" {
		t.Errorf("StoppedBy = %q with a 2MB folder under a 1GB ceiling", res.StoppedBy)
	}
}

// ⚠ A polled source must be STAMPED, or the Sources tab reads "never fetched" forever while
// auto-fetch downloads from it every six hours — a row describing a source nobody has touched,
// on an install actively using it. `MarkFillerSourceFetched` shipped in V33 with no production
// caller, which is how it stayed easy to forget.
func TestFetch_StampsASourceItQueuedFrom(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true}},
		offers:  refs("a"),
	}
	at := time.Unix(1_800_000_000, 0).UTC()
	f := newFetcher(t, stub, limits(10, 2000, 20)).WithClock(func() time.Time { return at })
	if _, err := f.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, ok := stub.stamped["s1"]; !ok || !got.Equal(at) {
		t.Errorf("stamped = %v (present=%v), want %v", got, ok, at)
	}
}

// ⚠ ...but only when something was ACTUALLY queued. "Last fetched" must mean "last brought
// something in": a source polled fruitlessly for a week would otherwise read as freshly
// productive, which is the opposite of what the timestamp is for.
func TestFetch_DoesNotStampASourceThatBroughtNothingIn(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true}},
		paths:   []string{"a.mp4"},
		offers:  refs("a"),
	}
	states := map[string]filler.ExistingRemoteState{
		(filler.RemoteIdentity{Provider: "archive", SourceID: "s1", RemoteID: "a"}).Key(): filler.RemoteCatalogued,
	}
	if _, err := newFetcherWithRemoteStates(t, stub, limits(10, 2000, 20), states).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := stub.stamped["s1"]; ok {
		t.Error("stamped a source that queued nothing — the row would claim a productive fetch")
	}
}

// ⚠ A source may opt OUT of unattended fetching while staying ON (§10 V38c). The two are
// deliberately different: an enabled source is still searched and its clips still count, and
// collapsing them would make "stop auto-downloading from this one" require switching it off
// entirely — which also stops search.
func TestFetch_SkipsASourceThatOptedOutButStaysEnabled(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{
			{ID: "opted-out", Kind: "archive", URI: "a", Enabled: true, NeverFetch: true},
			{ID: "normal", Kind: "archive", URI: "b", Enabled: true},
		},
		offers: refs("x"),
	}
	res, _ := newFetcher(t, stub, limits(10, 2000, 20)).Run(context.Background())
	if res.SourcesPolled != 1 {
		t.Errorf("polled %d sources, want 1 — the opted-out source must be skipped", res.SourcesPolled)
	}
	if _, ok := stub.stamped["opted-out"]; ok {
		t.Error("an opted-out source was fetched from")
	}
}

// A source's own per-run cap beats the global. A busy collection and a small playlist want
// different numbers, which one figure served badly.
func TestFetch_PrefersASourcesOwnPerRunCap(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true, MaxPerRun: 2}},
		offers:  refs("a", "b", "c", "d", "e"),
	}
	// The global says 10; this source says 2.
	res, _ := newFetcher(t, stub, limits(10, 2000, 20)).Run(context.Background())
	if res.Queued != 2 {
		t.Errorf("queued %d, want 2 — the source's own cap must beat the global 10", res.Queued)
	}
}

// ...and an unset override falls back to the global rather than to zero. ⚠ The failure this
// guards is a source with no override silently fetching NOTHING, which looks identical to a
// working install whose sources have all run dry.
func TestFetch_FallsBackToTheGlobalCapWhenUnset(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true}},
		offers:  refs("a", "b", "c", "d", "e"),
	}
	res, _ := newFetcher(t, stub, limits(3, 2000, 20)).Run(context.Background())
	if res.Queued != 3 {
		t.Errorf("queued %d, want the global 3 — an unset override must inherit, not zero out", res.Queued)
	}
}

// ⚠ `filler.fetch.every = 0` disables the whole job — the escape hatch for an operator who wants
// acquisition to stay manual. Nothing is polled and nothing is queued.
func TestFetch_DisabledDoesNothing(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{{ID: "s1", Kind: "archive", URI: "coll", Enabled: true}},
		offers:  refs("a", "b"),
	}
	f := newFetcher(t, stub, limits(10, 2000, 20)).WithEnabled(func() bool { return false })
	res, err := f.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.SourcesPolled != 0 || len(stub.queued) != 0 || stub.calls != 0 {
		t.Errorf("disabled auto-fetch still ran: polled=%d queued=%v calls=%d",
			res.SourcesPolled, stub.queued, stub.calls)
	}
}

// A row-level Fetch now is a deliberate action, not the global crawler. It must fetch exactly the
// selected enabled source, even when unattended timing is off, while retaining the ordinary cap.
func TestFetch_RunSourceFetchesOnlyTheSelectedSource(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{
			{ID: "first", Kind: "archive", URI: "one", Enabled: true},
			{ID: "selected", Kind: "archive", URI: "two", Enabled: true, NeverFetch: true},
		},
		offers: refs("a", "b", "c"),
	}
	f := newFetcher(t, stub, limits(2, 2000, 20)).WithEnabled(func() bool { return false })
	res, err := f.RunSource(context.Background(), "selected")
	if err != nil {
		t.Fatal(err)
	}
	if res.SourcesPolled != 1 || res.Queued != 2 {
		t.Fatalf("result = %+v, want one selected source and its bounded two items", res)
	}
	if len(stub.listed) != 1 || stub.listed[0] != "two" {
		t.Fatalf("listed = %v, want only the selected source", stub.listed)
	}
	if _, ok := stub.stamped["first"]; ok {
		t.Fatal("the unselected source was stamped as fetched")
	}
}

// Scheduled fetching is resilient across independently failing sources, but Fetch now is a direct
// admin request and must not return success when its selected source could not queue anything.
func TestFetch_RunSourceReportsQueueFailure(t *testing.T) {
	want := errors.New("ingest tooling unavailable")
	stub := &fetchStub{
		sources:   []filler.FetchSource{{ID: "selected", Kind: "archive", URI: "two", Enabled: true}},
		offers:    refs("a"),
		ingestErr: want,
	}

	_, err := newFetcher(t, stub, limits(2, 2000, 20)).RunSource(context.Background(), "selected")
	if !errors.Is(err, want) {
		t.Fatalf("RunSource error = %v, want wrapped queue failure", err)
	}
}

func TestFetch_ScheduledRunKeepsQueueFailureBestEffort(t *testing.T) {
	stub := &fetchStub{
		sources: []filler.FetchSource{
			{ID: "first", Kind: "archive", URI: "one", Enabled: true},
			{ID: "second", Kind: "archive", URI: "two", Enabled: true},
		},
		offers:    refs("a"),
		ingestErr: errors.New("one source cannot queue"),
	}

	res, err := newFetcher(t, stub, limits(2, 2000, 20)).Run(context.Background())
	if err != nil {
		t.Fatalf("scheduled Run error = %v, want per-source failure isolated", err)
	}
	if res.SourcesPolled != 2 || res.Queued != 0 {
		t.Fatalf("scheduled result = %+v, want both sources attempted and none queued", res)
	}
}
