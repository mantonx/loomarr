package filler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSegmentScreeningReferenceRoundTripsBesideRenderedChild(t *testing.T) {
	subject := screeningChildSubjectFixture(t)
	aggregate := screeningAggregateFixture(t, subject)
	reference, err := NewSegmentScreeningReference(subject, aggregate)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	media := filepath.Join(dir, "child.mp4")
	if err := os.WriteFile(media, []byte("rendered child"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteSidecarTags(media, SidecarTags{SegmentScreening: &reference}, false); err != nil {
		t.Fatal(err)
	}
	tags, state := ReadSidecarTagsState(media)
	if state != SidecarValid || tags.SegmentScreening == nil || *tags.SegmentScreening != reference {
		t.Fatalf("screening reference did not round-trip: state=%v tags=%+v", state, tags)
	}
}

func TestSegmentScreeningReferenceRejectsSubjectDrift(t *testing.T) {
	subject := screeningChildSubjectFixture(t)
	aggregate := screeningAggregateFixture(t, subject)
	other := subject
	other.CatalogHash = strings.Repeat("f", 64)
	other.SHA256 = SegmentScreeningSubjectSHA256(other)
	if _, err := NewSegmentScreeningReference(other, aggregate); err == nil {
		t.Fatal("reference joined an aggregate to a different subject")
	}
}

func TestSidecarRejectsMalformedSegmentScreeningReference(t *testing.T) {
	for _, raw := range []string{
		`{"loomarr":{"segmentScreening":null}}`,
		`{"loomarr":{"segmentScreening":{}}}`,
		`{"loomarr":{"segmentScreening":{"schemaVersion":1,"subjectSha256":"bad","evidenceSha256":"bad"}}}`,
	} {
		if _, state, _ := decodeSidecarTags([]byte(raw)); state != SidecarInvalid {
			t.Fatalf("malformed reference state=%v, want invalid: %s", state, raw)
		}
	}
}

func TestSidecarRequiresStructureDecisionAndRoleTogether(t *testing.T) {
	lineage := `"childHash":"` + strings.Repeat("1", 64) + `","parentHash":"` + strings.Repeat("2", 64) + `","intendedStartMs":0,"intendedEndMs":30000,`
	for _, raw := range []string{
		`{"loomarr":{"conditioningLineage":{` + lineage + `"structureDecisionSha256":"` + strings.Repeat("3", 64) + `"}}}`,
		`{"loomarr":{"conditioningLineage":{` + lineage + `"structureRole":"commercial"}}}`,
	} {
		if _, state, _ := decodeSidecarTags([]byte(raw)); state != SidecarInvalid {
			t.Fatalf("unpaired structure authority state=%v, want invalid: %s", state, raw)
		}
	}
}

func screeningAggregateFixture(t *testing.T, subject SegmentScreeningSubject) SegmentScreeningEvidence {
	t.Helper()
	records := passingAxisEvidence(t, subject)
	results := make([]SegmentScreeningResult, 0, len(records))
	for _, recorded := range records {
		results = append(results, recorded.Evidence.Result())
	}
	aggregate, err := NewSegmentScreeningEvidence(subject, results, time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return aggregate
}
