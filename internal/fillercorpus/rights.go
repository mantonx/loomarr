package fillercorpus

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const RightsWorksheetSchemaVersion = 6

// QuarantineInspectionBinding identifies the exact immutable quarantine report
// and every authority input that report validated. It is nil only for an
// inventory whose selected cases are all direct local media.
type QuarantineInspectionBinding struct {
	ReportSHA256              string `json:"reportSha256"`
	InventorySHA256           string `json:"inventorySha256"`
	DownloadLedgerSHA256      string `json:"downloadLedgerSha256"`
	PriorPublicManifestSHA256 string `json:"priorPublicManifestSha256"`
	PriorAuthoritySHA256      string `json:"priorAuthoritySha256"`
}

// QuarantineInspectionCaseBinding is the per-source authority retained by a
// worksheet row and locked decision. The report is independently reopened at
// every transition; these projected fields never validate themselves.
type QuarantineInspectionCaseBinding struct {
	Report        QuarantineInspectionBinding `json:"report"`
	ContentSHA256 string                      `json:"contentSha256"`
}

type RightsWorksheet struct {
	SchemaVersion        int                          `json:"schemaVersion"`
	Profile              string                       `json:"profile,omitempty"`
	InventorySHA256      string                       `json:"inventorySha256"`
	SnapshotAt           time.Time                    `json:"snapshotAt"`
	PreparedAt           time.Time                    `json:"preparedAt"`
	MinItems             int                          `json:"minItems"`
	MaxItems             int                          `json:"maxItems"`
	Instructions         []string                     `json:"instructions"`
	HoldoutTemplate      *HoldoutRightsTemplate       `json:"holdoutTemplate,omitempty"`
	QuarantineInspection *QuarantineInspectionBinding `json:"quarantineInspection,omitempty"`
	Cases                []RightsReviewRow            `json:"cases"`
}

type RightsReviewRow struct {
	Rank                    int                              `json:"rank"`
	InventorySHA256         string                           `json:"inventorySha256"`
	CaseID                  string                           `json:"caseId"`
	CaptureIDs              []string                         `json:"captureIds"`
	Authority               string                           `json:"authority"`
	ItemID                  string                           `json:"itemId"`
	Title                   string                           `json:"title"`
	RoleHints               []string                         `json:"roleHints"`
	Collection              []string                         `json:"collection,omitempty"`
	Creator                 []string                         `json:"creator,omitempty"`
	Campaign                string                           `json:"campaign,omitempty"`
	SourceFamily            string                           `json:"sourceFamily,omitempty"`
	Date                    string                           `json:"date,omitempty"`
	LicenseURL              string                           `json:"licenseUrl,omitempty"`
	RightsAssertions        []string                         `json:"rightsAssertions"`
	PossibleCopyrightStatus []string                         `json:"possibleCopyrightStatus,omitempty"`
	ItemURL                 string                           `json:"itemUrl"`
	MetadataURL             string                           `json:"metadataUrl"`
	MetadataRetrievedAt     time.Time                        `json:"metadataRetrievedAt"`
	MetadataSHA256          string                           `json:"metadataSha256"`
	Evidence                []InventoryEvidence              `json:"evidence,omitempty"`
	AllowedMediaHosts       []string                         `json:"allowedMediaHosts"`
	Representation          InventoryRepresentation          `json:"representation"`
	ReviewerID              string                           `json:"reviewerId"`
	ReviewedAt              string                           `json:"reviewedAt"`
	Decision                string                           `json:"decision"`
	Basis                   string                           `json:"basis"`
	Redistributable         bool                             `json:"redistributable"`
	RequiredCredit          string                           `json:"requiredCredit,omitempty"`
	Restrictions            []string                         `json:"restrictions"`
	QuarantineInspection    *QuarantineInspectionCaseBinding `json:"quarantineInspection,omitempty"`
}

var rightsReviewCSVHeader = []string{
	"rank", "inventory_sha256", "case_id", "capture_ids_json", "authority", "item_id", "metadata_sha256", "title", "role_hints_json", "collection_json", "creator_json", "campaign", "source_family", "date",
	"license_url", "rights_assertions_json", "possible_copyright_status_json", "item_url", "metadata_url", "metadata_retrieved_at", "evidence_json",
	"representation_transport", "representation_name", "representation_url", "representation_path", "representation_mime_type", "representation_origin", "representation_bytes", "representation_sha256", "representation_sha1", "representation_md5", "allowed_media_hosts_json",
	"quarantine_inspection_json",
	"reviewer_id", "reviewed_at", "decision", "basis", "redistributable", "required_credit", "restrictions_json",
}

var holdoutRightsDecisionCSVFields = []string{
	"reviewer_id", "reviewed_at", "decision", "basis",
	"schedule_id", "schedule_sha256", "signer_authority_status", "signer_authority_evidence_sha256",
	"commercial_evaluation", "copy_and_storage", "technical_modification", "evidence_extraction", "provider_transfer",
	"embedded_music", "embedded_performers_voices", "embedded_stock_artwork", "embedded_trademarks", "embedded_privacy_publicity", "embedded_locations", "embedded_rights_evidence_sha256",
	"redistribution_scope", "territory", "term", "expires_at", "withdrawal",
	"adjudicator_id", "adjudicated_at", "adjudication_disposition", "required_credit", "restrictions_json",
}

var quarantineRightsDecisionCSVFields = []string{
	"reviewer_id", "reviewed_at", "decision", "basis",
	"copy_and_storage", "local_technical_inspection", "provider_transfer", "redistribution",
	"corpus_preparation", "training", "catalog_ingestion", "scheduling", "production_admission",
	"required_credit", "restrictions_json",
}

func RightsReviewCSVHeader() []string { return append([]string(nil), rightsReviewCSVHeader...) }

func HoldoutRightsReviewCSVHeader() []string {
	return append(append([]string(nil), rightsReviewCSVHeader[:len(rightsReviewCSVHeader)-7]...), holdoutRightsDecisionCSVFields...)
}

func QuarantineRightsReviewCSVHeader() []string {
	// Quarantine schema v5 predates the post-download report and deliberately
	// retains its exact pre-download CSV contract.
	return append(append([]string(nil), rightsReviewCSVHeader[:len(rightsReviewCSVHeader)-8]...), quarantineRightsDecisionCSVFields...)
}

func RightsReviewCSVHeaderForProfile(profile string) ([]string, error) {
	switch profile {
	case RightsProfileQuarantine:
		return QuarantineRightsReviewCSVHeader(), nil
	case RightsProfileDevelopment:
		return RightsReviewCSVHeader(), nil
	case RightsProfileCertification:
		return HoldoutRightsReviewCSVHeader(), nil
	default:
		return nil, fmt.Errorf("unknown rights profile %q", profile)
	}
}

func RightsReviewRowFromCase(item InventoryCase) RightsReviewRow {
	return RightsReviewRow{
		CaseID: item.CaseID, CaptureIDs: append([]string(nil), item.CaptureIDs...), Authority: item.Authority, ItemID: item.ItemID, Title: item.Title,
		RoleHints: item.RoleHints, Collection: item.Collection, Creator: item.Creator, Campaign: item.Campaign, SourceFamily: item.SourceFamily, Date: item.Date,
		LicenseURL: item.LicenseURL, RightsAssertions: item.RightsAssertions,
		PossibleCopyrightStatus: item.PossibleCopyrightStatus, ItemURL: item.ItemURL, MetadataURL: item.MetadataURL,
		MetadataRetrievedAt: item.MetadataRetrievedAt, MetadataSHA256: item.MetadataSHA256, Evidence: item.Evidence,
		AllowedMediaHosts: item.AllowedMediaHosts, Representation: item.Representation,
		Restrictions: []string{},
	}
}

func ImmutableRightsReviewRecord(row RightsReviewRow) []string {
	return []string{
		strconv.Itoa(row.Rank), row.InventorySHA256, row.CaseID, JSONCell(row.CaptureIDs), row.Authority, row.ItemID, row.MetadataSHA256, SpreadsheetSafe(row.Title), JSONCell(row.RoleHints), JSONCell(row.Collection), JSONCell(row.Creator), SpreadsheetSafe(row.Campaign), SpreadsheetSafe(row.SourceFamily), SpreadsheetSafe(row.Date),
		row.LicenseURL, JSONCell(row.RightsAssertions), JSONCell(row.PossibleCopyrightStatus), row.ItemURL, row.MetadataURL, row.MetadataRetrievedAt.UTC().Format(time.RFC3339), JSONCell(row.Evidence),
		row.Representation.Transport, SpreadsheetSafe(row.Representation.Name), row.Representation.URL, SpreadsheetSafe(row.Representation.Path), SpreadsheetSafe(row.Representation.MIMEType), SpreadsheetSafe(row.Representation.Origin), strconv.FormatInt(row.Representation.Bytes, 10), row.Representation.SHA256, row.Representation.SHA1, row.Representation.MD5, JSONCell(row.AllowedMediaHosts),
	}
}

func ImmutableRightsReviewRecordForProfile(row RightsReviewRow, profile string) []string {
	record := ImmutableRightsReviewRecord(row)
	if profile == RightsProfileDevelopment || profile == RightsProfileCertification {
		record = append(record, JSONCell(row.QuarantineInspection))
	}
	return record
}

func JSONCell(value any) string {
	if values, ok := value.([]string); ok && len(values) == 0 {
		return "null"
	}
	data, _ := json.Marshal(value)
	return SpreadsheetSafe(string(data))
}

func SpreadsheetSafe(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
