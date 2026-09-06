package fillercorpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
)

type metRightsBatchInputs struct {
	inventory       Inventory
	worksheet       RightsWorksheet
	prescreen       MetRightsPrescreen
	inventorySHA256 string
	worksheetSHA256 string
	prescreenSHA256 string
}

func validateMetRightsBatchInputs(inventoryRaw, worksheetRaw, prescreenRaw []byte) (metRightsBatchInputs, error) {
	inventory, err := DecodeInventoryBytes(inventoryRaw)
	if err != nil {
		return metRightsBatchInputs{}, fmt.Errorf("met rights batch inventory: %w", err)
	}
	if len(inventory.Cases) == 0 || len(inventory.Cases) > 500 {
		return metRightsBatchInputs{}, fmt.Errorf("met rights batch inventory count is outside its ceiling")
	}
	for _, item := range inventory.Cases {
		if item.Authority != MetAuthority {
			return metRightsBatchInputs{}, fmt.Errorf("met rights batch cannot mix authority %q", item.Authority)
		}
	}
	inventorySHA256 := InventorySHA256(inventoryRaw)

	var worksheet RightsWorksheet
	if err := decodeMetRightsBatchJSON(worksheetRaw, &worksheet); err != nil {
		return metRightsBatchInputs{}, fmt.Errorf("decode Met rights batch worksheet: %w", err)
	}
	if worksheet.SchemaVersion != RightsWorksheetSchemaVersion || worksheet.Profile != RightsProfileDevelopment ||
		worksheet.InventorySHA256 != inventorySHA256 || worksheet.HoldoutTemplate != nil ||
		worksheet.SnapshotAt.IsZero() || worksheet.SnapshotAt.Location() != time.UTC || !worksheet.SnapshotAt.Equal(inventory.SnapshotAt) ||
		worksheet.PreparedAt.IsZero() || worksheet.PreparedAt.Location() != time.UTC || worksheet.PreparedAt.Before(worksheet.SnapshotAt) ||
		worksheet.MinItems <= 0 || worksheet.MaxItems < worksheet.MinItems || worksheet.MaxItems > 500 ||
		len(inventory.Cases) < worksheet.MinItems || len(inventory.Cases) > worksheet.MaxItems || len(worksheet.Cases) != len(inventory.Cases) {
		return metRightsBatchInputs{}, fmt.Errorf("met rights batch worksheet identity, profile, coverage, or time is invalid")
	}
	expectedRows := make([]RightsReviewRow, 0, len(inventory.Cases))
	for _, item := range inventory.Cases {
		expectedRows = append(expectedRows, RightsReviewRowFromCase(item))
	}
	sort.Slice(expectedRows, func(i, j int) bool {
		return sha256HexString(inventorySHA256+"/"+expectedRows[i].CaseID) < sha256HexString(inventorySHA256+"/"+expectedRows[j].CaseID)
	})
	for index, row := range worksheet.Cases {
		expected := expectedRows[index]
		expected.Rank, expected.InventorySHA256 = index+1, inventorySHA256
		if !reflect.DeepEqual(ImmutableRightsReviewRecord(row), ImmutableRightsReviewRecord(expected)) || row.Rank != index+1 ||
			row.InventorySHA256 != inventorySHA256 || row.ReviewerID != "" || row.ReviewedAt != "" || row.Decision != "" ||
			row.Basis != "" || row.Redistributable || row.RequiredCredit != "" || row.Restrictions == nil || len(row.Restrictions) != 0 {
			return metRightsBatchInputs{}, fmt.Errorf("met rights batch worksheet row %q is changed, reordered, or not inert", row.CaseID)
		}
	}

	var prescreen MetRightsPrescreen
	if err := decodeMetRightsBatchJSON(prescreenRaw, &prescreen); err != nil {
		return metRightsBatchInputs{}, fmt.Errorf("decode Met rights pre-screen: %w", err)
	}
	if prescreen.SchemaVersion != MetRightsPrescreenSchemaVersion || prescreen.InventorySHA256 != inventorySHA256 ||
		prescreen.PolicyEvidenceID != "met-open-access-metadata-prescreen-v1" || !IsSHA256(prescreen.PolicyEvidenceSHA256) ||
		!reflect.DeepEqual(prescreen.PolicySources, requiredMetPolicySources) || !reflect.DeepEqual(prescreen.Limitations, requiredMetPolicyLimitations) ||
		prescreen.PreparedAt.IsZero() || prescreen.PreparedAt.Location() != time.UTC || prescreen.PreparedAt.Before(worksheet.PreparedAt) ||
		prescreen.MinItems != worksheet.MinItems || prescreen.MaxItems != worksheet.MaxItems || prescreen.TotalCases != len(inventory.Cases) ||
		prescreen.PassedCases != len(inventory.Cases) || prescreen.HeldCases != 0 || !prescreen.CompleteCoverage ||
		prescreen.RightsApproval || prescreen.DownloadAuthority || prescreen.TruthAuthority || prescreen.TrainingAuthority ||
		prescreen.ProductionAuthority || prescreen.IngestionAuthority || prescreen.SchedulingAuthority || prescreen.BroadcastAuthority ||
		!reflect.DeepEqual(prescreen.Instructions, requiredMetRightsPrescreenInstructions) || len(prescreen.Cases) != len(inventory.Cases) {
		return metRightsBatchInputs{}, fmt.Errorf("met rights pre-screen is incomplete, changed, anomalous, or authority-bearing")
	}
	expectedCases := make([]MetRightsPrescreenCase, 0, len(inventory.Cases))
	for _, item := range inventory.Cases {
		expectedCases = append(expectedCases, MetRightsPrescreenCase{CaseID: item.CaseID, MetadataSHA256: item.MetadataSHA256, Status: metRightsPrescreenPass})
	}
	sort.Slice(expectedCases, func(i, j int) bool { return strings.Compare(expectedCases[i].CaseID, expectedCases[j].CaseID) < 0 })
	for index, actual := range prescreen.Cases {
		expected := expectedCases[index]
		if actual.CaseID != expected.CaseID || actual.MetadataSHA256 != expected.MetadataSHA256 || actual.Status != metRightsPrescreenPass || len(actual.ReasonCodes) != 0 {
			return metRightsBatchInputs{}, fmt.Errorf("met rights pre-screen cases do not exactly cover the frozen inventory")
		}
	}
	return metRightsBatchInputs{
		inventory: inventory, worksheet: worksheet, prescreen: prescreen,
		inventorySHA256: inventorySHA256, worksheetSHA256: InventorySHA256(worksheetRaw), prescreenSHA256: InventorySHA256(prescreenRaw),
	}, nil
}

func decodeMetRightsBatchJSON(raw []byte, value any) error {
	if len(raw) == 0 {
		return fmt.Errorf("empty JSON input")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}
