package filler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

const segmentScreeningEvidenceMaxBytes = 64 << 10

// FileSegmentScreeningEvidenceRepository stores rendered-child aggregates separately from
// complete-timeline assessment records while reusing the same private immutable-file discipline.
type FileSegmentScreeningEvidenceRepository struct {
	files *FileStructureAssessmentEvidenceRepository
}

func NewFileSegmentScreeningEvidenceRepository(root string) (*FileSegmentScreeningEvidenceRepository, error) {
	files, err := NewFileStructureAssessmentEvidenceRepository(root)
	if err != nil {
		return nil, err
	}
	return &FileSegmentScreeningEvidenceRepository{files: files}, nil
}

func (r *FileSegmentScreeningEvidenceRepository) PutSegmentScreeningEvidence(ctx context.Context, evidence SegmentScreeningEvidence) error {
	if r == nil || r.files == nil {
		return fmt.Errorf("segment screening evidence repository is unavailable")
	}
	if err := ValidateSegmentScreeningEvidence(evidence); err != nil {
		return err
	}
	raw, err := json.Marshal(evidence)
	if err != nil || len(raw) > segmentScreeningEvidenceMaxBytes {
		return fmt.Errorf("marshal segment screening evidence")
	}
	if err := r.files.putImmutable(ctx, r.path(evidence.SHA256), raw, segmentScreeningEvidenceMaxBytes); err != nil {
		return fmt.Errorf("persist segment screening evidence: %w", err)
	}
	return nil
}

func (r *FileSegmentScreeningEvidenceRepository) GetSegmentScreeningEvidence(ctx context.Context, evidenceSHA256 string) (SegmentScreeningEvidence, error) {
	if r == nil || r.files == nil || !structureEvidenceDigest(evidenceSHA256) {
		return SegmentScreeningEvidence{}, fmt.Errorf("segment screening evidence identity is invalid")
	}
	raw, err := r.files.readImmutable(ctx, r.path(evidenceSHA256), segmentScreeningEvidenceMaxBytes)
	if err != nil {
		return SegmentScreeningEvidence{}, fmt.Errorf("read segment screening evidence: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var evidence SegmentScreeningEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return SegmentScreeningEvidence{}, fmt.Errorf("decode segment screening evidence: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return SegmentScreeningEvidence{}, fmt.Errorf("decode segment screening evidence: trailing JSON")
	}
	if evidence.SHA256 != evidenceSHA256 {
		return SegmentScreeningEvidence{}, fmt.Errorf("segment screening path does not match its identity")
	}
	if err := ValidateSegmentScreeningEvidence(evidence); err != nil {
		return SegmentScreeningEvidence{}, err
	}
	return evidence, nil
}

func (r *FileSegmentScreeningEvidenceRepository) path(digest string) string {
	return filepath.Join(r.files.root, "screenings", digest[:2], digest+".json")
}

var _ SegmentScreeningEvidenceRepository = (*FileSegmentScreeningEvidenceRepository)(nil)
