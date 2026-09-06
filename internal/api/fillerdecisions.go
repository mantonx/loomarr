package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/loomarr/loomarr/internal/filleradmission"
	"github.com/loomarr/loomarr/internal/fillerdecision"
	"github.com/loomarr/loomarr/internal/store"
)

type fillerDecisionCountsDTO struct {
	Admitted          int `json:"admitted"`
	Rejected          int `json:"rejected"`
	Reviews           int `json:"reviews"`
	Operational       int `json:"operational"`
	Retryable         int `json:"retryable"`
	UnresolvedReviews int `json:"unresolvedReviews"`
}

type fillerDecisionOverviewDTO struct {
	Healthy     bool                      `json:"healthy"`
	NextAction  fillerdecision.NextAction `json:"nextAction" enum:"none,repair_processing,retry_processing,review_decisions"`
	ActionCount int                       `json:"actionCount,omitempty"`
	Counts      fillerDecisionCountsDTO   `json:"counts"`
}

type fillerDecisionOverviewOutput struct{ Body fillerDecisionOverviewDTO }

type fillerDecisionPageInput struct {
	Limit    int       `query:"limit" default:"100" minimum:"1" maximum:"100"`
	BeforeAt time.Time `query:"beforeAt,omitempty" doc:"Created-at value from the last row of the previous page"`
	BeforeID string    `query:"beforeId,omitempty" maxLength:"137" doc:"Opaque id from the last row of the previous page"`
}

type fillerDecisionReviewDTO struct {
	ID              string                         `json:"id"`
	ClipHash        string                         `json:"clipHash"`
	Question        string                         `json:"question"`
	ApplicationMode fillerdecision.ApplicationMode `json:"applicationMode" enum:"shadow,applied"`
	ReasonCodes     []filleradmission.ReasonCode   `json:"reasonCodes"`
	EvidenceRefs    []string                       `json:"evidenceRefs"`
	Conflicts       []filleradmission.Conflict     `json:"conflicts"`
	CreatedAt       time.Time                      `json:"createdAt"`
}

type fillerDecisionReviewsOutput struct {
	Body struct {
		Rows  []fillerDecisionReviewDTO `json:"rows"`
		Total int                       `json:"total"`
	}
}

type fillerDecisionDiagnosticDTO struct {
	ID        string                          `json:"id"`
	ClipHash  string                          `json:"clipHash"`
	Code      filleradmission.OperationalCode `json:"code"`
	Retryable bool                            `json:"retryable"`
	Recovery  fillerdecision.RecoveryAction   `json:"recovery" enum:"configure_provider,adjust_budget,retry_extraction,inspect_media,update_policy"`
	CreatedAt time.Time                       `json:"createdAt"`
}

type fillerDecisionDiagnosticsOutput struct {
	Body struct {
		Rows  []fillerDecisionDiagnosticDTO `json:"rows"`
		Total int                           `json:"total"`
	}
}

// Marshal-facing fields are separate so Huma documents each name rather than
// flattening a compact Go declaration into an opaque schema.
type fillerDecisionActivityWireDTO struct {
	ID              string                         `json:"id"`
	ActionID        string                         `json:"actionId,omitempty"`
	DecisionID      string                         `json:"decisionId"`
	ClipHash        string                         `json:"clipHash"`
	Kind            fillerdecision.ActivityKind    `json:"kind" enum:"automatic_admit,automatic_reject,review_requested,review_admit,review_reject,correction,review_abandoned,restore,reversal"`
	ApplicationMode fillerdecision.ApplicationMode `json:"applicationMode" enum:"shadow,applied"`
	CreatedAt       time.Time                      `json:"createdAt"`
}

type fillerDecisionActivityOutput struct {
	Body struct {
		Rows  []fillerDecisionActivityWireDTO `json:"rows"`
		Total int                             `json:"total"`
	}
}

type fillerDecisionActionInput struct {
	ID   string `path:"id" maxLength:"128"`
	Body struct {
		ActionID         string                    `json:"actionId" maxLength:"128"`
		Kind             fillerdecision.ActionKind `json:"kind" enum:"admit,reject,correct,abandon,restore,reverse"`
		Reason           string                    `json:"reason,omitempty" maxLength:"512"`
		Answer           string                    `json:"answer,omitempty" maxLength:"512"`
		CorrectedVerdict filleradmission.Verdict   `json:"correctedVerdict,omitempty" enum:"admit,reject"`
		SupersedesID     string                    `json:"supersedesId,omitempty" maxLength:"128"`
	}
}

type fillerDecisionActionOutput struct {
	Body struct {
		ID string `json:"id"`
	}
}

func (s *Server) registerFillerDecisions(api huma.API) {
	huma.Register(api, withRole(huma.Operation{
		OperationID: "filler-decision-overview", Method: http.MethodGet, Path: "/v1/filler/decisions/overview",
		Summary: "Admission decision health and next action", Description: "Member-readable server-owned V63 health and priority projection. Clients render nextAction; they do not reconstruct it from counts.", Tags: []string{"filler"},
	}, RoleMember), s.fillerDecisionOverview)
	huma.Register(api, withRole(huma.Operation{
		OperationID: "filler-decision-reviews", Method: http.MethodGet, Path: "/v1/filler/decisions/reviews",
		Summary: "Semantic filler decisions needing attention", Description: "Admin-only bounded semantic exceptions. Every row asks exactly one question and excludes machine work and provider failures (§10 V63).", Tags: []string{"filler"},
	}, RoleAdmin), s.fillerDecisionReviews)
	huma.Register(api, withRole(huma.Operation{
		OperationID: "filler-decision-activity", Method: http.MethodGet, Path: "/v1/filler/decisions/activity",
		Summary: "Filler admission decision activity", Description: "Member-readable bounded audit of automatic semantic decisions and subsequent operator actions (§10 V63).", Tags: []string{"filler"},
	}, RoleMember), s.fillerDecisionActivity)
	huma.Register(api, withRole(huma.Operation{
		OperationID: "filler-decision-diagnostics", Method: http.MethodGet, Path: "/v1/filler/decisions/diagnostics",
		Summary: "Filler admission processing diagnostics", Description: "Admin-only operational holds. Raw provider responses, prompts, paths, evidence locations, and secrets are never projected (§10 V63).", Tags: []string{"filler"},
	}, RoleAdmin), s.fillerDecisionDiagnostics)
	huma.Register(api, withRole(huma.Operation{
		OperationID: "act-on-filler-decision", Method: http.MethodPost, Path: "/v1/filler/decisions/{id}/actions",
		Summary: "Resolve or reverse a filler decision", Description: "Admin-only append-only action. The server records the authenticated actor and rejects stale or invalid state transitions (§10 V63).", Tags: []string{"filler"},
	}, RoleAdmin), s.actOnFillerDecision)
}

func (s *Server) fillerDecisionOverview(ctx context.Context, _ *struct{}) (*fillerDecisionOverviewOutput, error) {
	if s.fillerDecisions == nil {
		return nil, errFeatureNotConfigured("Filler decision audit unavailable", "The durable filler decision service is not configured.")
	}
	overview, err := s.fillerDecisions.Overview(ctx)
	if err != nil {
		return nil, err
	}
	return &fillerDecisionOverviewOutput{Body: fillerDecisionOverviewDTO{
		Healthy: overview.Healthy, NextAction: overview.Next, ActionCount: overview.ActionCount,
		Counts: fillerDecisionCountsDTO{
			Admitted: overview.Counts.Admitted, Rejected: overview.Counts.Rejected,
			Reviews: overview.Counts.Reviews, Operational: overview.Counts.Operational,
			Retryable: overview.Counts.Retryable, UnresolvedReviews: overview.Counts.UnresolvedReviews,
		},
	}}, nil
}

func (s *Server) fillerDecisionReviews(ctx context.Context, in *fillerDecisionPageInput) (*fillerDecisionReviewsOutput, error) {
	if s.fillerDecisions == nil {
		return nil, errFeatureNotConfigured("Filler decision audit unavailable", "The durable filler decision service is not configured.")
	}
	page, err := s.fillerDecisions.Reviews(ctx, decisionCursor(in), in.Limit)
	if err != nil {
		return nil, fillerDecisionError(err)
	}
	out := &fillerDecisionReviewsOutput{}
	out.Body.Total = page.Total
	out.Body.Rows = make([]fillerDecisionReviewDTO, 0, len(page.Rows))
	for _, item := range page.Rows {
		out.Body.Rows = append(out.Body.Rows, fillerDecisionReviewDTO{
			ID: item.ID, ClipHash: item.ClipHash, Question: item.Question,
			ApplicationMode: item.ApplicationMode,
			ReasonCodes:     append([]filleradmission.ReasonCode{}, item.ReasonCodes...),
			EvidenceRefs:    append([]string{}, item.EvidenceRefs...),
			Conflicts:       append([]filleradmission.Conflict{}, item.Conflicts...), CreatedAt: item.CreatedAt,
		})
	}
	return out, nil
}

func (s *Server) fillerDecisionDiagnostics(ctx context.Context, in *fillerDecisionPageInput) (*fillerDecisionDiagnosticsOutput, error) {
	if s.fillerDecisions == nil {
		return nil, errFeatureNotConfigured("Filler decision audit unavailable", "The durable filler decision service is not configured.")
	}
	page, err := s.fillerDecisions.Diagnostics(ctx, decisionCursor(in), in.Limit)
	if err != nil {
		return nil, fillerDecisionError(err)
	}
	out := &fillerDecisionDiagnosticsOutput{}
	out.Body.Total = page.Total
	out.Body.Rows = make([]fillerDecisionDiagnosticDTO, 0, len(page.Rows))
	for _, item := range page.Rows {
		out.Body.Rows = append(out.Body.Rows, fillerDecisionDiagnosticDTO{
			ID: item.ID, ClipHash: item.ClipHash, Code: item.Code,
			Retryable: item.Retryable, Recovery: item.Recovery, CreatedAt: item.CreatedAt,
		})
	}
	return out, nil
}

func (s *Server) fillerDecisionActivity(ctx context.Context, in *fillerDecisionPageInput) (*fillerDecisionActivityOutput, error) {
	if s.fillerDecisions == nil {
		return nil, errFeatureNotConfigured("Filler decision audit unavailable", "The durable filler decision service is not configured.")
	}
	page, err := s.fillerDecisions.Activity(ctx, decisionCursor(in), in.Limit)
	if err != nil {
		return nil, fillerDecisionError(err)
	}
	out := &fillerDecisionActivityOutput{}
	out.Body.Total = page.Total
	out.Body.Rows = make([]fillerDecisionActivityWireDTO, 0, len(page.Rows))
	for _, item := range page.Rows {
		out.Body.Rows = append(out.Body.Rows, fillerDecisionActivityWireDTO{
			ID: item.ID, ActionID: item.ActionID, DecisionID: item.DecisionID, ClipHash: item.ClipHash,
			Kind: item.Kind, ApplicationMode: item.ApplicationMode, CreatedAt: item.CreatedAt,
		})
	}
	return out, nil
}

func (s *Server) actOnFillerDecision(ctx context.Context, in *fillerDecisionActionInput) (*fillerDecisionActionOutput, error) {
	if s.fillerDecisions == nil {
		return nil, errFeatureNotConfigured("Filler decision audit unavailable", "The durable filler decision service is not configured.")
	}
	actor := userIDFromHuma(ctx)
	if actor == "" {
		actor = "api-token"
	}
	action := fillerdecision.Action{
		ID: strings.TrimSpace(in.Body.ActionID), DecisionID: in.ID, Kind: in.Body.Kind,
		ActorID: actor, Reason: strings.TrimSpace(in.Body.Reason), Answer: strings.TrimSpace(in.Body.Answer),
		CorrectedVerdict: in.Body.CorrectedVerdict, SupersedesID: in.Body.SupersedesID, CreatedAt: time.Now().UTC(),
	}
	if err := s.fillerDecisions.Act(ctx, action); err != nil {
		return nil, fillerDecisionError(err)
	}
	out := &fillerDecisionActionOutput{}
	out.Body.ID = action.ID
	return out, nil
}

func decisionCursor(in *fillerDecisionPageInput) fillerdecision.Cursor {
	return fillerdecision.Cursor{BeforeCreatedAt: in.BeforeAt, BeforeID: in.BeforeID}
}

func fillerDecisionError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("Filler decision not found.")
	case errors.Is(err, fillerdecision.ErrInvalid):
		return huma.Error422UnprocessableEntity("Invalid filler decision request", err)
	case errors.Is(err, fillerdecision.ErrConflict), errors.Is(err, fillerdecision.ErrActionStale):
		return huma.Error409Conflict("The filler decision changed; reload before trying again.")
	case errors.Is(err, fillerdecision.ErrActionNotAllowed):
		return huma.Error409Conflict("That action is not valid for the decision's current state.")
	default:
		return err
	}
}
