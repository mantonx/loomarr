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
	State         AcquisitionArtifactState
	RepairReason  string
	CompletedAt   time.Time
	UpdatedAt     time.Time
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

func manifestRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
