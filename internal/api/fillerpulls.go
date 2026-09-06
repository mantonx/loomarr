package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/loomarr/loomarr/internal/filler"
)

// Filler pulls — the approval gate for filler acquisition (§10 V35).
//
// ⚠ **The safety property this file exists for: a pending pull downloads NOTHING.** Proposing
// writes a row. Approving is the one place work is enqueued, and it enqueues through the
// EXISTING ingest path — a pull that downloaded through its own route would be a second
// implementation of ingest, which is the shape §10 rejects by name and the shape that let
// `filler_sources` ship with no reader.
//
// ⚠ **What the gate binds is composed plans, not an admin's own hands.** An admin searching one
// source and clicking "Queue download" on one result stays direct (`POST /v1/filler/ingest`),
// mirroring §7 where an admin may `POST /v1/titles` because the admin *is* the gate.

// FillerPullPlanner is the metadata-only candidate planner. It is intentionally narrower than
// FillerService: proposal cannot reach ingest, which preserves "the machine proposes" structurally.
type FillerPullPlanner interface {
	PlanAcquisition(context.Context, filler.AcquisitionIntent) (filler.AcquisitionPlan, error)
}

// PullPlanRowDTO is one exact remote item a pull would acquire.
type PullPlanRowDTO struct {
	CandidateID  string           `json:"candidateId" doc:"Stable handle used to drop this exact candidate"`
	SourceID     string           `json:"sourceId" doc:"The registered collection this item came from"`
	Provider     string           `json:"provider,omitempty"`
	RemoteID     string           `json:"remoteId,omitempty"`
	URL          string           `json:"url,omitempty" doc:"Exact remote item URL; absent only on historical source-level pulls"`
	License      string           `json:"license,omitempty" doc:"Provider/source-declared rights evidence; empty means unknown"`
	ObservedYear int              `json:"observedYear,omitempty" doc:"Weak remote observation; never grounded clip era"`
	PublishedAt  string           `json:"publishedAt,omitempty" doc:"Provider publication/upload date; never clip era"`
	DurationMS   int              `json:"durationMs,omitempty"`
	Height       int              `json:"height,omitempty"`
	Geography    filler.Geography `json:"geography,omitempty"`
	Tag          string           `json:"tag" doc:"Short label for the row's chip (an era, an audience)"`
	Name         string           `json:"name"`
	Why          string           `json:"why" doc:"Why THIS source is in the plan"`
	// ⚠ An estimate, never a promise: what a source yields depends on what is still there,
	// what deduplicates against the catalog, and what the splitter makes of a compilation.
	EstimateClips int  `json:"estimateClips" doc:"Expected clips from this row. An ESTIMATE — render it as one."`
	Dropped       bool `json:"dropped" doc:"The operator excluded this row before approving. Retained rather than removed, so the record shows what was proposed as well as what was agreed to."`
}

type PullRejectedCandidateDTO struct {
	CandidateID string `json:"candidateId"`
	SourceID    string `json:"sourceId"`
	Provider    string `json:"provider"`
	RemoteID    string `json:"remoteId"`
	Name        string `json:"name"`
	Disposition string `json:"disposition"`
	Detail      string `json:"detail"`
}

// PullDTO is a proposed acquisition awaiting a human.
type PullDTO struct {
	ID         string                             `json:"id"`
	Title      string                             `json:"title"`
	Reason     string                             `json:"reason" doc:"The gap this pull closes. Shown above the plan — 'approve this' without a reason is a button, not a decision."`
	ProposedBy string                             `json:"proposedBy"`
	Status     string                             `json:"status" enum:"pending,approved,dismissed"`
	Note       string                             `json:"note,omitempty" doc:"The operator's narrowing instruction, captured at approval"`
	Plan       []PullPlanRowDTO                   `json:"plan"`
	Intent     filler.AcquisitionIntent           `json:"intent"`
	Rejected   []PullRejectedCandidateDTO         `json:"rejected"`
	Sources    []filler.AcquisitionSourceDecision `json:"sources"`
	// EstimateClips totals the rows the operator has NOT dropped.
	EstimateClips  int    `json:"estimateClips"`
	CandidateCount int    `json:"candidateCount" doc:"Exact number of remote items still selected; unlike estimateClips this does not predict how many clips segmentation will yield."`
	CreatedAt      string `json:"createdAt" doc:"RFC3339"`
	DecidedAt      string `json:"decidedAt,omitempty" doc:"RFC3339; absent while pending"`
	DecidedBy      string `json:"decidedBy,omitempty"`
}

func pullToDTO(p filler.Pull) PullDTO {
	// Non-nil even when empty: a JSON `null` here would make every consumer guard before
	// iterating, and a pull with no rows is a real (refused) state, not a missing one.
	rows := make([]PullPlanRowDTO, 0, len(p.Plan))
	for _, r := range p.Plan {
		rows = append(rows, PullPlanRowDTO{
			CandidateID: r.CandidateID(), SourceID: r.SourceID, Provider: r.Provider,
			RemoteID: r.RemoteID, URL: r.URL, Tag: r.Tag, Name: r.Name, Why: r.Why,
			License: r.License, ObservedYear: r.ObservedYear, PublishedAt: r.PublishedAt,
			DurationMS: r.DurationMS, Height: r.Height, Geography: r.Geography,
			EstimateClips: r.EstimateClips, Dropped: r.Dropped,
		})
	}
	rejected := make([]PullRejectedCandidateDTO, 0, len(p.Rejected))
	for _, decision := range p.Rejected {
		candidate := decision.Candidate
		rejected = append(rejected, PullRejectedCandidateDTO{
			CandidateID: candidate.Identity.Token(), SourceID: candidate.Identity.SourceID,
			Provider: candidate.Identity.Provider, RemoteID: candidate.Identity.RemoteID,
			Name:        orPlaceholder(candidate.Title, candidate.Identity.RemoteID),
			Disposition: string(decision.Disposition), Detail: decision.Detail,
		})
	}
	sources := make([]filler.AcquisitionSourceDecision, len(p.Sources))
	copy(sources, p.Sources)
	dto := PullDTO{
		ID: p.ID, Title: p.Title, Reason: p.Reason, ProposedBy: p.ProposedBy,
		Status: string(p.Status), Note: p.Note, Plan: rows, Intent: p.Intent,
		Rejected: rejected, Sources: sources,
		EstimateClips:  p.EstimatedClips(),
		CandidateCount: len(p.Committed()),
		CreatedAt:      p.CreatedAt.UTC().Format(time.RFC3339),
		DecidedBy:      p.DecidedBy,
	}
	if !p.DecidedAt.IsZero() {
		dto.DecidedAt = p.DecidedAt.UTC().Format(time.RFC3339)
	}
	return dto
}

func (s *Server) registerFillerPulls(api huma.API) {
	huma.Register(api, withRole(huma.Operation{
		OperationID: "propose-filler-pull", Method: http.MethodPost, Path: "/v1/filler/pulls",
		Summary: "Propose a pull",
		Description: "Admin only (§10 V66). Enumerates enabled registered sources without downloading, " +
			"applies explicit acquisition intent, and persists exact selected item URLs plus rejected explanations. " +
			"⚠ **Downloads nothing** — the machine proposes and a human commits. Refused with 409 when no " +
			"source is eligible or no candidate satisfies the constraints.",
		Tags: []string{"filler"},
	}, RoleAdmin), s.proposeFillerPull)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "list-filler-pulls", Method: http.MethodGet, Path: "/v1/filler/pulls",
		Summary: "List pulls awaiting a decision",
		Description: "Admin only (§10 V35). `status` filters (pending | approved | dismissed); omit it for all. " +
			"Decided pulls are KEPT — the queue's History answers what was agreed to and who said so.",
		Tags: []string{"filler"},
	}, RoleAdmin), s.listFillerPulls)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "approve-filler-pull", Method: http.MethodPost, Path: "/v1/filler/pulls/{id}/approve",
		Summary: "Approve a pull — the commit point",
		Description: "Admin only (§10 V66). THE gate: this is the only path on which a pull downloads anything, " +
			"and it enqueues through the EXISTING ingest job rather than a route of its own. The body carries the " +
			"operator's edits — exact candidates dropped, and a note narrowing what to fetch. Dropped rows are recorded, not " +
			"erased. Approving an already-decided pull returns 409.",
		Tags: []string{"filler"},
	}, RoleAdmin), s.approveFillerPull)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "dismiss-filler-pull", Method: http.MethodPost, Path: "/v1/filler/pulls/{id}/dismiss",
		Summary:     "Decline a pull",
		Description: "Admin only (§10 V35). Records the decision and downloads nothing. The row is kept.",
		Tags:        []string{"filler"},
	}, RoleAdmin), s.dismissFillerPull)
}

type pullOutput struct {
	Body PullDTO
}

type listFillerPullsInput struct {
	Status string `query:"status" enum:"pending,approved,dismissed," doc:"Omit for all"`
}

type listFillerPullsOutput struct {
	Body struct {
		Pulls []PullDTO `json:"pulls"`
		Total int       `json:"total"`
	}
}

func (s *Server) listFillerPulls(ctx context.Context, in *listFillerPullsInput) (*listFillerPullsOutput, error) {
	if s.store == nil {
		return nil, huma.Error501NotImplemented("no store configured")
	}
	pulls, err := s.store.ListPulls(ctx, filler.PullStatus(in.Status))
	if err != nil {
		return nil, huma.Error500InternalServerError("list pulls", err)
	}
	out := &listFillerPullsOutput{}
	out.Body.Pulls = make([]PullDTO, 0, len(pulls))
	for _, p := range pulls {
		out.Body.Pulls = append(out.Body.Pulls, pullToDTO(p))
	}
	out.Body.Total = len(out.Body.Pulls)
	return out, nil
}

func (s *Server) fillerHomeGeography() filler.Geography {
	if s.liveConfig == nil {
		return filler.Geography{}
	}
	return filler.Geography{
		Country: s.liveConfig("filler.home_country"),
		Market:  s.liveConfig("filler.home_market"),
	}.Normalize()
}
