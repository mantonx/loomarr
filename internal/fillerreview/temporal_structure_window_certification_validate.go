package fillerreview

import (
	"errors"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

func ValidateTemporalStructureWindowCertificationArtifact(artifact TemporalStructureWindowCertificationArtifact) error {
	if artifact.SchemaVersion != TemporalStructureWindowCertificationSchemaVersion ||
		artifact.ContractVersion != TemporalStructureWindowCertificationContractVersion ||
		!reviewSHA256(artifact.WindowSetManifestSHA256) || !reviewSHA256(artifact.SuiteSHA256) ||
		!reviewSHA256(artifact.SuiteFileSHA256) || len(artifact.Families) != 2 ||
		artifact.Report.SuiteSHA256 != artifact.SuiteSHA256 ||
		artifact.Report.SHA256 != fillerstructurewindowcert.ReportSHA256(artifact.Report) ||
		artifact.Report.TrainingAllowed || artifact.Report.AutomaticMaterializationAllowed ||
		artifact.TrainingAllowed || artifact.AutomaticMaterializationAllowed ||
		!reviewSHA256(artifact.SHA256) || artifact.SHA256 != temporalStructureWindowCertificationSHA256(artifact) {
		return errors.New("window certification artifact identity or disposition is invalid")
	}
	if !slices.IsSortedFunc(artifact.Families, func(left, right TemporalStructureWindowCertificationFamilyEvidence) int {
		return strings.Compare(left.Assessor.ID, right.Assessor.ID)
	}) {
		return errors.New("window certification families are not canonically ordered")
	}
	seenIDs := make(map[string]struct{}, 2)
	for _, family := range artifact.Families {
		if fillerstructure.ValidateAssessorProfile(family.Assessor) != nil || !reviewSHA256(family.ResultSHA256) ||
			!reviewSHA256(family.ResultFileSHA256) {
			return errors.New("window certification family evidence is invalid")
		}
		if _, duplicate := seenIDs[family.Assessor.ID]; duplicate {
			return errors.New("window certification repeats an assessor")
		}
		seenIDs[family.Assessor.ID] = struct{}{}
	}
	return nil
}
