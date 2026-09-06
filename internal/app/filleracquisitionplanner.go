package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/store"
)

const (
	maxAcquisitionPlanningSources    = 12
	maxAcquisitionPlanningCandidates = 100
	acquisitionPlanningTimeout       = 90 * time.Second
)

type fillerPullPlanningStore interface {
	ListFillerSources(context.Context) ([]store.FillerSource, error)
	ListPulls(context.Context, filler.PullStatus) ([]filler.Pull, error)
	ListAcquisitionRemoteStates(context.Context) (map[string]filler.ExistingRemoteState, error)
}

// PlanAcquisition is the application-owned orchestration around the pure deterministic planner.
// Enumeration is metadata-only; this method cannot reach the downloader or admission pipeline.
func (a fillerServiceAdapter) PlanAcquisition(ctx context.Context, intent filler.AcquisitionIntent) (filler.AcquisitionPlan, error) {
	if a.pullPlanning == nil || a.sourceEnum == nil {
		return filler.AcquisitionPlan{}, errors.New("filler acquisition planner is not configured")
	}
	if intent.Geography.Normalize().Country == "" && a.home != nil {
		intent.Geography = a.home()
	}
	intent = intent.Normalize()
	if err := intent.Validate(); err != nil {
		return filler.AcquisitionPlan{}, fmt.Errorf("%w: %v", filler.ErrInvalidAcquisitionIntent, err)
	}
	planningCtx, cancel := context.WithTimeout(ctx, acquisitionPlanningTimeout)
	defer cancel()

	if strings.TrimSpace(intent.CatalogReason) == "" {
		if a.pool != nil {
			pool, err := a.pool(planningCtx)
			if err != nil {
				return filler.AcquisitionPlan{}, fmt.Errorf("derive acquisition coverage intent: %w", err)
			}
			defaults := filler.DefaultAcquisitionIntent(pool, intent.Geography)
			intent.CatalogReason = defaults.CatalogReason
		} else {
			intent.CatalogReason = "Increase the eligible filler catalog."
		}
	}

	sources, err := a.pullPlanning.ListFillerSources(planningCtx)
	if err != nil {
		return filler.AcquisitionPlan{}, fmt.Errorf("list acquisition sources: %w", err)
	}
	allPulls, err := a.pullPlanning.ListPulls(planningCtx, "")
	if err != nil {
		return filler.AcquisitionPlan{}, fmt.Errorf("list acquisition decisions: %w", err)
	}
	existing := existingRemoteStates(allPulls, intent)
	artifactStates, err := a.pullPlanning.ListAcquisitionRemoteStates(planningCtx)
	if err != nil {
		return filler.AcquisitionPlan{}, fmt.Errorf("list acquired remote identities: %w", err)
	}
	for key, state := range artifactStates {
		if existing[key] != filler.RemoteCatalogued || state == filler.RemoteCatalogued {
			existing[key] = state
		}
	}

	type eligibleSource struct {
		source        store.FillerSource
		decisionIndex int
	}
	decisions := make([]filler.AcquisitionSourceDecision, 0, len(sources))
	eligible := make([]eligibleSource, 0, len(sources))
	for _, source := range sources {
		decision := filler.AcquisitionSourceDecision{
			SourceID: source.ID, Provider: source.Kind, Label: source.Label, Geography: source.Geography,
		}
		switch {
		case !source.Enabled:
			decision.Disposition, decision.Detail = filler.AcquisitionSourceDisabled, "registered source is disabled"
		case !source.Fetchable():
			decision.Disposition, decision.Detail = filler.AcquisitionSourceNotFetchable, "registered source cannot be downloaded from"
		case len(intent.SourceAllowlist) > 0 && !containsSourceID(intent.SourceAllowlist, source.ID):
			decision.Disposition, decision.Detail = filler.AcquisitionSourceNotAllowed, "registered source is outside the intent allow-list"
		case !source.GeographicallyEligible(intent.Geography):
			decision.Disposition, decision.Detail = filler.AcquisitionSourceGeographyMismatch, "registered source does not cover the target geography"
		default:
			eligible = append(eligible, eligibleSource{source: source, decisionIndex: len(decisions)})
		}
		decisions = append(decisions, decision)
	}
	if len(eligible) == 0 {
		return filler.AcquisitionPlan{}, filler.ErrNoAcquisitionSources
	}
	if len(eligible) > maxAcquisitionPlanningSources {
		for _, skipped := range eligible[maxAcquisitionPlanningSources:] {
			decisions[skipped.decisionIndex].Disposition = filler.AcquisitionSourceLimitExceeded
			decisions[skipped.decisionIndex].Detail = fmt.Sprintf("outside the deterministic %d-source planning bound", maxAcquisitionPlanningSources)
		}
		eligible = eligible[:maxAcquisitionPlanningSources]
	}

	candidates := make([]filler.AcquisitionCandidate, 0, maxAcquisitionPlanningCandidates)
	for sourceIndex, eligibleSource := range eligible {
		if err := planningCtx.Err(); err != nil {
			return filler.AcquisitionPlan{}, fmt.Errorf("filler acquisition planning exceeded %s: %w", acquisitionPlanningTimeout, err)
		}
		source := eligibleSource.source
		limit := maxAcquisitionPlanningCandidates / len(eligible)
		if sourceIndex < maxAcquisitionPlanningCandidates%len(eligible) {
			limit++
		}
		items, _, enumerateErr := a.sourceEnum.Enumerate(planningCtx, filler.FetchSource{
			ID: source.ID, Kind: source.Kind, URI: source.URI, Enabled: source.Enabled,
		}, limit)
		if enumerateErr != nil {
			decisions[eligibleSource.decisionIndex].Disposition = filler.AcquisitionSourceEnumerationFailed
			decisions[eligibleSource.decisionIndex].Detail = enumerateErr.Error()
			continue
		}
		if len(items) > limit {
			items = items[:limit]
		}
		decisions[eligibleSource.decisionIndex].Disposition = filler.AcquisitionSourceEnumerated
		decisions[eligibleSource.decisionIndex].CandidateCount = len(items)
		decisions[eligibleSource.decisionIndex].Detail = fmt.Sprintf("enumerated %d metadata-only candidates within a %d-item source quota", len(items), limit)
		for _, item := range items {
			license := item.License
			if license == "" {
				license = source.License
			}
			candidates = append(candidates, filler.AcquisitionCandidate{
				Identity: filler.RemoteIdentity{Provider: source.Kind, SourceID: source.ID, RemoteID: item.ID},
				URL:      item.URL, Title: item.Title, License: license,
				ObservedYear: item.ObservedYear, PublishedAt: item.PublishedAt,
				DurationMS: item.DurationMS, Height: item.Height, Geography: source.Geography,
			})
		}
	}
	plan, err := filler.PlanAcquisition(intent, candidates, existing)
	if err != nil {
		return filler.AcquisitionPlan{}, err
	}
	plan.Sources = decisions
	if len(plan.Selected) == 0 {
		return plan, filler.ErrNoAcquisitionCandidates
	}
	return plan, nil
}

func existingRemoteStates(pulls []filler.Pull, intent filler.AcquisitionIntent) map[string]filler.ExistingRemoteState {
	out := map[string]filler.ExistingRemoteState{}
	for _, pull := range pulls {
		for _, row := range pull.Plan {
			state := filler.RemoteQueued
			if pull.Status == filler.PullDismissed || row.Dropped {
				if pull.Intent.FamilyKey() != intent.FamilyKey() {
					continue
				}
				state = filler.RemoteDeclined
			}
			identity := row.Identity()
			if identity.Validate() != nil {
				continue // a historical V35 source-level row has no candidate identity
			}
			key := identity.Key()
			if out[key] != filler.RemoteQueued {
				out[key] = state
			}
		}
	}
	return out
}

func containsSourceID(ids []string, candidate string) bool {
	for _, id := range ids {
		if strings.EqualFold(strings.TrimSpace(id), candidate) {
			return true
		}
	}
	return false
}
