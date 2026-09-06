package fillercorpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMetRightsBatchReviewExpandsOneAttestationIntoItemBoundRows(t *testing.T) {
	fixture := newMetRightsBatchFixture(t)
	template, err := PrepareMetRightsBatchAttestation(fixture.inventoryRaw, fixture.worksheetRaw, fixture.prescreenRaw)
	if err != nil {
		t.Fatal(err)
	}
	if template.SchemaVersion != MetRightsBatchAttestationSchemaVersion || template.InventorySHA256 != digestBytes(fixture.inventoryRaw) ||
		template.WorksheetSHA256 != digestBytes(fixture.worksheetRaw) || template.PrescreenSHA256 != digestBytes(fixture.prescreenRaw) ||
		template.Acceptance != MetRightsBatchAcceptancePending || template.ReviewerID != "" || template.ReviewedAt != "" || template.Basis != "" {
		t.Fatalf("template = %+v", template)
	}
	if !reflect.DeepEqual(template.AcceptedLimitations, requiredMetPolicyLimitations) ||
		!reflect.DeepEqual(template.AuthorizedUses, requiredMetBatchAuthorizedUses) ||
		!reflect.DeepEqual(template.ExcludedAuthorities, requiredMetBatchExcludedAuthorities) {
		t.Fatalf("template declarations = %+v", template)
	}

	template.ReviewerID = "maintainer"
	template.ReviewedAt = fixture.reviewedAt.Format(time.RFC3339)
	template.Acceptance = MetRightsBatchAcceptanceAccepted
	template.Basis = "met_cc0_open_access_object_reviewed_v1: exact pinned policy evidence and complete zero-anomaly metadata pre-screen reviewed for private development use."
	attestationRaw, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := CompleteMetRightsBatchReview(fixture.inventoryRaw, fixture.worksheetRaw, fixture.prescreenRaw, attestationRaw)
	if err != nil {
		t.Fatal(err)
	}
	if completion.RowCount != len(fixture.inventory.Cases) || completion.AttestationSHA256 != digestBytes(attestationRaw) || completion.DownloadAuthority {
		t.Fatalf("completion = %+v", completion)
	}
	records, err := csv.NewReader(bytes.NewReader(completion.CompletedCSV)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != len(fixture.inventory.Cases)+1 || !reflect.DeepEqual(records[0], RightsReviewCSVHeader()) {
		t.Fatalf("records = %+v", records)
	}
	for index, row := range fixture.worksheet.Cases {
		record := records[index+1]
		immutable := ImmutableRightsReviewRecord(row)
		if !reflect.DeepEqual(record[:len(immutable)], immutable) {
			t.Fatalf("row %d changed immutable identity", index)
		}
		decision := record[len(immutable):]
		if len(decision) != 7 || decision[0] != "maintainer" || decision[1] != fixture.reviewedAt.Format(time.RFC3339) ||
			decision[2] != "approved" || !strings.Contains(decision[3], completion.AttestationSHA256) || decision[4] != "true" || decision[5] != "" || decision[6] != "[]" {
			t.Fatalf("row %d decision = %#v", index, decision)
		}
	}
}

func TestMetRightsBatchReviewRejectsAnythingOtherThanExactZeroAnomalyEvidence(t *testing.T) {
	tests := map[string]func(*testing.T, *metRightsBatchFixture, *MetRightsBatchAttestation){
		"held pre-screen case": func(t *testing.T, fixture *metRightsBatchFixture, _ *MetRightsBatchAttestation) {
			var report MetRightsPrescreen
			if err := json.Unmarshal(fixture.prescreenRaw, &report); err != nil {
				t.Fatal(err)
			}
			report.PassedCases, report.HeldCases = 0, 1
			report.Cases[0].Status = metRightsPrescreenHold
			report.Cases[0].ReasonCodes = []string{"rights_and_reproduction_nonempty"}
			fixture.prescreenRaw, _ = json.Marshal(report)
		},
		"changed inventory": func(_ *testing.T, fixture *metRightsBatchFixture, _ *MetRightsBatchAttestation) {
			fixture.inventoryRaw = append(fixture.inventoryRaw, ' ')
		},
		"changed worksheet": func(_ *testing.T, fixture *metRightsBatchFixture, _ *MetRightsBatchAttestation) {
			fixture.worksheetRaw = append(fixture.worksheetRaw, ' ')
		},
		"changed pre-screen": func(_ *testing.T, fixture *metRightsBatchFixture, _ *MetRightsBatchAttestation) {
			fixture.prescreenRaw = append(fixture.prescreenRaw, ' ')
		},
		"review before pre-screen": func(_ *testing.T, fixture *metRightsBatchFixture, attestation *MetRightsBatchAttestation) {
			attestation.ReviewedAt = fixture.prescreen.PreparedAt.Add(-time.Second).Format(time.RFC3339)
		},
		"limitations not accepted": func(_ *testing.T, _ *metRightsBatchFixture, attestation *MetRightsBatchAttestation) {
			attestation.AcceptedLimitations = attestation.AcceptedLimitations[:len(attestation.AcceptedLimitations)-1]
		},
		"authority widened": func(_ *testing.T, _ *metRightsBatchFixture, attestation *MetRightsBatchAttestation) {
			attestation.ExcludedAuthorities = attestation.ExcludedAuthorities[:len(attestation.ExcludedAuthorities)-1]
		},
		"attribution added": func(_ *testing.T, _ *metRightsBatchFixture, attestation *MetRightsBatchAttestation) {
			attestation.RequiredCredit = "The Met"
		},
		"restriction added": func(_ *testing.T, _ *metRightsBatchFixture, attestation *MetRightsBatchAttestation) {
			attestation.Restrictions = []string{"unknown restriction"}
		},
		"basis lacks policy prefix": func(_ *testing.T, _ *metRightsBatchFixture, attestation *MetRightsBatchAttestation) {
			attestation.Basis = "looks acceptable"
		},
		"basis omits required separator": func(_ *testing.T, _ *metRightsBatchFixture, attestation *MetRightsBatchAttestation) {
			attestation.Basis = "met_cc0_open_access_object_reviewed_v1:reviewed."
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newMetRightsBatchFixture(t)
			template, err := PrepareMetRightsBatchAttestation(fixture.inventoryRaw, fixture.worksheetRaw, fixture.prescreenRaw)
			if err != nil {
				t.Fatal(err)
			}
			template.ReviewerID = "maintainer"
			template.ReviewedAt = fixture.reviewedAt.Format(time.RFC3339)
			template.Acceptance = MetRightsBatchAcceptanceAccepted
			template.Basis = "met_cc0_open_access_object_reviewed_v1: reviewed."
			mutate(t, fixture, &template)
			attestationRaw, err := json.Marshal(template)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := CompleteMetRightsBatchReview(fixture.inventoryRaw, fixture.worksheetRaw, fixture.prescreenRaw, attestationRaw); err == nil {
				t.Fatal("unsafe batch completion succeeded")
			}
		})
	}
}

func TestMetRightsBatchReviewRejectsUnknownFieldsAndNonInertWorksheet(t *testing.T) {
	fixture := newMetRightsBatchFixture(t)
	template, err := PrepareMetRightsBatchAttestation(fixture.inventoryRaw, fixture.worksheetRaw, fixture.prescreenRaw)
	if err != nil {
		t.Fatal(err)
	}
	template.ReviewerID = "maintainer"
	template.ReviewedAt = fixture.reviewedAt.Format(time.RFC3339)
	template.Acceptance = MetRightsBatchAcceptanceAccepted
	template.Basis = "met_cc0_open_access_object_reviewed_v1: reviewed."
	raw, _ := json.Marshal(template)
	raw = bytes.Replace(raw, []byte(`"basis":`), []byte(`"unexpected":true,"basis":`), 1)
	if _, err := CompleteMetRightsBatchReview(fixture.inventoryRaw, fixture.worksheetRaw, fixture.prescreenRaw, raw); err == nil {
		t.Fatal("unknown attestation field passed")
	}

	fixture = newMetRightsBatchFixture(t)
	fixture.worksheet.Cases[0].Decision = "approved"
	fixture.worksheetRaw, _ = json.Marshal(fixture.worksheet)
	if _, err := PrepareMetRightsBatchAttestation(fixture.inventoryRaw, fixture.worksheetRaw, fixture.prescreenRaw); err == nil {
		t.Fatal("pre-authorized worksheet passed")
	}
}

type metRightsBatchFixture struct {
	inventory    Inventory
	inventoryRaw []byte
	worksheet    RightsWorksheet
	worksheetRaw []byte
	prescreen    MetRightsPrescreen
	prescreenRaw []byte
	reviewedAt   time.Time
}

func newMetRightsBatchFixture(t *testing.T) *metRightsBatchFixture {
	t.Helper()
	fixture := newMetRightsPrescreenFixture(t, `"rightsAndReproduction":"",`)
	inventoryRaw := fixture.inventoryBytes(t)
	prescreen, err := PrepareMetRightsPrescreen(inventoryRaw, fixture.policyBytes(t), fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	prescreenRaw, _ := json.Marshal(prescreen)
	row := RightsReviewRowFromCase(fixture.inventory.Cases[0])
	row.Rank = 1
	row.InventorySHA256 = digestBytes(inventoryRaw)
	worksheet := RightsWorksheet{
		SchemaVersion:   RightsWorksheetSchemaVersion,
		Profile:         RightsProfileDevelopment,
		InventorySHA256: digestBytes(inventoryRaw),
		SnapshotAt:      fixture.inventory.SnapshotAt,
		PreparedAt:      fixture.inventory.SnapshotAt.Add(30 * time.Minute),
		MinItems:        1,
		MaxItems:        1,
		Instructions:    []string{"Review the exact item."},
		Cases:           []RightsReviewRow{row},
	}
	worksheetRaw, _ := json.Marshal(worksheet)
	return &metRightsBatchFixture{
		inventory: fixture.inventory, inventoryRaw: inventoryRaw,
		worksheet: worksheet, worksheetRaw: worksheetRaw,
		prescreen: prescreen, prescreenRaw: prescreenRaw,
		reviewedAt: prescreen.PreparedAt.Add(time.Minute),
	}
}

func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
