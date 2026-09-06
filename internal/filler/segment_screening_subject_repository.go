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

const segmentScreeningSubjectMaxBytes = 64 << 10

func (r *FileSegmentScreeningEvidenceRepository) PutSegmentScreeningSubject(ctx context.Context, subject SegmentScreeningSubject) error {
	if r == nil || r.files == nil {
		return fmt.Errorf("segment screening evidence repository is unavailable")
	}
	if err := ValidateSegmentScreeningSubject(subject); err != nil {
		return err
	}
	raw, err := json.Marshal(subject)
	if err != nil || len(raw) > segmentScreeningSubjectMaxBytes {
		return fmt.Errorf("marshal segment screening subject")
	}
	if err := r.files.putImmutable(ctx, r.subjectPath(subject.SHA256), raw, segmentScreeningSubjectMaxBytes); err != nil {
		return fmt.Errorf("persist segment screening subject: %w", err)
	}
	return nil
}

func (r *FileSegmentScreeningEvidenceRepository) GetSegmentScreeningSubject(ctx context.Context, subjectSHA256 string) (SegmentScreeningSubject, error) {
	if r == nil || r.files == nil || !structureEvidenceDigest(subjectSHA256) {
		return SegmentScreeningSubject{}, fmt.Errorf("segment screening subject identity is invalid")
	}
	raw, err := r.files.readImmutable(ctx, r.subjectPath(subjectSHA256), segmentScreeningSubjectMaxBytes)
	if err != nil {
		return SegmentScreeningSubject{}, fmt.Errorf("read segment screening subject: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var subject SegmentScreeningSubject
	if err := decoder.Decode(&subject); err != nil {
		return SegmentScreeningSubject{}, fmt.Errorf("decode segment screening subject: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return SegmentScreeningSubject{}, fmt.Errorf("decode segment screening subject: trailing JSON")
	}
	if subject.SHA256 != subjectSHA256 {
		return SegmentScreeningSubject{}, fmt.Errorf("segment screening subject path does not match its identity")
	}
	if err := ValidateSegmentScreeningSubject(subject); err != nil {
		return SegmentScreeningSubject{}, err
	}
	return subject, nil
}

func (r *FileSegmentScreeningEvidenceRepository) subjectPath(digest string) string {
	return filepath.Join(r.files.root, "screening-subjects", digest[:2], digest+".json")
}
