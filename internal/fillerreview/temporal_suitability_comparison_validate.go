package fillerreview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type temporalSuitabilityComparisonLoaded struct {
	first, second       TemporalSuitabilityResult
	evidenceSHA         string
	selectionSHA        string
	firstSHA, secondSHA string
}

func loadTemporalSuitabilityComparison(config TemporalSuitabilityComparisonConfig) (temporalSuitabilityComparisonLoaded, error) {
	if strings.TrimSpace(config.EvidenceManifestPath) == "" || strings.TrimSpace(config.FirstResultPath) == "" || strings.TrimSpace(config.SecondResultPath) == "" || strings.TrimSpace(config.OutputPath) == "" || config.ComparedAt.IsZero() || config.ExpectedCases <= 0 {
		return temporalSuitabilityComparisonLoaded{}, fmt.Errorf("suitability comparison requires evidence, two results, expected cases, comparison time, and output")
	}
	manifest, evidenceSHA, err := loadTemporalSuitabilityEvidence(config.EvidenceManifestPath, config.StructureAuthorityPath)
	if err != nil {
		return temporalSuitabilityComparisonLoaded{}, err
	}
	first, firstSHA, err := loadTemporalSuitabilityResultAuthority(config.FirstResultPath, manifest, evidenceSHA, config.ExpectedCases)
	if err != nil {
		return temporalSuitabilityComparisonLoaded{}, fmt.Errorf("first suitability result: %w", err)
	}
	second, secondSHA, err := loadTemporalSuitabilityResultAuthority(config.SecondResultPath, manifest, evidenceSHA, config.ExpectedCases)
	if err != nil {
		return temporalSuitabilityComparisonLoaded{}, fmt.Errorf("second suitability result: %w", err)
	}
	if first.SelectionSHA256 != second.SelectionSHA256 || first.SelectionSHA256 != temporalTruthJSONSHA(first.SelectionAliases) || second.SelectionSHA256 != temporalTruthJSONSHA(second.SelectionAliases) {
		return temporalSuitabilityComparisonLoaded{}, fmt.Errorf("suitability results do not bind one complete selection")
	}
	if config.ComparedAt.Before(first.CompletedAt) || config.ComparedAt.Before(second.CompletedAt) {
		return temporalSuitabilityComparisonLoaded{}, fmt.Errorf("suitability comparison predates a completed result")
	}
	return temporalSuitabilityComparisonLoaded{first: first, second: second, evidenceSHA: evidenceSHA, selectionSHA: first.SelectionSHA256, firstSHA: firstSHA, secondSHA: secondSHA}, nil
}

func loadTemporalSuitabilityResultAuthority(path string, manifest TemporalTruthEvidenceManifest, evidenceSHA string, expectedCases int) (TemporalSuitabilityResult, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return TemporalSuitabilityResult{}, "", err
	}
	result, err := DecodeTemporalSuitabilityResult(raw)
	if err != nil {
		return TemporalSuitabilityResult{}, "", err
	}
	if result.EvidenceManifestSHA256 != evidenceSHA || len(result.Assessments) != expectedCases || len(result.SelectionAliases) != expectedCases || len(result.Attempts) != expectedCases || result.Requests != expectedCases || result.CompletedAt.IsZero() || result.ProductionAdmissionAllowed {
		return TemporalSuitabilityResult{}, "", fmt.Errorf("suitability result identity, counts, or admission boundary is invalid")
	}
	caseByAlias := make(map[string]TemporalTruthEvidenceCase, len(manifest.Cases))
	for _, item := range manifest.Cases {
		caseByAlias[item.Alias] = item
	}
	assessmentByAlias := suitabilityAssessmentIndex(result.Assessments)
	if len(assessmentByAlias) != expectedCases {
		return TemporalSuitabilityResult{}, "", fmt.Errorf("suitability result repeats an assessment alias")
	}
	for index, alias := range result.SelectionAliases {
		item, exists := caseByAlias[alias]
		assessment, assessed := assessmentByAlias[alias]
		if !exists || !assessed || result.Attempts[index].EvidenceAlias != alias {
			return TemporalSuitabilityResult{}, "", fmt.Errorf("suitability result names unknown or disordered evidence")
		}
		if err := validateTemporalSuitabilityAssessment(assessment, item.DurationMS); err != nil {
			return TemporalSuitabilityResult{}, "", err
		}
		attempt := result.Attempts[index]
		if attempt.ResponseSHA256 == "" || attempt.RawResponsePath != filepath.ToSlash(filepath.Join(temporalSuitabilityResponsesDir, alias+".json")) || attempt.ResponseSHA256 != assessment.RawResponseSHA256 {
			return TemporalSuitabilityResult{}, "", fmt.Errorf("suitability result lacks one raw response binding")
		}
		responsePath := filepath.Join(path+".private", filepath.FromSlash(attempt.RawResponsePath))
		info, statErr := os.Lstat(responsePath)
		responseSHA, hashErr := hashFile(responsePath)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || hashErr != nil || responseSHA != attempt.ResponseSHA256 {
			return TemporalSuitabilityResult{}, "", fmt.Errorf("suitability raw response binding failed")
		}
	}
	return result, hashBytes(raw), nil
}
