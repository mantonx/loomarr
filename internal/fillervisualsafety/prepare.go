package fillervisualsafety

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/loomarr/loomarr/internal/mediatools"
)

// SourceRequest joins path-free authority to its private machine-local locator.
type SourceRequest struct {
	Authority SourceAuthority
	Path      string `json:"-"`
}

// PreparedSource owns an immutable private snapshot and its deterministic
// complete-duration coverage plan. Adapters receive SnapshotPath, never the
// mutable caller path.
type PreparedSource struct {
	Authority    SourceAuthority
	Plan         CoveragePlan
	SnapshotPath string
	snapshot     *mediatools.FileSnapshot
}

// Prepare verifies and snapshots the exact source before any extraction or inference adapter runs.
func Prepare(ctx context.Context, request SourceRequest, profile CoverageProfile) (*PreparedSource, error) {
	if ctx == nil || ctx.Err() != nil || ValidateSourceAuthority(request.Authority) != nil ||
		ValidateCoverageProfile(profile) != nil {
		return nil, errors.New("visual-safety source preparation input is invalid")
	}
	clean := filepath.Clean(request.Path)
	if strings.TrimSpace(request.Path) == "" || !filepath.IsAbs(clean) || clean != request.Path {
		return nil, errors.New("visual-safety source locator is invalid")
	}
	snapshot, err := mediatools.SnapshotRegularFile(ctx, request.Path)
	if err != nil {
		return nil, errors.New("visual-safety source could not be snapshotted")
	}
	if snapshot.SHA256() != request.Authority.SourceSHA256 || snapshot.Bytes() != request.Authority.SourceBytes {
		_ = snapshot.Close()
		return nil, errors.New("visual-safety source bytes drifted")
	}
	plan, err := PlanCoverage(request.Authority, profile)
	if err != nil {
		_ = snapshot.Close()
		return nil, err
	}
	return &PreparedSource{
		Authority: request.Authority, Plan: plan, SnapshotPath: snapshot.Path(), snapshot: snapshot,
	}, nil
}

// Close removes the private snapshot. It is safe to call repeatedly.
func (source *PreparedSource) Close() error {
	if source == nil || source.snapshot == nil {
		return nil
	}
	err := source.snapshot.Close()
	source.snapshot = nil
	return err
}
