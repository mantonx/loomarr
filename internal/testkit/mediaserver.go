package testkit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// MediaServer is the shared Emby/Jellyfin test double (AGENTS.md: one shared
// mock per service, both flavors). It serves the pinned Phase-0 fixtures so the
// library adapter is tested against ground truth, never the network. It also
// records the auth headers it received, so flavor-specific header construction
// can be asserted.
type MediaServer struct {
	*httptest.Server
	// mu guards the mutable fields below against the httptest handler goroutine racing a
	// test goroutine — needed once a background job (e.g. the library scan) issues requests
	// concurrently with the test. Header captures + SearchItems reads take it.
	mu sync.RWMutex
	// LastAuthHeader / LastEmbyToken / LastEmbyAuthz capture what the adapter sent
	// on the most recent request, for header-shape assertions.
	LastAuthHeader string
	LastEmbyToken  string
	LastEmbyAuthz  string
	requests       []MediaServerRequest
	// metadataRequests counts BULK metadata lookups (§9.1). Read via MetadataRequests();
	// it exists so a test can assert that N programmes cost ONE request rather than N — the
	// property that makes the XMLTV guide affordable on a route a media server polls.
	metadataRequests int
	// AdminToken is the token the mock accepts for /Users and /Items.
	AdminToken string
	// GoodUser/GoodPass authenticate successfully via /Users/AuthenticateByName.
	GoodUser string
	GoodPass string
	// PresentTMDB, if set, is an additional tmdb id the mock reports present
	// (beyond the pinned 16153) — lets ingest tests flip a title to confirmed.
	PresentTMDB string
	// ItemRunTimeTicks, if set, is the RunTimeTicks a GET /Items?Ids=<id> lookup
	// returns (§9 ItemDurationMs). 0 ⇒ an empty item list (no runtime).
	ItemRunTimeTicks int64
	// ItemMetadata maps a library item id → its display metadata, for the BULK
	// `GET /Items?Ids=a,b,c&Fields=Overview,...` the XMLTV guide uses (§9.1). An id
	// absent from this map is absent from the response, which is how a test models a
	// title removed from the library since the lineup was built.
	ItemMetadata map[string]ItemMetadata
	// Accounts maps username→(password, isAdmin, disabled) for AuthenticateByName.
	// If nil, GoodUser/GoodPass authenticate as an admin. Lets auth tests model
	// admin vs member vs disabled logins.
	Accounts map[string]Account
	// AuthStatus, when non-zero, forces AuthenticateByName to return that status.
	// It lets auth tests distinguish authoritative rejection from provider outage.
	AuthStatus int
	// Users, when set, is the account list returned by GET /Users. Nil keeps the
	// pinned Emby fixture; a non-nil slice lets provisioning tests cover mixed
	// roles and disabled accounts through the real library adapter.
	Users []MediaServerUser
	// SearchItems, when set, makes the /Items SearchTerm search RETURN these items
	// (matched case-insensitively by term substring against a stub's Terms) instead
	// of the pinned matrix fixture. Each stub carries the real /Items shape incl.
	// OfficialRating/Genres, so a test can drive a themed intent through the real
	// library→catalog→proposal→policy path with in-library titles that have ratings.
	// The same stubs answer the AnyProviderIdEquals presence check (by tmdb id), so
	// in-library backfill is consistent. Pinned fixtures still serve when unset.
	SearchItems []SearchStub
	// EpisodeItems, when set, answers a ParentId-scoped episode enumeration. This
	// keeps episode adapter tests on the shared media-server double while allowing
	// them to exercise fields that the filler fixture does not model.
	EpisodeItems []EpisodeStub
	// EpisodeJSON, when set, is the exact raw JSON for each episode object. It is
	// used only when an adapter contract must preserve duplicate object members or
	// malformed editorial values that a typed EpisodeStub cannot represent.
	EpisodeJSON []json.RawMessage
	// InventoryItems maps an item id to an exact rich Emby/Jellyfin item object. It drives the
	// provider-neutral Media Inventory importer without adding a private service double.
	InventoryItems map[string]json.RawMessage
}

// MediaServerRequest is one captured call to the shared media-server double. Tests that
// exercise live configuration use the per-request record instead of racing on the legacy
// last-header fields or mistaking a background scan for the operation under assertion.
type MediaServerRequest struct {
	Method        string
	Path          string
	RawQuery      string
	Authorization string
	EmbyToken     string
	EmbyAuthz     string
}

// Requests returns a copy of every captured media-server request in arrival order.
func (ms *MediaServer) Requests() []MediaServerRequest {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return append([]MediaServerRequest(nil), ms.requests...)
}

// SearchStub is one in-library item the scriptable search returns. Terms are the
// query substrings it matches (case-insensitive); the rest is the /Items item shape
// the library adapter parses.
type SearchStub struct {
	Terms          []string // query substrings this item answers (e.g. "cartoon", "90s")
	LibraryItemID  string
	Name           string
	Type           string // "Movie" | "Series"
	Year           int
	TMDBID         int
	TVDBID         int // for series correlation (the scan keys series on tvdb); 0 → omitted
	Genres         []string
	OfficialRating string
	// RunTimeTicks is the item's runtime (Emby ticks; 1 tick = 100ns) returned by
	// the by-id lookup the scheduler uses for program duration (§9). Lets a test give
	// each in-library title a real, distinct runtime so the break interleave fires.
	RunTimeTicks int64
}

// EpisodeStub is one Emby/Jellyfin episode returned from a ParentId-scoped
// /Items query. RunTimeMs is converted to the server's 100-nanosecond ticks.
type EpisodeStub struct {
	LibraryItemID string
	Name          string
	RunTimeMs     int64
	Season        int
	// OmitSeason distinguishes an absent provider field from an explicit season 0 special.
	OmitSeason bool
	Episode    int
	// OmitEpisode distinguishes an absent provider field from the invalid numeric zero.
	OmitEpisode     bool
	EpisodeEnd      int
	ProductionYear  int
	OfficialRating  string
	CommunityRating float64
	Overview        string
	Tags            []string
}

// stubRuntime returns the RunTimeTicks a scripted stub declares for a library item
// id (the Ids-filtered duration lookup), or 0 if none.
func (ms *MediaServer) stubRuntime(libraryItemID string) int64 {
	for _, s := range ms.searchItems() {
		if s.LibraryItemID == libraryItemID {
			return s.RunTimeTicks
		}
	}
	return 0
}

// stubForProv returns the scripted stub matching an AnyProviderIdEquals value
// (e.g. "tmdb.100"), so the presence check answers with that stub's full item —
// rating and all — exactly as the real library does. This is what lets the
// in-library backfill carry the rating through discovery (FINDING 6).
func (ms *MediaServer) stubForProv(prov string) (SearchStub, bool) {
	for _, s := range ms.searchItems() {
		if s.TMDBID > 0 && strings.EqualFold(prov, "tmdb."+strconv.Itoa(s.TMDBID)) {
			return s, true
		}
	}
	return SearchStub{}, false
}

func (s SearchStub) matches(term string) bool {
	lt := strings.ToLower(term)
	for _, t := range s.Terms {
		if strings.Contains(lt, strings.ToLower(t)) || strings.Contains(strings.ToLower(t), lt) {
			return true
		}
	}
	return false
}

// itemJSON renders the stub as one /Items entry (the real Emby item shape the
// search adapter parses: Id/Name/Type/ProductionYear/Genres/Overview/OfficialRating/
// ProviderIds).
func (s SearchStub) itemJSON() string {
	// Tvdb is rendered only when set (series correlation), matching real Emby, which
	// omits an id namespace an item doesn't carry.
	tvdb := ""
	if s.TVDBID > 0 {
		tvdb = strconv.Itoa(s.TVDBID)
	}
	b, _ := json.Marshal(struct {
		Id             string   `json:"Id"`
		Name           string   `json:"Name"`
		Type           string   `json:"Type"`
		ProductionYear int      `json:"ProductionYear"`
		Genres         []string `json:"Genres"`
		OfficialRating string   `json:"OfficialRating"`
		RunTimeTicks   int64    `json:"RunTimeTicks"`
		ProviderIds    struct {
			Tmdb string `json:"Tmdb,omitempty"`
			Tvdb string `json:"Tvdb,omitempty"`
		} `json:"ProviderIds"`
	}{
		Id: s.LibraryItemID, Name: s.Name, Type: s.Type, ProductionYear: s.Year,
		Genres: s.Genres, OfficialRating: s.OfficialRating, RunTimeTicks: s.RunTimeTicks,
		ProviderIds: struct {
			Tmdb string `json:"Tmdb,omitempty"`
			Tvdb string `json:"Tvdb,omitempty"`
		}{Tmdb: tmdbOrEmpty(s.TMDBID), Tvdb: tvdb},
	})
	return string(b)
}

// tmdbOrEmpty renders a positive TMDB id, else "" so ProviderIds omits it (a series stub may
// carry only a tvdb id). Mirrors real Emby, which never emits a "0" provider id.
func tmdbOrEmpty(id int) string {
	if id > 0 {
		return strconv.Itoa(id)
	}
	return ""
}

// Account is a media-server login the mock accepts.
type Account struct {
	Password string
	ID       string
	IsAdmin  bool
	Disabled bool
}

// MediaServerUser is one account returned by the mock's GET /Users endpoint.
type MediaServerUser struct {
	ID       string
	Name     string
	IsAdmin  bool
	Disabled bool
}

// MetadataRequests reports how many BULK metadata lookups the server has served.
//
// The assertion it enables is the point of the bulk API: N programmes must cost ONE request,
// not N. A media server polls the guide, so a per-item lookup would multiply that load by the
// size of the listings.
func (ms *MediaServer) MetadataRequests() int {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return ms.metadataRequests
}

// ItemMetadata is a library item's display metadata, as the bulk `/Items?Ids=…&Fields=Overview,…`
// lookup returns it (§9.1 — the XMLTV guide's descriptions, genres, year and rating).
type ItemMetadata struct {
	Overview       string
	Genres         []string
	Year           int
	OfficialRating string
	// RunTimeMs is the item's own runtime. Declared in MILLISECONDS here and rendered as
	// Emby ticks on the wire, so a test states a duration in the unit it thinks in while
	// the adapter still exercises the real tick conversion.
	RunTimeMs int64
}

// NewMediaServer starts a mock media server serving the pinned fixtures.
func NewMediaServer(t testing.TB) *MediaServer {
	t.Helper()
	ms := &MediaServer{AdminToken: "test-admin-token", GoodUser: "Fixture Admin", GoodPass: "correct-horse"}
	mux := http.NewServeMux()

	// /Library/VirtualFolders — library enumeration, for resolving a filler library's NAME to the
	// item id `ParentId` needs (§10 V38c).
	//
	// ⚠ Serves a BARE ARRAY, because that is what Emby 4.10.0.22 returns — the one endpoint here
	// without the `{"Items": […]}` envelope. Captured 2026-08-02; see fixtures/emby/FINDINGS.md.
	// Wrapping it for consistency would make every test agree with a parser that cannot read the
	// real server.
	mux.HandleFunc("GET /Library/VirtualFolders", func(w http.ResponseWriter, r *http.Request) {
		ms.capture(r)
		_, _ = w.Write(Fixture(t, "emby/virtual_folders.json"))
	})

	// /Items — presence lookup (§6) AND term search (§7.2). SearchTerm branches
	// to the search fixture; otherwise AnyProviderIdEquals decides present/absent.
	mux.HandleFunc("GET /Items", func(w http.ResponseWriter, r *http.Request) {
		ms.capture(r)
		// Episode enumeration (§9) is also ParentId-scoped. Scripted episodes take
		// precedence; the pinned filler fixture remains the default for §10 reads.
		if pid := r.URL.Query().Get("ParentId"); pid != "" {
			if strings.EqualFold(r.URL.Query().Get("IncludeItemTypes"), "Episode") {
				if items := ms.rawEpisodeItems(); items != nil {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"Items": items, "TotalRecordCount": len(items),
					})
					return
				}
				if items := ms.episodeItems(); items != nil {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"Items": items, "TotalRecordCount": len(items),
					})
					return
				}
			}
			_, _ = w.Write(Fixture(t, "emby/filler_library.json"))
			return
		}
		if ids := r.URL.Query().Get("Ids"); ids != "" &&
			strings.Contains(r.URL.Query().Get("Fields"), "MediaSources") {
			if raw, ok := ms.InventoryItems[ids]; ok {
				_, _ = fmt.Fprintf(w, `{"Items":[%s],"TotalRecordCount":1}`, raw)
			} else {
				_, _ = w.Write([]byte(`{"Items":[],"TotalRecordCount":0}`))
			}
			return
		}
		// Single-item runtime lookup (§9 ItemDurationMs): GET /Items?Ids=<id>&
		// Fields=RunTimeTicks. Emby rejects the bare /Items/<id> path unless user-
		// scoped, so the adapter uses this Ids-filtered list. A configured
		// ItemRunTimeTicks answers with that runtime; else an empty list (0 → the
		// scheduler falls back, never dead air).
		// BULK METADATA (§9.1 XMLTV guide): `Ids=a,b,c` with Overview/Genres/etc. requested.
		// Distinct from the single-id duration lookup below because it answers a LIST — the
		// whole point of the bulk call is that one request covers a guide's worth of items.
		if ids := r.URL.Query().Get("Ids"); ids != "" &&
			strings.Contains(r.URL.Query().Get("Fields"), "Overview") {
			ms.mu.Lock()
			ms.metadataRequests++
			ms.mu.Unlock()
			out := make([]string, 0)
			for _, id := range strings.Split(ids, ",") {
				m, ok := ms.ItemMetadata[id]
				if !ok {
					continue // absent = removed from the library; the caller keeps its title
				}
				genres, _ := json.Marshal(m.Genres)
				if m.Genres == nil {
					genres = []byte("[]")
				}
				ov, _ := json.Marshal(m.Overview)
				rating, _ := json.Marshal(m.OfficialRating)
				out = append(out, fmt.Sprintf(
					`{"Id":%q,"Overview":%s,"Genres":%s,"ProductionYear":%d,"OfficialRating":%s,"RunTimeTicks":%d}`,
					id, ov, genres, m.Year, rating, m.RunTimeMs*10_000))
			}
			_, _ = fmt.Fprintf(w, `{"Items":[%s],"TotalRecordCount":%d}`,
				strings.Join(out, ","), len(out))
			return
		}
		if ids := r.URL.Query().Get("Ids"); ids != "" {
			// A scripted stub's runtime (per library item id) takes precedence, so a
			// test can give each title a distinct real runtime; else the global
			// ItemRunTimeTicks; else an empty item list.
			ticks := ms.ItemRunTimeTicks
			if t := ms.stubRuntime(ids); t > 0 {
				ticks = t
			}
			if ticks == 0 {
				_, _ = w.Write([]byte(`{"Items":[],"TotalRecordCount":0}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"Items":[{"Id":%q,"RunTimeTicks":%d}],"TotalRecordCount":1}`, ids, ticks)
			return
		}
		// Bulk scan (poll-based availability, §4): RecentlyAdded/AllItems send SortBy=DateCreated
		// with no SearchTerm/Ids/ParentId/AnyProviderIdEquals. Return the scriptable SearchItems
		// as the "library contents" so a test can say which titles are present and drive the scan
		// job's LibraryConfirmed correlation. Date filtering (MinDateLastSaved) is not modeled —
		// correlation is by provider id, and tests control membership via SearchItems directly.
		if sb := r.URL.Query().Get("SortBy"); strings.Contains(sb, "DateCreated") {
			var items []string
			for _, s := range ms.searchItems() {
				items = append(items, s.itemJSON())
			}
			_, _ = fmt.Fprintf(w, `{"Items":[%s],"TotalRecordCount":%d}`, strings.Join(items, ","), len(items))
			return
		}
		if term := r.URL.Query().Get("SearchTerm"); term != "" {
			// Scriptable stubs (with OfficialRating/genres) take precedence when set,
			// so a test can drive a themed intent through real in-library titles.
			if len(ms.searchItems()) > 0 {
				var items []string
				for _, s := range ms.searchItems() {
					if s.matches(term) {
						items = append(items, s.itemJSON())
					}
				}
				_, _ = fmt.Fprintf(w, `{"Items":[%s],"TotalRecordCount":%d}`, strings.Join(items, ","), len(items))
				return
			}
			// The pinned search fixture answers "matrix"; any other term → empty.
			if strings.EqualFold(term, "matrix") {
				_, _ = w.Write(Fixture(t, "emby/search_matrix.json"))
			} else {
				_, _ = w.Write([]byte(`{"Items":[],"TotalRecordCount":0}`))
			}
			return
		}
		prov := r.URL.Query().Get("AnyProviderIdEquals")
		// A scripted stub answers with ITS OWN item (carrying OfficialRating/genres),
		// so the presence backfill picks up the rating exactly as the real Emby does
		// with Fields=OfficialRating — the seam FINDING 6 was hiding in. A static
		// fixture here would return the title present but unrated, masking the bug.
		if stub, ok := ms.stubForProv(prov); ok {
			_, _ = fmt.Fprintf(w, `{"Items":[%s],"TotalRecordCount":1}`, stub.itemJSON())
			return
		}
		// The pinned present fixture is tmdb.16153 (Phase 0); a test may add one
		// more present id via PresentTMDB.
		present := strings.EqualFold(prov, "tmdb.16153") ||
			(ms.PresentTMDB != "" && strings.EqualFold(prov, "tmdb."+ms.PresentTMDB))
		if present {
			_, _ = w.Write(Fixture(t, "emby/lookup_present.json"))
			return
		}
		_, _ = w.Write(Fixture(t, "emby/lookup_absent.json"))
	})

	// /Users — list for import/sync (§11).
	mux.HandleFunc("GET /Users", func(w http.ResponseWriter, r *http.Request) {
		ms.capture(r)
		if ms.Users != nil {
			type policy struct {
				IsAdministrator bool `json:"IsAdministrator"`
				IsDisabled      bool `json:"IsDisabled"`
			}
			type user struct {
				ID     string `json:"Id"`
				Name   string `json:"Name"`
				Policy policy `json:"Policy"`
			}
			users := make([]user, 0, len(ms.Users))
			for _, u := range ms.Users {
				users = append(users, user{
					ID: u.ID, Name: u.Name,
					Policy: policy{IsAdministrator: u.IsAdmin, IsDisabled: u.Disabled},
				})
			}
			_ = json.NewEncoder(w).Encode(users)
			return
		}
		_, _ = w.Write(Fixture(t, "emby/users_list.json"))
	})

	// /Users/AuthenticateByName — credential check (§11).
	mux.HandleFunc("POST /Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		ms.capture(r)
		var body struct{ Username, Pw string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if ms.AuthStatus != 0 {
			w.WriteHeader(ms.AuthStatus)
			return
		}

		// Configurable accounts take precedence; otherwise the default admin.
		if ms.Accounts != nil {
			if acct, ok := ms.Accounts[body.Username]; ok && acct.Password == body.Pw {
				id := acct.ID
				if id == "" {
					id = "user-" + body.Username
				}
				resp := map[string]any{
					"AccessToken": "ms-session-token",
					"User": map[string]any{
						"Id": id, "Name": body.Username,
						"Policy": map[string]any{"IsAdministrator": acct.IsAdmin, "IsDisabled": acct.Disabled},
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
		} else if body.Username == ms.GoodUser && body.Pw == ms.GoodPass {
			resp := map[string]any{
				"AccessToken": "ms-session-token",
				"User": map[string]any{
					"Id": "00000000000000000000000000000007", "Name": ms.GoodUser,
					"Policy": map[string]any{"IsAdministrator": true, "IsDisabled": false},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		// Bad creds: the real Emby returns 401 (Phase 0 pinned this).
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(Fixture(t, "emby/auth_badpw_response.json"))
	})

	// /Sessions/Logout — best-effort token discard (§11).
	mux.HandleFunc("POST /Sessions/Logout", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	ms.Server = httptest.NewServer(mux)
	return ms
}

func (ms *MediaServer) capture(r *http.Request) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.LastAuthHeader = r.Header.Get("Authorization")
	ms.LastEmbyToken = r.Header.Get("X-Emby-Token")
	ms.LastEmbyAuthz = r.Header.Get("X-Emby-Authorization")
	ms.requests = append(ms.requests, MediaServerRequest{
		Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery,
		Authorization: ms.LastAuthHeader, EmbyToken: ms.LastEmbyToken, EmbyAuthz: ms.LastEmbyAuthz,
	})
}

// SetSearchItems sets the scriptable in-library items under the lock — use this (not a direct
// field write) when a background job may hit the mock concurrently with the test goroutine.
func (ms *MediaServer) SetSearchItems(items ...SearchStub) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.SearchItems = items
}

// SetEpisodeItems sets the scriptable episode enumeration under the lock.
func (ms *MediaServer) SetEpisodeItems(items ...EpisodeStub) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.EpisodeItems = items
	ms.EpisodeJSON = nil
}

// SetRawEpisodeItems sets exact episode-object JSON while keeping the adapter
// test on the shared media-server boundary. Each value must be one JSON object;
// the server intentionally does not normalize duplicate members.
func (ms *MediaServer) SetRawEpisodeItems(items ...string) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.EpisodeItems = nil
	ms.EpisodeJSON = make([]json.RawMessage, len(items))
	for i, item := range items {
		ms.EpisodeJSON[i] = json.RawMessage(item)
	}
}

func (ms *MediaServer) rawEpisodeItems() []json.RawMessage {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	if ms.EpisodeJSON == nil {
		return nil
	}
	return append([]json.RawMessage(nil), ms.EpisodeJSON...)
}

func (ms *MediaServer) episodeItems() []map[string]any {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	if ms.EpisodeItems == nil {
		return nil
	}
	items := make([]map[string]any, 0, len(ms.EpisodeItems))
	for _, e := range ms.EpisodeItems {
		item := map[string]any{
			"Id": e.LibraryItemID, "Name": e.Name, "RunTimeTicks": e.RunTimeMs * 10_000,
			"ProductionYear": e.ProductionYear, "OfficialRating": e.OfficialRating,
			"CommunityRating": e.CommunityRating, "Overview": e.Overview, "Tags": e.Tags,
		}
		if !e.OmitSeason {
			item["ParentIndexNumber"] = e.Season
		}
		if !e.OmitEpisode {
			item["IndexNumber"] = e.Episode
		}
		if e.EpisodeEnd > 0 {
			item["IndexNumberEnd"] = e.EpisodeEnd
		}
		items = append(items, item)
	}
	return items
}

// searchItems reads the scriptable items under the read lock, for the handlers.
func (ms *MediaServer) searchItems() []SearchStub {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return ms.SearchItems
}
