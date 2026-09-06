package fillerreview

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

// ValidateTemporalStructureWindowFamilyResult verifies the self-contained, truth-blind family
// artifact. Certification assembly additionally binds it to the exact public manifest.
func ValidateTemporalStructureWindowFamilyResult(result TemporalStructureWindowFamilyResult) error {
	if result.SchemaVersion != TemporalStructureWindowFamilySchemaVersion ||
		result.ContractVersion != TemporalStructureWindowFamilyContractVersion ||
		!reviewSHA256(result.WindowSetManifestSHA256) || !reviewSHA256(result.CapabilitySnapshotSHA256) ||
		fillerstructure.ValidateAssessorProfile(result.Assessor) != nil ||
		result.CompletedAt.IsZero() || result.CompletedAt != result.CompletedAt.UTC() ||
		len(result.Cases) != TemporalStructureWindowCorpusCases || result.TrainingAllowed ||
		result.ProductionAdmissionAllowed || !reviewSHA256(result.SHA256) ||
		result.SHA256 != temporalStructureWindowFamilySHA256(result) {
		return errors.New("window family result identity, count, or disposition is invalid")
	}
	aliases := make(map[string]struct{}, len(result.Cases))
	mediaSets := make(map[string]struct{}, len(result.Cases))
	wantAccounting := TemporalStructureWindowFamilyResult{}
	for index, item := range result.Cases {
		if len(item.Alias) != len("case-")+24 || !strings.HasPrefix(item.Alias, "case-") || !isLowerHex(item.Alias[len("case-"):]) {
			return fmt.Errorf("window family case %d has invalid alias", index)
		}
		if _, duplicate := aliases[item.Alias]; duplicate {
			return fmt.Errorf("window family result repeats alias %q", item.Alias)
		}
		stitch := item.Evidence.Stitch
		if _, duplicate := mediaSets[stitch.MediaSet.SHA256]; duplicate {
			return fmt.Errorf("window family result repeats media set %q", stitch.MediaSet.SHA256)
		}
		if stitch.Assessor != result.Assessor {
			return fmt.Errorf("window family case %d mixes assessor identity", index)
		}
		if err := filler.ValidateStructureWindowFamilyEvidence(item.Evidence); err != nil {
			return fmt.Errorf("window family case %d evidence: %w", index, err)
		}
		accumulateTemporalStructureWindowFamilyAccounting(&wantAccounting, item.Evidence)
		aliases[item.Alias] = struct{}{}
		mediaSets[stitch.MediaSet.SHA256] = struct{}{}
	}
	if result.CallRecords != wantAccounting.CallRecords || result.ProviderRequests != wantAccounting.ProviderRequests ||
		result.ChargedNanoUSD != wantAccounting.ChargedNanoUSD || result.AccountedNanoUSD != wantAccounting.AccountedNanoUSD ||
		result.UnknownChargeReservations != wantAccounting.UnknownChargeReservations {
		return errors.New("window family result accounting does not reproduce")
	}
	return nil
}

func validateTemporalStructureWindowFamilyResultAgainstManifest(result TemporalStructureWindowFamilyResult, manifest TemporalStructureWindowSetManifest, manifestSHA string) error {
	if err := ValidateTemporalStructureWindowFamilyResult(result); err != nil {
		return err
	}
	if result.WindowSetManifestSHA256 != manifestSHA || len(result.Cases) != len(manifest.Cases) ||
		result.CompletedAt.Before(manifest.PreparedAt) {
		return errors.New("window family result does not bind the public window set")
	}
	for index, item := range result.Cases {
		public := manifest.Cases[index]
		if item.Alias != public.Alias || !reflect.DeepEqual(item.Evidence.Stitch.MediaSet, public.MediaSet) {
			return fmt.Errorf("window family case %d drifted from public window set", index)
		}
	}
	return nil
}
