package fillerreview

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerreference"
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
	ledgerSHA, ledger, err := loadTemporalStructureHoldoutReferenceDownloadLedger(config.ReferenceDownloadLedgerPath, referenceAudit)
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
	inventory, inventorySHA, err := loadTemporalStructureHoldoutProgrammeInventory(config.ProgrammeInventoryPath, config.SourceRoot, ledger, config.PlannedAt)
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
		{Name: "reference_download_ledger", SHA256: ledgerSHA},
		{Name: "selection", SHA256: hashBytes(selectionRaw)},
		{Name: "suitability", SHA256: suitabilitySHA},
		{Name: "transition_authority", SHA256: transitionSHA},
	}
	for _, input := range inputs {
		if !reviewSHA256(input.SHA256) {
			return temporalStructureHoldoutLoaded{}, fmt.Errorf("temporal structure holdout could not hash input %q", input.Name)
		}
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })
	return temporalStructureHoldoutLoaded{
		selection: selection, evidence: evidence, evidenceSHA: evidenceSHA, privateMap: privateMap,
		human: human, humanSHA: humanSHA, quality: quality, suitability: suitability,
		family: family, transition: transition, programmeInventory: inventory, inputs: inputs,
	}, nil
}

func loadTemporalStructureHoldoutReferenceDownloadLedger(path string, audit fillerreference.Audit) (string, fillerreference.DownloadLedger, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fillerreference.DownloadLedger{}, fmt.Errorf("read temporal structure holdout reference download ledger: %w", err)
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, temporalStructureProgrammeEvidenceMaxBytes+1))
	if err != nil {
		return "", fillerreference.DownloadLedger{}, fmt.Errorf("read temporal structure holdout reference download ledger: %w", err)
	}
	if len(raw) > temporalStructureProgrammeEvidenceMaxBytes {
		return "", fillerreference.DownloadLedger{}, fmt.Errorf("temporal structure holdout reference download ledger exceeds %d bytes", temporalStructureProgrammeEvidenceMaxBytes)
	}
	sha := hashBytes(raw)
	if sha != audit.Inputs.DownloadLedgerSHA256 {
		return "", fillerreference.DownloadLedger{}, fmt.Errorf("temporal structure holdout reference download ledger does not bind reference audit")
	}
	ledger, err := fillerreference.DecodeDownloadLedger(raw)
	if err != nil {
		return "", fillerreference.DownloadLedger{}, fmt.Errorf("decode temporal structure holdout reference download ledger: %w", err)
	}
	if err := validateTemporalStructureHoldoutReferenceDownloadJoin(audit, ledger); err != nil {
		return "", fillerreference.DownloadLedger{}, err
	}
	return sha, ledger, nil
}

func validateTemporalStructureHoldoutReferenceDownloadJoin(audit fillerreference.Audit, ledger fillerreference.DownloadLedger) error {
	if ledger.SchemaVersion != 1 || len(ledger.Cases) != len(audit.Cases) {
		return fmt.Errorf("temporal structure holdout reference download ledger is incomplete")
	}
	byID := make(map[string]fillerreference.DownloadCase, len(ledger.Cases))
	for _, item := range ledger.Cases {
		if item.CaseID == "" || item.Authority == "" || item.ItemID == "" || item.ItemURL == "" || !reviewSHA256(item.ContentSHA256) {
			return fmt.Errorf("temporal structure holdout reference download ledger contains an incomplete case")
		}
		if _, err := canonicalProgrammeReference(item.ItemURL); err != nil {
			return fmt.Errorf("temporal structure holdout reference download ledger contains an invalid item URL")
		}
		if _, duplicate := byID[item.CaseID]; duplicate {
			return fmt.Errorf("temporal structure holdout reference download ledger repeats a case")
		}
		byID[item.CaseID] = item
	}
	for _, item := range audit.Cases {
		download, ok := byID[item.CaseID]
		if !ok || download.ItemID != strings.TrimSpace(item.SourceItemID) || download.ContentSHA256 != item.ContentSHA256 {
			return fmt.Errorf("temporal structure holdout reference download ledger does not bind audit case %q", item.CaseID)
		}
		if path, ok := canonicalRelativeProgrammePath(item.SourceLocalFile); ok {
			downloadPath, downloadOK := canonicalRelativeProgrammePath(download.LocalFile)
			if !downloadOK || downloadPath != path {
				return fmt.Errorf("temporal structure holdout reference download ledger local path does not bind audit case %q", item.CaseID)
			}
		}
		delete(byID, item.CaseID)
	}
	if len(byID) != 0 {
		return fmt.Errorf("temporal structure holdout reference download ledger contains extra cases")
	}
	return nil
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
