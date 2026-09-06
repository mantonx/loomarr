package fillervisualsafety

import (
	"cmp"
	"errors"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
)

const (
	visualCorpusRightsEvidenceSchemaVersion = 2
	visualCorpusRightsEvidenceKind          = "loomarr-visual-corpus-rights-review-v2"
)

func sealVisualCorpusDraftAuthority(authority VisualCorpusDraftAuthority) (VisualCorpusDraftAuthority, error) {
	authority.SchemaVersion = VisualCorpusDraftAuthoritySchemaVersion
	authority.ContractVersion = VisualCorpusDraftAuthorityContractVersion
	authority.AuthoredAt = authority.AuthoredAt.UTC()
	authority.Candidates = slices.Clone(authority.Candidates)
	for index := range authority.Candidates {
		authority.Candidates[index].Slices = slices.Clone(authority.Candidates[index].Slices)
		slices.Sort(authority.Candidates[index].Slices)
	}
	slices.SortFunc(authority.Candidates, func(left, right VisualCorpusDraftCandidate) int {
		return cmp.Compare(left.CandidateID, right.CandidateID)
	})
	authority.SHA256 = VisualCorpusDraftAuthoritySHA256(authority)
	if err := validateVisualCorpusDraftAuthority(authority); err != nil {
		return VisualCorpusDraftAuthority{}, err
	}
	return authority, nil
}

func validateVisualCorpusDraftAuthority(authority VisualCorpusDraftAuthority) error {
	if authority.SchemaVersion != VisualCorpusDraftAuthoritySchemaVersion ||
		authority.ContractVersion != VisualCorpusDraftAuthorityContractVersion ||
		authority.AuthoredAt.IsZero() || authority.AuthoredAt.Location() != time.UTC ||
		!validDigest(authority.PolicySHA256) || !validDigest(authority.AliasSeedSHA256) ||
		authority.PositiveCandidateTarget < MinimumVisualPositiveCandidateTarget ||
		authority.CleanCandidateTarget < MinimumVisualCleanCandidateTarget ||
		authority.PositiveCandidateTarget+authority.CleanCandidateTarget > MaximumVisualCorpusDraftCases ||
		authority.TransportDecision != VisualCorpusTransportDecisionUnresolved || authority.CandidateModelOutput ||
		len(authority.Candidates) < authority.PositiveCandidateTarget+authority.CleanCandidateTarget ||
		len(authority.Candidates) > MaximumVisualCorpusDraftCases {
		return errors.New("visual corpus draft authority is invalid")
	}
	seenCandidate := make(map[string]struct{}, len(authority.Candidates))
	seenWork := make(map[string]struct{}, len(authority.Candidates))
	seenFamily := make(map[string]struct{}, len(authority.Candidates))
	seenIndependence := make(map[string]struct{}, len(authority.Candidates))
	seenPositiveCreator := make(map[string]struct{}, authority.PositiveCandidateTarget)
	seenAsset := make(map[string]struct{}, len(authority.Candidates))
	seenRights := make(map[string]struct{}, len(authority.Candidates))
	positive, clean := 0, 0
	totalBytes := int64(0)
	previous := ""
	for _, candidate := range authority.Candidates {
		if previous != "" && cmp.Compare(previous, candidate.CandidateID) >= 0 {
			return errors.New("visual corpus draft candidates are not canonical")
		}
		previous = candidate.CandidateID
		if err := validateVisualCorpusDraftCandidate(candidate); err != nil {
			return err
		}
		candidateBytes := candidate.Asset.Bytes + candidate.RightsEvidence.Bytes
		if candidateBytes < candidate.Asset.Bytes || totalBytes > MaximumVisualCorpusDraftBytes-candidateBytes {
			return errors.New("visual corpus draft exceeds its total byte ceiling")
		}
		totalBytes += candidateBytes
		if duplicateIdentity(seenCandidate, candidate.CandidateID) ||
			duplicateIdentity(seenWork, candidate.SourceWorkID) ||
			duplicateIdentity(seenFamily, candidate.SourceFamilyID) ||
			duplicateIdentity(seenIndependence, candidate.IndependenceGroupID) ||
			duplicateIdentity(seenAsset, candidate.Asset.SHA256) ||
			duplicateIdentity(seenRights, candidate.RightsEvidence.SHA256) {
			return errors.New("visual corpus draft contains a source-family or evidence collision")
		}
		switch candidate.Nomination {
		case VisualCorpusNominationPositive:
			positive++
			if duplicateIdentity(seenPositiveCreator, candidate.CreatorID) {
				return errors.New("visual corpus positive candidates share a creator independence bucket")
			}
		case VisualCorpusNominationClean:
			clean++
		default:
			return errors.New("visual corpus draft nomination is invalid")
		}
	}
	if positive < authority.PositiveCandidateTarget || clean < authority.CleanCandidateTarget ||
		authority.SHA256 == "" || authority.SHA256 != VisualCorpusDraftAuthoritySHA256(authority) {
		return errors.New("visual corpus draft authority population or digest is invalid")
	}
	return nil
}

func validateVisualCorpusDraftCandidate(candidate VisualCorpusDraftCandidate) error {
	if !validIdentity(candidate.CandidateID) || !validIdentity(candidate.InstitutionID) ||
		!validIdentity(candidate.SourceWorkID) || !validIdentity(candidate.SourceFamilyID) ||
		!validIdentity(candidate.IndependenceGroupID) || !validIdentity(candidate.CreatorID) ||
		!validHTTPSURL(candidate.ObjectURL) || !validHTTPSURL(candidate.RightsURL) ||
		(candidate.RightsBasis != VisualCorpusRightsCC0 && candidate.RightsBasis != VisualCorpusRightsPublicDomainMark) ||
		candidate.GeneratedStatus != VisualCorpusGeneratedNo ||
		!validCorpusRelativePath(candidate.AssetRelativePath) || !validCorpusRelativePath(candidate.RightsRelativePath) ||
		candidate.AssetRelativePath == candidate.RightsRelativePath ||
		!validFileIdentity(candidate.Asset, MaximumVisualCorpusAssetBytes) ||
		!validFileIdentity(candidate.RightsEvidence, MaximumVisualCorpusRightsBytes) ||
		len(candidate.Slices) == 0 || len(candidate.Slices) > 32 {
		return errors.New("visual corpus draft candidate is invalid")
	}
	if candidate.Nomination == VisualCorpusNominationPositive && candidate.SubjectStatus != VisualCorpusSubjectHistoricalAdult {
		return errors.New("visual corpus positive candidate lacks an adult historical-subject review")
	}
	if candidate.Nomination == VisualCorpusNominationClean && candidate.SubjectStatus != VisualCorpusSubjectNoRiskFound {
		return errors.New("visual corpus clean candidate lacks a no-sensitive-subject review")
	}
	previous := ""
	for _, slice := range candidate.Slices {
		if !validVisualDiagnosticSlice(slice) || (previous != "" && cmp.Compare(previous, slice) >= 0) {
			return errors.New("visual corpus draft candidate slices are invalid")
		}
		previous = slice
	}
	return nil
}

func validateVisualCorpusRightsEvidence(candidate VisualCorpusDraftCandidate, evidence VisualCorpusRightsEvidence, authoredAt time.Time) error {
	if evidence.SchemaVersion != visualCorpusRightsEvidenceSchemaVersion || evidence.Kind != visualCorpusRightsEvidenceKind ||
		!validDigest(evidence.InventorySHA256) || !validDigest(evidence.MaterializationSHA256) ||
		!validDigest(evidence.RightsApprovalSHA256) || evidence.CaseID != candidate.CandidateID ||
		evidence.ContentSHA256 != candidate.Asset.SHA256 ||
		evidence.ReviewedAt.IsZero() || evidence.ReviewedAt.Location() != time.UTC || evidence.ReviewedAt.After(authoredAt) ||
		!validIdentity(evidence.ReviewedBy) || evidence.InstitutionID != candidate.InstitutionID ||
		evidence.SourceWorkID != candidate.SourceWorkID || evidence.ObjectURL != candidate.ObjectURL ||
		evidence.RightsURL != candidate.RightsURL || evidence.RightsBasis != candidate.RightsBasis ||
		evidence.SubjectStatus != candidate.SubjectStatus || evidence.GeneratedStatus != candidate.GeneratedStatus ||
		!evidence.PrivateRetentionAllowed || !evidence.PrivateModelEvaluation || evidence.TrainingAllowed ||
		evidence.ProductionBroadcastAllowed {
		return errors.New("visual corpus rights evidence is invalid or does not match its candidate")
	}
	return nil
}

func validFileIdentity(identity VisualCorpusFileIdentity, maximum int64) bool {
	return validDigest(identity.SHA256) && identity.Bytes > 0 && identity.Bytes <= maximum
}

func validCorpusRelativePath(value string) bool {
	return value != "" && value == path.Clean(value) && value != "." && !path.IsAbs(value) &&
		!strings.HasPrefix(value, "../") && !strings.Contains(value, `\`)
}

func validHTTPSURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == "" &&
		parsed.Path != "" && value == parsed.String()
}

func duplicateIdentity(seen map[string]struct{}, value string) bool {
	if _, exists := seen[value]; exists {
		return true
	}
	seen[value] = struct{}{}
	return false
}

func validVisualDiagnosticSlice(value string) bool {
	switch value {
	case DiagnosticSliceShortExposure, DiagnosticSliceCuts, DiagnosticSliceCropLetterbox,
		DiagnosticSliceTranscode, DiagnosticSliceVFRCFR, DiagnosticSliceAnimation,
		DiagnosticSliceMonochrome, DiagnosticSliceLowLight, DiagnosticSliceMultiplePeople,
		DiagnosticSliceCompilationPlacement, DiagnosticSliceDamagedTail, DiagnosticSliceProgramme,
		DiagnosticSliceAdvertising, DiagnosticSliceHistoricalGraphics, DiagnosticSliceSkinTone,
		DiagnosticSliceMedical, DiagnosticSliceBeach, DiagnosticSliceUnderwear, DiagnosticSliceVisuallyBusy:
		return true
	default:
		return false
	}
}
