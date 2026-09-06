package fillerairworthiness

type policyAction uint8

const (
	policyAllow policyAction = iota
	policyReview
	policyReject
)

func policyActionFor(profile Profile, observation Observation) policyAction {
	if profile == ProfileRestrictedArchive {
		return policyAllow
	}
	if profile == ProfileAllAges {
		return allAgesAction(observation)
	}
	return generalAudienceAction(observation)
}

func allAgesAction(observation Observation) policyAction {
	switch observation.Flag {
	case FlagAdultNudity, FlagGraphicViolenceOrGore, FlagHatefulTargeting,
		FlagMinorNudityOrSexualRisk, FlagRegulatedProductPromotion,
		FlagSelfHarmOrSuicide, FlagSexualActivityOrPresentation,
		FlagSlurOrDegradingLanguage, FlagExplicitSexualLanguage:
		return policyReject
	case FlagWeaponDepiction, FlagAlcohol, FlagDrug, FlagGambling, FlagTobaccoOrNicotine:
		if promotionOrInstruction(observation.Context) || observation.Severity != SeverityLow {
			return policyReject
		}
		return policyReview
	case FlagThreat, FlagNonGraphicViolence, FlagHumanDeathOrCorpse, FlagAnimalHarmOrDeath:
		if observation.Severity != SeverityLow {
			return policyReject
		}
		return policyReview
	case FlagHateOrExtremistSymbol:
		if promotionOrInstruction(observation.Context) || observation.Severity == SeverityHigh {
			return policyReject
		}
		return policyReview
	case FlagProfanity:
		if observation.Severity != SeverityLow {
			return policyReject
		}
		return policyReview
	case FlagFrighteningOrDisturbing, FlagSevereInjuryOrMedical:
		if observation.Severity == SeverityHigh {
			return policyReject
		}
		return policyReview
	default:
		return policyAllow
	}
}

func generalAudienceAction(observation Observation) policyAction {
	switch observation.Flag {
	case FlagAdultNudity, FlagGraphicViolenceOrGore, FlagHatefulTargeting,
		FlagMinorNudityOrSexualRisk, FlagRegulatedProductPromotion,
		FlagSlurOrDegradingLanguage:
		return policyReject
	case FlagSexualActivityOrPresentation, FlagExplicitSexualLanguage:
		if observation.Severity == SeverityLow {
			return policyReview
		}
		return policyReject
	case FlagWeaponDepiction:
		if promotionOrInstruction(observation.Context) {
			return policyReject
		}
		if observation.Severity == SeverityHigh {
			return policyReview
		}
		return policyAllow
	case FlagThreat, FlagNonGraphicViolence:
		switch observation.Severity {
		case SeverityHigh:
			return policyReject
		case SeverityModerate:
			return policyReview
		default:
			return policyAllow
		}
	case FlagHumanDeathOrCorpse:
		if observation.Severity == SeverityHigh {
			return policyReject
		}
		return policyReview
	case FlagAnimalHarmOrDeath:
		switch observation.Severity {
		case SeverityHigh:
			return policyReject
		case SeverityModerate:
			return policyReview
		default:
			return policyAllow
		}
	case FlagSelfHarmOrSuicide:
		if promotionOrInstruction(observation.Context) || observation.Severity == SeverityHigh {
			return policyReject
		}
		return policyReview
	case FlagAlcohol, FlagDrug, FlagGambling, FlagTobaccoOrNicotine:
		if promotionOrInstruction(observation.Context) {
			return policyReject
		}
		if observation.Severity == SeverityHigh {
			return policyReview
		}
		return policyAllow
	case FlagHateOrExtremistSymbol:
		if promotionOrInstruction(observation.Context) {
			return policyReject
		}
		if observation.Severity == SeverityHigh {
			return policyReview
		}
		return policyAllow
	case FlagProfanity:
		switch observation.Severity {
		case SeverityHigh:
			return policyReject
		case SeverityModerate:
			return policyReview
		default:
			return policyAllow
		}
	case FlagFrighteningOrDisturbing, FlagSevereInjuryOrMedical:
		switch observation.Severity {
		case SeverityHigh:
			return policyReject
		case SeverityModerate:
			return policyReview
		default:
			return policyAllow
		}
	default:
		return policyAllow
	}
}

func promotionOrInstruction(context Context) bool {
	return context == ContextPromotion || context == ContextInstruction
}
