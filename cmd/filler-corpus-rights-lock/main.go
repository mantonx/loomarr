// Command filler-corpus-rights-lock validates a completed spreadsheet review
// against its inert JSON worksheet and emits downloader-compatible JSONL.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillerquarantine"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-corpus-rights-lock", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "frozen source inventory JSON")
	quarantineInspectionPath := flags.String("quarantine-inspection", "", "immutable quarantine-inspection report (development/certification non-local cases)")
	worksheetPath := flags.String("worksheet", "", "inert rights worksheet JSON")
	csvPath := flags.String("completed-csv", "", "completed rights review CSV")
	outputPath := flags.String("approvals-out", "", "validated rights decisions JSONL")
	lockedAtText := flags.String("locked-at", "", "decision lock time in RFC3339 format")
	profile := flags.String("profile", "", "required rights profile: quarantine, development, or certification")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *inventoryPath == "" || *worksheetPath == "" || *csvPath == "" || *outputPath == "" || *lockedAtText == "" || !fillercorpus.KnownRightsProfile(*profile) {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-lock: inventory, worksheet, completed CSV, approvals output, lock time, and explicit quarantine/development/certification profile are required")
		return 2
	}
	lockedAt, err := time.Parse(time.RFC3339, *lockedAtText)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-lock: parse --locked-at:", err)
		return 2
	}
	if err := requireNewApprovals(*outputPath); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-lock:", err)
		return 1
	}
	decisions, err := lockDecisionsForProfile(*inventoryPath, *worksheetPath, *csvPath, lockedAt, *profile, *quarantineInspectionPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-lock:", err)
		return 1
	}
	if err := writeJSONL(*outputPath, decisions); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-lock: write approvals:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-corpus-rights-lock: locked %d item-level rights decisions for inventory %s\n", len(decisions), decisions[0].InventorySHA256)
	return 0
}

func lockDecisionsForProfile(inventoryPath, worksheetPath, csvPath string, lockedAt time.Time, profile string, inspectionPaths ...string) ([]fillercorpus.RightsDecision, error) {
	if len(inspectionPaths) > 1 {
		return nil, fmt.Errorf("at most one quarantine inspection path is permitted")
	}
	inspectionPath := ""
	if len(inspectionPaths) == 1 {
		inspectionPath = inspectionPaths[0]
	}
	inventoryRaw, err := os.ReadFile(inventoryPath)
	if err != nil {
		return nil, fmt.Errorf("read inventory: %w", err)
	}
	inventoryDigest := fmt.Sprintf("%x", sha256.Sum256(inventoryRaw))
	inv, err := fillercorpus.DecodeInventoryBytes(inventoryRaw)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(worksheetPath)
	if err != nil {
		return nil, fmt.Errorf("read worksheet: %w", err)
	}
	sheet, err := decodeWorksheet(raw)
	if err != nil {
		return nil, fmt.Errorf("decode worksheet: %w", err)
	}
	expectedSchema, knownProfile := fillercorpus.RightsWorksheetSchemaForProfile(profile)
	if !knownProfile {
		return nil, fmt.Errorf("unknown rights profile %q", profile)
	}
	profileMatches := sheet.Profile == profile
	if sheet.SchemaVersion != expectedSchema || !profileMatches || len(sheet.Cases) == 0 || sheet.InventorySHA256 != inventoryDigest ||
		!sheet.SnapshotAt.Equal(inv.SnapshotAt) ||
		sheet.PreparedAt.Before(sheet.SnapshotAt) || sheet.MinItems <= 0 || sheet.MaxItems < sheet.MinItems {
		return nil, fmt.Errorf("worksheet identity is invalid")
	}
	if profile == fillercorpus.RightsProfileCertification {
		if err := fillercorpus.ValidateHoldoutRightsTemplate(sheet.HoldoutTemplate); err != nil {
			return nil, err
		}
	} else if sheet.HoldoutTemplate != nil {
		return nil, fmt.Errorf("non-certification worksheet cannot contain a holdout contract template")
	}
	expectedCases := make([]fillercorpus.RightsReviewRow, 0, len(inv.Cases))
	expectedInspectionByCase := make(map[string]*fillercorpus.QuarantineInspectionCaseBinding, len(inv.Cases))
	if profile == fillercorpus.RightsProfileQuarantine {
		if inspectionPath != "" || sheet.QuarantineInspection != nil {
			return nil, fmt.Errorf("quarantine profile cannot consume a post-download inspection")
		}
		for _, item := range inv.Cases {
			expectedCases = append(expectedCases, fillercorpus.RightsReviewRowFromCase(item))
		}
		sort.Slice(expectedCases, func(i, j int) bool {
			return sha256Hex(inventoryDigest+"/"+expectedCases[i].CaseID) < sha256Hex(inventoryDigest+"/"+expectedCases[j].CaseID)
		})
		if len(expectedCases) > sheet.MaxItems {
			expectedCases = expectedCases[:sheet.MaxItems]
		}
		if len(expectedCases) < sheet.MinItems {
			return nil, fmt.Errorf("worksheet selection is below its minimum")
		}
	} else {
		var inspectionRaw []byte
		if inspectionPath != "" {
			inspectionRaw, err = os.ReadFile(inspectionPath)
			if err != nil {
				return nil, fmt.Errorf("read quarantine inspection: %w", err)
			}
		}
		authority, openErr := fillerquarantine.OpenRightsEligibility(inventoryRaw, inspectionRaw)
		if openErr != nil {
			return nil, openErr
		}
		selection, selectErr := authority.Selected(sheet.MinItems, sheet.MaxItems)
		if selectErr != nil {
			return nil, selectErr
		}
		if !reflect.DeepEqual(sheet.QuarantineInspection, selection.QuarantineInspection) {
			return nil, fmt.Errorf("worksheet does not bind the exact quarantine inspection")
		}
		for _, candidate := range selection.Cases {
			row := fillercorpus.RightsReviewRowFromCase(candidate.Inventory)
			row.QuarantineInspection = candidate.QuarantineInspection
			expectedCases = append(expectedCases, row)
			expectedInspectionByCase[candidate.Inventory.CaseID] = candidate.QuarantineInspection
		}
	}
	if len(sheet.Cases) != len(expectedCases) {
		return nil, fmt.Errorf("worksheet selection does not match its frozen inventory and bounds")
	}
	file, err := os.Open(csvPath)
	if err != nil {
		return nil, fmt.Errorf("read completed CSV: %w", err)
	}
	defer func() { _ = file.Close() }()
	reader := csv.NewReader(file)
	header, err := fillercorpus.RightsReviewCSVHeaderForProfile(profile)
	if err != nil {
		return nil, err
	}
	reader.FieldsPerRecord = len(header)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("decode completed CSV: %w", err)
	}
	if len(records) == 0 || !reflect.DeepEqual(records[0], header) {
		return nil, fmt.Errorf("completed CSV header does not match the worksheet contract")
	}
	byID := make(map[string]fillercorpus.RightsReviewRow, len(sheet.Cases))
	for index, row := range sheet.Cases {
		if row.InventorySHA256 != sheet.InventorySHA256 || row.CaseID == "" || row.MetadataSHA256 == "" || row.MetadataRetrievedAt.IsZero() {
			return nil, fmt.Errorf("worksheet row %q has incomplete frozen identity", row.CaseID)
		}
		if _, duplicate := byID[row.CaseID]; duplicate {
			return nil, fmt.Errorf("duplicate worksheet row %s", row.CaseID)
		}
		if profile == fillercorpus.RightsProfileQuarantine && row.QuarantineInspection != nil {
			return nil, fmt.Errorf("quarantine worksheet row %q cannot contain post-download authority", row.CaseID)
		}
		item := expectedCases[index]
		item.Rank = index + 1
		item.InventorySHA256 = inventoryDigest
		if row.Rank != index+1 || !reflect.DeepEqual(fillercorpus.ImmutableRightsReviewRecordForProfile(row, profile), fillercorpus.ImmutableRightsReviewRecordForProfile(item, profile)) {
			return nil, fmt.Errorf("worksheet row %s changes the deterministic frozen selection", row.CaseID)
		}
		byID[row.CaseID] = row
	}
	seen := make(map[string]struct{}, len(sheet.Cases))
	decisions := make([]fillercorpus.RightsDecision, 0, len(sheet.Cases))
	for index, record := range records[1:] {
		caseID := record[2]
		row, ok := byID[caseID]
		if !ok {
			return nil, fmt.Errorf("CSV row %d item %q is absent from the worksheet", index+2, caseID)
		}
		if _, duplicate := seen[caseID]; duplicate {
			return nil, fmt.Errorf("duplicate completed review row %s", caseID)
		}
		seen[caseID] = struct{}{}
		expected := fillercorpus.ImmutableRightsReviewRecordForProfile(row, profile)
		if !reflect.DeepEqual(record[:len(expected)], expected) {
			return nil, fmt.Errorf("completed review row %s changes immutable worksheet fields", caseID)
		}
		fields := record[len(expected):]
		var decision fillercorpus.RightsDecision
		switch profile {
		case fillercorpus.RightsProfileQuarantine:
			decision, err = parseQuarantineDecision(row, fields, lockedAt)
		case fillercorpus.RightsProfileCertification:
			decision, err = parseHoldoutDecision(row, sheet.HoldoutTemplate, fields, lockedAt)
		default:
			decision, err = parseDecision(row, fields, lockedAt)
		}
		if err != nil {
			return nil, fmt.Errorf("completed review row %s: %w", caseID, err)
		}
		if binding := expectedInspectionByCase[caseID]; binding != nil {
			value := *binding
			decision.QuarantineInspection = &value
		}
		if profile != fillercorpus.RightsProfileQuarantine {
			decision.WorksheetSchemaVersion = sheet.SchemaVersion
		}
		decisions = append(decisions, decision)
	}
	if len(seen) != len(sheet.Cases) {
		return nil, fmt.Errorf("completed CSV covers %d of %d worksheet rows", len(seen), len(sheet.Cases))
	}
	return decisions, nil
}

func parseQuarantineDecision(row fillercorpus.RightsReviewRow, fields []string, lockedAt time.Time) (fillercorpus.RightsDecision, error) {
	if len(fields) != 15 {
		return fillercorpus.RightsDecision{}, fmt.Errorf("quarantine review has %d fields; want 15", len(fields))
	}
	reviewerID := strings.TrimSpace(fields[0])
	reviewedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[1]))
	if reviewerID == "" || err != nil || reviewedAt.Before(row.MetadataRetrievedAt) || reviewedAt.After(lockedAt) {
		return fillercorpus.RightsDecision{}, fmt.Errorf("reviewer and review time must bind a completed review to the frozen metadata")
	}
	decision := strings.TrimSpace(fields[2])
	if decision != "approved" && decision != "held" {
		return fillercorpus.RightsDecision{}, fmt.Errorf("decision must be approved or held")
	}
	basis := strings.TrimSpace(fields[3])
	if basis == "" {
		return fillercorpus.RightsDecision{}, fmt.Errorf("a reasoned basis is required")
	}
	values := make([]bool, 9)
	for index := range values {
		value, parseErr := strconv.ParseBool(strings.TrimSpace(fields[4+index]))
		if parseErr != nil {
			return fillercorpus.RightsDecision{}, fmt.Errorf("%s must be explicitly true or false", fillercorpus.QuarantineRightsReviewCSVHeader()[len(fillercorpus.ImmutableRightsReviewRecord(row))+4+index])
		}
		values[index] = value
	}
	contract := &fillercorpus.QuarantineAcquisitionContract{
		SchemaVersion:  fillercorpus.QuarantineAcquisitionContractSchemaVersion,
		Purpose:        fillercorpus.QuarantinePurposeLocalInspection,
		CopyAndStorage: values[0], LocalTechnicalInspection: values[1], ProviderTransfer: values[2],
		Redistribution: values[3], CorpusPreparation: values[4], Training: values[5],
		CatalogIngestion: values[6], Scheduling: values[7], ProductionAdmission: values[8],
	}
	contract.HoldReasons = fillercorpus.QuarantineAcquisitionHoldReasons(contract)
	if decision == "approved" && len(contract.HoldReasons) != 0 {
		return fillercorpus.RightsDecision{}, fmt.Errorf("approval is held by: %s", strings.Join(contract.HoldReasons, ", "))
	}
	if decision == "held" {
		if slices.Contains(values, true) {
			return fillercorpus.RightsDecision{}, fmt.Errorf("held rows cannot grant quarantine or downstream authority")
		}
		if len(contract.HoldReasons) == 0 {
			contract.HoldReasons = []string{"reviewer_hold"}
		}
	}
	requiredCredit := strings.TrimSpace(fields[13])
	if decision == "approved" && requiresCredit(row.LicenseURL) && requiredCredit == "" {
		return fillercorpus.RightsDecision{}, fmt.Errorf("the asserted license requires attribution")
	}
	var restrictions []string
	if err := json.Unmarshal([]byte(fields[14]), &restrictions); err != nil {
		return fillercorpus.RightsDecision{}, fmt.Errorf("restrictions_json must be a JSON string array")
	}
	return fillercorpus.RightsDecision{
		InventorySHA256: row.InventorySHA256, CaseID: row.CaseID, CaptureIDs: append([]string(nil), row.CaptureIDs...), Authority: row.Authority, ItemID: row.ItemID, MetadataSHA256: row.MetadataSHA256,
		ReviewerID: reviewerID, ReviewedAt: reviewedAt.UTC(), Decision: decision, Basis: basis, Redistributable: false,
		RequiredCredit: requiredCredit, Restrictions: restrictions, QuarantineContract: contract,
	}, nil
}

func parseHoldoutDecision(row fillercorpus.RightsReviewRow, template *fillercorpus.HoldoutRightsTemplate, fields []string, lockedAt time.Time) (fillercorpus.RightsDecision, error) {
	if len(fields) != 30 {
		return fillercorpus.RightsDecision{}, fmt.Errorf("certification schedule has %d fields; want 30", len(fields))
	}
	reviewerID := strings.TrimSpace(fields[0])
	reviewedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[1]))
	if reviewerID == "" || err != nil || reviewedAt.Before(row.MetadataRetrievedAt) || reviewedAt.After(lockedAt) {
		return fillercorpus.RightsDecision{}, fmt.Errorf("reviewer and review time must bind a completed review to the frozen metadata")
	}
	decision := strings.TrimSpace(fields[2])
	if decision != "approved" && decision != "held" {
		return fillercorpus.RightsDecision{}, fmt.Errorf("decision must be approved or held")
	}
	basis := strings.TrimSpace(fields[3])
	if basis == "" {
		return fillercorpus.RightsDecision{}, fmt.Errorf("a reasoned basis is required")
	}
	parseBool := func(index int) (bool, error) {
		raw := strings.TrimSpace(fields[index])
		if raw == "" {
			return false, nil
		}
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return false, fmt.Errorf("%s must be true or false", fillercorpus.HoldoutRightsReviewCSVHeader()[len(fillercorpus.ImmutableRightsReviewRecord(row))+index])
		}
		return value, nil
	}
	grants := fillercorpus.HoldoutRightsGrants{}
	grantTargets := []*bool{&grants.CommercialEvaluation, &grants.CopyAndStorage, &grants.TechnicalModification, &grants.EvidenceExtraction, &grants.ProviderTransfer}
	for offset, target := range grantTargets {
		value, parseErr := parseBool(8 + offset)
		if parseErr != nil {
			return fillercorpus.RightsDecision{}, parseErr
		}
		*target = value
	}
	parseOptionalTime := func(value string) (*time.Time, error) {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, nil
		}
		parsed, parseErr := time.Parse(time.RFC3339, value)
		if parseErr != nil {
			return nil, parseErr
		}
		parsed = parsed.UTC()
		return &parsed, nil
	}
	expiresAt, err := parseOptionalTime(fields[23])
	if err != nil {
		return fillercorpus.RightsDecision{}, fmt.Errorf("expires_at must be blank or RFC3339")
	}
	adjudicatedAt, err := parseOptionalTime(fields[26])
	if err != nil {
		return fillercorpus.RightsDecision{}, fmt.Errorf("adjudicated_at must be blank or RFC3339")
	}
	var restrictions []string
	if strings.TrimSpace(fields[29]) != "" {
		err = json.Unmarshal([]byte(fields[29]), &restrictions)
	}
	if err != nil {
		return fillercorpus.RightsDecision{}, fmt.Errorf("restrictions_json must be a JSON string array")
	}
	contract := &fillercorpus.HoldoutRightsContract{
		SchemaVersion: fillercorpus.HoldoutRightsContractSchemaVersion,
		AgreementID:   template.AgreementID, AgreementSHA256: template.AgreementSHA256,
		ScheduleID: strings.TrimSpace(fields[4]), ScheduleSHA256: strings.TrimSpace(fields[5]),
		SignerAuthorityStatus: strings.TrimSpace(fields[6]), SignerAuthorityEvidenceSHA256: strings.TrimSpace(fields[7]),
		ProcessorID: template.ProcessorID, ProcessorTermsSHA256: template.ProcessorTermsSHA256, Grants: grants,
		EmbeddedRights:               fillercorpus.EmbeddedRightsStatus{Music: strings.TrimSpace(fields[13]), PerformersAndVoices: strings.TrimSpace(fields[14]), StockAndArtwork: strings.TrimSpace(fields[15]), Trademarks: strings.TrimSpace(fields[16]), PrivacyAndPublicity: strings.TrimSpace(fields[17]), Locations: strings.TrimSpace(fields[18])},
		EmbeddedRightsEvidenceSHA256: strings.TrimSpace(fields[19]), RedistributionScope: strings.TrimSpace(fields[20]), Territory: strings.TrimSpace(fields[21]), Term: strings.TrimSpace(fields[22]), ExpiresAt: expiresAt, Withdrawal: strings.TrimSpace(fields[24]),
		AdjudicatorID: strings.TrimSpace(fields[25]), AdjudicatedAt: adjudicatedAt, AdjudicationDisposition: strings.TrimSpace(fields[27]),
	}
	contract.HoldReasons = fillercorpus.HoldoutRightsHoldReasons(contract, lockedAt)
	if decision == "approved" && len(contract.HoldReasons) != 0 {
		return fillercorpus.RightsDecision{}, fmt.Errorf("approval is held by: %s", strings.Join(contract.HoldReasons, ", "))
	}
	if decision == "held" && len(contract.HoldReasons) == 0 {
		contract.HoldReasons = []string{"reviewer_hold"}
	}
	redistributable := contract.RedistributionScope == fillercorpus.RedistributionMasterAndDerivatives
	requiredCredit := strings.TrimSpace(fields[28])
	if decision == "approved" && requiresCredit(row.LicenseURL) && requiredCredit == "" {
		return fillercorpus.RightsDecision{}, fmt.Errorf("the asserted license requires attribution")
	}
	return fillercorpus.RightsDecision{
		InventorySHA256: row.InventorySHA256, CaseID: row.CaseID, CaptureIDs: append([]string(nil), row.CaptureIDs...), Authority: row.Authority, ItemID: row.ItemID, MetadataSHA256: row.MetadataSHA256,
		ReviewerID: reviewerID, ReviewedAt: reviewedAt.UTC(), Decision: decision, Basis: basis, Redistributable: redistributable, RequiredCredit: requiredCredit, Restrictions: restrictions, HoldoutContract: contract,
	}, nil
}

func sha256Hex(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func parseDecision(row fillercorpus.RightsReviewRow, fields []string, lockedAt time.Time) (fillercorpus.RightsDecision, error) {
	reviewerID := strings.TrimSpace(fields[0])
	reviewedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[1]))
	if reviewerID == "" || err != nil || reviewedAt.Before(row.MetadataRetrievedAt) || reviewedAt.After(lockedAt) {
		return fillercorpus.RightsDecision{}, fmt.Errorf("reviewer and review time must bind a completed review to the frozen metadata")
	}
	decision := strings.TrimSpace(fields[2])
	if decision != "approved" && decision != "held" {
		return fillercorpus.RightsDecision{}, fmt.Errorf("decision must be approved or held")
	}
	basis := strings.TrimSpace(fields[3])
	if basis == "" {
		return fillercorpus.RightsDecision{}, fmt.Errorf("a reasoned basis is required")
	}
	redistributable, err := strconv.ParseBool(strings.TrimSpace(fields[4]))
	if err != nil {
		return fillercorpus.RightsDecision{}, fmt.Errorf("redistributable must be true or false")
	}
	requiredCredit := strings.TrimSpace(fields[5])
	var restrictions []string
	if err := json.Unmarshal([]byte(fields[6]), &restrictions); err != nil {
		return fillercorpus.RightsDecision{}, fmt.Errorf("restrictions_json must be a JSON string array")
	}
	if decision == "approved" && !redistributable {
		return fillercorpus.RightsDecision{}, fmt.Errorf("approved rows must explicitly be redistributable")
	}
	if decision == "held" && redistributable {
		return fillercorpus.RightsDecision{}, fmt.Errorf("held rows cannot grant redistribution authority")
	}
	if decision == "approved" && requiresCredit(row.LicenseURL) && requiredCredit == "" {
		return fillercorpus.RightsDecision{}, fmt.Errorf("the asserted license requires attribution")
	}
	return fillercorpus.RightsDecision{
		InventorySHA256: row.InventorySHA256, CaseID: row.CaseID, CaptureIDs: append([]string(nil), row.CaptureIDs...), Authority: row.Authority, ItemID: row.ItemID, MetadataSHA256: row.MetadataSHA256,
		ReviewerID: reviewerID, ReviewedAt: reviewedAt.UTC(), Decision: decision, Basis: basis,
		Redistributable: redistributable, RequiredCredit: requiredCredit, Restrictions: restrictions,
	}, nil
}

func requiresCredit(licenseURL string) bool {
	value := strings.ToLower(licenseURL)
	return strings.Contains(value, "/licenses/by/") || strings.Contains(value, "/licenses/by-sa/")
}

func decodeWorksheet(raw []byte) (fillercorpus.RightsWorksheet, error) {
	var sheet fillercorpus.RightsWorksheet
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sheet); err != nil {
		return sheet, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("trailing JSON value")
		}
		return sheet, err
	}
	return sheet, nil
}

func requireNewApprovals(path string) error {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return fmt.Errorf("approvals output already exists")
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("inspect approvals output: %w", err)
	}
}

func writeJSONL(path string, decisions []fillercorpus.RightsDecision) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(absolute), ".filler-corpus-rights-lock-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempName)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	encoder := json.NewEncoder(temp)
	encoder.SetEscapeHTML(false)
	for _, decision := range decisions {
		if err := encoder.Encode(decision); err != nil {
			return err
		}
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(tempName, absolute); err != nil {
		return fmt.Errorf("publish immutable rights decisions: %w", err)
	}
	if err := os.Remove(tempName); err != nil {
		_ = os.Remove(absolute)
		return err
	}
	ok = true
	return nil
}
