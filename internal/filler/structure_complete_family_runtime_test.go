package filler

import (
	"os"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func TestStructureCompleteFamilyRuntimePublishesAndResumesExactCall(t *testing.T) {
	source := structureSource(10_000)
	order := []string{}
	assessor := runtimeAssessorFixtures(source, &order)[0]
	repository := structureEvidenceRepositoryFixture(t)
	runtime, err := NewStructureCompleteFamilyRuntime(assessor, repository)
	if err != nil {
		t.Fatal(err)
	}
	media := structureAssessmentMediaFixture(source, "/tmp/conditioned.mp4")
	first, err := runtime.AssessWithEvidence(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.AssessWithEvidence(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || first.Record.SHA256 != second.Record.SHA256 {
		t.Fatalf("calls=%v first=%q second=%q", order, first.Record.SHA256, second.Record.SHA256)
	}
}

func TestStructureCompleteFamilyRuntimeRejectsDetachedResume(t *testing.T) {
	source := structureSource(10_000)
	order := []string{}
	assessor := runtimeAssessorFixtures(source, &order)[0]
	repository := structureEvidenceRepositoryFixture(t)
	runtime, err := NewStructureCompleteFamilyRuntime(assessor, repository)
	if err != nil {
		t.Fatal(err)
	}
	media := structureAssessmentMediaFixture(source, "/tmp/conditioned.mp4")
	recorded, err := runtime.AssessWithEvidence(t.Context(), media)
	if err != nil {
		t.Fatal(err)
	}
	operation := recorded.Record
	path := repository.blobPath("assessment-publications", fillerstructureAssessmentOperation(operation))
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AssessWithEvidence(t.Context(), media); err == nil || len(order) != 1 {
		t.Fatalf("detached resume error=%v calls=%v", err, order)
	}
}

func fillerstructureAssessmentOperation(record fillerstructure.AssessmentRecord) string {
	return fillerstructure.AssessmentOperationSHA256(record.Source, record.Media, record.Assessor)
}
