package fillercorpus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"
)

const (
	MaterializationLedgerSchemaVersion = 3
	MaximumMaterializedImagePixels     = int64(50_000_000)
)

// MaterializationLedger binds rights-approved source records to the exact
// private bytes acquired for later corpus work. It grants no semantic truth,
// training, production, ingestion, scheduling, or broadcast authority.
type MaterializationLedger struct {
	SchemaVersion        int                `json:"schemaVersion"`
	Profile              string             `json:"profile"`
	ProcessorID          string             `json:"processorId,omitempty"`
	ProcessorTermsSHA256 string             `json:"processorTermsSha256,omitempty"`
	InventorySHA256      string             `json:"inventorySha256"`
	GeneratedAt          time.Time          `json:"generatedAt"`
	MaxRequests          int                `json:"maxRequests"`
	RequestsUsed         int                `json:"requestsUsed"`
	MaxItems             int                `json:"maxItems"`
	MaxBytes             int64              `json:"maxBytes"`
	Bytes                int64              `json:"bytes"`
	MaxImagePixels       int64              `json:"maxImagePixels"`
	Cases                []MaterializedCase `json:"cases"`
}

type MaterializedCase struct {
	CaseID              string                  `json:"caseId"`
	CaptureIDs          []string                `json:"captureIds"`
	Authority           string                  `json:"authority"`
	ItemID              string                  `json:"itemId"`
	RoleHints           []string                `json:"roleHints"`
	Creator             []string                `json:"creator,omitempty"`
	SubjectTerms        []string                `json:"subjectTerms,omitempty"`
	Campaign            string                  `json:"campaign,omitempty"`
	SourceFamily        string                  `json:"sourceFamily,omitempty"`
	LicenseURL          string                  `json:"licenseUrl,omitempty"`
	ItemURL             string                  `json:"itemUrl"`
	MetadataURL         string                  `json:"metadataUrl"`
	MetadataRetrievedAt time.Time               `json:"metadataRetrievedAt"`
	MetadataSHA256      string                  `json:"metadataSha256"`
	Representation      InventoryRepresentation `json:"representation"`
	LocalFile           string                  `json:"localFile"`
	ContentSHA256       string                  `json:"contentSha256"`
	VerifiedMediaType   string                  `json:"verifiedMediaType"`
	Width               int                     `json:"width,omitempty"`
	Height              int                     `json:"height,omitempty"`
	Approval            RightsDecision          `json:"approval"`
	VerifiedAt          time.Time               `json:"verifiedAt"`
}

func DecodeMaterializationLedger(reader io.Reader) (MaterializationLedger, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var ledger MaterializationLedger
	if err := decoder.Decode(&ledger); err != nil {
		return MaterializationLedger{}, fmt.Errorf("decode materialization ledger: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return MaterializationLedger{}, errors.New("decode materialization ledger: trailing JSON value")
	}
	if err := validateMaterializationLedgerShape(ledger); err != nil {
		return MaterializationLedger{}, err
	}
	return ledger, nil
}

func DecodeMaterializationLedgerBytes(raw []byte) (MaterializationLedger, error) {
	return DecodeMaterializationLedger(bytes.NewReader(raw))
}

// ValidateMaterializationLedger proves that every ledger row is a canonical
// projection of one case in the exact frozen inventory and its approval.
func ValidateMaterializationLedger(ledger MaterializationLedger, inventory Inventory, inventorySHA256 string) error {
	if failures := ValidateInventory(inventory); len(failures) != 0 {
		return fmt.Errorf("materialization inventory is invalid: %s", strings.Join(failures, "; "))
	}
	if err := validateMaterializationLedgerShape(ledger); err != nil {
		return err
	}
	if !IsSHA256(inventorySHA256) || ledger.InventorySHA256 != inventorySHA256 {
		return errors.New("materialization ledger is not bound to the exact inventory")
	}
	byID := make(map[string]InventoryCase, len(inventory.Cases))
	for _, candidate := range inventory.Cases {
		byID[candidate.CaseID] = candidate
	}
	for _, materialized := range ledger.Cases {
		candidate, ok := byID[materialized.CaseID]
		if !ok {
			return fmt.Errorf("materialized case %s is absent from the inventory", materialized.CaseID)
		}
		if !materializedCaseMatchesInventory(materialized, candidate) {
			return fmt.Errorf("materialized case %s changes frozen inventory provenance", materialized.CaseID)
		}
		approval := materialized.Approval
		if approval.InventorySHA256 != inventorySHA256 || approval.CaseID != candidate.CaseID ||
			!slices.Equal(approval.CaptureIDs, candidate.CaptureIDs) || approval.Authority != candidate.Authority ||
			approval.ItemID != candidate.ItemID || approval.MetadataSHA256 != candidate.MetadataSHA256 ||
			approval.Decision != "approved" || strings.TrimSpace(approval.ReviewerID) == "" ||
			approval.ReviewedAt.IsZero() || approval.ReviewedAt.Location() != time.UTC ||
			approval.ReviewedAt.Before(candidate.MetadataRetrievedAt) || approval.ReviewedAt.After(ledger.GeneratedAt) ||
			strings.TrimSpace(approval.Basis) == "" {
			return fmt.Errorf("materialized case %s lacks its exact completed rights approval", materialized.CaseID)
		}
		if ledger.Profile == RightsProfileDevelopment && (!approval.Redistributable || approval.HoldoutContract != nil) {
			return fmt.Errorf("materialized development case %s lacks development redistribution authority", materialized.CaseID)
		}
		if ledger.Profile == RightsProfileCertification {
			contract := approval.HoldoutContract
			if contract == nil || contract.ProcessorID != ledger.ProcessorID || contract.ProcessorTermsSHA256 != ledger.ProcessorTermsSHA256 || len(HoldoutRightsHoldReasons(contract, ledger.GeneratedAt)) != 0 {
				return fmt.Errorf("materialized certification case %s lacks its exact holdout contract", materialized.CaseID)
			}
		}
	}
	return nil
}

func validateMaterializationLedgerShape(ledger MaterializationLedger) error {
	if ledger.SchemaVersion != MaterializationLedgerSchemaVersion ||
		(ledger.Profile != RightsProfileDevelopment && ledger.Profile != RightsProfileCertification) ||
		!IsSHA256(ledger.InventorySHA256) || ledger.GeneratedAt.IsZero() || ledger.GeneratedAt.Location() != time.UTC ||
		ledger.MaxRequests <= 0 || ledger.RequestsUsed < 0 || ledger.RequestsUsed > ledger.MaxRequests ||
		ledger.MaxItems <= 0 || ledger.MaxBytes <= 0 || ledger.MaxImagePixels <= 0 ||
		ledger.MaxImagePixels > MaximumMaterializedImagePixels || len(ledger.Cases) == 0 ||
		len(ledger.Cases) > ledger.MaxItems || ledger.Bytes <= 0 || ledger.Bytes > ledger.MaxBytes {
		return errors.New("materialization ledger envelope is invalid")
	}
	if (ledger.Profile == RightsProfileDevelopment && (ledger.ProcessorID != "" || ledger.ProcessorTermsSHA256 != "")) ||
		(ledger.Profile == RightsProfileCertification && (strings.TrimSpace(ledger.ProcessorID) == "" || !IsSHA256(ledger.ProcessorTermsSHA256))) {
		return errors.New("materialization ledger processor identity is invalid for its profile")
	}
	seenCase := make(map[string]struct{}, len(ledger.Cases))
	seenContent := make(map[string]struct{}, len(ledger.Cases))
	totalBytes := int64(0)
	previous := ""
	for _, item := range ledger.Cases {
		if previous != "" && strings.Compare(previous, item.CaseID) >= 0 {
			return errors.New("materialization ledger cases are not canonical")
		}
		previous = item.CaseID
		if _, duplicate := seenCase[item.CaseID]; duplicate {
			return fmt.Errorf("materialization ledger repeats case %s", item.CaseID)
		}
		seenCase[item.CaseID] = struct{}{}
		if !IsSHA256(item.ContentSHA256) {
			return fmt.Errorf("materialized case %s has an invalid content identity", item.CaseID)
		}
		if _, duplicate := seenContent[item.ContentSHA256]; duplicate {
			return fmt.Errorf("materialization ledger repeats exact media %s", item.ContentSHA256)
		}
		seenContent[item.ContentSHA256] = struct{}{}
		if item.CaseID == "" || item.Authority == "" || item.ItemID == "" || item.ItemURL == "" ||
			item.MetadataURL == "" || !IsSHA256(item.MetadataSHA256) || item.MetadataRetrievedAt.IsZero() ||
			item.MetadataRetrievedAt.Location() != time.UTC || len(item.CaptureIDs) == 0 || len(item.RoleHints) == 0 ||
			item.LocalFile == "" || path.Base(item.LocalFile) != item.LocalFile || item.LocalFile == "." ||
			item.VerifiedMediaType != item.Representation.MIMEType || item.Representation.Bytes <= 0 ||
			item.VerifiedAt.IsZero() || item.VerifiedAt.Location() != time.UTC || item.VerifiedAt.Before(item.Approval.ReviewedAt) {
			return fmt.Errorf("materialized case %s identity is invalid", item.CaseID)
		}
		extension, image := materializedExtension(item.VerifiedMediaType)
		if extension == "" || !strings.HasSuffix(item.LocalFile, extension) {
			return fmt.Errorf("materialized case %s local media type is invalid", item.CaseID)
		}
		if image {
			if item.Width <= 0 || item.Height <= 0 || int64(item.Width) > ledger.MaxImagePixels/int64(item.Height) {
				return fmt.Errorf("materialized case %s image dimensions exceed the ledger envelope", item.CaseID)
			}
		} else if item.Width != 0 || item.Height != 0 {
			return fmt.Errorf("materialized video case %s carries unverified image dimensions", item.CaseID)
		}
		if totalBytes > ledger.MaxBytes-item.Representation.Bytes {
			return errors.New("materialization ledger byte total overflows its envelope")
		}
		totalBytes += item.Representation.Bytes
	}
	if totalBytes != ledger.Bytes {
		return errors.New("materialization ledger byte total is inconsistent")
	}
	return nil
}

func materializedCaseMatchesInventory(item MaterializedCase, candidate InventoryCase) bool {
	return item.CaseID == candidate.CaseID && slices.Equal(item.CaptureIDs, candidate.CaptureIDs) &&
		item.Authority == candidate.Authority && item.ItemID == candidate.ItemID &&
		slices.Equal(item.RoleHints, candidate.RoleHints) && slices.Equal(item.Creator, candidate.Creator) &&
		slices.Equal(item.SubjectTerms, candidate.SubjectTerms) && item.Campaign == candidate.Campaign &&
		item.SourceFamily == candidate.SourceFamily && item.LicenseURL == candidate.LicenseURL &&
		item.ItemURL == candidate.ItemURL && item.MetadataURL == candidate.MetadataURL &&
		item.MetadataRetrievedAt.Equal(candidate.MetadataRetrievedAt) && item.MetadataSHA256 == candidate.MetadataSHA256 &&
		reflect.DeepEqual(item.Representation, candidate.Representation)
}

func materializedExtension(mediaType string) (string, bool) {
	switch mediaType {
	case "video/mp4":
		return ".mp4", false
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	default:
		return "", false
	}
}
