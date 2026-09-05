package tmdb_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/testkit"
	"github.com/loomarr/loomarr/internal/tmdb"
)

func TestSearch_MapsMovieAndTV_DropsPerson(t *testing.T) {
	mock := testkit.NewTMDB(t)
	mock.SetDiscoveryEvidence(provision.Movie, 101, "en", []string{"US"}, 136, 7.4, 1200)
	c := tmdb.NewWithBase(mock.URL, "key")

	got, err := c.Search(context.Background(), "the", 20) // matches The Rock, The Matrix
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected results")
	}
	for _, cand := range got {
		if cand.TMDBID == 0 {
			t.Errorf("candidate has no TMDB id (grounding requires real ids): %+v", cand)
		}
		if cand.MediaType != provision.Movie && cand.MediaType != provision.Series {
			t.Errorf("person/unknown result not filtered: %+v", cand)
		}
		if cand.TMDBID == 101 && (cand.OriginalLanguage != "en" || len(cand.OriginCountries) != 1 ||
			cand.OriginCountries[0] != "US" || cand.VoteAverage != 7.4 || cand.VoteCount != 1200) {
			t.Errorf("TMDB result lost discovery evidence: %+v", cand)
		}
	}
}

func TestDiscoverKeywordsCarriesResolvedKeywordEvidence(t *testing.T) {
	mock := testkit.NewTMDB(t)
	mock.AddKeywordMovie(77_001, "Winter Story", 2020, []int{10_751}, "A family reunion.", "Christmas")
	c := tmdb.NewWithBase(mock.URL, "key")

	got, err := c.Discover(context.Background(), catalog.DiscoveryQuery{
		MediaType: provision.Movie, Keywords: []string{"christmas"},
	}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Keywords) != 1 || got[0].Keywords[0] != "Christmas" {
		t.Fatalf("keyword discovery evidence = %+v, want exact resolved TMDB keyword", got)
	}
}

func TestDiscoverKeywordsDoesNotOverclaimORKeywordEvidence(t *testing.T) {
	mock := testkit.NewTMDB(t)
	mock.AddKeywordMovie(77_001, "Winter Story", 2020, nil, "A family reunion.", "Christmas")
	mock.AddKeywordMovie(77_002, "October Story", 2021, nil, "A masked visitor.", "Halloween")
	c := tmdb.NewWithBase(mock.URL, "key")

	got, err := c.Discover(context.Background(), catalog.DiscoveryQuery{
		MediaType: provision.Movie, Keywords: []string{"christmas", "halloween"},
	}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("keyword discovery = %+v, want two OR matches", got)
	}
	for _, candidate := range got {
		if len(candidate.Keywords) != 0 {
			t.Fatalf("OR discovery attributed unresolved row-level keywords: %+v", candidate)
		}
	}
}

func TestDiscover_MapsScienceFictionToTVGenreID(t *testing.T) {
	mock := testkit.NewTMDB(t)
	mock.AddSeries(52_001, "Midnight Orbit", 2022, []int{10765}, "Atmospheric science fiction after dark.")
	c := tmdb.NewWithBase(mock.URL, "key")

	got, err := c.Discover(context.Background(), catalog.DiscoveryQuery{
		MediaType: provision.Series, Genres: []string{"science fiction"},
	}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TMDBID != 52_001 {
		t.Fatalf("TV science-fiction discovery = %+v, want Midnight Orbit via genre 10765", got)
	}
}

func TestDiscover_AppliesAuthoritativeScalarQualifiersToMovieAndTV(t *testing.T) {
	mock := testkit.NewTMDB(t)
	c := tmdb.NewWithBase(mock.URL, "key")

	_, err := c.Discover(context.Background(), catalog.DiscoveryQuery{
		Genres:           []string{"Drama"},
		OriginalLanguage: "en",
		OriginCountry:    "GB",
		RuntimeMin:       20,
		RuntimeMax:       90,
		VoteAverageMin:   7.5,
		VoteCountMin:     100,
	}, 20)
	if err != nil {
		t.Fatal(err)
	}

	want := url.Values{
		"with_original_language": {"en"},
		"with_origin_country":    {"GB"},
		"with_runtime.gte":       {"20"},
		"with_runtime.lte":       {"90"},
		"vote_average.gte":       {"7.5"},
		"vote_count.gte":         {"100"},
	}
	seen := map[string]bool{}
	for _, request := range mock.Requests() {
		if request.Path != "/discover/movie" && request.Path != "/discover/tv" {
			continue
		}
		seen[request.Path] = true
		got, parseErr := url.ParseQuery(request.RawQuery)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for key, values := range want {
			if got.Get(key) != values[0] {
				t.Errorf("%s %s = %q, want %q", request.Path, key, got.Get(key), values[0])
			}
		}
	}
	if !seen["/discover/movie"] || !seen["/discover/tv"] {
		t.Fatalf("discover requests = %+v, want movie and TV", mock.Requests())
	}
}

func TestDiscover_ResolvesMovieCastAndCreatorsToGroundedPeople(t *testing.T) {
	mock := testkit.NewTMDB(t)
	mock.AddPerson(31, "Tom Hanks")
	mock.AddPerson(560, "Nora Ephron")
	mock.SetMoviePeople(100, []int{31}, []int{560})
	c := tmdb.NewWithBase(mock.URL, "key")

	got, err := c.Discover(context.Background(), catalog.DiscoveryQuery{
		MediaType: provision.Movie, Cast: []string{"Tom Hanks"}, Creators: []string{"Nora Ephron"},
	}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TMDBID != 100 || !reflect.DeepEqual(got[0].Cast, []string{"Tom Hanks"}) ||
		!reflect.DeepEqual(got[0].Creators, []string{"Nora Ephron"}) {
		t.Fatalf("person-qualified discovery = %+v", got)
	}
	var searches int
	for _, request := range mock.Requests() {
		if request.Path == "/search/person" {
			searches++
		}
		if request.Path == "/discover/movie" {
			query, parseErr := url.ParseQuery(request.RawQuery)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			if query.Get("with_cast") != "31" || query.Get("with_crew") != "560" {
				t.Fatalf("person filters = %q/%q", query.Get("with_cast"), query.Get("with_crew"))
			}
		}
	}
	if searches != 2 {
		t.Fatalf("person searches = %d, want 2", searches)
	}
}

func TestDiscover_ResolvesAmbiguousNetworkByCountryWithoutLeakingCredentialToExport(t *testing.T) {
	mock := testkit.NewTMDB(t)
	mock.AddSeries(20_001, "Signal House", 2024, []int{18}, "A prestige drama.")
	mock.SetDiscoveryEvidence(provision.Series, 20_001, "en", []string{"US"}, 55, 8.1, 800)
	mock.AddNetwork(49, "HBO", "US")
	mock.AddNetwork(1590, "HBO", "DE")
	mock.SetSeriesNetwork(20_001, 49)
	c := tmdb.NewWithBase(mock.URL, "secret-key")

	got, err := c.Discover(context.Background(), catalog.DiscoveryQuery{
		MediaType: provision.Series, Network: "HBO", OriginCountry: "US",
	}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TMDBID != 20_001 || !reflect.DeepEqual(got[0].Networks, []string{"HBO"}) {
		t.Fatalf("network-qualified discovery = %+v", got)
	}
	var exportRequests, detailRequests, discoverRequests int
	for _, request := range mock.Requests() {
		switch {
		case strings.HasPrefix(request.Path, "/p/exports/"):
			exportRequests++
			if request.Authorization != "" {
				t.Fatalf("TMDB credential leaked to export host: %q", request.Authorization)
			}
		case strings.HasPrefix(request.Path, "/network/"):
			detailRequests++
			if request.Authorization != "Bearer secret-key" {
				t.Fatalf("network detail authorization = %q", request.Authorization)
			}
		case request.Path == "/discover/tv":
			discoverRequests++
			query, parseErr := url.ParseQuery(request.RawQuery)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			if query.Get("with_networks") != "49" {
				t.Fatalf("with_networks = %q, want 49", query.Get("with_networks"))
			}
		}
	}
	if exportRequests != 1 || detailRequests != 2 || discoverRequests != 1 {
		t.Fatalf("request counts export/detail/discover = %d/%d/%d", exportRequests, detailRequests, discoverRequests)
	}
}

func TestDiscover_UnresolvedOrAmbiguousEntityFailsBeforeDiscovery(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testkit.TMDB)
		query catalog.DiscoveryQuery
		want  string
	}{
		{
			name:  "ambiguous person",
			setup: func(mock *testkit.TMDB) { mock.AddPerson(1, "Alex Smith"); mock.AddPerson(2, "Alex Smith") },
			query: catalog.DiscoveryQuery{MediaType: provision.Movie, Cast: []string{"Alex Smith"}},
			want:  "2 exact identities",
		},
		{
			name:  "person interior whitespace is not normalized",
			setup: func(mock *testkit.TMDB) { mock.AddPerson(31, "Tom Hanks") },
			query: catalog.DiscoveryQuery{MediaType: provision.Movie, Cast: []string{"Tom  Hanks"}},
			want:  "0 exact identities",
		},
		{
			name:  "unresolved network",
			setup: func(mock *testkit.TMDB) { mock.AddNetwork(49, "HBO", "US") },
			query: catalog.DiscoveryQuery{MediaType: provision.Series, Network: "Imaginary Network"},
			want:  "no exact identity",
		},
		{
			name:  "ambiguous network without country",
			setup: func(mock *testkit.TMDB) { mock.AddNetwork(49, "HBO", "US"); mock.AddNetwork(1590, "HBO", "DE") },
			query: catalog.DiscoveryQuery{MediaType: provision.Series, Network: "HBO"},
			want:  "include origin_country",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := testkit.NewTMDB(t)
			tt.setup(mock)
			_, err := tmdb.NewWithBase(mock.URL, "key").Discover(context.Background(), tt.query, 20)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			for _, request := range mock.Requests() {
				if strings.HasPrefix(request.Path, "/discover/") {
					t.Fatalf("failed entity resolution reached discovery: %+v", mock.Requests())
				}
			}
		})
	}
}

func TestDiscover_RejectsUnsupportedEntityMediaTypeBeforeHTTP(t *testing.T) {
	mock := testkit.NewTMDB(t)
	_, err := tmdb.NewWithBase(mock.URL, "key").Discover(context.Background(), catalog.DiscoveryQuery{
		MediaType: provision.Series, Cast: []string{"Idris Elba"},
	}, 20)
	if err == nil || !strings.Contains(err.Error(), "movie media type") {
		t.Fatalf("error = %v", err)
	}
	if mock.RequestCount() != 0 {
		t.Fatalf("unsupported entity query made requests: %+v", mock.Requests())
	}
}

func TestExists_RealID200_FabricatedID404(t *testing.T) {
	mock := testkit.NewTMDB(t)
	c := tmdb.NewWithBase(mock.URL, "key")

	// Real movie id (100 = Speed) exists.
	ok, err := c.Exists(context.Background(), provision.Movie, 100)
	if err != nil || !ok {
		t.Fatalf("Exists(movie,100) = %v,%v want true,nil", ok, err)
	}
	// Fabricated id → does not exist (the LLM-hallucination path §8).
	ok, err = c.Exists(context.Background(), provision.Movie, 88888888)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("fabricated TMDB id must report not-exists (grounding drops it)")
	}
	// Real series id.
	ok, _ = c.Exists(context.Background(), provision.Series, 1396)
	if !ok {
		t.Error("Breaking Bad (tv:1396) should exist")
	}
}

func TestExists_ZeroIDIsFalse(t *testing.T) {
	c := tmdb.NewWithBase("http://unused", "key")
	ok, err := c.Exists(context.Background(), provision.Movie, 0)
	if err != nil || ok {
		t.Errorf("Exists(_,0) = %v,%v want false,nil", ok, err)
	}
}

// CollectionID reads belongs_to_collection.id from /movie/{id} (§5 franchise ordering): a
// franchise film returns its collection id, a standalone returns 0, a series 0 (no lookup).
func TestCollectionID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie/85": // Raiders — in the Indiana Jones Collection (84)
			_, _ = w.Write([]byte(`{"id":85,"belongs_to_collection":{"id":84,"name":"Indiana Jones Collection"}}`))
		case "/movie/700": // a standalone — belongs_to_collection is null
			_, _ = w.Write([]byte(`{"id":700,"belongs_to_collection":null}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := tmdb.NewWithBase(srv.URL, "key")

	if id, err := c.CollectionID(context.Background(), provision.Movie, 85); err != nil || id != 84 {
		t.Errorf("CollectionID(movie,85) = %d,%v want 84,nil", id, err)
	}
	if id, err := c.CollectionID(context.Background(), provision.Movie, 700); err != nil || id != 0 {
		t.Errorf("CollectionID(movie,700) = %d,%v want 0,nil (standalone)", id, err)
	}
	// A series never has a collection — no HTTP call, returns 0.
	if id, err := c.CollectionID(context.Background(), provision.Series, 1396); err != nil || id != 0 {
		t.Errorf("CollectionID(series,_) = %d,%v want 0,nil (no lookup)", id, err)
	}
}

// EpisodeStillURL reads still_path from GET /tv/{id}/season/{s}/episode/{e} (the live-TV
// timeline's per-episode hover thumbnail). An episode with an image returns imageBase+path, one
// without returns "", and a not-found episode returns "" + no error (best-effort, mirrors
// PosterURL's no-poster/not-found handling).
func TestEpisodeStillURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tv/1396/season/1/episode/1": // Breaking Bad S1E1 — has a still
			_, _ = w.Write([]byte(`{"id":62085,"still_path":"/abc.jpg"}`))
		case "/tv/1396/season/1/episode/2": // has no still image
			_, _ = w.Write([]byte(`{"id":62086,"still_path":""}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := tmdb.NewWithBase(srv.URL, "key")

	// ⚠ `original`, not `w500`, since V52 phase 7: the caller ADOPTS this URL into the image service
	// rather than handing it to a browser, and §22 fetches the original once then builds the width
	// ladder locally — asking TMDB for a pre-shrunk copy would upscale the larger rungs.
	if u, err := c.EpisodeStillURL(context.Background(), 1396, 1, 1); err != nil || u != "https://image.tmdb.org/t/p/original/abc.jpg" {
		t.Errorf("EpisodeStillURL(1396,1,1) = %q,%v want image url,nil", u, err)
	}
	if u, err := c.EpisodeStillURL(context.Background(), 1396, 1, 2); err != nil || u != "" {
		t.Errorf("EpisodeStillURL(1396,1,2) = %q,%v want \"\",nil (no still)", u, err)
	}
	// A season/episode TMDB doesn't have → "" and NO error (not-found is not a failure).
	if u, err := c.EpisodeStillURL(context.Background(), 1396, 99, 99); err != nil || u != "" {
		t.Errorf("EpisodeStillURL(1396,99,99) = %q,%v want \"\",nil (not found)", u, err)
	}
	// Zero id → no HTTP call, "" + nil.
	if u, err := c.EpisodeStillURL(context.Background(), 0, 1, 1); err != nil || u != "" {
		t.Errorf("EpisodeStillURL(0,_,_) = %q,%v want \"\",nil", u, err)
	}
}

// Movie previews share the episode still's landscape shape. A backdrop is the movie-level
// equivalent; using poster_path here would make the film a narrow 2:3 sliver in the same card.
func TestBackdropURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie/603":
			_, _ = w.Write([]byte(`{"backdrop_path":"/matrix-wide.jpg","poster_path":"/matrix-poster.jpg"}`))
		case "/movie/604":
			_, _ = w.Write([]byte(`{"backdrop_path":""}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := tmdb.NewWithBase(srv.URL, "key")

	if u, err := c.BackdropURL(context.Background(), provision.Movie, 603); err != nil || u != "https://image.tmdb.org/t/p/original/matrix-wide.jpg" {
		t.Errorf("BackdropURL(movie,603) = %q,%v want landscape image url,nil", u, err)
	}
	if u, err := c.BackdropURL(context.Background(), provision.Movie, 604); err != nil || u != "" {
		t.Errorf("BackdropURL(movie,604) = %q,%v want empty,nil", u, err)
	}
	if u, err := c.BackdropURL(context.Background(), provision.Movie, 0); err != nil || u != "" {
		t.Errorf("BackdropURL(movie,0) = %q,%v want empty,nil", u, err)
	}
}

// ContentRating pulls the US rating from /content_ratings (tv) or /release_dates
// (movie) — the source for an acquisition's rating before it's in the library (§389).
// Sparse coverage is normal, so a title with none returns "" and no error.
func TestContentRating(t *testing.T) {
	mock := testkit.NewTMDB(t)
	mock.SetRating(provision.Series, 1396, "TV-MA") // Breaking Bad
	mock.SetRating(provision.Movie, 603, "R")       // The Matrix
	c := tmdb.NewWithBase(mock.URL, "key")

	if r, err := c.ContentRating(context.Background(), provision.Series, 1396); err != nil || r != "TV-MA" {
		t.Errorf("tv rating = %q,%v want TV-MA,nil", r, err)
	}
	if r, err := c.ContentRating(context.Background(), provision.Movie, 603); err != nil || r != "R" {
		t.Errorf("movie rating = %q,%v want R,nil", r, err)
	}
	// A title TMDB doesn't rate → "" and NO error (sparse coverage is expected).
	if r, err := c.ContentRating(context.Background(), provision.Movie, 100); err != nil || r != "" {
		t.Errorf("unrated title = %q,%v want \"\",nil", r, err)
	}
	if r, err := c.ContentRating(context.Background(), provision.Movie, 0); err != nil || r != "" {
		t.Errorf("zero id = %q,%v want \"\",nil", r, err)
	}
}
