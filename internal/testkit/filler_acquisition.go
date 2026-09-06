package testkit

import (
	"context"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/store"
)

// FillerAcquisitionPlanner is a shared API-test fixture that keeps source-policy
// checks and acquisition ranking real while allowing a test to supply discovered
// candidates where that is the subject under test.
type FillerAcquisitionPlanner struct {
	Store      store.Store
	Candidates []filler.AcquisitionCandidate
}

func (p FillerAcquisitionPlanner) PlanAcquisition(ctx context.Context, intent filler.AcquisitionIntent) (filler.AcquisitionPlan, error) {
	if intent.CatalogReason == "" {
		intent.CatalogReason = "Increase the eligible filler catalog."
	}
	intent = intent.Normalize()
	candidates := append([]filler.AcquisitionCandidate(nil), p.Candidates...)
	if len(candidates) == 0 && p.Store != nil {
		sources, err := p.Store.ListFillerSources(ctx)
		if err != nil {
			return filler.AcquisitionPlan{}, err
		}
		for _, source := range sources {
			if !source.Enabled || !source.Fetchable() || !source.GeographicallyEligible(intent.Geography) {
				continue
			}
			candidates = append(candidates, filler.AcquisitionCandidate{
				Identity: filler.RemoteIdentity{Provider: source.Kind, SourceID: source.ID, RemoteID: source.ID},
				URL:      source.URI, Title: source.Label, License: source.License, Geography: source.Geography,
			})
		}
	}
	if len(candidates) == 0 {
		return filler.AcquisitionPlan{}, filler.ErrNoAcquisitionSources
	}
	plan, err := filler.PlanAcquisition(intent, candidates, nil)
	if err == nil && len(plan.Selected) == 0 {
		err = filler.ErrNoAcquisitionCandidates
	}
	return plan, err
}
