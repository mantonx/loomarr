package fillerstructure

import (
	"encoding/hex"
	"errors"
	"slices"
	"strings"
)

func invalidCandidates(request Request) bool {
	if !validSource(request.Source) || ValidateAssessmentInput(request.Input) != nil ||
		request.Input.Source != request.Source || request.BoundaryToleranceMS < 0 || len(request.Candidates) < 2 {
		return true
	}
	assessors := make(map[string]struct{}, len(request.Candidates))
	families := make(map[string]struct{}, len(request.Candidates))
	for _, candidate := range request.Candidates {
		identity := candidate.Assessor
		family := strings.ToLower(strings.TrimSpace(identity.ModelFamily))
		if candidate.Source != request.Source || candidate.InputSHA256 != request.Input.SHA256 || family == "" || !validAssessor(identity) {
			return true
		}
		if _, duplicate := assessors[identity.ID]; duplicate {
			return true
		}
		assessors[identity.ID] = struct{}{}
		families[family] = struct{}{}
		if candidate.Failure != "" {
			if strings.TrimSpace(candidate.Failure) != candidate.Failure || len(candidate.Failure) > 64 || candidate.Unit != "" || candidate.Role != "" || len(candidate.Segments) != 0 {
				return true
			}
			continue
		}
		if !validUnit(candidate.Unit) || !validCandidateRole(candidate) || !completeTimeline(candidate.Segments, request.Source.DurationMS) {
			return true
		}
	}
	return len(families) < 2
}

func validSource(source Source) bool {
	return digest(source.SHA256) && source.Bytes > 0 && source.DurationMS > 0
}

// ValidateSource checks the immutable complete-file identity consumed by structure protocols.
func ValidateSource(source Source) error {
	if !validSource(source) {
		return errors.New("filler structure source is invalid")
	}
	return nil
}

func validAssessmentMedia(media AssessmentMedia, source Source) bool {
	return digest(media.SHA256) && media.Bytes > 0 && media.Bytes <= AssessmentMediaMaximumBytes && media.DurationMS > 0 &&
		digest(media.ProfileSHA256) && digest(media.LineageSHA256) &&
		absolute(media.DurationMS-source.DurationMS) <= AssessmentMediaMaximumTimelineDriftMS
}

func ValidateAssessmentMedia(source Source, media AssessmentMedia) error {
	if !validSource(source) || !validAssessmentMedia(media, source) {
		return errors.New("filler structure assessment media identity is invalid")
	}
	return nil
}

func absolute(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func canonicalIdentity(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= 256
}

func validCandidateRole(candidate Candidate) bool {
	if candidate.Unit == UnitStandalone {
		return fillerRole(candidate.Role) && len(candidate.Segments) == 1 && candidate.Segments[0].Role == candidate.Role
	}
	return candidate.Role == ""
}

func completeTimeline(segments []Segment, durationMS int64) bool {
	if len(segments) == 0 {
		return false
	}
	next := int64(0)
	for _, segment := range segments {
		if segment.StartMS != next || segment.EndMS <= segment.StartMS || segment.EndMS > durationMS || !validRole(segment.Role) {
			return false
		}
		next = segment.EndMS
	}
	return next == durationMS
}

func validUnit(unit Unit) bool {
	return slices.Contains([]Unit{UnitStandalone, UnitCompilation, UnitProgrammeExcerpt, UnitProgrammeSpots, UnitUnusable, UnitUnclear}, unit)
}

func validRole(role Role) bool {
	return fillerRole(role) || role == RoleProgrammeFragment || role == RoleNonFiller || role == RoleAmbiguous || role == RoleUnusable
}

func fillerRole(role Role) bool {
	return slices.Contains([]Role{RoleCommercial, RolePromo, RoleBumper, RolePSA, RoleStationID, RoleTrailer, RoleInterstitial}, role)
}

func digest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
