package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// registerSearch mounts GET /v1/search (§7.2). Any authenticated user may search
// (read-only); adding a missing result still routes through submit→approve, so
// search adds no new privilege surface (§7.2). This is the SAME catalog impl as
// the LLM grounding tool — humans and the model see identical results.
func (s *Server) registerSearch(api huma.API) {
	huma.Register(api, withRole(huma.Operation{
		OperationID: "search", Method: http.MethodGet, Path: "/v1/search",
		Summary: "Federated search (library + TMDB)", Tags: []string{"search"},
	}, RoleMember), s.doSearch)
}

type searchInput struct {
	Q string `query:"q" doc:"Title search terms. Omit when using structured discovery qualifiers."`
	// Clips are NOT a scope (§7.2): Candidate models a provisionable title, and a clip
	// is not one (§10). Clip search is GET /v1/filler?q=, which returns ClipDTOs.
	Scope            string   `query:"scope" enum:"library,tmdb,all" doc:"Corpus to search (default all). Clips are not searchable here — use /v1/filler?q="`
	Limit            int      `query:"limit" minimum:"1" doc:"Max results (default 20)"`
	MediaType        string   `query:"media_type" enum:"movie,series" doc:"Optional title-result narrowing, or the media type for structured discovery"`
	Genres           []string `query:"genres,explode" doc:"Structured genre terms; repeat the parameter for multiple values"`
	Keywords         []string `query:"keywords,explode" doc:"Structured thematic keyword terms; repeat the parameter for multiple values"`
	YearFrom         int      `query:"year_from" minimum:"1000" maximum:"9999" doc:"Earliest release or first-air year"`
	YearTo           int      `query:"year_to" minimum:"1000" maximum:"9999" doc:"Latest release or first-air year"`
	OriginalLanguage string   `query:"original_language" maxLength:"2" doc:"Two-letter original-language code"`
	OriginCountry    string   `query:"origin_country" maxLength:"2" doc:"Two-letter origin-country code"`
	RuntimeMin       int      `query:"runtime_min" minimum:"1" maximum:"1440" doc:"Minimum runtime in minutes"`
	RuntimeMax       int      `query:"runtime_max" minimum:"1" maximum:"1440" doc:"Maximum runtime in minutes"`
	VoteAverageMin   float64  `query:"vote_average_min" exclusiveMinimum:"0" maximum:"10" doc:"Minimum TMDB vote average"`
	VoteCountMin     int      `query:"vote_count_min" minimum:"1" maximum:"100000000" doc:"Minimum TMDB vote count"`
	Network          string   `query:"network" maxLength:"100" doc:"Exact TV network name; requires media_type=series"`
	Cast             []string `query:"cast,explode" maxItems:"4" doc:"Exact movie cast names; repeat the parameter for multiple values"`
	Creators         []string `query:"creators,explode" maxItems:"4" doc:"Exact movie director, writer, or crew names; repeat the parameter for multiple values"`
}
type searchOutput struct {
	Body struct {
		Candidates []SearchCandidate `json:"candidates"`
	}
}

func (s *Server) doSearch(ctx context.Context, in *searchInput) (*searchOutput, error) {
	if s.search == nil {
		return nil, errNotImplemented("Search isn't set up", "Connect your media library or add a TMDB API key in Settings to search for titles.")
	}
	request, err := normalizeSearchRequest(in)
	if err != nil {
		return nil, errBadRequest("Invalid search", err.Error())
	}
	if request.Discovery != nil {
		if request.Scope == "library" {
			return nil, errBadRequest("Invalid search scope", "Structured discovery requires the tmdb or all scope.")
		}
		if s.liveConfig != nil && strings.TrimSpace(s.liveConfig("tmdb.api_key")) == "" {
			return nil, errNotImplemented("TMDB search isn't set up", "Add a TMDB API key in Settings to discover titles.")
		}
	} else {
		scope, configured := s.configuredSearchScope(request.Scope)
		if !configured {
			switch normalizeSearchScope(request.Scope) {
			case "library":
				return nil, errNotImplemented("Library search isn't set up", "Connect your media library in Settings to search it.")
			case "tmdb":
				return nil, errNotImplemented("TMDB search isn't set up", "Add a TMDB API key in Settings to search TMDB.")
			default:
				return nil, errNotImplemented("Search isn't set up", "Connect your media library or add a TMDB API key in Settings to search for titles.")
			}
		}
		request.Scope = scope
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	request.Limit = limit
	cands, err := s.search.Search(ctx, request)
	if err != nil {
		return nil, apiErrWithCause(http.StatusBadGateway, "Search failed",
			"The search couldn't be completed. Check the configured title sources and try again.", err)
	}
	out := &searchOutput{}
	out.Body.Candidates = cands
	return out, nil
}

func normalizeSearchRequest(in *searchInput) (SearchRequest, error) {
	query := strings.TrimSpace(in.Q)
	discovery := &SearchDiscovery{
		MediaType: in.MediaType, Keywords: in.Keywords, Genres: in.Genres,
		YearFrom: in.YearFrom, YearTo: in.YearTo,
		OriginalLanguage: strings.ToLower(strings.TrimSpace(in.OriginalLanguage)),
		OriginCountry:    strings.ToUpper(strings.TrimSpace(in.OriginCountry)),
		RuntimeMin:       in.RuntimeMin, RuntimeMax: in.RuntimeMax,
		VoteAverageMin: in.VoteAverageMin, VoteCountMin: in.VoteCountMin,
		Network: strings.TrimSpace(in.Network),
	}
	var err error
	if discovery.Genres, err = normalizeSearchTerms(in.Genres, 0, 0, "genres"); err != nil {
		return SearchRequest{}, err
	}
	if discovery.Keywords, err = normalizeSearchTerms(in.Keywords, 0, 0, "keywords"); err != nil {
		return SearchRequest{}, err
	}
	if discovery.Cast, err = normalizeSearchTerms(in.Cast, 4, 100, "cast"); err != nil {
		return SearchRequest{}, err
	}
	if discovery.Creators, err = normalizeSearchTerms(in.Creators, 4, 100, "creators"); err != nil {
		return SearchRequest{}, err
	}
	if discovery.Network != "" && len([]rune(discovery.Network)) > 100 {
		return SearchRequest{}, fmt.Errorf("network must be at most 100 characters")
	}
	if discovery.OriginalLanguage != "" && !twoLetterCode(discovery.OriginalLanguage) {
		return SearchRequest{}, fmt.Errorf("original_language must be a two-letter code")
	}
	if discovery.OriginCountry != "" && !twoLetterCode(discovery.OriginCountry) {
		return SearchRequest{}, fmt.Errorf("origin_country must be a two-letter code")
	}
	if discovery.YearFrom > 0 && discovery.YearTo > 0 && discovery.YearFrom > discovery.YearTo {
		return SearchRequest{}, fmt.Errorf("year_from must not exceed year_to")
	}
	if discovery.RuntimeMin > 0 && discovery.RuntimeMax > 0 && discovery.RuntimeMin > discovery.RuntimeMax {
		return SearchRequest{}, fmt.Errorf("runtime_min must not exceed runtime_max")
	}
	hasPeople := len(discovery.Cast) > 0 || len(discovery.Creators) > 0
	if discovery.Network != "" && hasPeople {
		return SearchRequest{}, fmt.Errorf("network and person constraints cannot be combined")
	}
	if discovery.Network != "" && discovery.MediaType != "series" {
		return SearchRequest{}, fmt.Errorf("network requires media_type series")
	}
	if hasPeople && discovery.MediaType != "movie" {
		return SearchRequest{}, fmt.Errorf("cast and creators require media_type movie")
	}
	discoveryMode := len(discovery.Genres) > 0 || len(discovery.Keywords) > 0 ||
		discovery.YearFrom > 0 || discovery.YearTo > 0 || discovery.OriginalLanguage != "" ||
		discovery.OriginCountry != "" || discovery.RuntimeMin > 0 || discovery.RuntimeMax > 0 ||
		discovery.VoteAverageMin > 0 || discovery.VoteCountMin > 0 || discovery.Network != "" || hasPeople
	if query != "" && discoveryMode {
		return SearchRequest{}, fmt.Errorf("q cannot be combined with discovery qualifiers")
	}
	if query == "" && !discoveryMode {
		return SearchRequest{}, fmt.Errorf("provide q or a discovery qualifier")
	}
	request := SearchRequest{Query: query, MediaType: discovery.MediaType, Scope: normalizeSearchScope(in.Scope)}
	if discoveryMode {
		request.Discovery = discovery
	}
	return request, nil
}

func normalizeSearchTerms(values []string, maxItems, maxRunes int, name string) ([]string, error) {
	if maxItems > 0 && len(values) > maxItems {
		return nil, fmt.Errorf("%s must contain at most %d names", name, maxItems)
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, fmt.Errorf("%s must contain non-empty values", name)
		}
		if maxRunes > 0 && len([]rune(value)) > maxRunes {
			return nil, fmt.Errorf("%s values must be at most %d characters", name, maxRunes)
		}
		normalized := strings.ToLower(value)
		if seen[normalized] {
			return nil, fmt.Errorf("%s contains duplicate value %q", name, value)
		}
		seen[normalized] = true
		out = append(out, value)
	}
	return out, nil
}

func twoLetterCode(value string) bool {
	return len(value) == 2 && asciiLetter(value[0]) && asciiLetter(value[1])
}

func asciiLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func normalizeSearchScope(scope string) string {
	switch scope {
	case "library", "tmdb":
		return scope
	default:
		return "all"
	}
}

// configuredSearchScope narrows all/default to the corpora configured for this
// request. The composition root keeps both adapters alive, so a saved setting
// changes the next call without rebuilding the server. A nil liveConfig is the
// established unit-test convention: an explicitly wired fake is usable.
func (s *Server) configuredSearchScope(requested string) (string, bool) {
	scope := normalizeSearchScope(requested)
	libraryConfigured := !s.libraryUnconfigured()
	// A nil liveConfig is the established unit-test convention: an explicitly wired
	// adapter is usable. Production always supplies it, so the current key gates TMDB.
	tmdbConfigured := s.liveConfig == nil || strings.TrimSpace(s.liveConfig("tmdb.api_key")) != ""
	switch scope {
	case "library":
		return scope, libraryConfigured
	case "tmdb":
		return scope, tmdbConfigured
	default:
		switch {
		case libraryConfigured && tmdbConfigured:
			return "all", true
		case libraryConfigured:
			return "library", true
		case tmdbConfigured:
			return "tmdb", true
		default:
			return "all", false
		}
	}
}
