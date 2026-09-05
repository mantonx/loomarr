package fillerquarantine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

// RightsEligibility is the validated quarantine authority used by review,
// lock, and preparation. Its indexes are deliberately private so callers
// cannot reinterpret a report disposition or bypass transport policy.
type RightsEligibility struct {
	inventorySHA256 string
	reportBinding   *fillercorpus.QuarantineInspectionBinding
	cases           []EligibleRightsCase
	byCase          map[string]EligibleRightsCase
}

// EligibleRightsCase is one deterministic review candidate. Direct local
// media has no post-download inspection binding; every non-local case does.
type EligibleRightsCase struct {
	Inventory            fillercorpus.InventoryCase
	QuarantineInspection *fillercorpus.QuarantineInspectionCaseBinding
}

// RightsSelection freezes the selected cases and the report identity that a
// worksheet must retain. QuarantineInspection is nil only for local-only
// selection.
type RightsSelection struct {
	QuarantineInspection *fillercorpus.QuarantineInspectionBinding
	Cases                []EligibleRightsCase
}

// OpenRightsEligibility strictly decodes the exact inventory and inspection
// report, validates their identity relationship, and builds the private case
// index used by every later authority transition. inspectionRaw may be absent
// only when the inventory has no non-local case.
func OpenRightsEligibility(inventoryRaw, inspectionRaw []byte) (RightsEligibility, error) {
	inventory, err := fillercorpus.DecodeInventoryBytes(inventoryRaw)
	if err != nil {
		return RightsEligibility{}, err
	}
	inventorySHA256 := fillercorpus.InventorySHA256(inventoryRaw)
	nonLocal := false
	inventoryByCase := make(map[string]fillercorpus.InventoryCase, len(inventory.Cases))
	for _, item := range inventory.Cases {
		inventoryByCase[item.CaseID] = item
		if item.Representation.Transport != fillercorpus.TransportLocal {
			nonLocal = true
		}
	}
	if !nonLocal {
		if len(bytes.TrimSpace(inspectionRaw)) != 0 {
			return RightsEligibility{}, fmt.Errorf("quarantine inspection is invalid for a local-only inventory")
		}
		cases := make([]EligibleRightsCase, 0, len(inventory.Cases))
		byCase := make(map[string]EligibleRightsCase, len(inventory.Cases))
		for _, item := range inventory.Cases {
			candidate := EligibleRightsCase{Inventory: item}
			cases = append(cases, candidate)
			byCase[item.CaseID] = candidate
		}
		return RightsEligibility{inventorySHA256: inventorySHA256, cases: cases, byCase: byCase}, nil
	}
	if len(bytes.TrimSpace(inspectionRaw)) == 0 {
		return RightsEligibility{}, fmt.Errorf("quarantine inspection is required for non-local inventory cases")
	}

	report, err := decodeReport(inspectionRaw)
	if err != nil {
		return RightsEligibility{}, err
	}
	if err := Validate(report); err != nil {
		return RightsEligibility{}, fmt.Errorf("validate quarantine inspection: %w", err)
	}
	if report.Inputs.InventorySHA256 != inventorySHA256 {
		return RightsEligibility{}, fmt.Errorf("quarantine inspection inventory identity does not match")
	}
	binding := fillercorpus.QuarantineInspectionBinding{
		ReportSHA256:              fillercorpus.InventorySHA256(inspectionRaw),
		InventorySHA256:           report.Inputs.InventorySHA256,
		DownloadLedgerSHA256:      report.Inputs.DownloadLedgerSHA256,
		PriorPublicManifestSHA256: report.Inputs.PriorPublicManifestSHA256,
		PriorAuthoritySHA256:      report.Inputs.PriorAuthoritySHA256,
	}
	reportCases := make(map[string]Case, len(report.Cases))
	for _, inspected := range report.Cases {
		item, exists := inventoryByCase[inspected.CaseID]
		if !exists {
			return RightsEligibility{}, fmt.Errorf("quarantine inspection contains unknown case %q", inspected.CaseID)
		}
		if item.Representation.Transport == fillercorpus.TransportLocal {
			return RightsEligibility{}, fmt.Errorf("quarantine inspection contains direct local case %q", inspected.CaseID)
		}
		reportCases[inspected.CaseID] = inspected
	}

	cases := make([]EligibleRightsCase, 0, len(inventory.Cases))
	byCase := make(map[string]EligibleRightsCase, len(inventory.Cases))
	for _, item := range inventory.Cases {
		candidate := EligibleRightsCase{Inventory: item}
		if item.Representation.Transport != fillercorpus.TransportLocal {
			inspected, exists := reportCases[item.CaseID]
			if !exists || inspected.Disposition != DispositionEligibleForRightsReview || len(inspected.HoldReasons) != 0 {
				continue
			}
			caseBinding := fillercorpus.QuarantineInspectionCaseBinding{Report: binding, ContentSHA256: inspected.ContentSHA256}
			candidate.QuarantineInspection = &caseBinding
		}
		cases = append(cases, candidate)
		byCase[item.CaseID] = candidate
	}
	return RightsEligibility{inventorySHA256: inventorySHA256, reportBinding: &binding, cases: cases, byCase: byCase}, nil
}

// Selected returns the exact deterministic worksheet selection. The ordering
// remains stable across review and lock, including when maxItems truncates it.
func (authority RightsEligibility) Selected(minItems, maxItems int) (RightsSelection, error) {
	if minItems <= 0 || maxItems < minItems {
		return RightsSelection{}, fmt.Errorf("positive rights-selection bounds are required")
	}
	if len(authority.cases) < minItems {
		return RightsSelection{}, fmt.Errorf("quarantine eligibility has %d cases; minimum is %d", len(authority.cases), minItems)
	}
	cases := make([]EligibleRightsCase, 0, len(authority.cases))
	for _, candidate := range authority.cases {
		cases = append(cases, cloneEligibleRightsCase(candidate))
	}
	slices.SortFunc(cases, func(a, b EligibleRightsCase) int {
		left := fillercorpus.InventorySHA256([]byte(authority.inventorySHA256 + "/" + a.Inventory.CaseID))
		right := fillercorpus.InventorySHA256([]byte(authority.inventorySHA256 + "/" + b.Inventory.CaseID))
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return 0
	})
	if len(cases) > maxItems {
		cases = cases[:maxItems]
	}
	return RightsSelection{QuarantineInspection: cloneReportBinding(authority.reportBinding), Cases: cases}, nil
}

// Require independently proves that one locked decision and its reopened
// source bytes retain the exact authority needed for preparation.
func (authority RightsEligibility) Require(decision fillercorpus.RightsDecision, actualSHA256 string) error {
	if decision.InventorySHA256 != authority.inventorySHA256 {
		return fmt.Errorf("case %q decision has a different inventory identity", decision.CaseID)
	}
	candidate, exists := authority.byCase[decision.CaseID]
	if !exists {
		return fmt.Errorf("case %q is not eligible under the quarantine inspection", decision.CaseID)
	}
	if candidate.Inventory.Representation.Transport == fillercorpus.TransportLocal {
		if decision.QuarantineInspection != nil {
			return fmt.Errorf("direct local case %q carries an inapplicable quarantine inspection", decision.CaseID)
		}
		return nil
	}
	if candidate.QuarantineInspection == nil || decision.QuarantineInspection == nil || *decision.QuarantineInspection != *candidate.QuarantineInspection {
		return fmt.Errorf("case %q decision does not bind the exact quarantine inspection", decision.CaseID)
	}
	if actualSHA256 != candidate.QuarantineInspection.ContentSHA256 {
		return fmt.Errorf("case %q source bytes differ from quarantine inspection", decision.CaseID)
	}
	return nil
}

func decodeReport(raw []byte) (Report, error) {
	var report Report
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return Report{}, fmt.Errorf("decode quarantine inspection: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Report{}, fmt.Errorf("decode quarantine inspection: trailing JSON value")
	}
	return report, nil
}

func cloneReportBinding(value *fillercorpus.QuarantineInspectionBinding) *fillercorpus.QuarantineInspectionBinding {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneEligibleRightsCase(value EligibleRightsCase) EligibleRightsCase {
	if value.QuarantineInspection != nil {
		binding := *value.QuarantineInspection
		value.QuarantineInspection = &binding
	}
	return value
}
