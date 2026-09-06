package clipfetch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/proctree"
)

// This file holds the REAL downloaders — the ones that touch the network and the
// yt-dlp binary. They are behind the Downloader interface so the Ingestor's
// orchestration is unit-tested with fakes. The yt-dlp process boundary is tested
// offline with a repository-built executable; Archive HTTP remains behind its
// test server boundary (AGENTS.md: unit tests never touch the network).

// YtDlpDownloader shells out to the bundled yt-dlp (with ffmpeg for
// post-processing) to fetch a YouTube playlist/video into the drop-folder,
// preserving the info-JSON sidecar yt-dlp writes (the source title/description
// the core's AI tagging reads as text signals, §10). The shared process-tree
// supervisor owns yt-dlp and every ffmpeg or Deno descendant (§9.1).
type YtDlpDownloader struct {
	ytDlpPath  string
	ffmpegPath string
}

const ytDlpDiagnosticLimit = 64 << 10

type diagnosticTail struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func (b *diagnosticTail) Write(p []byte) (int, error) {
	written := len(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
		b.truncated = true
		return written, nil
	}
	if overflow := len(b.data) + len(p) - b.limit; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return written, nil
}

func (b *diagnosticTail) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.truncated {
		return "[... yt-dlp output truncated ...]\n" + string(b.data)
	}
	return string(b.data)
}

// NewYtDlpDownloader builds the yt-dlp downloader.
func NewYtDlpDownloader(ytDlpPath, ffmpegPath string) *YtDlpDownloader {
	return &YtDlpDownloader{ytDlpPath: ytDlpPath, ffmpegPath: ffmpegPath}
}

// Download runs supervised yt-dlp for one source. It writes video + `.info.json` sidecars
// into dropDir and uses --download-archive so a re-run skips already-fetched
// items (idempotent ingest). It parses only yt-dlp's final paths so provenance
// reaches the exact sidecars created by this invocation; the media server still
// scans the folder and the core syncs from there. It returns (0,0,nil) on success,
// surfacing only exec failure.
func (d *YtDlpDownloader) Download(ctx context.Context, src Source, dropDir string) (DownloadResult, error) {
	absDropDir, err := filepath.Abs(dropDir)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("resolve yt-dlp drop directory: %w", err)
	}
	if err := os.MkdirAll(absDropDir, 0o750); err != nil {
		return DownloadResult{}, fmt.Errorf("create yt-dlp drop directory: %w", err)
	}
	archiveFile := filepath.Join(absDropDir, ".yt-dlp-archive.txt")
	if src.archiveAttempt {
		// Each staged attempt starts from the durable shared archive, but never advances it
		// until Ingestor has persisted ownership. Resetting this private file is deliberate:
		// a previous failed manifest write must not poison a retry in the same staging path.
		if err := seedAttemptArchive(src.PublicationDir, archiveFile); err != nil {
			return DownloadResult{}, err
		}
	} else if src.PublicationDir != "" {
		// Direct downloader users retain their historic shared-archive contract.
		archiveFile = filepath.Join(src.PublicationDir, ".yt-dlp-archive.txt")
	}
	resultFile, err := os.CreateTemp(absDropDir, ".loomarr-ytdlp-results-*")
	if err != nil {
		return DownloadResult{}, fmt.Errorf("create yt-dlp result file: %w", err)
	}
	resultPath := resultFile.Name()
	if err := resultFile.Close(); err != nil {
		_ = os.Remove(resultPath)
		return DownloadResult{}, fmt.Errorf("close yt-dlp result file: %w", err)
	}
	defer func() { _ = os.Remove(resultPath) }()
	// -o with a sanitized template into the drop folder; --write-info-json so the
	// title/description survive for AI tagging (§10); --download-archive for
	// idempotent re-runs; --ffmpeg-location for the bundled binary.
	args := []string{
		"--no-progress",
		"--write-info-json",
		"--download-archive", archiveFile,
		"--ffmpeg-location", d.ffmpegPath,
		"-o", filepath.Join(absDropDir, "%(title)s [%(id)s].%(ext)s"),
		"--print-to-file", "after_move:%(id)s\t%(filepath)j", resultPath,
		src.URL,
	}
	cmd := exec.Command(d.ytDlpPath, args...)
	out := diagnosticTail{limit: ytDlpDiagnosticLimit}
	cmd.Stdout = &out
	cmd.Stderr = &out
	supervisor, err := proctree.Start(ctx, cmd)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("yt-dlp %s: %w: %s", src.URL, err, out.String())
	}
	err = supervisor.Wait()
	if supervisor.Stopped() {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return DownloadResult{}, fmt.Errorf("yt-dlp %s: %w: %s", src.URL, ctxErr, out.String())
		}
	}
	if err != nil {
		return DownloadResult{}, fmt.Errorf("yt-dlp %s: %w: %s", src.URL, err, out.String())
	}
	// ⚠ **Mark what we just downloaded as OURS** — the held/filed fork's only signal (§10 V38c).
	// A clip Loomarr fetched waits in Incoming for a human; one an operator dropped in is filed on
	// sight. Only the downloader can tell them apart.
	//
	// Stamped AFTERWARDS rather than written here, because yt-dlp owns this sidecar
	// (`--write-info-json`) and re-creating it would throw away the title and description that
	// are the tagger's real text signals. `stampFetched` merges into what yt-dlp wrote.
	//
	// A stamp failure is returned with the exact output as a held repair. Durable acquisition
	// ownership prevents missing portable provenance from making the media intake-visible.
	root, err := os.OpenRoot(absDropDir)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("open yt-dlp output root: %w", err)
	}
	defer func() { _ = root.Close() }()
	mediaPaths, err := ytDlpMediaPaths(root, resultPath)
	if err != nil {
		return DownloadResult{}, err
	}
	result := DownloadResult{Fetched: len(mediaPaths), Outputs: make([]Output, 0, len(mediaPaths))}
	var provenanceErr error
	var attemptArchive []string
	if src.archiveAttempt {
		attemptArchive, err = archiveLines(archiveFile)
		if err != nil {
			return DownloadResult{}, fmt.Errorf("read yt-dlp attempt archive: %w", err)
		}
	}
	for _, reported := range mediaPaths {
		relativeMedia := reported.path
		mediaPath := filepath.Join(absDropDir, relativeMedia)
		digest, size, clipHash, inspectErr := inspectOutput(mediaPath)
		if inspectErr != nil {
			provenanceErr = errors.Join(provenanceErr, inspectErr)
			continue
		}
		relativeSidecar := strings.TrimSuffix(relativeMedia, filepath.Ext(relativeMedia)) + ".info.json"
		sidecarFile, sidecarErr := root.OpenFile(relativeSidecar, os.O_RDWR, 0)
		output := Output{
			MediaPath: mediaPath, SidecarPath: filepath.Join(absDropDir, relativeSidecar),
			SHA256: digest, Bytes: size, ClipHash: clipHash,
		}
		if sidecarErr != nil {
			output.SidecarPath = ""
			output.Repair = "missing or unreadable yt-dlp sidecar: " + sidecarErr.Error()
			provenanceErr = errors.Join(provenanceErr, errors.New(output.Repair))
		} else if stampErr := stampFetched(root, fetchedSidecar{path: relativeSidecar, file: sidecarFile}, src.ID, src.AcquisitionID); stampErr != nil {
			output.Repair = stampErr.Error()
			provenanceErr = errors.Join(provenanceErr, stampErr)
		}
		output.ArchiveID = reported.archiveID
		if output.ArchiveID == "" {
			output.ArchiveID = archiveIDFromSidecar(output.SidecarPath)
		}
		if src.archiveAttempt {
			entry, entryErr := exactArchiveEntry(attemptArchive, output.ArchiveID)
			if entryErr != nil {
				if output.Repair == "" {
					output.Repair = entryErr.Error()
				}
				provenanceErr = errors.Join(provenanceErr, entryErr)
			} else {
				output.ArchiveEntry = entry
			}
		}
		result.Outputs = append(result.Outputs, output)
	}
	return result, provenanceErr
}

// CommitArchive merges this attempt's yt-dlp archive into the shared publication archive. The
// caller invokes it only after staged ownership is durable; a failed manifest write therefore
// leaves the shared retry authority untouched while retaining the hidden bytes for repair.
func (d *YtDlpDownloader) CommitArchive(dropDir, publicationDir string, outputs []Output) error {
	_ = dropDir
	if publicationDir == "" {
		return nil
	}
	entries := make([]string, 0, len(outputs))
	for _, output := range outputs {
		if output.ArchiveEntry != "" {
			entries = append(entries, output.ArchiveEntry)
		}
	}
	return commitProviderArchive(publicationDir, entries)
}

func commitProviderArchive(publicationDir string, entries []string) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(publicationDir, 0o750); err != nil {
		return fmt.Errorf("create yt-dlp publication archive directory: %w", err)
	}
	shared := filepath.Join(publicationDir, ".yt-dlp-archive.txt")
	existing, err := archiveLines(shared)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read yt-dlp publication archive: %w", err)
	}
	known := make(map[string]struct{}, len(existing))
	for _, line := range existing {
		known[line] = struct{}{}
	}
	var additions []string
	for _, line := range entries {
		if err := filler.ValidateProviderArchiveEntry(line); err != nil {
			return fmt.Errorf("validate yt-dlp archive entry: %w", err)
		}
		if _, already := known[line]; already {
			continue
		}
		known[line] = struct{}{}
		additions = append(additions, line)
	}
	if len(additions) == 0 {
		return nil
	}
	file, err := os.OpenFile(shared, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open yt-dlp publication archive: %w", err)
	}
	defer func() { _ = file.Close() }()
	for _, line := range additions {
		if _, err := file.WriteString(line + "\n"); err != nil {
			return fmt.Errorf("append yt-dlp publication archive: %w", err)
		}
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync yt-dlp publication archive: %w", err)
	}
	return nil
}

func seedAttemptArchive(publicationDir, attempt string) error {
	shared, err := os.ReadFile(filepath.Join(publicationDir, ".yt-dlp-archive.txt"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read yt-dlp publication archive: %w", err)
	}
	if err := os.WriteFile(attempt, shared, 0o640); err != nil {
		return fmt.Errorf("seed yt-dlp attempt archive: %w", err)
	}
	return nil
}

func archiveLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4*1024), 1024*1024)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}

func exactArchiveEntry(lines []string, archiveID string) (string, error) {
	if strings.TrimSpace(archiveID) == "" || strings.ContainsAny(archiveID, " \t\r\n") {
		return "", errors.New("yt-dlp output has no exact provider archive identity")
	}
	var match string
	for _, line := range lines {
		if filler.ValidateProviderArchiveEntry(line) != nil {
			continue
		}
		fields := strings.Fields(line)
		if fields[1] != archiveID {
			continue
		}
		if match != "" && match != line {
			return "", errors.New("yt-dlp output has ambiguous provider archive identity")
		}
		match = line
	}
	if match == "" {
		return "", errors.New("yt-dlp output has no matching provider archive entry")
	}
	return match, nil
}

func archiveIDFromSidecar(path string) string {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var sidecar struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(bytes, &sidecar) != nil {
		return ""
	}
	return sidecar.ID
}

// ytDlpMediaPaths reads yt-dlp's output ID plus JSON-encoded after-move path from the private
// result file for this invocation. Legacy path-only fixture records remain accepted; in that case
// the exact ID is recovered from the paired sidecar. Ordinary diagnostics are never provenance.
// A skipped retry writes no records. Candidates are opened through root, so their descriptor stays
// bound to the file yt-dlp named even if its path changes before stamping.
type ytDlpReportedMedia struct {
	path      string
	archiveID string
}

func ytDlpMediaPaths(root *os.Root, resultPath string) ([]ytDlpReportedMedia, error) {
	file, err := os.Open(resultPath)
	if err != nil {
		return nil, fmt.Errorf("open yt-dlp result file: %w", err)
	}
	defer func() { _ = file.Close() }()

	var paths []ytDlpReportedMedia
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		archiveID := ""
		if before, after, ok := strings.Cut(line, "\t"); ok {
			archiveID, line = before, after
		}
		var media string
		if err := json.Unmarshal([]byte(line), &media); err != nil || media == "" {
			continue
		}
		if !filepath.IsAbs(media) {
			media = filepath.Join(root.Name(), media)
		}
		relativeMedia, err := filepath.Rel(root.Name(), media)
		if err != nil || !withinDir(relativeMedia) {
			continue
		}
		if _, ok := seen[relativeMedia]; ok {
			continue
		}
		info, err := root.Lstat(relativeMedia)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		seen[relativeMedia] = struct{}{}
		paths = append(paths, ytDlpReportedMedia{path: relativeMedia, archiveID: archiveID})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read yt-dlp result file: %w", err)
	}
	return paths, nil
}

func withinDir(path string) bool {
	return path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator)) && !filepath.IsAbs(path)
}

type fetchedSidecar struct {
	path string
	file *os.File
}

// stampFetched adds Loomarr's `fetchedBy` mark to sidecars yt-dlp demonstrably produced during
// this invocation. It must never sweep the drop folder: a later invocation must not claim an
// unrelated operator drop or a previous download as its own acquisition.
//
// Anything already stamped is left alone, so a re-run is cheap and idempotent.
func stampFetched(root *os.Root, sidecar fetchedSidecar, sourceID, acquisitionID string) error {
	defer func() { _ = sidecar.file.Close() }()
	fileInfo, err := sidecar.file.Stat()
	if err != nil {
		return fmt.Errorf("stat yt-dlp sidecar: %w", err)
	}
	pathInfo, err := root.Lstat(sidecar.path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !os.SameFile(fileInfo, pathInfo) {
		return errors.New("yt-dlp sidecar was replaced or is not a regular file")
	}
	if _, err := sidecar.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek yt-dlp sidecar: %w", err)
	}
	raw, err := io.ReadAll(sidecar.file)
	if err != nil {
		return fmt.Errorf("read yt-dlp sidecar: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("yt-dlp sidecar is not readable JSON: %w", err)
	}
	if _, done := doc[filler.SidecarLoomarrKey()]; done {
		return nil
	}
	doc[filler.SidecarLoomarrKey()] = filler.SidecarFetchedMarkForAcquisition(sourceID, acquisitionID)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode yt-dlp provenance: %w", err)
	}
	if err := sidecar.file.Truncate(0); err != nil {
		return fmt.Errorf("truncate yt-dlp sidecar: %w", err)
	}
	if _, err := sidecar.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek yt-dlp sidecar for write: %w", err)
	}
	if _, err := sidecar.file.Write(out); err != nil { //nolint:gosec // metadata beside media the operator owns
		return fmt.Errorf("write yt-dlp provenance: %w", err)
	}
	if err := sidecar.file.Sync(); err != nil {
		return fmt.Errorf("sync yt-dlp provenance: %w", err)
	}
	return nil
}

func inspectOutput(path string) (digest string, size int64, clipHash string, err error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", 0, "", fmt.Errorf("downloaded output %s is not a regular file", path)
	}
	digest, size, err = filler.FileSHA256(path)
	if err != nil {
		return "", 0, "", err
	}
	clipHash, err = filler.ClipID(path)
	if err != nil {
		return "", 0, "", err
	}
	return digest, size, clipHash, nil
}

// ArchiveDownloader fetches an Archive.org item/collection via plain net/http
// (no special tooling — §10). It walks Archive's public JSON APIs (metadata +
// advancedsearch), picks the smallest video derivative, and writes the media +
// an info-JSON sidecar into the drop-folder. The walk logic lives in
// archiveClient (injectable HTTP + fs → unit-tested against a mock server).
type ArchiveDownloader struct {
	client *archiveClient
}

// NewArchiveDownloader builds the Archive.org downloader against the real host.
// preferOriginal keeps full-quality masters instead of the small derivative
// (default false — the derivative is right for filler; §10).
func NewArchiveDownloader(preferOriginal bool) *ArchiveDownloader {
	c := newArchiveClient("https://archive.org", nil, diskSink{})
	c.preferOriginal = preferOriginal
	return &ArchiveDownloader{client: c}
}

// Download fetches an Archive.org source into dropDir (§10).
func (d *ArchiveDownloader) Download(ctx context.Context, src Source, dropDir string) (DownloadResult, error) {
	fetched, skipped, outputs, err := d.client.walk(withAcquisition(ctx, src.ID, src.AcquisitionID, src.PublicationDir), src.URL, dropDir)
	return DownloadResult{Fetched: fetched, Skipped: skipped, Outputs: outputs}, err
}

var (
	_ Downloader = (*YtDlpDownloader)(nil)
	_ Downloader = (*ArchiveDownloader)(nil)
)
