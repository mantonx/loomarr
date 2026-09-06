package filler

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// AcquisitionArtifactState is the durable publication lifecycle of one exact downloaded file.
// It is deliberately independent of AcquisitionStatus: a run may terminate with a repairable
// artifact still quarantined, and a published artifact remains authoritative after the run ends.
type AcquisitionArtifactState string

const (
	ArtifactStaged    AcquisitionArtifactState = "staged"
	ArtifactPublished AcquisitionArtifactState = "published"
	ArtifactConsumed  AcquisitionArtifactState = "consumed"
	ArtifactRepair    AcquisitionArtifactState = "repair"
)

// AcquisitionArtifact is the canonical manifest value connecting downloaded bytes to intake.
// Paths are relative to the configured watch folder; absolute and escaping paths are invalid.
// MediaSHA256 is a full-file digest, distinct from the sparse ClipHash used for catalog identity.
type AcquisitionArtifact struct {
	ID            string
	AcquisitionID string
	SourceID      string
	Provider      string
	SourceURL     string
	StagingPath   string
	MediaPath     string
	SidecarPath   string
	MediaSHA256   string
	MediaBytes    int64
	ClipHash      string
	// ProviderArchiveEntry is the exact downloader-emitted deduplication line for this output.
	// ProviderArchiveCommitted becomes true only after that line is durable in the shared archive.
	ProviderArchiveEntry     string
	ProviderArchiveCommitted bool
	State                    AcquisitionArtifactState
	RepairReason             string
	CompletedAt              time.Time
	UpdatedAt                time.Time
}

// AcquisitionArtifactCursor is the stable ordering boundary for bounded recovery scans.
// It deliberately uses the immutable scan key captured before recovery mutates a manifest.
type AcquisitionArtifactCursor struct {
	UpdatedAt time.Time
	ID        string
}

// Validate rejects manifests that could widen filesystem authority or fail to identify exact
// bytes. State-specific filesystem checks remain at publication/intake, where the files exist.
func (a AcquisitionArtifact) Validate() error {
	if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(a.AcquisitionID) == "" {
		return errors.New("acquisition artifact requires id and acquisition id")
	}
	if strings.TrimSpace(a.Provider) == "" || strings.TrimSpace(a.SourceURL) == "" {
		return errors.New("acquisition artifact requires provider and source URL")
	}
	if !manifestRelativePath(a.MediaPath) {
		return fmt.Errorf("acquisition artifact media path %q is not watch-relative", a.MediaPath)
	}
	if a.StagingPath != "" && !manifestRelativePath(a.StagingPath) {
		return fmt.Errorf("acquisition artifact staging path %q is not watch-relative", a.StagingPath)
	}
	if a.SidecarPath != "" && !manifestRelativePath(a.SidecarPath) {
		return fmt.Errorf("acquisition artifact sidecar path %q is not watch-relative", a.SidecarPath)
	}
	if len(a.MediaSHA256) != 64 || strings.Trim(a.MediaSHA256, "0123456789abcdef") != "" {
		return errors.New("acquisition artifact requires a lowercase full SHA-256 digest")
	}
	if a.MediaBytes <= 0 {
		return errors.New("acquisition artifact requires a positive media byte length")
	}
	if a.ProviderArchiveEntry != "" {
		if err := ValidateProviderArchiveEntry(a.ProviderArchiveEntry); err != nil {
			return fmt.Errorf("acquisition artifact provider archive entry: %w", err)
		}
	} else if a.ProviderArchiveCommitted {
		return errors.New("acquisition artifact cannot commit an empty provider archive entry")
	}
	switch a.State {
	case ArtifactStaged, ArtifactPublished, ArtifactConsumed, ArtifactRepair:
	default:
		return fmt.Errorf("unknown acquisition artifact state %q", a.State)
	}
	if a.State == ArtifactRepair && strings.TrimSpace(a.RepairReason) == "" {
		return errors.New("repair acquisition artifact requires a reason")
	}
	if a.CompletedAt.IsZero() || a.UpdatedAt.IsZero() {
		return errors.New("acquisition artifact requires completion and update times")
	}
	return nil
}

// ValidateProviderArchiveEntry accepts exactly one canonical extractor-and-ID archive line.
// Newlines, extra fields, and non-canonical spacing could otherwise smuggle additional identities.
func ValidateProviderArchiveEntry(entry string) error {
	if len(entry) > 4096 || strings.ContainsAny(entry, "\r\n") {
		return errors.New("provider archive entry must be one bounded line")
	}
	fields := strings.Fields(entry)
	if len(fields) != 2 || entry != fields[0]+" "+fields[1] {
		return errors.New("provider archive entry must contain one extractor and one identity")
	}
	return nil
}

func manifestRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
