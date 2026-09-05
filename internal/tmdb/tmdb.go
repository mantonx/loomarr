// Package tmdb is the TMDB adapter (design §8 grounding): the TMDB-scope corpus
// for the catalog and the exists-check for acquisition validation. TMDB grounds
// the suggester — the LLM selects from real TMDB ids, and every acquisition is
// re-validated against TMDB (exists) before it's actionable (§8).
//
// Built against TMDB's documented v3 API (api.themoviedb.org/3); the live fixture
// capture is deferred (no TMDB_API_KEY this session) — see
// testkit/fixtures/llm/FINDINGS.md. When a key is supplied, pin real fixtures and
// reconcile any shape difference doc-first.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/httpx"
	"github.com/loomarr/loomarr/internal/metrics"
	"github.com/loomarr/loomarr/internal/provision"
)

// Client is the TMDB v3 client. The api key rides as a bearer token (v4 auth) or
// the api_key query param (v3) — we use the Authorization bearer form so the key
// never lands in a URL/log (§6 anti-leak discipline, applied to TMDB too).
type Client struct {
	baseURL              string
	networkExportBaseURL string
	apiKey               func() string
	http                 *http.Client
	networkMu            sync.Mutex
	networkIdentities    []namedIdentity
}

// ErrAPIKeyRequired reports that an operation could not start because TMDB is
// not configured. It is returned before any request is built or sent.
var ErrAPIKeyRequired = errors.New("tmdb api key is required")

type operationAPIKey struct {
	value string
}

// New builds a TMDB client. baseURL defaults to the public API host.
func New(apiKey string) *Client {
	return NewDynamic(func() string { return apiKey })
}

// NewWithBase is for tests: point at a mock TMDB server.
func NewWithBase(baseURL, apiKey string) *Client {
	return NewDynamicWithBase(baseURL, func() string { return apiKey })
}

// NewDynamic builds a TMDB client whose API key is resolved once at the start
// of every exported operation. A settings change therefore applies to the next
// operation without allowing one multi-request operation to mix credentials.
func NewDynamic(apiKey func() string) *Client {
	return NewDynamicWithBase("https://api.themoviedb.org/3", apiKey)
}

// NewDynamicWithBase is NewDynamic with an alternate endpoint for tests and
// development overrides. The base URL is fixed for the client's lifetime; only
// the credential is live configuration.
func NewDynamicWithBase(baseURL string, apiKey func() string) *Client {
	return newDynamicWithHTTP(baseURL, apiKey, httpx.NewNamed("tmdb", httpx.TimeoutTMDB))
}

// NewDynamicObserved binds outbound observations to one application generation.
func NewDynamicObserved(apiKey func() string, recorder *metrics.Recorder) *Client {
	return NewDynamicWithBaseObserved("https://api.themoviedb.org/3", apiKey, recorder)
}

// NewDynamicWithBaseObserved is NewDynamicObserved with an alternate endpoint.
func NewDynamicWithBaseObserved(
	baseURL string,
	apiKey func() string,
	recorder *metrics.Recorder,
) *Client {
	return newDynamicWithHTTP(
		baseURL, apiKey, httpx.NewNamedObserved("tmdb", httpx.TimeoutTMDB, recorder),
	)
}

// newDynamicWithHTTP is the adapter's in-process transport seam. Production
// constructors keep ownership of policy-bearing httpx clients; unit tests can
// exercise the same TMDB interface with a no-network RoundTripper.
func newDynamicWithHTTP(baseURL string, apiKey func() string, httpClient *http.Client) *Client {
	if apiKey == nil {
		apiKey = func() string { return "" }
	}
	baseURL = strings.TrimRight(baseURL, "/")
	exportBaseURL := baseURL + "/p/exports"
	if baseURL == "https://api.themoviedb.org/3" {
		exportBaseURL = "https://files.tmdb.org/p/exports"
	}
	return &Client{baseURL: baseURL, networkExportBaseURL: exportBaseURL, apiKey: apiKey, http: httpClient}
}

// operation snapshots the live credential once, or reuses the snapshot when
// an exported operation delegates to another exported operation.
func (c *Client) operation(ctx context.Context) (context.Context, error) {
	if snapshot, ok := ctx.Value(operationAPIKey{}).(operationAPIKey); ok {
		if snapshot.value == "" {
			return ctx, ErrAPIKeyRequired
		}
		return ctx, nil
	}
	key := strings.TrimSpace(c.apiKey())
	if key == "" {
		return ctx, ErrAPIKeyRequired
	}
	return context.WithValue(ctx, operationAPIKey{}, operationAPIKey{value: key}), nil
}

// multiResult is one /search/multi row. media_type distinguishes movie/tv/person;
// we keep only movie+tv. Movies carry `title`/`release_date`, tv `name`/
// `first_air_date`.
type multiResult struct {
	ID            int    `json:"id"`
	MediaType     string `json:"media_type"`
	Title         string `json:"title"`
	Name          string `json:"name"`
	ReleaseDate   string `json:"release_date"`
	FirstAirDate  string `json:"first_air_date"`
	OriginalTitle string `json:"original_title"`
	// Enrichment (§8): TMDB already returns these on /search/multi + /discover; we
	// now parse them so the model reasons about theme (genre/overview), not titles.
	GenreIDs         []int    `json:"genre_ids"`
	Overview         string   `json:"overview"`
	OriginalLanguage string   `json:"original_language"`
	OriginCountries  []string `json:"origin_country"`
	VoteAverage      float64  `json:"vote_average"`
	VoteCount        int      `json:"vote_count"`
}

type multiResponse struct {
	Results []multiResult `json:"results"`
}

type keywordResult struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type keywordResponse struct {
	Results []keywordResult `json:"results"`
}

// Search implements catalog.TMDBSearcher: GET /search/multi?query=<q>, mapping
// movie/tv results to Candidates with real TMDB ids (§8 grounding). Person
// results are dropped. in_library is false here — the catalog sets it by merging
// with library results.
func (c *Client) Search(ctx context.Context, term string, limit int) ([]catalog.Candidate, error) {
	ctx, err := c.operation(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("query", term)
	q.Set("include_adult", "false")

	var resp multiResponse
	if err := c.get(ctx, "/search/multi?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	out := make([]catalog.Candidate, 0, len(resp.Results))
	for _, r := range resp.Results {
		mt, ok := mediaType(r.MediaType)
		if !ok {
			continue // person / unknown
		}
		name := r.Title
		date := r.ReleaseDate
		if mt == provision.Series {
			name = r.Name
			date = r.FirstAirDate
		}
		out = append(out, catalog.Candidate{
			MediaType:        mt,
			TMDBID:           r.ID,
			Name:             name,
			Year:             yearFromDate(date),
			InLibrary:        false,
			Genres:           genreNames(r.GenreIDs),
			Overview:         r.Overview,
			OriginalLanguage: normalizedLanguage(r.OriginalLanguage),
			OriginCountries:  normalizedCodes(r.OriginCountries),
			VoteAverage:      validVoteAverage(r.VoteAverage, r.VoteCount),
			VoteCount:        validVoteCount(r.VoteAverage, r.VoteCount),
			Source:           catalog.ScopeTMDB,
			RelevanceRank:    len(out) + 1,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// discoverResult is one /discover row. Same shape as multiResult minus
// media_type (discover is per-endpoint: /discover/movie vs /discover/tv).
type discoverResult struct {
	ID               int      `json:"id"`
	Title            string   `json:"title"`
	Name             string   `json:"name"`
	ReleaseDate      string   `json:"release_date"`
	FirstAirDate     string   `json:"first_air_date"`
	GenreIDs         []int    `json:"genre_ids"`
	Overview         string   `json:"overview"`
	OriginalLanguage string   `json:"original_language"`
	OriginCountries  []string `json:"origin_country"`
	VoteAverage      float64  `json:"vote_average"`
	VoteCount        int      `json:"vote_count"`
}

type discoverResponse struct {
	Page       int              `json:"page"`
	TotalPages int              `json:"total_pages"`
	Results    []discoverResult `json:"results"`
}

// Discover finds titles through TMDB's structured movie/TV filters (§8). It
// resolves thematic keywords, maps human genres into endpoint-specific ids, and
// forwards already-validated scalar qualifiers. Movies + series are blended
// unless the request pins one media type.
func (c *Client) Discover(ctx context.Context, query catalog.DiscoveryQuery, limit int) ([]catalog.Candidate, error) {
	ctx, err := c.operation(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if err := validateEntityQuery(query); err != nil {
		return nil, err
	}
	var keywordIDs []int
	var keywordNames []string
	if len(query.Keywords) > 0 {
		keywordIDs, keywordNames, err = c.resolveKeywordIDs(ctx, query.Keywords)
		if err != nil || len(keywordIDs) == 0 {
			return nil, err
		}
	}
	entities, err := c.resolveDiscoveryEntities(ctx, query)
	if err != nil {
		return nil, err
	}
	var movies, series []catalog.Candidate
	if query.MediaType == "" || query.MediaType == provision.Movie {
		rows, err := c.discover(ctx, "/discover/movie", genreIDsForMedia(query.Genres, provision.Movie), keywordIDs, entities, query, "primary_release_date", limit)
		if err != nil {
			return nil, err
		}
		movies = appendDiscover(movies, rows, provision.Movie)
		attachKeywords(movies, keywordNames)
		attachEntityEvidence(movies, entities)
	}
	if query.MediaType == "" || query.MediaType == provision.Series {
		rows, err := c.discover(ctx, "/discover/tv", genreIDsForMedia(query.Genres, provision.Series), keywordIDs, entities, query, "first_air_date", limit)
		if err != nil {
			return nil, err
		}
		series = appendDiscover(series, rows, provision.Series)
		attachKeywords(series, keywordNames)
		attachEntityEvidence(series, entities)
	}
	return blendMediaTypes(movies, series, limit), nil
}

// blendMediaTypes prevents the two-endpoint TMDB API from becoming an accidental
// movies-only result whenever /discover/movie fills the limit before TV rows are
// appended. An unpinned request gives each populated type half the bounded window;
// an undersubscribed side yields its unused slots to the other.
func blendMediaTypes(movies, series []catalog.Candidate, limit int) []catalog.Candidate {
	if limit <= 0 {
		limit = 20
	}
	if len(movies) == 0 {
		return append([]catalog.Candidate(nil), series[:min(limit, len(series))]...)
	}
	if len(series) == 0 {
		return append([]catalog.Candidate(nil), movies[:min(limit, len(movies))]...)
	}
	movieSlots := min((limit+1)/2, len(movies))
	seriesSlots := min(limit/2, len(series))
	if movieSlots+seriesSlots < limit {
		movieSlots = min(len(movies), limit-seriesSlots)
		seriesSlots = min(len(series), limit-movieSlots)
	}
	out := make([]catalog.Candidate, 0, movieSlots+seriesSlots)
	// Movie and TV discovery are separate ranked endpoints. Interleave equal
	// source ranks so a later catalog blend can preserve relevance without one
	// endpoint consuming the entire mixed-media window.
	for i := 0; i < max(movieSlots, seriesSlots); i++ {
		if i < movieSlots {
			out = append(out, movies[i])
		}
		if i < seriesSlots {
			out = append(out, series[i])
		}
	}
	for i := range out {
		out[i].RelevanceRank = i + 1
	}
	return out
}

func (c *Client) resolveKeywordIDs(ctx context.Context, keywords []string) ([]int, []string, error) {
	seen := map[int]bool{}
	ids := make([]int, 0, len(keywords))
	names := make([]string, 0, len(keywords))
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		q := url.Values{}
		q.Set("query", keyword)
		q.Set("page", "1")
		var resp keywordResponse
		if err := c.get(ctx, "/search/keyword?"+q.Encode(), &resp); err != nil {
			return nil, nil, err
		}
		if len(resp.Results) == 0 {
			continue
		}
		chosen := resp.Results[0]
		for _, candidate := range resp.Results {
			if strings.EqualFold(strings.TrimSpace(candidate.Name), keyword) {
				chosen = candidate
				break
			}
		}
		name := strings.TrimSpace(chosen.Name)
		if chosen.ID > 0 && name != "" && !seen[chosen.ID] {
			seen[chosen.ID] = true
			ids = append(ids, chosen.ID)
			names = append(names, name)
		}
	}
	return ids, names, nil
}

// Recommendations returns TMDB's behavioural neighbours for one title — the
// "people who watched this also watched…" graph (programming-design §8.3).
//
// ⚠ /recommendations, NEVER /similar. The two endpoints read as interchangeable and
// are not: /similar is computed from genre+keyword overlap and is effectively noise
// (probing the dev channel, it returned "Land of the Blind" for Die Hard, The
// Terminator AND RoboCop, and "A Man Escaped" for Die Hard). /recommendations is
// behavioural and coherent (Terminator → Doomsday, Replicant, Hardware). Swapping
// them would produce baffling channels that read as a Loomarr bug rather than as a
// bad data source.
//
// A title with no neighbours (obscure, or newly added to TMDB) returns an empty
// slice, not an error — one unproductive seed must not fail a whole adjacency walk.
func (c *Client) Recommendations(ctx context.Context, mt provision.MediaType, tmdbID, limit int) ([]catalog.Candidate, error) {
	ctx, err := c.operation(ctx)
	if err != nil {
		return nil, err
	}
	path := "/movie/"
	if mt == provision.Series {
		path = "/tv/"
	}
	var resp discoverResponse
	if err := c.get(ctx, fmt.Sprintf("%s%d/recommendations", path, tmdbID), &resp); err != nil {
		return nil, err
	}
	out := appendDiscover(nil, resp.Results, mt)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (c *Client) discover(
	ctx context.Context,
	path string,
	genreIDs []int,
	keywordIDs []int,
	entities resolvedDiscoveryEntities,
	query catalog.DiscoveryQuery,
	dateField string,
	limit int,
) ([]discoverResult, error) {
	q := url.Values{}
	q.Set("include_adult", "false")
	q.Set("sort_by", "popularity.desc")
	if len(genreIDs) > 0 {
		parts := make([]string, len(genreIDs))
		for i, id := range genreIDs {
			parts[i] = strconv.Itoa(id)
		}
		q.Set("with_genres", strings.Join(parts, ",")) // comma = AND
	}
	if len(keywordIDs) > 0 {
		parts := make([]string, len(keywordIDs))
		for i, id := range keywordIDs {
			parts[i] = strconv.Itoa(id)
		}
		q.Set("with_keywords", strings.Join(parts, "|")) // pipe = OR
	}
	if entities.networkID > 0 {
		q.Set("with_networks", strconv.Itoa(entities.networkID))
	}
	if len(entities.castIDs) > 0 {
		q.Set("with_cast", joinIDs(entities.castIDs))
	}
	if len(entities.creatorIDs) > 0 {
		q.Set("with_crew", joinIDs(entities.creatorIDs))
	}
	if query.YearFrom > 0 {
		q.Set(dateField+".gte", fmt.Sprintf("%04d-01-01", query.YearFrom))
	}
	if query.YearTo > 0 {
		q.Set(dateField+".lte", fmt.Sprintf("%04d-12-31", query.YearTo))
	}
	if query.OriginalLanguage != "" {
		q.Set("with_original_language", query.OriginalLanguage)
	}
	if query.OriginCountry != "" {
		q.Set("with_origin_country", query.OriginCountry)
	}
	if query.RuntimeMin > 0 {
		q.Set("with_runtime.gte", strconv.Itoa(query.RuntimeMin))
	}
	if query.RuntimeMax > 0 {
		q.Set("with_runtime.lte", strconv.Itoa(query.RuntimeMax))
	}
	if query.VoteAverageMin > 0 {
		q.Set("vote_average.gte", strconv.FormatFloat(query.VoteAverageMin, 'f', -1, 64))
	}
	if query.VoteCountMin > 0 {
		q.Set("vote_count.gte", strconv.Itoa(query.VoteCountMin))
	}
	rows := make([]discoverResult, 0, limit)
	for page := 1; len(rows) < limit; page++ {
		q.Set("page", strconv.Itoa(page))
		var resp discoverResponse
		if err := c.get(ctx, path+"?"+q.Encode(), &resp); err != nil {
			return nil, err
		}
		rows = append(rows, resp.Results...)
		if len(resp.Results) == 0 || (resp.TotalPages > 0 && page >= resp.TotalPages) {
			break
		}
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func appendDiscover(out []catalog.Candidate, rows []discoverResult, mt provision.MediaType) []catalog.Candidate {
	for _, r := range rows {
		name, date := r.Title, r.ReleaseDate
		if mt == provision.Series {
			name, date = r.Name, r.FirstAirDate
		}
		out = append(out, catalog.Candidate{
			MediaType: mt, TMDBID: r.ID, Name: name, Year: yearFromDate(date),
			InLibrary: false, Genres: genreNames(r.GenreIDs), Overview: r.Overview,
			OriginalLanguage: normalizedLanguage(r.OriginalLanguage),
			OriginCountries:  normalizedCodes(r.OriginCountries),
			VoteAverage:      validVoteAverage(r.VoteAverage, r.VoteCount),
			VoteCount:        validVoteCount(r.VoteAverage, r.VoteCount),
			Source:           catalog.ScopeTMDB,
			RelevanceRank:    len(out) + 1,
		})
	}
	return out
}

func attachKeywords(candidates []catalog.Candidate, keywords []string) {
	// Discover's OR query proves that each row matched at least one requested
	// keyword, but it does not identify which one. Only a singleton query supports
	// row-level attribution without a follow-up details request.
	if len(keywords) != 1 {
		return
	}
	for i := range candidates {
		candidates[i].Keywords = append([]string(nil), keywords...)
	}
}

func normalizedCodes(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if len(value) != 2 || !asciiLetters(value) || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func normalizedLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if (len(value) != 2 && len(value) != 3) || !asciiLetters(value) {
		return ""
	}
	return value
}

func asciiLetters(value string) bool {
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') {
			return false
		}
	}
	return true
}

func validVoteAverage(average float64, count int) float64 {
	if !validVote(average, count) {
		return 0
	}
	return average
}

func validVoteCount(average float64, count int) int {
	if !validVote(average, count) {
		return 0
	}
	return count
}

func validVote(average float64, count int) bool {
	return count > 0 && !math.IsNaN(average) && !math.IsInf(average, 0) && average >= 0 && average <= 10
}

// genreIDsFor maps human genre names (case-insensitive) to TMDB ids, dropping
// unknowns. Used by Discover to translate an intent's genre terms.
func genreIDsForMedia(names []string, mt provision.MediaType) []int {
	seen := map[int]bool{}
	var out []int
	for _, n := range names {
		if id, ok := genreIDByName[canonGenre(n)]; ok {
			switch mt {
			case provision.Series:
				switch id {
				case 28, 12: // movie Action / Adventure → TV Action & Adventure
					id = 10759
				case 14, 878: // movie Fantasy / Science Fiction → TV Sci-Fi & Fantasy
					id = 10765
				case 10751: // movie Family → TV Kids
					id = 10762
				}
			case provision.Movie:
				switch id {
				case 10759:
					id = 28
				case 10765:
					id = 878
				case 10762:
					id = 10751
				}
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Exists re-validates an acquisition against TMDB (§8): GET /movie/{id} or
// /tv/{id}; a 200 means the id is real. A 404 means the LLM proposed a
// non-existent id — the acquisition must be dropped, never actioned.
func (c *Client) Exists(ctx context.Context, mt provision.MediaType, tmdbID int) (bool, error) {
	ctx, err := c.operation(ctx)
	if err != nil {
		return false, err
	}
	if tmdbID <= 0 {
		return false, nil
	}
	path := "/movie/" + strconv.Itoa(tmdbID)
	if mt == provision.Series {
		path = "/tv/" + strconv.Itoa(tmdbID)
	}
	status, err := c.getStatus(ctx, path, nil)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("tmdb exists %s: status %d", path, status)
	}
}

// CollectionID returns the TMDB collection (franchise) a MOVIE belongs to — the id of
// belongs_to_collection on GET /movie/{id} — so the scheduler can keep a franchise's films
// together, in release order (§5). Returns 0 (no error) for a standalone movie or a series
// (TV has no belongs_to_collection). This is the authoritative franchise signal: it groups
// "Raiders of the Lost Ark" with the "Indiana Jones and the…" films even though they share
// no title base, which a title heuristic can't do. Fetched at reconcile-heal time and
// stamped onto the lineup entry (like the rating heal), so the pure scheduler stays I/O-free.
func (c *Client) CollectionID(ctx context.Context, mt provision.MediaType, tmdbID int) (int, error) {
	ctx, err := c.operation(ctx)
	if err != nil {
		return 0, err
	}
	if tmdbID <= 0 || mt == provision.Series {
		return 0, nil
	}
	var body struct {
		BelongsToCollection *struct {
			ID int `json:"id"`
		} `json:"belongs_to_collection"`
	}
	if err := c.get(ctx, "/movie/"+strconv.Itoa(tmdbID), &body); err != nil {
		return 0, err
	}
	if body.BelongsToCollection != nil {
		return body.BelongsToCollection.ID, nil
	}
	return 0, nil
}

// ContentRating returns the US content rating for a title — TV series via
// /tv/{id}/content_ratings (the TV-* certifications), movies via
// /movie/{id}/release_dates (the MPAA certification on a US release). It exists so a
// not-yet-owned acquisition can still carry a rating: the library cannot rate a title
// it does not have, and under an audience ceiling an unrated entry is dropped
// (dead air, §9). Returns "" (no error) when TMDB has no US rating — sparse coverage
// is normal, so an empty answer is a legitimate result, not a failure.
func (c *Client) ContentRating(ctx context.Context, mt provision.MediaType, tmdbID int) (string, error) {
	ctx, err := c.operation(ctx)
	if err != nil {
		return "", err
	}
	if tmdbID <= 0 {
		return "", nil
	}
	if mt == provision.Series {
		var body struct {
			Results []struct {
				ISO    string `json:"iso_3166_1"`
				Rating string `json:"rating"`
			} `json:"results"`
		}
		if err := c.get(ctx, "/tv/"+strconv.Itoa(tmdbID)+"/content_ratings", &body); err != nil {
			return "", err
		}
		for _, r := range body.Results {
			if r.ISO == "US" {
				return strings.TrimSpace(r.Rating), nil
			}
		}
		return "", nil
	}
	var body struct {
		Results []struct {
			ISO          string `json:"iso_3166_1"`
			ReleaseDates []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	}
	if err := c.get(ctx, "/movie/"+strconv.Itoa(tmdbID)+"/release_dates", &body); err != nil {
		return "", err
	}
	for _, r := range body.Results {
		if r.ISO != "US" {
			continue
		}
		for _, d := range r.ReleaseDates {
			if cert := strings.TrimSpace(d.Certification); cert != "" {
				return cert, nil
			}
		}
	}
	return "", nil
}

// imageBase is TMDB's image CDN at the ORIGINAL size. TMDB's /configuration endpoint returns
// the canonical base, but it's stable and documented, so we hardcode it (one fewer round-trip)
// — same pragmatism as the other pinned TMDB shapes.
//
// ⚠ **`original`, not `w500`, since V52 phase 7 — and the width choice moved for a reason worth
// keeping.** These URLs used to be handed straight to a browser and to Tunarr, so a mid-size
// rendition was the right compromise: crisp on a guide tile without being a full-res download.
// They are no longer fetched by any client. Every caller now hands the URL to `images.Adopt`,
// which downloads it once server-side and generates the whole width ladder locally (§22) — so
// asking TMDB for a pre-shrunk copy would throw away the resolution the ladder's larger rungs
// need, and would make our 780px poster an upscale of their 500px one.
//
// It also means these URLs are an INTERNAL detail now: they are what we store as `source_url`,
// never what a page loads. Removing third-party origins from the operator's browser is the whole
// point of §22, so a caller that cannot adopt returns no image rather than falling back to a
// hot-link.
const imageBase = "https://image.tmdb.org/t/p/original"

// PosterURL returns a full, directly-fetchable poster image URL for a title (§icon), or ""
// (no error) when TMDB has no poster — sparse coverage is normal, so an empty answer is a
// legitimate result the caller falls back on (no icon), not a failure. Used to give a
// channel a themed icon from its primary series/movie. A poster (portrait) reads better as a
// channel tile than a backdrop.
//
// ⚠ The caller ADOPTS this URL (§22); nothing fetches it from a browser. This comment used to
// say "Tunarr fetches this URL directly, so no proxying is needed" — true until V52 phase 7,
// when the image service became what Tunarr and the operator's browser both fetch from.
func (c *Client) PosterURL(ctx context.Context, mt provision.MediaType, tmdbID int) (string, error) {
	ctx, err := c.operation(ctx)
	if err != nil {
		return "", err
	}
	if tmdbID <= 0 {
		return "", nil
	}
	path := "/movie/" + strconv.Itoa(tmdbID)
	if mt == provision.Series {
		path = "/tv/" + strconv.Itoa(tmdbID)
	}
	var body struct {
		PosterPath string `json:"poster_path"`
	}
	if err := c.get(ctx, path, &body); err != nil {
		return "", err
	}
	if p := strings.TrimSpace(body.PosterPath); p != "" {
		return imageBase + p, nil // poster_path already has a leading "/"
	}
	return "", nil
}

// BackdropURL returns a title's landscape artwork, or "" when TMDB has none. Backdrops are the
// movie-level counterpart to episode stills: both are 16:9 and therefore share one preview shape
// in the Guide and Watch timeline. PosterURL remains separate because portrait artwork is still
// the right source for channel icons and title tiles.
//
// As with PosterURL, the caller adopts this original-size URL into Loomarr's image service; it is
// never handed to an operator's browser.
func (c *Client) BackdropURL(ctx context.Context, mt provision.MediaType, tmdbID int) (string, error) {
	ctx, err := c.operation(ctx)
	if err != nil {
		return "", err
	}
	if tmdbID <= 0 {
		return "", nil
	}
	path := "/movie/" + strconv.Itoa(tmdbID)
	if mt == provision.Series {
		path = "/tv/" + strconv.Itoa(tmdbID)
	}
	var body struct {
		BackdropPath string `json:"backdrop_path"`
	}
	if err := c.get(ctx, path, &body); err != nil {
		return "", err
	}
	if p := strings.TrimSpace(body.BackdropPath); p != "" {
		return imageBase + p, nil // backdrop_path already has a leading "/"
	}
	return "", nil
}

// PosterURLByTVDB resolves a TVDB series id to a TMDB poster via the /find bridge
// (/find/{tvdb_id}?external_source=tvdb_id → the matching tv result's poster_path). Our
// series are often TVDB-keyed (Seerr's canonical id for series), but posters live on TMDB;
// this is the one-hop bridge. Returns "" (no error) when no match / no poster.
func (c *Client) PosterURLByTVDB(ctx context.Context, tvdbID int) (string, error) {
	ctx, err := c.operation(ctx)
	if err != nil {
		return "", err
	}
	if tvdbID <= 0 {
		return "", nil
	}
	var body struct {
		TVResults []struct {
			PosterPath string `json:"poster_path"`
		} `json:"tv_results"`
	}
	if err := c.get(ctx, "/find/"+strconv.Itoa(tvdbID)+"?external_source=tvdb_id", &body); err != nil {
		return "", err
	}
	for _, r := range body.TVResults {
		if p := strings.TrimSpace(r.PosterPath); p != "" {
			return imageBase + p, nil
		}
	}
	return "", nil
}

// EpisodeStillURLByTVDB resolves a TVDB series id to a per-episode still, via the same /find bridge
// PosterURLByTVDB uses (/find/{tvdb_id}?external_source=tvdb_id → the tv result's TMDB id) and then
// EpisodeStillURL for that id + season/episode. Series are usually TVDB-keyed (§3 series key), so the
// live-TV timeline needs this to show an episode thumbnail for them. Best-effort throughout: no TVDB
// match, no TMDB id, or no still all return "" + nil, never a hard failure.
func (c *Client) EpisodeStillURLByTVDB(ctx context.Context, tvdbID, season, episode int) (string, error) {
	ctx, err := c.operation(ctx)
	if err != nil {
		return "", err
	}
	if tvdbID <= 0 {
		return "", nil
	}
	var body struct {
		TVResults []struct {
			ID int `json:"id"` // the TMDB series id — the bridge from tvdb to tmdb
		} `json:"tv_results"`
	}
	if err := c.get(ctx, "/find/"+strconv.Itoa(tvdbID)+"?external_source=tvdb_id", &body); err != nil {
		return "", err
	}
	for _, r := range body.TVResults {
		if r.ID > 0 {
			return c.EpisodeStillURL(ctx, r.ID, season, episode)
		}
	}
	return "", nil
}

// EpisodeStillURL returns the absolute image URL of a series episode's still frame (the little
// preview image TMDB has per episode), or "" when TMDB has none for that episode. Best-effort like
// PosterURL: a not-found episode or an image-less one is "" + nil error, never a hard failure — the
// caller renders a fallback. Used by the live-TV timeline to show a per-episode thumbnail on hover.
func (c *Client) EpisodeStillURL(ctx context.Context, tmdbID, season, episode int) (string, error) {
	ctx, err := c.operation(ctx)
	if err != nil {
		return "", err
	}
	if tmdbID <= 0 {
		return "", nil
	}
	path := fmt.Sprintf("/tv/%d/season/%d/episode/%d", tmdbID, season, episode)
	var body struct {
		StillPath string `json:"still_path"`
	}
	status, err := c.getStatus(ctx, path, &body)
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound {
		return "", nil
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("tmdb GET %s: status %d", path, status)
	}
	if p := strings.TrimSpace(body.StillPath); p != "" {
		return imageBase + p, nil // still_path already has a leading "/"
	}
	return "", nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	status, err := c.getStatus(ctx, path, out)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("tmdb GET %s: status %d", path, status)
	}
	return nil
}

func (c *Client) getStatus(ctx context.Context, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, err
	}
	// Bearer auth (v4 token style) keeps the key out of the URL (§6 anti-leak).
	snapshot, ok := ctx.Value(operationAPIKey{}).(operationAPIKey)
	if !ok || snapshot.value == "" {
		return 0, ErrAPIKeyRequired
	}
	req.Header.Set("Authorization", "Bearer "+snapshot.value)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("tmdb GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("tmdb decode %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

// mediaType maps TMDB's media_type to provision's, reporting whether it's a kind
// we keep (movie/tv → true; person/other → false).
func mediaType(t string) (provision.MediaType, bool) {
	switch t {
	case "movie":
		return provision.Movie, true
	case "tv":
		return provision.Series, true
	default:
		return "", false
	}
}

// yearFromDate extracts the year from a TMDB date ("1994-06-10" → 1994).
func yearFromDate(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return y
}

var _ catalog.TMDBSearcher = (*Client)(nil)
