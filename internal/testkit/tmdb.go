package testkit

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/loomarr/loomarr/internal/provision"
)

// TMDB is the shared TMDB test double (AGENTS.md: one mock per service). It
// serves a small in-memory catalog for /search/multi and answers /movie/{id} +
// /tv/{id} exists-checks, so grounding tests can distinguish a real id from a
// fabricated one (the id-not-in-catalog case is the LLM-hallucination path §8).
type TMDB struct {
	*httptest.Server
	mu       sync.Mutex
	requests []TMDBRequest
	// movies/series are the "real" ids the mock knows. /search/multi returns
	// matches by name substring; Exists (GET /movie|tv/{id}) 200s iff the id is
	// here, else 404 — that 404 is what the suggester's validation drops.
	movies map[int]tmdbTitle
	series map[int]tmdbTitle
	// keywords model TMDB's separate keyword corpus. Titles carry keyword ids;
	// /search/keyword resolves human terms and /discover uses with_keywords.
	keywords      map[int]string
	nextKeywordID int
	// recommends is the adjacency graph /{movie,tv}/{id}/recommendations serves
	// (programming-design §8.3): seed tmdb id → the ids it recommends. Empty for a
	// seed the test didn't wire, which is the real API's behaviour for an obscure
	// title — an unproductive seed, not an error.
	recommends map[int][]int
	people     map[int]string
	networks   map[int]tmdbNetwork
}

type tmdbNetwork struct {
	ID            int
	Name          string
	OriginCountry string
}

// TMDBRequest is one request observed by the shared TMDB adapter. It records
// only the fields adapter contract tests need and deliberately keeps secrets
// out of URLs by exposing the query separately from Authorization.
type TMDBRequest struct {
	Path          string
	RawQuery      string
	Authorization string
}

// Requests returns a stable copy of all requests received so far.
func (m *TMDB) Requests() []TMDBRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]TMDBRequest(nil), m.requests...)
}

// RequestCount returns the number of requests received so far.
func (m *TMDB) RequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

// WithRecommendations wires the adjacency graph a test wants /recommendations to serve.
// Seeds absent from the map return an empty result set, so a test can assert the
// best-effort skip without standing up a failing server.
func (m *TMDB) WithRecommendations(graph map[int][]int) *TMDB {
	m.recommends = graph
	return m
}

type tmdbTitle struct {
	ID               int
	CollectionID     int
	Name             string
	Year             int
	Date             string
	GenreIDs         []int // §8 enrichment: endpoint-specific TMDB genre ids (movie 878 vs TV 10765 for Sci-Fi)
	KeywordIDs       []int
	Overview         string // short synopsis the model reasons about
	OriginalLanguage string
	OriginCountries  []string
	VoteAverage      float64
	VoteCount        int
	RuntimeMinutes   int
	CastIDs          []int
	CreatorIDs       []int
	NetworkID        int
	// USRating is the US content rating the /content_ratings (tv) or /release_dates
	// (movie) endpoint reports (§389 acquisition enrichment). Empty ⇒ no US rating,
	// which is the common sparse-coverage case a test may assert is handled.
	USRating string
}

// SetDiscoveryEvidence scripts source metadata and the runtime used by TMDB's
// remote discovery filter. It keeps adapter tests on the shared service mock.
func (m *TMDB) SetDiscoveryEvidence(mt provision.MediaType, id int, language string, countries []string, runtimeMinutes int, voteAverage float64, voteCount int) {
	catalog := m.movies
	if mt == provision.Series {
		catalog = m.series
	}
	title := catalog[id]
	title.ID = id
	title.OriginalLanguage = language
	title.OriginCountries = append([]string(nil), countries...)
	title.RuntimeMinutes = runtimeMinutes
	title.VoteAverage = voteAverage
	title.VoteCount = voteCount
	catalog[id] = title
}

// SetCollectionID scripts belongs_to_collection on the public movie detail
// boundary used by schedule materialization.
func (m *TMDB) SetCollectionID(movieID, collectionID int) {
	t := m.movies[movieID]
	t.ID, t.CollectionID = movieID, collectionID
	m.movies[movieID] = t
}

// SetRating scripts a title's US content rating so a test can drive the §389
// acquisition-enrichment path. Adds the id to the relevant catalog if absent.
func (m *TMDB) SetRating(mt provision.MediaType, tmdbID int, rating string) {
	cat := m.movies
	if mt == provision.Series {
		cat = m.series
	}
	t := cat[tmdbID]
	t.ID, t.USRating = tmdbID, rating
	cat[tmdbID] = t
}

// AddMovie adds one grounded movie to the shared TMDB corpus. Tests use this to
// exercise realistic result windows and pagination without inventing a private
// TMDB mock beside the repository-wide service double.
func (m *TMDB) AddMovie(id int, name string, year int, genreIDs []int, overview string) {
	m.movies[id] = tmdbTitle{
		ID: id, Name: name, Year: year, Date: fmt.Sprintf("%04d-01-01", year),
		GenreIDs: append([]int(nil), genreIDs...), Overview: overview,
	}
}

// AddSeries adds one grounded series to the shared TMDB corpus.
func (m *TMDB) AddSeries(id int, name string, year int, genreIDs []int, overview string) {
	m.series[id] = tmdbTitle{
		ID: id, Name: name, Year: year, Date: fmt.Sprintf("%04d-01-01", year),
		GenreIDs: append([]int(nil), genreIDs...), Overview: overview,
	}
}

// AddPerson adds one authoritative identity to /search/person.
func (m *TMDB) AddPerson(id int, name string) { m.people[id] = strings.TrimSpace(name) }

// SetMoviePeople binds exact cast and creator identities to a grounded movie.
func (m *TMDB) SetMoviePeople(movieID int, castIDs, creatorIDs []int) {
	title := m.movies[movieID]
	title.CastIDs = append([]int(nil), castIDs...)
	title.CreatorIDs = append([]int(nil), creatorIDs...)
	m.movies[movieID] = title
}

// AddNetwork adds one authoritative TV-network identity to the daily export and details endpoint.
func (m *TMDB) AddNetwork(id int, name, originCountry string) {
	m.networks[id] = tmdbNetwork{ID: id, Name: strings.TrimSpace(name), OriginCountry: strings.ToUpper(strings.TrimSpace(originCountry))}
}

// SetSeriesNetwork binds a series to the network identity used by TV discovery.
func (m *TMDB) SetSeriesNetwork(seriesID, networkID int) {
	title := m.series[seriesID]
	title.NetworkID = networkID
	m.series[seriesID] = title
}

// AddKeywordMovie adds a movie whose thematic match lives in TMDB's keyword
// metadata rather than its title. It is the realistic boundary fixture for
// holiday/motif requests such as "Christmas" finding "Snowbound Reunion".
func (m *TMDB) AddKeywordMovie(id int, name string, year int, genreIDs []int, overview string, keywords ...string) {
	keywordIDs := make([]int, 0, len(keywords))
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		var keywordID int
		for id, existing := range m.keywords {
			if strings.EqualFold(existing, keyword) {
				keywordID = id
				break
			}
		}
		if keywordID == 0 {
			keywordID = m.nextKeywordID
			m.nextKeywordID++
			m.keywords[keywordID] = keyword
		}
		keywordIDs = append(keywordIDs, keywordID)
	}
	m.movies[id] = tmdbTitle{
		ID: id, Name: name, Year: year, Date: fmt.Sprintf("%04d-01-01", year),
		GenreIDs: append([]int(nil), genreIDs...), KeywordIDs: keywordIDs, Overview: overview,
	}
}

// NewTMDB starts a mock TMDB with a fixed small catalog (Speed/The Rock movies,
// one series) — enough to exercise search + exists + the fabricated-id path.
func NewTMDB(t testing.TB) *TMDB {
	t.Helper()
	m := &TMDB{
		keywords:      map[int]string{},
		nextKeywordID: 5000,
		movies: map[int]tmdbTitle{
			100: {ID: 100, Name: "Speed", Year: 1994, Date: "1994-06-10", GenreIDs: []int{28, 53}, Overview: "A cop must keep a bus above 50mph or a bomb detonates."},
			101: {ID: 101, Name: "The Rock", Year: 1996, Date: "1996-06-07", GenreIDs: []int{28, 12, 53}, Overview: "A chemist and an ex-con storm Alcatraz to stop a rogue general."},
			603: {ID: 603, Name: "The Matrix", Year: 1999, Date: "1999-03-31", GenreIDs: []int{28, 878}, Overview: "A hacker learns reality is a simulation and joins a rebellion."},
		},
		series: map[int]tmdbTitle{
			1396: {ID: 1396, Name: "Breaking Bad", Year: 2008, Date: "2008-01-20", GenreIDs: []int{18, 80}, Overview: "A chemistry teacher turns to making meth."},
		},
		people:   map[int]string{},
		networks: map[int]tmdbNetwork{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search/multi", func(w http.ResponseWriter, r *http.Request) {
		q := strings.ToLower(r.URL.Query().Get("query"))
		var results []map[string]any
		for _, mv := range m.movies {
			if strings.Contains(strings.ToLower(mv.Name), q) {
				results = append(results, movieRow(mv))
			}
		}
		for _, s := range m.series {
			if strings.Contains(strings.ToLower(s.Name), q) {
				results = append(results, tvRow(s))
			}
		}
		// Include a person result to prove it's filtered out.
		results = append(results, map[string]any{"id": 9999, "media_type": "person", "name": "Some Actor"})
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
	mux.HandleFunc("GET /search/keyword", func(w http.ResponseWriter, r *http.Request) {
		q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
		ids := make([]int, 0, len(m.keywords))
		for id := range m.keywords {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		var results []map[string]any
		for _, id := range ids {
			if strings.Contains(strings.ToLower(m.keywords[id]), q) {
				results = append(results, map[string]any{"id": id, "name": m.keywords[id]})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"page": 1, "total_pages": 1, "results": results})
	})
	mux.HandleFunc("GET /search/person", func(w http.ResponseWriter, r *http.Request) {
		q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
		ids := make([]int, 0, len(m.people))
		for id := range m.people {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		results := make([]map[string]any, 0)
		for _, id := range ids {
			if strings.Contains(strings.ToLower(m.people[id]), q) {
				results = append(results, map[string]any{"id": id, "name": m.people[id]})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"page": 1, "total_pages": 1, "results": results})
	})
	mux.HandleFunc("GET /p/exports/{file}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		zipped := gzip.NewWriter(w)
		ids := make([]int, 0, len(m.networks))
		for id := range m.networks {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			network := m.networks[id]
			_ = json.NewEncoder(zipped).Encode(map[string]any{"id": network.ID, "name": network.Name})
		}
		_ = zipped.Close()
	})
	mux.HandleFunc("GET /network/{id}", func(w http.ResponseWriter, r *http.Request) {
		network, ok := m.networks[atoiPath(r.PathValue("id"))]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": network.ID, "name": network.Name, "origin_country": network.OriginCountry})
	})
	// /discover/{movie,tv}: filter the catalog by with_genres (§8 discovery path).
	// A comma/pipe-separated with_genres matches any listed genre id; empty = all.
	mux.HandleFunc("GET /discover/movie", func(w http.ResponseWriter, r *http.Request) {
		want := parseGenreParam(r.URL.Query().Get("with_genres"))
		wantKeywords := parseGenreParam(r.URL.Query().Get("with_keywords"))
		var results []map[string]any
		for _, mv := range sortedTitles(m.movies) {
			if allIDsMatch(mv.GenreIDs, want) && anyIDMatches(mv.KeywordIDs, wantKeywords) &&
				allIDsMatch(mv.CastIDs, parseGenreParam(r.URL.Query().Get("with_cast"))) &&
				allIDsMatch(mv.CreatorIDs, parseGenreParam(r.URL.Query().Get("with_crew"))) &&
				matchesDiscoveryQualifiers(mv, r.URL.Query()) {
				results = append(results, movieRow(mv))
			}
		}
		results, page, totalPages := tmdbPage(results, r.URL.Query().Get("page"))
		_ = json.NewEncoder(w).Encode(map[string]any{"page": page, "total_pages": totalPages, "results": results})
	})
	mux.HandleFunc("GET /discover/tv", func(w http.ResponseWriter, r *http.Request) {
		want := parseGenreParam(r.URL.Query().Get("with_genres"))
		wantKeywords := parseGenreParam(r.URL.Query().Get("with_keywords"))
		var results []map[string]any
		for _, s := range sortedTitles(m.series) {
			if allIDsMatch(s.GenreIDs, want) && anyIDMatches(s.KeywordIDs, wantKeywords) &&
				matchesNetwork(s.NetworkID, parseGenreParam(r.URL.Query().Get("with_networks"))) &&
				matchesDiscoveryQualifiers(s, r.URL.Query()) {
				results = append(results, tvRow(s))
			}
		}
		results, page, totalPages := tmdbPage(results, r.URL.Query().Get("page"))
		_ = json.NewEncoder(w).Encode(map[string]any{"page": page, "total_pages": totalPages, "results": results})
	})
	// /{movie,tv}/{id}/recommendations: the §8.3 adjacency graph. Registered BEFORE the
	// bare /{id} routes so the more specific pattern wins.
	mux.HandleFunc("GET /movie/{id}/recommendations", func(w http.ResponseWriter, r *http.Request) {
		m.recommendHandler(w, r, m.movies)
	})
	mux.HandleFunc("GET /tv/{id}/recommendations", func(w http.ResponseWriter, r *http.Request) {
		m.recommendHandler(w, r, m.series)
	})
	mux.HandleFunc("GET /movie/{id}", func(w http.ResponseWriter, r *http.Request) {
		m.existsHandler(w, r, m.movies)
	})
	mux.HandleFunc("GET /tv/{id}", func(w http.ResponseWriter, r *http.Request) {
		m.existsHandler(w, r, m.series)
	})
	// Content ratings (§389 acquisition enrichment). A US entry iff the title scripts
	// a USRating; an empty results array is the realistic sparse-coverage answer.
	mux.HandleFunc("GET /tv/{id}/content_ratings", func(w http.ResponseWriter, r *http.Request) {
		results := []map[string]any{}
		if t, ok := m.series[atoiPath(r.PathValue("id"))]; ok && t.USRating != "" {
			results = append(results, map[string]any{"iso_3166_1": "US", "rating": t.USRating})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
	mux.HandleFunc("GET /movie/{id}/release_dates", func(w http.ResponseWriter, r *http.Request) {
		results := []map[string]any{}
		if t, ok := m.movies[atoiPath(r.PathValue("id"))]; ok && t.USRating != "" {
			results = append(results, map[string]any{
				"iso_3166_1":    "US",
				"release_dates": []map[string]any{{"certification": t.USRating}},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := TMDBRequest{
			Path:          r.URL.Path,
			RawQuery:      r.URL.RawQuery,
			Authorization: r.Header.Get("Authorization"),
		}
		m.mu.Lock()
		m.requests = append(m.requests, request)
		m.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *TMDB) existsHandler(w http.ResponseWriter, r *http.Request, cat map[int]tmdbTitle) {
	id := atoiPath(r.PathValue("id"))
	if t, ok := cat[id]; ok {
		row := map[string]any{"id": t.ID, "title": t.Name, "belongs_to_collection": nil}
		if t.CollectionID > 0 {
			row["belongs_to_collection"] = map[string]any{"id": t.CollectionID}
		}
		_ = json.NewEncoder(w).Encode(row)
		return
	}
	w.WriteHeader(http.StatusNotFound) // fabricated id → 404 (grounding drops it)
}

func atoiPath(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// movieRow / tvRow render a title as a /search or /discover result row, now
// carrying genre_ids + overview (§8 enrichment).
func movieRow(mv tmdbTitle) map[string]any {
	return map[string]any{
		"id": mv.ID, "media_type": "movie", "title": mv.Name, "release_date": mv.Date,
		"genre_ids": mv.GenreIDs, "overview": mv.Overview,
		"original_language": mv.OriginalLanguage, "origin_country": mv.OriginCountries,
		"vote_average": mv.VoteAverage, "vote_count": mv.VoteCount,
	}
}

func tvRow(s tmdbTitle) map[string]any {
	return map[string]any{
		"id": s.ID, "media_type": "tv", "name": s.Name, "first_air_date": s.Date,
		"genre_ids": s.GenreIDs, "overview": s.Overview,
		"original_language": s.OriginalLanguage, "origin_country": s.OriginCountries,
		"vote_average": s.VoteAverage, "vote_count": s.VoteCount,
	}
}

func sortedTitles(catalog map[int]tmdbTitle) []tmdbTitle {
	ids := make([]int, 0, len(catalog))
	for id := range catalog {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]tmdbTitle, 0, len(ids))
	for _, id := range ids {
		out = append(out, catalog[id])
	}
	return out
}

func matchesDiscoveryQualifiers(title tmdbTitle, query url.Values) bool {
	if want := query.Get("with_original_language"); want != "" && !strings.EqualFold(title.OriginalLanguage, want) {
		return false
	}
	if want := query.Get("with_origin_country"); want != "" {
		matched := false
		for _, country := range title.OriginCountries {
			matched = matched || strings.EqualFold(country, want)
		}
		if !matched {
			return false
		}
	}
	if minimum := atoiPath(query.Get("with_runtime.gte")); minimum > 0 && title.RuntimeMinutes < minimum {
		return false
	}
	if maximum := atoiPath(query.Get("with_runtime.lte")); maximum > 0 && title.RuntimeMinutes > maximum {
		return false
	}
	if minimum, _ := strconv.ParseFloat(query.Get("vote_average.gte"), 64); minimum > 0 && title.VoteAverage < minimum {
		return false
	}
	if minimum := atoiPath(query.Get("vote_count.gte")); minimum > 0 && title.VoteCount < minimum {
		return false
	}
	return true
}

// TMDB discovery pages contain at most twenty results. Modelling that boundary
// is load-bearing: a client that asks for forty candidates but never advances
// page=2 has still only widened its local slice, not its real discovery corpus.
func tmdbPage(results []map[string]any, rawPage string) ([]map[string]any, int, int) {
	const pageSize = 20
	page := atoiPath(rawPage)
	if page <= 0 {
		page = 1
	}
	totalPages := max(1, (len(results)+pageSize-1)/pageSize)
	start := (page - 1) * pageSize
	if start >= len(results) {
		return nil, page, totalPages
	}
	end := min(start+pageSize, len(results))
	return results[start:end], page, totalPages
}

// parseGenreParam splits TMDB's with_genres (comma = OR, pipe = OR here) into ids.
func parseGenreParam(v string) []int {
	if v == "" {
		return nil
	}
	var out []int
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '|' }) {
		if id := atoiPath(strings.TrimSpace(part)); id != 0 {
			out = append(out, id)
		}
	}
	return out
}

// allIDsMatch models TMDB's comma-separated with_genres semantics (AND).
func allIDsMatch(have, want []int) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		found := false
		for _, h := range have {
			if h == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// anyIDMatches models pipe-separated with_keywords alternatives (OR).
func anyIDMatches(have, want []int) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		for _, h := range have {
			if h == w {
				return true
			}
		}
	}
	return false
}

func matchesNetwork(have int, want []int) bool {
	return len(want) == 0 || len(want) == 1 && have == want[0]
}

// recommendHandler serves the §8.3 adjacency graph for one seed. An unwired seed returns
// an empty result list (200), matching TMDB's answer for a title with no neighbours —
// the case the catalog walk must skip rather than fail on.
func (m *TMDB) recommendHandler(w http.ResponseWriter, r *http.Request, from map[int]tmdbTitle) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	var results []map[string]any
	for _, recID := range m.recommends[id] {
		if t, ok := from[recID]; ok {
			if _, isSeries := m.series[recID]; isSeries {
				results = append(results, tvRow(t))
			} else {
				results = append(results, movieRow(t))
			}
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
}
