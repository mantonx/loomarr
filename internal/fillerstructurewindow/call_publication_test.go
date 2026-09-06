package fillerstructurewindow

import (
	"strings"
	"testing"
)

func TestCallPublicationBindsOneCompletedOperationToOneRecord(t *testing.T) {
	recorded, err := NewRecordedAssessment(acceptedCallRecordInput(callRecordMediaSetFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	publication, err := NewCallPublication(recorded.Record)
	if err != nil {
		t.Fatal(err)
	}
	if publication.OperationSHA256 != CallOperationSHA256(recorded.Record.MediaSet, recorded.Record.WindowOrdinal, recorded.Record.Assessor) ||
		publication.RecordSHA256 != recorded.Record.SHA256 || publication.SHA256 != CallPublicationSHA256(publication) {
		t.Fatalf("publication=%+v", publication)
	}

	drifted := publication
	drifted.RecordSHA256 = strings.Repeat("f", 64)
	drifted.SHA256 = CallPublicationSHA256(drifted)
	if err := ValidateCallPublication(drifted, recorded.Record); err == nil {
		t.Fatal("publication for another call record was accepted")
	}
}
