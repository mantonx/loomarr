package fillerairworthiness

import "testing"

func TestVersionOnePolicyBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		flag     Flag
		severity Severity
		context  Context
		allAges  policyAction
		general  policyAction
	}{
		{"adult nudity", FlagAdultNudity, SeverityLow, ContextDepiction, policyReject, policyReject},
		{"minor sexual risk", FlagMinorNudityOrSexualRisk, SeverityLow, ContextDepiction, policyReject, policyReject},
		{"sexual presentation low", FlagSexualActivityOrPresentation, SeverityLow, ContextDepiction, policyReject, policyReview},
		{"sexual presentation moderate", FlagSexualActivityOrPresentation, SeverityModerate, ContextDepiction, policyReject, policyReject},
		{"weapon low depiction", FlagWeaponDepiction, SeverityLow, ContextDepiction, policyReview, policyAllow},
		{"weapon moderate depiction", FlagWeaponDepiction, SeverityModerate, ContextDepiction, policyReject, policyAllow},
		{"weapon high depiction", FlagWeaponDepiction, SeverityHigh, ContextDepiction, policyReject, policyReview},
		{"weapon promotion", FlagWeaponDepiction, SeverityLow, ContextPromotion, policyReject, policyReject},
		{"threat low", FlagThreat, SeverityLow, ContextDepiction, policyReview, policyAllow},
		{"threat moderate", FlagThreat, SeverityModerate, ContextDepiction, policyReject, policyReview},
		{"threat high", FlagThreat, SeverityHigh, ContextDepiction, policyReject, policyReject},
		{"non-graphic violence moderate", FlagNonGraphicViolence, SeverityModerate, ContextDepiction, policyReject, policyReview},
		{"graphic violence", FlagGraphicViolenceOrGore, SeverityLow, ContextDepiction, policyReject, policyReject},
		{"corpse low", FlagHumanDeathOrCorpse, SeverityLow, ContextDepiction, policyReview, policyReview},
		{"corpse moderate", FlagHumanDeathOrCorpse, SeverityModerate, ContextDepiction, policyReject, policyReview},
		{"corpse high", FlagHumanDeathOrCorpse, SeverityHigh, ContextDepiction, policyReject, policyReject},
		{"animal harm low", FlagAnimalHarmOrDeath, SeverityLow, ContextDepiction, policyReview, policyAllow},
		{"animal harm moderate", FlagAnimalHarmOrDeath, SeverityModerate, ContextDepiction, policyReject, policyReview},
		{"animal harm high", FlagAnimalHarmOrDeath, SeverityHigh, ContextDepiction, policyReject, policyReject},
		{"self harm low", FlagSelfHarmOrSuicide, SeverityLow, ContextDepiction, policyReject, policyReview},
		{"self harm high", FlagSelfHarmOrSuicide, SeverityHigh, ContextDepiction, policyReject, policyReject},
		{"self harm instruction", FlagSelfHarmOrSuicide, SeverityLow, ContextInstruction, policyReject, policyReject},
		{"substance low depiction", FlagAlcohol, SeverityLow, ContextDepiction, policyReview, policyAllow},
		{"substance moderate depiction", FlagDrug, SeverityModerate, ContextDepiction, policyReject, policyAllow},
		{"substance high depiction", FlagTobaccoOrNicotine, SeverityHigh, ContextDepiction, policyReject, policyReview},
		{"gambling promotion", FlagGambling, SeverityLow, ContextPromotion, policyReject, policyReject},
		{"regulated promotion", FlagRegulatedProductPromotion, SeverityLow, ContextPromotion, policyReject, policyReject},
		{"hate symbol low", FlagHateOrExtremistSymbol, SeverityLow, ContextDepiction, policyReview, policyAllow},
		{"hate symbol high", FlagHateOrExtremistSymbol, SeverityHigh, ContextDepiction, policyReject, policyReview},
		{"hate symbol promotion", FlagHateOrExtremistSymbol, SeverityLow, ContextPromotion, policyReject, policyReject},
		{"hateful targeting", FlagHatefulTargeting, SeverityLow, ContextDepiction, policyReject, policyReject},
		{"slur", FlagSlurOrDegradingLanguage, SeverityLow, ContextDepiction, policyReject, policyReject},
		{"profanity low", FlagProfanity, SeverityLow, ContextDepiction, policyReview, policyAllow},
		{"profanity moderate", FlagProfanity, SeverityModerate, ContextDepiction, policyReject, policyReview},
		{"profanity high", FlagProfanity, SeverityHigh, ContextDepiction, policyReject, policyReject},
		{"sexual language low", FlagExplicitSexualLanguage, SeverityLow, ContextDepiction, policyReject, policyReview},
		{"sexual language moderate", FlagExplicitSexualLanguage, SeverityModerate, ContextDepiction, policyReject, policyReject},
		{"frightening low", FlagFrighteningOrDisturbing, SeverityLow, ContextDepiction, policyReview, policyAllow},
		{"medical moderate", FlagSevereInjuryOrMedical, SeverityModerate, ContextDepiction, policyReview, policyReview},
		{"medical high", FlagSevereInjuryOrMedical, SeverityHigh, ContextDepiction, policyReject, policyReject},
		{"minor present", FlagMinorPresent, SeverityHigh, ContextPresence, policyAllow, policyAllow},
		{"age ambiguity", FlagAgeAmbiguous, SeverityHigh, ContextPresence, policyAllow, policyAllow},
		{"religious suffering", FlagReligiousSuffering, SeverityHigh, ContextDepiction, policyAllow, policyAllow},
		{"war", FlagWarOrMilitary, SeverityHigh, ContextDepiction, policyAllow, policyAllow},
		{"commercial", FlagCommercialOrBrand, SeverityHigh, ContextPromotion, policyAllow, policyAllow},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observation := Observation{Flag: test.flag, Severity: test.severity, Context: test.context}
			if got := policyActionFor(ProfileAllAges, observation); got != test.allAges {
				t.Fatalf("all_ages action = %v, want %v", got, test.allAges)
			}
			if got := policyActionFor(ProfileGeneralAudience, observation); got != test.general {
				t.Fatalf("general_audience action = %v, want %v", got, test.general)
			}
		})
	}
}

func TestPolicyIsTotalForClosedVocabulary(t *testing.T) {
	t.Parallel()
	for _, profile := range []Profile{ProfileAllAges, ProfileGeneralAudience, ProfileRestrictedArchive} {
		for _, flag := range Vocabulary() {
			for _, severity := range []Severity{SeverityLow, SeverityModerate, SeverityHigh} {
				for _, context := range []Context{ContextPresence, ContextDepiction, ContextPromotion, ContextInstruction} {
					action := policyActionFor(profile, Observation{Flag: flag, Severity: severity, Context: context})
					if action != policyAllow && action != policyReview && action != policyReject {
						t.Fatalf("no action for profile=%s flag=%s severity=%s context=%s", profile, flag, severity, context)
					}
				}
			}
		}
	}
}
