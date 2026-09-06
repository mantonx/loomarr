package filler

import (
	"errors"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

// StructureCompleteFamilyEvidence is the portable path-free proof for one complete-video family
// call. Provider response bytes remain in the private repository and are bound by the call record.
type StructureCompleteFamilyEvidence struct {
	Record      fillerstructure.AssessmentRecord      `json:"record"`
	Publication fillerstructure.AssessmentPublication `json:"publication"`
}

func NewStructureCompleteFamilyEvidence(recorded fillerstructure.RecordedAssessment) (StructureCompleteFamilyEvidence, error) {
	if err := fillerstructure.ValidateRecordedAssessment(recorded); err != nil {
		return StructureCompleteFamilyEvidence{}, err
	}
	publication, err := fillerstructure.NewAssessmentPublication(recorded.Record)
	if err != nil {
		return StructureCompleteFamilyEvidence{}, err
	}
	evidence := StructureCompleteFamilyEvidence{Record: recorded.Record, Publication: publication}
	return evidence, ValidateStructureCompleteFamilyEvidence(evidence)
}

func ValidateStructureCompleteFamilyEvidence(evidence StructureCompleteFamilyEvidence) error {
	if err := fillerstructure.ValidateAssessmentRecord(evidence.Record); err != nil {
		return err
	}
	if err := fillerstructure.ValidateAssessmentPublication(evidence.Publication, evidence.Record); err != nil {
		return err
	}
	if evidence.Publication.RecordSHA256 != evidence.Record.SHA256 {
		return errors.New("structure complete family evidence is detached")
	}
	return nil
}
