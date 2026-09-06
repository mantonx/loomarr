package filler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const (
	SegmentScreeningAxisEvidenceSchemaVersion   = 2
	SegmentScreeningAxisEvidenceContractVersion = "filler-rendered-child-screening-axis-evidence-v2"
)

// SegmentScreeningAxisProfile is the immutable evaluator identity locked by a release authority.
// The certificate owns model/route details when an axis uses inference; deterministic axes bind
// their measurement certification through the same small interface.
type SegmentScreeningAxisProfile struct {
	Axis                 SegmentScreeningAxis `json:"axis"`
	EvidenceContract     string               `json:"evidenceContract"`
	PolicySHA256         string               `json:"policySha256"`
	CertificationSHA256  string               `json:"certificationSha256"`
	ImplementationSHA256 string               `json:"implementationSha256"`
}

// SegmentScreeningAxisEvidence is the provider-neutral closed result for one axis and rendered child.
// RawEvidenceSHA256 points at private bounded bytes stored before this record is published.
type SegmentScreeningAxisEvidence struct {
	SchemaVersion     int                         `json:"schemaVersion"`
	ContractVersion   string                      `json:"contractVersion"`
	SubjectSHA256     string                      `json:"subjectSha256"`
	Profile           SegmentScreeningAxisProfile `json:"profile"`
	Outcome           SegmentScreeningOutcome     `json:"outcome"`
	ReasonCode        string                      `json:"reasonCode"`
	RawEvidenceSHA256 string                      `json:"rawEvidenceSha256"`
	AssessedAt        time.Time                   `json:"assessedAt"`
	SHA256            string                      `json:"sha256"`
}

type RecordedSegmentScreeningAxisEvidence struct {
	Evidence    SegmentScreeningAxisEvidence
	RawEvidence []byte
}

func NewSegmentScreeningAxisEvidence(subject SegmentScreeningSubject, profile SegmentScreeningAxisProfile, outcome SegmentScreeningOutcome, reasonCode string, rawEvidence []byte, assessedAt time.Time) (RecordedSegmentScreeningAxisEvidence, error) {
	if err := ValidateSegmentScreeningSubject(subject); err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, err
	}
	record := RecordedSegmentScreeningAxisEvidence{
		Evidence: SegmentScreeningAxisEvidence{
			SchemaVersion: SegmentScreeningAxisEvidenceSchemaVersion, ContractVersion: SegmentScreeningAxisEvidenceContractVersion,
			SubjectSHA256: subject.SHA256, Profile: profile,
			Outcome: outcome, ReasonCode: reasonCode, RawEvidenceSHA256: screeningBytesSHA256(rawEvidence), AssessedAt: assessedAt.UTC(),
		},
		RawEvidence: append([]byte(nil), rawEvidence...),
	}
	record.Evidence.SHA256 = SegmentScreeningAxisEvidenceSHA256(record.Evidence)
	if err := ValidateRecordedSegmentScreeningAxisEvidence(record); err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, err
	}
	return record, nil
}

func ValidateRecordedSegmentScreeningAxisEvidence(record RecordedSegmentScreeningAxisEvidence) error {
	if err := ValidateSegmentScreeningAxisEvidence(record.Evidence); err != nil {
		return err
	}
	if len(record.RawEvidence) == 0 || screeningBytesSHA256(record.RawEvidence) != record.Evidence.RawEvidenceSHA256 {
		return fmt.Errorf("segment screening axis raw evidence is missing or drifted")
	}
	return nil
}

func ValidateSegmentScreeningAxisEvidence(evidence SegmentScreeningAxisEvidence) error {
	projected := SegmentScreeningResult{
		Axis: evidence.Profile.Axis, Outcome: evidence.Outcome,
		AuthoritySHA256: evidence.SHA256, ReasonCode: evidence.ReasonCode,
	}
	if evidence.SchemaVersion != SegmentScreeningAxisEvidenceSchemaVersion ||
		evidence.ContractVersion != SegmentScreeningAxisEvidenceContractVersion ||
		!isContentHash(evidence.SubjectSHA256) || ValidateSegmentScreeningAxisProfile(evidence.Profile) != nil ||
		validateSegmentScreeningResult(projected) != nil || !isContentHash(evidence.RawEvidenceSHA256) ||
		evidence.AssessedAt.IsZero() || evidence.SHA256 != SegmentScreeningAxisEvidenceSHA256(evidence) {
		return fmt.Errorf("segment screening axis evidence is invalid")
	}
	return nil
}

func ValidateSegmentScreeningAxisProfile(profile SegmentScreeningAxisProfile) error {
	if validateSegmentScreeningAxis(profile.Axis) != nil || !validScreeningReasonCode(profile.EvidenceContract) ||
		!isContentHash(profile.PolicySHA256) || !isContentHash(profile.CertificationSHA256) || !isContentHash(profile.ImplementationSHA256) {
		return fmt.Errorf("segment screening axis profile is invalid")
	}
	return nil
}

func (e SegmentScreeningAxisEvidence) Result() SegmentScreeningResult {
	return SegmentScreeningResult{Axis: e.Profile.Axis, Outcome: e.Outcome, AuthoritySHA256: e.SHA256, ReasonCode: e.ReasonCode}
}

func SegmentScreeningAxisEvidenceSHA256(evidence SegmentScreeningAxisEvidence) string {
	evidence.SHA256 = ""
	raw, err := json.Marshal(evidence)
	if err != nil {
		return ""
	}
	return screeningBytesSHA256(raw)
}

func screeningBytesSHA256(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
