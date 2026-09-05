package fillercorpus

import (
	"strings"
	"testing"
	"time"
)

func TestValidateQuarantineDownloadLedgerAcceptsOnlyExactLocalInspectionAuthority(t *testing.T) {
	inventory, raw, ledger := quarantineDownloadFixture()
	if err := ValidateQuarantineDownloadLedger(inventory, InventorySHA256(raw), ledger); err != nil {
		t.Fatal(err)
	}
	redirected := ledger
	redirected.MaxRequests = 2
	redirected.RequestsUsed = 2
	if err := ValidateQuarantineDownloadLedger(inventory, InventorySHA256(raw), redirected); err != nil {
		t.Fatalf("ledger counting a redirect as a request was rejected: %v", err)
	}

	tests := map[string]func(*DownloadLedger){
		"profile":            func(value *DownloadLedger) { value.Profile = RightsProfileDevelopment },
		"request ceiling":    func(value *DownloadLedger) { value.RequestsUsed = value.MaxRequests + 1 },
		"provider transfer":  func(value *DownloadLedger) { value.Cases[0].Approval.QuarantineContract.ProviderTransfer = true },
		"inventory identity": func(value *DownloadLedger) { value.Cases[0].Approval.InventorySHA256 = strings.Repeat("f", 64) },
		"unsafe path":        func(value *DownloadLedger) { value.Cases[0].LocalFile = "../escape.mp4" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, candidate := quarantineDownloadFixture()
			mutate(&candidate)
			if err := ValidateQuarantineDownloadLedger(inventory, InventorySHA256(raw), candidate); err == nil {
				t.Fatal("invalid quarantine ledger accepted")
			}
		})
	}
}

func TestDecodeDownloadLedgerIsStrict(t *testing.T) {
	if _, err := DecodeDownloadLedger(strings.NewReader(`{"schemaVersion":2,"unknown":true}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := DecodeDownloadLedger(strings.NewReader(`{} {}`)); err == nil {
		t.Fatal("trailing value accepted")
	}
}

func quarantineDownloadFixture() (Inventory, []byte, DownloadLedger) {
	snapshot := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	metadata := snapshot.Add(-2 * time.Hour)
	reviewed := snapshot.Add(-time.Hour)
	caseID := CaseID("loc.gov/national-screening-room", "fixture")
	captureID := NewCaptureID("loc.gov/national-screening-room", "fixture", "commercial")
	representation := InventoryRepresentation{Transport: TransportHTTPS, Name: "fixture.mp4", URL: "https://tile.loc.gov/fixture.mp4", MIMEType: "video/mp4", Bytes: 100, SHA1: strings.Repeat("1", 40)}
	inventory := Inventory{SchemaVersion: InventorySchemaVersion, SnapshotAt: snapshot, Captures: []Capture{{
		CaptureID: captureID, Transport: TransportHTTPS, Authority: "loc.gov/national-screening-room", Collection: "fixture", RoleHint: "commercial", SnapshotAt: snapshot,
		MaxRequests: 1, RequestsUsed: 1, MaxResponseBytes: 1024, ResponseBytes: 100, MaxPredictedMediaBytes: 100, PredictedMediaBytes: 100, MaxWallTimeMS: 1_000, WallTimeMS: 1,
	}}, Cases: []InventoryCase{{
		CaseID: caseID, CaptureIDs: []string{captureID}, Authority: "loc.gov/national-screening-room", ItemID: "fixture", Title: "Fixture", RoleHints: []string{"commercial"}, Collection: []string{"fixture"},
		RightsAssertions: []string{"review required"}, ItemURL: "https://www.loc.gov/item/fixture/", MetadataURL: "https://www.loc.gov/item/fixture/?fo=json", MetadataRetrievedAt: metadata,
		MetadataSHA256: strings.Repeat("a", 64), AllowedMediaHosts: []string{"tile.loc.gov"}, Representation: representation,
	}}}
	raw := []byte(`fixture inventory bytes`)
	inventorySHA := InventorySHA256(raw)
	approval := RightsDecision{
		InventorySHA256: inventorySHA, CaseID: caseID, CaptureIDs: []string{captureID}, Authority: "loc.gov/national-screening-room", ItemID: "fixture", MetadataSHA256: strings.Repeat("a", 64),
		ReviewerID: "reviewer", ReviewedAt: reviewed, Decision: "approved", Basis: "local inspection only", QuarantineContract: &QuarantineAcquisitionContract{
			SchemaVersion: QuarantineAcquisitionContractSchemaVersion, Purpose: QuarantinePurposeLocalInspection, CopyAndStorage: true, LocalTechnicalInspection: true,
		},
	}
	ledger := DownloadLedger{
		SchemaVersion: DownloadLedgerSchemaVersion, Profile: RightsProfileQuarantine, InventorySHA256: inventorySHA, GeneratedAt: snapshot,
		MaxRequests: 1, RequestsUsed: 1, MaxItems: 1, MaxBytes: 100, Bytes: 100,
		Cases: []DownloadCase{{
			CaseID: caseID, Authority: inventory.Cases[0].Authority, ItemID: inventory.Cases[0].ItemID, ItemURL: inventory.Cases[0].ItemURL, MetadataURL: inventory.Cases[0].MetadataURL,
			MetadataRetrievedAt: metadata, MetadataSHA256: inventory.Cases[0].MetadataSHA256, Representation: representation, LocalFile: "fixture.mp4", ContentSHA256: strings.Repeat("b", 64), Approval: approval, VerifiedAt: snapshot,
		}},
	}
	return inventory, raw, ledger
}
