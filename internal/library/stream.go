package library

import (
	"net/url"
	"strings"
)

// Resolving a library item to something ffmpeg can read (§9.1, fact T1 — REVISED).
//
// T1 was: "Loomarr does not know where media lives" — LibraryItem carries an id, a rating
// and genres, no path. That never mattered while Tunarr did the playing, because Tunarr
// resolved paths itself. Internal playout needs a real ffmpeg input.
//
// ⚠ **The original T1 rejected the file path and always used the HTTP stream — that was wrong,
// and it is the reason playout transcoded everything.** Reading `GET /Videos/{id}/stream` and
// re-encoding is backwards when the media file is on a disk Loomarr can read: it forces a
// transcode (and an HTTP round-trip through the media server's own streaming layer) on content
// that is usually already a playable codec. DIRECT PLAY — read the file, `-c copy` when the
// codec is already compatible — is the standard every mature media server (Plex/Emby/Jellyfin)
// uses, and it is now the default here too.
//
// The old objection was that Emby's `Path` is EMBY'S view of the filesystem (`/data/tv/…`), not
// Loomarr's. True — but that is a PATH MAPPING problem, not an impossibility: the same file is
// mounted on the playout box at its own prefix (e.g. `/cifs/fictionalserver/tv/…`), and a
// prefix substitution resolves it. This is exactly the "path mapping" setting every media server
// exposes. So the resolution order is:
//
//	(a) The FILE (default). Fetch `Fields=Path`, apply `library.path_map` (§15) to translate the
//	    media-server prefix to the local mount, and if the mapped file is readable, hand ffmpeg the
//	    file directly — copy the video when its codec is compatible, transcode only what is not
//	    (ffprobe decides, playout.PlanCopy).
//
//	(b) `GET /Videos/{id}/stream?static=true` (FALLBACK). When no mapping resolves a readable local
//	    file — a media server on a different host with no shared mount — ffmpeg reads it over HTTP,
//	    as before. This keeps a no-shared-mount install working with zero config.
//
// `static=true` on the fallback asks for the ORIGINAL file, not a media-server transcode (playout
// does its own normalizing; two encodes in series is twice the CPU for a worse picture).

// StreamURL returns a URL ffmpeg can read for a library item.
//
// `static=true` asks the media server for the ORIGINAL file, not a transcode. That matters:
// playout does its own normalizing to a single profile (§9.1), and letting the media server
// transcode first would mean two encodes in series — twice the CPU for a worse picture.
//
// The token rides in the query string rather than a header because the consumer is ffmpeg,
// which is handed a URL and nothing else. That is the same reason playout's own segment URLs
// carry `playout_token` (§11 device auth): a process or an appliance given a URL cannot set
// headers.
//
// Returns "" when the media server is unconfigured — callers should treat that as "cannot
// play this", not as a relative URL, since an empty base would produce a request to
// ourselves.
func (c *Client) StreamURL(itemID string) string {
	return c.StreamURLForSource(itemID, "")
}

// StreamURLForSource returns a fresh authenticated original-file URL for one exact Library media
// source. MediaSourceId is omitted when the importer reported no source identity, preserving the
// ordinary server-selected original used by StreamURL.
func (c *Client) StreamURLForSource(itemID, sourceID string) string {
	c, err := c.operation()
	if err != nil {
		return ""
	}
	base := c.baseURL()
	if base == "" || itemID == "" {
		return ""
	}
	q := url.Values{}
	q.Set("static", "true")
	if sourceID = strings.TrimSpace(sourceID); sourceID != "" {
		q.Set("MediaSourceId", sourceID)
	}
	if t := c.token(); t != "" {
		// api_key is the query-string form both Emby and Jellyfin accept. The
		// header-based form (X-Emby-Token / MediaBrowser) is unavailable here — see above.
		q.Set("api_key", t)
	}
	return base + "/Videos/" + url.PathEscape(itemID) + "/stream?" + q.Encode()
}

// RedactStreamURL strips the token from a stream URL for logging.
//
// A playout log line naturally wants to say which URL it is reading, and that URL carries a
// credential. The settings Redactor (config-design §4) scrubs known secret VALUES from log
// output, but a URL assembled at call time is easy to hand to a log before it reaches that
// path — so this makes the safe form cheap to reach for.
func RedactStreamURL(raw string) string {
	i := strings.Index(raw, "api_key=")
	if i < 0 {
		return raw
	}
	end := strings.IndexByte(raw[i:], '&')
	if end < 0 {
		return raw[:i] + "api_key=‹redacted›"
	}
	return raw[:i] + "api_key=‹redacted›" + raw[i+end:]
}
