package filler

import (
	"os"
	"reflect"
	"testing"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func TestFileStructureWindowEvidenceRepositoryRoundTripsValidatedArtifacts(t *testing.T) {
	_, prepared := structureWindowRuntimeFixture(t)
	set := prepared.Authority
	timeline := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 120_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 120_000, EndMS: 300_000, Role: fillerstructure.RolePromo},
	}
	profile := windowAssessorFixture("assessor-a", "family-a", "a", timeline, &[]string{}).Profile()
	recorded := structureWindowRecordedAssessmentFixtures(t, set, profile, timeline)
	assessments := recordedWindowAssessments(recorded)
	stitch, err := fillerstructurewindow.Stitch(set, assessments, 2_000)
	if err != nil {
		t.Fatal(err)
	}
	repository := structureEvidenceRepositoryFixture(t)
	for _, assessment := range recorded {
		if err := repository.PutStructureWindowAssessmentEvidence(t.Context(), assessment); err != nil {
			t.Fatal(err)
		}
		if err := repository.PutStructureWindowAssessmentEvidence(t.Context(), assessment); err != nil {
			t.Fatalf("idempotent assessment persistence: %v", err)
		}
		loaded, err := repository.GetStructureWindowAssessmentEvidence(t.Context(), set, assessment.Record.SHA256)
		if err != nil || !reflect.DeepEqual(loaded, assessment) {
			t.Fatalf("loaded assessment=%+v error=%v", loaded, err)
		}
		found, ok, err := repository.FindStructureWindowAssessmentEvidence(t.Context(), set, assessment.Record.WindowOrdinal, assessment.Record.Assessor)
		if err != nil || !ok || !reflect.DeepEqual(found, assessment) {
			t.Fatalf("found assessment=%+v ok=%t error=%v", found, ok, err)
		}
	}
	if _, ok, err := repository.FindStructureWindowAssessmentEvidence(t.Context(), set, 0, windowAssessorFixture("other", "other-family", "c", timeline, &[]string{}).Profile()); err != nil || ok {
		t.Fatalf("missing operation ok=%t error=%v", ok, err)
	}
	if err := repository.PutStructureWindowStitch(t.Context(), stitch); err != nil {
		t.Fatal(err)
	}
	if err := repository.PutStructureWindowStitch(t.Context(), stitch); err != nil {
		t.Fatalf("idempotent stitch persistence: %v", err)
	}
	loaded, err := repository.GetStructureWindowStitch(t.Context(), stitch.SHA256)
	if err != nil || !reflect.DeepEqual(loaded, stitch) {
		t.Fatalf("loaded stitch=%+v error=%v", loaded, err)
	}
}

func TestFileStructureWindowEvidenceRepositoryRejectsDrift(t *testing.T) {
	_, prepared := structureWindowRuntimeFixture(t)
	set := prepared.Authority
	timeline := []fillerstructure.Segment{{StartMS: 0, EndMS: 300_000, Role: fillerstructure.RoleCommercial}}
	profile := windowAssessorFixture("assessor-a", "family-a", "a", timeline, &[]string{}).Profile()
	recorded := structureWindowRecordedAssessmentFixtures(t, set, profile, timeline)
	assessments := recordedWindowAssessments(recorded)
	stitch, err := fillerstructurewindow.Stitch(set, assessments, 2_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"publication", "record", "assessment", "response", "output", "stitch"} {
		t.Run(target, func(t *testing.T) {
			repository := structureEvidenceRepositoryFixture(t)
			var path string
			if target != "stitch" {
				if err := repository.PutStructureWindowAssessmentEvidence(t.Context(), recorded[0]); err != nil {
					t.Fatal(err)
				}
				switch target {
				case "record":
					path = repository.blobPath("window-call-records", recorded[0].Record.SHA256)
				case "publication":
					operation := fillerstructurewindow.CallOperationSHA256(set, recorded[0].Record.WindowOrdinal, recorded[0].Record.Assessor)
					path = repository.blobPath("window-call-publications", operation)
				case "assessment":
					path = repository.blobPath("window-assessments", recorded[0].Assessment.SHA256)
				case "response":
					path = repository.blobPath("responses", recorded[0].Record.ResponseSHA256)
				case "output":
					path = repository.blobPath("outputs", recorded[0].Record.StructuredOutputSHA256)
				}
			} else {
				if err := repository.PutStructureWindowStitch(t.Context(), stitch); err != nil {
					t.Fatal(err)
				}
				path = repository.blobPath("window-stitches", stitch.SHA256)
			}
			if err := os.WriteFile(path, []byte(`{"tampered":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if target != "stitch" {
				var err error
				if target == "publication" {
					_, _, err = repository.FindStructureWindowAssessmentEvidence(t.Context(), set, recorded[0].Record.WindowOrdinal, recorded[0].Record.Assessor)
				} else {
					_, err = repository.GetStructureWindowAssessmentEvidence(t.Context(), set, recorded[0].Record.SHA256)
				}
				if err == nil {
					t.Fatal("tampered window call evidence was accepted")
				}
			} else if _, err := repository.GetStructureWindowStitch(t.Context(), stitch.SHA256); err == nil {
				t.Fatal("tampered stitch was accepted")
			}
		})
	}
}

func structureWindowRecordedAssessmentFixtures(t *testing.T, set fillerstructurewindow.MediaSet, profile fillerstructure.AssessorProfile, timeline []fillerstructure.Segment) []fillerstructurewindow.RecordedAssessment {
	t.Helper()
	recorded := make([]fillerstructurewindow.RecordedAssessment, len(set.Plan.Windows))
	assessor := &capturedWindowAssessor{profile: profile, timeline: timeline, events: &[]string{}, failureOrdinal: -1}
	for ordinal, window := range set.Plan.Windows {
		assessment, err := assessor.AssessWindow(t.Context(), set, StructureAssessmentWindowMedia{
			Window: window, Media: set.Windows[ordinal], FullPath: "unused",
		})
		if err != nil {
			t.Fatal(err)
		}
		recorded[ordinal] = assessment
	}
	return recorded
}

func recordedWindowAssessments(recorded []fillerstructurewindow.RecordedAssessment) []fillerstructurewindow.Assessment {
	assessments := make([]fillerstructurewindow.Assessment, len(recorded))
	for index := range recorded {
		assessments[index] = recorded[index].Assessment
	}
	return assessments
}
