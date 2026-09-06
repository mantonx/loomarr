package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerdecision"
	"github.com/loomarr/loomarr/internal/store"
)

const adminToken = "test-admin-token"

// memberToken authenticates as a MEMBER in tests.
//
// ⚠ **Its absence is why the anonymous-read gap survived review.** The production
// `tokenAuthorizer` resolves admin-or-anonymous only — by design, since API_TOKEN is a
// break-glass admin credential (§11). So member-vs-anonymous was structurally untestable in
// the default harness, and four tests asserting "a member can read this" were passing NO
// token at all: they proved an ANONYMOUS caller could read it, under names that said
// otherwise.
//
// Deliberately a test-only authorizer rather than a member branch on the real one: adding a
// second credential to shipped auth code so a test can express itself is how production
// grows a path that only tests use.
const memberToken = "test-member-token"

// testAuthorizer resolves two fixed bearer tokens to two roles. It exists so a test can say
// "member" and mean it.
type testAuthorizer struct{}

func (testAuthorizer) Authorize(r *http.Request) api.Role {
	switch strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") {
	case adminToken:
		return api.RoleAdmin
	case memberToken:
		return api.RoleMember
	default:
		return api.RoleAnonymous
	}
}

// The migrated-template harness.
//
// ⚠ `store.Open` on a fresh file runs every migration, and this package calls it once per test.
// Measured under `-race` on a 24-thread i9: **503ms** to migrate, **17ms** to build the router,
// and there are 462 tests — so ~232s of a ~259s package was the same 45 migrations, re-run. CI's
// 2–4 core runners are ~2.5× slower, which is what put the package past Go's default 10m
// per-package timeout and turned a green suite into a `panic: test timed out` with no partial
// results (the timeout kills the binary; it does not fail one test).
//
// So migrate ONCE per package into a template file and copy it per test: **26ms**, a 19.6×
// reduction on the step, with every test still running against a real, fully-migrated database
// built by the real `store.Open`. No assertion changed and nothing is shared between tests — the
// copy is per-test exactly as `t.TempDir()` was.
//
// ⚠ It cannot produce a false green, and the reason is worth stating because "a shared fixture"
// is normally where one hides. `autoMigrate` stays true on the per-test open, so the two ways
// this can go wrong both fail safe: an empty or truncated copy is a version-0 database that
// goose simply migrates the old way — slow, still correct — and a CORRUPT one fails `store.Open`
// outright and calls t.Fatal. There is no path where a test runs against a schema it did not ask
// for. The 10× wall-clock drop is the evidence the fast path is actually being taken.
var (
	templateOnce sync.Once
	templateDB   string
	templateErr  error
)

// migratedTemplate returns the path to a SQLite file with every migration applied.
func migratedTemplate(t *testing.T) string {
	t.Helper()
	templateOnce.Do(func() {
		var dir string
		if dir, templateErr = os.MkdirTemp("", "loomarr-api-template"); templateErr != nil {
			return
		}
		templateDB = filepath.Join(dir, "template.db")
		var st store.Store
		if st, templateErr = store.Open(context.Background(), "sqlite://"+templateDB, true); templateErr != nil {
			return
		}
		// ⚠ Close BEFORE anything copies this. The last connection closing is what checkpoints
		// WAL back into the main file; copying a live database is how you get a torn one.
		templateErr = st.Close()
	})
	if templateErr != nil {
		t.Fatalf("build migrated template: %v", templateErr)
	}
	return templateDB
}

// removeTemplate drops the template directory. Called from TestMain after the run, because a
// t.Cleanup would delete it after the first test and strand the other 461.
func removeTemplate() {
	if templateDB != "" {
		_ = os.RemoveAll(filepath.Dir(templateDB))
	}
}

// copyTemplate lays the template down at dst. The `-wal`/`-shm` siblings are copied when present
// rather than assumed checkpointed away — "usually checkpoints on close" is not a property worth
// resting 462 tests on, and copying an absent file is a no-op.
func copyTemplate(t *testing.T, src, dst string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(src + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read template%s: %v", suffix, err)
		}
		if err := os.WriteFile(dst+suffix, data, 0o600); err != nil {
			t.Fatalf("write template%s: %v", suffix, err)
		}
	}
}

// openTestStore opens a fully-migrated SQLite store at path, laid down from the template.
//
// ⚠ Use this instead of calling `store.Open` directly in this package's tests. `newServer` alone
// covers only 15 of ~462 tests here; the rest reach for a store through their own file-local
// helper, and a site that opens a fresh file re-pays the whole 503ms migration run. Closing is
// left to the caller so this drops into existing helpers without doubling their cleanup.
func openTestStore(t *testing.T, path string) store.Store {
	t.Helper()
	copyTemplate(t, migratedTemplate(t), path)
	// autoMigrate stays true: goose reads the version the template already carries and no-ops,
	// which keeps this the same call production makes rather than a test-only shortcut.
	st, err := store.Open(context.Background(), "sqlite://"+path, true)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func newServer(t *testing.T) (*httptest.Server, store.Store) {
	t.Helper()
	st := openTestStore(t, filepath.Join(t.TempDir(), "api.db"))
	t.Cleanup(func() { _ = st.Close() })
	decisions, err := fillerdecision.New(st)
	if err != nil {
		t.Fatal(err)
	}
	rights, err := filler.NewFillerRightsRegistry(st)
	if err != nil {
		t.Fatal(err)
	}
	h := api.Router(slog.New(slog.DiscardHandler), api.Options{
		Store:           st,
		Auth:            testAuthorizer{},
		Log:             slog.New(slog.DiscardHandler),
		BackupSQLite:    store.SQLiteBackuper(st),
		FillerDecisions: decisions,
		FillerRights:    rights,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, st
}

func do(t *testing.T, srv *httptest.Server, method, path, token, body string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// OpenAPI is 3.1 and its State enum equals the code enum (§7.1).
func TestOpenAPISpec(t *testing.T) {
	spec, err := api.ExportOpenAPI(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	s := string(spec)
	if !strings.Contains(s, "openapi: 3.1") {
		t.Error("spec is not OpenAPI 3.1")
	}
	for _, st := range []string{"wanted", "requested", "downloading", "available", "unavailable"} {
		if !strings.Contains(s, "- "+st) {
			t.Errorf("spec State enum missing %q (must equal the code enum, §7.1)", st)
		}
	}
}

// Enqueue requires admin; a valid admin token creates the title as `wanted`.
func TestEnqueueTitleAdmin(t *testing.T) {
	srv, _ := newServer(t)
	resp := do(t, srv, http.MethodPost, "/v1/titles", adminToken,
		`{"mediaType":"movie","tmdbId":1111867,"name":"In Flames"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enqueue → %d, want 200", resp.StatusCode)
	}
	var body struct{ Key, State string }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Key != "movie:tmdb:1111867" || body.State != "wanted" {
		t.Errorf("enqueue body = %+v, want key movie:tmdb:1111867 state wanted", body)
	}
}

// Enqueue is idempotent (§4 inv. 3): a second identical POST returns the current
// record, not a duplicate or error.
func TestEnqueueIdempotent(t *testing.T) {
	srv, st := newServer(t)
	for i := 0; i < 2; i++ {
		resp := do(t, srv, http.MethodPost, "/v1/titles", adminToken, `{"mediaType":"movie","tmdbId":5}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("enqueue %d → %d", i, resp.StatusCode)
		}
	}
	// Exactly one wanted row.
	recs, _ := st.ListTitlesByState(context.Background(), "wanted")
	if len(recs) != 1 {
		t.Errorf("idempotent enqueue produced %d rows, want 1", len(recs))
	}
}

// POST/DELETE /v1/titles require admin (§7) — anonymous and wrong-token → 403.
func TestTitlesMutationRequiresAdmin(t *testing.T) {
	srv, _ := newServer(t)
	for _, tok := range []string{"", "wrong"} {
		resp := do(t, srv, http.MethodPost, "/v1/titles", tok, `{"mediaType":"movie","tmdbId":1}`)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("POST with token %q → %d, want 401", tok, resp.StatusCode)
		}
	}
}

// list requires the state filter (§7); missing → 400.
func TestListRequiresState(t *testing.T) {
	srv, _ := newServer(t)
	resp := do(t, srv, http.MethodGet, "/v1/titles", adminToken, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("list without state → %d, want 400", resp.StatusCode)
	}
}

// GET a missing title → 404.
func TestGetMissingTitle(t *testing.T) {
	srv, _ := newServer(t)
	resp := do(t, srv, http.MethodGet, "/v1/titles/movie:tmdb:404", adminToken, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("get missing → %d, want 404", resp.StatusCode)
	}
}

// DELETE gives up a title (→ unavailable), admin only.
func TestDeleteTitle(t *testing.T) {
	srv, st := newServer(t)
	_ = do(t, srv, http.MethodPost, "/v1/titles", adminToken, `{"mediaType":"movie","tmdbId":7}`)
	resp := do(t, srv, http.MethodDelete, "/v1/titles/movie:tmdb:7", adminToken, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete → %d, want 204", resp.StatusCode)
	}
	rec, _ := st.GetTitle(context.Background(), "movie:tmdb:7")
	if rec.State != "unavailable" {
		t.Errorf("deleted title state = %s, want unavailable (audit-preserving give-up)", rec.State)
	}
}

// SQLite backend serves a backup snapshot; admin only.
func TestBackupSQLite(t *testing.T) {
	srv, _ := newServer(t)
	resp := do(t, srv, http.MethodGet, "/v1/backup", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("backup → %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("backup content-type = %q", ct)
	}
	b, _ := io.ReadAll(resp.Body)
	if len(b) == 0 || string(b[:15]) != "SQLite format 3" {
		t.Error("backup body is not a SQLite database snapshot")
	}
}

// Backup requires admin.
func TestBackupRequiresAdmin(t *testing.T) {
	srv, _ := newServer(t)
	resp := do(t, srv, http.MethodGet, "/v1/backup", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("backup without admin → %d, want 401", resp.StatusCode)
	}
}

// The API reference is offline: no external (CDN) asset references (§7.1).
func TestDocsOffline(t *testing.T) {
	srv, _ := newServer(t)
	resp := do(t, srv, http.MethodGet, "/v1/reference", "", "")
	b, _ := io.ReadAll(resp.Body)
	page := string(b)
	if strings.Contains(page, "cdn.") || strings.Contains(page, "unpkg") || strings.Contains(page, "jsdelivr") {
		t.Error("/v1/reference references a CDN — violates the offline rule (§7.1)")
	}
	// ⚠ Its two spec links are hardcoded in a const while the real paths come from
	// cfg.OpenAPIPath, so nothing but this connects them. They pointed at the pre-/v1
	// /openapi.json until it was spotted by hand — a reference page whose only links 404.
	for _, link := range []string{"/v1/openapi.json", "/v1/openapi.yaml"} {
		if !strings.Contains(page, link) {
			t.Errorf("the reference page does not link %s; its links have drifted from cfg.OpenAPIPath", link)
			continue
		}
		if r := do(t, srv, http.MethodGet, link, "", ""); r.StatusCode != http.StatusOK {
			t.Errorf("the reference page links %s, which answers %d", link, r.StatusCode)
		}
	}
}
