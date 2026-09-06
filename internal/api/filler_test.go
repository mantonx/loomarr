package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/images"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/testkit"
)

// fakeFiller records sync/tag calls.
type fakeFiller struct {
	testkit.FillerAcquisitionPlanner
	syncs, tags, fetches int
	fetchedSourceIDs     []string
	rewinds              []struct {
		hash  string
		from  filler.StageID
		force bool
	}
	retries  []string
	ingested []string
	// asked records only what came through IngestAsked — the operator-initiated path, and the
	// only one that may register a source. Separate from `ingested` so a test can tell the two
	// entry points apart; collapsing them is how the real adapter's bug hid.
	asked []string
	// unavailable simulates loomarr:latest — the image with no ingest tooling.
	unavailable bool
	// discovered records the queries Discover was asked for; discoverErr forces the
	// upstream-failure path.
	discovered    []string
	discoverLimit int
	discoverErr   error
	// collections records what DiscoverCollection was asked for, SEPARATELY from
	// `discovered`. Two fields rather than one, so a test can prove a collection request
	// never lands on the keyword search (and vice versa) — a single log would pass either
	// way round.
	collections       []string
	collectionQueries []string
	// enriched records the ids EnrichDiscovered was asked for, so a test can prove the
	// handler asks for exactly what the client sent — the cost is per id, so an extra one
	// is a real upstream request nobody wanted.
	enriched  []string
	enrichErr error
	// V34 split knobs.
	splits           []fakeSplitCall
	splitUnavailable bool
	splitNotFound    bool
	confirmNotFound  bool
	confirmInvalid   bool
	fetchStatus      filler.FetchStatus
	fetchErr         error
	readiness        filler.Readiness
	pullID           string
	pullTargets      []filler.AcquisitionTarget
}

func (f *fakeFiller) Readiness(context.Context) (filler.Readiness, error) { return f.readiness, nil }

func (f *fakeFiller) FetchStatus(context.Context) (filler.FetchStatus, error) {
	return f.fetchStatus, nil
}

func (f *fakeFiller) Fetch(_ context.Context, sourceID string) (filler.FetchResult, error) {
	f.fetches++
	f.fetchedSourceIDs = append(f.fetchedSourceIDs, sourceID)
	if f.fetchErr != nil {
		return filler.FetchResult{}, f.fetchErr
	}
	return filler.FetchResult{SourcesPolled: 1, Queued: 2}, nil
}

func (f *fakeFiller) Rewind(_ context.Context, hash string, from filler.StageID, force bool) error {
	f.rewinds = append(f.rewinds, struct {
		hash  string
		from  filler.StageID
		force bool
	}{hash: hash, from: from, force: force})
	return nil
}

func (f *fakeFiller) RetryFailure(_ context.Context, hash string) error {
	if hash == "settled" {
		return filler.ErrPipelineNotRetryable
	}
	f.retries = append(f.retries, hash)
	return nil
}

func (f *fakeFiller) Sync(context.Context) (int, int, int, int, error) {
	f.syncs++
	return 4, 2, 1, 0, nil
}
func (f *fakeFiller) Tag(context.Context) (int, int, int, int, error) {
	f.tags++
	return 3, 2, 1, 0, nil
}

// Discover records the query so a test can prove the handler passes it through, and returns a
// total LARGER than the item count — the real API pages, and a fake that returned len(items)
// would let a handler reporting the page length pass.
func (f *fakeFiller) Discover(_ context.Context, query string, limit int) ([]api.DiscoveredClip, int, error) {
	f.discovered = append(f.discovered, query)
	f.discoverLimit = limit
	if f.discoverErr != nil {
		return nil, 0, f.discoverErr
	}
	return []api.DiscoveredClip{
		{ID: "cm-1993-4", Title: "Commercials 1993", Year: 1993, URL: "https://archive.org/details/cm-1993-4"},
		{ID: "no-year-item", Title: "Untitled reel", URL: "https://archive.org/details/no-year-item"},
	}, 54, nil
}

// DiscoverCollection returns a DIFFERENT item set from Discover, deliberately: identical
// fixtures would let a handler that called the wrong method pass every assertion.
func (f *fakeFiller) DiscoverCollection(_ context.Context, ref, query string, limit int) ([]api.DiscoveredClip, int, error) {
	f.collections = append(f.collections, ref)
	f.collectionQueries = append(f.collectionQueries, query)
	f.discoverLimit = limit
	if f.discoverErr != nil {
		return nil, 0, f.discoverErr
	}
	return []api.DiscoveredClip{
		{ID: "pack-1", Title: "Starter reel", Year: 1985, URL: "https://archive.org/details/pack-1"},
	}, 667, nil
}

// EnrichDiscovered answers for SOME ids and not others, deliberately. An item archive.org has
// never probed has no duration, and a fake that answered for everything would let a handler
// (or a UI) that renders 0 as "0:00" pass — which is the whole failure this split-out route
// exists around.
func (f *fakeFiller) EnrichDiscovered(_ context.Context, ids []string) (map[string]api.DiscoveredClipStats, error) {
	f.enriched = append(f.enriched, ids...)
	if f.enrichErr != nil {
		return nil, f.enrichErr
	}
	out := map[string]api.DiscoveredClipStats{}
	for _, id := range ids {
		if id == "unprobed" {
			continue // absent, never zeroed
		}
		out[id] = api.DiscoveredClipStats{DurationMS: 91_090, Height: 960}
	}
	return out, nil
}

func (f *fakeFiller) Ingest(_ context.Context, urls []string) (string, error) {
	if f.unavailable {
		return "", api.ErrIngestUnavailable
	}
	f.ingested = append(f.ingested, urls...)
	return "job-1", nil
}

func (f *fakeFiller) IngestPull(_ context.Context, pullID string, targets []filler.AcquisitionTarget) (string, error) {
	if f.unavailable {
		return "", api.ErrIngestUnavailable
	}
	f.pullID = pullID
	f.pullTargets = append([]filler.AcquisitionTarget(nil), targets...)
	for _, target := range targets {
		f.ingested = append(f.ingested, target.URL)
	}
	return "job-1", nil
}

// ⚠ Records SEPARATELY from `Ingest`, so a test can prove which entry point a route used. The two
// differ only in whether the target is registered as a source, and the real adapter got that
// wrong for a release — a double that collapsed them could not have caught it.
func (f *fakeFiller) IngestAsked(_ context.Context, urls []string) (string, error) {
	if f.unavailable {
		return "", api.ErrIngestUnavailable
	}
	f.ingested = append(f.ingested, urls...)
	f.asked = append(f.asked, urls...)
	return "job-1", nil
}

// Split/ConfirmSplit (V34): record calls; the error knobs force the handler's
// 404/409/422 branches.
type fakeSplitCall struct {
	clipID     string
	proposalID string
	segments   int
}

func (f *fakeFiller) Split(_ context.Context, clipID string) (string, error) {
	if f.splitUnavailable {
		return "", api.ErrSplitUnavailable
	}
	if f.splitNotFound {
		return "", store.ErrNotFound
	}
	f.splits = append(f.splits, fakeSplitCall{clipID: clipID})
	return "split-job-1", nil
}

func (f *fakeFiller) ConfirmSplit(_ context.Context, proposalID string, segments []filler.SplitSegment) error {
	if f.confirmNotFound {
		return store.ErrNotFound
	}
	if f.confirmInvalid {
		return filler.ErrSplitValidation
	}
	f.splits = append(f.splits, fakeSplitCall{proposalID: proposalID, segments: len(segments)})
	return nil
}

func newFillerServer(t *testing.T) (*httptest.Server, store.Store, *fakeFiller) {
	return newFillerServerWithImages(t, nil)
}

func newFillerServerWithImages(t *testing.T, imageService api.ImageService) (*httptest.Server, store.Store, *fakeFiller) {
	return newFillerServerWithConfig(t, imageService, nil)
}

func newFillerServerWithConfig(t *testing.T, imageService api.ImageService, liveConfig func(string) string) (*httptest.Server, store.Store, *fakeFiller) {
	t.Helper()
	st := openTestStore(t, t.TempDir()+"/f.db")
	t.Cleanup(func() { _ = st.Close() })
	ff := &fakeFiller{FillerAcquisitionPlanner: testkit.FillerAcquisitionPlanner{Store: st}}
	h := api.Router(slog.New(slog.DiscardHandler), api.Options{
		Store: st,
		// ⚠ `testAuthorizer`, not `NewTokenAuthorizer(adminToken)`. The production authorizer
		// resolves admin-or-ANONYMOUS only (API_TOKEN is a break-glass admin credential, §11), so
		// with it a test that passes `memberToken` is really testing an anonymous caller — which
		// is the exact gap api_test.go records four tests once falling into. `/v1/filler/watch`
		// is member-readable and that has to be provable.
		Auth:       testAuthorizer{},
		Log:        slog.New(slog.DiscardHandler),
		Filler:     ff,
		Images:     imageService,
		LiveConfig: liveConfig,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	// ⚠ **Start with an EMPTY source registry, deliberately.** Migration 00034 seeds four default
	// sources so a real install can fetch on day one — correct there, and fatal to every assertion
	// here phrased as an absolute ("want 1", "registered sources = 1", "unconfigured"). Eleven
	// tests across three files went red the moment the migration landed, none of them wrong.
	//
	// Clearing here rather than teaching each assertion to say "+4" keeps them readable AND keeps
	// them honest when the seeded set changes again — which it will. The seeding itself is not
	// untested: `TestMigrations_SeedDefaultSources` owns exactly that, and is the ONLY test that
	// should ever depend on what 00034 inserts.
	clearSeededSources(t, st)
	return srv, st, ff
}

// The catalog's still and hover loop are both public image-service records. The animated bit and
// content-addressed source are what let the frontend defer the loop until hover without a private
// filler artwork route.
func TestListFiller_CarriesStillAndAnimatedImageServiceRecords(t *testing.T) {
	imageService := newFakeImageService()
	imageService.records["still-art"] = images.Image{
		Hash: "still-art", Role: images.RoleThumb, Width: 320, Height: 180,
		Visibility: images.VisibilityMember,
	}
	imageService.records["hover-art"] = images.Image{
		Hash: "hover-art", Role: images.RoleThumb, Width: 320, Height: 180, Animated: true,
		Visibility: images.VisibilityMember,
	}
	srv, st, _ := newFillerServerWithImages(t, imageService)
	if err := st.UpsertClip(context.Background(), store.Clip{Clip: filler.Clip{
		Hash: "clip-art", Path: "clip-art.mp4", Name: "Period commercial", Kind: filler.Commercial,
		DurationMs: 30_000, ThumbImageHash: "still-art", HoverImageHash: "hover-art",
	}}); err != nil {
		t.Fatal(err)
	}

	resp := do(t, srv, http.MethodGet, "/v1/filler", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Clips []api.ClipDTO `json:"clips"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Clips) != 1 {
		t.Fatalf("got %d clips, want 1", len(body.Clips))
	}
	still, hover := body.Clips[0].ThumbImage, body.Clips[0].HoverImage
	if still == nil || still.Hash != "still-art" || still.Src != "/v1/images/still-art/w780.jpg" {
		t.Errorf("still image = %+v, want content-addressed image record", still)
	}
	if hover == nil || hover.Hash != "hover-art" || !hover.Animated || hover.SrcSetWebP != "/v1/images/hover-art/w320.webp 320w" {
		t.Errorf("hover image = %+v, want animated image record", hover)
	}
}

// clearSeededSources drops whatever migrations pre-populated, so a test describes a state it built.
func clearSeededSources(t *testing.T, st store.Store) {
	t.Helper()
	seeded, err := st.ListFillerSources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range seeded {
		if err := st.DeleteFillerSource(context.Background(), s.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func seedClip(t *testing.T, st store.Store, id string, kind filler.Kind, era int, aud filler.Audience, cat string) {
	t.Helper()
	c := store.Clip{}
	// ⚠ Identity is the HASH since V38c (§10), not the path. These tests use the readable id as
	// both so assertions stay legible — the store does not care what a hash looks like, and the
	// 64-hex shape is enforced (and tested) in `filler.ClipPath`.
	c.Hash = id
	c.Path = id
	c.TunarrProgramID = "tun-" + id
	c.Name = "clip " + id
	c.Kind = kind
	c.Era = era
	c.Audience = aud
	c.Category = cat
	c.DurationMs = 30000
	if err := st.UpsertClip(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

func TestListFiller_FiltersAndVisibleToAll(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedClip(t, st, "c1", filler.Commercial, 1992, filler.Kids, "cereal")
	seedClip(t, st, "c2", filler.Commercial, 1994, filler.Kids, "toys")
	seedClip(t, st, "b1", filler.Bumper, 1992, filler.General, "")

	resp := do(t, srv, http.MethodGet, "/v1/filler?kind=commercial", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list → %d", resp.StatusCode)
	}
	var body struct {
		Clips []struct {
			Path, Kind string
			Tagged     bool
		}
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Clips) != 2 {
		t.Errorf("kind=commercial = %d, want 2", len(body.Clips))
	}
	for _, c := range body.Clips {
		if !c.Tagged {
			t.Errorf("fully-tagged clip %s reported untagged", c.Path)
		}
	}
}

func TestListFiller_TaxonFilterIncludesDescendantMatches(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedClip(t, st, "cereal-ad", filler.Commercial, 1992, filler.Kids, "")
	seedClip(t, st, "beer-ad", filler.Commercial, 1994, filler.General, "")
	if p := do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken,
		`{"hash":"cereal-ad","tags":["cereal"]}`); p.StatusCode != http.StatusOK {
		t.Fatalf("tag cereal-ad → %d", p.StatusCode)
	}
	if p := do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken,
		`{"hash":"beer-ad","tags":["beer"]}`); p.StatusCode != http.StatusOK {
		t.Fatalf("tag beer-ad → %d", p.StatusCode)
	}

	resp := do(t, srv, http.MethodGet, "/v1/filler?taxon=food", memberToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list taxon=food → %d, want 200", resp.StatusCode)
	}
	var body struct {
		Clips []struct{ Hash string }
		Total int
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Total != 1 || len(body.Clips) != 1 || body.Clips[0].Hash != "cereal-ad" {
		t.Errorf("taxon=food → total %d clips %+v, want only cereal-ad via its inherited food tag", body.Total, body.Clips)
	}
}

func TestListFiller_UnclassifiedMeansNoTaxonomyRows(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedClip(t, st, "classified-bumper", filler.Bumper, 0, "", "")
	seedClip(t, st, "unclassified-bumper", filler.Bumper, 0, "", "")
	if p := do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken,
		`{"hash":"classified-bumper","tags":["promo"]}`); p.StatusCode != http.StatusOK {
		t.Fatalf("tag classified-bumper → %d", p.StatusCode)
	}

	resp := do(t, srv, http.MethodGet, "/v1/filler?unclassified=true", memberToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list unclassified=true → %d, want 200", resp.StatusCode)
	}
	var body struct {
		Clips []struct{ Hash string }
		Total int
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Total != 1 || len(body.Clips) != 1 || body.Clips[0].Hash != "unclassified-bumper" {
		t.Errorf("unclassified=true → total %d clips %+v, want only the bumper with no taxonomy rows", body.Total, body.Clips)
	}
}

func TestListFiller_WithoutAxisIgnoresTagsOnOtherAxes(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedClip(t, st, "format-only", filler.Bumper, 0, "", "")
	seedClip(t, st, "product-and-format", filler.Commercial, 0, "", "")
	if p := do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken,
		`{"hash":"format-only","tags":["promo"]}`); p.StatusCode != http.StatusOK {
		t.Fatalf("tag format-only → %d", p.StatusCode)
	}
	if p := do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken,
		`{"hash":"product-and-format","tags":["cereal","promo"]}`); p.StatusCode != http.StatusOK {
		t.Fatalf("tag product-and-format → %d", p.StatusCode)
	}

	resp := do(t, srv, http.MethodGet, "/v1/filler?withoutAxis=product", memberToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list withoutAxis=product → %d, want 200", resp.StatusCode)
	}
	var body struct {
		Clips []struct{ Hash string }
		Total int
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Total != 1 || len(body.Clips) != 1 || body.Clips[0].Hash != "format-only" {
		t.Errorf("withoutAxis=product → total %d clips %+v, want format-only despite its format tag", body.Total, body.Clips)
	}
}

func TestPatchClip_RequiresAdmin(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedClip(t, st, "u1", filler.Commercial, 0, "", "")
	resp := do(t, srv, http.MethodPatch, "/v1/filler/tags", "", `{"hash":"u1","era":1994,"audience":"kids","tags":["cereal"]}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("member patch → %d, want 401", resp.StatusCode)
	}
}

func TestPatchClip_AdminEditsTags(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedClip(t, st, "u1", filler.Commercial, 0, "", "")
	// §10 V45a: the PATCH carries a taxonomy TAG SET, not a flat category. `cereal` is a product leaf
	// in the seeded forest, so it grounds; `category` in the response is the DERIVED product-leaf shadow.
	resp := do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken, `{"hash":"u1","era":1994,"audience":"kids","brand":"  Kellogg's  ","tags":["cereal"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin patch → %d", resp.StatusCode)
	}
	var body struct {
		Era          int
		Audience     string
		Category     string
		Brand        string
		Tags         []string
		AssertedTags []string
		Tagged       bool
		AITagged     bool
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Era != 1994 || body.Audience != "kids" || !body.Tagged {
		t.Errorf("patch didn't apply: %+v", body)
	}
	// The tag set persisted, and category is derived from it (cereal → its own product leaf).
	if body.Category != "cereal" {
		t.Errorf("derived category = %q, want cereal (the primary product leaf of the tag set)", body.Category)
	}
	if body.Brand != "Kellogg's" {
		t.Errorf("brand = %q, want trimmed operator correction", body.Brand)
	}
	if !slices.Contains(body.Tags, "cereal") {
		t.Errorf("tags = %v, want to contain cereal", body.Tags)
	}
	if !slices.Contains(body.Tags, "food") {
		t.Errorf("full tags = %v, want inherited food rollup", body.Tags)
	}
	if !slices.Equal(body.AssertedTags, []string{"cereal"}) {
		t.Errorf("asserted tags = %v, want only cereal (never the food rollup)", body.AssertedTags)
	}
	if body.AITagged {
		t.Error("a manual edit should clear the AI-tagged flag")
	}
	resp = do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken, `{"hash":"u1","era":1994,"audience":"kids","brand":""}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear brand → %d", resp.StatusCode)
	}
	cleared, err := st.GetClip(context.Background(), "u1")
	if err != nil || cleared.Brand != "" {
		t.Errorf("cleared brand = %q (%v), want empty", cleared.Brand, err)
	}
	// Missing clip → 404.
	resp = do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken, `{"hash":"nope","era":1990}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("patch missing → %d, want 404", resp.StatusCode)
	}
}

func TestPatchClip_AdminGroundsGeography(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedClip(t, st, "geo", filler.Commercial, 1994, "kids", "cereal")
	resp := do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken,
		`{"hash":"geo","era":1994,"audience":"kids","geography":{"scope":"local","country":"us","market":" New York ","network":"Fox","station":"WNYW","airDate":"1994-05-06"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("geography patch → %d", resp.StatusCode)
	}
	got, err := st.GetClip(context.Background(), "geo")
	if err != nil {
		t.Fatal(err)
	}
	if got.GeographicScope != filler.GeographicLocal || got.Country != "US" || got.Market != "New York" ||
		got.Network != "Fox" || got.Station != "WNYW" || got.AirDate != "1994-05-06" || got.GeoEvidence != "operator" {
		t.Fatalf("stored geography = %+v", got.Clip)
	}

	bad := do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken,
		`{"hash":"geo","era":1994,"audience":"kids","geography":{"scope":"local","country":"US"}}`)
	if bad.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("local without market → %d, want 422", bad.StatusCode)
	}
}

func TestRewindFillerClip_IsAdminOnlyAndNamesTheStage(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedClip(t, st, "stuck", filler.Commercial, 0, "", "")

	member := do(t, srv, http.MethodPost, "/v1/filler/rewind", memberToken, `{"hash":"stuck","from":"tag"}`)
	if member.StatusCode != http.StatusForbidden {
		t.Fatalf("member rewind → %d, want 403", member.StatusCode)
	}
	admin := do(t, srv, http.MethodPost, "/v1/filler/rewind", adminToken, `{"hash":"stuck","from":"tag"}`)
	if admin.StatusCode != http.StatusNoContent {
		t.Fatalf("admin rewind → %d, want 204", admin.StatusCode)
	}
	if len(ff.rewinds) != 1 || ff.rewinds[0].hash != "stuck" || ff.rewinds[0].from != filler.StageTag || ff.rewinds[0].force {
		t.Errorf("rewinds = %+v, want stuck from tag without force", ff.rewinds)
	}

	missing := do(t, srv, http.MethodPost, "/v1/filler/rewind", adminToken, `{"hash":"gone","from":"tag"}`)
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("missing clip rewind → %d, want 404", missing.StatusCode)
	}
}

func TestRetryFillerFailures_IsAdminOnlyBoundedAndServerSelected(t *testing.T) {
	srv, _, ff := newFillerServer(t)
	member := do(t, srv, http.MethodPost, "/v1/filler/retry", memberToken, `{"hashes":["failed"]}`)
	if member.StatusCode != http.StatusForbidden {
		t.Fatalf("member retry → %d, want 403", member.StatusCode)
	}
	admin := do(t, srv, http.MethodPost, "/v1/filler/retry", adminToken,
		`{"hashes":["failed","settled","failed"]}`)
	if admin.StatusCode != http.StatusOK {
		t.Fatalf("admin retry → %d, want 200", admin.StatusCode)
	}
	var body struct {
		Retried      int `json:"retried"`
		NotRetryable int `json:"notRetryable"`
	}
	if err := json.NewDecoder(admin.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Retried != 1 || body.NotRetryable != 2 {
		t.Fatalf("retry result = %+v, want one retried and two unchanged", body)
	}
	if !slices.Equal(ff.retries, []string{"failed"}) {
		t.Fatalf("retry calls = %v, want one server-selected retry", ff.retries)
	}
	hashes := make([]string, 51)
	for i := range hashes {
		hashes[i] = fmt.Sprintf("failed-%d", i)
	}
	payload, err := json.Marshal(map[string]any{"hashes": hashes})
	if err != nil {
		t.Fatal(err)
	}
	tooMany := do(t, srv, http.MethodPost, "/v1/filler/retry", adminToken, string(payload))
	if tooMany.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("51 retries → %d, want 422", tooMany.StatusCode)
	}
	if len(ff.retries) != 1 {
		t.Fatalf("oversized retry reached service: %v", ff.retries)
	}
}

// Era suggestions (§10, V34): the list surfaces an unconfirmed suggestion, and
// PATCHing era CONFIRMS it — the suggestion clears in the same write.
func TestPatchClip_ConfirmsEraSuggestion(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedClip(t, st, "u1", filler.Commercial, 0, "", "")
	if err := st.SetClipTags(context.Background(), "u1", []string{"cereal"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateClipClassification(context.Background(), "u1", 0, "kids", 1985, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The suggestion rides the DTO so the UI can ask the question.
	resp := do(t, srv, http.MethodGet, "/v1/filler", adminToken, "")
	var list struct {
		Clips []struct {
			Path         string
			Era          int
			SuggestedEra int `json:"suggestedEra"`
		}
	}
	_ = json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Clips) != 1 || list.Clips[0].SuggestedEra != 1985 || list.Clips[0].Era != 0 {
		t.Fatalf("suggestion not surfaced: %+v", list.Clips)
	}
	// Confirm: era lands, suggestion clears.
	resp = do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken, `{"hash":"u1","era":1985}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm patch → %d", resp.StatusCode)
	}
	var body struct {
		Era          int
		SuggestedEra int `json:"suggestedEra"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Era != 1985 || body.SuggestedEra != 0 {
		t.Errorf("confirm did not clear the suggestion: %+v", body)
	}
}

func TestSyncFiller_AdminOnly(t *testing.T) {
	srv, _, ff := newFillerServer(t)
	// Member → 403.
	if resp := do(t, srv, http.MethodPost, "/v1/filler/sync", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("member sync → %d, want 401", resp.StatusCode)
	}
	// Admin → runs.
	resp := do(t, srv, http.MethodPost, "/v1/filler/sync", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin sync → %d", resp.StatusCode)
	}
	var body struct{ Total, Added, Pruned int }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Total != 4 || body.Added != 2 {
		t.Errorf("sync result = %+v", body)
	}
	if ff.syncs != 1 {
		t.Errorf("sync invoked %d times, want 1", ff.syncs)
	}
}

func TestTagFiller_AdminOnly(t *testing.T) {
	srv, _, ff := newFillerServer(t)
	if resp := do(t, srv, http.MethodPost, "/v1/filler/tag", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("member tag → %d, want 401", resp.StatusCode)
	}
	resp := do(t, srv, http.MethodPost, "/v1/filler/tag", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin tag → %d", resp.StatusCode)
	}
	if ff.tags != 1 {
		t.Errorf("tag invoked %d times, want 1", ff.tags)
	}
}

// Clip search lives on /v1/filler, not /v1/search (§7.2). A clip is not a provisionable
// title, so it cannot be a federated Candidate without pushing a non-title through the
// LLM grounding path — the leak §10 exists to prevent.
func TestFiller_NameSearch(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedClip(t, st, "c1", filler.Commercial, 1992, filler.Kids, "cereal")
	seedClip(t, st, "c2", filler.Commercial, 1994, filler.Kids, "toys")

	resp := do(t, srv, http.MethodGet, "/v1/filler?q=C1", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search → %d, want 200", resp.StatusCode)
	}
	var body struct {
		Clips []struct {
			Hash string `json:"hash"`
		} `json:"clips"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	// Case-insensitive, and the result carries the clip's content HASH — the wire identity (V45a)
	// that makes a search hit addressable, which a title-shaped Candidate could not carry. (seedClip
	// sets hash==id, so the hit is "c1".)
	if len(body.Clips) != 1 || body.Clips[0].Hash != "c1" {
		t.Errorf("q=C1 → %+v, want exactly clip c1", body.Clips)
	}
}

// Kind is correctable by hand (§10): detection at sync mis-reads a trailer as a
// commercial often enough to matter, and kind drives pod ROLE, so a wrong kind produces
// structurally wrong pods rather than merely a mis-tagged clip.
func TestFiller_PatchCorrectsKind(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedClip(t, st, "t1", filler.Commercial, 1994, filler.Kids, "toys")

	resp := do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken,
		`{"hash":"t1","era":1994,"audience":"kids","tags":["toys"],"kind":"trailer"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch → %d, want 200", resp.StatusCode)
	}
	got, err := st.GetClip(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != filler.Trailer {
		t.Errorf("kind = %q, want trailer", got.Kind)
	}

	// Omitting kind must leave it alone, so a tag-only edit never rewrites it.
	resp = do(t, srv, http.MethodPatch, "/v1/filler/tags", adminToken, `{"hash":"t1","era":1995,"audience":"kids","tags":["toys"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tag-only patch → %d, want 200", resp.StatusCode)
	}
	if got, _ = st.GetClip(context.Background(), "t1"); got.Kind != filler.Trailer {
		t.Errorf("tag-only edit rewrote kind to %q, want it left as trailer", got.Kind)
	}
}

// Ingest is admin-only, returns a job id rather than blocking, and reports the
// image-variant gate as something a setting cannot fix.
func TestFiller_Ingest(t *testing.T) {
	srv, _, ff := newFillerServer(t)

	// §19 negative: downloading arbitrary URLs onto the host is admin-only.
	if resp := do(t, srv, http.MethodPost, "/v1/filler/ingest", "", `{"urls":["https://archive.org/details/x"]}`); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("member ingest → %d, want 401", resp.StatusCode)
	}

	resp := do(t, srv, http.MethodPost, "/v1/filler/ingest", adminToken, `{"urls":["https://archive.org/details/x"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ingest → %d, want 200", resp.StatusCode)
	}
	var body struct {
		JobID string `json:"jobId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	// A job id, not a result: the download outlives the request (§10).
	if body.JobID == "" {
		t.Error("no jobId returned — progress is unwatchable without one")
	}
	if len(ff.ingested) != 1 {
		t.Errorf("ingested = %v, want the one URL passed through", ff.ingested)
	}
}

// On loomarr:latest the gate is NOT a configuration problem, and the error must not
// send the operator to a Settings page that cannot help them.
func TestFiller_IngestUnavailableOnDefaultImage(t *testing.T) {
	srv, _, ff := newFillerServer(t)
	ff.unavailable = true

	resp := do(t, srv, http.MethodPost, "/v1/filler/ingest", adminToken, `{"urls":["https://youtube.com/playlist?list=x"]}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("ingest without tooling → %d, want 409", resp.StatusCode)
	}
	var problem struct {
		Detail string `json:"detail"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	// ⚠ This assertion is INVERTED from what it used to be. It required the detail to name
	// `loomarr:filler` (retired-ok) — a remedy that died when the two-tag split collapsed
	// into the single image (§16), so the test was actively holding a dead instruction in
	// place. The branch must still say something ACTIONABLE, hence both checks.
	// The dead image name (retired-ok), asserted ABSENT.
	if strings.Contains(problem.Detail, "loomarr:filler") { // retired-ok

		t.Errorf("detail = %q, but that image variant no longer exists", problem.Detail)
	}
	if !strings.Contains(problem.Detail, "INGEST_YTDLP_PATH") {
		t.Errorf("detail = %q, want it to name something the operator can actually check", problem.Detail)
	}
}

// --- discovery (§10, V33) ---

func decodeDiscover(t *testing.T, resp *http.Response) struct {
	Items []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Year  int    `json:"year"`
		URL   string `json:"url"`
	} `json:"items"`
	Total       int    `json:"total"`
	LicenceNote string `json:"licenceNote"`
} {
	t.Helper()
	var body struct {
		Items []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Year  int    `json:"year"`
			URL   string `json:"url"`
		} `json:"items"`
		Total       int    `json:"total"`
		LicenceNote string `json:"licenceNote"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func TestDiscoverFiller_ReturnsCandidatesWithTheSourcesTotal(t *testing.T) {
	srv, _, ff := newFillerServer(t)

	resp := do(t, srv, http.MethodGet, "/v1/filler/discover?q=1980s+cereal+commercial", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeDiscover(t, resp)

	if len(body.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(body.Items))
	}
	// ⚠ Total is the SOURCE's match count, not the page length. An operator judging "is this
	// search any good" needs the real number — 54 hits shown 2 at a time is a different
	// situation from 2 hits total.
	if body.Total != 54 {
		t.Errorf("total = %d, want 54 (the source's count, not len(items))", body.Total)
	}
	if body.Total == len(body.Items) {
		t.Error("total equals the page length — it is reporting the page, not the search")
	}
	// The query reaches the service rather than being dropped.
	if len(ff.discovered) != 1 || ff.discovered[0] != "1980s cereal commercial" {
		t.Errorf("service saw %v, want the typed query", ff.discovered)
	}
}

// ⚠ The licence note is sent ONCE, about the search, not per row. archive.org declares a
// licence on ~8% of items and yt-dlp on none, so a per-result chip would read "unknown" on
// nearly every row — implying a per-item check that never happened (build plan §6.3).
func TestDiscoverFiller_StatesTheLicenceCaveatOnceNotPerItem(t *testing.T) {
	srv, _, _ := newFillerServer(t)

	body := decodeDiscover(t, do(t, srv, http.MethodGet, "/v1/filler/discover?q=cereal", adminToken, ""))
	if body.LicenceNote == "" {
		t.Error("no licence note — an operator has no signal that licences are unknown")
	}
	// If a per-item licence field is ever added, this test should be revisited deliberately
	// rather than silently: assert the DTO has no such field today.
	raw, _ := json.Marshal(body.Items)
	if bytes.Contains(raw, []byte("licence")) || bytes.Contains(raw, []byte("license")) {
		t.Error("an item carries a licence field — §6.3 says it would read 'unknown' on nearly every row")
	}
}

// An item with no year is the common case (Solr omits the field), and it must round-trip as
// absent rather than as the year 0.
func TestDiscoverFiller_OmitsAnUnknownYear(t *testing.T) {
	srv, _, _ := newFillerServer(t)

	resp := do(t, srv, http.MethodGet, "/v1/filler/discover?q=cereal", adminToken, "")
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"year":0`)) {
		t.Error(`an unknown year serialised as "year":0 — it should be omitted`)
	}
}

// Upstream failures are archive.org's, not the caller's: a 502-shaped problem that names which
// side broke rather than blaming the query.
func TestDiscoverFiller_UpstreamFailureIsABadGateway(t *testing.T) {
	srv, _, ff := newFillerServer(t)
	ff.discoverErr = errors.New("dial tcp: connection refused")

	resp := do(t, srv, http.MethodGet, "/v1/filler/discover?q=cereal", adminToken, "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

// --- per-result stats, fetched on demand (V35) ---

func decodeDiscoverStats(t *testing.T, resp *http.Response) map[string]api.DiscoveredClipStats {
	t.Helper()
	var body struct {
		Stats map[string]api.DiscoveredClipStats `json:"stats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Stats
}

// The route exists because a SEARCH cannot afford these fields: one upstream call per row,
// measured at 22.6s for a page of 25. The handler must ask for exactly what it was sent.
func TestDiscoverFillerStats_AsksForExactlyTheIdsRequested(t *testing.T) {
	srv, _, ff := newFillerServer(t)

	// ⚠ REPEATED params, not `?id=a,b`. This test used to hand-write the comma form and pass,
	// while the generated client sent the repeated form and had one id bound — the two sides
	// were each self-consistent and disagreed with each other, with nothing testing the seam.
	resp := do(t, srv, http.MethodGet, "/v1/filler/discover/stats?id=a&id=b", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	stats := decodeDiscoverStats(t, resp)

	if len(ff.enriched) != 2 || ff.enriched[0] != "a" || ff.enriched[1] != "b" {
		t.Errorf("service saw %v, want exactly [a b] — each extra id is a real upstream request", ff.enriched)
	}
	if stats["a"].DurationMS == 0 || stats["a"].Height == 0 {
		t.Errorf("stats[a] = %+v, want a runtime and a height", stats["a"])
	}
}

// ⚠ The load-bearing contract. An item archive.org never probed is ABSENT from the map, never
// present-with-zeros: 0 renders as "0:00", which claims the clip is empty, and "unknown" is the
// only honest answer. The fake withholds `unprobed` precisely so this can be asserted.
func TestDiscoverFillerStats_OmitsWhatItCouldNotLearnRatherThanZeroingIt(t *testing.T) {
	srv, _, _ := newFillerServer(t)

	stats := decodeDiscoverStats(t,
		do(t, srv, http.MethodGet, "/v1/filler/discover/stats?id=known&id=unprobed", adminToken, ""))

	if _, present := stats["unprobed"]; present {
		t.Errorf("an unprobed id came back as %+v — absent is the only honest answer, since a "+
			"client renders 0 as \"0:00\" and claims the clip is empty", stats["unprobed"])
	}
	if _, present := stats["known"]; !present {
		t.Error("the probed id is missing — withholding everything is not the fix either")
	}
}

func TestDiscoverFillerStats_UpstreamFailureIsABadGateway(t *testing.T) {
	srv, _, ff := newFillerServer(t)
	ff.enrichErr = errors.New("dial tcp: connection refused")

	resp := do(t, srv, http.MethodGet, "/v1/filler/discover/stats?id=a", adminToken, "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

// ⚠ The cap is a real defence, not tidiness: each id is one outbound request, so an uncapped
// list is a way to make Loomarr hammer archive.org on someone else's behalf.
func TestDiscoverFillerStats_CapsTheIdList(t *testing.T) {
	srv, _, ff := newFillerServer(t)

	ids := make([]string, 40)
	for i := range ids {
		ids[i] = fmt.Sprintf("item-%d", i)
	}
	// ⚠ **This is the assertion that could not fire before.** With explode off, 40 repeated
	// params bound as ONE element, so `maxItems:25` saw a slice of length 1 and passed — the cap
	// existed and was unreachable. Sent as repeated params (what the client sends), the slice is
	// genuinely 40 long and the limit does its job.
	resp := do(t, srv, http.MethodGet, "/v1/filler/discover/stats?id="+strings.Join(ids, "&id="), adminToken, "")

	if resp.StatusCode == http.StatusOK {
		t.Errorf("40 ids were accepted — that is 40 upstream requests from one client call")
	}
	if len(ff.enriched) != 0 {
		t.Errorf("service saw %d ids; an over-cap request must be refused BEFORE any upstream call", len(ff.enriched))
	}
}

func TestDiscoverFillerStats_IsAdminOnly(t *testing.T) {
	srv, _, _ := newFillerServer(t)

	if resp := do(t, srv, http.MethodGet, "/v1/filler/discover/stats?id=a", memberToken, ""); resp.StatusCode == http.StatusOK {
		t.Error("a member could spend upstream requests")
	}
}

// Admin-only: it names an outbound integration and feeds the ingest path.
func TestDiscoverFiller_IsAdminOnly(t *testing.T) {
	srv, _, _ := newFillerServer(t)

	if resp := do(t, srv, http.MethodGet, "/v1/filler/discover?q=cereal", memberToken, ""); resp.StatusCode == http.StatusOK {
		t.Error("a member could search for clips to add")
	}
}

// An empty query would return archive.org's whole movies corpus ranked by nothing — refused at
// the schema, so the request never reaches the service.
func TestDiscoverFiller_RequiresAQuery(t *testing.T) {
	srv, _, ff := newFillerServer(t)

	resp := do(t, srv, http.MethodGet, "/v1/filler/discover", adminToken, "")
	if resp.StatusCode == http.StatusOK {
		t.Error("an empty query was accepted")
	}
	if len(ff.discovered) != 0 {
		t.Errorf("the service was called with %v despite an invalid request", ff.discovered)
	}
}

// --- the starter pack: discovery by collection (§10, V17d) ---

// A collection request lists that collection and NEVER reaches the keyword search. The two
// modes are separately recorded on the fake, so a handler that routed a collection into
// Search would fail here rather than pass on a shared call log.
func TestDiscoverFiller_ListsACollectionWithoutSearching(t *testing.T) {
	srv, _, ff := newFillerServer(t)

	resp := do(t, srv, http.MethodGet, "/v1/filler/discover?collection=classic_tv_commercials", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeDiscover(t, resp)

	if len(ff.collections) != 1 || ff.collections[0] != "classic_tv_commercials" {
		t.Errorf("service saw collections %v, want the requested collection", ff.collections)
	}
	if len(ff.discovered) != 0 {
		t.Errorf("a collection request also ran the keyword search (%v)", ff.discovered)
	}
	// The collection's own items, not the search fixture — proof of which method answered.
	if len(body.Items) != 1 || body.Items[0].ID != "pack-1" {
		t.Errorf("items = %+v, want the collection's items", body.Items)
	}
	if body.Total != 667 {
		t.Errorf("total = %d, want the collection's size", body.Total)
	}
	// ⚠ The caveat is about the SOURCE, not the mode. A starter pack is still archive.org
	// content, so dropping the licence note here would imply a curation that never happened.
	if body.LicenceNote == "" {
		t.Error("a collection listing dropped the licence caveat")
	}
}

// ⚠ A keyword search must NOT be answered by the collection lister. The mirror of the test
// above — together they pin that each mode reaches exactly one method.
func TestDiscoverFiller_SearchDoesNotListACollection(t *testing.T) {
	srv, _, ff := newFillerServer(t)

	do(t, srv, http.MethodGet, "/v1/filler/discover?q=cereal", adminToken, "")
	if len(ff.collections) != 0 {
		t.Errorf("a keyword search listed collections %v", ff.collections)
	}
}

// Supplying both modes means search WITHIN the named collection. This is the request made by a
// Sources-row search; treating q as global would make the row label a lie.
func TestDiscoverFiller_SearchesWithinACollection(t *testing.T) {
	srv, _, ff := newFillerServer(t)

	resp := do(t, srv, http.MethodGet, "/v1/filler/discover?q=cereal&collection=classic_tv_commercials", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !slices.Equal(ff.collections, []string{"classic_tv_commercials"}) {
		t.Errorf("collections = %v, want the source collection", ff.collections)
	}
	if !slices.Equal(ff.collectionQueries, []string{"cereal"}) {
		t.Errorf("collection queries = %v, want the typed words", ff.collectionQueries)
	}
	if len(ff.discovered) != 0 {
		t.Errorf("the scoped request also ran the global keyword search (%v)", ff.discovered)
	}
}

// --- compilation splitting (§10, V34) ---

// The propose route is admin-only (it writes a job against the catalog), 404s a
// missing clip synchronously, and 409s when there is no drop-folder to cut into.
func TestSplitFiller_Route(t *testing.T) {
	srv, _, ff := newFillerServer(t)

	// Member negative (§19): a member must not start detection. §10 V45a: the clip path is in the BODY.
	resp := do(t, srv, http.MethodPost, "/v1/filler/split", "", `{"hash":"comps/1987.mp4"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("member split → %d, want 401", resp.StatusCode)
	}

	resp = do(t, srv, http.MethodPost, "/v1/filler/split", adminToken, `{"hash":"comps/1987.mp4"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin split → %d", resp.StatusCode)
	}
	var body struct {
		JobID string `json:"jobId"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.JobID != "split-job-1" {
		t.Errorf("job id = %q, want split-job-1", body.JobID)
	}
	if len(ff.splits) != 1 || ff.splits[0].clipID == "" {
		t.Errorf("service not called with the clip id: %+v", ff.splits)
	}

	// Missing clip → 404, not an SSE error seconds later.
	ff.splitNotFound = true
	resp = do(t, srv, http.MethodPost, "/v1/filler/split", adminToken, `{"hash":"gone.mp4"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("split of a missing clip → %d, want 404", resp.StatusCode)
	}
	ff.splitNotFound = false

	// No drop-folder → 409 with the remedy named (Settings, not a different image).
	ff.splitUnavailable = true
	resp = do(t, srv, http.MethodPost, "/v1/filler/split", adminToken, `{"hash":"comps/1987.mp4"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("split with no drop-folder → %d, want 409", resp.StatusCode)
	}
}

// The proposal read comes straight from the store — the review's reconnect truth.
func TestGetFillerSplit_ReadsThePersistedProposal(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	p := filler.SplitProposal{
		ID: "sp_1", ClipHash: "hash-of-comps/1987.mp4", CreatedAt: time.Now().UTC(),
		Segments: []filler.SplitSegment{
			{Index: 0, StartMs: 0, EndMs: 30000, Name: "McDonald's", Era: 1987, Audience: filler.Kids, Category: "fast_food"},
			{Index: 1, StartMs: 30000, EndMs: 149000, Name: "part 2", SuggestedEra: 1985, DupOf: "old/ad.mp4", Unsplittable: true, Looked: true},
		},
	}
	if err := st.UpsertSplitProposal(context.Background(), p); err != nil {
		t.Fatal(err)
	}

	resp := do(t, srv, http.MethodGet, "/v1/filler/splits/sp_1", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get split → %d", resp.StatusCode)
	}
	var got filler.SplitProposal
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.ID != "sp_1" || len(got.Segments) != 2 {
		t.Fatalf("proposal = %+v", got)
	}
	// The V34 review fields must cross the wire — the UI renders from exactly these.
	s1 := got.Segments[1]
	if s1.SuggestedEra != 1985 || s1.DupOf != "old/ad.mp4" || !s1.Unsplittable || !s1.Looked {
		t.Errorf("review fields lost: %+v", s1)
	}

	resp = do(t, srv, http.MethodGet, "/v1/filler/splits/nope", adminToken, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown proposal → %d, want 404", resp.StatusCode)
	}
	if err := st.UpsertSplitProposal(context.Background(), filler.SplitProposal{
		ID: "sp_detecting", ClipHash: "long-reel", CreatedAt: time.Now().UTC(),
		Detection: &filler.SplitDetectionProgress{ScannedThroughMs: 600_000},
	}); err != nil {
		t.Fatal(err)
	}
	resp = do(t, srv, http.MethodGet, "/v1/filler/splits/sp_detecting", adminToken, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("incomplete detector checkpoint → %d, want 404 until reviewable", resp.StatusCode)
	}
	resp = do(t, srv, http.MethodGet, "/v1/filler/splits/sp_1", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("member read → %d, want 401 (§19)", resp.StatusCode)
	}
}

func TestGetFillerSplitOperation_RecoversTerminalResultWithoutEvents(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	now := time.Now().UTC()
	operation := store.InteractiveOperation{
		ID: "split-job-1", Kind: store.InteractiveOperationFillerSplit, Subject: "clip-hash",
		Status: store.InteractiveOperationSuccess, ResultID: "sp_1",
		StartedAt: now.Add(-time.Minute), CompletedAt: now, UpdatedAt: now,
	}
	if err := st.UpsertInteractiveOperation(t.Context(), operation); err != nil {
		t.Fatal(err)
	}

	resp := do(t, srv, http.MethodGet, "/v1/filler/split-operations/split-job-1", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get split operation -> %d", resp.StatusCode)
	}
	var got struct {
		JobID      string `json:"jobId"`
		Status     string `json:"status"`
		ProposalID string `json:"proposalId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.JobID != operation.ID || got.Status != string(operation.Status) || got.ProposalID != operation.ResultID {
		t.Fatalf("split operation = %+v, want terminal proposal result", got)
	}

	resp = do(t, srv, http.MethodGet, "/v1/filler/split-operations/missing", adminToken, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing split operation -> %d, want 404", resp.StatusCode)
	}
	resp = do(t, srv, http.MethodGet, "/v1/filler/split-operations/split-job-1", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("member split operation -> %d, want 401", resp.StatusCode)
	}
}

// Confirm maps the splitter's sentinels: 422 for a rejected edit, 404 for a
// missing proposal — and reports how many clips it wrote.
func TestConfirmFillerSplit_Route(t *testing.T) {
	srv, _, ff := newFillerServer(t)

	resp := do(t, srv, http.MethodPost, "/v1/filler/splits/sp_1/confirm", adminToken,
		`{"segments":[{"index":0,"startMs":0,"endMs":30000,"name":"a"},{"index":1,"startMs":30000,"endMs":60000,"name":"b"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm → %d", resp.StatusCode)
	}
	var body struct {
		Clips int `json:"clips"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Clips != 2 || ff.splits[0].proposalID != "sp_1" {
		t.Errorf("confirm result = %+v, calls = %+v", body, ff.splits)
	}

	// Member negative (§19): confirm is the catalog write — never a member's call.
	resp = do(t, srv, http.MethodPost, "/v1/filler/splits/sp_1/confirm", "",
		`{"segments":[{"index":0,"startMs":0,"endMs":30000,"name":"a"}]}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("member confirm → %d, want 401", resp.StatusCode)
	}

	ff.confirmInvalid = true
	resp = do(t, srv, http.MethodPost, "/v1/filler/splits/sp_1/confirm", adminToken,
		`{"segments":[{"index":0,"startMs":0,"endMs":30000,"name":"a"}]}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("rejected edit → %d, want 422", resp.StatusCode)
	}
	ff.confirmInvalid = false

	ff.confirmNotFound = true
	resp = do(t, srv, http.MethodPost, "/v1/filler/splits/gone/confirm", adminToken,
		`{"segments":[{"index":0,"startMs":0,"endMs":30000,"name":"a"}]}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing proposal → %d, want 404", resp.StatusCode)
	}
}

// ⚠ **The default page size is an API concern, and this test is what pins it there** (§10 V51d).
// `store.ClipFilter.Limit == 0` means "no LIMIT" because pod assembly loads the catalog through
// the zero filter; if the default ever migrates into the store, every channel's break pool
// silently truncates to 100 clips with no error and no log line. Asserting it at the HTTP edge is
// what keeps the two layers honest about which one owns the number.
func TestListFiller_DefaultsToOnePageAndReportsTheTotal(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	for i := range 120 {
		seedClip(t, st, fmt.Sprintf("p%03d", i), filler.Commercial, 1992, filler.Kids, "cereal")
	}

	var body struct {
		Clips []struct{ Hash string }
		Total int
	}
	resp := do(t, srv, http.MethodGet, "/v1/filler", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list → %d", resp.StatusCode)
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Clips) != 100 {
		t.Errorf("unparameterised list returned %d clips, want the 100-row default page", len(body.Clips))
	}
	if body.Total != 120 {
		t.Errorf("total = %d, want 120 — the total is how many MATCH, not how many are on the page", body.Total)
	}

	// The last page is short, and the total does not change with it.
	resp = do(t, srv, http.MethodGet, "/v1/filler?limit=100&offset=100", adminToken, "")
	body.Clips = nil
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Clips) != 20 || body.Total != 120 {
		t.Errorf("page 2 = %d clips / total %d, want 20 / 120", len(body.Clips), body.Total)
	}
}

// The cap and the floor are both real. ⚠ The floor matters as much: `limit=0` would otherwise
// reach the store's "unbounded" sentinel and restore the exact behaviour paging removed.
func TestListFiller_RejectsUnboundedAndOversizedPages(t *testing.T) {
	srv, _, _ := newFillerServer(t)
	for _, q := range []string{"limit=0", "limit=501", "limit=-1", "sort=path", "order=sideways"} {
		resp := do(t, srv, http.MethodGet, "/v1/filler?"+q, adminToken, "")
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s → %d, want 422 — an out-of-range page or an unknown sort must be refused, "+
				"never quietly coerced to something else", q, resp.StatusCode)
		}
	}
}

// Held clips are opt-in on the wire, and the DTO says which ones they are (§10 V38/V51d). ⚠ The
// pair is the point: a client that can ASK for held clips but cannot TELL which they are renders
// an unreviewed clip identically to a filed one.
func TestListFiller_HeldIsOptInAndLabelled(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedClip(t, st, "filed", filler.Commercial, 1992, filler.Kids, "cereal")
	seedClip(t, st, "waiting", filler.Commercial, 1992, filler.Kids, "cereal")
	if _, err := st.SetClipsHeld(context.Background(), []string{"waiting"}, true, false, time.Now()); err != nil {
		t.Fatal(err)
	}

	var body struct {
		Clips []struct {
			Hash string
			Held bool
		}
		Total int
	}
	resp := do(t, srv, http.MethodGet, "/v1/filler", adminToken, "")
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Clips) != 1 || body.Total != 1 {
		t.Fatalf("default list = %d clips / total %d, want just the filed one — a held clip is not "+
			"in the playable catalog", len(body.Clips), body.Total)
	}

	body.Clips = nil
	resp = do(t, srv, http.MethodGet, "/v1/filler?includeHeld=true", adminToken, "")
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Clips) != 2 || body.Total != 2 {
		t.Fatalf("includeHeld = %d clips / total %d, want 2 / 2", len(body.Clips), body.Total)
	}
	var labelled int
	for _, c := range body.Clips {
		if c.Held {
			labelled++
		}
	}
	if labelled != 1 {
		t.Errorf("%d clips carry held=true, want exactly 1 — the flag ships WITH the parameter, "+
			"or the client cannot tell an unreviewed clip from a filed one", labelled)
	}
}

// The batch read the pin/exclude editor uses instead of loading the catalog (§10 V51d).
func TestListFiller_HashesResolvesExactlyThoseClips(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	for _, id := range []string{"k1", "k2", "k3"} {
		seedClip(t, st, id, filler.Commercial, 1992, filler.Kids, "cereal")
	}
	var body struct {
		Clips []struct{ Hash string }
		Total int
	}
	// ⚠ REPEATED params — the form the generated client sends. Hand-written as `?hashes=k1,k3,gone`
	// this passed for the whole time the feature was broken in the browser: the server split on
	// commas, the client repeated the key, and only the FIRST pinned clip ever resolved. Every
	// other pin rendered as unresolved, which the channel page reported as "no longer in your
	// catalog" — a real clip, confidently declared missing.
	resp := do(t, srv, http.MethodGet, "/v1/filler?hashes=k1&hashes=k3&hashes=gone", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hashes → %d", resp.StatusCode)
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Clips) != 2 || body.Total != 2 {
		t.Errorf("got %d clips / total %d, want the 2 that exist (an unknown hash is absent, not an error)",
			len(body.Clips), body.Total)
	}
}
