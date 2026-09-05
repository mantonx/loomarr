package fillerquarantine_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillerquarantine"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestRightsEligibilitySelectsOnlyInspectedEligibleNonLocalCases(t *testing.T) {
	inventory, raw := remoteRightsInventory(t, "eligible", "held", "absent")
	dispositions := map[string]string{
		inventory.Cases[0].CaseID: fillerquarantine.DispositionEligibleForRightsReview,
		inventory.Cases[1].CaseID: fillerquarantine.DispositionHold,
	}
	reportRaw := testkit.FillerQuarantineReport(t, raw, dispositions, nil)
	authority, err := fillerquarantine.OpenRightsEligibility(raw, reportRaw)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := authority.Selected(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Cases) != 1 || selection.Cases[0].Inventory.CaseID != inventory.Cases[0].CaseID || selection.Cases[0].QuarantineInspection == nil || selection.QuarantineInspection == nil {
		t.Fatalf("selection = %+v", selection)
	}
	if selection.Cases[0].QuarantineInspection.Report != *selection.QuarantineInspection {
		t.Fatal("case binding does not retain the selected report identity")
	}
}

func TestRightsEligibilityRequiresExactDecisionAndInspectedSourceBytes(t *testing.T) {
	inventory, raw := remoteRightsInventory(t, "eligible")
	content := fillercorpus.InventorySHA256([]byte("downloaded source"))
	reportRaw := testkit.FillerQuarantineReport(t, raw, map[string]string{inventory.Cases[0].CaseID: fillerquarantine.DispositionEligibleForRightsReview}, map[string]string{inventory.Cases[0].CaseID: content})
	authority, err := fillerquarantine.OpenRightsEligibility(raw, reportRaw)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := authority.Selected(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	decision := fillercorpus.RightsDecision{InventorySHA256: fillercorpus.InventorySHA256(raw), CaseID: inventory.Cases[0].CaseID, QuarantineInspection: selection.Cases[0].QuarantineInspection}
	if err := authority.Require(decision, content); err != nil {
		t.Fatal(err)
	}
	tampered := *selection.Cases[0].QuarantineInspection
	tampered.ContentSHA256 = strings.Repeat("e", 64)
	decision.QuarantineInspection = &tampered
	if err := authority.Require(decision, content); err == nil {
		t.Fatal("caller mutation changed the authority's private binding")
	}
	decision.QuarantineInspection = nil
	if err := authority.Require(decision, content); err == nil {
		t.Fatal("unbound decision was accepted")
	}
	decision.QuarantineInspection = selection.Cases[0].QuarantineInspection
	if err := authority.Require(decision, strings.Repeat("f", 64)); err == nil {
		t.Fatal("source bytes different from the inspected content were accepted")
	}
}

func TestRightsEligibilityRejectsMissingDriftedAndMalformedReports(t *testing.T) {
	inventory, raw := remoteRightsInventory(t, "eligible")
	reportRaw := testkit.FillerQuarantineReport(t, raw, map[string]string{inventory.Cases[0].CaseID: fillerquarantine.DispositionEligibleForRightsReview}, nil)
	for name, candidate := range map[string][]byte{
		"missing":   nil,
		"trailing":  append(append([]byte(nil), reportRaw...), []byte(" {}")...),
		"malformed": []byte(`{"schemaVersion":1,"unknown":true}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := fillerquarantine.OpenRightsEligibility(raw, candidate); err == nil {
				t.Fatal("invalid report was accepted")
			}
		})
	}
	var report fillerquarantine.Report
	if err := json.Unmarshal(reportRaw, &report); err != nil {
		t.Fatal(err)
	}
	report.Inputs.InventorySHA256 = strings.Repeat("f", 64)
	drifted, _ := json.Marshal(report)
	if _, err := fillerquarantine.OpenRightsEligibility(raw, drifted); err == nil {
		t.Fatal("report for another inventory was accepted")
	}
}

func TestRightsEligibilityPreservesDirectLocalLaneWithoutAReport(t *testing.T) {
	inventory, raw := localRightsInventory(t)
	authority, err := fillerquarantine.OpenRightsEligibility(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := authority.Selected(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if selection.QuarantineInspection != nil || selection.Cases[0].QuarantineInspection != nil {
		t.Fatalf("local selection invented inspection authority: %+v", selection)
	}
	decision := fillercorpus.RightsDecision{InventorySHA256: fillercorpus.InventorySHA256(raw), CaseID: inventory.Cases[0].CaseID}
	if err := authority.Require(decision, inventory.Cases[0].Representation.SHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := fillerquarantine.OpenRightsEligibility(raw, []byte(`{}`)); err == nil {
		t.Fatal("local-only inventory accepted an inapplicable report")
	}
}

func remoteRightsInventory(t *testing.T, ids ...string) (fillercorpus.Inventory, []byte) {
	t.Helper()
	snapshot := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	lane := fillercorpus.Lane{Authority: "archive.org/prelinger", MaxRequests: 10, RequestsUsed: 1, MaxResponseBytes: 10_000, ResponseBytes: 100, MaxPredictedMediaBytes: int64(len(ids)) * 1_000, PredictedMediaBytes: int64(len(ids)) * 100, MaxWallTimeMS: 1_000, WallTimeMS: 10}
	for _, id := range ids {
		lane.Cases = append(lane.Cases, fillercorpus.Candidate{
			ItemID: id, Title: id, RoleHints: []string{"commercial"}, ItemURL: "https://archive.org/details/" + id,
			MetadataURL: "https://archive.org/metadata/" + id, MetadataRetrievedAt: snapshot, MetadataSHA256: strings.Repeat("a", 64),
			RightsAssertions: []string{"CC0"}, Representation: fillercorpus.Representation{Name: id + ".mp4", URL: "https://archive.org/download/" + id + "/" + id + ".mp4", MIMEType: "video/mp4", Bytes: 100},
		})
	}
	inventory, err := fillercorpus.InventoryFromLane(lane, fillercorpus.LaneInventoryOptions{SnapshotAt: snapshot, Collection: "prelinger", AllowedMediaHosts: []string{"archive.org", ".archive.org"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	return inventory, raw
}

func localRightsInventory(t *testing.T) (fillercorpus.Inventory, []byte) {
	t.Helper()
	snapshot := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	authority, collection, role := "direct-license", "fixture", "commercial"
	captureID := fillercorpus.NewCaptureID(authority, collection, role)
	media := []byte("direct media")
	inventory := fillercorpus.Inventory{
		SchemaVersion: fillercorpus.InventorySchemaVersion, SnapshotAt: snapshot,
		Captures: []fillercorpus.Capture{{CaptureID: captureID, Transport: fillercorpus.TransportLocal, Authority: authority, Collection: collection, RoleHint: role, SnapshotAt: snapshot, MaxPredictedMediaBytes: int64(len(media)), PredictedMediaBytes: int64(len(media)), MaxWallTimeMS: 1_000}},
		Cases: []fillercorpus.InventoryCase{{
			CaseID: fillercorpus.CaseID(authority, "local"), CaptureIDs: []string{captureID}, Authority: authority, ItemID: "local", Title: "local", RoleHints: []string{role}, RightsAssertions: []string{"signed grant"},
			MetadataRetrievedAt: snapshot, MetadataSHA256: strings.Repeat("a", 64),
			Evidence:       []fillercorpus.InventoryEvidence{{Kind: "rights", Path: "rights.txt", Bytes: 1, SHA256: strings.Repeat("b", 64)}, {Kind: "provenance", Path: "provenance.txt", Bytes: 1, SHA256: strings.Repeat("c", 64)}},
			Representation: fillercorpus.InventoryRepresentation{Transport: fillercorpus.TransportLocal, Name: "local.mp4", Path: "local.mp4", MIMEType: "video/mp4", Bytes: int64(len(media)), SHA256: fillercorpus.InventorySHA256(media)},
		}},
	}
	raw, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fillercorpus.DecodeInventoryBytes(raw); err != nil {
		t.Fatal(err)
	}
	return inventory, raw
}
