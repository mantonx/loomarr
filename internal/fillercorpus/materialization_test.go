package fillercorpus

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestValidateMaterializationLedgerBindsCompleteInventoryProvenance(t *testing.T) {
	inventory, inventorySHA256, ledger := validMaterializationFixture(t)
	if err := ValidateMaterializationLedger(ledger, inventory, inventorySHA256); err != nil {
		t.Fatal(err)
	}

	drifted := ledger
	drifted.Cases = append([]MaterializedCase(nil), ledger.Cases...)
	drifted.Cases[0].Creator = []string{"different creator"}
	if err := ValidateMaterializationLedger(drifted, inventory, inventorySHA256); err == nil {
		t.Fatal("ValidateMaterializationLedger accepted changed creator provenance")
	}

	drifted = ledger
	drifted.Cases = append([]MaterializedCase(nil), ledger.Cases...)
	drifted.Cases[0].Approval.MetadataSHA256 = strings.Repeat("b", 64)
	if err := ValidateMaterializationLedger(drifted, inventory, inventorySHA256); err == nil {
		t.Fatal("ValidateMaterializationLedger accepted an approval for different metadata")
	}
}

func TestDecodeMaterializationLedgerIsStrictAndRejectsLegacySchema(t *testing.T) {
	_, _, ledger := validMaterializationFixture(t)
	raw, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeMaterializationLedger(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Replace(raw, []byte(`"schemaVersion":3`), []byte(`"schemaVersion":2`), 1)
	if _, err := DecodeMaterializationLedger(bytes.NewReader(legacy)); err == nil {
		t.Fatal("DecodeMaterializationLedger accepted schema 2")
	}
	unknown := bytes.Replace(raw, []byte(`"profile":`), []byte(`"unknown":true,"profile":`), 1)
	if _, err := DecodeMaterializationLedger(bytes.NewReader(unknown)); err == nil {
		t.Fatal("DecodeMaterializationLedger accepted an unknown field")
	}
	if _, err := DecodeMaterializationLedger(bytes.NewReader(append(raw, []byte(` {}`)...))); err == nil {
		t.Fatal("DecodeMaterializationLedger accepted trailing JSON")
	}
}

func validMaterializationFixture(t *testing.T) (Inventory, string, MaterializationLedger) {
	t.Helper()
	snapshot := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	inventory := validInventoryForMerge(snapshot, "archive.org/prelinger", "archive.org")
	inventory.Cases[0].Creator = []string{"Example Creator"}
	inventory.Cases[0].SubjectTerms = []string{"Advertising"}
	inventory.Cases[0].Campaign = "Example Campaign"
	inventory.Cases[0].SourceFamily = "example-family"
	inventorySHA256 := strings.Repeat("f", 64)
	candidate := inventory.Cases[0]
	approval := RightsDecision{
		InventorySHA256: inventorySHA256, CaseID: candidate.CaseID, CaptureIDs: candidate.CaptureIDs,
		Authority: candidate.Authority, ItemID: candidate.ItemID, MetadataSHA256: candidate.MetadataSHA256,
		ReviewerID: "rights-reviewer", ReviewedAt: snapshot.Add(time.Minute), Decision: "approved",
		Basis: "frozen source grant reviewed", Redistributable: true,
	}
	materialized := MaterializedCase{
		CaseID: candidate.CaseID, CaptureIDs: candidate.CaptureIDs, Authority: candidate.Authority,
		ItemID: candidate.ItemID, RoleHints: candidate.RoleHints, Creator: candidate.Creator,
		SubjectTerms: candidate.SubjectTerms, Campaign: candidate.Campaign, SourceFamily: candidate.SourceFamily,
		LicenseURL: candidate.LicenseURL, ItemURL: candidate.ItemURL, MetadataURL: candidate.MetadataURL,
		MetadataRetrievedAt: candidate.MetadataRetrievedAt, MetadataSHA256: candidate.MetadataSHA256,
		Representation: candidate.Representation, LocalFile: "materialized.mp4", ContentSHA256: strings.Repeat("c", 64),
		VerifiedMediaType: "video/mp4", Approval: approval, VerifiedAt: snapshot.Add(2 * time.Minute),
	}
	ledger := MaterializationLedger{
		SchemaVersion: MaterializationLedgerSchemaVersion, Profile: RightsProfileDevelopment,
		InventorySHA256: inventorySHA256, GeneratedAt: snapshot.Add(3 * time.Minute),
		MaxRequests: 2, RequestsUsed: 1, MaxItems: 2, MaxBytes: 200, Bytes: 100,
		MaxImagePixels: MaximumMaterializedImagePixels, Cases: []MaterializedCase{materialized},
	}
	return inventory, inventorySHA256, ledger
}
