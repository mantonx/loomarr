//go:build eval

package eval

import (
	"fmt"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/buildinfo"
	"github.com/loomarr/loomarr/internal/quality"
)

// buildScorecardRunSnapshot projects only bounded, comparison-relevant facts
// from one completed scorecard. Resolution stays empty unless every observed
// generator call reports the same concrete model; mixed or missing routes are
// evidence gaps, not permission to copy the requested model.
func buildScorecardRunSnapshot(card Scorecard, accountingAvailable bool) *quality.RunSnapshot {
	if card.CorpusVersion == "" || card.Generator.Model == "" || card.Profile == "" || card.GeneratedAt.IsZero() {
		return nil
	}
	createdAt := card.GeneratedAt.UTC().Truncate(time.Second)
	snapshot := quality.RunSnapshot{
		SchemaVersion:       quality.RunSnapshotSchemaVersion,
		CorpusVersion:       card.CorpusVersion,
		RequestedModel:      card.Generator.Model,
		ResolvedModel:       unanimousResolvedGeneratorModel(card.Results),
		Provider:            qualityProvider(card.Generator.Provider),
		BudgetProfile:       card.Profile,
		ApplicationVersion:  scorecardApplicationVersion(),
		AccountingAvailable: accountingAvailable,
		CreatedAt:           createdAt,
	}
	snapshot.ID = quality.RunSnapshotID(snapshot)
	if err := snapshot.Validate(); err != nil {
		return nil
	}
	return &snapshot
}

func validateScorecardRunSnapshot(card Scorecard) error {
	if card.SchemaVersion == 10 || card.SchemaVersion == 11 {
		return nil
	}
	if card.SchemaVersion != scorecardSchemaVersion {
		return fmt.Errorf("unsupported scorecard schema %d", card.SchemaVersion)
	}
	if card.RunSnapshot == nil {
		return fmt.Errorf("schema-v%d scorecard lacks its run snapshot", card.SchemaVersion)
	}
	snapshot := *card.RunSnapshot
	if err := snapshot.Validate(); err != nil {
		return err
	}
	if snapshot.ID != quality.RunSnapshotID(snapshot) || snapshot.CorpusVersion != card.CorpusVersion ||
		snapshot.RequestedModel != card.Generator.Model || snapshot.ResolvedModel != unanimousResolvedGeneratorModel(card.Results) ||
		snapshot.Provider != qualityProvider(card.Generator.Provider) || snapshot.BudgetProfile != card.Profile ||
		!snapshot.CreatedAt.Equal(card.GeneratedAt.UTC().Truncate(time.Second)) {
		return fmt.Errorf("scorecard run snapshot does not match its evaluation facts")
	}
	return nil
}

func unanimousResolvedGeneratorModel(results []Result) string {
	resolved := ""
	observed := false
	for _, result := range results {
		for _, call := range result.GeneratorCalls {
			if call.ResolvedModel == "" {
				return ""
			}
			if !observed {
				resolved = call.ResolvedModel
				observed = true
				continue
			}
			if call.ResolvedModel != resolved {
				return ""
			}
		}
	}
	return resolved
}

func qualityProvider(provider string) quality.Provider {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "":
		return quality.ProviderUnknown
	case "ollama":
		return quality.ProviderOllama
	case "openrouter":
		return quality.ProviderOpenRouter
	default:
		return quality.ProviderCustom
	}
}

func scorecardApplicationVersion() string {
	info := buildinfo.Get()
	version := info.Version
	if info.Commit != "" {
		version += "+" + info.Commit
	}
	if info.Dirty {
		version += ".dirty"
	}
	return version
}
