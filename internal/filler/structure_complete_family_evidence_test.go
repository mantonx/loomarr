package filler

import (
	"testing"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestStructureCompleteFamilyEvidenceBindsRecordAndPublication(t *testing.T) {
	recorded := runtimeAssessorFixtures(structureSource(10_000), &[]string{})[0].(*capturedStructureAssessor).recorded
	evidence, err := NewStructureCompleteFamilyEvidence(recorded)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Record.SHA256 != recorded.Record.SHA256 || evidence.Publication.RecordSHA256 != evidence.Record.SHA256 {
		t.Fatalf("evidence=%+v", evidence)
	}
	if err := ValidateStructureCompleteFamilyEvidence(evidence); err != nil {
		t.Fatal(err)
	}
	evidence.Publication.RecordSHA256 = evidence.Record.RequestSHA256
	evidence.Publication.SHA256 = fillerstructure.AssessmentPublicationSHA256(evidence.Publication)
	if err := ValidateStructureCompleteFamilyEvidence(evidence); err == nil {
		t.Fatal("complete family evidence accepted a detached publication")
	}
}
