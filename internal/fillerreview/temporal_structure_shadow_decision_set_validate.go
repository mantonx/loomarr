package fillerreview

import (
	"errors"
	"reflect"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

func ValidateTemporalStructureShadowDecisionSet(set TemporalStructureShadowDecisionSet) error {
	if set.SchemaVersion != TemporalStructureShadowDecisionSetSchemaVersion ||
		set.ContractVersion != TemporalStructureShadowDecisionSetContractVersion ||
		set.InputKind != fillerstructure.AssessmentInputCompleteVideo && set.InputKind != fillerstructure.AssessmentInputWindowMediaSet ||
		set.ReducerVersion != fillerstructure.ReducerContractVersion || set.BoundaryToleranceMS != fillerstructurewindowcert.BoundaryToleranceMS ||
		!reviewSHA256(set.WindowSetManifestSHA256) || set.DecidedAt.IsZero() || set.DecidedAt != set.DecidedAt.UTC() ||
		len(set.Families) != 2 || len(set.Cases) != TemporalStructureWindowCorpusCases ||
		set.TrainingAllowed || set.ProductionAdmissionAllowed || !reviewSHA256(set.SHA256) ||
		set.SHA256 != temporalStructureShadowDecisionSetSHA256(set) {
		return errors.New("structure shadow decision set identity or disposition is invalid")
	}
	if !slices.IsSortedFunc(set.Families, func(left, right TemporalStructureShadowDecisionFamily) int {
		return strings.Compare(left.Assessor.ID, right.Assessor.ID)
	}) || !slices.IsSortedFunc(set.Cases, func(left, right TemporalStructureShadowDecisionCase) int {
		return strings.Compare(left.Alias, right.Alias)
	}) {
		return errors.New("structure shadow decision set is not canonically ordered")
	}
	profiles := make([]fillerstructure.AssessorProfile, 0, len(set.Families))
	resultDigests := make(map[string]struct{}, len(set.Families))
	for _, family := range set.Families {
		if fillerstructure.ValidateAssessorProfile(family.Assessor) != nil || !reviewSHA256(family.ResultSHA256) ||
			!reviewSHA256(family.ResultFileSHA256) {
			return errors.New("structure shadow decision family is invalid")
		}
		if _, duplicate := resultDigests[family.ResultSHA256]; duplicate {
			return errors.New("structure shadow decision set repeats family evidence")
		}
		resultDigests[family.ResultSHA256] = struct{}{}
		profiles = append(profiles, family.Assessor)
	}
	if err := fillerstructure.ValidateAssessorProfiles(profiles); err != nil {
		return err
	}
	seenAliases := make(map[string]struct{}, len(set.Cases))
	for _, item := range set.Cases {
		if strings.TrimSpace(item.Alias) != item.Alias || item.Alias == "" {
			return errors.New("structure shadow decision alias is invalid")
		}
		if _, duplicate := seenAliases[item.Alias]; duplicate {
			return errors.New("structure shadow decision set repeats a case")
		}
		seenAliases[item.Alias] = struct{}{}
		if fillerstructure.ValidateArtifact(item.Artifact) != nil || item.Artifact.ReducerVersion != set.ReducerVersion ||
			item.Artifact.BoundaryToleranceMS != set.BoundaryToleranceMS || item.Artifact.Decision.Input.Kind != set.InputKind ||
			item.Artifact.DecidedAt.After(set.DecidedAt) {
			return errors.New("structure shadow decision artifact is invalid")
		}
		artifactProfiles := make([]fillerstructure.AssessorProfile, 0, len(item.Artifact.Decision.Candidates))
		for _, candidate := range item.Artifact.Decision.Candidates {
			artifactProfiles = append(artifactProfiles, fillerstructure.Profile(candidate.Assessor))
		}
		if !reflect.DeepEqual(artifactProfiles, profiles) {
			return errors.New("structure shadow decision artifact profiles drifted")
		}
	}
	return nil
}

func validateTemporalStructureShadowDecisionSetAgainstManifest(set TemporalStructureShadowDecisionSet, manifest TemporalStructureWindowSetManifest, manifestSHA string) error {
	if err := ValidateTemporalStructureShadowDecisionSet(set); err != nil {
		return err
	}
	if set.WindowSetManifestSHA256 != manifestSHA || len(set.Cases) != len(manifest.Cases) {
		return errors.New("structure shadow decision set does not bind the public manifest")
	}
	publicByAlias := make(map[string]TemporalStructureWindowSetPublicCase, len(manifest.Cases))
	for _, item := range manifest.Cases {
		publicByAlias[item.Alias] = item
	}
	for _, item := range set.Cases {
		public, ok := publicByAlias[item.Alias]
		if !ok {
			return errors.New("structure shadow decision set names an unknown public case")
		}
		source := fillerstructure.Source{SHA256: public.Source.SHA256, Bytes: public.Source.Bytes, DurationMS: public.Source.DurationMs}
		if item.Artifact.Decision.Source != source {
			return errors.New("structure shadow decision set case drifted from the public manifest")
		}
		if set.InputKind == fillerstructure.AssessmentInputWindowMediaSet &&
			(item.Artifact.Decision.Input.SHA256 == "" || item.Artifact.Decision.Input.PlanSHA256 != public.MediaSet.Plan.SHA256) {
			return errors.New("structure shadow window decision does not bind the public plan")
		}
	}
	return nil
}
