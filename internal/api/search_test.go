package api_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/testkit"
)

func newConfiguredSearchHandler(
	t *testing.T,
	cfg map[string]string,
) (http.Handler, *testkit.SearchService[api.SearchRequest, api.SearchCandidate]) {
	t.Helper()
	search := &testkit.SearchService[api.SearchRequest, api.SearchCandidate]{Results: []api.SearchCandidate{{
		MediaType: "movie", TMDBID: 603, Name: "The Matrix",
	}}}
	log := slog.New(slog.DiscardHandler)
	handler := api.Router(log, api.Options{
		Auth:   testAuthorizer{},
		Log:    log,
		Search: search,
		LiveConfig: func(key string) string {
			return cfg[key]
		},
		LibraryConfigured: func() bool {
			return cfg["library.flavor"] != "" && cfg["library.url"] != "" && cfg["library.token"] != ""
		},
	})
	return handler, search
}

func searchRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func TestSearchScopesFollowLiveConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		cfg       map[string]string
		wantCode  int
		wantScope string
	}{
		{name: "library configured", path: "/v1/search?q=matrix&scope=library", cfg: libraryConfig(), wantCode: http.StatusOK, wantScope: "library"},
		{name: "library missing", path: "/v1/search?q=matrix&scope=library", cfg: map[string]string{"tmdb.api_key": "key"}, wantCode: http.StatusNotImplemented},
		{name: "tmdb configured", path: "/v1/search?q=matrix&scope=tmdb", cfg: map[string]string{"tmdb.api_key": "key"}, wantCode: http.StatusOK, wantScope: "tmdb"},
		{name: "tmdb missing", path: "/v1/search?q=matrix&scope=tmdb", cfg: libraryConfig(), wantCode: http.StatusNotImplemented},
		{name: "all with both", path: "/v1/search?q=matrix&scope=all", cfg: libraryConfig("tmdb.api_key", "key"), wantCode: http.StatusOK, wantScope: "all"},
		{name: "all narrows to library", path: "/v1/search?q=matrix&scope=all", cfg: libraryConfig(), wantCode: http.StatusOK, wantScope: "library"},
		{name: "library triple incomplete", path: "/v1/search?q=matrix&scope=library", cfg: map[string]string{"library.flavor": "jellyfin", "library.url": "http://library"}, wantCode: http.StatusNotImplemented},
		{name: "default narrows to tmdb", path: "/v1/search?q=matrix", cfg: map[string]string{"tmdb.api_key": "key"}, wantCode: http.StatusOK, wantScope: "tmdb"},
		{name: "neither configured", path: "/v1/search?q=matrix", cfg: map[string]string{}, wantCode: http.StatusNotImplemented},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, search := newConfiguredSearchHandler(t, tt.cfg)
			resp := searchRequest(handler, tt.path)
			if resp.Code != tt.wantCode {
				t.Fatalf("search status = %d, want %d", resp.Code, tt.wantCode)
			}
			requests := search.Requests()
			if tt.wantCode != http.StatusOK {
				if len(requests) != 0 {
					t.Fatalf("unconfigured search reached adapter: %+v", requests)
				}
				return
			}
			if len(requests) != 1 || requests[0].Scope != tt.wantScope {
				t.Fatalf("search requests = %+v, want one %q scope", requests, tt.wantScope)
			}
		})
	}
}

func libraryConfig(extra ...string) map[string]string {
	cfg := map[string]string{
		"library.flavor": "jellyfin", "library.url": "http://library", "library.token": "token",
	}
	for i := 0; i+1 < len(extra); i += 2 {
		cfg[extra[i]] = extra[i+1]
	}
	return cfg
}

func TestSearchConfigurationHotApplies(t *testing.T) {
	cfg := map[string]string{}
	handler, search := newConfiguredSearchHandler(t, cfg)

	resp := searchRequest(handler, "/v1/search?q=matrix")
	if resp.Code != http.StatusNotImplemented {
		t.Fatalf("unconfigured search = %d, want 501", resp.Code)
	}

	cfg["tmdb.api_key"] = "key"
	resp = searchRequest(handler, "/v1/search?q=matrix")
	if resp.Code != http.StatusOK {
		t.Fatalf("search after setting TMDB key = %d, want 200", resp.Code)
	}

	delete(cfg, "tmdb.api_key")
	for key, value := range libraryConfig() {
		cfg[key] = value
	}
	resp = searchRequest(handler, "/v1/search?q=matrix")
	if resp.Code != http.StatusOK {
		t.Fatalf("search after switching to library = %d, want 200", resp.Code)
	}

	requests := search.Requests()
	if len(requests) != 2 || requests[0].Scope != "tmdb" || requests[1].Scope != "library" {
		t.Fatalf("hot-applied scopes = %+v, want tmdb then library", requests)
	}
}

func TestSearchStructuredDiscoveryUsesPublicCatalogPath(t *testing.T) {
	handler, search := newConfiguredSearchHandler(t, libraryConfig("tmdb.api_key", "key"))

	resp := searchRequest(handler, "/v1/search?scope=all&limit=12&media_type=series&genres=Comedy&keywords=family&year_from=1990&year_to=1999&original_language=en&origin_country=us&runtime_min=20&runtime_max=45&vote_average_min=7.5&vote_count_min=100&network=ABC")
	if resp.Code != http.StatusOK {
		t.Fatalf("structured search status = %d, want 200: %s", resp.Code, resp.Body.String())
	}

	requests := search.Requests()
	if len(requests) != 1 {
		t.Fatalf("search requests = %+v, want one", requests)
	}
	request := requests[0]
	if request.Query != "" || request.Scope != "all" || request.Limit != 12 || request.Discovery == nil {
		t.Fatalf("structured request envelope = %+v", request)
	}
	discovery := request.Discovery
	if discovery.MediaType != "series" || discovery.Network != "ABC" || discovery.OriginCountry != "US" ||
		discovery.OriginalLanguage != "en" || discovery.YearFrom != 1990 || discovery.YearTo != 1999 ||
		discovery.RuntimeMin != 20 || discovery.RuntimeMax != 45 || discovery.VoteAverageMin != 7.5 ||
		discovery.VoteCountMin != 100 || len(discovery.Genres) != 1 || discovery.Genres[0] != "Comedy" ||
		len(discovery.Keywords) != 1 || discovery.Keywords[0] != "family" {
		t.Fatalf("structured discovery = %+v", discovery)
	}
}

func TestSearchStructuredDiscoveryPreservesPeopleArguments(t *testing.T) {
	handler, search := newConfiguredSearchHandler(t, map[string]string{"tmdb.api_key": "key"})

	resp := searchRequest(handler, "/v1/search?scope=tmdb&media_type=movie&cast=Jamie%20Lee%20Curtis&cast=Daniel%20Kaluuya&creators=Jordan%20Peele")
	if resp.Code != http.StatusOK {
		t.Fatalf("people search status = %d, want 200: %s", resp.Code, resp.Body.String())
	}

	request := search.Requests()[0]
	if request.Discovery == nil || len(request.Discovery.Cast) != 2 || len(request.Discovery.Creators) != 1 ||
		request.Discovery.Cast[0] != "Jamie Lee Curtis" || request.Discovery.Cast[1] != "Daniel Kaluuya" ||
		request.Discovery.Creators[0] != "Jordan Peele" {
		t.Fatalf("people discovery = %+v", request.Discovery)
	}
}

func TestSearchTitleModeAllowsMediaTypeNarrowing(t *testing.T) {
	handler, search := newConfiguredSearchHandler(t, map[string]string{"tmdb.api_key": "key"})

	resp := searchRequest(handler, "/v1/search?q=Alien&scope=tmdb&media_type=movie")
	if resp.Code != http.StatusOK {
		t.Fatalf("title search status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	request := search.Requests()[0]
	if request.Query != "Alien" || request.MediaType != "movie" || request.Discovery != nil {
		t.Fatalf("title search request = %+v", request)
	}
}

func TestSearchRejectsAmbiguousOperationOrUnsupportedDiscoveryScope(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "neither title nor discovery", path: "/v1/search?scope=all"},
		{name: "title and discovery", path: "/v1/search?q=ABC&media_type=series&network=ABC"},
		{name: "library-only discovery", path: "/v1/search?scope=library&media_type=series&network=ABC"},
		{name: "network with movie", path: "/v1/search?media_type=movie&network=ABC"},
		{name: "people with series", path: "/v1/search?media_type=series&cast=Alex%20Smith"},
		{name: "mixed network and people", path: "/v1/search?media_type=series&network=ABC&cast=Alex%20Smith"},
		{name: "reversed year range", path: "/v1/search?genres=Comedy&year_from=2000&year_to=1990"},
		{name: "reversed runtime range", path: "/v1/search?genres=Comedy&runtime_min=60&runtime_max=30"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, search := newConfiguredSearchHandler(t, libraryConfig("tmdb.api_key", "key"))
			resp := searchRequest(handler, tt.path)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("search status = %d, want 400: %s", resp.Code, resp.Body.String())
			}
			if requests := search.Requests(); len(requests) != 0 {
				t.Fatalf("invalid search reached adapter: %+v", requests)
			}
		})
	}
}

func TestSearchStructuredDiscoveryRequiresConfiguredTMDB(t *testing.T) {
	handler, search := newConfiguredSearchHandler(t, libraryConfig())

	resp := searchRequest(handler, "/v1/search?scope=all&media_type=series&network=ABC")
	if resp.Code != http.StatusNotImplemented {
		t.Fatalf("unconfigured discovery status = %d, want 501: %s", resp.Code, resp.Body.String())
	}
	if requests := search.Requests(); len(requests) != 0 {
		t.Fatalf("unconfigured discovery reached adapter: %+v", requests)
	}
}
