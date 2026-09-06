package filler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	FillerRightsGrantSchemaVersion   = 1
	FillerRightsGrantContractVersion = "filler-current-broadcast-rights-grant-v1"
)

var ErrFillerRightsGrantConflict = errors.New("filler rights grant conflicts with current authority")

// FillerRightsScope is the stable part of a current-use rights question. A child subject is
// deliberately absent: one reviewed acquisition grant can answer for each exact rendered child
// derived from the bound source master, while the resulting decision still binds that child.
type FillerRightsScope struct {
	SourceID           string `json:"sourceId"`
	AcquisitionID      string `json:"acquisitionId"`
	SourceMasterSHA256 string `json:"sourceMasterSha256"`
	PolicySHA256       string `json:"policySha256"`
	Use                string `json:"use"`
}

// FillerRightsGrant is an immutable operator-reviewed rights decision. SupersedesSHA256 forms one
// compare-and-swap history per scope; EvidenceSHA256 identifies the private legal/review basis
// without copying that material into catalog or screening evidence.
type FillerRightsGrant struct {
	SchemaVersion    int                          `json:"schemaVersion"`
	ContractVersion  string                       `json:"contractVersion"`
	Scope            FillerRightsScope            `json:"scope"`
	Status           FillerRightsDecisionStatus   `json:"status"`
	Withdrawal       FillerRightsWithdrawalStatus `json:"withdrawal"`
	EvidenceSHA256   string                       `json:"evidenceSha256"`
	ActorID          string                       `json:"actorId"`
	EffectiveAt      time.Time                    `json:"effectiveAt"`
	ValidUntil       *time.Time                   `json:"validUntil,omitempty"`
	WithdrawnAt      *time.Time                   `json:"withdrawnAt,omitempty"`
	SupersedesSHA256 string                       `json:"supersedesSha256,omitempty"`
	RecordedAt       time.Time                    `json:"recordedAt"`
	SHA256           string                       `json:"sha256"`
}

// FillerRightsGrantRepository is the persistence seam beneath the rights registry. The adapter
// must atomically compare SupersedesSHA256 with the current head when it records a grant.
type FillerRightsGrantRepository interface {
	PutFillerRightsGrant(context.Context, FillerRightsGrant) error
	CurrentFillerRightsGrant(context.Context, FillerRightsScope) (FillerRightsGrant, bool, error)
}

// FillerRightsRegistry owns current-time interpretation and exposes the same current-use
// authority to initial screening and terminal release.
type FillerRightsRegistry struct {
	repository FillerRightsGrantRepository
}

func NewFillerRightsRegistry(repository FillerRightsGrantRepository) (*FillerRightsRegistry, error) {
	if repository == nil {
		return nil, fmt.Errorf("filler rights registry requires a repository")
	}
	return &FillerRightsRegistry{repository: repository}, nil
}

func (r *FillerRightsRegistry) Record(ctx context.Context, grant FillerRightsGrant) error {
	if r == nil || r.repository == nil {
		return fmt.Errorf("filler rights registry is unavailable")
	}
	if err := ValidateFillerRightsGrant(grant); err != nil {
		return err
	}
	return r.repository.PutFillerRightsGrant(ctx, grant)
}

func (r *FillerRightsRegistry) CurrentGrant(ctx context.Context, scope FillerRightsScope) (FillerRightsGrant, bool, error) {
	if r == nil || r.repository == nil {
		return FillerRightsGrant{}, false, fmt.Errorf("filler rights registry is unavailable")
	}
	if err := ValidateFillerRightsScope(scope); err != nil {
		return FillerRightsGrant{}, false, err
	}
	grant, found, err := r.repository.CurrentFillerRightsGrant(ctx, scope)
	if err != nil || !found {
		return grant, found, err
	}
	if err := ValidateFillerRightsGrant(grant); err != nil || grant.Scope != scope {
		return FillerRightsGrant{}, false, fmt.Errorf("current filler rights grant is invalid or scope-drifted")
	}
	return grant, true, nil
}

func (r *FillerRightsRegistry) CurrentFillerRights(ctx context.Context, request FillerRightsUseRequest) (FillerRightsUseDecision, bool, error) {
	if r == nil || r.repository == nil || !validFillerRightsUseRequest(request) {
		return FillerRightsUseDecision{}, false, fmt.Errorf("filler rights registry request is invalid")
	}
	scope := fillerRightsScopeForRequest(request)
	grant, found, err := r.CurrentGrant(ctx, scope)
	if err != nil {
		return FillerRightsUseDecision{}, false, fmt.Errorf("read current filler rights grant: %w", err)
	}
	if !found {
		return FillerRightsUseDecision{}, false, nil
	}
	status, withdrawal := grant.Status, grant.Withdrawal
	validUntil, withdrawnAt := cloneUTCTime(grant.ValidUntil), cloneUTCTime(grant.WithdrawnAt)
	if request.RequestedAt.Before(grant.EffectiveAt) || grant.ValidUntil != nil && !request.RequestedAt.Before(*grant.ValidUntil) {
		status, withdrawal = FillerRightsUnknown, FillerRightsWithdrawalClear
		validUntil, withdrawnAt = nil, nil
	}
	decision, err := NewFillerRightsUseDecision(request, status, withdrawal, grant.SHA256, validUntil, withdrawnAt)
	if err != nil {
		return FillerRightsUseDecision{}, false, fmt.Errorf("derive current filler rights decision: %w", err)
	}
	return decision, true, nil
}

func NewFillerRightsGrant(
	scope FillerRightsScope,
	status FillerRightsDecisionStatus,
	withdrawal FillerRightsWithdrawalStatus,
	evidenceSHA256, actorID string,
	effectiveAt time.Time,
	validUntil, withdrawnAt *time.Time,
	supersedesSHA256 string,
	recordedAt time.Time,
) (FillerRightsGrant, error) {
	grant := FillerRightsGrant{
		SchemaVersion: FillerRightsGrantSchemaVersion, ContractVersion: FillerRightsGrantContractVersion,
		Scope: scope, Status: status, Withdrawal: withdrawal,
		EvidenceSHA256: evidenceSHA256, ActorID: strings.TrimSpace(actorID), EffectiveAt: effectiveAt.UTC(),
		ValidUntil: cloneUTCTime(validUntil), WithdrawnAt: cloneUTCTime(withdrawnAt),
		SupersedesSHA256: supersedesSHA256, RecordedAt: recordedAt.UTC(),
	}
	grant.SHA256 = FillerRightsGrantSHA256(grant)
	if err := ValidateFillerRightsGrant(grant); err != nil {
		return FillerRightsGrant{}, err
	}
	return grant, nil
}

func ValidateFillerRightsGrant(grant FillerRightsGrant) error {
	if grant.SchemaVersion != FillerRightsGrantSchemaVersion || grant.ContractVersion != FillerRightsGrantContractVersion ||
		!validFillerRightsScope(grant.Scope) || !isContentHash(grant.EvidenceSHA256) ||
		!validFillerRightsIdentifier(grant.ActorID) || !canonicalRightsTime(grant.EffectiveAt) ||
		!canonicalRightsTime(grant.RecordedAt) || grant.SHA256 != FillerRightsGrantSHA256(grant) {
		return fmt.Errorf("filler rights grant identity is invalid")
	}
	if grant.SupersedesSHA256 != "" && !isContentHash(grant.SupersedesSHA256) || grant.SupersedesSHA256 == grant.SHA256 {
		return fmt.Errorf("filler rights grant supersession is invalid")
	}
	if grant.ValidUntil != nil && (!canonicalRightsTime(*grant.ValidUntil) || !grant.ValidUntil.After(grant.EffectiveAt)) {
		return fmt.Errorf("filler rights grant expiry is invalid")
	}
	switch grant.Status {
	case FillerRightsAuthorized:
		if grant.Withdrawal != FillerRightsWithdrawalClear || grant.WithdrawnAt != nil {
			return fmt.Errorf("authorized filler rights grant is not withdrawal-clear")
		}
	case FillerRightsProhibited:
		if grant.Withdrawal != FillerRightsWithdrawalClear && grant.Withdrawal != FillerRightsWithdrawalActive {
			return fmt.Errorf("prohibited filler rights grant has unknown withdrawal state")
		}
	case FillerRightsUnknown:
		if grant.Withdrawal != FillerRightsWithdrawalClear && grant.Withdrawal != FillerRightsWithdrawalUnknown || grant.WithdrawnAt != nil {
			return fmt.Errorf("unknown filler rights grant has invalid withdrawal state")
		}
	default:
		return fmt.Errorf("filler rights grant status is invalid")
	}
	if grant.Withdrawal == FillerRightsWithdrawalActive {
		if grant.WithdrawnAt == nil || !canonicalRightsTime(*grant.WithdrawnAt) || grant.WithdrawnAt.After(grant.EffectiveAt) {
			return fmt.Errorf("withdrawn filler rights grant lacks an effective withdrawal")
		}
	} else if grant.WithdrawnAt != nil {
		return fmt.Errorf("filler rights grant has an unbound withdrawal time")
	}
	return nil
}

func FillerRightsGrantSHA256(grant FillerRightsGrant) string {
	grant.SHA256 = ""
	raw, err := json.Marshal(grant)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validFillerRightsScope(scope FillerRightsScope) bool {
	return validFillerRightsIdentifier(scope.SourceID) && validFillerRightsIdentifier(scope.AcquisitionID) &&
		isContentHash(scope.SourceMasterSHA256) && isContentHash(scope.PolicySHA256) && scope.Use == FillerBroadcastUse
}

func validFillerRightsIdentifier(value string) bool {
	return validRequiredScreeningSubjectID(value) && utf8.ValidString(value) &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

func ValidateFillerRightsScope(scope FillerRightsScope) error {
	if !validFillerRightsScope(scope) {
		return fmt.Errorf("filler rights scope is invalid")
	}
	return nil
}

func fillerRightsScopeForRequest(request FillerRightsUseRequest) FillerRightsScope {
	return FillerRightsScope{
		SourceID: request.SourceID, AcquisitionID: request.AcquisitionID,
		SourceMasterSHA256: request.SourceMasterSHA256, PolicySHA256: request.PolicySHA256, Use: request.Use,
	}
}

var _ CurrentFillerRightsAuthority = (*FillerRightsRegistry)(nil)
