package clipfetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestEnumerateCollection_PaginatesWithinAHardMetadataBound(t *testing.T) {
	var pages []int
	mux := http.NewServeMux()
	mux.HandleFunc("/advancedsearch.php", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
		pages = append(pages, page)
		_, _ = fmt.Fprintf(w, `{"response":{"numFound":80,"docs":[`)
		for i := 0; i < rows; i++ {
			if i > 0 {
				_, _ = fmt.Fprint(w, ",")
			}
			_, _ = fmt.Fprintf(w, `{"identifier":"page-%d-item-%d"}`, page, i)
		}
		_, _ = fmt.Fprint(w, `]}}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	result, err := discoverer(t, srv.URL).EnumerateCollection(t.Context(), "classic", 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 60 || !slices.Equal(pages, []int{1, 2, 3}) {
		t.Fatalf("items/pages = %d/%v, want 60/[1 2 3]", len(result.Items), pages)
	}
}

// Discovery is tested against the PINNED fixture, served by a local stub — never the live API
// (AGENTS.md: unit tests never touch the network). The fixture is a real capture, and it keeps
// all four doc shapes the live API returns; see fixtures/archive/FINDINGS.md.
func discoverServer(t *testing.T, onQuery func(url.Values)) *httptest.Server {
	t.Helper()
	raw, err := os.ReadFile("../testkit/fixtures/archive/collection_search.json")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/advancedsearch.php", func(w http.ResponseWriter, r *http.Request) {
		if onQuery != nil {
			onQuery(r.URL.Query())
		}
		_, _ = w.Write(raw)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func discoverer(t *testing.T, base string) *ArchiveDownloader {
	t.Helper()
	return &ArchiveDownloader{client: newArchiveClient(base, nil, diskSink{})}
}

// ⚠ The headline behaviour: a listing must NOT download anything. An operator browsing to
// decide whether a collection is worth having cannot be made to fetch gigabytes to find out.
// Asserted by serving ONLY the search endpoint — any metadata or file request 404s, and the
// walk would fail rather than succeed quietly.
func TestDiscoverCollection_DownloadsNothing(t *testing.T) {
	var hits int
	srv := discoverServer(t, func(url.Values) { hits++ })

	res, err := discoverer(t, srv.URL).DiscoverCollection(context.Background(), "classic_tv_commercials", 0)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("made %d search requests, want exactly 1 — a listing is one question", hits)
	}
	if len(res.Items) == 0 {
		t.Fatal("no items")
	}
}

// ⚠ Total is the COLLECTION's size, not the page's. Reporting len(Items) would tell an operator
// that an 8362-item collection holds 5 — and that number is exactly what they use to decide.
func TestDiscoverCollection_ReportsTheFullCollectionSize(t *testing.T) {
	srv := discoverServer(t, nil)

	res, err := discoverer(t, srv.URL).DiscoverCollection(context.Background(), "classic_tv_commercials", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 8362 {
		t.Errorf("Total = %d, want 8362 (numFound, not the page length)", res.Total)
	}
	if res.Total == len(res.Items) {
		t.Error("Total equals the page length — it is reporting the page, not the collection")
	}
}

// The fixture deliberately mixes shapes because the live API does: Solr omits absent fields
// entirely. A parser assuming title/licenceurl/year is always present passes on a tidy fixture
// and fails on the first real collection.
func TestDiscoverCollection_HandlesTheShapesTheAPIActuallySends(t *testing.T) {
	srv := discoverServer(t, nil)

	res, err := discoverer(t, srv.URL).DiscoverCollection(context.Background(), "classic_tv_commercials", 0)
	if err != nil {
		t.Fatal(err)
	}

	var withLicence, withoutLicence, withoutYear int
	for _, it := range res.Items {
		if it.ID == "" {
			t.Error("an item has no identifier — nothing could download it")
		}
		if it.License != "" {
			withLicence++
		} else {
			withoutLicence++
		}
		if it.Year == 0 {
			withoutYear++
		}
	}
	// Both licence states must be represented, or this test is not exercising the ~92%-absent
	// case that the whole "empty means unknown" rule exists for.
	if withLicence == 0 || withoutLicence == 0 {
		t.Errorf("fixture no longer covers both licence states (%d with, %d without) — "+
			"re-capture it with a mix, or this stops testing the absent case",
			withLicence, withoutLicence)
	}
	if withoutYear == 0 {
		t.Error("fixture no longer covers a doc with no year")
	}
}

// A caller may paste a URL, a /details/ path, or a bare id — the same spellings Download
// accepts. Making an operator know which form a field wants is a papercut with no upside.
func TestDiscoverCollection_AcceptsTheSpellingsDownloadDoes(t *testing.T) {
	var gotQuery string
	srv := discoverServer(t, func(q url.Values) { gotQuery = q.Get("q") })

	for _, ref := range []string{
		"classic_tv_commercials",
		"https://archive.org/details/classic_tv_commercials",
		"archive.org/details/classic_tv_commercials",
	} {
		if _, err := discoverer(t, srv.URL).DiscoverCollection(context.Background(), ref, 0); err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		if !strings.Contains(gotQuery, "classic_tv_commercials") {
			t.Errorf("%s → query %q, which does not name the collection", ref, gotQuery)
		}
	}
}

// ⚠ The identifier is a VALUE in a Solr query, so it must be quoted — otherwise an id with a
// colon or a space changes the query's MEANING rather than being searched for.
func TestDiscoverCollection_QuotesTheIdentifier(t *testing.T) {
	var gotQuery string
	srv := discoverServer(t, func(q url.Values) { gotQuery = q.Get("q") })

	if _, err := discoverer(t, srv.URL).DiscoverCollection(context.Background(), "odd id:with colon", 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQuery, `collection:"odd id:with colon"`) {
		t.Errorf("query = %q, want the identifier quoted as a single value", gotQuery)
	}
}

func TestDiscoverCollection_RejectsAnUnusableRef(t *testing.T) {
	srv := discoverServer(t, nil)

	if _, err := discoverer(t, srv.URL).DiscoverCollection(context.Background(), "", 0); err == nil {
		t.Error("an empty ref was accepted")
	}
}

// The row cap is a decision aid, not a browser: an operator judges a source from a handful of
// titles. An unbounded limit would spend the operator's latency and archive.org's bandwidth on
// rows nobody reads.
func TestDiscoverCollection_CapsRows(t *testing.T) {
	var gotRows string
	srv := discoverServer(t, func(q url.Values) { gotRows = q.Get("rows") })

	if _, err := discoverer(t, srv.URL).DiscoverCollection(context.Background(), "c", 10_000); err != nil {
		t.Fatal(err)
	}
	if gotRows != "25" {
		t.Errorf("rows = %s, want the 25 cap", gotRows)
	}
}

// --- per-item enrichment: duration + quality (V35) ---

// enrichServer serves the search fixture AND the pinned item metadata, which is what the live
// API does and what `discoverServer` deliberately does not — see the download-nothing test.
func enrichServer(t *testing.T, onMeta func(id string)) *httptest.Server {
	t.Helper()
	search, err := os.ReadFile("../testkit/fixtures/archive/collection_search.json")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := os.ReadFile("../testkit/fixtures/archive/metadata_item.json")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/advancedsearch.php", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(search)
	})
	mux.HandleFunc("/metadata/", func(w http.ResponseWriter, r *http.Request) {
		if onMeta != nil {
			onMeta(strings.TrimPrefix(r.URL.Path, "/metadata/"))
		}
		_, _ = w.Write(meta)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// The mock's search row draws `date · duration · quality`. Solr indexes only `date` at item
// level — measured against the live API, not assumed — so duration and quality come from a
// per-item metadata call, and this is the test that they actually arrive.
func TestEnrich_FillsDurationAndQuality(t *testing.T) {
	srv := enrichServer(t, nil)

	items := []DiscoveredItem{{ID: "a"}, {ID: "b"}}
	newArchiveClient(srv.URL, nil, diskSink{}).enrich(context.Background(), items)

	for _, it := range items {
		// 91.09s in the pinned fixture, from the h.264 derivative.
		if it.DurationMS != 91_090 {
			t.Errorf("%s: DurationMS = %d, want 91090 from the fixture's length", it.ID, it.DurationMS)
		}
		// ⚠ The BEST of the two video files (a 480p derivative beside a 960p original), not the
		// first listed — the honest quality answer is what is available, not what came first.
		if it.Height != 960 {
			t.Errorf("%s: Height = %d, want 960 — the best derivative, not the first", it.ID, it.Height)
		}
	}
}

// ⚠ A LISTING must not enrich, and this is the assertion that keeps it that way. Enriching
// inline was built and measured against the live API at 22.6s for a page of 25 (median 1.78s
// per call, and WORSE with a wider fan-out because archive.org throttles). The one-request
// property the file header describes is now load-bearing for latency, not just politeness.
func TestDiscoverCollection_DoesNotEnrichInline(t *testing.T) {
	var metaCalls int
	srv := enrichServer(t, func(string) { metaCalls++ })

	res, err := discoverer(t, srv.URL).DiscoverCollection(context.Background(), "classic_tv_commercials", 0)
	if err != nil {
		t.Fatal(err)
	}
	if metaCalls != 0 {
		t.Errorf("a listing made %d metadata calls — that is 22.6s for a full page, measured", metaCalls)
	}
	// And the rows still arrive, with what the ONE search request knows.
	if len(res.Items) == 0 {
		t.Fatal("no items")
	}
	for _, it := range res.Items {
		if it.DurationMS != 0 || it.Height != 0 {
			t.Errorf("%s: a listing reported %dms/%dp without asking", it.ID, it.DurationMS, it.Height)
		}
	}
}

// `date` rides the SAME search request as everything else — one extra field in fl[], not
// another round trip. The fixture keeps docs with and without it, as the live API does.
func TestDiscover_CarriesTheCataloguedDateWithoutAnExtraRequest(t *testing.T) {
	var fields []string
	srv := discoverServer(t, func(q url.Values) { fields = q["fl[]"] })

	res, err := discoverer(t, srv.URL).DiscoverCollection(context.Background(), "classic_tv_commercials", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fields, "date") {
		t.Errorf("fl[] = %v, want date requested in the same query", fields)
	}
	var withDate, withoutDate int
	for _, it := range res.Items {
		if it.Date != "" {
			withDate++
		} else {
			withoutDate++
		}
	}
	if withDate == 0 || withoutDate == 0 {
		t.Errorf("fixture no longer covers both date states (%d with, %d without) — "+
			"re-capture with a mix, or this stops testing the absent case", withDate, withoutDate)
	}
}

// ⚠ A metadata failure must cost that ITEM its extra fields and nothing more. One row reading
// "—" is a far better outcome than a panel that fails because one of 25 items timed out —
// which, at a max observed 7.75s per call, is a live possibility rather than a hypothetical.
func TestEnrich_AnItemWhoseMetadataFailsKeepsItsSearchFields(t *testing.T) {
	meta, err := os.ReadFile("../testkit/fixtures/archive/metadata_item.json")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata/", func(w http.ResponseWriter, r *http.Request) {
		// Exactly one item fails, so the assertion distinguishes "isolated" from "all broken".
		if strings.HasSuffix(r.URL.Path, "/broken") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(meta)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	items := []DiscoveredItem{
		{ID: "ok", Title: "A good one"},
		{ID: "broken", Title: "The one that fails"},
	}
	newArchiveClient(srv.URL, nil, diskSink{}).enrich(context.Background(), items)

	if items[0].DurationMS == 0 {
		t.Error("the healthy item lost its stats because a SIBLING failed")
	}
	// ⚠ 0 is UNKNOWN here, and the UI renders it as "—". It must never be shown as "0:00".
	if items[1].DurationMS != 0 || items[1].Height != 0 {
		t.Errorf("broken: got %dms/%dp from a failing metadata call", items[1].DurationMS, items[1].Height)
	}
	if items[1].Title != "The one that fails" {
		t.Error("the failing item lost its search fields")
	}
}

// Enrichment asks once per item and no more. Each call is ~1.8s of someone else's
// infrastructure, so a duplicate is not merely wasteful — it is the difference between a
// usable panel and one that stalls.
func TestEnrich_AsksOncePerItem(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	srv := enrichServer(t, func(id string) {
		mu.Lock()
		defer mu.Unlock()
		seen[id]++
	})

	items := []DiscoveredItem{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	newArchiveClient(srv.URL, nil, diskSink{}).enrich(context.Background(), items)

	if len(seen) != len(items) {
		t.Errorf("fetched metadata for %d ids, want %d — one per item", len(seen), len(items))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%s fetched %d times, want exactly 1", id, n)
		}
	}
}

// A cancelled request must not fetch metadata nobody is waiting for.
//
// ⚠ **What this pins is the REQUEST context, not enrich's select.** Sabotage says so: deleting
// the `case <-ctx.Done()` changes nothing here, because `metadata` → `getJSON` builds with
// http.NewRequestWithContext and the transport refuses a dead context by itself. Detaching that
// (a `context.Background()` request) makes this test fail with all 18 calls fired, which is how
// the real guarantee was located. The select is an optimisation over the queued goroutines and
// is documented as such at its site.
//
// ⚠ It drives `enrich` DIRECTLY, which is the only route to a cancelled enrichment: through
// DiscoverCollection the SEARCH fails first (cancelled up front) or the body read fails
// (cancelled from the handler), so enrichment is never reached and the assertion would hold
// vacuously. An earlier version of this test did exactly that and proved nothing.
//
// More items than the concurrency cap, so the later goroutines are genuinely still queued.
func TestEnrich_StopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var mu sync.Mutex
	var metaCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata/", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		metaCalls++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	items := make([]DiscoveredItem, enrichConcurrency*3)
	for i := range items {
		items[i].ID = "item-" + strconv.Itoa(i)
	}
	newArchiveClient(srv.URL, nil, diskSink{}).enrich(ctx, items)

	mu.Lock()
	defer mu.Unlock()
	if metaCalls != 0 {
		t.Errorf("made %d metadata calls on an already-cancelled context, want 0", metaCalls)
	}
}

// The three spellings archive.org really sends. ⚠ Measured: 36 video files across 5 live items
// were all seconds-as-string, but the colon form is documented on some derivatives, so it is
// parsed rather than left to truncate to 0 the first time one appears.
func TestParseLengthMS(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"91.09", 91_090},      // seconds with a decimal — the common case
		{"660", 660_000},       // seconds without one — the licensed fixture's shape
		{"9645.34", 9_645_340}, // a 2.7-hour feature, still seconds
		{"2:30", 150_000},      // MM:SS
		{"1:00:00", 3_600_000}, // HH:MM:SS
		{"", 0},                // absent — every non-video file
		{"garbage", 0},         // unparseable is UNKNOWN, never zero-length
		{"1:bad", 0},           // a malformed segment poisons the whole value
		{"-5", 0},              // negative is not a runtime
	} {
		if got := parseLengthMS(tc.raw); got != tc.want {
			t.Errorf("parseLengthMS(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// --- keyword search across archive.org (V33) ---

func searchServer(t *testing.T, onQuery func(url.Values)) *httptest.Server {
	t.Helper()
	raw, err := os.ReadFile("../testkit/fixtures/archive/keyword_search.json")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/advancedsearch.php", func(w http.ResponseWriter, r *http.Request) {
		if onQuery != nil {
			onQuery(r.URL.Query())
		}
		_, _ = w.Write(raw)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSearch_ReturnsItemsWithTheirTotal(t *testing.T) {
	srv := searchServer(t, nil)

	res, err := discoverer(t, srv.URL).Search(context.Background(), "1980s cereal commercial", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) == 0 {
		t.Fatal("no items")
	}
	// numFound from the real capture — the count an operator judges the search by, not the
	// page length.
	if res.Total != 54 {
		t.Errorf("Total = %d, want 54", res.Total)
	}
	for _, it := range res.Items {
		if it.ID == "" {
			t.Error("an item has no identifier — nothing could download it")
		}
	}
}

// ⚠ Scoped to mediatype:movies. Without it the same words return texts, audio and software —
// real results for the query, useless for a clip catalog.
func TestSearch_ScopesToVideo(t *testing.T) {
	var got string
	srv := searchServer(t, func(q url.Values) { got = q.Get("q") })

	if _, err := discoverer(t, srv.URL).Search(context.Background(), "cereal advert", 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "mediatype:movies") {
		t.Errorf("query = %q, want it scoped to mediatype:movies", got)
	}
}

// A search opened from one registered collection must stay inside that collection. Without the
// collection clause, a search for "cereal" returns podcasts, full broadcasts, and unrelated
// uploads from all of Archive.org even though the UI says it is searching one source.
func TestSearchCollection_ScopesWordsToTheNamedCollection(t *testing.T) {
	var got string
	srv := searchServer(t, func(q url.Values) { got = q.Get("q") })

	if _, err := discoverer(t, srv.URL).SearchCollection(
		context.Background(), "classic_tv_commercials", "cereal advert", 0,
	); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `collection:"classic_tv_commercials"`) {
		t.Errorf("query = %q, want the registered collection clause", got)
	}
	if !strings.Contains(got, "(cereal advert)") {
		t.Errorf("query = %q, want the operator's words", got)
	}
	if !strings.Contains(got, "mediatype:movies") {
		t.Errorf("query = %q, want it scoped to video", got)
	}
}

// ⚠ PARENTHESISED, not quoted. Quoting forces an exact-phrase match, so a three-word search
// would find only items containing that literal string — almost nothing. The parentheses also
// stop `AND mediatype:movies` binding to just the last word.
func TestSearch_DoesNotForceAnExactPhrase(t *testing.T) {
	var got string
	srv := searchServer(t, func(q url.Values) { got = q.Get("q") })

	if _, err := discoverer(t, srv.URL).Search(context.Background(), "1980s cereal commercial", 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, `"1980s cereal commercial"`) {
		t.Errorf("query = %q — quoting forces an exact phrase and finds nothing", got)
	}
	if !strings.Contains(got, "(1980s cereal commercial)") {
		t.Errorf("query = %q, want the words parenthesised", got)
	}
}

// A person pasting a URL or typing a colon must not accidentally write a Solr field query or a
// range — they would get a syntax error with no visible cause.
func TestSearch_StripsSolrSyntaxFromUserWords(t *testing.T) {
	var got string
	srv := searchServer(t, func(q url.Values) { got = q.Get("q") })

	if _, err := discoverer(t, srv.URL).Search(context.Background(), `collection:foo [a TO z] "x"`, 0); err != nil {
		t.Fatal(err)
	}
	// The only colon left is the one WE added for mediatype.
	if strings.Count(got, ":") != 1 {
		t.Errorf("query = %q, want the user's colons stripped", got)
	}
	if strings.ContainsAny(strings.TrimSuffix(got, ") AND mediatype:movies"), `[]"`) {
		t.Errorf("query = %q, want brackets and quotes stripped", got)
	}
}

func TestSearch_RejectsAnEmptyQuery(t *testing.T) {
	srv := searchServer(t, nil)

	if _, err := discoverer(t, srv.URL).Search(context.Background(), "   ", 0); err == nil {
		t.Error("an all-whitespace query was accepted")
	}
}
