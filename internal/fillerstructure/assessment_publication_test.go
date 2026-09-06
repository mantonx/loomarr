package fillerstructure

import "testing"

func TestAssessmentPublicationBindsCompletedOperationToRecord(t *testing.T) {
	input := acceptedAssessmentInput()
	input.PromptSHA256 = DirectVideoPromptSHA256(input.Source.DurationMS)
	input.SchemaSHA256 = DirectVideoSchemaSHA256(input.Source.DurationMS)
	recorded, err := NewAssessmentRecord(input)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := NewAssessmentPublication(recorded.Record)
	if err != nil {
		t.Fatal(err)
	}
	if publication.RecordSHA256 != recorded.Record.SHA256 ||
		publication.OperationSHA256 != AssessmentOperationSHA256(input.Source, input.Media, input.Assessor) ||
		publication.SHA256 != AssessmentPublicationSHA256(publication) {
		t.Fatalf("publication=%+v", publication)
	}
	if err := ValidateAssessmentPublication(publication, recorded.Record); err != nil {
		t.Fatal(err)
	}
}

func TestAssessmentPublicationRejectsRecordOrOperationDrift(t *testing.T) {
	input := acceptedAssessmentInput()
	input.PromptSHA256 = DirectVideoPromptSHA256(input.Source.DurationMS)
	input.SchemaSHA256 = DirectVideoSchemaSHA256(input.Source.DurationMS)
	recorded, err := NewAssessmentRecord(input)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := NewAssessmentPublication(recorded.Record)
	if err != nil {
		t.Fatal(err)
	}
	publication.OperationSHA256 = input.RequestSHA256
	publication.SHA256 = AssessmentPublicationSHA256(publication)
	if err := ValidateAssessmentPublication(publication, recorded.Record); err == nil {
		t.Fatal("publication accepted operation drift")
	}
	publication, err = NewAssessmentPublication(recorded.Record)
	if err != nil {
		t.Fatal(err)
	}
	drifted := recorded.Record
	drifted.RequestSHA256 = input.Source.SHA256
	drifted.SHA256 = AssessmentRecordSHA256(drifted)
	if err := ValidateAssessmentPublication(publication, drifted); err == nil {
		t.Fatal("publication accepted a different record")
	}
}
