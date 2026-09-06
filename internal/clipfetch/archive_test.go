package clipfetch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/filler"
)

// memSink is an in-memory fileSink for testing the walk without touching disk.
type memSink struct {
	files map[string][]byte
}

func newMemSink() *memSink { return &memSink{files: map[string][]byte{}} }

func (m *memSink) Exists(path string) bool { _, ok := m.files[path]; return ok }
func (m *memSink) WriteStream(path string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.files[path] = b
	return nil
}
func (m *memSink) WriteFile(path string, data []byte) error { m.files[path] = data; return nil }
func (m *memSink) Inspect(path string) (string, int64, string, error) {
	return inspectBytes(m.files[path])
}

// mockArchive serves the pinned metadata/search/download shapes.
func mockArchive(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// A single item: two video files (a big original + a small derivative) + a thumbnail.
	itemMeta := metadataResp{
		Server: "SELF", // replaced with the test server host at request time
		Dir:    "/0/items/test-ad",
		Metadata: archiveMetadata{
			MediaType: "movies", Title: "Test 90s Cereal Ad",
			Description: "A 1994 cereal commercial.",
		},
		Files: []archiveFile{
			{Name: "big.mp4", Format: "MPEG4", Size: "246000000", Source: "original"},
			{Name: "small.ia.mp4", Format: "h.264 IA", Size: "9000000", Source: "derivative"},
			{Name: "thumb.jpg", Format: "Thumbnail", Size: "12000", Source: "derivative"},
		},
	}

	mux.HandleFunc("/metadata/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/metadata/")
		switch id {
		case "test-ad":
			m := itemMeta
			m.Server = r.Host // download URL points back at this test server
			_ = json.NewEncoder(w).Encode(m)
		case "test-collection":
			_ = json.NewEncoder(w).Encode(metadataResp{
				Metadata: archiveMetadata{MediaType: "collection", Title: "Test Collection"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	mux.HandleFunc("/advancedsearch.php", func(w http.ResponseWriter, r *http.Request) {
		// The collection has one member item: test-ad.
		var sr searchResp
		sr.Response.NumFound = 1
		sr.Response.Docs = []searchDoc{{Identifier: "test-ad"}}
		_ = json.NewEncoder(w).Encode(sr)
	})
	// The download endpoint: /0/items/test-ad/<file> → some bytes.
	mux.HandleFunc("/0/items/test-ad/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake video bytes"))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(t *testing.T, fs fileSink) *archiveClient {
	srv := mockArchive(t)
	return newArchiveClient(srv.URL, srv.Client(), fs)
}

func TestArchive_DownloadsItemAndSidecar(t *testing.T) {
	fs := newMemSink()
	c := newTestClient(t, fs)

	fetched, skipped, _, err := c.walk(context.Background(), "https://archive.org/details/test-ad", "/drop")
	if err != nil {
		t.Fatal(err)
	}
	if fetched != 1 || skipped != 0 {
		t.Fatalf("walk = fetched %d skipped %d, want 1/0", fetched, skipped)
	}

	// The SMALL derivative was chosen (not the 246MB original).
	var mediaPath, sidecarPath string
	for p := range fs.files {
		if strings.HasSuffix(p, ".info.json") {
			sidecarPath = p
		} else {
			mediaPath = p
		}
	}
	if !strings.Contains(mediaPath, "small.ia.mp4") {
		t.Errorf("should download the small derivative, got %q", mediaPath)
	}
	if strings.Contains(mediaPath, "big.mp4") {
		t.Error("downloaded the 246MB original instead of the derivative")
	}
	// The sidecar preserves title/description (AI-tagging text signals, §10).
	if sidecarPath == "" {
		t.Fatal("no info-JSON sidecar written")
	}
	var sc map[string]any
	_ = json.Unmarshal(fs.files[sidecarPath], &sc)
	if sc["title"] != "Test 90s Cereal Ad" || sc["description"] != "A 1994 cereal commercial." {
		t.Errorf("sidecar lost text signals: %+v", sc)
	}

	// ⚠ **THE APPROVAL GATE.** A clip Loomarr DOWNLOADED must be marked as ours, or the sync
	// files it on sight instead of holding it in Incoming for a human (§10 V38c). Nothing wrote
	// this mark until V38c.8 — the `fetched=true` branch of `TakeIn` had no caller at all — so
	// every auto-fetched clip went straight to air unreviewed. Found by running auto-fetch
	// against real archive.org collections and reading `held=false` off every row.
	//
	// Asserted through `filler.SidecarFetchedMark()` rather than a literal, so this test and the
	// sync's `wasFetchedByUs` cannot drift apart into two spellings of the same key.
	ours, ok := sc[filler.SidecarLoomarrKey()].(map[string]any)
	if !ok {
		t.Fatalf("downloaded clip is not marked as ours — it would file WITHOUT REVIEW: %+v", sc)
	}
	for k, want := range filler.SidecarFetchedMark() {
		if ours[k] != want {
			t.Errorf("fetched mark %s = %v, want %v", k, ours[k], want)
		}
	}
}

func TestArchive_DownloadCarriesAcquisitionProvenance(t *testing.T) {
	fs := newMemSink()
	c := newTestClient(t, fs)
	ctx := withAcquisition(context.Background(), "archive:classic", "acq-17", "")

	if _, _, _, err := c.walk(ctx, "test-ad", "/drop"); err != nil {
		t.Fatal(err)
	}
	for path, raw := range fs.files {
		if !strings.HasSuffix(path, ".info.json") {
			continue
		}
		var sc map[string]any
		if err := json.Unmarshal(raw, &sc); err != nil {
			t.Fatal(err)
		}
		ours, _ := sc[filler.SidecarLoomarrKey()].(map[string]any)
		if ours["sourceId"] != "archive:classic" || ours["acquisitionId"] != "acq-17" {
			t.Fatalf("loomarr sidecar = %#v, want source and acquisition provenance", ours)
		}
		return
	}
	t.Fatal("no info-JSON sidecar written")
}

func TestArchive_SkipsIfPresent(t *testing.T) {
	fs := newMemSink()
	c := newTestClient(t, fs)
	// First fetch.
	_, _, _, _ = c.walk(context.Background(), "test-ad", "/drop")
	// Second walk: the media file exists → skipped, not re-downloaded.
	fetched, skipped, _, err := c.walk(context.Background(), "test-ad", "/drop")
	if err != nil {
		t.Fatal(err)
	}
	if fetched != 0 || skipped != 1 {
		t.Errorf("re-walk = fetched %d skipped %d, want 0/1 (idempotent)", fetched, skipped)
	}
}

func TestArchive_WalksCollection(t *testing.T) {
	fs := newMemSink()
	c := newTestClient(t, fs)
	fetched, _, _, err := c.walk(context.Background(), "https://archive.org/details/test-collection", "/drop")
	if err != nil {
		t.Fatal(err)
	}
	if fetched != 1 {
		t.Errorf("collection walk fetched %d, want 1 (its one member item)", fetched)
	}
}

func TestPickVideoFile_PrefersSmallestDerivative(t *testing.T) {
	files := []archiveFile{
		{Name: "orig.mp4", Format: "MPEG4", Size: "246000000"},
		{Name: "deriv.ia.mp4", Format: "h.264 IA", Size: "9000000"},
		{Name: "thumb.jpg", Format: "Thumbnail", Size: "12000"},
		{Name: "meta.xml", Format: "Metadata", Size: "500"},
	}
	// Default (filler): smallest derivative.
	f, ok := pickVideoFile(files, false)
	if !ok {
		t.Fatal("expected a video file")
	}
	if f.Name != "deriv.ia.mp4" {
		t.Errorf("default picked %q, want the small derivative deriv.ia.mp4", f.Name)
	}
	// preferOriginal: the full-quality master.
	f, ok = pickVideoFile(files, true)
	if !ok {
		t.Fatal("expected a video file")
	}
	if f.Name != "orig.mp4" {
		t.Errorf("preferOriginal picked %q, want the 246MB original orig.mp4", f.Name)
	}
}

func TestPickVideoFile_NoneWhenNoVideo(t *testing.T) {
	if _, ok := pickVideoFile([]archiveFile{{Name: "x.jpg", Format: "Thumbnail"}}, false); ok {
		t.Error("a thumbnail-only item should yield no video file")
	}
}

func TestArchiveIDFromURL(t *testing.T) {
	cases := map[string]string{
		"https://archive.org/details/warning-cic-logo": "warning-cic-logo",
		"https://archive.org/metadata/some-item":       "some-item",
		"https://archive.org/details/classic-tv-ads/":  "classic-tv-ads", // trailing slash
		"bare-id-123": "bare-id-123",
	}
	for in, want := range cases {
		if got := archiveIDFromURL(in); got != want {
			t.Errorf("archiveIDFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}
