package filler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

const (
	StructureWindowFamilyEvidenceSchemaVersion   = 1
	StructureWindowFamilyEvidenceContractVersion = "filler-structure-window-family-evidence-v1"
)

// StructureWindowFamilyEvidence is the path-free proof behind one family stitch. Call records
// retain exact provider, response, token, charge, and semantic-assessment identities; publications
// prove the completed-operation index that made each call resumable.
type StructureWindowFamilyEvidence struct {
	SchemaVersion   int                                     `json:"schemaVersion"`
	ContractVersion string                                  `json:"contractVersion"`
	CallRecords     []fillerstructurewindow.CallRecord      `json:"callRecords"`
	Publications    []fillerstructurewindow.CallPublication `json:"publications"`
	Stitch          fillerstructurewindow.StitchResult      `json:"stitch"`
	SHA256          string                                  `json:"sha256"`
}

func NewStructureWindowFamilyEvidence(recorded []fillerstructurewindow.RecordedAssessment, stitch fillerstructurewindow.StitchResult) (StructureWindowFamilyEvidence, error) {
	evidence := StructureWindowFamilyEvidence{
		SchemaVersion: StructureWindowFamilyEvidenceSchemaVersion, ContractVersion: StructureWindowFamilyEvidenceContractVersion,
		CallRecords:  make([]fillerstructurewindow.CallRecord, 0, len(recorded)),
		Publications: make([]fillerstructurewindow.CallPublication, 0, len(recorded)), Stitch: stitch,
	}
	for index, item := range recorded {
		if err := fillerstructurewindow.ValidateRecordedAssessment(item); err != nil {
			return StructureWindowFamilyEvidence{}, fmt.Errorf("structure window family recorded assessment %d: %w", index, err)
		}
		publication, err := fillerstructurewindow.NewCallPublication(item.Record)
		if err != nil {
			return StructureWindowFamilyEvidence{}, fmt.Errorf("structure window family publication %d: %w", index, err)
		}
		evidence.CallRecords = append(evidence.CallRecords, item.Record)
		evidence.Publications = append(evidence.Publications, publication)
	}
	evidence.SHA256 = StructureWindowFamilyEvidenceSHA256(evidence)
	return evidence, ValidateStructureWindowFamilyEvidence(evidence)
}

func StructureWindowFamilyEvidenceSHA256(evidence StructureWindowFamilyEvidence) string {
	evidence.SHA256 = ""
	raw, err := json.Marshal(evidence)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func ValidateStructureWindowFamilyEvidence(evidence StructureWindowFamilyEvidence) error {
	if evidence.SchemaVersion != StructureWindowFamilyEvidenceSchemaVersion ||
		evidence.ContractVersion != StructureWindowFamilyEvidenceContractVersion ||
		fillerstructurewindow.ValidateStitchResult(evidence.Stitch) != nil ||
		len(evidence.CallRecords) != len(evidence.Stitch.Assessments) ||
		len(evidence.Publications) != len(evidence.CallRecords) ||
		len(evidence.CallRecords) != len(evidence.Stitch.MediaSet.Plan.Windows) ||
		len(evidence.SHA256) != 64 || evidence.SHA256 != StructureWindowFamilyEvidenceSHA256(evidence) {
		return errors.New("structure window family evidence identity or coverage is invalid")
	}
	seenRequests := make(map[string]struct{}, len(evidence.CallRecords))
	seenOperations := make(map[string]struct{}, len(evidence.CallRecords))
	for ordinal, record := range evidence.CallRecords {
		publication := evidence.Publications[ordinal]
		assessment := evidence.Stitch.Assessments[ordinal]
		if fillerstructurewindow.ValidateCallRecord(record) != nil ||
			!reflect.DeepEqual(record.MediaSet, evidence.Stitch.MediaSet) || record.WindowOrdinal != ordinal ||
			record.Assessor != evidence.Stitch.Assessor || record.AssessmentSHA256 != assessment.SHA256 ||
			fillerstructurewindow.ValidateCallPublication(publication, record) != nil {
			return fmt.Errorf("structure window family call evidence %d drifted", ordinal)
		}
		if _, duplicate := seenRequests[record.RequestSHA256]; duplicate {
			return errors.New("structure window family evidence repeats a request")
		}
		if _, duplicate := seenOperations[publication.OperationSHA256]; duplicate {
			return errors.New("structure window family evidence repeats an operation")
		}
		seenRequests[record.RequestSHA256] = struct{}{}
		seenOperations[publication.OperationSHA256] = struct{}{}
	}
	return nil
}
