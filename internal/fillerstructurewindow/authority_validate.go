package fillerstructurewindow

import (
	"errors"
	"reflect"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func ValidateMaterializationAuthority(authority MaterializationAuthority) error {
	profile := CanonicalProfile()
	if authority.SchemaVersion != MaterializationAuthoritySchemaVersion || authority.ContractVersion != MaterializationAuthorityContractVersion ||
		!contentHash(authority.WindowCertificationSHA256) || !contentHash(authority.ShortLongShadowSHA256) ||
		authority.WindowProfileSHA256 != profile.SHA256 || authority.AssessmentMediaProfileSHA256 != profile.AssessmentMediaProfileSHA256 ||
		authority.MinimumSourceDurationMS <= 0 || authority.MaximumSourceDurationMS < authority.MinimumSourceDurationMS ||
		authority.MaximumSourceDurationMS > profile.MaximumSourceDurationMS || authority.MaximumWindowBytes <= 0 ||
		authority.MaximumWindowBytes > profile.MaximumWindowBytes || authority.MaximumWindows <= 0 || authority.MaximumWindows > profile.MaximumWindows ||
		authority.ReducerVersion != fillerstructure.ReducerContractVersion || authority.BoundaryToleranceMS < 0 ||
		authority.BoundaryToleranceMS >= profile.ContextOverlapMS || strings.TrimSpace(authority.ReviewerID) != authority.ReviewerID ||
		authority.ReviewerID == "" || len(authority.ReviewerID) > 128 || authority.ReviewedAt.IsZero() || authority.ReviewedAt != authority.ReviewedAt.UTC() ||
		authority.TrainingAllowed || authority.ProductionAdmissionAllowed || !authority.AutomaticMaterializationAllowed ||
		!contentHash(authority.SHA256) || authority.SHA256 != MaterializationAuthoritySHA256(authority) {
		return errors.New("structure window materialization authority identity or disposition is invalid")
	}
	if len(authority.Assessors) != 2 || !slices.IsSortedFunc(authority.Assessors, func(left, right fillerstructure.AssessorProfile) int {
		return strings.Compare(left.ID, right.ID)
	}) {
		return errors.New("structure window materialization authority assessor profiles are incomplete or non-canonical")
	}
	if err := fillerstructure.ValidateAssessorProfiles(authority.Assessors); err != nil {
		return err
	}
	if !validMaterializationUnits(authority.AllowedUnits) || !validMaterializationRoles(authority.AllowedRoles) {
		return errors.New("structure window materialization authority slices are invalid")
	}
	return nil
}

// VerifyMaterializationAuthority proves that a long-reel reducer artifact is inside the exact
// certified window protocol and reviewed source/signal envelope.
func VerifyMaterializationAuthority(artifact fillerstructure.Artifact, authority MaterializationAuthority) error {
	if err := fillerstructure.ValidateArtifact(artifact); err != nil {
		return err
	}
	if err := ValidateMaterializationAuthority(authority); err != nil {
		return err
	}
	decision := artifact.Decision
	if decision.Status != fillerstructure.StatusConfirmed || decision.Input.Kind != fillerstructure.AssessmentInputWindowMediaSet ||
		artifact.ReducerVersion != authority.ReducerVersion || artifact.BoundaryToleranceMS != authority.BoundaryToleranceMS ||
		decision.Source.DurationMS < authority.MinimumSourceDurationMS || decision.Source.DurationMS > authority.MaximumSourceDurationMS ||
		decision.Input.ProfileSHA256 != authority.AssessmentMediaProfileSHA256 ||
		!slices.Contains(authority.AllowedUnits, decision.Unit) {
		return errors.New("structure window decision is outside certified materialization policy")
	}
	plan, err := NewPlan(decision.Source)
	if err != nil || plan.Profile.SHA256 != authority.WindowProfileSHA256 || decision.Input.PlanSHA256 != plan.SHA256 ||
		len(decision.Input.Items) != len(plan.Windows) || len(decision.Input.Items) > authority.MaximumWindows {
		return errors.New("structure window decision does not reproduce the certified plan")
	}
	for index, item := range decision.Input.Items {
		window := plan.Windows[index]
		if item.ProfileSHA256 != authority.AssessmentMediaProfileSHA256 || item.Bytes > authority.MaximumWindowBytes ||
			absolute(item.DurationMS-(window.MediaEndMS-window.MediaStartMS)) > plan.Profile.MaximumTimelineDriftMS {
			return errors.New("structure window decision media is outside the certified envelope")
		}
	}
	for _, segment := range decision.Segments {
		if !slices.Contains(authority.AllowedRoles, segment.Role) {
			return errors.New("structure window decision role is outside certified slices")
		}
	}
	profiles := make([]fillerstructure.AssessorProfile, 0, len(decision.Candidates))
	for _, candidate := range decision.Candidates {
		profiles = append(profiles, fillerstructure.Profile(candidate.Assessor))
	}
	if !reflect.DeepEqual(profiles, authority.Assessors) {
		return errors.New("structure window decision assessor profiles do not match")
	}
	return nil
}

func validMaterializationUnits(units []fillerstructure.Unit) bool {
	if len(units) == 0 || !slices.IsSorted(units) || len(slices.Compact(slices.Clone(units))) != len(units) {
		return false
	}
	for _, unit := range units {
		if unit != fillerstructure.UnitCompilation && unit != fillerstructure.UnitProgrammeSpots {
			return false
		}
	}
	return true
}

func validMaterializationRoles(roles []fillerstructure.Role) bool {
	if len(roles) == 0 || !slices.IsSorted(roles) || len(slices.Compact(slices.Clone(roles))) != len(roles) {
		return false
	}
	for _, role := range roles {
		switch role {
		case fillerstructure.RoleCommercial, fillerstructure.RolePromo, fillerstructure.RoleBumper,
			fillerstructure.RoleStationID, fillerstructure.RolePSA, fillerstructure.RoleTrailer,
			fillerstructure.RoleInterstitial, fillerstructure.RoleProgrammeFragment, fillerstructure.RoleNonFiller:
		default:
			return false
		}
	}
	return true
}
