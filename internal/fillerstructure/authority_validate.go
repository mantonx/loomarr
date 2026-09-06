package fillerstructure

import (
	"errors"
	"reflect"
	"slices"
	"strings"
)

func ValidateAuthority(authority Authority) error {
	if authority.SchemaVersion != AuthoritySchemaVersion || authority.ContractVersion != AuthorityContractVersion ||
		authority.ReducerVersion != ReducerContractVersion || !digest(authority.CertificateSHA256) ||
		!digest(authority.AssessmentMediaProfileSHA256) || authority.MinimumSourceDurationMS <= 0 ||
		authority.MaximumSourceDurationMS < authority.MinimumSourceDurationMS || authority.MaximumAssessmentMediaBytes <= 0 || authority.MaximumAssessmentMediaBytes > AssessmentMediaMaximumBytes ||
		authority.BoundaryToleranceMS < 0 || !digest(authority.SHA256) || authority.SHA256 != AuthoritySHA256(authority) {
		return errors.New("filler structure authority: identity or policy is invalid")
	}
	if len(authority.Assessors) < 2 || !slices.IsSortedFunc(authority.Assessors, compareProfiles) {
		return errors.New("filler structure authority: assessor profiles are incomplete or non-canonical")
	}
	if err := ValidateAssessorProfiles(authority.Assessors); err != nil {
		return err
	}
	if !validAuthorityUnits(authority.AllowedUnits) || !validAuthorityRoles(authority.AllowedRoles) {
		return errors.New("filler structure authority: allowed slices are invalid")
	}
	return nil
}

func ValidateAssessorProfiles(profiles []AssessorProfile) error {
	if len(profiles) < 2 {
		return errors.New("filler structure authority: at least two assessor profiles are required")
	}
	ids, families := map[string]struct{}{}, map[string]struct{}{}
	for _, profile := range profiles {
		if !validProfile(profile) {
			return errors.New("filler structure authority: assessor profile is invalid")
		}
		if _, duplicate := ids[profile.ID]; duplicate {
			return errors.New("filler structure authority: assessor profile repeats")
		}
		ids[profile.ID], families[profile.ModelFamily] = struct{}{}, struct{}{}
	}
	if len(families) < 2 {
		return errors.New("filler structure authority: independent model families are required")
	}
	return nil
}

func ValidateAssessorProfile(profile AssessorProfile) error {
	if !validProfile(profile) {
		return errors.New("filler structure authority: assessor profile is invalid")
	}
	return nil
}

func VerifyAuthority(artifact Artifact, authority Authority) error {
	if err := ValidateArtifact(artifact); err != nil {
		return err
	}
	if err := ValidateAuthority(authority); err != nil {
		return err
	}
	decision := artifact.Decision
	if !authority.AutomaticMaterializationAllowed || artifact.ReducerVersion != authority.ReducerVersion ||
		artifact.BoundaryToleranceMS != authority.BoundaryToleranceMS || decision.Status != StatusConfirmed ||
		decision.Input.Kind != AssessmentInputCompleteVideo ||
		decision.Input.ProfileSHA256 != authority.AssessmentMediaProfileSHA256 || len(decision.Input.Items) != 1 ||
		decision.Source.DurationMS < authority.MinimumSourceDurationMS || decision.Source.DurationMS > authority.MaximumSourceDurationMS ||
		decision.Input.Items[0].Bytes > authority.MaximumAssessmentMediaBytes ||
		!slices.Contains(authority.AllowedUnits, decision.Unit) {
		return errors.New("filler structure authority: decision is outside certified policy")
	}
	for _, segment := range decision.Segments {
		if !slices.Contains(authority.AllowedRoles, segment.Role) {
			return errors.New("filler structure authority: segment role is outside certified slices")
		}
	}
	profiles := make([]AssessorProfile, 0, len(decision.Candidates))
	for _, candidate := range decision.Candidates {
		profiles = append(profiles, Profile(candidate.Assessor))
	}
	if !reflect.DeepEqual(profiles, authority.Assessors) {
		return errors.New("filler structure authority: assessor profiles do not match")
	}
	return nil
}

func validProfile(profile AssessorProfile) bool {
	return canonicalIdentity(profile.ID) && profile.ModelFamily == strings.ToLower(strings.TrimSpace(profile.ModelFamily)) &&
		canonicalIdentity(profile.ModelFamily) && canonicalIdentity(profile.Provider) && canonicalIdentity(profile.Model) &&
		digest(profile.ModelDigest) && digest(profile.CapabilitySHA256) && canonicalIdentity(profile.PromptVersion) &&
		canonicalIdentity(profile.EvidenceContract)
}

func compareProfiles(left, right AssessorProfile) int { return strings.Compare(left.ID, right.ID) }

func validAuthorityUnits(units []Unit) bool {
	if len(units) == 0 || !slices.IsSorted(units) || len(slices.Compact(slices.Clone(units))) != len(units) {
		return false
	}
	for _, unit := range units {
		if unit != UnitStandalone && unit != UnitCompilation && unit != UnitProgrammeExcerpt && unit != UnitProgrammeSpots {
			return false
		}
	}
	return true
}

func validAuthorityRoles(roles []Role) bool {
	if len(roles) == 0 || !slices.IsSorted(roles) || len(slices.Compact(slices.Clone(roles))) != len(roles) {
		return false
	}
	for _, role := range roles {
		if !validRole(role) || role == RoleAmbiguous || role == RoleUnusable {
			return false
		}
	}
	return true
}
