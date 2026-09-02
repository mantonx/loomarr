package fillerreview

import (
	"fmt"
	"strings"
)

type temporalSuitabilityProjectionLoaded struct {
	manifest      TemporalStructureChallengeManifest
	authority     TemporalStructureChallengeAuthority
	comparison    TemporalSuitabilityComparisonReport
	first         TemporalSuitabilityResult
	second        TemporalSuitabilityResult
	manifestSHA   string
	authoritySHA  string
	comparisonSHA string
	firstSHA      string
	secondSHA     string
}

func loadTemporalSuitabilityProjection(config TemporalSuitabilityProjectionConfig) (temporalSuitabilityProjectionLoaded, error) {
	if strings.TrimSpace(config.PublicManifestPath) == "" || strings.TrimSpace(config.StructureAuthorityPath) == "" || strings.TrimSpace(config.SuitabilityComparisonPath) == "" || strings.TrimSpace(config.FirstResultPath) == "" || strings.TrimSpace(config.SecondResultPath) == "" || strings.TrimSpace(config.OutputPath) == "" || config.ExpectedCases <= 0 || config.ProjectedAt.IsZero() {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("suitability projection requires challenge, comparison, two results, exact count, fixed time, and output")
	}
	manifest, authority, manifestSHA, authoritySHA, err := LoadTemporalStructureChallenge(config.PublicManifestPath, config.StructureAuthorityPath, config.ExpectedCases)
	if err != nil {
		return temporalSuitabilityProjectionLoaded{}, err
	}
	evidence, evidenceSHA, err := loadTemporalSuitabilityEvidence(config.PublicManifestPath, config.StructureAuthorityPath)
	if err != nil {
		return temporalSuitabilityProjectionLoaded{}, err
	}
	if evidenceSHA != manifestSHA {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("suitability projection evidence does not bind the challenge manifest")
	}
	comparison, comparisonSHA, err := loadTemporalStructureHoldoutSuitability(config.SuitabilityComparisonPath, evidence, evidenceSHA)
	if err != nil {
		return temporalSuitabilityProjectionLoaded{}, err
	}
	first, firstSHA, err := loadTemporalSuitabilityResultAuthority(config.FirstResultPath, evidence, evidenceSHA, config.ExpectedCases)
	if err != nil {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("first suitability result: %w", err)
	}
	second, secondSHA, err := loadTemporalSuitabilityResultAuthority(config.SecondResultPath, evidence, evidenceSHA, config.ExpectedCases)
	if err != nil {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("second suitability result: %w", err)
	}
	if comparison.FirstResultSHA256 != firstSHA || comparison.SecondResultSHA256 != secondSHA || comparison.FirstAssessor != first.Assessor || comparison.SecondAssessor != second.Assessor || first.SelectionSHA256 != second.SelectionSHA256 {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("suitability projection results do not bind the locked comparison")
	}
	recomputed, err := CompareTemporalSuitabilityResults(first, second, evidenceSHA, first.SelectionSHA256, firstSHA, secondSHA, comparison.ComparedAt)
	if err != nil {
		return temporalSuitabilityProjectionLoaded{}, err
	}
	if hashJSON(recomputed) != hashJSON(comparison) {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("suitability projection comparison does not reproduce from its results")
	}
	if config.ProjectedAt.Before(comparison.ComparedAt) || config.ProjectedAt.Before(first.CompletedAt) || config.ProjectedAt.Before(second.CompletedAt) {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("suitability projection predates its authority")
	}
	return temporalSuitabilityProjectionLoaded{
		manifest: manifest, authority: authority, comparison: comparison, first: first, second: second,
		manifestSHA: manifestSHA, authoritySHA: authoritySHA, comparisonSHA: comparisonSHA, firstSHA: firstSHA, secondSHA: secondSHA,
	}, nil
}
