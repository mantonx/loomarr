// Command filler-corpus-rights-review prepares a deterministic, non-authorizing
// worksheet from a frozen source inventory. It never downloads media.
package main

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillerquarantine"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-corpus-rights-review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "frozen source inventory JSON")
	quarantineInspectionPath := flags.String("quarantine-inspection", "", "immutable quarantine-inspection report (development/certification non-local cases)")
	outputPath := flags.String("out", "", "non-authorizing rights worksheet JSON")
	csvOutputPath := flags.String("csv-out", "", "spreadsheet-safe non-authorizing rights worksheet CSV")
	preparedAtText := flags.String("prepared-at", "", "worksheet preparation time in RFC3339 format")
	profile := flags.String("profile", "", "required rights profile: quarantine, development, or certification")
	agreementID := flags.String("agreement-id", "", "maintainer/counsel-approved agreement identifier (certification only)")
	agreementSHA256 := flags.String("agreement-sha256", "", "SHA-256 of the approved agreement form (certification only)")
	processorID := flags.String("processor-id", "", "exact approved inference processor identifier (certification only)")
	processorTermsSHA256 := flags.String("processor-terms-sha256", "", "SHA-256 of approved processor terms snapshot (certification only)")
	minItems := flags.Int("min-items", 0, "required minimum worksheet cases")
	maxItems := flags.Int("max-items", 0, "hard worksheet case ceiling")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *inventoryPath == "" || *outputPath == "" || *preparedAtText == "" || *minItems <= 0 || *maxItems < *minItems || !fillercorpus.KnownRightsProfile(*profile) {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review: inventory, output, preparation time, explicit quarantine/development/certification profile, and positive min/max item bounds are required")
		return 2
	}
	preparedAt, err := time.Parse(time.RFC3339, *preparedAtText)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review: parse --prepared-at:", err)
		return 2
	}
	if err := requireNewOutputs(*outputPath, *csvOutputPath); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review:", err)
		return 1
	}
	raw, err := os.ReadFile(*inventoryPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review: read inventory:", err)
		return 1
	}
	inv, err := fillercorpus.DecodeInventoryBytes(raw)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review: decode inventory:", err)
		return 1
	}
	var template *fillercorpus.HoldoutRightsTemplate
	if *profile == fillercorpus.RightsProfileCertification {
		template = &fillercorpus.HoldoutRightsTemplate{AgreementID: *agreementID, AgreementSHA256: *agreementSHA256, ProcessorID: *processorID, ProcessorTermsSHA256: *processorTermsSHA256}
		if err := fillercorpus.ValidateHoldoutRightsTemplate(template); err != nil {
			_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review:", err)
			return 2
		}
	}
	var selection *fillerquarantine.RightsSelection
	if *profile != fillercorpus.RightsProfileQuarantine {
		var inspectionRaw []byte
		if *quarantineInspectionPath != "" {
			inspectionRaw, err = os.ReadFile(*quarantineInspectionPath)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review: read quarantine inspection:", err)
				return 1
			}
		}
		authority, openErr := fillerquarantine.OpenRightsEligibility(raw, inspectionRaw)
		if openErr != nil {
			_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review:", openErr)
			return 1
		}
		selected, selectErr := authority.Selected(*minItems, *maxItems)
		if selectErr != nil {
			_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review:", selectErr)
			return 1
		}
		selection = &selected
	} else if *quarantineInspectionPath != "" {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review: quarantine profile cannot consume a post-download inspection")
		return 2
	}
	result, err := prepareWorksheetForProfile(inv, sha256Hex(raw), preparedAt, *minItems, *maxItems, *profile, template, selection)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review:", err)
		return 1
	}
	if err := writeJSON(*outputPath, result); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review: write worksheet:", err)
		return 1
	}
	if *csvOutputPath != "" {
		if err := writeReviewCSV(*csvOutputPath, result); err != nil {
			_, _ = fmt.Fprintln(stderr, "filler-corpus-rights-review: write CSV worksheet:", err)
			return 1
		}
	}
	_, _ = fmt.Fprintf(stdout, "filler-corpus-rights-review: prepared %d inert review rows for inventory %s\n", len(result.Cases), result.InventorySHA256)
	return 0
}

func writeReviewCSV(path string, sheet fillercorpus.RightsWorksheet) error {
	return writeAtomic(path, func(writer io.Writer) error {
		csvWriter := csv.NewWriter(writer)
		header, err := fillercorpus.RightsReviewCSVHeaderForProfile(sheet.Profile)
		if err != nil {
			return err
		}
		if err := csvWriter.Write(header); err != nil {
			return err
		}
		for _, row := range sheet.Cases {
			immutable := fillercorpus.ImmutableRightsReviewRecordForProfile(row, sheet.Profile)
			record := append(immutable, make([]string, len(header)-len(immutable))...)
			if err := csvWriter.Write(record); err != nil {
				return err
			}
		}
		csvWriter.Flush()
		return csvWriter.Error()
	})
}

func prepareWorksheetForProfile(inv fillercorpus.Inventory, digest string, preparedAt time.Time, minItems, maxItems int, profile string, template *fillercorpus.HoldoutRightsTemplate, selection *fillerquarantine.RightsSelection) (fillercorpus.RightsWorksheet, error) {
	if failures := fillercorpus.ValidateInventory(inv); len(failures) != 0 || preparedAt.Before(inv.SnapshotAt) {
		return fillercorpus.RightsWorksheet{}, fmt.Errorf("inventory identity or worksheet time is invalid")
	}
	schemaVersion, knownProfile := fillercorpus.RightsWorksheetSchemaForProfile(profile)
	if !knownProfile {
		return fillercorpus.RightsWorksheet{}, fmt.Errorf("unknown rights profile %q", profile)
	}
	if profile == fillercorpus.RightsProfileCertification {
		if err := fillercorpus.ValidateHoldoutRightsTemplate(template); err != nil {
			return fillercorpus.RightsWorksheet{}, err
		}
	} else if template != nil {
		return fillercorpus.RightsWorksheet{}, fmt.Errorf("rights profile and holdout template are inconsistent")
	}
	var cases []fillerquarantine.EligibleRightsCase
	if profile == fillercorpus.RightsProfileQuarantine {
		if selection != nil {
			return fillercorpus.RightsWorksheet{}, fmt.Errorf("quarantine worksheet cannot consume post-download eligibility")
		}
		if len(inv.Cases) < minItems {
			return fillercorpus.RightsWorksheet{}, fmt.Errorf("inventory has %d cases; minimum is %d", len(inv.Cases), minItems)
		}
		for _, item := range inv.Cases {
			cases = append(cases, fillerquarantine.EligibleRightsCase{Inventory: item})
		}
		// Pre-download quarantine preserves its existing inventory order and
		// deterministic digest shuffle.
		sortRightsCases(cases, digest)
		if len(cases) > maxItems {
			cases = cases[:maxItems]
		}
	} else {
		if selection == nil {
			return fillercorpus.RightsWorksheet{}, fmt.Errorf("development and certification worksheets require validated quarantine eligibility")
		}
		cases = selection.Cases
	}
	result := fillercorpus.RightsWorksheet{
		SchemaVersion: schemaVersion, Profile: profile, InventorySHA256: digest,
		SnapshotAt: inv.SnapshotAt.UTC(), PreparedAt: preparedAt.UTC(), MinItems: minItems, MaxItems: maxItems,
		HoldoutTemplate: template, QuarantineInspection: selectionReportBinding(selection),
		Instructions: []string{
			"This worksheet is not download authority; blank rows fail closed.",
			"Review the exact item, metadata, selected file, license assertion, rights prose, embedded material, attribution, and non-copyright restrictions.",
			"Set decision to approved only with explicit redistributable=true and a reasoned basis; otherwise set decision to held.",
		},
	}
	switch profile {
	case fillercorpus.RightsProfileCertification:
		result.Instructions = []string{
			"This worksheet is not legal, outreach, acquisition, download, provider-transfer, or spend authority; blank rows fail closed.",
			"Review the exact executed per-master schedule, signer authority, every required grant, embedded rights category, territory, term, attribution, restriction, and any ambiguity adjudication.",
			"Set decision to approved only when every certification field is complete and supported by the agreement and evidence digests; otherwise set decision to held with the unresolved facts preserved.",
		}
	case fillercorpus.RightsProfileQuarantine:
		result.Instructions = []string{
			"This worksheet is not legal, provider-transfer, redistribution, corpus-preparation, training, ingestion, scheduling, production, or spend authority; blank rows fail closed.",
			"Review the exact item, metadata, selected representation, source terms, and local copy/inspection basis.",
			"Approve only local copying/storage and local technical inspection; every downstream permission must be explicitly false.",
		}
	}
	seen := map[string]struct{}{}
	for index, candidate := range cases {
		item := candidate.Inventory
		if _, exists := seen[item.CaseID]; exists {
			return fillercorpus.RightsWorksheet{}, fmt.Errorf("duplicate candidate %s", item.CaseID)
		}
		seen[item.CaseID] = struct{}{}
		row := fillercorpus.RightsReviewRowFromCase(item)
		row.Rank, row.InventorySHA256 = index+1, digest
		row.QuarantineInspection = candidate.QuarantineInspection
		result.Cases = append(result.Cases, row)
	}
	return result, nil
}

func sortRightsCases(cases []fillerquarantine.EligibleRightsCase, digest string) {
	sort.Slice(cases, func(i, j int) bool {
		return sha256Hex([]byte(digest+"/"+cases[i].Inventory.CaseID)) < sha256Hex([]byte(digest+"/"+cases[j].Inventory.CaseID))
	})
}

func selectionReportBinding(selection *fillerquarantine.RightsSelection) *fillercorpus.QuarantineInspectionBinding {
	if selection == nil || selection.QuarantineInspection == nil {
		return nil
	}
	value := *selection.QuarantineInspection
	return &value
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, func(writer io.Writer) error {
		_, err := writer.Write(append(data, '\n'))
		return err
	})
}

func writeAtomic(path string, write func(io.Writer) error) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(absolute), ".filler-corpus-rights-review-*")
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
	if err := write(temp); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Link(tempName, absolute); err != nil {
		return fmt.Errorf("publish immutable rights worksheet: %w", err)
	}
	if err := os.Remove(tempName); err != nil {
		_ = os.Remove(absolute)
		return err
	}
	ok = true
	return nil
}

func requireNewOutputs(paths ...string) error {
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve output path: %w", err)
		}
		if _, duplicate := seen[absolute]; duplicate {
			return fmt.Errorf("worksheet outputs must use distinct paths")
		}
		seen[absolute] = struct{}{}
		_, err = os.Lstat(absolute)
		switch {
		case err == nil:
			return fmt.Errorf("worksheet output already exists: %s", absolute)
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return fmt.Errorf("inspect worksheet output: %w", err)
		}
	}
	return nil
}
