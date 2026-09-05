package fillercorpus

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	RightsProfileQuarantine    = "quarantine"
	RightsProfileDevelopment   = "development"
	RightsProfileCertification = "certification"

	QuarantineRightsWorksheetSchemaVersion     = 5
	HoldoutRightsWorksheetSchemaVersion        = 7
	HoldoutRightsContractSchemaVersion         = 1
	QuarantineAcquisitionContractSchemaVersion = 1
	QuarantinePurposeLocalInspection           = "local_quarantine_inspection"

	RightsStatusCleared     = "cleared"
	RightsStatusNotPresent  = "not_present"
	RightsStatusUnknown     = "unknown"
	RightsStatusConflicting = "conflicting"

	RightsTermPerpetualIrrevocable   = "perpetual_irrevocable"
	RightsTermExpires                = "expires"
	RightsTerritoryWorldwide         = "worldwide"
	RightsWithdrawalDefectRetirement = "rights_defect_retirement_only"

	RedistributionMasterAndDerivatives = "master_and_permitted_derivatives"
	RedistributionExternalOnly         = "external_evaluation_only"
)

type QuarantineAcquisitionContract struct {
	SchemaVersion            int      `json:"schemaVersion"`
	Purpose                  string   `json:"purpose"`
	CopyAndStorage           bool     `json:"copyAndStorage"`
	LocalTechnicalInspection bool     `json:"localTechnicalInspection"`
	ProviderTransfer         bool     `json:"providerTransfer"`
	Redistribution           bool     `json:"redistribution"`
	CorpusPreparation        bool     `json:"corpusPreparation"`
	Training                 bool     `json:"training"`
	CatalogIngestion         bool     `json:"catalogIngestion"`
	Scheduling               bool     `json:"scheduling"`
	ProductionAdmission      bool     `json:"productionAdmission"`
	HoldReasons              []string `json:"holdReasons,omitempty"`
}

func KnownRightsProfile(value string) bool {
	switch value {
	case RightsProfileQuarantine, RightsProfileDevelopment, RightsProfileCertification:
		return true
	default:
		return false
	}
}

func RightsWorksheetSchemaForProfile(profile string) (int, bool) {
	switch profile {
	case RightsProfileQuarantine:
		return QuarantineRightsWorksheetSchemaVersion, true
	case RightsProfileDevelopment:
		return RightsWorksheetSchemaVersion, true
	case RightsProfileCertification:
		return HoldoutRightsWorksheetSchemaVersion, true
	default:
		return 0, false
	}
}

func QuarantineAcquisitionHoldReasons(value *QuarantineAcquisitionContract) []string {
	if value == nil {
		return []string{"quarantine_contract_missing"}
	}
	reasons := make([]string, 0, 12)
	add := func(condition bool, reason string) {
		if condition {
			reasons = append(reasons, reason)
		}
	}
	add(value.SchemaVersion != QuarantineAcquisitionContractSchemaVersion, "contract_schema_invalid")
	add(value.Purpose != QuarantinePurposeLocalInspection, "purpose_invalid")
	add(!value.CopyAndStorage, "copy_storage_missing")
	add(!value.LocalTechnicalInspection, "local_technical_inspection_missing")
	add(value.ProviderTransfer, "provider_transfer_forbidden")
	add(value.Redistribution, "redistribution_forbidden")
	add(value.CorpusPreparation, "corpus_preparation_forbidden")
	add(value.Training, "training_forbidden")
	add(value.CatalogIngestion, "catalog_ingestion_forbidden")
	add(value.Scheduling, "scheduling_forbidden")
	add(value.ProductionAdmission, "production_admission_forbidden")
	sort.Strings(reasons)
	return reasons
}

type HoldoutRightsTemplate struct {
	AgreementID          string `json:"agreementId"`
	AgreementSHA256      string `json:"agreementSha256"`
	ProcessorID          string `json:"processorId"`
	ProcessorTermsSHA256 string `json:"processorTermsSha256"`
}

type HoldoutRightsGrants struct {
	CommercialEvaluation  bool `json:"commercialEvaluation"`
	CopyAndStorage        bool `json:"copyAndStorage"`
	TechnicalModification bool `json:"technicalModification"`
	EvidenceExtraction    bool `json:"evidenceExtraction"`
	ProviderTransfer      bool `json:"providerTransfer"`
}

type EmbeddedRightsStatus struct {
	Music               string `json:"music"`
	PerformersAndVoices string `json:"performersAndVoices"`
	StockAndArtwork     string `json:"stockAndArtwork"`
	Trademarks          string `json:"trademarks"`
	PrivacyAndPublicity string `json:"privacyAndPublicity"`
	Locations           string `json:"locations"`
}

type HoldoutRightsContract struct {
	SchemaVersion                 int                  `json:"schemaVersion"`
	AgreementID                   string               `json:"agreementId"`
	AgreementSHA256               string               `json:"agreementSha256"`
	ScheduleID                    string               `json:"scheduleId"`
	ScheduleSHA256                string               `json:"scheduleSha256"`
	SignerAuthorityStatus         string               `json:"signerAuthorityStatus"`
	SignerAuthorityEvidenceSHA256 string               `json:"signerAuthorityEvidenceSha256"`
	ProcessorID                   string               `json:"processorId"`
	ProcessorTermsSHA256          string               `json:"processorTermsSha256"`
	Grants                        HoldoutRightsGrants  `json:"grants"`
	EmbeddedRights                EmbeddedRightsStatus `json:"embeddedRights"`
	EmbeddedRightsEvidenceSHA256  string               `json:"embeddedRightsEvidenceSha256"`
	RedistributionScope           string               `json:"redistributionScope"`
	Territory                     string               `json:"territory"`
	Term                          string               `json:"term"`
	ExpiresAt                     *time.Time           `json:"expiresAt,omitempty"`
	Withdrawal                    string               `json:"withdrawal"`
	AdjudicatorID                 string               `json:"adjudicatorId,omitempty"`
	AdjudicatedAt                 *time.Time           `json:"adjudicatedAt,omitempty"`
	AdjudicationDisposition       string               `json:"adjudicationDisposition,omitempty"`
	HoldReasons                   []string             `json:"holdReasons,omitempty"`
}

func ValidateHoldoutRightsTemplate(value *HoldoutRightsTemplate) error {
	if value == nil || strings.TrimSpace(value.AgreementID) == "" || !IsSHA256(value.AgreementSHA256) ||
		strings.TrimSpace(value.ProcessorID) == "" || !IsSHA256(value.ProcessorTermsSHA256) {
		return fmt.Errorf("certification requires an agreement id/digest and exact processor id/terms digest")
	}
	return nil
}

func HoldoutRightsHoldReasons(value *HoldoutRightsContract, at time.Time) []string {
	if value == nil {
		return []string{"holdout_contract_missing"}
	}
	reasons := make([]string, 0, 24)
	add := func(condition bool, reason string) {
		if condition {
			reasons = append(reasons, reason)
		}
	}
	add(value.SchemaVersion != HoldoutRightsContractSchemaVersion, "contract_schema_invalid")
	add(strings.TrimSpace(value.AgreementID) == "" || !IsSHA256(value.AgreementSHA256), "agreement_identity_invalid")
	add(strings.TrimSpace(value.ScheduleID) == "" || !IsSHA256(value.ScheduleSHA256), "schedule_identity_invalid")
	add(value.SignerAuthorityStatus != RightsStatusCleared, "signer_authority_unconfirmed")
	add(!IsSHA256(value.SignerAuthorityEvidenceSHA256), "signer_authority_evidence_invalid")
	add(strings.TrimSpace(value.ProcessorID) == "" || !IsSHA256(value.ProcessorTermsSHA256), "processor_identity_invalid")
	add(!value.Grants.CommercialEvaluation, "grant_commercial_evaluation_missing")
	add(!value.Grants.CopyAndStorage, "grant_copy_storage_missing")
	add(!value.Grants.TechnicalModification, "grant_technical_modification_missing")
	add(!value.Grants.EvidenceExtraction, "grant_evidence_extraction_missing")
	add(!value.Grants.ProviderTransfer, "grant_provider_transfer_missing")
	statuses := []struct{ name, value string }{
		{"music", value.EmbeddedRights.Music},
		{"performers_voices", value.EmbeddedRights.PerformersAndVoices},
		{"stock_artwork", value.EmbeddedRights.StockAndArtwork},
		{"trademarks", value.EmbeddedRights.Trademarks},
		{"privacy_publicity", value.EmbeddedRights.PrivacyAndPublicity},
		{"locations", value.EmbeddedRights.Locations},
	}
	for _, status := range statuses {
		add(status.value != RightsStatusCleared && status.value != RightsStatusNotPresent, "embedded_rights_"+status.name+"_unresolved")
	}
	add(!IsSHA256(value.EmbeddedRightsEvidenceSHA256), "embedded_rights_evidence_invalid")
	add(value.RedistributionScope != RedistributionMasterAndDerivatives && value.RedistributionScope != RedistributionExternalOnly, "redistribution_scope_invalid")
	add(value.Territory != RightsTerritoryWorldwide, "territory_not_worldwide")
	switch value.Term {
	case RightsTermPerpetualIrrevocable:
		add(value.ExpiresAt != nil, "term_expiry_conflict")
	case RightsTermExpires:
		add(value.ExpiresAt == nil, "term_expiry_missing")
		if value.ExpiresAt != nil {
			add(!value.ExpiresAt.After(at), "term_expired")
		}
	default:
		add(true, "term_invalid")
	}
	add(value.Withdrawal != RightsWithdrawalDefectRetirement, "withdrawal_policy_invalid")
	if value.AdjudicatorID != "" || value.AdjudicatedAt != nil || value.AdjudicationDisposition != "" {
		add(strings.TrimSpace(value.AdjudicatorID) == "" || value.AdjudicatedAt == nil || strings.TrimSpace(value.AdjudicationDisposition) == "", "adjudication_incomplete")
	}
	sort.Strings(reasons)
	return reasons
}

func IsSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
