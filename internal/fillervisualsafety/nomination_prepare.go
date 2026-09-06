package fillervisualsafety

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
)

var visualCorpusNominationCSVHeader = []string{
	"rank", "inventory_sha256", "materialization_sha256", "case_id", "capture_ids_json",
	"authority", "item_id", "role_hints_json", "creator_json", "subject_terms_json", "source_family",
	"institution_id", "source_work_id", "source_family_id", "independence_group_id", "creator_id",
	"object_url", "rights_url", "rights_basis", "metadata_sha256", "local_file", "content_sha256",
	"asset_bytes", "media_type", "width", "height", "perceptual_hash", "rights_approval_sha256",
	"rights_reviewer_id", "rights_reviewed_at", "rights_review_basis", "rights_required_credit",
	"rights_restrictions_json", "nomination", "subject_status", "generated_status", "slices_json",
}

func VisualCorpusNominationCSVHeader() []string {
	return slices.Clone(visualCorpusNominationCSVHeader)
}

func ImmutableVisualCorpusNominationCSVRecord(worksheet VisualCorpusNominationWorksheet, row VisualCorpusNominationRow) []string {
	return []string{
		strconv.Itoa(row.Rank), worksheet.InventorySHA256, worksheet.MaterializationSHA256, row.CaseID,
		fillercorpus.JSONCell(row.CaptureIDs), row.Authority, row.ItemID, fillercorpus.JSONCell(row.RoleHints),
		fillercorpus.JSONCell(row.Creator), fillercorpus.JSONCell(row.SubjectTerms), fillercorpus.SpreadsheetSafe(row.SourceFamily),
		row.InstitutionID, row.SourceWorkID, row.SourceFamilyID, row.IndependenceGroupID, row.CreatorID,
		row.ObjectURL, row.RightsURL, row.RightsBasis, row.MetadataSHA256, fillercorpus.SpreadsheetSafe(row.LocalFile),
		row.Asset.SHA256, strconv.FormatInt(row.Asset.Bytes, 10), row.MediaType, strconv.Itoa(row.Width),
		strconv.Itoa(row.Height), row.PerceptualHash, row.RightsApprovalSHA256, fillercorpus.SpreadsheetSafe(row.RightsReviewerID),
		row.RightsReviewedAt.UTC().Format(time.RFC3339), fillercorpus.SpreadsheetSafe(row.RightsReviewBasis),
		fillercorpus.SpreadsheetSafe(row.RightsRequiredCredit), fillercorpus.JSONCell(row.RightsRestrictions),
	}
}

func prepareVisualCorpusNominationWorksheet(ctx context.Context, config VisualCorpusNominationPrepareConfig) (VisualCorpusNominationWorksheet, error) {
	if ctx == nil || ctx.Err() != nil || len(config.InventoryJSON) == 0 || len(config.MaterializationJSON) == 0 ||
		!cleanAbsoluteReviewPath(config.MediaRoot) || config.PreparedAt.IsZero() || config.PreparedAt.Location() != time.UTC {
		return VisualCorpusNominationWorksheet{}, errors.New("visual corpus nomination preparation is invalid")
	}
	if err := validatePrivateReviewDirectory(config.MediaRoot); err != nil {
		return VisualCorpusNominationWorksheet{}, errors.New("visual corpus nomination media root is not private")
	}
	inventory, err := fillercorpus.DecodeInventoryBytes(config.InventoryJSON)
	if err != nil {
		return VisualCorpusNominationWorksheet{}, err
	}
	ledger, err := fillercorpus.DecodeMaterializationLedgerBytes(config.MaterializationJSON)
	if err != nil {
		return VisualCorpusNominationWorksheet{}, err
	}
	inventorySHA256 := digestBytes(config.InventoryJSON)
	materializationSHA256 := digestBytes(config.MaterializationJSON)
	if err := fillercorpus.ValidateMaterializationLedger(ledger, inventory, inventorySHA256); err != nil {
		return VisualCorpusNominationWorksheet{}, err
	}
	if config.PreparedAt.Before(ledger.GeneratedAt) {
		return VisualCorpusNominationWorksheet{}, errors.New("visual corpus nomination predates materialization")
	}
	worksheet := VisualCorpusNominationWorksheet{
		SchemaVersion: VisualCorpusNominationWorksheetSchemaVersion, ContractVersion: VisualCorpusNominationWorksheetContractVersion,
		Profile: ledger.Profile, InventorySHA256: inventorySHA256, MaterializationSHA256: materializationSHA256,
		PreparedAt: config.PreparedAt,
		Instructions: []string{
			"Do not edit immutable columns; locking compares every value with this exact worksheet and source ledger.",
			"Complete only nomination, subject_status, generated_status, and slices_json in the CSV companion.",
			"A positive candidate requires historical_art_adult_only; a clean candidate requires no_sensitive_subject_identified.",
			"Use not_generated only after checking the work is not generated; uncertainty must remain outside the locked set.",
			"This worksheet creates no truth, training, production, ingestion, scheduling, or broadcast authority.",
		},
		Cases: []VisualCorpusNominationRow{}, CandidateModelOutput: false, TruthAuthorityCreated: false,
		TrainingAllowed: false, ProductionUseAllowed: false,
	}
	seenPerceptual := make(map[string]string, len(ledger.Cases))
	for index, item := range ledger.Cases {
		if err := ctx.Err(); err != nil {
			return VisualCorpusNominationWorksheet{}, fmt.Errorf("prepare visual corpus nominations: %w", err)
		}
		if config.PreparedAt.Before(item.VerifiedAt) {
			return VisualCorpusNominationWorksheet{}, fmt.Errorf("visual corpus nomination predates verification of %s", item.CaseID)
		}
		row, err := prepareVisualCorpusNominationRow(config.MediaRoot, index+1, item)
		if err != nil {
			return VisualCorpusNominationWorksheet{}, err
		}
		if previous, duplicate := seenPerceptual[row.PerceptualHash]; duplicate {
			return VisualCorpusNominationWorksheet{}, fmt.Errorf("visual corpus nomination normalized-image collision between %s and %s", previous, row.CaseID)
		}
		seenPerceptual[row.PerceptualHash] = row.CaseID
		worksheet.Cases = append(worksheet.Cases, row)
	}
	worksheet.SHA256 = VisualCorpusNominationWorksheetSHA256(worksheet)
	if err := validateVisualCorpusNominationWorksheet(worksheet); err != nil {
		return VisualCorpusNominationWorksheet{}, err
	}
	return worksheet, nil
}

func prepareVisualCorpusNominationRow(mediaRoot string, rank int, item fillercorpus.MaterializedCase) (VisualCorpusNominationRow, error) {
	if item.Authority != fillercorpus.MetAuthority || item.VerifiedMediaType != "image/jpeg" || len(item.Creator) != 1 ||
		strings.TrimSpace(item.SourceFamily) == "" || !strings.HasPrefix(item.Approval.Basis, VisualCorpusMetCC0ApprovalBasisPrefix) ||
		strings.TrimSpace(strings.TrimPrefix(item.Approval.Basis, VisualCorpusMetCC0ApprovalBasisPrefix)) == "" ||
		len(item.Approval.Restrictions) != 0 {
		return VisualCorpusNominationRow{}, fmt.Errorf("materialized case %s lacks the closed Met CC0 visual-corpus projection", item.CaseID)
	}
	asset := VisualCorpusFileIdentity{SHA256: item.ContentSHA256, Bytes: item.Representation.Bytes}
	raw, err := readVisualCorpusInput(mediaRoot, item.LocalFile, asset)
	if err != nil {
		return VisualCorpusNominationRow{}, fmt.Errorf("materialized case %s media does not reproduce: %w", item.CaseID, err)
	}
	mediaType, width, height, perceptual, err := inspectVisualCorpusImage(raw)
	if err != nil || mediaType != item.VerifiedMediaType || width != item.Width || height != item.Height {
		return VisualCorpusNominationRow{}, fmt.Errorf("materialized case %s image evidence drifted", item.CaseID)
	}
	creatorID := "creator-sha256:" + digestJSON(item.Creator)
	row := VisualCorpusNominationRow{
		Rank: rank, CaseID: item.CaseID, CaptureIDs: slices.Clone(item.CaptureIDs), Authority: item.Authority,
		ItemID: item.ItemID, RoleHints: slices.Clone(item.RoleHints), Creator: slices.Clone(item.Creator),
		SubjectTerms: slices.Clone(item.SubjectTerms), SourceFamily: item.SourceFamily,
		InstitutionID: item.Authority, SourceWorkID: item.SourceFamily, SourceFamilyID: item.SourceFamily,
		IndependenceGroupID: item.SourceFamily, CreatorID: creatorID, ObjectURL: item.ItemURL,
		RightsURL: item.ItemURL, RightsBasis: VisualCorpusRightsCC0, MetadataSHA256: item.MetadataSHA256,
		LocalFile: item.LocalFile, Asset: asset, MediaType: mediaType, Width: width, Height: height,
		PerceptualHash: perceptual, RightsApprovalSHA256: digestJSON(item.Approval),
		RightsReviewerID: item.Approval.ReviewerID, RightsReviewedAt: item.Approval.ReviewedAt,
		RightsReviewBasis: item.Approval.Basis, RightsRequiredCredit: item.Approval.RequiredCredit,
		RightsRestrictions: slices.Clone(item.Approval.Restrictions),
	}
	if !validIdentity(row.CaseID) || !validIdentity(row.InstitutionID) || !validIdentity(row.SourceWorkID) ||
		!validIdentity(row.SourceFamilyID) || !validIdentity(row.IndependenceGroupID) || !validIdentity(row.CreatorID) ||
		!validDigest(row.RightsApprovalSHA256) {
		return VisualCorpusNominationRow{}, fmt.Errorf("materialized case %s cannot form a visual-corpus identity", item.CaseID)
	}
	return row, nil
}

func validateVisualCorpusNominationWorksheet(worksheet VisualCorpusNominationWorksheet) error {
	if worksheet.SchemaVersion != VisualCorpusNominationWorksheetSchemaVersion ||
		worksheet.ContractVersion != VisualCorpusNominationWorksheetContractVersion ||
		(worksheet.Profile != fillercorpus.RightsProfileDevelopment && worksheet.Profile != fillercorpus.RightsProfileCertification) ||
		!validDigest(worksheet.InventorySHA256) || !validDigest(worksheet.MaterializationSHA256) ||
		worksheet.PreparedAt.IsZero() || worksheet.PreparedAt.Location() != time.UTC || len(worksheet.Instructions) == 0 ||
		len(worksheet.Cases) == 0 || len(worksheet.Cases) > MaximumVisualCorpusDraftCases || worksheet.CandidateModelOutput ||
		worksheet.TruthAuthorityCreated || worksheet.TrainingAllowed || worksheet.ProductionUseAllowed ||
		worksheet.SHA256 == "" || worksheet.SHA256 != VisualCorpusNominationWorksheetSHA256(worksheet) {
		return errors.New("visual corpus nomination worksheet is invalid")
	}
	seenCase := make(map[string]struct{}, len(worksheet.Cases))
	seenAsset := make(map[string]struct{}, len(worksheet.Cases))
	seenPerceptual := make(map[string]struct{}, len(worksheet.Cases))
	for index, row := range worksheet.Cases {
		if row.Rank != index+1 || !validIdentity(row.CaseID) || len(row.CaptureIDs) == 0 || row.Authority == "" ||
			row.ItemID == "" || len(row.RoleHints) == 0 || len(row.Creator) == 0 || row.SourceFamily == "" ||
			!validIdentity(row.InstitutionID) || !validIdentity(row.SourceWorkID) || !validIdentity(row.SourceFamilyID) ||
			!validIdentity(row.IndependenceGroupID) || !validIdentity(row.CreatorID) || !validHTTPSURL(row.ObjectURL) ||
			!validHTTPSURL(row.RightsURL) || row.RightsBasis != VisualCorpusRightsCC0 || !validDigest(row.MetadataSHA256) ||
			!validCorpusRelativePath(row.LocalFile) || !validFileIdentity(row.Asset, MaximumVisualCorpusAssetBytes) ||
			(row.MediaType != "image/jpeg" && row.MediaType != "image/png") || row.Width <= 0 || row.Height <= 0 ||
			int64(row.Width) > MaximumVisualCorpusPixels/int64(row.Height) || !validPerceptualHash(row.PerceptualHash) ||
			!validDigest(row.RightsApprovalSHA256) || !validIdentity(row.RightsReviewerID) || row.RightsReviewedAt.IsZero() ||
			row.RightsReviewedAt.Location() != time.UTC || strings.TrimSpace(row.RightsReviewBasis) == "" ||
			len(row.RightsRestrictions) != 0 || duplicateIdentity(seenCase, row.CaseID) ||
			duplicateIdentity(seenAsset, row.Asset.SHA256) || duplicateIdentity(seenPerceptual, row.PerceptualHash) {
			return fmt.Errorf("visual corpus nomination worksheet row %d is invalid", index+1)
		}
	}
	return nil
}

func sameVisualCorpusNominationWorksheet(left, right VisualCorpusNominationWorksheet) bool {
	return reflect.DeepEqual(left, right)
}
