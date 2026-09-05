package fillercorpus

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestHoldoutRightsHoldReasonsFailClosedByIndependentAxis(t *testing.T) {
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	valid := func() *HoldoutRightsContract {
		return &HoldoutRightsContract{
			SchemaVersion: HoldoutRightsContractSchemaVersion,
			AgreementID:   "agreement-v1", AgreementSHA256: strings.Repeat("a", 64), ScheduleID: "schedule-v1", ScheduleSHA256: strings.Repeat("b", 64),
			SignerAuthorityStatus: RightsStatusCleared, SignerAuthorityEvidenceSHA256: strings.Repeat("c", 64), ProcessorID: "processor-v1", ProcessorTermsSHA256: strings.Repeat("d", 64),
			Grants:                       HoldoutRightsGrants{CommercialEvaluation: true, CopyAndStorage: true, TechnicalModification: true, EvidenceExtraction: true, ProviderTransfer: true},
			EmbeddedRights:               EmbeddedRightsStatus{Music: RightsStatusNotPresent, PerformersAndVoices: RightsStatusCleared, StockAndArtwork: RightsStatusNotPresent, Trademarks: RightsStatusCleared, PrivacyAndPublicity: RightsStatusCleared, Locations: RightsStatusNotPresent},
			EmbeddedRightsEvidenceSHA256: strings.Repeat("e", 64), RedistributionScope: RedistributionExternalOnly, Territory: RightsTerritoryWorldwide, Term: RightsTermPerpetualIrrevocable, Withdrawal: RightsWithdrawalDefectRetirement,
		}
	}
	if reasons := HoldoutRightsHoldReasons(valid(), at); len(reasons) != 0 {
		t.Fatalf("valid contract held by %v", reasons)
	}
	tests := map[string]struct {
		mutate func(*HoldoutRightsContract)
		want   string
	}{
		"schedule digest":   {func(v *HoldoutRightsContract) { v.ScheduleSHA256 = "bad" }, "schedule_identity_invalid"},
		"signer unknown":    {func(v *HoldoutRightsContract) { v.SignerAuthorityStatus = RightsStatusUnknown }, "signer_authority_unconfirmed"},
		"provider grant":    {func(v *HoldoutRightsContract) { v.Grants.ProviderTransfer = false }, "grant_provider_transfer_missing"},
		"embedded conflict": {func(v *HoldoutRightsContract) { v.EmbeddedRights.Music = RightsStatusConflicting }, "embedded_rights_music_unresolved"},
		"territory":         {func(v *HoldoutRightsContract) { v.Territory = "us_only" }, "territory_not_worldwide"},
		"expired": {func(v *HoldoutRightsContract) {
			expired := at.Add(-time.Minute)
			v.Term, v.ExpiresAt = RightsTermExpires, &expired
		}, "term_expired"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			contract := valid()
			test.mutate(contract)
			if reasons := HoldoutRightsHoldReasons(contract, at); !slices.Contains(reasons, test.want) {
				t.Fatalf("reasons = %v; want %q", reasons, test.want)
			}
		})
	}
}

func TestQuarantineAcquisitionHoldReasonsForbidEveryDownstreamUse(t *testing.T) {
	valid := func() *QuarantineAcquisitionContract {
		return &QuarantineAcquisitionContract{
			SchemaVersion:            QuarantineAcquisitionContractSchemaVersion,
			Purpose:                  QuarantinePurposeLocalInspection,
			CopyAndStorage:           true,
			LocalTechnicalInspection: true,
		}
	}
	if reasons := QuarantineAcquisitionHoldReasons(valid()); len(reasons) != 0 {
		t.Fatalf("valid quarantine contract held by %v", reasons)
	}
	tests := map[string]struct {
		mutate func(*QuarantineAcquisitionContract)
		want   string
	}{
		"copy":               {func(value *QuarantineAcquisitionContract) { value.CopyAndStorage = false }, "copy_storage_missing"},
		"inspection":         {func(value *QuarantineAcquisitionContract) { value.LocalTechnicalInspection = false }, "local_technical_inspection_missing"},
		"provider transfer":  {func(value *QuarantineAcquisitionContract) { value.ProviderTransfer = true }, "provider_transfer_forbidden"},
		"redistribution":     {func(value *QuarantineAcquisitionContract) { value.Redistribution = true }, "redistribution_forbidden"},
		"corpus preparation": {func(value *QuarantineAcquisitionContract) { value.CorpusPreparation = true }, "corpus_preparation_forbidden"},
		"training":           {func(value *QuarantineAcquisitionContract) { value.Training = true }, "training_forbidden"},
		"catalog ingestion":  {func(value *QuarantineAcquisitionContract) { value.CatalogIngestion = true }, "catalog_ingestion_forbidden"},
		"scheduling":         {func(value *QuarantineAcquisitionContract) { value.Scheduling = true }, "scheduling_forbidden"},
		"production":         {func(value *QuarantineAcquisitionContract) { value.ProductionAdmission = true }, "production_admission_forbidden"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			contract := valid()
			test.mutate(contract)
			if reasons := QuarantineAcquisitionHoldReasons(contract); !slices.Contains(reasons, test.want) {
				t.Fatalf("reasons = %v; want %q", reasons, test.want)
			}
		})
	}
}
