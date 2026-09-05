package fillercorpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

const (
	DownloadLedgerLegacySchemaVersion = 1
	DownloadLedgerSchemaVersion       = 2
)

// DownloadLedger is the content-addressed acquisition receipt shared by the
// downloader and every later verifier. Profile is absent only in historical
// schema-v1 development receipts; schema v2 always names its authority scope.
type DownloadLedger struct {
	SchemaVersion   int            `json:"schemaVersion"`
	Profile         string         `json:"profile,omitempty"`
	InventorySHA256 string         `json:"inventorySha256"`
	GeneratedAt     time.Time      `json:"generatedAt"`
	MaxRequests     int            `json:"maxRequests"`
	RequestsUsed    int            `json:"requestsUsed"`
	MaxItems        int            `json:"maxItems"`
	MaxBytes        int64          `json:"maxBytes"`
	Bytes           int64          `json:"bytes"`
	Cases           []DownloadCase `json:"cases"`
}

type DownloadCase struct {
	CaseID              string                  `json:"caseId"`
	Authority           string                  `json:"authority"`
	ItemID              string                  `json:"itemId"`
	LicenseURL          string                  `json:"licenseUrl"`
	ItemURL             string                  `json:"itemUrl"`
	MetadataURL         string                  `json:"metadataUrl"`
	MetadataRetrievedAt time.Time               `json:"metadataRetrievedAt"`
	MetadataSHA256      string                  `json:"metadataSha256"`
	Representation      InventoryRepresentation `json:"representation"`
	LocalFile           string                  `json:"localFile"`
	ContentSHA256       string                  `json:"contentSha256"`
	Approval            RightsDecision          `json:"approval"`
	VerifiedAt          time.Time               `json:"verifiedAt"`
}

// DecodeDownloadLedger accepts exactly one strict download-ledger JSON value.
// Profile-specific consumers must additionally call their profile validator;
// decoding alone never grants downstream use.
func DecodeDownloadLedger(reader io.Reader) (DownloadLedger, error) {
	var value DownloadLedger
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return DownloadLedger{}, fmt.Errorf("decode download ledger: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return DownloadLedger{}, fmt.Errorf("decode download ledger: trailing JSON value")
	}
	return value, nil
}

func DecodeDownloadLedgerBytes(raw []byte) (DownloadLedger, error) {
	return DecodeDownloadLedger(bytes.NewReader(raw))
}

// ValidateQuarantineDownloadLedger proves that every downloaded case is an
// approved local-inspection-only projection of the exact frozen inventory.
// It does not inspect the files; the quarantine inspector reopens and hashes
// them after this authority check succeeds.
func ValidateQuarantineDownloadLedger(inventory Inventory, inventorySHA256 string, ledger DownloadLedger) error {
	if failures := ValidateInventory(inventory); len(failures) != 0 {
		return fmt.Errorf("invalid inventory: %s", strings.Join(failures, "; "))
	}
	if ledger.SchemaVersion != DownloadLedgerSchemaVersion || ledger.Profile != RightsProfileQuarantine || ledger.InventorySHA256 != inventorySHA256 || !digest(inventorySHA256, 64) || ledger.GeneratedAt.IsZero() || ledger.MaxRequests <= 0 || ledger.RequestsUsed < 0 || ledger.RequestsUsed > ledger.MaxRequests || ledger.MaxItems <= 0 || ledger.MaxBytes <= 0 || ledger.Bytes <= 0 || ledger.Bytes > ledger.MaxBytes || len(ledger.Cases) == 0 || len(ledger.Cases) > ledger.MaxItems {
		return fmt.Errorf("quarantine download ledger has invalid identity, profile, or ceilings")
	}
	inventoryByID := make(map[string]InventoryCase, len(inventory.Cases))
	for _, item := range inventory.Cases {
		inventoryByID[item.CaseID] = item
	}
	seenCases := make(map[string]struct{}, len(ledger.Cases))
	seenFiles := make(map[string]struct{}, len(ledger.Cases))
	var totalBytes int64
	for index, item := range ledger.Cases {
		candidate, ok := inventoryByID[item.CaseID]
		if !ok || item.Authority != candidate.Authority || item.ItemID != candidate.ItemID || item.LicenseURL != candidate.LicenseURL || item.ItemURL != candidate.ItemURL || item.MetadataURL != candidate.MetadataURL || !item.MetadataRetrievedAt.Equal(candidate.MetadataRetrievedAt) || item.MetadataSHA256 != candidate.MetadataSHA256 || item.Representation != candidate.Representation {
			return fmt.Errorf("quarantine download case %d does not bind one inventory case", index)
		}
		if _, duplicate := seenCases[item.CaseID]; duplicate {
			return fmt.Errorf("quarantine download ledger repeats case %q", item.CaseID)
		}
		if !safeRelativePath(item.LocalFile) {
			return fmt.Errorf("quarantine download case %q has unsafe local file", item.CaseID)
		}
		if _, duplicate := seenFiles[item.LocalFile]; duplicate {
			return fmt.Errorf("quarantine download ledger repeats local file %q", item.LocalFile)
		}
		if !digest(item.ContentSHA256, 64) || item.VerifiedAt.IsZero() || item.VerifiedAt.Before(item.Approval.ReviewedAt) {
			return fmt.Errorf("quarantine download case %q has invalid content verification", item.CaseID)
		}
		approval := item.Approval
		if approval.InventorySHA256 != inventorySHA256 || approval.CaseID != candidate.CaseID || !slices.Equal(approval.CaptureIDs, candidate.CaptureIDs) || approval.Authority != candidate.Authority || approval.ItemID != candidate.ItemID || approval.MetadataSHA256 != candidate.MetadataSHA256 || approval.ReviewerID == "" || approval.ReviewedAt.IsZero() || approval.ReviewedAt.Before(candidate.MetadataRetrievedAt) || approval.ReviewedAt.After(ledger.GeneratedAt) || approval.Decision != "approved" || strings.TrimSpace(approval.Basis) == "" || approval.Redistributable || approval.QuarantineContract == nil || approval.HoldoutContract != nil || len(approval.QuarantineContract.HoldReasons) != 0 || len(QuarantineAcquisitionHoldReasons(approval.QuarantineContract)) != 0 {
			return fmt.Errorf("quarantine download case %q lacks exact local-inspection authority", item.CaseID)
		}
		seenCases[item.CaseID] = struct{}{}
		seenFiles[item.LocalFile] = struct{}{}
		totalBytes += item.Representation.Bytes
	}
	if totalBytes != ledger.Bytes {
		return fmt.Errorf("quarantine download ledger byte total is %d; want %d", ledger.Bytes, totalBytes)
	}
	return nil
}
