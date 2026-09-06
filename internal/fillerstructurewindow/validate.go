package fillerstructurewindow

import (
	"errors"
	"reflect"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

// ValidatePlan reproduces the complete window geometry and content identity rather than accepting
// a merely plausible list of intervals.
func ValidatePlan(plan Plan) error {
	if plan.SchemaVersion != PlanSchemaVersion || plan.ContractVersion != PlanContractVersion ||
		plan.Profile != CanonicalProfile() || plan.Profile.SHA256 != ProfileSHA256(plan.Profile) ||
		plan.Source.DurationMS > plan.Profile.MaximumSourceDurationMS ||
		len(plan.Windows) == 0 || len(plan.Windows) > plan.Profile.MaximumWindows ||
		plan.SHA256 == "" || plan.SHA256 != PlanSHA256(plan) {
		return errors.New("structure window plan identity is invalid")
	}
	if err := fillerstructure.ValidateSource(plan.Source); err != nil {
		return err
	}
	want := planWindows(plan.Source.DurationMS, plan.Profile)
	if !reflect.DeepEqual(plan.Windows, want) {
		return errors.New("structure window plan does not reproduce complete primary coverage")
	}
	for _, window := range plan.Windows {
		if window.PrimaryStartMS < window.MediaStartMS || window.PrimaryEndMS > window.MediaEndMS ||
			window.PrimaryStartMS >= window.PrimaryEndMS || window.MediaStartMS >= window.MediaEndMS ||
			window.MediaEndMS-window.MediaStartMS > plan.Profile.MaximumWindowDurationMS {
			return errors.New("structure window media context is invalid")
		}
	}
	return nil
}
