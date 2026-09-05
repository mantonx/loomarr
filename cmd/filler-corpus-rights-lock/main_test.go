package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillerquarantine"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestRunLocksCompleteSpreadsheetReviewToDownloaderJSONL(t *testing.T) {
	dir := t.TempDir()
	inventoryPath := filepath.Join(dir, "inventory.json")
	worksheetPath := filepath.Join(dir, "worksheet.json")
	csvPath := filepath.Join(dir, "completed.csv")
	approvalsPath := filepath.Join(dir, "approvals.jsonl")
	inspectionPath := filepath.Join(dir, "inspection.json")
	heldInspectionPath := filepath.Join(dir, "held-inspection.json")
	metadataDigest := strings.Repeat("a", 64)
	retrievedAt := "2026-08-25T08:00:00Z"
	reviewedAt := "2026-08-25T09:00:00Z"
	retrievedTime, _ := time.Parse(time.RFC3339, retrievedAt)
	authority := "archive.org/prelinger"
	captureID := fillercorpus.NewCaptureID(authority, "prelinger", "commercial")
	item := fillercorpus.InventoryCase{CaseID: fillercorpus.CaseID(authority, "soda-ad"), CaptureIDs: []string{captureID}, Authority: authority, ItemID: "soda-ad", Title: "Mountain Dew", RoleHints: []string{"commercial"}, LicenseURL: "https://creativecommons.org/publicdomain/zero/1.0/", RightsAssertions: []string{"CC0"}, ItemURL: "https://archive.org/details/soda-ad", MetadataURL: "https://archive.org/metadata/soda-ad", MetadataRetrievedAt: retrievedTime, MetadataSHA256: metadataDigest, AllowedMediaHosts: []string{"archive.org", ".archive.org"}, Representation: fillercorpus.InventoryRepresentation{Transport: fillercorpus.TransportHTTPS, Name: "soda.mp4", URL: "https://archive.org/download/soda-ad/soda.mp4", MIMEType: "video/mp4", Origin: "original", Bytes: 1024}}
	inventory := fillercorpus.Inventory{SchemaVersion: fillercorpus.InventorySchemaVersion, SnapshotAt: retrievedTime, Captures: []fillercorpus.Capture{{CaptureID: captureID, Transport: fillercorpus.TransportHTTPS, Authority: authority, Collection: "prelinger", RoleHint: "commercial", SnapshotAt: retrievedTime, MaxRequests: 2, RequestsUsed: 1, MaxResponseBytes: 2048, ResponseBytes: 100, MaxPredictedMediaBytes: 2048, PredictedMediaBytes: 1024, MaxWallTimeMS: 1000, WallTimeMS: 10}}, Cases: []fillercorpus.InventoryCase{item}}
	inventoryRaw, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(inventoryRaw))
	if err := os.WriteFile(inventoryPath, inventoryRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	reportRaw := testkit.FillerQuarantineReport(t, inventoryRaw, map[string]string{item.CaseID: fillerquarantine.DispositionEligibleForRightsReview}, nil)
	if err := os.WriteFile(inspectionPath, reportRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	heldReportRaw := testkit.FillerQuarantineReport(t, inventoryRaw, map[string]string{item.CaseID: fillerquarantine.DispositionHold}, nil)
	if err := os.WriteFile(heldInspectionPath, heldReportRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	authorityView, err := fillerquarantine.OpenRightsEligibility(inventoryRaw, reportRaw)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := authorityView.Selected(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	rowValue := fillercorpus.RightsReviewRowFromCase(item)
	rowValue.Rank, rowValue.InventorySHA256 = 1, digest
	rowValue.QuarantineInspection = selection.Cases[0].QuarantineInspection
	worksheet := fillercorpus.RightsWorksheet{SchemaVersion: fillercorpus.RightsWorksheetSchemaVersion, Profile: fillercorpus.RightsProfileDevelopment, InventorySHA256: digest, SnapshotAt: retrievedTime, PreparedAt: retrievedTime.Add(30 * time.Minute), MinItems: 1, MaxItems: 1, QuarantineInspection: selection.QuarantineInspection, Cases: []fillercorpus.RightsReviewRow{rowValue}}
	raw, err := json.Marshal(worksheet)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(worksheetPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := csv.NewWriter(file)
	completed := append(fillercorpus.ImmutableRightsReviewRecordForProfile(rowValue, fillercorpus.RightsProfileDevelopment), "rights-reviewer", reviewedAt, "approved", "CC0 dedication and item inspection permit redistribution.", "true", "", "[]")
	if err := writer.WriteAll([][]string{fillercorpus.RightsReviewCSVHeader(), completed}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for name, reportPath := range map[string]string{
		"held":  heldInspectionPath,
		"drift": writeReportFixture(t, dir, "drifted-inspection.json", append(append([]byte(nil), reportRaw...), '\n')),
	} {
		t.Run(name+" report fails closed", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			output := filepath.Join(dir, name+"-approvals.jsonl")
			code := run([]string{
				"--inventory", inventoryPath, "--quarantine-inspection", reportPath,
				"--worksheet", worksheetPath, "--completed-csv", csvPath, "--approvals-out", output,
				"--locked-at", "2026-08-25T10:00:00Z", "--profile", "development",
			}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("failed lock published authority: %v", err)
			}
		})
	}
	for name, mutate := range map[string]func(*fillercorpus.RightsWorksheet){
		"missing global binding": func(value *fillercorpus.RightsWorksheet) { value.QuarantineInspection = nil },
		"changed case binding": func(value *fillercorpus.RightsWorksheet) {
			value.Cases[0].QuarantineInspection.ContentSHA256 = strings.Repeat("f", 64)
		},
	} {
		t.Run(name+" fails closed", func(t *testing.T) {
			worksheetCopyRaw, err := json.Marshal(worksheet)
			if err != nil {
				t.Fatal(err)
			}
			var changed fillercorpus.RightsWorksheet
			if err := json.Unmarshal(worksheetCopyRaw, &changed); err != nil {
				t.Fatal(err)
			}
			mutate(&changed)
			changedRaw, _ := json.Marshal(changed)
			changedPath := writeReportFixture(t, dir, strings.ReplaceAll(name, " ", "-")+".json", changedRaw)
			output := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".jsonl")
			var stdout, stderr bytes.Buffer
			code := run([]string{
				"--inventory", inventoryPath, "--quarantine-inspection", inspectionPath,
				"--worksheet", changedPath, "--completed-csv", csvPath, "--approvals-out", output,
				"--locked-at", "2026-08-25T10:00:00Z", "--profile", "development",
			}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--inventory", inventoryPath,
		"--quarantine-inspection", inspectionPath,
		"--worksheet", worksheetPath,
		"--completed-csv", csvPath,
		"--approvals-out", approvalsPath,
		"--locked-at", "2026-08-25T10:00:00Z",
		"--profile", "development",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	approvalRaw, err := os.ReadFile(approvalsPath)
	if err != nil {
		t.Fatal(err)
	}
	var approval struct {
		WorksheetSchemaVersion int                                           `json:"worksheetSchemaVersion"`
		InventorySHA256        string                                        `json:"inventorySha256"`
		CaseID                 string                                        `json:"caseId"`
		MetadataSHA256         string                                        `json:"metadataSha256"`
		ReviewerID             string                                        `json:"reviewerId"`
		ReviewedAt             time.Time                                     `json:"reviewedAt"`
		Decision               string                                        `json:"decision"`
		Redistributable        bool                                          `json:"redistributable"`
		QuarantineInspection   *fillercorpus.QuarantineInspectionCaseBinding `json:"quarantineInspection"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(approvalRaw), &approval); err != nil {
		t.Fatal(err)
	}
	if approval.WorksheetSchemaVersion != fillercorpus.RightsWorksheetSchemaVersion || approval.InventorySHA256 != digest || approval.CaseID != item.CaseID || approval.MetadataSHA256 != metadataDigest || approval.ReviewerID != "rights-reviewer" || approval.Decision != "approved" || !approval.Redistributable || approval.QuarantineInspection == nil || approval.QuarantineInspection.Report.ReportSHA256 != fillercorpus.InventorySHA256(reportRaw) {
		t.Fatalf("unexpected locked approval: %+v", approval)
	}
}

func writeReportFixture(t *testing.T, dir, name string, raw []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunRefusesExistingApprovalsBeforeReadingInputs(t *testing.T) {
	dir := t.TempDir()
	approvalsPath := filepath.Join(dir, "approvals.jsonl")
	original := []byte("existing locked authority\n")
	if err := os.WriteFile(approvalsPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--inventory", filepath.Join(dir, "missing-inventory.json"),
		"--worksheet", filepath.Join(dir, "missing-worksheet.json"),
		"--completed-csv", filepath.Join(dir, "missing-review.csv"),
		"--approvals-out", approvalsPath,
		"--locked-at", "2026-09-05T12:00:00Z",
		"--profile", "quarantine",
	}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "approvals output already exists") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got, err := os.ReadFile(approvalsPath); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("existing approvals changed: got=%q err=%v", got, err)
	}
}

func TestDecodeWorksheetRejectsUnknownAndTrailingFields(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown":  `{"schemaVersion":3,"unknown":true}`,
		"trailing": `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeWorksheet([]byte(raw)); err == nil {
				t.Fatal("non-strict worksheet was accepted")
			}
		})
	}
}

func TestWriteJSONLCannotReplacePublishedApprovals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.jsonl")
	first := []fillercorpus.RightsDecision{{CaseID: "first"}}
	if err := writeJSONL(path, first); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(path, []fillercorpus.RightsDecision{{CaseID: "replacement"}}); err == nil {
		t.Fatal("immutable rights decisions were replaced")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("published approvals changed: got=%q err=%v", got, err)
	}
}

func TestParseDecisionRejectsIncompleteOrInconsistentAuthority(t *testing.T) {
	retrievedAt := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	lockedAt := retrievedAt.Add(2 * time.Hour)
	row := fillercorpus.RightsReviewRow{MetadataRetrievedAt: retrievedAt, LicenseURL: "https://creativecommons.org/publicdomain/zero/1.0/"}
	valid := []string{"rights-reviewer", retrievedAt.Add(time.Hour).Format(time.RFC3339), "approved", "Exact item inspection confirms CC0 redistribution.", "true", "", "[]"}
	tests := map[string]func([]string){
		"missing reviewer":           func(fields []string) { fields[0] = "" },
		"review before metadata":     func(fields []string) { fields[1] = retrievedAt.Add(-time.Minute).Format(time.RFC3339) },
		"review after lock":          func(fields []string) { fields[1] = lockedAt.Add(time.Minute).Format(time.RFC3339) },
		"unknown decision":           func(fields []string) { fields[2] = "maybe" },
		"missing basis":              func(fields []string) { fields[3] = "" },
		"invalid redistribution":     func(fields []string) { fields[4] = "yes" },
		"approval without authority": func(fields []string) { fields[4] = "false" },
		"malformed restrictions":     func(fields []string) { fields[6] = "none" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fields := append([]string(nil), valid...)
			mutate(fields)
			if _, err := parseDecision(row, fields, lockedAt); err == nil {
				t.Fatal("invalid rights authority was accepted")
			}
		})
	}
	t.Run("held row grants redistribution", func(t *testing.T) {
		fields := append([]string(nil), valid...)
		fields[2] = "held"
		if _, err := parseDecision(row, fields, lockedAt); err == nil {
			t.Fatal("held row granted redistribution authority")
		}
	})
	t.Run("attribution license lacks credit", func(t *testing.T) {
		byRow := row
		byRow.LicenseURL = "https://creativecommons.org/licenses/by/4.0/"
		if _, err := parseDecision(byRow, valid, lockedAt); err == nil {
			t.Fatal("attribution-bearing approval without credit was accepted")
		}
	})
}

func TestParseQuarantineDecisionAllowsOnlyLocalCopyAndInspection(t *testing.T) {
	retrievedAt := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	lockedAt := retrievedAt.Add(2 * time.Hour)
	row := fillercorpus.RightsReviewRow{CaseID: "loc/item", MetadataRetrievedAt: retrievedAt}
	valid := []string{
		"rights-reviewer", retrievedAt.Add(time.Hour).Format(time.RFC3339), "approved", "Exact source terms permit a local quarantine copy and inspection.",
		"true", "true", "false", "false", "false", "false", "false", "false", "false", "", "[]",
	}
	decision, err := parseQuarantineDecision(row, valid, lockedAt)
	if err != nil || decision.QuarantineContract == nil || decision.Redistributable || len(decision.QuarantineContract.HoldReasons) != 0 {
		t.Fatalf("decision = %+v, %v", decision, err)
	}
	for index, name := range []string{"provider transfer", "redistribution", "corpus preparation", "training", "catalog ingestion", "scheduling", "production admission"} {
		t.Run(name, func(t *testing.T) {
			fields := append([]string(nil), valid...)
			fields[6+index] = "true"
			if _, err := parseQuarantineDecision(row, fields, lockedAt); err == nil {
				t.Fatal("downstream authority was accepted")
			}
		})
	}
	for _, index := range []int{4, 5} {
		fields := append([]string(nil), valid...)
		fields[index] = "false"
		if _, err := parseQuarantineDecision(row, fields, lockedAt); err == nil {
			t.Fatalf("required local authority field %d was omitted", index)
		}
	}
	t.Run("held row remains inert", func(t *testing.T) {
		fields := append([]string(nil), valid...)
		fields[2] = "held"
		for index := 4; index <= 12; index++ {
			fields[index] = "false"
		}
		decision, err := parseQuarantineDecision(row, fields, lockedAt)
		if err != nil || decision.QuarantineContract == nil || len(decision.QuarantineContract.HoldReasons) == 0 {
			t.Fatalf("held decision = %+v, %v", decision, err)
		}
	})
}

func TestParseHoldoutDecisionRequiresEveryIndependentAuthorityAxis(t *testing.T) {
	retrievedAt := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	lockedAt := retrievedAt.Add(2 * time.Hour)
	row := fillercorpus.RightsReviewRow{CaseID: "direct/one", MetadataRetrievedAt: retrievedAt}
	template := &fillercorpus.HoldoutRightsTemplate{AgreementID: "agreement-v1", AgreementSHA256: strings.Repeat("a", 64), ProcessorID: "openrouter/vertex", ProcessorTermsSHA256: strings.Repeat("b", 64)}
	valid := []string{
		"rights-reviewer", retrievedAt.Add(time.Hour).Format(time.RFC3339), "approved", "Executed schedule and every evidence bundle inspected.",
		"schedule-one", strings.Repeat("c", 64), fillercorpus.RightsStatusCleared, strings.Repeat("d", 64),
		"true", "true", "true", "true", "true",
		fillercorpus.RightsStatusNotPresent, fillercorpus.RightsStatusCleared, fillercorpus.RightsStatusNotPresent, fillercorpus.RightsStatusCleared, fillercorpus.RightsStatusCleared, fillercorpus.RightsStatusNotPresent, strings.Repeat("e", 64),
		fillercorpus.RedistributionExternalOnly, fillercorpus.RightsTerritoryWorldwide, fillercorpus.RightsTermPerpetualIrrevocable, "", fillercorpus.RightsWithdrawalDefectRetirement,
		"", "", "", "", "[]",
	}
	decision, err := parseHoldoutDecision(row, template, valid, lockedAt)
	if err != nil || decision.HoldoutContract == nil || len(decision.HoldoutContract.HoldReasons) != 0 || decision.Redistributable {
		t.Fatalf("decision = %+v, %v", decision, err)
	}
	tests := map[string]func([]string){
		"schedule digest mismatch": func(fields []string) { fields[5] = "bad" },
		"unknown signer authority": func(fields []string) { fields[6] = fillercorpus.RightsStatusUnknown },
		"provider grant missing":   func(fields []string) { fields[12] = "false" },
		"embedded rights conflict": func(fields []string) { fields[13] = fillercorpus.RightsStatusConflicting },
		"expired term": func(fields []string) {
			fields[22] = fillercorpus.RightsTermExpires
			fields[23] = lockedAt.Add(-time.Minute).Format(time.RFC3339)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fields := append([]string(nil), valid...)
			mutate(fields)
			if _, err := parseHoldoutDecision(row, template, fields, lockedAt); err == nil {
				t.Fatal("incomplete certification authority was approved")
			}
		})
	}
	t.Run("blank held schedule emits reasons", func(t *testing.T) {
		fields := append([]string(nil), valid...)
		fields[2] = "held"
		for index := 4; index < len(fields); index++ {
			fields[index] = ""
		}
		decision, err := parseHoldoutDecision(row, template, fields, lockedAt)
		if err != nil || decision.HoldoutContract == nil || len(decision.HoldoutContract.HoldReasons) == 0 {
			t.Fatalf("held decision = %+v, %v", decision, err)
		}
	})
}
