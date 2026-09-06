package clipfetch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
)

// Archive.org ingestion (§10): a plain-net/http walk of Archive's public JSON
// APIs (no key, no tooling — that's why the design picked net/http here). The
// walk, captured live 2026-07-13 (fixtures/archive/):
//
//  1. Resolve the URL to an Archive id, then GET /metadata/<id> →
//     {server, dir, files[], metadata{mediatype, title, description}}.
//  2. If mediatype == "collection": list member items via the advancedsearch
//     API (q=collection:<id>) and walk each item.
//  3. If an item: deterministically select the best declared source representation from files[],
//     download it to the drop-folder, and write an
//     info-JSON sidecar preserving title/description (the text signals the core's
//     AI tagging reads, §10 — same shape yt-dlp's --write-info-json produces).
//  4. Skip files already in the drop-folder (idempotent re-runs).
//
// HTTP + filesystem are injected so this is unit-tested against a mock Archive
// server with no live network (AGENTS.md: unit tests never touch the network).

// archiveBase is the Archive.org host; overridable in tests.
type archiveClient struct {
	base   string // e.g. https://archive.org
	scheme string // "https" (prod) or "http" (tests) — for the file-download URL
	http   *http.Client
	fs     fileSink
	maxPer int // cap items pulled per collection per pass (density guard)
}

// fileSink abstracts writing downloaded media + sidecars (real = disk).
type fileSink interface {
	Exists(path string) bool
	WriteStream(path string, r io.Reader) error
	WriteFile(path string, data []byte) error
	Inspect(path string) (digest string, size int64, clipHash string, err error)
}

// newArchiveClient builds the walker. base defaults to https://archive.org.
func newArchiveClient(base string, httpc *http.Client, fs fileSink) *archiveClient {
	if base == "" {
		base = "https://archive.org"
	}
	if httpc == nil {
		httpc = &http.Client{Timeout: 5 * time.Minute}
	}
	scheme := "https"
	if strings.HasPrefix(base, "http://") {
		scheme = "http"
	}
	return &archiveClient{base: strings.TrimRight(base, "/"), scheme: scheme, http: httpc, fs: fs, maxPer: 50}
}

// --- wire types (pinned to the live capture) ---

type metadataResp struct {
	Server   string          `json:"server"`
	Dir      string          `json:"dir"`
	Metadata archiveMetadata `json:"metadata"`
	Files    []archiveFile   `json:"files"`
}

type archiveMetadata struct {
	MediaType   string `json:"mediatype"` // "movies" (item) | "collection"
	Title       string `json:"title"`
	Description string `json:"description"`
	// LicenseURL is Archive's declared licence, e.g.
	// "https://creativecommons.org/licenses/by-nc-sa/4.0/" (V33).
	//
	// ⚠ The field is `licenseurl`, ONE WORD — not `license`, not `rights`. Both of those were
	// checked against the live API during the 2026-07-31 capture and neither exists.
	//
	// ⚠ **Usually absent, and that is the normal case.** In `classic_tv_commercials`, 667 of
	// 8362 items declare one — about 8%. Empty therefore means UNKNOWN, never "public domain",
	// and nothing downstream may treat it as permission.
	LicenseURL string `json:"licenseurl"`
}

type archiveFile struct {
	Name   string `json:"name"`
	Format string `json:"format"` // "h.264 IA", "MPEG4", "512Kb MPEG4", "Thumbnail", …
	Size   string `json:"size"`   // bytes, as a string
	Source string `json:"source"` // "original" | "derivative" | "metadata"
	// Length is the runtime, as a STRING and in one of two spellings: seconds with a decimal
	// ("91.09"), seconds without one ("660"), or — on some audio derivatives — "MM:SS". Absent
	// on every non-video file, which is what distinguishes a real derivative from a thumbnail.
	//
	// ⚠ Only present on DERIVATIVES and originals that Archive has probed. A parser must treat
	// absence as "unknown", never as zero: a 0-second clip is a real (and wrong) claim, while
	// "unknown" is the honest one and the UI renders it as such.
	Length string `json:"length"`
	// Height is the vertical resolution, as a string, and is what the Sources search renders as
	// a quality hint (480 → "480p"). Absent alongside Length on non-video files.
	Height string `json:"height"`
	// Width is less consistently present than Height but participates when Archive declares it.
	// Missing remains unknown and never becomes a fabricated square pixel count.
	Width string `json:"width"`
}

type searchResp struct {
	Response struct {
		// NumFound is the collection's TOTAL size, not len(Docs) — the search is paged, so a
		// listing that reported len(Docs) would tell an operator a 8362-item collection has 5.
		NumFound int         `json:"numFound"`
		Docs     []searchDoc `json:"docs"`
	} `json:"response"`
}

// searchDoc is one item in a collection listing.
//
// ⚠ **Every field except Identifier is OPTIONAL, and that is the live API's behaviour rather
// than defensive coding.** Solr omits an absent field entirely instead of sending an empty
// value, so of five real docs captured from `classic_tv_commercials`: two carry a licence,
// three do not, and two have no `year`. A parser assuming any of these is present passes on a
// tidy fixture and fails on the first real collection — which is why the pinned fixture keeps
// all four shapes.
type searchDoc struct {
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	// LicenseURL is absent on ~92% of items. Empty means UNKNOWN, never "public domain".
	LicenseURL string `json:"licenseurl"`
	// Year is Archive's own metadata, and it is a WEAK era hint at best: it is when the item
	// was catalogued as being from, which for a compilation upload is often the upload year
	// rather than the broadcast year. Carried so a human can read it; never used to set Era,
	// for the same reason `upload_date` is not (see filler/sidecar.go).
	//
	// ⚠ An INTEGER on the wire, not a string — I typed it `string` from memory and the pinned
	// fixture rejected it ("cannot unmarshal number into … .year of type string"). Absent
	// entirely on items with no year, which `omitempty`-style zero handling covers: 0 renders
	// as "no year" rather than as the year 0.
	Year int `json:"year"`
	// Date is Archive's catalogued date, RFC3339 ("1996-07-28T00:00:00Z"), and the field the
	// Sources search row renders. Absent on 2 of the 5 pinned docs — the same optionality every
	// field but Identifier has.
	//
	// ⚠ Carries the SAME weak-hint caveat as Year, and the live data shows it plainly: one
	// pinned doc is dated 1996 while its `publicdate` is 2023. This is when the item is
	// catalogued as being FROM, which for an upload is often neither the broadcast date nor the
	// upload date. Rendered for a human to read; never used to set a clip's era.
	Date string `json:"date"`
}

type registeredSourceContextKey struct{}

type acquisitionContext struct {
	sourceID       string
	acquisitionID  string
	publicationDir string
}

func withAcquisition(ctx context.Context, sourceID, acquisitionID, publicationDir string) context.Context {
	return context.WithValue(ctx, registeredSourceContextKey{}, acquisitionContext{
		sourceID: sourceID, acquisitionID: acquisitionID, publicationDir: publicationDir,
	})
}

func acquisitionFrom(ctx context.Context) acquisitionContext {
	acquisition, _ := ctx.Value(registeredSourceContextKey{}).(acquisitionContext)
	return acquisition
}

// walk resolves an Archive URL/id and downloads its video content. Returns
// (fetched, skipped, error). A per-item failure inside a collection is logged by
// the caller via the aggregate error; here a collection continues past a bad item.
func (c *archiveClient) walk(ctx context.Context, rawURL, dropDir string) (int, int, []Output, error) {
	id := archiveIDFromURL(rawURL)
	if id == "" {
		return 0, 0, nil, fmt.Errorf("archive: cannot extract id from %q", rawURL)
	}
	meta, err := c.metadata(ctx, id)
	if err != nil {
		return 0, 0, nil, err
	}
	if meta.Metadata.MediaType == "collection" {
		return c.walkCollection(ctx, id, dropDir)
	}
	return c.downloadItem(ctx, id, meta, dropDir)
}

// walkCollection lists a collection's member items and walks each (capped).
func (c *archiveClient) walkCollection(ctx context.Context, collID, dropDir string) (int, int, []Output, error) {
	ids, err := c.collectionItems(ctx, collID)
	if err != nil {
		return 0, 0, nil, err
	}
	var fetched, skipped int
	var outputs []Output
	var failures error
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return fetched, skipped, outputs, ctx.Err()
		default:
		}
		meta, err := c.metadata(ctx, id)
		if err != nil {
			failures = errors.Join(failures, err)
			continue // skip a bad item, keep the collection going
		}
		f, s, itemOutputs, err := c.downloadItem(ctx, id, meta, dropDir)
		fetched += f
		skipped += s
		outputs = append(outputs, itemOutputs...)
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
	}
	return fetched, skipped, outputs, failures
}

// downloadItem picks the best video derivative and downloads it + a sidecar.
func (c *archiveClient) downloadItem(ctx context.Context, id string, meta metadataResp, dropDir string) (int, int, []Output, error) {
	file, ok := pickVideoFile(meta.Files)
	if !ok {
		return 0, 0, nil, nil // no video file (e.g. audio-only) → nothing to fetch
	}
	// Target filenames in the drop-folder: "<id> - <file>" so ids don't collide.
	base := sanitize(id + " - " + file.Name)
	mediaPath := filepath.Join(dropDir, base)
	sidecarPath := strings.TrimSuffix(mediaPath, filepath.Ext(mediaPath)) + ".info.json"

	acquisition := acquisitionFrom(ctx)
	existingPath := mediaPath
	if acquisition.publicationDir != "" {
		existingPath = filepath.Join(acquisition.publicationDir, base)
	}
	if c.fs.Exists(existingPath) {
		return 0, 1, nil, nil // idempotent: already fetched
	}

	// Download from <scheme>://<server><dir>/<url-encoded file name> (prod: https;
	// the server host + dir come from the metadata response).
	dlURL := c.scheme + "://" + meta.Server + meta.Dir + "/" + url.PathEscape(file.Name)
	if err := c.fetchTo(ctx, dlURL, mediaPath); err != nil {
		return 0, 0, nil, fmt.Errorf("archive download %s: %w", id, err)
	}
	digest, size, clipHash, err := c.fs.Inspect(mediaPath)
	if err != nil {
		return 1, 0, nil, fmt.Errorf("inspect archive download %s: %w", id, err)
	}
	output := Output{MediaPath: mediaPath, SidecarPath: sidecarPath, SHA256: digest, Bytes: size, ClipHash: clipHash}
	// Write the info-JSON sidecar (title/description → AI-tagging text signals, §10).
	fields := map[string]any{
		"id":                     id,
		"title":                  meta.Metadata.Title,
		"description":            meta.Metadata.Description,
		"source":                 "archive.org",
		"webpage_url":            c.base + "/details/" + id,
		"archive_representation": archiveRepresentationEvidence(file),
	}
	// ⚠ OMITTED when Archive declares none, rather than written as "". About 92% of items
	// carry no licence, and an empty string in a sidecar reads as "we looked and it is
	// unlicensed" — the absent key reads as "unknown", which is the true claim. The sidecar
	// parser treats a missing key and an empty value the same way, so this costs nothing.
	if meta.Metadata.LicenseURL != "" {
		fields["license"] = meta.Metadata.LicenseURL
	}
	// ⚠ **Mark it as OURS.** This is the held/filed fork's only signal (§10 V38c): a clip Loomarr
	// downloaded waits in Incoming for a human, while one an operator dropped in is filed on
	// sight. The downloader is the only party that knows which this is — the sync sees a file in
	// a folder and cannot tell.
	//
	// Nothing wrote this until V38c.8, so every auto-fetched clip landed `held=false` and went
	// straight to air unreviewed. Caught by running auto-fetch against real collections and
	// reading the rows back, not by any test.
	fields[filler.SidecarLoomarrKey()] = filler.SidecarFetchedMarkForAcquisition(
		acquisition.sourceID, acquisition.acquisitionID,
	)
	sidecar, _ := json.MarshalIndent(fields, "", "  ")
	if err := c.fs.WriteFile(sidecarPath, sidecar); err != nil {
		output.SidecarPath = ""
		output.Repair = "archive sidecar could not be written: " + err.Error()
		return 1, 0, []Output{output}, fmt.Errorf("archive sidecar %s: %w", id, err)
	}
	return 1, 0, []Output{output}, nil
}

func (c *archiveClient) metadata(ctx context.Context, id string) (metadataResp, error) {
	var out metadataResp
	if err := c.getJSON(ctx, c.base+"/metadata/"+url.PathEscape(id), &out); err != nil {
		return metadataResp{}, fmt.Errorf("archive metadata %s: %w", id, err)
	}
	return out, nil
}

func (c *archiveClient) collectionItems(ctx context.Context, collID string) ([]string, error) {
	q := url.Values{}
	q.Set("q", "collection:"+collID)
	q.Set("fl[]", "identifier")
	q.Set("rows", strconv.Itoa(c.maxPer))
	q.Set("output", "json")
	var out searchResp
	if err := c.getJSON(ctx, c.base+"/advancedsearch.php?"+q.Encode(), &out); err != nil {
		return nil, fmt.Errorf("archive collection %s: %w", collID, err)
	}
	ids := make([]string, 0, len(out.Response.Docs))
	for _, d := range out.Response.Docs {
		ids = append(ids, d.Identifier)
	}
	return ids, nil
}

func (c *archiveClient) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *archiveClient) fetchTo(ctx context.Context, u, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	return c.fs.WriteStream(path, resp.Body)
}

// archiveIDFromURL extracts the Archive item/collection id from a URL or bare id.
// Handles archive.org/details/<id>, archive.org/metadata/<id>, and a plain id.
func archiveIDFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "/") {
		return raw // already a bare id
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if (p == "details" || p == "metadata") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	// Fall back to the last path segment.
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

// sanitize makes a filename safe for the drop-folder (drops path separators).
func sanitize(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	return name
}

// --- the real filesystem sink ---

type diskSink struct{}

func (diskSink) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (diskSink) WriteStream(path string, r io.Reader) error {
	// Write to a temp file then rename, so a partial download isn't seen as
	// complete by the media server's scan (atomic publish).
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (diskSink) WriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func (diskSink) Inspect(path string) (string, int64, string, error) {
	return inspectOutput(path)
}

func inspectBytes(data []byte) (string, int64, string, error) {
	digest := sha256.Sum256(data)
	clipHash, err := filler.ClipIDFromReaderAt(bytes.NewReader(data), int64(len(data)))
	return fmt.Sprintf("%x", digest[:]), int64(len(data)), clipHash, err
}
