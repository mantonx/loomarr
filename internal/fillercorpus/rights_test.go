package fillercorpus

import (
	"slices"
	"testing"
)

func TestRightsReviewFreezesAcquisitionProvenance(t *testing.T) {
	row := RightsReviewRowFromCase(InventoryCase{
		Campaign:     "campaign-one",
		SourceFamily: "master-one",
		SubjectTerms: []string{"Female Nudes", "Venus"},
	})
	header := RightsReviewCSVHeader()
	record := ImmutableRightsReviewRecord(row)
	for field, want := range map[string]string{"campaign": "campaign-one", "source_family": "master-one", "subject_terms_json": `["Female Nudes","Venus"]`} {
		index := slices.Index(header, field)
		if index < 0 || index >= len(record) {
			t.Fatalf("immutable field %s missing from record", field)
		}
		if record[index] != want {
			t.Fatalf("immutable %s = %q; want %q", field, record[index], want)
		}
	}
}
