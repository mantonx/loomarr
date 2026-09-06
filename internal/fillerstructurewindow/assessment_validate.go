package fillerstructurewindow

import (
	"errors"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
)

// ValidateAssessment binds a window answer to exact media-set geometry, lineage, assessor
// identity, and either complete source-relative coverage or a closed operational failure.
func ValidateAssessment(set MediaSet, assessment Assessment) error {
	if err := ValidateMediaSet(set); err != nil {
		return err
	}
	plan := set.Plan
	if assessment.SchemaVersion != AssessmentSchemaVersion || assessment.ContractVersion != AssessmentContractVersion ||
		assessment.PlanSHA256 != plan.SHA256 || assessment.MediaSetSHA256 != set.SHA256 || assessment.Source != plan.Source ||
		assessment.WindowOrdinal < 0 || assessment.WindowOrdinal >= len(plan.Windows) ||
		assessment.AssessedAt.IsZero() || assessment.AssessedAt != assessment.AssessedAt.UTC() ||
		!contentHash(assessment.SHA256) || assessment.SHA256 != AssessmentSHA256(assessment) {
		return errors.New("structure window assessment identity is invalid")
	}
	if err := fillerstructure.ValidateAssessorProfile(assessment.Assessor); err != nil {
		return err
	}
	window := plan.Windows[assessment.WindowOrdinal]
	expectedMedia := set.Windows[assessment.WindowOrdinal].Media
	windowDuration := window.MediaEndMS - window.MediaStartMS
	if !contentHash(assessment.Media.SHA256) || assessment.Media.Bytes <= 0 ||
		assessment.Media.Bytes > fillerstructuremedia.MaximumVideoBytes || assessment.Media.DurationMS <= 0 ||
		absolute(assessment.Media.DurationMS-windowDuration) > fillerstructure.AssessmentMediaMaximumTimelineDriftMS ||
		assessment.Media.ProfileSHA256 != plan.Profile.AssessmentMediaProfileSHA256 ||
		!contentHash(assessment.Media.LineageSHA256) || assessment.Media != expectedMedia {
		return errors.New("structure window assessment media is invalid")
	}
	switch assessment.State {
	case AssessmentAccepted:
		if assessment.Failure != "" || !completeWindowSegments(window, assessment.Segments) {
			return errors.New("accepted structure window assessment is incomplete")
		}
	case AssessmentOperationalFailure:
		if len(assessment.Segments) != 0 || !closedFailure(assessment.Failure) {
			return errors.New("failed structure window assessment has semantic output or invalid failure")
		}
	default:
		return errors.New("structure window assessment state is invalid")
	}
	return nil
}

func completeWindowSegments(window Window, segments []fillerstructure.Segment) bool {
	if len(segments) == 0 || segments[0].StartMS != window.MediaStartMS || segments[len(segments)-1].EndMS != window.MediaEndMS {
		return false
	}
	for index, segment := range segments {
		if segment.StartMS < window.MediaStartMS || segment.EndMS > window.MediaEndMS ||
			segment.StartMS >= segment.EndMS || !windowRole(segment.Role) ||
			(index > 0 && segment.StartMS != segments[index-1].EndMS) {
			return false
		}
	}
	return true
}

func windowRole(role fillerstructure.Role) bool {
	switch role {
	case fillerstructure.RoleCommercial, fillerstructure.RolePromo, fillerstructure.RoleBumper,
		fillerstructure.RolePSA, fillerstructure.RoleStationID, fillerstructure.RoleTrailer,
		fillerstructure.RoleInterstitial, fillerstructure.RoleProgrammeFragment,
		fillerstructure.RoleNonFiller, fillerstructure.RoleAmbiguous, fillerstructure.RoleUnusable:
		return true
	default:
		return false
	}
}

func closedFailure(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value && strings.ToLower(value) == value &&
		!strings.ContainsAny(value, " \t\r\n")
}

func contentHash(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func absolute(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
