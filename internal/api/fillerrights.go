package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/loomarr/loomarr/internal/filler"
)

// FillerRightsService is the narrow application interface behind the admin rights-review
// surface. Its implementation owns immutable history and current-head compare-and-swap.
type FillerRightsService interface {
	Record(context.Context, filler.FillerRightsGrant) error
	CurrentGrant(context.Context, filler.FillerRightsScope) (filler.FillerRightsGrant, bool, error)
}

type fillerRightsGrantInput struct {
	Body struct {
		SourceID           string                              `json:"sourceId" maxLength:"256"`
		AcquisitionID      string                              `json:"acquisitionId" maxLength:"256"`
		SourceMasterSHA256 string                              `json:"sourceMasterSha256" minLength:"64" maxLength:"64"`
		PolicySHA256       string                              `json:"policySha256" minLength:"64" maxLength:"64"`
		Status             filler.FillerRightsDecisionStatus   `json:"status" enum:"authorized,prohibited,unknown"`
		Withdrawal         filler.FillerRightsWithdrawalStatus `json:"withdrawal" enum:"clear,withdrawn,unknown"`
		EvidenceSHA256     string                              `json:"evidenceSha256" minLength:"64" maxLength:"64"`
		EffectiveAt        time.Time                           `json:"effectiveAt,omitempty" doc:"UTC instant; defaults to the recording instant"`
		ValidUntil         *time.Time                          `json:"validUntil,omitempty"`
		WithdrawnAt        *time.Time                          `json:"withdrawnAt,omitempty"`
		SupersedesSHA256   string                              `json:"supersedesSha256,omitempty" maxLength:"64" doc:"Exact current grant digest required when replacing an authority"`
	}
}

type fillerRightsCurrentInput struct {
	SourceID           string `query:"sourceId" maxLength:"256" required:"true"`
	AcquisitionID      string `query:"acquisitionId" maxLength:"256" required:"true"`
	SourceMasterSHA256 string `query:"sourceMasterSha256" minLength:"64" maxLength:"64" required:"true"`
	PolicySHA256       string `query:"policySha256" minLength:"64" maxLength:"64" required:"true"`
}

type fillerRightsGrantDTO struct {
	SHA256             string                              `json:"sha256"`
	SourceID           string                              `json:"sourceId"`
	AcquisitionID      string                              `json:"acquisitionId"`
	SourceMasterSHA256 string                              `json:"sourceMasterSha256"`
	PolicySHA256       string                              `json:"policySha256"`
	Use                string                              `json:"use"`
	Status             filler.FillerRightsDecisionStatus   `json:"status" enum:"authorized,prohibited,unknown"`
	Withdrawal         filler.FillerRightsWithdrawalStatus `json:"withdrawal" enum:"clear,withdrawn,unknown"`
	EvidenceSHA256     string                              `json:"evidenceSha256"`
	ActorID            string                              `json:"actorId"`
	EffectiveAt        time.Time                           `json:"effectiveAt"`
	ValidUntil         *time.Time                          `json:"validUntil,omitempty"`
	WithdrawnAt        *time.Time                          `json:"withdrawnAt,omitempty"`
	SupersedesSHA256   string                              `json:"supersedesSha256,omitempty"`
	RecordedAt         time.Time                           `json:"recordedAt"`
}

type fillerRightsGrantOutput struct{ Body fillerRightsGrantDTO }

func (s *Server) registerFillerRights(api huma.API) {
	huma.Register(api, withRole(huma.Operation{
		OperationID: "record-filler-rights-grant", Method: http.MethodPost, Path: "/v1/filler/rights/grants",
		Summary:     "Record current-use filler rights authority",
		Description: "Admin-only append of one immutable reviewed grant. Replacing a current grant requires its exact digest; history cannot be edited or deleted (§10 V67).",
		Tags:        []string{"filler"},
	}, RoleAdmin), s.recordFillerRightsGrant)
	huma.Register(api, withRole(huma.Operation{
		OperationID: "current-filler-rights-grant", Method: http.MethodGet, Path: "/v1/filler/rights/current",
		Summary:     "Read current filler rights authority",
		Description: "Admin-only exact-scope read of the immutable grant currently used by rendered-child screening and terminal release (§10 V67).",
		Tags:        []string{"filler"},
	}, RoleAdmin), s.currentFillerRightsGrant)
}

func (s *Server) recordFillerRightsGrant(ctx context.Context, in *fillerRightsGrantInput) (*fillerRightsGrantOutput, error) {
	if s.fillerRights == nil {
		return nil, errFeatureNotConfigured("Filler rights registry unavailable", "The current-use filler rights registry is not configured.")
	}
	recordedAt := time.Now().UTC()
	effectiveAt := in.Body.EffectiveAt
	if effectiveAt.IsZero() {
		effectiveAt = recordedAt
	}
	actor := userIDFromHuma(ctx)
	if actor == "" {
		actor = "api-token"
	}
	grant, err := filler.NewFillerRightsGrant(
		filler.FillerRightsScope{
			SourceID: strings.TrimSpace(in.Body.SourceID), AcquisitionID: strings.TrimSpace(in.Body.AcquisitionID),
			SourceMasterSHA256: in.Body.SourceMasterSHA256, PolicySHA256: in.Body.PolicySHA256,
			Use: filler.FillerBroadcastUse,
		},
		in.Body.Status, in.Body.Withdrawal, in.Body.EvidenceSHA256, actor, effectiveAt,
		in.Body.ValidUntil, in.Body.WithdrawnAt, in.Body.SupersedesSHA256, recordedAt,
	)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("Invalid filler rights grant", err)
	}
	if err := s.fillerRights.Record(ctx, grant); err != nil {
		if errors.Is(err, filler.ErrFillerRightsGrantConflict) {
			return nil, huma.Error409Conflict("Filler rights authority changed", err)
		}
		return nil, huma.Error500InternalServerError("Record filler rights authority", err)
	}
	return &fillerRightsGrantOutput{Body: fillerRightsGrantDTOFrom(grant)}, nil
}

func (s *Server) currentFillerRightsGrant(ctx context.Context, in *fillerRightsCurrentInput) (*fillerRightsGrantOutput, error) {
	if s.fillerRights == nil {
		return nil, errFeatureNotConfigured("Filler rights registry unavailable", "The current-use filler rights registry is not configured.")
	}
	scope := filler.FillerRightsScope{
		SourceID: strings.TrimSpace(in.SourceID), AcquisitionID: strings.TrimSpace(in.AcquisitionID),
		SourceMasterSHA256: in.SourceMasterSHA256, PolicySHA256: in.PolicySHA256,
		Use: filler.FillerBroadcastUse,
	}
	if err := filler.ValidateFillerRightsScope(scope); err != nil {
		return nil, huma.Error422UnprocessableEntity("Invalid filler rights scope", err)
	}
	grant, found, err := s.fillerRights.CurrentGrant(ctx, scope)
	if err != nil {
		return nil, huma.Error500InternalServerError("Read filler rights authority", err)
	}
	if !found {
		return nil, huma.Error404NotFound("Filler rights authority not found")
	}
	return &fillerRightsGrantOutput{Body: fillerRightsGrantDTOFrom(grant)}, nil
}

func fillerRightsGrantDTOFrom(grant filler.FillerRightsGrant) fillerRightsGrantDTO {
	return fillerRightsGrantDTO{
		SHA256: grant.SHA256, SourceID: grant.Scope.SourceID, AcquisitionID: grant.Scope.AcquisitionID,
		SourceMasterSHA256: grant.Scope.SourceMasterSHA256, PolicySHA256: grant.Scope.PolicySHA256,
		Use: grant.Scope.Use, Status: grant.Status, Withdrawal: grant.Withdrawal,
		EvidenceSHA256: grant.EvidenceSHA256, ActorID: grant.ActorID, EffectiveAt: grant.EffectiveAt,
		ValidUntil: grant.ValidUntil, WithdrawnAt: grant.WithdrawnAt,
		SupersedesSHA256: grant.SupersedesSHA256, RecordedAt: grant.RecordedAt,
	}
}

var _ FillerRightsService = (*filler.FillerRightsRegistry)(nil)
