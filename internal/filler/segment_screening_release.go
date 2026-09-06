package filler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"
)

const (
	SegmentScreeningReleaseSchemaVersion   = 3
	SegmentScreeningReleaseContractVersion = "filler-rendered-child-screening-release-v3"
)

type SegmentScreeningReleaseAuthority struct {
	SchemaVersion              int                           `json:"schemaVersion"`
	ContractVersion            string                        `json:"contractVersion"`
	CertificateSHA256          string                        `json:"certificateSha256"`
	AggregateContractVersion   string                        `json:"aggregateContractVersion"`
	Profiles                   []SegmentScreeningAxisProfile `json:"profiles"`
	ProductionAdmissionAllowed bool                          `json:"productionAdmissionAllowed"`
	SHA256                     string                        `json:"sha256"`
}

type SegmentScreeningCertificationEvidenceReader interface {
	SegmentScreeningAxisEvidenceReplay
	GetSegmentScreeningSubject(context.Context, string) (SegmentScreeningSubject, error)
	GetSegmentScreeningEvidence(context.Context, string) (SegmentScreeningEvidence, error)
	GetSegmentScreeningAxisEvidence(context.Context, string) (RecordedSegmentScreeningAxisEvidence, error)
}

// SegmentScreeningCertification replays every exact axis artifact and its raw evidence against one
// immutable release authority. The auto-confirm gate receives this concrete verified module rather
// than a predicate that can turn an aggregate into a pass without evidence.
type SegmentScreeningCertification struct {
	authority SegmentScreeningReleaseAuthority
	evidence  SegmentScreeningCertificationEvidenceReader
	rights    CurrentFillerRightsAuthority
	now       func() time.Time
}

func NewSegmentScreeningCertification(authority SegmentScreeningReleaseAuthority, evidence SegmentScreeningCertificationEvidenceReader, rights CurrentFillerRightsAuthority, now func() time.Time) (*SegmentScreeningCertification, error) {
	if err := ValidateSegmentScreeningReleaseAuthority(authority); err != nil {
		return nil, err
	}
	if evidence == nil || rights == nil || now == nil {
		return nil, fmt.Errorf("segment screening certification requires evidence, current rights, and clock")
	}
	return &SegmentScreeningCertification{authority: authority, evidence: evidence, rights: rights, now: now}, nil
}

func (c *SegmentScreeningCertification) Verify(ctx context.Context, aggregate SegmentScreeningEvidence) error {
	if c == nil || c.evidence == nil || c.rights == nil || c.now == nil {
		return fmt.Errorf("segment screening certification is unavailable")
	}
	if err := ValidateSegmentScreeningReleaseAuthority(c.authority); err != nil {
		return err
	}
	if !c.authority.ProductionAdmissionAllowed || c.authority.AggregateContractVersion != aggregate.ContractVersion {
		return fmt.Errorf("segment screening release does not authorize production admission")
	}
	if err := ValidateSegmentScreeningEvidence(aggregate); err != nil || !aggregate.Passes() {
		return fmt.Errorf("segment screening aggregate is invalid or does not pass")
	}
	subject, err := c.evidence.GetSegmentScreeningSubject(ctx, aggregate.SubjectSHA256)
	if err != nil {
		return fmt.Errorf("replay segment screening subject: %w", err)
	}
	if err := ValidateSegmentScreeningSubject(subject); err != nil || subject.SHA256 != aggregate.SubjectSHA256 {
		return fmt.Errorf("segment screening subject does not reproduce aggregate identity")
	}
	persisted, err := c.evidence.GetSegmentScreeningEvidence(ctx, aggregate.SHA256)
	if err != nil {
		return fmt.Errorf("replay segment screening aggregate: %w", err)
	}
	if !reflect.DeepEqual(persisted, aggregate) {
		return fmt.Errorf("segment screening aggregate does not reproduce persisted evidence")
	}
	profiles := make([]SegmentScreeningAxisProfile, 0, len(aggregate.Results))
	for index, result := range aggregate.Results {
		recorded, err := c.evidence.GetSegmentScreeningAxisEvidence(ctx, result.AuthoritySHA256)
		if err != nil {
			return fmt.Errorf("replay segment screening axis %q: %w", result.Axis, err)
		}
		if err := ValidateRecordedSegmentScreeningAxisEvidence(recorded); err != nil ||
			recorded.Evidence.SubjectSHA256 != aggregate.SubjectSHA256 ||
			recorded.Evidence.Result() != result {
			return fmt.Errorf("segment screening axis %q does not reproduce aggregate result %d", result.Axis, index)
		}
		settled, found, err := c.evidence.FindSegmentScreeningAxisEvidence(ctx, aggregate.SubjectSHA256, recorded.Evidence.Profile)
		if err != nil || !found || !reflect.DeepEqual(settled, recorded) {
			return fmt.Errorf("segment screening axis %q is not the settled subject/profile operation", result.Axis)
		}
		profiles = append(profiles, recorded.Evidence.Profile)
	}
	canonicalizeSegmentScreeningProfiles(profiles)
	if !reflect.DeepEqual(profiles, c.authority.Profiles) {
		return fmt.Errorf("segment screening profiles do not match release authority")
	}
	if err := c.verifyCurrentRights(ctx, subject, profiles); err != nil {
		return err
	}
	return nil
}

func (c *SegmentScreeningCertification) verifyCurrentRights(ctx context.Context, subject SegmentScreeningSubject, profiles []SegmentScreeningAxisProfile) error {
	rightsIndex := slices.IndexFunc(profiles, func(profile SegmentScreeningAxisProfile) bool { return profile.Axis == ScreenRights })
	if rightsIndex < 0 {
		return fmt.Errorf("segment screening release lacks a rights profile")
	}
	request := FillerRightsUseRequest{
		SubjectSHA256: subject.SHA256, SourceID: subject.SourceID, AcquisitionID: subject.AcquisitionID,
		SourceMasterSHA256: subject.SourceMasterSHA256, PolicySHA256: profiles[rightsIndex].PolicySHA256,
		Use: FillerBroadcastUse, RequestedAt: c.now().UTC(),
	}
	if !validFillerRightsUseRequest(request) {
		return fmt.Errorf("segment screening release lacks rights-bound child identity")
	}
	decision, found, err := c.rights.CurrentFillerRights(ctx, request)
	if err != nil {
		return fmt.Errorf("recheck current filler rights: %w", err)
	}
	if !found || ValidateFillerRightsUseDecision(decision) != nil || !fillerRightsDecisionMatchesRequest(decision, request) ||
		decision.Status != FillerRightsAuthorized || decision.Withdrawal != FillerRightsWithdrawalClear {
		return fmt.Errorf("segment screening release does not have current broadcast rights")
	}
	return nil
}

func (c *SegmentScreeningCertification) AuthoritySHA256() string {
	if c == nil || ValidateSegmentScreeningReleaseAuthority(c.authority) != nil {
		return ""
	}
	return c.authority.SHA256
}

func ValidateSegmentScreeningReleaseAuthority(authority SegmentScreeningReleaseAuthority) error {
	if authority.SchemaVersion != SegmentScreeningReleaseSchemaVersion || authority.ContractVersion != SegmentScreeningReleaseContractVersion ||
		!isContentHash(authority.CertificateSHA256) || authority.AggregateContractVersion != SegmentScreeningContractVersion ||
		len(authority.Profiles) != len(segmentScreeningAxisOrder) || authority.SHA256 != SegmentScreeningReleaseAuthoritySHA256(authority) {
		return fmt.Errorf("segment screening release authority identity is invalid")
	}
	seen := make(map[SegmentScreeningAxis]struct{}, len(authority.Profiles))
	for index, profile := range authority.Profiles {
		if ValidateSegmentScreeningAxisProfile(profile) != nil || profile.Axis != segmentScreeningAxisOrder[index] {
			return fmt.Errorf("segment screening release profiles are invalid or non-canonical")
		}
		if _, duplicate := seen[profile.Axis]; duplicate {
			return fmt.Errorf("segment screening release repeats an axis profile")
		}
		seen[profile.Axis] = struct{}{}
	}
	return nil
}

func SegmentScreeningReleaseAuthoritySHA256(authority SegmentScreeningReleaseAuthority) string {
	authority.SHA256 = ""
	raw, err := json.Marshal(authority)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func canonicalizeSegmentScreeningProfiles(profiles []SegmentScreeningAxisProfile) {
	order := make(map[SegmentScreeningAxis]int, len(segmentScreeningAxisOrder))
	for index, axis := range segmentScreeningAxisOrder {
		order[axis] = index
	}
	slices.SortFunc(profiles, func(left, right SegmentScreeningAxisProfile) int { return order[left.Axis] - order[right.Axis] })
}
