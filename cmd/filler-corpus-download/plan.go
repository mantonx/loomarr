package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

func planDownloads(inv fillercorpus.Inventory, approvals []fillercorpus.RightsDecision, opts options) ([]plannedDownload, error) {
	if failures := fillercorpus.ValidateInventory(inv); len(failures) != 0 {
		return nil, fmt.Errorf("invalid inventory: %s", strings.Join(failures, "; "))
	}
	byID := make(map[string]fillercorpus.InventoryCase, len(inv.Cases))
	for _, candidate := range inv.Cases {
		if _, duplicate := byID[candidate.CaseID]; duplicate {
			return nil, fmt.Errorf("duplicate inventory candidate %s", candidate.CaseID)
		}
		byID[candidate.CaseID] = candidate
	}
	seen := map[string]struct{}{}
	var plan []plannedDownload
	var bytes int64
	for _, approval := range approvals {
		if _, duplicate := seen[approval.CaseID]; duplicate {
			return nil, fmt.Errorf("duplicate rights decision for %s", approval.CaseID)
		}
		seen[approval.CaseID] = struct{}{}
		candidate, ok := byID[approval.CaseID]
		if !ok {
			return nil, fmt.Errorf("rights-reviewed item %s is absent from the inventory", approval.CaseID)
		}
		if err := validateDownloadApproval(candidate, approval, opts); err != nil {
			return nil, err
		}
		if approval.Decision == "held" || candidate.Representation.Transport == fillercorpus.TransportLocal {
			continue
		}
		if err := fillercorpus.ValidateMediaURL(candidate.Representation.URL, candidate.AllowedMediaHosts); err != nil {
			return nil, err
		}
		extension, err := downloadExtension(candidate.Representation.MIMEType)
		if err != nil {
			return nil, fmt.Errorf("approved item %s: %w", approval.CaseID, err)
		}
		bytes += candidate.Representation.Bytes
		name := fillercorpus.InventorySHA256([]byte(candidate.CaseID))[:16] + extension
		plan = append(plan, plannedDownload{candidate: candidate, approval: approval, path: filepath.Join(opts.outputDir, name)})
	}
	if len(plan) == 0 {
		return nil, fmt.Errorf("rights ledger approves no media")
	}
	if len(plan) > opts.maxItems || bytes > opts.maxBytes {
		return nil, fmt.Errorf("approved plan has %d items and %d bytes; ceilings are %d and %d", len(plan), bytes, opts.maxItems, opts.maxBytes)
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].candidate.CaseID < plan[j].candidate.CaseID })
	return plan, nil
}

func validateDownloadApproval(candidate fillercorpus.InventoryCase, approval fillercorpus.RightsDecision, opts options) error {
	if approval.InventorySHA256 != opts.inventorySHA256 {
		return fmt.Errorf("rights-reviewed item %s is not tied to the frozen inventory", approval.CaseID)
	}
	if !slices.Equal(approval.CaptureIDs, candidate.CaptureIDs) || approval.Authority != candidate.Authority || approval.ItemID != candidate.ItemID {
		return fmt.Errorf("rights-reviewed item %s changes its source identity", approval.CaseID)
	}
	if approval.Decision != "approved" && approval.Decision != "held" {
		return fmt.Errorf("rights-reviewed item %s has unknown decision %q", approval.CaseID, approval.Decision)
	}
	if approval.MetadataSHA256 != candidate.MetadataSHA256 || approval.ReviewerID == "" || approval.ReviewedAt.IsZero() || approval.ReviewedAt.Before(candidate.MetadataRetrievedAt) || approval.ReviewedAt.After(opts.generatedAt) || strings.TrimSpace(approval.Basis) == "" {
		return fmt.Errorf("rights-reviewed item %s is not tied to its metadata and complete review", approval.CaseID)
	}
	if approval.Decision == "held" {
		return nil
	}
	if opts.profile == fillercorpus.RightsProfileCertification {
		contract := approval.HoldoutContract
		if reasons := fillercorpus.HoldoutRightsHoldReasons(contract, opts.generatedAt); len(reasons) != 0 || contract.ProcessorID != opts.processorID || contract.ProcessorTermsSHA256 != opts.processorTermsSHA256 || len(contract.HoldReasons) != 0 {
			return fmt.Errorf("approved item %s lacks the exact certification holdout authority", approval.CaseID)
		}
	} else if approval.HoldoutContract != nil {
		return fmt.Errorf("development item %s carries a certification-only holdout contract", approval.CaseID)
	}
	if !approval.Redistributable && opts.profile != fillercorpus.RightsProfileCertification {
		return fmt.Errorf("approved item %s is not explicitly redistributable", approval.CaseID)
	}
	if requiresCredit(candidate.LicenseURL) && strings.TrimSpace(approval.RequiredCredit) == "" {
		return fmt.Errorf("approved item %s requires attribution", approval.CaseID)
	}
	return nil
}

func downloadExtension(mediaType string) (string, error) {
	switch mediaType {
	case "video/mp4":
		return ".mp4", nil
	case "image/jpeg":
		return ".jpg", nil
	case "image/png":
		return ".png", nil
	default:
		return "", fmt.Errorf("unsupported representation MIME type %q", mediaType)
	}
}

func requiresCredit(license string) bool {
	normalized := strings.ToLower(license)
	return strings.Contains(normalized, "/licenses/by/") || strings.Contains(normalized, "/licenses/by-sa/")
}
