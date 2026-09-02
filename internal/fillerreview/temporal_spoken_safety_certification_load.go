package fillerreview

import (
	"fmt"
	"strings"
	"time"
)

type temporalSpokenSafetyCertificationLoaded struct {
	authority     TemporalSpokenSafetyChallengeAuthority
	projection    TemporalSpokenSafetyReport
	sources       map[string]TemporalSpokenSafetySourceDisposition
	authoritySHA  string
	projectionSHA string
}

func loadTemporalSpokenSafetyCertification(config TemporalSpokenSafetyCertificationConfig) (temporalSpokenSafetyCertificationLoaded, error) {
	if strings.TrimSpace(config.AuthorityPath) == "" || strings.TrimSpace(config.SpokenSafetyReportPath) == "" || strings.TrimSpace(config.OutputPath) == "" || config.ScoredAt.IsZero() {
		return temporalSpokenSafetyCertificationLoaded{}, fmt.Errorf("spoken-safety certification requires authority, projection, fixed score time, and output")
	}
	projection, err := readStrictJSON[TemporalSpokenSafetyReport](config.SpokenSafetyReportPath)
	if err != nil {
		return temporalSpokenSafetyCertificationLoaded{}, fmt.Errorf("read spoken-safety projection: %w", err)
	}
	if err := validateTemporalSpokenSafetyReport(projection); err != nil {
		return temporalSpokenSafetyCertificationLoaded{}, fmt.Errorf("validate spoken-safety projection: %w", err)
	}
	projectionSHA, err := hashFile(config.SpokenSafetyReportPath)
	if err != nil {
		return temporalSpokenSafetyCertificationLoaded{}, err
	}
	authority, err := readStrictJSON[TemporalSpokenSafetyChallengeAuthority](config.AuthorityPath)
	if err != nil {
		return temporalSpokenSafetyCertificationLoaded{}, fmt.Errorf("read spoken-safety challenge authority: %w", err)
	}
	if err := validateTemporalSpokenSafetyChallengeAuthority(authority, projection, config.ScoredAt); err != nil {
		return temporalSpokenSafetyCertificationLoaded{}, err
	}
	authoritySHA, err := hashFile(config.AuthorityPath)
	if err != nil {
		return temporalSpokenSafetyCertificationLoaded{}, err
	}
	sources := make(map[string]TemporalSpokenSafetySourceDisposition, len(projection.SourceDispositions))
	for _, source := range projection.SourceDispositions {
		if _, duplicate := sources[source.SourceSHA256]; duplicate {
			return temporalSpokenSafetyCertificationLoaded{}, fmt.Errorf("spoken-safety projection repeats source content authority")
		}
		sources[source.SourceSHA256] = source
	}
	return temporalSpokenSafetyCertificationLoaded{authority: authority, projection: projection, sources: sources, authoritySHA: authoritySHA, projectionSHA: projectionSHA}, nil
}

func validateTemporalSpokenSafetyChallengeAuthority(authority TemporalSpokenSafetyChallengeAuthority, projection TemporalSpokenSafetyReport, scoredAt time.Time) error {
	if authority.SchemaVersion != TemporalSpokenSafetyCertificationSchemaVersion || authority.ContractVersion != TemporalSpokenSafetyCertificationContractVersion || authority.AuthoredAt.IsZero() || authority.AuthoredAt.After(projection.ProjectedAt) || scoredAt.Before(projection.ProjectedAt) || authority.ChallengeKind != TemporalSpokenSafetyChallengeDevelopment && authority.ChallengeKind != TemporalSpokenSafetyChallengeCertification || authority.CorpusManifestSHA256 != projection.CorpusManifestSHA256 || authority.PolicySHA256 != projection.PolicySHA256 || len(authority.Cases) == 0 {
		return fmt.Errorf("spoken-safety challenge does not bind its projection, policy, and time")
	}
	// Further case-level validation is kept beside scoring vocabulary.
	return validateTemporalSpokenSafetyChallengeCases(authority.Cases, projection)
}
