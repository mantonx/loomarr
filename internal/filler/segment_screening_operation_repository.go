package filler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"reflect"
)

const (
	segmentScreeningOperationSchemaVersion   = 1
	segmentScreeningOperationContractVersion = "filler-rendered-child-screening-operation-v1"
	segmentScreeningOperationMaxBytes        = 64 << 10
)

// SegmentScreeningAxisEvidenceReplay lets an evaluator replay the one already settled result for
// a subject/profile operation. A fixed operation key cannot silently acquire a second answer.
type SegmentScreeningAxisEvidenceReplay interface {
	FindSegmentScreeningAxisEvidence(context.Context, string, SegmentScreeningAxisProfile) (RecordedSegmentScreeningAxisEvidence, bool, error)
}

type segmentScreeningOperationRecord struct {
	SchemaVersion   int                         `json:"schemaVersion"`
	ContractVersion string                      `json:"contractVersion"`
	SubjectSHA256   string                      `json:"subjectSha256"`
	Profile         SegmentScreeningAxisProfile `json:"profile"`
	EvidenceSHA256  string                      `json:"evidenceSha256"`
	OperationSHA256 string                      `json:"operationSha256"`
}

func newSegmentScreeningOperationRecord(evidence SegmentScreeningAxisEvidence) (segmentScreeningOperationRecord, error) {
	if err := ValidateSegmentScreeningAxisEvidence(evidence); err != nil {
		return segmentScreeningOperationRecord{}, err
	}
	record := segmentScreeningOperationRecord{
		SchemaVersion: segmentScreeningOperationSchemaVersion, ContractVersion: segmentScreeningOperationContractVersion,
		SubjectSHA256: evidence.SubjectSHA256, Profile: evidence.Profile, EvidenceSHA256: evidence.SHA256,
	}
	record.OperationSHA256 = segmentScreeningOperationSHA256(record.SubjectSHA256, record.Profile)
	return record, validateSegmentScreeningOperationRecord(record)
}

func validateSegmentScreeningOperationRecord(record segmentScreeningOperationRecord) error {
	if record.SchemaVersion != segmentScreeningOperationSchemaVersion || record.ContractVersion != segmentScreeningOperationContractVersion ||
		!isContentHash(record.SubjectSHA256) || ValidateSegmentScreeningAxisProfile(record.Profile) != nil || !isContentHash(record.EvidenceSHA256) ||
		record.OperationSHA256 != segmentScreeningOperationSHA256(record.SubjectSHA256, record.Profile) {
		return fmt.Errorf("segment screening operation record is invalid")
	}
	return nil
}

func segmentScreeningOperationSHA256(subjectSHA256 string, profile SegmentScreeningAxisProfile) string {
	projection := struct {
		SchemaVersion   int                         `json:"schemaVersion"`
		ContractVersion string                      `json:"contractVersion"`
		SubjectSHA256   string                      `json:"subjectSha256"`
		Profile         SegmentScreeningAxisProfile `json:"profile"`
	}{
		SchemaVersion: segmentScreeningOperationSchemaVersion, ContractVersion: segmentScreeningOperationContractVersion,
		SubjectSHA256: subjectSHA256, Profile: profile,
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		return ""
	}
	return screeningBytesSHA256(raw)
}

func (r *FileSegmentScreeningEvidenceRepository) putSegmentScreeningOperation(ctx context.Context, evidence SegmentScreeningAxisEvidence) error {
	record, err := newSegmentScreeningOperationRecord(evidence)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > segmentScreeningOperationMaxBytes {
		return fmt.Errorf("marshal segment screening operation")
	}
	if err := r.files.putImmutable(ctx, r.operationPath(record.OperationSHA256), raw, segmentScreeningOperationMaxBytes); err != nil {
		return fmt.Errorf("persist segment screening operation: %w", err)
	}
	return nil
}

func (r *FileSegmentScreeningEvidenceRepository) FindSegmentScreeningAxisEvidence(ctx context.Context, subjectSHA256 string, profile SegmentScreeningAxisProfile) (RecordedSegmentScreeningAxisEvidence, bool, error) {
	if r == nil || r.files == nil || !isContentHash(subjectSHA256) || ValidateSegmentScreeningAxisProfile(profile) != nil {
		return RecordedSegmentScreeningAxisEvidence{}, false, fmt.Errorf("segment screening operation identity is invalid")
	}
	operationSHA256 := segmentScreeningOperationSHA256(subjectSHA256, profile)
	raw, err := r.files.readImmutable(ctx, r.operationPath(operationSHA256), segmentScreeningOperationMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return RecordedSegmentScreeningAxisEvidence{}, false, nil
	}
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, false, fmt.Errorf("read segment screening operation: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var operation segmentScreeningOperationRecord
	if err := decoder.Decode(&operation); err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, false, fmt.Errorf("decode segment screening operation: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return RecordedSegmentScreeningAxisEvidence{}, false, fmt.Errorf("decode segment screening operation: trailing JSON")
	}
	if err := validateSegmentScreeningOperationRecord(operation); err != nil || operation.OperationSHA256 != operationSHA256 ||
		operation.SubjectSHA256 != subjectSHA256 || !reflect.DeepEqual(operation.Profile, profile) {
		return RecordedSegmentScreeningAxisEvidence{}, false, fmt.Errorf("segment screening operation does not match its request")
	}
	recorded, err := r.GetSegmentScreeningAxisEvidence(ctx, operation.EvidenceSHA256)
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, false, fmt.Errorf("replay segment screening operation evidence: %w", err)
	}
	if recorded.Evidence.SubjectSHA256 != subjectSHA256 || !reflect.DeepEqual(recorded.Evidence.Profile, profile) {
		return RecordedSegmentScreeningAxisEvidence{}, false, fmt.Errorf("segment screening operation evidence drifted")
	}
	return recorded, true, nil
}

func (r *FileSegmentScreeningEvidenceRepository) operationPath(digest string) string {
	return filepath.Join(r.files.root, "screening-axis-operations", digest[:2], digest+".json")
}

var _ SegmentScreeningAxisEvidenceReplay = (*FileSegmentScreeningEvidenceRepository)(nil)
