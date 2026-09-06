package fillerreview

import (
	"errors"
	"reflect"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

func ValidateTemporalStructureShortLongShadowArtifact(artifact TemporalStructureShortLongShadowArtifact) error {
	if artifact.SchemaVersion != TemporalStructureShortLongShadowSchemaVersion ||
		artifact.ContractVersion != TemporalStructureShortLongShadowContractVersion ||
		!reviewSHA256(artifact.WindowSetManifestSHA256) ||
		!reviewSHA256(artifact.WindowCertificationSHA256) || !reviewSHA256(artifact.WindowCertificationFileSHA256) ||
		!reviewSHA256(artifact.CompleteDecisionSetSHA256) || !reviewSHA256(artifact.CompleteDecisionSetFileSHA256) ||
		!reviewSHA256(artifact.WindowDecisionSetSHA256) || !reviewSHA256(artifact.WindowDecisionSetFileSHA256) ||
		artifact.TrainingAllowed || artifact.ProductionAdmissionAllowed || artifact.AutomaticMaterializationAllowed ||
		artifact.Report.WindowSetManifestSHA256 != artifact.WindowSetManifestSHA256 ||
		artifact.Report.WindowCertificationSHA256 != artifact.WindowCertificationSHA256 ||
		!reviewSHA256(artifact.SHA256) || artifact.SHA256 != temporalStructureShortLongShadowSHA256(artifact) {
		return errors.New("short-long shadow artifact identity or disposition is invalid")
	}
	if err := fillerstructurewindowcert.ValidateShadowReport(artifact.Report); err != nil {
		return err
	}
	return nil
}

func requirePassingWindowCertification(artifact TemporalStructureWindowCertificationArtifact) error {
	if err := ValidateTemporalStructureWindowCertificationArtifact(artifact); err != nil {
		return err
	}
	report := artifact.Report
	if report.Status != fillerstructurewindowcert.StatusPassed || report.Cases != TemporalStructureWindowCorpusCases ||
		report.DecidedCases != report.Cases || report.HeldCases != 0 || report.WrongCases != 0 ||
		len(report.FailureCodes) != 0 || report.NextAction != "run_locked_short_long_shadow_comparison" ||
		len(report.Slices) != len(fillerstructurewindowcert.RequiredSlices()) {
		return errors.New("short-long shadow requires a completely passing window certificate")
	}
	for index, required := range fillerstructurewindowcert.RequiredSlices() {
		slice := report.Slices[index]
		if slice.Slice != required || slice.Cases < fillerstructurewindowcert.MinimumSliceCases ||
			slice.DecidedCases != slice.Cases || slice.HeldCases != 0 || slice.WrongCases != 0 ||
			len(slice.FailureCodes) != 0 || !slice.Passed {
			return errors.New("short-long shadow window certificate has a non-passing required slice")
		}
	}
	profiles := make([]fillerstructure.AssessorProfile, 0, len(artifact.Families))
	for _, family := range artifact.Families {
		profiles = append(profiles, family.Assessor)
	}
	if !reflect.DeepEqual(profiles, report.AssessorProfiles) {
		return errors.New("short-long shadow window certificate family profiles drifted")
	}
	return nil
}

func sameWindowCertificationFamilies(certificate []TemporalStructureWindowCertificationFamilyEvidence, decision []TemporalStructureShadowDecisionFamily) bool {
	if len(certificate) != len(decision) {
		return false
	}
	for index := range certificate {
		if !reflect.DeepEqual(certificate[index].Assessor, decision[index].Assessor) ||
			certificate[index].ResultSHA256 != decision[index].ResultSHA256 ||
			certificate[index].ResultFileSHA256 != decision[index].ResultFileSHA256 {
			return false
		}
	}
	return true
}
