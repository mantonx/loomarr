package fillerreview

import (
	"errors"
	"strings"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func ValidateTemporalStructureCompleteFamilyResult(result TemporalStructureCompleteFamilyResult) error {
	if result.SchemaVersion != TemporalStructureCompleteFamilySchemaVersion ||
		result.ContractVersion != TemporalStructureCompleteFamilyContractVersion ||
		!reviewSHA256(result.WindowSetManifestSHA256) || !reviewSHA256(result.CapabilitySnapshotSHA256) ||
		fillerstructure.ValidateAssessorProfile(result.Assessor) != nil ||
		result.CompletedAt.IsZero() || result.CompletedAt != result.CompletedAt.UTC() ||
		len(result.Cases) != TemporalStructureWindowCorpusCases || result.CallRecords != len(result.Cases) ||
		result.ProviderRequests < 0 || result.ProviderRequests > result.CallRecords || result.ChargedNanoUSD < 0 ||
		result.AccountedNanoUSD < result.ChargedNanoUSD || result.UnknownChargeReservations < 0 ||
		result.UnknownChargeReservations > result.CallRecords || result.TrainingAllowed || result.ProductionAdmissionAllowed ||
		!reviewSHA256(result.SHA256) || result.SHA256 != temporalStructureCompleteFamilySHA256(result) {
		return errors.New("complete family result identity, coverage, accounting, or disposition is invalid")
	}
	seenAliases := make(map[string]struct{}, len(result.Cases))
	seenOperations := make(map[string]struct{}, len(result.Cases))
	want := TemporalStructureCompleteFamilyResult{}
	for _, item := range result.Cases {
		if strings.TrimSpace(item.Alias) != item.Alias || item.Alias == "" ||
			filler.ValidateStructureCompleteFamilyEvidence(item.Evidence) != nil ||
			item.Evidence.Record.Assessor != result.Assessor || item.Evidence.Record.AssessedAt.After(result.CompletedAt) {
			return errors.New("complete family case evidence is invalid")
		}
		if _, duplicate := seenAliases[item.Alias]; duplicate {
			return errors.New("complete family result repeats a case alias")
		}
		seenAliases[item.Alias] = struct{}{}
		operation := item.Evidence.Publication.OperationSHA256
		if _, duplicate := seenOperations[operation]; duplicate {
			return errors.New("complete family result repeats an operation")
		}
		seenOperations[operation] = struct{}{}
		accumulateTemporalStructureCompleteFamilyAccounting(&want, item.Evidence.Record)
	}
	if want.CallRecords != result.CallRecords || want.ProviderRequests != result.ProviderRequests ||
		want.ChargedNanoUSD != result.ChargedNanoUSD || want.AccountedNanoUSD != result.AccountedNanoUSD ||
		want.UnknownChargeReservations != result.UnknownChargeReservations {
		return errors.New("complete family result accounting does not reproduce")
	}
	return nil
}

func validateTemporalStructureCompleteFamilyResultAgainstManifest(result TemporalStructureCompleteFamilyResult, manifest TemporalStructureWindowSetManifest, manifestSHA string) error {
	if err := ValidateTemporalStructureCompleteFamilyResult(result); err != nil {
		return err
	}
	if result.WindowSetManifestSHA256 != manifestSHA || len(result.Cases) != len(manifest.Cases) {
		return errors.New("complete family result does not bind the public manifest")
	}
	for index, item := range result.Cases {
		public := manifest.Cases[index]
		source := fillerstructure.Source{SHA256: public.Source.SHA256, Bytes: public.Source.Bytes, DurationMS: public.Source.DurationMs}
		if item.Alias != public.Alias || item.Evidence.Record.Source != source ||
			item.Evidence.Record.Media.ProfileSHA256 != manifest.AssessmentMediaProfileSHA256 {
			return errors.New("complete family case drifted from public source or media profile")
		}
	}
	return nil
}
