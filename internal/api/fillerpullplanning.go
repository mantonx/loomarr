package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/loomarr/loomarr/internal/filler"
)

type proposeFillerPullInput struct {
	Body struct {
		Title  string `json:"title,omitempty" doc:"Optional operator-supplied summary; Loomarr composes one when omitted"`
		Reason string `json:"reason,omitempty" doc:"Optional; the gap this pull closes"`
		Intent struct {
			Roles           []filler.Kind           `json:"roles,omitempty"`
			EraStart        int                     `json:"eraStart,omitempty" minimum:"1800" maximum:"2200"`
			EraEnd          int                     `json:"eraEnd,omitempty" minimum:"1800" maximum:"2200"`
			Audiences       []filler.Audience       `json:"audiences,omitempty"`
			Geography       filler.Geography        `json:"geography,omitempty"`
			MaxDurationMS   int                     `json:"maxDurationMs,omitempty" minimum:"0"`
			TaxonomyGaps    []string                `json:"taxonomyGaps,omitempty"`
			Rights          filler.RightsPreference `json:"rights,omitempty" enum:"any,prefer_declared,require_declared"`
			SourceAllowlist []string                `json:"sourceAllowlist,omitempty"`
			MinHeight       int                     `json:"minHeight,omitempty" minimum:"0" maximum:"4320"`
			Count           int                     `json:"count,omitempty" minimum:"1" maximum:"50"`
		} `json:"intent,omitempty"`
	}
}

// proposeFillerPull composes a plan and writes it to the queue. It downloads nothing.
func (s *Server) proposeFillerPull(ctx context.Context, in *proposeFillerPullInput) (*pullOutput, error) {
	if s.store == nil {
		return nil, huma.Error501NotImplemented("no store configured")
	}

	planner, ok := s.filler.(FillerPullPlanner)
	if !ok {
		return nil, huma.Error501NotImplemented("filler acquisition planning is not configured")
	}
	intent := filler.AcquisitionIntent{
		Roles: in.Body.Intent.Roles, EraStart: in.Body.Intent.EraStart, EraEnd: in.Body.Intent.EraEnd,
		Audiences: in.Body.Intent.Audiences, Geography: in.Body.Intent.Geography,
		MaxDurationMS: in.Body.Intent.MaxDurationMS, TaxonomyGaps: in.Body.Intent.TaxonomyGaps,
		Rights: in.Body.Intent.Rights, SourceAllowlist: in.Body.Intent.SourceAllowlist,
		MinHeight: in.Body.Intent.MinHeight, Count: in.Body.Intent.Count,
		CatalogReason: strings.TrimSpace(in.Body.Reason),
	}
	if intent.Geography.Normalize().Country == "" {
		intent.Geography = s.fillerHomeGeography()
	}
	planResult, err := planner.PlanAcquisition(ctx, intent)
	if errors.Is(err, filler.ErrNoAcquisitionSources) {
		return nil, errConflict("No eligible filler source is available",
			"Loomarr can't plan a pull because every source is off, unclassified, or outside this installation's geography. Review Filler → Sources, then try again.")
	}
	if errors.Is(err, filler.ErrNoAcquisitionCandidates) {
		return nil, errConflict("No remote item satisfies this acquisition intent",
			"The sources were inspected without downloading, but every candidate was already known or failed a requested constraint. Widen the intent or add another source.")
	}
	if errors.Is(err, filler.ErrInvalidAcquisitionIntent) {
		return nil, errUnprocessable("Invalid filler acquisition intent", err.Error())
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("plan filler acquisition", err)
	}

	now := time.Now().UTC()
	plan := make([]filler.PullPlanRow, 0, len(planResult.Selected))
	for _, decision := range planResult.Selected {
		candidate := decision.Candidate
		tag := candidate.Identity.Provider
		if candidate.ObservedYear > 0 {
			tag = fmt.Sprint(candidate.ObservedYear)
		}
		plan = append(plan, filler.PullPlanRow{
			SourceID: candidate.Identity.SourceID, Provider: candidate.Identity.Provider,
			RemoteID: candidate.Identity.RemoteID, URL: candidate.URL, Tag: tag,
			Name: orPlaceholder(candidate.Title, candidate.Identity.RemoteID), Why: decision.Detail,
			License: candidate.License, ObservedYear: candidate.ObservedYear,
			PublishedAt: candidate.PublishedAt, DurationMS: candidate.DurationMS,
			Height: candidate.Height, Geography: candidate.Geography,
			// One remote item may be a compilation that yields many clips. Without inspecting its
			// media, zero is the only honest clip forecast; candidateCount remains exact.
			EstimateClips: 0,
		})
	}

	p := filler.Pull{
		ID:         "pull_" + fmt.Sprintf("%d", now.UnixNano()),
		Title:      orPlaceholder(strings.TrimSpace(in.Body.Title), fmt.Sprintf("Acquire %d selected filler items", len(plan))),
		Reason:     planResult.Intent.CatalogReason,
		ProposedBy: auditActor(ctx),
		Status:     filler.PullPending,
		Intent:     planResult.Intent,
		Plan:       plan,
		Rejected:   planResult.Rejected,
		Sources:    planResult.Sources,
		CreatedAt:  now,
	}
	if err := s.store.UpsertPull(ctx, p); err != nil {
		return nil, huma.Error500InternalServerError("save pull", err)
	}
	return &pullOutput{Body: pullToDTO(p)}, nil
}
