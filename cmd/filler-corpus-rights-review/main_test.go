package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillerquarantine"
	"github.com/loomarr/loomarr/internal/testkit"
)

func reviewInventory(snapshot time.Time, ids ...string) fillercorpus.Inventory {
	captureID := fillercorpus.NewCaptureID("archive.org/prelinger", "prelinger", "commercial")
	inv := fillercorpus.Inventory{SchemaVersion: fillercorpus.InventorySchemaVersion, SnapshotAt: snapshot, Captures: []fillercorpus.Capture{{CaptureID: captureID, Transport: fillercorpus.TransportHTTPS, Authority: "archive.org/prelinger", Collection: "prelinger", RoleHint: "commercial", SnapshotAt: snapshot, MaxRequests: 10, RequestsUsed: 1, MaxResponseBytes: 10_000, ResponseBytes: 100, MaxPredictedMediaBytes: 10_000, PredictedMediaBytes: int64(len(ids)) * 1024, MaxWallTimeMS: 1000, WallTimeMS: 10}}}
	for _, id := range ids {
		inv.Cases = append(inv.Cases, fillercorpus.InventoryCase{CaseID: fillercorpus.CaseID("archive.org/prelinger", id), CaptureIDs: []string{captureID}, Authority: "archive.org/prelinger", ItemID: id, Title: id, RoleHints: []string{"commercial"}, RightsAssertions: []string{"CC0"}, ItemURL: "https://archive.org/details/" + id, MetadataURL: "https://archive.org/metadata/" + id, MetadataRetrievedAt: snapshot, MetadataSHA256: strings.Repeat("a", 64), AllowedMediaHosts: []string{"archive.org", ".archive.org"}, Representation: fillercorpus.InventoryRepresentation{Transport: fillercorpus.TransportHTTPS, Name: id + ".mp4", URL: "https://archive.org/download/" + id + "/" + id + ".mp4", MIMEType: "video/mp4", Bytes: 1024}})
	}
	return inv
}

func TestRunWritesSpreadsheetSafeInertCSV(t *testing.T) {
	snapshot := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	inv := reviewInventory(snapshot, "formula-title")
	inv.Cases[0].Title = `=HYPERLINK("https://attacker.invalid")`
	raw, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	inventoryPath, worksheetPath, csvPath := filepath.Join(dir, "inventory.json"), filepath.Join(dir, "worksheet.json"), filepath.Join(dir, "worksheet.csv")
	if err := os.WriteFile(inventoryPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	inspectionPath := writeEligibleInspection(t, dir, raw, inv)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--inventory", inventoryPath, "--quarantine-inspection", inspectionPath, "--out", worksheetPath, "--csv-out", csvPath, "--prepared-at", snapshot.Add(time.Minute).Format(time.RFC3339), "--profile", "development", "--min-items", "1", "--max-items", "1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	csvRaw, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(bytes.NewReader(csvRaw)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	columns := map[string]string{}
	for i, name := range records[0] {
		columns[name] = records[1][i]
	}
	if columns["title"] != `'`+inv.Cases[0].Title {
		t.Fatalf("title = %q", columns["title"])
	}
	for _, field := range []string{"reviewer_id", "reviewed_at", "decision", "basis", "redistributable", "required_credit", "restrictions_json"} {
		if columns[field] != "" {
			t.Fatalf("%s unexpectedly grants authority", field)
		}
	}
}

func TestRunRefusesCollidingOrExistingOutputsBeforeReadingInventory(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.csv")
	original := []byte("review work that must survive\n")
	if err := os.WriteFile(existing, original, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, output, csvOutput, want string
	}{
		{name: "existing", output: filepath.Join(dir, "new.json"), csvOutput: existing, want: "worksheet output already exists"},
		{name: "same path", output: filepath.Join(dir, "same"), csvOutput: filepath.Join(dir, "same"), want: "distinct paths"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{
				"--inventory", filepath.Join(dir, "missing-inventory.json"),
				"--out", test.output,
				"--csv-out", test.csvOutput,
				"--prepared-at", "2026-09-05T12:00:00Z",
				"--profile", "quarantine",
				"--min-items", "1",
				"--max-items", "1",
			}, &stdout, &stderr)
			if code != 1 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
	if got, err := os.ReadFile(existing); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("existing review changed: got=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.json")); !os.IsNotExist(err) {
		t.Fatalf("paired output was partially published: %v", err)
	}
}

func TestWriteAtomicCannotReplacePublishedWorksheet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worksheet.json")
	if err := writeAtomic(path, func(writer io.Writer) error {
		_, err := writer.Write([]byte("first\n"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, func(writer io.Writer) error {
		_, err := writer.Write([]byte("replacement\n"))
		return err
	}); err == nil {
		t.Fatal("immutable worksheet was replaced")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "first\n" {
		t.Fatalf("published worksheet changed: got=%q err=%v", got, err)
	}
}

func TestPrepareWorksheetIsDeterministicAndInert(t *testing.T) {
	snapshot := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	inv := reviewInventory(snapshot, "third", "first", "second")
	raw, _ := json.Marshal(inv)
	selection := eligibleSelection(t, raw, inv, 2, 2)
	digest := fillercorpus.InventorySHA256(raw)
	first, err := prepareWorksheetForProfile(inv, digest, snapshot.Add(time.Minute), 2, 2, fillercorpus.RightsProfileDevelopment, nil, &selection)
	if err != nil {
		t.Fatal(err)
	}
	second, err := prepareWorksheetForProfile(inv, digest, snapshot.Add(time.Minute), 2, 2, fillercorpus.RightsProfileDevelopment, nil, &selection)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first.Cases) != 2 {
		t.Fatalf("non-deterministic worksheet")
	}
	for _, row := range first.Cases {
		if row.Decision != "" || row.ReviewerID != "" || row.Redistributable {
			t.Fatalf("row %s grants authority", row.CaseID)
		}
	}
}

func TestPrepareWorksheetFailsBelowMinimum(t *testing.T) {
	snapshot := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	inv := reviewInventory(snapshot, "one")
	raw, _ := json.Marshal(inv)
	report := testkit.FillerQuarantineReport(t, raw, dispositions(inv, fillerquarantine.DispositionEligibleForRightsReview), nil)
	authority, err := fillerquarantine.OpenRightsEligibility(raw, report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Selected(2, 10); err == nil {
		t.Fatal("undersized inventory was accepted")
	}
}

func TestRunPreparesInertCertificationScheduleBoundToApprovedTemplate(t *testing.T) {
	snapshot := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	inv := reviewInventory(snapshot, "holdout-one")
	raw, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	inventoryPath, worksheetPath, csvPath := filepath.Join(dir, "inventory.json"), filepath.Join(dir, "worksheet.json"), filepath.Join(dir, "worksheet.csv")
	if err := os.WriteFile(inventoryPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	inspectionPath := writeEligibleInspection(t, dir, raw, inv)
	shaA, shaB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--inventory", inventoryPath, "--quarantine-inspection", inspectionPath, "--out", worksheetPath, "--csv-out", csvPath, "--prepared-at", snapshot.Add(time.Minute).Format(time.RFC3339), "--profile", "certification", "--agreement-id", "agreement-v1", "--agreement-sha256", shaA, "--processor-id", "openrouter/vertex", "--processor-terms-sha256", shaB, "--min-items", "1", "--max-items", "1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	var worksheet fillercorpus.RightsWorksheet
	worksheetRaw, _ := os.ReadFile(worksheetPath)
	if err := json.Unmarshal(worksheetRaw, &worksheet); err != nil {
		t.Fatal(err)
	}
	if worksheet.SchemaVersion != fillercorpus.HoldoutRightsWorksheetSchemaVersion || worksheet.Profile != fillercorpus.RightsProfileCertification || worksheet.HoldoutTemplate == nil || worksheet.HoldoutTemplate.AgreementSHA256 != shaA || worksheet.HoldoutTemplate.ProcessorTermsSHA256 != shaB {
		t.Fatalf("worksheet = %+v", worksheet)
	}
	records, err := csv.NewReader(mustOpen(t, csvPath)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records[0], fillercorpus.HoldoutRightsReviewCSVHeader()) {
		t.Fatalf("header = %v", records[0])
	}
	for index := len(fillercorpus.ImmutableRightsReviewRecordForProfile(worksheet.Cases[0], worksheet.Profile)); index < len(records[1]); index++ {
		if records[1][index] != "" {
			t.Fatalf("authority field %s was prefilled", records[0][index])
		}
	}
}

func TestRunPreparesInertQuarantineWorksheetWithExplicitDeniedUses(t *testing.T) {
	snapshot := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	inv := reviewInventory(snapshot, "quarantine-one")
	raw, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	inventoryPath, worksheetPath, csvPath := filepath.Join(dir, "inventory.json"), filepath.Join(dir, "worksheet.json"), filepath.Join(dir, "worksheet.csv")
	if err := os.WriteFile(inventoryPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--inventory", inventoryPath, "--out", worksheetPath, "--csv-out", csvPath, "--prepared-at", snapshot.Add(time.Minute).Format(time.RFC3339), "--profile", "quarantine", "--min-items", "1", "--max-items", "1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	var worksheet fillercorpus.RightsWorksheet
	worksheetRaw, err := os.ReadFile(worksheetPath)
	if err != nil || json.Unmarshal(worksheetRaw, &worksheet) != nil {
		t.Fatalf("worksheet read = %v", err)
	}
	if worksheet.SchemaVersion != fillercorpus.QuarantineRightsWorksheetSchemaVersion || worksheet.Profile != fillercorpus.RightsProfileQuarantine || worksheet.HoldoutTemplate != nil {
		t.Fatalf("worksheet = %+v", worksheet)
	}
	records, err := csv.NewReader(mustOpen(t, csvPath)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records[0], fillercorpus.QuarantineRightsReviewCSVHeader()) {
		t.Fatalf("header = %v", records[0])
	}
	for index := len(fillercorpus.ImmutableRightsReviewRecord(worksheet.Cases[0])); index < len(records[1]); index++ {
		if records[1][index] != "" {
			t.Fatalf("authority field %s was prefilled", records[0][index])
		}
	}
}

func TestRunDevelopmentSelectsDirectLocalAndExcludesHeldRemote(t *testing.T) {
	snapshot := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	remote := reviewInventory(snapshot, "remote-held")
	local := directReviewInventory(snapshot, "local-eligible")
	inv, err := fillercorpus.MergeInventories(remote, local)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	disposition := map[string]string{remote.Cases[0].CaseID: fillerquarantine.DispositionHold}
	reportRaw := testkit.FillerQuarantineReport(t, raw, disposition, nil)
	dir := t.TempDir()
	inventoryPath := filepath.Join(dir, "inventory.json")
	reportPath := filepath.Join(dir, "inspection.json")
	worksheetPath := filepath.Join(dir, "worksheet.json")
	if err := os.WriteFile(inventoryPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, reportRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--inventory", inventoryPath, "--quarantine-inspection", reportPath, "--out", worksheetPath,
		"--prepared-at", snapshot.Add(time.Minute).Format(time.RFC3339), "--profile", "development", "--min-items", "1", "--max-items", "2",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var worksheet fillercorpus.RightsWorksheet
	worksheetRaw, err := os.ReadFile(worksheetPath)
	if err != nil || json.Unmarshal(worksheetRaw, &worksheet) != nil {
		t.Fatalf("worksheet read = %v", err)
	}
	if len(worksheet.Cases) != 1 || worksheet.Cases[0].CaseID != local.Cases[0].CaseID || worksheet.Cases[0].QuarantineInspection != nil || worksheet.QuarantineInspection == nil {
		t.Fatalf("transport-aware worksheet = %+v", worksheet)
	}
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func dispositions(inv fillercorpus.Inventory, disposition string) map[string]string {
	result := make(map[string]string, len(inv.Cases))
	for _, item := range inv.Cases {
		if item.Representation.Transport != fillercorpus.TransportLocal {
			result[item.CaseID] = disposition
		}
	}
	return result
}

func writeEligibleInspection(t *testing.T, dir string, raw []byte, inv fillercorpus.Inventory) string {
	t.Helper()
	path := filepath.Join(dir, "quarantine-inspection.json")
	if err := os.WriteFile(path, testkit.FillerQuarantineReport(t, raw, dispositions(inv, fillerquarantine.DispositionEligibleForRightsReview), nil), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func eligibleSelection(t *testing.T, raw []byte, inv fillercorpus.Inventory, minItems, maxItems int) fillerquarantine.RightsSelection {
	t.Helper()
	report := testkit.FillerQuarantineReport(t, raw, dispositions(inv, fillerquarantine.DispositionEligibleForRightsReview), nil)
	authority, err := fillerquarantine.OpenRightsEligibility(raw, report)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := authority.Selected(minItems, maxItems)
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func directReviewInventory(snapshot time.Time, id string) fillercorpus.Inventory {
	authority, collection, role := "direct-license", "fixture", "commercial"
	captureID := fillercorpus.NewCaptureID(authority, collection, role)
	media := []byte("direct fixture")
	return fillercorpus.Inventory{
		SchemaVersion: fillercorpus.InventorySchemaVersion, SnapshotAt: snapshot,
		Captures: []fillercorpus.Capture{{CaptureID: captureID, Transport: fillercorpus.TransportLocal, Authority: authority, Collection: collection, RoleHint: role, SnapshotAt: snapshot, MaxPredictedMediaBytes: int64(len(media)), PredictedMediaBytes: int64(len(media)), MaxWallTimeMS: 1_000}},
		Cases: []fillercorpus.InventoryCase{{
			CaseID: fillercorpus.CaseID(authority, id), CaptureIDs: []string{captureID}, Authority: authority, ItemID: id, Title: id, RoleHints: []string{role},
			RightsAssertions: []string{"signed grant"}, MetadataRetrievedAt: snapshot, MetadataSHA256: strings.Repeat("e", 64),
			Evidence:       []fillercorpus.InventoryEvidence{{Kind: "rights", Path: "rights.txt", Bytes: 1, SHA256: strings.Repeat("a", 64)}, {Kind: "provenance", Path: "provenance.txt", Bytes: 1, SHA256: strings.Repeat("b", 64)}},
			Representation: fillercorpus.InventoryRepresentation{Transport: fillercorpus.TransportLocal, Name: id + ".mp4", Path: id + ".mp4", MIMEType: "video/mp4", Bytes: int64(len(media)), SHA256: fillercorpus.InventorySHA256(media)},
		}},
	}
}
