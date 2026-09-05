package library_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/library"
)

// T1's resolution (§9.1): a library item becomes an ffmpeg-readable URL. The shape matters
// in specific ways, each of which has a failure mode that is not obvious from the outside.
func TestStreamURL_Shape(t *testing.T) {
	c := library.New(library.Emby, "http://emby:8096/", "tok-123", "dev-1")
	got := c.StreamURL("2920908")

	if !strings.HasPrefix(got, "http://emby:8096/Videos/2920908/stream?") {
		t.Fatalf("unexpected URL: %q", got)
	}
	// static=true asks for the ORIGINAL file. Without it the media server may transcode
	// first, and playout would then re-encode its output — two encodes in series, twice the
	// CPU, worse picture.
	if !strings.Contains(got, "static=true") {
		t.Error("missing static=true — the media server would transcode before we do")
	}
	// The token rides in the query because the consumer is ffmpeg, handed a URL and nothing
	// else. It cannot set a header.
	if !strings.Contains(got, "api_key=tok-123") {
		t.Error("missing api_key — ffmpeg cannot authenticate with a header")
	}
	// A trailing slash on the configured base must not produce a double slash.
	if strings.Contains(got, "8096//Videos") {
		t.Errorf("double slash from the trailing-slash base: %q", got)
	}
}

// An unconfigured media server must yield "" rather than a relative URL: a relative URL
// would send ffmpeg to ask US for the file, which is a confusing self-request rather than a
// clear "cannot play this".
func TestStreamURL_UnconfiguredYieldsEmpty(t *testing.T) {
	if got := library.New(library.Emby, "", "", "dev-1").StreamURL("123"); got != "" {
		t.Errorf("no base URL must yield empty, got %q", got)
	}
	c := library.New(library.Emby, "http://emby:8096", "tok", "dev-1")
	if got := c.StreamURL(""); got != "" {
		t.Errorf("no item id must yield empty, got %q", got)
	}
}

// An item id with characters needing escaping must not break the path.
func TestStreamURL_EscapesTheItemID(t *testing.T) {
	c := library.New(library.Emby, "http://emby:8096", "tok", "dev-1")
	got := c.StreamURL("weird id/../etc")
	if strings.Contains(got, "/../") {
		t.Errorf("item id not escaped — path traversal reaches the media server: %q", got)
	}
}

func TestStreamURLForSourceSelectsTheImportedOriginal(t *testing.T) {
	t.Parallel()
	c := library.New(library.Emby, "http://emby:8096", "token", "dev-1")
	got := c.StreamURLForSource("item 1", "source/4k")
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.EscapedPath() != "/Videos/item%201/stream" || parsed.Query().Get("MediaSourceId") != "source/4k" ||
		parsed.Query().Get("static") != "true" || parsed.Query().Get("api_key") != "token" {
		t.Fatalf("source stream URL = %q", got)
	}
}

// A playout log line naturally wants to name the URL it is reading, and that URL carries a
// credential.
func TestRedactStreamURL_HidesTheToken(t *testing.T) {
	c := library.New(library.Emby, "http://emby:8096", "super-secret", "dev-1")
	raw := c.StreamURL("42")
	red := library.RedactStreamURL(raw)

	if strings.Contains(red, "super-secret") {
		t.Errorf("token survived redaction: %q", red)
	}
	// The rest must remain useful — a redacted line still has to identify the item.
	if !strings.Contains(red, "/Videos/42/stream") {
		t.Errorf("redaction destroyed the useful part: %q", red)
	}
	// And a URL with no token is returned untouched.
	if got := library.RedactStreamURL("http://x/y"); got != "http://x/y" {
		t.Errorf("a tokenless URL should pass through, got %q", got)
	}
}
