// Package clipfetch downloads filler clips into the drop-folder (design §10, §16).
//
// It runs IN THE CORE, and the tooling it shells out to ships in the single image (§16) —
// so the `ingest` feature gate now reports a DEGRADED install (a binary that will not run)
// rather than an opt-in an operator has to take. ⚠ This doc used to describe both a
// `loomarr-ingest` sidecar and a `loomarr:filler` image variant (retired-ok);
// §10 records why each was
// reversed, and neither exists.
//
// ⚠ Named clipfetch, NOT ingest — but the reason has itself been retired-ok. This doc said
// "because internal/ingest is the Sonarr/Radarr WEBHOOK handler"; there is no internal/ingest
// package and there is no inbound arr hook (acquisition state comes from polling — see
// internal/reconcile). The name still earns its keep, for a smaller reason: `ingest` is what
// §10 calls the CLIP pipeline in internal/filler, so a package named ingest here would collide
// with that one autocomplete apart.
//
// The orchestration here is testable with fake downloaders; the real yt-dlp exec +
// Archive HTTP live behind the Downloader interface, so unit tests never touch the
// network or the yt-dlp binary (AGENTS.md testing rules).
package clipfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
)

// Kind is the source type — it selects the downloader.
type Kind string

const (
	YouTube Kind = "youtube" // a YouTube playlist/video (yt-dlp)
	Archive Kind = "archive" // an Archive.org collection/item (net/http)
)

// Source is one ingestion target the sidecar pulls into the drop-folder.
type Source struct {
	// ID is the registered source policy responsible for this acquisition. Empty means a manual
	// ingest whose arrivals use the folder admission policy.
	ID string
	// AcquisitionID ties every downloaded clip back to the durable run that requested it.
	// It is metadata only: admission continues to be governed by the registered source ID.
	AcquisitionID string
	Kind          Kind
	URL           string
	// PublicationDir is set only by the Ingestor while downloading into hidden staging. It lets
	// adapters retain their provider-specific idempotency index without publishing into it.
	PublicationDir string
	archiveAttempt bool
}

// Output is one exact file produced by a downloader. Provider adapters identify bytes; the
// Ingestor turns them into durable acquisition manifests and owns publication.
type Output struct {
	MediaPath   string
	SidecarPath string
	SHA256      string
	Bytes       int64
	ClipHash    string
	// ArchiveID is the provider identity reported by yt-dlp for this exact output. It is used
	// to bind the exact archive line emitted by the same invocation.
	ArchiveID string
	// ArchiveEntry is the exact provider deduplication line bound to this reported output.
	ArchiveEntry string
	Repair       string
}

type DownloadResult struct {
	Fetched int
	Skipped int
	Outputs []Output
}

// KindForURL infers the source kind from a URL (youtube.com / youtu.be →
// youtube; archive.org → archive). Unknown hosts default to youtube (yt-dlp
// handles the widest set of sites).
func KindForURL(url string) Kind {
	l := strings.ToLower(url)
	switch {
	case strings.Contains(l, "archive.org"):
		return Archive
	default:
		return YouTube
	}
}

// Downloader pulls one source's media into a drop directory, returning how many
// items it fetched (new) vs skipped (already present). Implementations: the real
// yt-dlp exec + Archive HTTP; tests use fakes.
type Downloader interface {
	// Download fetches src into outputDir and returns the exact completed outputs. The caller owns
	// whether and when those files become visible to ordinary intake.
	Download(ctx context.Context, src Source, outputDir string) (DownloadResult, error)
}

type ArtifactWriter interface {
	UpsertAcquisitionArtifacts(context.Context, []filler.AcquisitionArtifact) error
}

// ArchiveCommitter advances provider deduplication only after the downloaded output has a
// durable acquisition manifest. It is intentionally optional so non-yt-dlp downloaders retain
// their existing contract.
type ArchiveCommitter interface {
	CommitArchive(dropDir, publicationDir string, outputs []Output) error
}

// Ingestor orchestrates a pass over all sources, dispatching each to the right
// downloader. Deliberately thin — the value is in "which downloader, into where",
// not in the download mechanics.
type Ingestor struct {
	youtube Downloader
	archive Downloader
	dropDir string
	log     *slog.Logger
	writer  ArtifactWriter
	now     func() time.Time
}

// New builds an Ingestor from the two real (or fake) downloaders + the drop dir.
func New(youtube, archive Downloader, dropDir string, log *slog.Logger) *Ingestor {
	return &Ingestor{youtube: youtube, archive: archive, dropDir: dropDir, log: log, now: time.Now}
}

// WithArtifactWriter enables durable staged publication. Production always supplies it; the
// writer-free form remains useful for the narrow dispatcher tests that use filesystem-free fakes.
func (i *Ingestor) WithArtifactWriter(writer ArtifactWriter) *Ingestor {
	i.writer = writer
	return i
}

// Result aggregates one pass.
type Result struct {
	Fetched int
	Skipped int
	Failed  int
	// Empty counts sources that returned no clips and no error — a
	// nonexistent/typo'd id or empty source (surfaced so it isn't silent).
	Empty     int
	Artifacts []filler.AcquisitionArtifact
}

// Run ingests every source once. A failed source is logged and counted, never
// fatal — one bad playlist must not stop the rest (§6 resilience spirit).
func (i *Ingestor) Run(ctx context.Context, sources []Source) Result {
	var res Result
	for sourceIndex, src := range sources {
		select {
		case <-ctx.Done():
			return res
		default:
		}
		dl := i.downloaderFor(src.Kind)
		if dl == nil {
			i.logf("no downloader for kind %q (%s)", src.Kind, src.URL)
			res.Failed++
			continue
		}
		outputDir := i.dropDir
		if i.writer != nil {
			outputDir = filepath.Join(i.dropDir, ".loomarr-acquisitions", src.AcquisitionID, fmt.Sprintf("%03d", sourceIndex))
			if err := os.MkdirAll(outputDir, 0o750); err != nil {
				i.logf("prepare quarantine for %s: %v", src.URL, err)
				res.Failed++
				continue
			}
			src.PublicationDir = i.dropDir
			src.archiveAttempt = true
		}
		download, err := dl.Download(ctx, src, outputDir)
		manifests := i.manifests(src, outputDir, download.Outputs)
		if len(manifests) > 0 && i.writer != nil {
			if persistErr := i.writer.UpsertAcquisitionArtifacts(ctx, manifests); persistErr != nil {
				i.logf("record acquisition outputs for %s: %v", src.URL, persistErr)
				res.Failed++
				continue
			}
			if committer, ok := dl.(ArchiveCommitter); ok {
				if commitErr := committer.CommitArchive(outputDir, i.dropDir, download.Outputs); commitErr != nil {
					i.logf("commit provider archive for %s: %v", src.URL, commitErr)
					res.Failed++
					continue
				}
				archiveUpdated := false
				for index := range manifests {
					if manifests[index].ProviderArchiveEntry == "" {
						continue
					}
					manifests[index].ProviderArchiveCommitted = true
					manifests[index].UpdatedAt = i.now().UTC()
					archiveUpdated = true
				}
				if archiveUpdated {
					if persistErr := i.writer.UpsertAcquisitionArtifacts(ctx, manifests); persistErr != nil {
						i.logf("acknowledge provider archive for %s: %v", src.URL, persistErr)
						res.Failed++
						continue
					}
				}
			}
			published, publishErr := i.publish(ctx, manifests, download.Outputs)
			manifests = published
			if publishErr != nil {
				i.logf("publish acquisition outputs for %s: %v", src.URL, publishErr)
				err = errors.Join(err, publishErr)
			}
		}
		res.Artifacts = append(res.Artifacts, manifests...)
		if err != nil {
			i.logf("ingest %s failed: %v", src.URL, err)
			res.Failed++
			continue
		}
		if download.Fetched == 0 && download.Skipped == 0 {
			// A source that yielded no clips AND no error is almost always operator
			// error — a typo'd/nonexistent Archive id (Archive returns 200 {} for an
			// unknown item, so it's not a download failure), an empty collection, or a
			// YouTube source with no downloadable video. Without this the operator sees
			// "fetched:0 failed:0" and no reason why. Warn + count it so it's not silent.
			i.logf("ingest %s yielded no clips (nonexistent id / empty source?)", src.URL)
			res.Empty++
		}
		res.Fetched += download.Fetched
		res.Skipped += download.Skipped
	}
	return res
}

func (i *Ingestor) manifests(src Source, outputDir string, outputs []Output) []filler.AcquisitionArtifact {
	now := i.now().UTC()
	manifests := make([]filler.AcquisitionArtifact, 0, len(outputs))
	for _, output := range outputs {
		outputRelative, outputErr := filepath.Rel(outputDir, output.MediaPath)
		stagingPath, stageErr := filepath.Rel(i.dropDir, output.MediaPath)
		mediaPath := filepath.Base(output.MediaPath)
		if outputErr != nil || !withinDir(outputRelative) || stageErr != nil || !withinDir(stagingPath) {
			output.Repair = "downloader returned a path outside its quarantine"
			stagingPath = filepath.Join(".loomarr-acquisitions", src.AcquisitionID, filepath.Base(output.MediaPath))
		}
		sidecarPath := ""
		if output.SidecarPath != "" {
			expectedSidecar := strings.TrimSuffix(output.MediaPath, filepath.Ext(output.MediaPath)) + ".info.json"
			sidecarRelative, sidecarErr := filepath.Rel(outputDir, output.SidecarPath)
			if sidecarErr != nil || !withinDir(sidecarRelative) || filepath.Clean(output.SidecarPath) != filepath.Clean(expectedSidecar) {
				output.Repair = "downloader returned a sidecar outside its exact media pair"
			} else {
				sidecarPath = strings.TrimSuffix(mediaPath, filepath.Ext(mediaPath)) + ".info.json"
			}
		}
		state := filler.ArtifactStaged
		if src.Kind == YouTube && output.ArchiveEntry == "" && output.Repair == "" {
			output.Repair = "yt-dlp output has no exact provider archive entry"
		}
		if output.Repair != "" {
			state = filler.ArtifactRepair
		}
		identity := sha256.Sum256([]byte(src.AcquisitionID + "\x00" + src.URL + "\x00" + mediaPath + "\x00" + output.SHA256))
		manifests = append(manifests, filler.AcquisitionArtifact{
			ID: hex.EncodeToString(identity[:]), AcquisitionID: src.AcquisitionID, SourceID: src.ID,
			Provider: string(src.Kind), SourceURL: src.URL, StagingPath: stagingPath,
			MediaPath: mediaPath, SidecarPath: sidecarPath, MediaSHA256: output.SHA256,
			MediaBytes: output.Bytes, ClipHash: output.ClipHash, State: state,
			ProviderArchiveEntry: output.ArchiveEntry,
			RepairReason:         output.Repair, CompletedAt: now, UpdatedAt: now,
		})
	}
	return manifests
}

func (i *Ingestor) publish(ctx context.Context, manifests []filler.AcquisitionArtifact, outputs []Output) ([]filler.AcquisitionArtifact, error) {
	updated := append([]filler.AcquisitionArtifact(nil), manifests...)
	for index := range updated {
		if updated[index].State == filler.ArtifactRepair {
			continue
		}
		// Publish portable provenance first. A sidecar without media is inert; media without its
		// sidecar creates a race with intake even though the durable manifest remains authoritative.
		if outputs[index].SidecarPath != "" {
			if err := publishFile(outputs[index].SidecarPath, filepath.Join(i.dropDir, updated[index].SidecarPath)); err != nil {
				updated[index].State = filler.ArtifactRepair
				updated[index].RepairReason = "publish sidecar: " + err.Error()
				updated[index].UpdatedAt = i.now().UTC()
				_ = i.writer.UpsertAcquisitionArtifacts(ctx, updated[index:index+1])
				return updated, err
			}
		}
		target := filepath.Join(i.dropDir, updated[index].MediaPath)
		if err := publishFile(outputs[index].MediaPath, target); err != nil {
			updated[index].State = filler.ArtifactRepair
			updated[index].RepairReason = "publish media: " + err.Error()
			updated[index].UpdatedAt = i.now().UTC()
			_ = i.writer.UpsertAcquisitionArtifacts(ctx, updated[index:index+1])
			return updated, err
		}
		updated[index].State = filler.ArtifactPublished
		updated[index].UpdatedAt = i.now().UTC()
	}
	if err := i.writer.UpsertAcquisitionArtifacts(ctx, updated); err != nil {
		return updated, fmt.Errorf("acknowledge published acquisition outputs: %w", err)
	}
	return updated, nil
}

func publishFile(source, target string) error {
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("target %s already exists", target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(source, target)
}

func (i *Ingestor) downloaderFor(k Kind) Downloader {
	switch k {
	case YouTube:
		return i.youtube
	case Archive:
		return i.archive
	default:
		return nil
	}
}

func (i *Ingestor) logf(format string, args ...any) {
	if i.log != nil {
		i.log.Warn(fmt.Sprintf(format, args...))
	}
}
