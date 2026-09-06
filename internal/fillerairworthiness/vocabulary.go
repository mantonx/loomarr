package fillerairworthiness

import "slices"

var vocabulary = []Flag{
	FlagAdultNudity,
	FlagAgeAmbiguous,
	FlagAlcohol,
	FlagAnimalHarmOrDeath,
	FlagCommercialOrBrand,
	FlagDrug,
	FlagExplicitSexualLanguage,
	FlagFrighteningOrDisturbing,
	FlagGambling,
	FlagGraphicViolenceOrGore,
	FlagHateOrExtremistSymbol,
	FlagHatefulTargeting,
	FlagHumanDeathOrCorpse,
	FlagMinorNudityOrSexualRisk,
	FlagMinorPresent,
	FlagNonGraphicViolence,
	FlagProfanity,
	FlagRegulatedProductPromotion,
	FlagReligiousSuffering,
	FlagSelfHarmOrSuicide,
	FlagSevereInjuryOrMedical,
	FlagSexualActivityOrPresentation,
	FlagSlurOrDegradingLanguage,
	FlagThreat,
	FlagTobaccoOrNicotine,
	FlagWarOrMilitary,
	FlagWeaponDepiction,
}

var policyFlags = []Flag{
	FlagAdultNudity,
	FlagAlcohol,
	FlagAnimalHarmOrDeath,
	FlagDrug,
	FlagExplicitSexualLanguage,
	FlagFrighteningOrDisturbing,
	FlagGambling,
	FlagGraphicViolenceOrGore,
	FlagHateOrExtremistSymbol,
	FlagHatefulTargeting,
	FlagHumanDeathOrCorpse,
	FlagMinorNudityOrSexualRisk,
	FlagNonGraphicViolence,
	FlagProfanity,
	FlagRegulatedProductPromotion,
	FlagSelfHarmOrSuicide,
	FlagSevereInjuryOrMedical,
	FlagSexualActivityOrPresentation,
	FlagSlurOrDegradingLanguage,
	FlagThreat,
	FlagTobaccoOrNicotine,
	FlagWeaponDepiction,
}

var axisOrder = []Axis{AxisVisual, AxisSpoken, AxisWritten}

// Vocabulary returns the complete closed version-one suitability vocabulary.
func Vocabulary() []Flag { return slices.Clone(vocabulary) }

func validFlag(value Flag) bool { return slices.Contains(vocabulary, value) }

func validAxis(value Axis) bool { return slices.Contains(axisOrder, value) }

func validProfile(value Profile) bool {
	return value == ProfileAllAges || value == ProfileGeneralAudience || value == ProfileRestrictedArchive
}

func validSeverity(value Severity) bool {
	return value == SeverityLow || value == SeverityModerate || value == SeverityHigh
}

func validContext(value Context) bool {
	return value == ContextPresence || value == ContextDepiction || value == ContextPromotion || value == ContextInstruction
}

func flagOwners(flag Flag) []Axis {
	switch flag {
	case FlagAdultNudity, FlagAgeAmbiguous, FlagAnimalHarmOrDeath, FlagFrighteningOrDisturbing,
		FlagGraphicViolenceOrGore, FlagHumanDeathOrCorpse, FlagMinorNudityOrSexualRisk,
		FlagMinorPresent, FlagNonGraphicViolence, FlagSevereInjuryOrMedical,
		FlagSexualActivityOrPresentation, FlagWeaponDepiction:
		return []Axis{AxisVisual}
	case FlagExplicitSexualLanguage, FlagProfanity, FlagSlurOrDegradingLanguage:
		return []Axis{AxisSpoken, AxisWritten}
	case FlagHateOrExtremistSymbol:
		return []Axis{AxisVisual, AxisWritten}
	case FlagAlcohol, FlagCommercialOrBrand, FlagDrug, FlagGambling, FlagHatefulTargeting,
		FlagRegulatedProductPromotion, FlagReligiousSuffering, FlagSelfHarmOrSuicide,
		FlagThreat, FlagTobaccoOrNicotine, FlagWarOrMilitary:
		return []Axis{AxisVisual, AxisSpoken, AxisWritten}
	default:
		return nil
	}
}

// AxesForFlag returns the safety modalities whose certified coverage can
// support one closed suitability flag.
func AxesForFlag(flag Flag) []Axis { return slices.Clone(flagOwners(flag)) }

func axisOwnsFlag(axis Axis, flag Flag) bool { return slices.Contains(flagOwners(flag), axis) }
