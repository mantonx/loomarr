package filler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	fillerRightsEvidenceSchemaVersion   = 1
	fillerRightsEvidenceContractVersion = "filler-current-broadcast-rights-evidence-v1"
	FillerBroadcastUse                  = "filler_broadcast"
)

// FillerRightsDecisionStatus is the closed current-use answer supplied by the installation's
// rights authority. A declared licence string is evidence for that authority to review, not a
// decision value accepted here.
type FillerRightsDecisionStatus string

const (
	FillerRightsAuthorized FillerRightsDecisionStatus = "authorized"
	FillerRightsProhibited FillerRightsDecisionStatus = "prohibited"
	FillerRightsUnknown    FillerRightsDecisionStatus = "unknown"
)

type FillerRightsWithdrawalStatus string

const (
	FillerRightsWithdrawalClear   FillerRightsWithdrawalStatus = "clear"
	FillerRightsWithdrawalActive  FillerRightsWithdrawalStatus = "withdrawn"
	FillerRightsWithdrawalUnknown FillerRightsWithdrawalStatus = "unknown"
)

// FillerRightsUseRequest binds a current rights lookup to the exact rendered child and the
// production policy under which it might be used. RequestedAt is part of the question because
// expiry and withdrawal make rights time-sensitive.
type FillerRightsUseRequest struct {
	SubjectSHA256      string    `json:"subjectSha256"`
	SourceID           string    `json:"sourceId"`
	AcquisitionID      string    `json:"acquisitionId"`
	SourceMasterSHA256 string    `json:"sourceMasterSha256"`
	PolicySHA256       string    `json:"policySha256"`
	Use                string    `json:"use"`
	RequestedAt        time.Time `json:"requestedAt"`
}

// FillerRightsUseDecision is a path-free, content-addressed current-use answer. BasisSHA256
// identifies the private grant, prohibition, or review record without copying its contents into
// screening evidence.
type FillerRightsUseDecision struct {
	SchemaVersion      int                          `json:"schemaVersion"`
	ContractVersion    string                       `json:"contractVersion"`
	SubjectSHA256      string                       `json:"subjectSha256"`
	SourceID           string                       `json:"sourceId"`
	AcquisitionID      string                       `json:"acquisitionId"`
	SourceMasterSHA256 string                       `json:"sourceMasterSha256"`
	PolicySHA256       string                       `json:"policySha256"`
	Use                string                       `json:"use"`
	Status             FillerRightsDecisionStatus   `json:"status"`
	Withdrawal         FillerRightsWithdrawalStatus `json:"withdrawal"`
	BasisSHA256        string                       `json:"basisSha256"`
	EvaluatedAt        time.Time                    `json:"evaluatedAt"`
	ValidUntil         *time.Time                   `json:"validUntil,omitempty"`
	WithdrawnAt        *time.Time                   `json:"withdrawnAt,omitempty"`
	SHA256             string                       `json:"sha256"`
}

// CurrentFillerRightsAuthority is the sole semantic dependency of the rights screen. found=false
// means no current decision exists and becomes a durable hold; an error means no trustworthy
// answer exists and remains an operational hold outside the semantic aggregate.
type CurrentFillerRightsAuthority interface {
	CurrentFillerRights(context.Context, FillerRightsUseRequest) (FillerRightsUseDecision, bool, error)
}

type fillerRightsRawEvidence struct {
	SchemaVersion   int                      `json:"schemaVersion"`
	ContractVersion string                   `json:"contractVersion"`
	Request         FillerRightsUseRequest   `json:"request"`
	Decision        *FillerRightsUseDecision `json:"decision,omitempty"`
	Outcome         SegmentScreeningOutcome  `json:"outcome"`
	ReasonCode      string                   `json:"reasonCode"`
}

// FillerRightsEvaluator records the current rights answer for screening. Its settled result is
// historical evidence, not a live-use lease; terminal admission must query CurrentFillerRights
// again immediately before release.
type FillerRightsEvaluator struct {
	profile   SegmentScreeningAxisProfile
	replay    SegmentScreeningAxisEvidenceReplay
	authority CurrentFillerRightsAuthority
	now       func() time.Time
}

func NewFillerRightsEvaluator(profile SegmentScreeningAxisProfile, replay SegmentScreeningAxisEvidenceReplay, authority CurrentFillerRightsAuthority, now func() time.Time) (*FillerRightsEvaluator, error) {
	if ValidateSegmentScreeningAxisProfile(profile) != nil || profile.Axis != ScreenRights || profile.EvidenceContract != fillerRightsEvidenceContractVersion {
		return nil, fmt.Errorf("filler rights evaluator profile is invalid")
	}
	if replay == nil || authority == nil || now == nil {
		return nil, fmt.Errorf("filler rights evaluator requires replay, current authority, and clock")
	}
	return &FillerRightsEvaluator{profile: profile, replay: replay, authority: authority, now: now}, nil
}

func (e *FillerRightsEvaluator) Axis() SegmentScreeningAxis { return ScreenRights }

func (e *FillerRightsEvaluator) Evaluate(ctx context.Context, media SegmentScreeningMedia) (RecordedSegmentScreeningAxisEvidence, error) {
	if e == nil || e.replay == nil || e.authority == nil || e.now == nil ||
		ValidateSegmentScreeningAxisProfile(e.profile) != nil || e.profile.Axis != ScreenRights || e.profile.EvidenceContract != fillerRightsEvidenceContractVersion {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("filler rights evaluator is unavailable")
	}
	if err := validateSegmentScreeningMedia(media); err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, err
	}
	if err := ctx.Err(); err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, err
	}
	replayed, found, err := e.replay.FindSegmentScreeningAxisEvidence(ctx, media.Subject.SHA256, e.profile)
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("replay filler rights evidence: %w", err)
	}
	if found {
		return replayed, nil
	}

	at := e.now().UTC()
	request := FillerRightsUseRequest{
		SubjectSHA256: media.Subject.SHA256, SourceID: media.Subject.SourceID,
		AcquisitionID: media.Subject.AcquisitionID, SourceMasterSHA256: media.Subject.SourceMasterSHA256,
		PolicySHA256: e.profile.PolicySHA256, Use: FillerBroadcastUse, RequestedAt: at,
	}
	outcome, reasonCode := ScreenHold, "rights_identity_missing"
	var decision *FillerRightsUseDecision
	if validFillerRightsUseRequest(request) {
		answer, exists, err := e.authority.CurrentFillerRights(ctx, request)
		if err != nil {
			return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("query current filler rights: %w", err)
		}
		if !exists {
			reasonCode = "rights_unknown"
		} else {
			if err := ValidateFillerRightsUseDecision(answer); err != nil || !fillerRightsDecisionMatchesRequest(answer, request) {
				return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("current filler rights decision is invalid or request-drifted")
			}
			decision = &answer
			switch answer.Status {
			case FillerRightsAuthorized:
				outcome, reasonCode = ScreenPass, "rights_current"
			case FillerRightsProhibited:
				outcome, reasonCode = ScreenReject, "rights_prohibited"
				if answer.Withdrawal == FillerRightsWithdrawalActive {
					reasonCode = "rights_withdrawn"
				}
			case FillerRightsUnknown:
				outcome, reasonCode = ScreenHold, "rights_unknown"
			}
		}
	}
	raw, err := json.Marshal(fillerRightsRawEvidence{
		SchemaVersion: fillerRightsEvidenceSchemaVersion, ContractVersion: fillerRightsEvidenceContractVersion,
		Request: request, Decision: decision, Outcome: outcome, ReasonCode: reasonCode,
	})
	if err != nil {
		return RecordedSegmentScreeningAxisEvidence{}, fmt.Errorf("marshal filler rights evidence: %w", err)
	}
	return NewSegmentScreeningAxisEvidence(media.Subject, e.profile, outcome, reasonCode, nil, raw, at)
}

func validFillerRightsUseRequest(request FillerRightsUseRequest) bool {
	return isContentHash(request.SubjectSHA256) && validRequiredScreeningSubjectID(request.SourceID) &&
		validRequiredScreeningSubjectID(request.AcquisitionID) && isContentHash(request.SourceMasterSHA256) &&
		isContentHash(request.PolicySHA256) && request.Use == FillerBroadcastUse && canonicalRightsTime(request.RequestedAt)
}

func ValidateFillerRightsUseDecision(decision FillerRightsUseDecision) error {
	if decision.SchemaVersion != fillerRightsEvidenceSchemaVersion || decision.ContractVersion != fillerRightsEvidenceContractVersion ||
		!isContentHash(decision.SubjectSHA256) || !validRequiredScreeningSubjectID(decision.SourceID) ||
		!validRequiredScreeningSubjectID(decision.AcquisitionID) || !isContentHash(decision.SourceMasterSHA256) ||
		!isContentHash(decision.PolicySHA256) || decision.Use != FillerBroadcastUse || !isContentHash(decision.BasisSHA256) ||
		!canonicalRightsTime(decision.EvaluatedAt) {
		return fmt.Errorf("filler rights decision identity is invalid")
	}
	if decision.ValidUntil != nil && (!canonicalRightsTime(*decision.ValidUntil) || !decision.ValidUntil.After(decision.EvaluatedAt)) {
		return fmt.Errorf("filler rights decision is expired")
	}
	switch decision.Status {
	case FillerRightsAuthorized:
		if decision.Withdrawal != FillerRightsWithdrawalClear || decision.WithdrawnAt != nil {
			return fmt.Errorf("authorized filler rights are not withdrawal-clear")
		}
	case FillerRightsProhibited:
		if decision.Withdrawal != FillerRightsWithdrawalClear && decision.Withdrawal != FillerRightsWithdrawalActive {
			return fmt.Errorf("prohibited filler rights have unknown withdrawal state")
		}
	case FillerRightsUnknown:
		if decision.Withdrawal != FillerRightsWithdrawalClear && decision.Withdrawal != FillerRightsWithdrawalUnknown {
			return fmt.Errorf("unknown filler rights have invalid withdrawal state")
		}
	default:
		return fmt.Errorf("filler rights decision status is invalid")
	}
	if decision.Withdrawal == FillerRightsWithdrawalActive {
		if decision.WithdrawnAt == nil || !canonicalRightsTime(*decision.WithdrawnAt) || decision.WithdrawnAt.After(decision.EvaluatedAt) {
			return fmt.Errorf("withdrawn filler rights lack a current withdrawal")
		}
	} else if decision.WithdrawnAt != nil {
		return fmt.Errorf("filler rights decision has an unbound withdrawal time")
	}
	if decision.SHA256 == "" || decision.SHA256 != FillerRightsUseDecisionSHA256(decision) {
		return fmt.Errorf("filler rights decision digest is invalid")
	}
	return nil
}

func NewFillerRightsUseDecision(request FillerRightsUseRequest, status FillerRightsDecisionStatus, withdrawal FillerRightsWithdrawalStatus, basisSHA256 string, validUntil, withdrawnAt *time.Time) (FillerRightsUseDecision, error) {
	if !validFillerRightsUseRequest(request) {
		return FillerRightsUseDecision{}, fmt.Errorf("filler rights request is invalid")
	}
	decision := FillerRightsUseDecision{
		SchemaVersion: fillerRightsEvidenceSchemaVersion, ContractVersion: fillerRightsEvidenceContractVersion,
		SubjectSHA256: request.SubjectSHA256, SourceID: request.SourceID, AcquisitionID: request.AcquisitionID,
		SourceMasterSHA256: request.SourceMasterSHA256, PolicySHA256: request.PolicySHA256, Use: request.Use,
		Status: status, Withdrawal: withdrawal, BasisSHA256: basisSHA256, EvaluatedAt: request.RequestedAt.UTC(),
		ValidUntil: cloneUTCTime(validUntil), WithdrawnAt: cloneUTCTime(withdrawnAt),
	}
	decision.SHA256 = FillerRightsUseDecisionSHA256(decision)
	if err := ValidateFillerRightsUseDecision(decision); err != nil {
		return FillerRightsUseDecision{}, err
	}
	return decision, nil
}

func FillerRightsUseDecisionSHA256(decision FillerRightsUseDecision) string {
	decision.SHA256 = ""
	raw, err := json.Marshal(decision)
	if err != nil {
		return ""
	}
	return screeningBytesSHA256(raw)
}

func fillerRightsDecisionMatchesRequest(decision FillerRightsUseDecision, request FillerRightsUseRequest) bool {
	return decision.SubjectSHA256 == request.SubjectSHA256 && decision.SourceID == request.SourceID &&
		decision.AcquisitionID == request.AcquisitionID && decision.SourceMasterSHA256 == request.SourceMasterSHA256 &&
		decision.PolicySHA256 == request.PolicySHA256 && decision.Use == request.Use && decision.EvaluatedAt.Equal(request.RequestedAt)
}

func validRequiredScreeningSubjectID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 256
}

func cloneUTCTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func canonicalRightsTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

var _ SegmentScreeningEvaluator = (*FillerRightsEvaluator)(nil)
