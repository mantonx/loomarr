package fillerreview

import (
	"fmt"
	"os"
	"sort"

	"github.com/loomarr/loomarr/internal/fillereval"
)

type temporalStructureHoldoutLoaded struct {
	selection          fillereval.TemporalTruthSelection
	evidence           TemporalTruthEvidenceManifest
	evidenceSHA        string
	privateMap         TemporalTruthEvidencePrivateMap
	human              TemporalHumanAssessmentSet
	humanSHA           string
	quality            TemporalMediaQualityReport
	suitability        TemporalSuitabilityComparisonReport
	family             temporalStructureHoldoutFamilyAudit
	transition         TemporalTransitionAuthority
	programmeInventory TemporalStructureHoldoutProgrammeInventory
	prior              temporalStructureHoldoutPrior
	inputs             []TemporalStructureHoldoutInput
}

func loadTemporalStructureHoldout(config TemporalStructureHoldoutConfig) (temporalStructureHoldoutLoaded, error) {
	selectionRaw, err := os.ReadFile(config.SelectionPath)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, fmt.Errorf("read temporal structure holdout selection: %w", err)
	}
	selection, err := fillereval.DecodeTemporalTruthSelection(selectionRaw)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	evidence, evidenceSHA, err := LoadTemporalTruthEvidence(config.EvidenceManifestPath)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	privateMapRaw, err := os.ReadFile(config.EvidencePrivateMapPath)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, fmt.Errorf("read temporal structure holdout evidence map: %w", err)
	}
	privateMap, err := readStrictJSON[TemporalTruthEvidencePrivateMap](config.EvidencePrivateMapPath)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, fmt.Errorf("read temporal structure holdout evidence map: %w", err)
	}
	if err := validateTemporalHumanEvidenceJoin(evidence, evidenceSHA, privateMap); err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	if err := validateTemporalHumanSelectionJoin(selection, privateMap); err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	human, attestation, humanSHA, attestationFileSHA, err := loadTemporalHumanLockAuthority(config.HumanAssessmentPath, config.HumanAttestationPath)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	if err := validateTemporalStructureHoldoutHuman(human, attestation, evidence, evidenceSHA); err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	quality, _, qualitySHA, err := loadMediaIntegrityQualityReport(config.MediaQualityPath)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	if err := validateTemporalStructureHoldoutQuality(quality, human, attestation, humanSHA, attestationFileSHA, evidence, evidenceSHA, config.PlannedAt); err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	suitability, suitabilitySHA, err := loadTemporalStructureHoldoutSuitability(config.SuitabilityPath, evidence, evidenceSHA)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	referenceAudit, referenceAuditSHA, err := loadTemporalStructureHoldoutReferenceAudit(config.ReferenceAuditPath, config.PlannedAt)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	family, familySHA, err := loadTemporalStructureHoldoutFamily(config.FamilyAuditPath, selection, referenceAudit, referenceAuditSHA, config.PlannedAt)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	transition, transitionSHA, err := loadTemporalTransitionAuthority(config.TransitionAuthorityPath, evidence, evidenceSHA, privateMap, hashBytes(privateMapRaw), config.PlannedAt)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	inventory, inventorySHA, err := loadTemporalStructureHoldoutProgrammeInventory(config.ProgrammeInventoryPath, config.SourceRoot, config.PlannedAt)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	prior, err := loadTemporalStructureHoldoutPrior(config)
	if err != nil {
		return temporalStructureHoldoutLoaded{}, err
	}
	inputs := []TemporalStructureHoldoutInput{
		{Name: "evidence_manifest", SHA256: evidenceSHA},
		{Name: "evidence_private_map", SHA256: hashBytes(privateMapRaw)},
		{Name: "family_audit", SHA256: familySHA},
		{Name: "human_assessment", SHA256: humanSHA},
		{Name: "human_attestation", SHA256: attestationFileSHA},
		{Name: "media_quality", SHA256: qualitySHA},
		{Name: "programme_inventory", SHA256: inventorySHA},
		{Name: "reference_audit", SHA256: referenceAuditSHA},
		{Name: "selection", SHA256: hashBytes(selectionRaw)},
		{Name: "suitability", SHA256: suitabilitySHA},
		{Name: "transition_authority", SHA256: transitionSHA},
	}
	inputs = append(inputs, prior.inputs...)
	for _, input := range inputs {
		if !reviewSHA256(input.SHA256) {
			return temporalStructureHoldoutLoaded{}, fmt.Errorf("temporal structure holdout could not hash input %q", input.Name)
		}
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })
	return temporalStructureHoldoutLoaded{
		selection: selection, evidence: evidence, evidenceSHA: evidenceSHA, privateMap: privateMap,
		human: human, humanSHA: humanSHA, quality: quality, suitability: suitability,
		family: family, transition: transition, programmeInventory: inventory, prior: prior, inputs: inputs,
	}, nil
}

func validateTemporalStructureHoldoutHuman(set TemporalHumanAssessmentSet, attestation TemporalHumanReviewAttestation, evidence TemporalTruthEvidenceManifest, evidenceSHA string) error {
	if set.SchemaVersion != TemporalHumanReviewSchemaVersion || set.ContractVersion != TemporalHumanReviewContractVersion || set.EvidenceManifestSHA256 != evidenceSHA || set.SelectionSHA256 != evidence.SelectionSHA256 || len(set.Assessments) != len(evidence.Cases) || attestation.LockedAt.Before(set.CompletedAt) {
		return fmt.Errorf("temporal structure holdout human authority drift")
	}
	durations := make(map[string]int64, len(evidence.Cases))
	for _, item := range evidence.Cases {
		durations[item.Alias] = item.DurationMS
	}
	seen := make(map[string]struct{}, len(set.Assessments))
	for _, assessment := range set.Assessments {
		duration, exists := durations[assessment.EvidenceAlias]
		if !exists || assessment.DecisiveAtMS < 0 || assessment.DecisiveAtMS >= duration || !validHumanUnit(assessment.Unit) {
			return fmt.Errorf("temporal structure holdout human assessment is invalid")
		}
		if _, duplicate := seen[assessment.EvidenceAlias]; duplicate {
			return fmt.Errorf("temporal structure holdout human authority repeats an alias")
		}
		seen[assessment.EvidenceAlias] = struct{}{}
		if assessment.Unit == fillereval.UnitStandalone {
			if assessment.Role == nil || !validHumanRole(*assessment.Role) {
				return fmt.Errorf("temporal structure holdout standalone anchor lacks a role")
			}
		} else if assessment.Role != nil {
			return fmt.Errorf("temporal structure holdout non-standalone assessment carries a role")
		}
	}
	return nil
}
