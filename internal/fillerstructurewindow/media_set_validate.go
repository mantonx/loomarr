package fillerstructurewindow

import "errors"

// ValidateMediaSet reproduces the complete plan and every ordinal's exact media constraints.
func ValidateMediaSet(set MediaSet) error {
	if set.SchemaVersion != MediaSetSchemaVersion || set.ContractVersion != MediaSetContractVersion ||
		!contentHash(set.SHA256) || set.SHA256 != MediaSetSHA256(set) {
		return errors.New("structure window media set identity is invalid")
	}
	if err := ValidatePlan(set.Plan); err != nil {
		return err
	}
	if len(set.Windows) != len(set.Plan.Windows) {
		return errors.New("structure window media set is incomplete")
	}
	for ordinal, item := range set.Windows {
		window := set.Plan.Windows[ordinal]
		media := item.Media
		windowDuration := window.MediaEndMS - window.MediaStartMS
		if item.Ordinal != ordinal || !contentHash(media.SHA256) || media.Bytes <= 0 ||
			media.Bytes > set.Plan.Profile.MaximumWindowBytes || media.DurationMS <= 0 ||
			absolute(media.DurationMS-windowDuration) > set.Plan.Profile.MaximumTimelineDriftMS ||
			media.ProfileSHA256 != set.Plan.Profile.AssessmentMediaProfileSHA256 ||
			!contentHash(media.LineageSHA256) {
			return errors.New("structure window media set item is invalid")
		}
	}
	return nil
}
