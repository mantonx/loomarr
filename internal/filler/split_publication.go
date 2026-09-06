package filler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/loomarr/loomarr/internal/mediatools"
)

// preparedSplitCut is one fully staged child plus its final content-addressed location.
// Split confirmation owns domain enrollment; splitPublication owns only filesystem visibility.
type preparedSplitCut struct {
	segment  SplitSegment
	kind     Kind
	hash     string
	path     string
	staged   string
	final    string
	existing bool
}

// splitPublication keeps a reviewed generation reversible until catalog selection commits.
// Its small interface hides the ordering-sensitive sidecar/media link and rollback details.
type splitPublication struct {
	cuts     []preparedSplitCut
	token    string
	retained bool
}

func snapshotSplitComposite(ctx context.Context, source, stageDir, extension, expectedHash string) (string, error) {
	snapshot := filepath.Join(stageDir, "composite"+extension)
	if err := snapshotOwnedFile(ctx, source, snapshot); err != nil {
		return "", fmt.Errorf("split confirm: snapshot composite: %w", err)
	}
	snapshotHash, err := ClipID(snapshot)
	if err != nil {
		return "", fmt.Errorf("split confirm: identify composite snapshot: %w", err)
	}
	if snapshotHash != expectedHash {
		return "", fmt.Errorf("split confirm: composite bytes do not match catalog identity %s", expectedHash)
	}
	if err := validateSplitCompositeOwnership(ctx, source, snapshot); err != nil {
		return "", err
	}
	return snapshot, nil
}

func validateSplitCompositeOwnership(ctx context.Context, source, snapshot string) error {
	equal, err := exactFileBytesEqual(ctx, source, snapshot, mediatools.ConditioningMaxSnapshotBytes)
	if err != nil {
		return fmt.Errorf("split confirm: compare composite snapshot: %w", err)
	}
	if !equal {
		return fmt.Errorf("split confirm: composite snapshot is not exact")
	}
	return nil
}

func (p *splitPublication) prepare(ctx context.Context) error {
	for i := range p.cuts {
		cut := &p.cuts[i]
		if cut.existing {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := prepareSplitSidecar(cut.staged, cut.final); err != nil {
			return fmt.Errorf("split confirm: prepare segment %d sidecar: %w", i, err)
		}
	}
	return nil
}

// prepareSplitSidecar lets a recovered durable claim replace an orphan sidecar while the media
// name is still absent. Once media exists, confirm transfers ownership only after exact byte and
// lineage validation.
func prepareSplitSidecar(stagedMedia, finalMedia string) error {
	stagedSidecar, finalSidecar := sidecarPathFor(stagedMedia), sidecarPathFor(finalMedia)
	if err := os.Link(stagedSidecar, finalSidecar); err == nil {
		return nil
	} else if !os.IsExist(err) {
		return err
	}
	if _, err := os.Stat(finalMedia); err == nil || !os.IsNotExist(err) {
		return fmt.Errorf("final sidecar already belongs to published or unreadable media")
	}
	info, err := os.Lstat(finalSidecar)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("orphan final sidecar is not a regular file")
	}
	if err := os.Remove(finalSidecar); err != nil {
		return err
	}
	return os.Link(stagedSidecar, finalSidecar)
}

func (p *splitPublication) publish(ctx context.Context) error {
	for i := range p.cuts {
		cut := &p.cuts[i]
		if cut.existing {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := publishPreparedMedia(cut.staged, cut.final); err != nil {
			return fmt.Errorf("split confirm: publish segment %d: %w", i, err)
		}
	}
	return nil
}

func (p *splitPublication) retain() { p.retained = true }

func (p *splitPublication) rollback() {
	if p.retained {
		return
	}
	for _, cut := range p.cuts {
		if cut.existing {
			continue
		}
		tags, state := ReadSidecarTagsState(cut.final)
		if state != SidecarValid || tags.SplitPublicationToken != p.token {
			continue
		}
		if err := os.Remove(cut.final); err != nil && !os.IsNotExist(err) {
			continue
		}
		_ = os.Remove(sidecarPathFor(cut.final))
	}
}
