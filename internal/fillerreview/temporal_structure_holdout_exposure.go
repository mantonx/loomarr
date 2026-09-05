package fillerreview

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

type temporalStructureHoldoutPrior struct {
	planKind string
	exposure TemporalStructureHoldoutTrainingExclusion
	inputs   []TemporalStructureHoldoutInput
}

func loadTemporalStructureHoldoutPrior(config TemporalStructureHoldoutConfig) (temporalStructureHoldoutPrior, error) {
	if config.Genesis {
		return temporalStructureHoldoutPrior{
			planKind: TemporalStructureHoldoutPlanGenesis,
			exposure: emptyTemporalStructureHoldoutExposure(),
		}, nil
	}
	result := temporalStructureHoldoutPrior{
		planKind: TemporalStructureHoldoutPlanReplacement,
		exposure: emptyTemporalStructureHoldoutExposure(),
	}
	seenFiles := make(map[string]struct{}, len(config.PriorAdjudicationPaths))
	seenChallenges := make(map[string]struct{}, len(config.PriorAdjudicationPaths))
	for _, path := range config.PriorAdjudicationPaths {
		authority, err := readStrictJSON[TemporalStructureAnchorAdjudicationAuthority](path)
		if err != nil {
			return temporalStructureHoldoutPrior{}, fmt.Errorf("decode prior anchor adjudication: %w", err)
		}
		digest, err := hashFile(path)
		if err != nil {
			return temporalStructureHoldoutPrior{}, err
		}
		if _, duplicate := seenFiles[digest]; duplicate {
			return temporalStructureHoldoutPrior{}, fmt.Errorf("replacement holdout repeats a prior adjudication file")
		}
		if _, duplicate := seenChallenges[authority.ChallengeID]; duplicate {
			return temporalStructureHoldoutPrior{}, fmt.Errorf("replacement holdout repeats prior challenge %q", authority.ChallengeID)
		}
		if err := validateTemporalStructurePriorAdjudication(authority, config.PlannedAt); err != nil {
			return temporalStructureHoldoutPrior{}, fmt.Errorf("prior adjudication %q: %w", authority.ChallengeID, err)
		}
		seenFiles[digest], seenChallenges[authority.ChallengeID] = struct{}{}, struct{}{}
		result.inputs = append(result.inputs, TemporalStructureHoldoutInput{Name: "prior_adjudication:" + authority.ChallengeID, SHA256: digest})
		result.exposure = unionTemporalStructureHoldoutExposure(result.exposure, authority.PriorExposure)
	}
	sort.Slice(result.inputs, func(i, j int) bool { return result.inputs[i].Name < result.inputs[j].Name })
	return result, nil
}

func validateTemporalStructurePriorAdjudication(authority TemporalStructureAnchorAdjudicationAuthority, plannedAt time.Time) error {
	if authority.SchemaVersion != TemporalStructureAnchorAdjudicationSchemaVersion ||
		authority.ContractVersion != TemporalStructureAnchorAdjudicationAuthorityContract ||
		strings.TrimSpace(authority.ChallengeID) == "" || strings.TrimSpace(authority.ReviewerID) == "" ||
		authority.AdjudicatedAt.IsZero() || authority.AdjudicatedAt.After(plannedAt) ||
		!reviewSHA256(authority.EvidenceManifestSHA256) || !reviewSHA256(authority.HumanAssessmentSHA256) ||
		!reviewSHA256(authority.PlanReceiptSHA256) || !reviewSHA256(authority.ComparisonSHA256) ||
		authority.ChallengeDisposition != TemporalStructureBurnedDiagnosticOnly || authority.BlindHumanAuditRequired ||
		authority.CertificationScoreRepairAllowed || authority.TrainingAllowed || authority.ProductionAdmissionAllowed || len(authority.Cases) == 0 {
		return fmt.Errorf("identity or burned disposition is invalid")
	}
	if err := validateTemporalStructureHoldoutExposure(authority.PriorExposure); err != nil {
		return fmt.Errorf("prior exposure: %w", err)
	}
	if len(authority.Inputs) == 0 || !sort.SliceIsSorted(authority.Inputs, func(i, j int) bool { return authority.Inputs[i].Name < authority.Inputs[j].Name }) {
		return fmt.Errorf("input authority is missing or unordered")
	}
	seenInputs := make(map[string]struct{}, len(authority.Inputs))
	for _, input := range authority.Inputs {
		if strings.TrimSpace(input.Name) == "" || !reviewSHA256(input.SHA256) {
			return fmt.Errorf("input authority is invalid")
		}
		if _, duplicate := seenInputs[input.Name]; duplicate {
			return fmt.Errorf("input authority repeats %q", input.Name)
		}
		seenInputs[input.Name] = struct{}{}
	}
	for _, required := range []string{"comparison", "plan_authoring", "plan_receipt", "private_authority", "public_manifest", "submission"} {
		if _, exists := seenInputs[required]; !exists {
			return fmt.Errorf("input authority omits %q", required)
		}
	}
	if err := validateTemporalStructurePriorAdjudicationCases(authority.Cases, authority.PriorExposure); err != nil {
		return err
	}
	return nil
}

func validateTemporalStructurePriorAdjudicationCases(cases []TemporalStructureAnchorAdjudicationCase, exposure TemporalStructureHoldoutTrainingExclusion) error {
	if !sort.SliceIsSorted(cases, func(i, j int) bool { return cases[i].Alias < cases[j].Alias }) {
		return fmt.Errorf("cases are not canonical")
	}
	sources, families := stringSet(exposure.SourceSHA256), stringSet(exposure.FamilyIDs)
	seen := make(map[string]struct{}, len(cases))
	for _, item := range cases {
		if strings.TrimSpace(item.Alias) == "" || strings.TrimSpace(item.EvidenceAlias) == "" || strings.TrimSpace(item.CaseID) == "" ||
			strings.TrimSpace(item.SourceID) == "" || !reviewSHA256(item.SourceSHA256) || strings.TrimSpace(item.FamilyID) == "" ||
			item.DurationMS <= 0 || item.Coverage != TemporalStructureAnchorReviewComplete ||
			item.Original.Unit != fillereval.UnitStandalone || !validHumanRole(item.Original.Role) ||
			!validTemporalStructureTimes(item.DecisiveAtMS, item.DurationMS, item.Adjudicated.Unit == fillereval.UnitUnclear) ||
			item.Rationale != strings.TrimSpace(item.Rationale) || item.Rationale == "" || len(item.Rationale) > temporalStructureAnchorRationaleMaximumBytes {
			return fmt.Errorf("case %q is invalid", item.Alias)
		}
		if _, duplicate := seen[item.Alias]; duplicate {
			return fmt.Errorf("cases repeat alias %q", item.Alias)
		}
		seen[item.Alias] = struct{}{}
		if _, exists := sources[item.SourceSHA256]; !exists {
			return fmt.Errorf("case %q source is absent from prior exposure", item.Alias)
		}
		if _, exists := families[item.FamilyID]; !exists {
			return fmt.Errorf("case %q family is absent from prior exposure", item.Alias)
		}
		if err := validateTemporalStructureAnchorObservations(item.Observations, item.DurationMS); err != nil {
			return fmt.Errorf("case %q observations: %w", item.Alias, err)
		}
		switch item.Disposition {
		case TemporalStructureAnchorConfirmed:
			if item.Adjudicated != item.Original {
				return fmt.Errorf("case %q confirmed label drift", item.Alias)
			}
		case TemporalStructureAnchorStructuralDisqualification:
			if !validHumanUnit(item.Adjudicated.Unit) || item.Adjudicated.Unit == fillereval.UnitStandalone || item.Adjudicated.Role != "" {
				return fmt.Errorf("case %q structural disposition is invalid", item.Alias)
			}
		case TemporalStructureAnchorRoleCorrection:
			if item.Adjudicated.Unit != fillereval.UnitStandalone || !validHumanRole(item.Adjudicated.Role) || item.Adjudicated.Role == item.Original.Role {
				return fmt.Errorf("case %q role correction is invalid", item.Alias)
			}
		default:
			return fmt.Errorf("case %q disposition is invalid", item.Alias)
		}
	}
	return nil
}

func emptyTemporalStructureHoldoutExposure() TemporalStructureHoldoutTrainingExclusion {
	return TemporalStructureHoldoutTrainingExclusion{
		Split: "holdout", SourceSHA256: []string{}, FamilyIDs: []string{},
		ProgrammeProvenance: []TemporalStructureHoldoutProgrammeProvenance{},
	}
}

func validateTemporalStructureHoldoutExposure(value TemporalStructureHoldoutTrainingExclusion) error {
	if value.Split != "holdout" || value.SourceSHA256 == nil || value.FamilyIDs == nil || value.ProgrammeProvenance == nil ||
		!sort.StringsAreSorted(value.SourceSHA256) || adjacentDuplicate(value.SourceSHA256) ||
		!sort.StringsAreSorted(value.FamilyIDs) || adjacentDuplicate(value.FamilyIDs) ||
		!sort.SliceIsSorted(value.ProgrammeProvenance, func(i, j int) bool {
			return temporalStructureProgrammeProvenanceKey(value.ProgrammeProvenance[i]) < temporalStructureProgrammeProvenanceKey(value.ProgrammeProvenance[j])
		}) {
		return fmt.Errorf("set is absent, unordered, or duplicated")
	}
	previous := ""
	for _, digest := range value.SourceSHA256 {
		if !reviewSHA256(digest) {
			return fmt.Errorf("source digest is invalid")
		}
	}
	for _, family := range value.FamilyIDs {
		if strings.TrimSpace(family) == "" || family != strings.TrimSpace(family) {
			return fmt.Errorf("family is invalid")
		}
	}
	for _, provenance := range value.ProgrammeProvenance {
		key := temporalStructureProgrammeProvenanceKey(provenance)
		if strings.TrimSpace(provenance.Authority) == "" || strings.TrimSpace(provenance.Reference) == "" || key == previous {
			return fmt.Errorf("programme provenance is invalid or duplicated")
		}
		previous = key
	}
	return nil
}

func unionTemporalStructureHoldoutExposure(left, right TemporalStructureHoldoutTrainingExclusion) TemporalStructureHoldoutTrainingExclusion {
	result := emptyTemporalStructureHoldoutExposure()
	result.SourceSHA256 = sortedStringUnion(left.SourceSHA256, right.SourceSHA256)
	result.FamilyIDs = sortedStringUnion(left.FamilyIDs, right.FamilyIDs)
	provenance := make(map[string]TemporalStructureHoldoutProgrammeProvenance, len(left.ProgrammeProvenance)+len(right.ProgrammeProvenance))
	for _, item := range append(append([]TemporalStructureHoldoutProgrammeProvenance{}, left.ProgrammeProvenance...), right.ProgrammeProvenance...) {
		provenance[temporalStructureProgrammeProvenanceKey(item)] = item
	}
	for _, item := range provenance {
		result.ProgrammeProvenance = append(result.ProgrammeProvenance, item)
	}
	sort.Slice(result.ProgrammeProvenance, func(i, j int) bool {
		return temporalStructureProgrammeProvenanceKey(result.ProgrammeProvenance[i]) < temporalStructureProgrammeProvenanceKey(result.ProgrammeProvenance[j])
	})
	return result
}

func sortedStringUnion(left, right []string) []string {
	set := make(map[string]struct{}, len(left)+len(right))
	for _, value := range append(append([]string{}, left...), right...) {
		set[value] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func temporalStructureProgrammeProvenanceKey(value TemporalStructureHoldoutProgrammeProvenance) string {
	return value.Authority + "\x00" + value.Reference
}
