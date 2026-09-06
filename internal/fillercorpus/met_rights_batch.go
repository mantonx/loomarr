package fillercorpus

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

const (
	MetRightsBatchAttestationSchemaVersion = 1
	MetRightsBatchAcceptancePending        = "pending"
	MetRightsBatchAcceptanceAccepted       = "accepted"
	MetRightsApprovalBasisPrefix           = "met_cc0_open_access_object_reviewed_v1: "

	metRightsBatchPurpose              = "private_development_corpus"
	metRightsBatchChainOfTitleWarranty = "not_asserted"
)

var requiredMetBatchAuthorizedUses = []string{
	"private_development_copy_and_storage",
	"private_development_evidence_extraction",
	"private_development_technical_transformation",
}

var requiredMetBatchExcludedAuthorities = []string{
	"broadcast",
	"certification",
	"ingestion",
	"production",
	"provider_transfer",
	"scheduling",
	"training",
	"truth",
}

// MetRightsBatchAttestation is the one human decision required for a complete,
// zero-anomaly Met development inventory. Pending templates and accepted
// attestations are not downloader authority; only the existing rights locker
// can turn their item-bound CSV rows into a rights ledger.
type MetRightsBatchAttestation struct {
	SchemaVersion        int      `json:"schemaVersion"`
	InventorySHA256      string   `json:"inventorySha256"`
	WorksheetSHA256      string   `json:"worksheetSha256"`
	PrescreenSHA256      string   `json:"prescreenSha256"`
	PolicyEvidenceSHA256 string   `json:"policyEvidenceSha256"`
	Purpose              string   `json:"purpose"`
	AcceptedLimitations  []string `json:"acceptedLimitations"`
	AuthorizedUses       []string `json:"authorizedUses"`
	ExcludedAuthorities  []string `json:"excludedAuthorities"`
	ChainOfTitleWarranty string   `json:"chainOfTitleWarranty"`
	Redistributable      bool     `json:"redistributable"`
	RequiredCredit       string   `json:"requiredCredit"`
	Restrictions         []string `json:"restrictions"`
	ReviewerID           string   `json:"reviewerId"`
	ReviewedAt           string   `json:"reviewedAt"`
	Acceptance           string   `json:"acceptance"`
	Basis                string   `json:"basis"`
}

// MetRightsBatchCompletion is a completed review aid, not authority. The CSV
// must still pass the existing item-level rights locker.
type MetRightsBatchCompletion struct {
	InventorySHA256   string
	WorksheetSHA256   string
	PrescreenSHA256   string
	AttestationSHA256 string
	RowCount          int
	DownloadAuthority bool
	CompletedCSV      []byte
}

// PrepareMetRightsBatchAttestation validates all immutable inputs and returns
// one pending, digest-bound attestation template. It performs no I/O.
func PrepareMetRightsBatchAttestation(inventoryRaw, worksheetRaw, prescreenRaw []byte) (MetRightsBatchAttestation, error) {
	inputs, err := validateMetRightsBatchInputs(inventoryRaw, worksheetRaw, prescreenRaw)
	if err != nil {
		return MetRightsBatchAttestation{}, err
	}
	return MetRightsBatchAttestation{
		SchemaVersion:   MetRightsBatchAttestationSchemaVersion,
		InventorySHA256: inputs.inventorySHA256, WorksheetSHA256: inputs.worksheetSHA256,
		PrescreenSHA256: inputs.prescreenSHA256, PolicyEvidenceSHA256: inputs.prescreen.PolicyEvidenceSHA256,
		Purpose: metRightsBatchPurpose, AcceptedLimitations: append([]string(nil), requiredMetPolicyLimitations...),
		AuthorizedUses:       append([]string(nil), requiredMetBatchAuthorizedUses...),
		ExcludedAuthorities:  append([]string(nil), requiredMetBatchExcludedAuthorities...),
		ChainOfTitleWarranty: metRightsBatchChainOfTitleWarranty,
		Redistributable:      true, Restrictions: []string{}, Acceptance: MetRightsBatchAcceptancePending,
	}, nil
}

// CompleteMetRightsBatchReview expands one accepted maintainer attestation
// into the existing item-level CSV contract. Any anomaly or immutable drift
// refuses the entire batch; callers then use the ordinary exception workflow.
func CompleteMetRightsBatchReview(inventoryRaw, worksheetRaw, prescreenRaw, attestationRaw []byte) (MetRightsBatchCompletion, error) {
	inputs, err := validateMetRightsBatchInputs(inventoryRaw, worksheetRaw, prescreenRaw)
	if err != nil {
		return MetRightsBatchCompletion{}, err
	}
	var attestation MetRightsBatchAttestation
	if err := decodeMetRightsBatchJSON(attestationRaw, &attestation); err != nil {
		return MetRightsBatchCompletion{}, fmt.Errorf("decode Met rights batch attestation: %w", err)
	}
	expected, err := PrepareMetRightsBatchAttestation(inventoryRaw, worksheetRaw, prescreenRaw)
	if err != nil {
		return MetRightsBatchCompletion{}, err
	}
	if attestation.SchemaVersion != expected.SchemaVersion || attestation.InventorySHA256 != expected.InventorySHA256 ||
		attestation.WorksheetSHA256 != expected.WorksheetSHA256 || attestation.PrescreenSHA256 != expected.PrescreenSHA256 ||
		attestation.PolicyEvidenceSHA256 != expected.PolicyEvidenceSHA256 || attestation.Purpose != expected.Purpose ||
		!reflect.DeepEqual(attestation.AcceptedLimitations, expected.AcceptedLimitations) ||
		!reflect.DeepEqual(attestation.AuthorizedUses, expected.AuthorizedUses) ||
		!reflect.DeepEqual(attestation.ExcludedAuthorities, expected.ExcludedAuthorities) ||
		attestation.ChainOfTitleWarranty != expected.ChainOfTitleWarranty || !attestation.Redistributable ||
		attestation.RequiredCredit != "" || len(attestation.Restrictions) != 0 {
		return MetRightsBatchCompletion{}, fmt.Errorf("met rights batch attestation changes its frozen scope or authority exclusions")
	}
	if attestation.Acceptance != MetRightsBatchAcceptanceAccepted {
		return MetRightsBatchCompletion{}, fmt.Errorf("met rights batch attestation remains unaccepted")
	}
	if strings.TrimSpace(attestation.ReviewerID) != attestation.ReviewerID || attestation.ReviewerID == "" || len(attestation.ReviewerID) > 256 || strings.ContainsAny(attestation.ReviewerID, "\r\n\x00") {
		return MetRightsBatchCompletion{}, fmt.Errorf("met rights batch reviewer identity is invalid")
	}
	reviewedAt, err := time.Parse(time.RFC3339, attestation.ReviewedAt)
	if err != nil || attestation.ReviewedAt != reviewedAt.UTC().Format(time.RFC3339) || reviewedAt.Before(inputs.prescreen.PreparedAt) {
		return MetRightsBatchCompletion{}, fmt.Errorf("met rights batch review time must be canonical UTC and no earlier than the pre-screen")
	}
	if strings.TrimSpace(attestation.Basis) != attestation.Basis || !strings.HasPrefix(attestation.Basis, MetRightsApprovalBasisPrefix) ||
		len(attestation.Basis) <= len(MetRightsApprovalBasisPrefix) || len(attestation.Basis) > 2048 || strings.ContainsAny(attestation.Basis, "\r\n\x00") {
		return MetRightsBatchCompletion{}, fmt.Errorf("met rights batch basis must be a bounded single-line rationale beginning with %q", MetRightsApprovalBasisPrefix)
	}
	attestationSHA256 := InventorySHA256(attestationRaw)
	completedCSV, err := metRightsBatchCSV(inputs.worksheet, attestation, attestationSHA256)
	if err != nil {
		return MetRightsBatchCompletion{}, err
	}
	return MetRightsBatchCompletion{
		InventorySHA256: inputs.inventorySHA256, WorksheetSHA256: inputs.worksheetSHA256,
		PrescreenSHA256: inputs.prescreenSHA256, AttestationSHA256: attestationSHA256,
		RowCount: len(inputs.worksheet.Cases), DownloadAuthority: false, CompletedCSV: completedCSV,
	}, nil
}
