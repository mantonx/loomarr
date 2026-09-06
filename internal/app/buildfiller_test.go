package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/clipfetch"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestFillerSourceAdapter_HotEnablesTunarrAnnotation(t *testing.T) {
	client := testkit.NewTunarr()
	enabled := false
	adapter := fillerSourceAdapter{
		prog:       client,
		configured: func() bool { return enabled },
	}
	if got, err := adapter.LocalClipIDsByName(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("disabled annotation = %v, %v; want empty success", got, err)
	}
	if client.FillerClipReads != 0 {
		t.Fatalf("disabled adapter made %d Tunarr calls", client.FillerClipReads)
	}

	enabled = true
	if _, err := adapter.LocalClipIDsByName(context.Background()); err != nil {
		t.Fatalf("enabled annotation: %v", err)
	}
	if client.FillerClipReads != 1 {
		t.Fatalf("enabled adapter made %d calls, want 1", client.FillerClipReads)
	}
}

func TestFetchStoreAdapter_ExcludesUnclassifiedAndOutOfMarketSources(t *testing.T) {
	st := testkit.MigratedSQLiteStore(t)
	for _, tc := range []struct {
		id, country, market string
	}{
		{"us-wide", "US", ""},
		{"ny-local", "US", "New York"},
		{"california", "US", "California"},
		{"canadian", "CA", ""},
		{"unknown", "", ""},
	} {
		src := store.NewFillerSource(tc.id, "archive", tc.id, tc.id, time.Now().UTC())
		src.Geography = filler.Geography{Country: tc.country, Market: tc.market}
		if err := st.UpsertFillerSource(t.Context(), src); err != nil {
			t.Fatal(err)
		}
	}
	adapter := fetchStoreAdapter{
		st: st, fetchEvery: func() time.Duration { return time.Hour },
		home: func() filler.Geography { return filler.Geography{Country: "US", Market: "New York"} },
	}
	sources, err := adapter.ListFetchSources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, src := range sources {
		got[src.ID] = true
	}
	if !got["us-wide"] || !got["ny-local"] || len(got) != 2 {
		t.Fatalf("fetch sources = %v, want only US-wide and New York local", got)
	}
}

func TestBuildFetcher_DownloadsIntoTheAppliedWatchFolder(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("LOOMARR_YTDLP_ARGS", argsFile)
	ytdlp := testkit.Executable(t, "yt-dlp", "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$LOOMARR_YTDLP_ARGS\"\n")
	ffmpeg := testkit.Executable(t, "ffmpeg", "#!/bin/sh\nexit 0\n")
	clipDir := filepath.Join(t.TempDir(), "clips")
	watchDir := filepath.Join(t.TempDir(), "incoming")
	layout, err := filler.NewLayout(clipDir, watchDir)
	if err != nil {
		t.Fatal(err)
	}
	set := visionSet(t, map[string]string{
		"ingest.ytdlp_path":  ytdlp,
		"ingest.ffmpeg_path": ffmpeg,
	})
	fetcher := buildFetcher(set, layout, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if fetcher == nil {
		t.Fatal("buildFetcher returned nil with both tools configured")
	}
	fetcher.Run(context.Background(), []clipfetch.Source{{Kind: clipfetch.YouTube, URL: "https://example.invalid/video"}})

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := string(raw)
	if !strings.Contains(args, watchDir+"/%(title)s [%(id)s].%(ext)s") {
		t.Errorf("yt-dlp args = %q, want output under applied watch %q", args, watchDir)
	}
	if strings.Contains(args, clipDir+"/%(title)s") {
		t.Errorf("yt-dlp args = %q, unexpectedly write raw arrivals into clip library %q", args, clipDir)
	}
}

// The hosted picker stores credentials under the branded provider, not the flattened `openai`
// wire kind. The filler language path must resolve that same active selection or it sends an
// unauthenticated request even though Settings says the provider is configured.
func TestHostedLanguageAsker_UsesTheSelectedProvidersNamespacedKey(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"en"}}]}`))
	}))
	t.Cleanup(server.Close)

	set := visionSet(t, map[string]string{
		"llm.provider":           "openai",
		"llm.hosted_provider":    "openrouter",
		"llm.url":                server.URL,
		"llm.model":              "audio-model",
		"llm.api_key.openrouter": "provider-secret",
	})
	asker := hostedLanguageAsker(set, nil)
	if asker == nil {
		t.Fatal("hosted language asker is nil for a configured provider")
	}
	if _, err := asker.AskAboutAudio(context.Background(), filler.AudioAsk{
		Audio: []byte("audio"), Format: "wav", Prompt: "language?",
	}); err != nil {
		t.Fatalf("ask about audio: %v", err)
	}
	if authorization != "Bearer provider-secret" {
		t.Errorf("authorization = %q, want the selected provider's namespaced key", authorization)
	}
}

func TestHostedTranscriber_UsesTheSelectedProvidersNamespacedKey(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"Buy now.","duration":1,"segments":[{"start":0,"end":1,"text":"Buy now."}]}`))
	}))
	t.Cleanup(server.Close)

	// The seam under test is provider selection, not ffmpeg. This stand-in writes the requested
	// output path so the production HostedTranscriber reaches its HTTP client without requiring a
	// media fixture or weakening its extraction contract.
	ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nfor last; do :; done\nprintf wav > \"$last\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	set := visionSet(t, map[string]string{
		"playout.ffmpeg_path":        ffmpeg,
		"llm.provider":               "openai",
		"llm.hosted_provider":        "openrouter",
		"llm.url":                    server.URL,
		"llm.model":                  "openai/gpt-4o-mini",
		"llm.api_key.openrouter":     "provider-secret",
		"filler.transcribe.provider": "hosted",
		"filler.transcribe.model":    "openai/whisper-large-v3",
	})

	segments, err := buildFillerMediaTools(set, nil).Transcribe(context.Background(), "clip.mp4", 0, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].Text != "Buy now." {
		t.Fatalf("segments = %+v", segments)
	}
	if authorization != "Bearer provider-secret" {
		t.Errorf("authorization = %q, want the selected provider's namespaced key", authorization)
	}
}

func TestFillerTagger_UsesTheSelectedProvidersNamespacedKey(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}]}`))
	}))
	t.Cleanup(server.Close)

	set := visionSet(t, map[string]string{
		"filler.ai_tagging":      "true",
		"llm.provider":           "openai",
		"llm.hosted_provider":    "openrouter",
		"llm.url":                server.URL,
		"llm.model":              "openai/gpt-4o-mini",
		"llm.api_key.openrouter": "provider-secret",
	})
	provider, _ := buildTagger(nil, set, filler.Layout{}, nil, nil, nil)
	if provider == nil {
		t.Fatal("tagger provider is nil for configured OpenRouter")
	}
	if _, err := provider.Chat(context.Background(), nil, llm.ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer provider-secret" {
		t.Errorf("authorization = %q, want the selected provider's namespaced key", authorization)
	}
}
