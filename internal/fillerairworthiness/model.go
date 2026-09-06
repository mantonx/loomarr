// Package fillerairworthiness owns deterministic audience-policy evaluation
// over closed, authority-bound filler suitability evidence.
package fillerairworthiness

const (
	EvidenceSchemaVersion   = 1
	EvidenceContractVersion = "filler-airworthiness-evidence-v1"
	DecisionSchemaVersion   = 1
	DecisionContractVersion = "filler-airworthiness-decision-v1"
	PolicyVersion           = "filler-airworthiness-policy-v1"
	VocabularyVersion       = "filler-suitability-vocabulary-v1"
	maximumEvidenceAxes     = 3
	maximumObservations     = 256
	// MaximumRenderedDurationMS bounds an Airworthiness evidence document.
	MaximumRenderedDurationMS = int64(24 * 60 * 60 * 1000)
	maximumRenderedDurationMS = MaximumRenderedDurationMS
)

type Profile string

const (
	ProfileAllAges           Profile = "all_ages"
	ProfileGeneralAudience   Profile = "general_audience"
	ProfileRestrictedArchive Profile = "restricted_archive"
)

type Axis string

const (
	AxisVisual  Axis = "visual"
	AxisSpoken  Axis = "spoken"
	AxisWritten Axis = "written"
)

type Coverage string

const (
	CoverageComplete   Coverage = "complete"
	CoverageIncomplete Coverage = "incomplete"
	CoverageFailed     Coverage = "failed"
	CoverageConflict   Coverage = "conflict"
)

type Flag string

const (
	FlagAdultNudity                  Flag = "adult_nudity"
	FlagAgeAmbiguous                 Flag = "age_ambiguous"
	FlagAlcohol                      Flag = "alcohol"
	FlagAnimalHarmOrDeath            Flag = "animal_harm_or_death"
	FlagCommercialOrBrand            Flag = "commercial_or_brand"
	FlagDrug                         Flag = "drug"
	FlagExplicitSexualLanguage       Flag = "explicit_sexual_language"
	FlagFrighteningOrDisturbing      Flag = "frightening_or_disturbing"
	FlagGambling                     Flag = "gambling"
	FlagGraphicViolenceOrGore        Flag = "graphic_violence_or_gore"
	FlagHateOrExtremistSymbol        Flag = "hate_or_extremist_symbol"
	FlagHatefulTargeting             Flag = "hateful_targeting"
	FlagHumanDeathOrCorpse           Flag = "human_death_or_corpse"
	FlagMinorNudityOrSexualRisk      Flag = "minor_nudity_or_sexual_risk"
	FlagMinorPresent                 Flag = "minor_present"
	FlagNonGraphicViolence           Flag = "non_graphic_violence"
	FlagProfanity                    Flag = "profanity"
	FlagRegulatedProductPromotion    Flag = "regulated_product_promotion"
	FlagReligiousSuffering           Flag = "religious_suffering"
	FlagSelfHarmOrSuicide            Flag = "self_harm_or_suicide"
	FlagSevereInjuryOrMedical        Flag = "severe_injury_or_medical"
	FlagSexualActivityOrPresentation Flag = "sexual_activity_or_presentation"
	FlagSlurOrDegradingLanguage      Flag = "slur_or_degrading_language"
	FlagThreat                       Flag = "threat"
	FlagTobaccoOrNicotine            Flag = "tobacco_or_nicotine"
	FlagWarOrMilitary                Flag = "war_or_military"
	FlagWeaponDepiction              Flag = "weapon_depiction"
)

type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityModerate Severity = "moderate"
	SeverityHigh     Severity = "high"
)

type Context string

const (
	ContextPresence    Context = "presence"
	ContextDepiction   Context = "depiction"
	ContextPromotion   Context = "promotion"
	ContextInstruction Context = "instruction"
)

type AxisProfile struct {
	Axis                 Axis   `json:"axis"`
	EvidenceContract     string `json:"evidenceContract"`
	PolicySHA256         string `json:"policySha256"`
	CertificationSHA256  string `json:"certificationSha256"`
	ImplementationSHA256 string `json:"implementationSha256"`
	CertifiedFlags       []Flag `json:"certifiedFlags"`
}

// Document contains already-reduced axis evidence. It carries positive
// observations and complete-coverage state, never raw model votes or text.
type Document struct {
	SchemaVersion   int            `json:"schemaVersion"`
	ContractVersion string         `json:"contractVersion"`
	SubjectSHA256   string         `json:"subjectSha256"`
	DurationMS      int64          `json:"durationMs"`
	Axes            []AxisEvidence `json:"axes"`
}

type AxisEvidence struct {
	SubjectSHA256  string        `json:"subjectSha256"`
	Profile        AxisProfile   `json:"profile"`
	Coverage       Coverage      `json:"coverage"`
	EvidenceSHA256 string        `json:"evidenceSha256"`
	Observations   []Observation `json:"observations"`
}

type Observation struct {
	ID       string   `json:"id"`
	Flag     Flag     `json:"flag"`
	Severity Severity `json:"severity"`
	Context  Context  `json:"context"`
	StartMS  int64    `json:"startMs"`
	EndMS    int64    `json:"endMs"`
}

type Verdict string

const (
	VerdictPass   Verdict = "pass"
	VerdictReject Verdict = "reject"
	VerdictHold   Verdict = "hold"
)

type Reason string

const (
	ReasonEvidenceSatisfied         Reason = "airworthiness_evidence_satisfied"
	ReasonProhibitedObservation     Reason = "airworthiness_prohibited_observation"
	ReasonObservationRequiresReview Reason = "airworthiness_observation_requires_review"
	ReasonCoverageIncomplete        Reason = "airworthiness_coverage_incomplete"
	ReasonCertificationIncomplete   Reason = "airworthiness_certification_incomplete"
	ReasonEvidenceInvalid           Reason = "airworthiness_evidence_invalid"
	ReasonRestrictedArchive         Reason = "airworthiness_restricted_archive_not_playout"
)

type Effect string

const (
	EffectReject Effect = "reject"
	EffectReview Effect = "review"
)

type Trigger struct {
	ObservationID  string   `json:"observationId"`
	Axis           Axis     `json:"axis"`
	Flag           Flag     `json:"flag"`
	Severity       Severity `json:"severity"`
	Context        Context  `json:"context"`
	StartMS        int64    `json:"startMs"`
	EndMS          int64    `json:"endMs"`
	EvidenceSHA256 string   `json:"evidenceSha256"`
	Effect         Effect   `json:"effect"`
}

// Decision is the path-free, public result. It contains only closed values
// and evidence digests; restricted text and model prose cannot enter it.
type Decision struct {
	SchemaVersion     int       `json:"schemaVersion"`
	ContractVersion   string    `json:"contractVersion"`
	SubjectSHA256     string    `json:"subjectSha256"`
	Profile           Profile   `json:"profile"`
	PolicyVersion     string    `json:"policyVersion"`
	VocabularyVersion string    `json:"vocabularyVersion"`
	AuthoritySHA256   string    `json:"authoritySha256"`
	Verdict           Verdict   `json:"verdict"`
	ReasonCodes       []Reason  `json:"reasonCodes"`
	ObservedFlags     []Flag    `json:"observedFlags"`
	Triggers          []Trigger `json:"triggers"`
	HeldAxes          []Axis    `json:"heldAxes"`
	EvidenceSHA256s   []string  `json:"evidenceSha256s"`
	SHA256            string    `json:"sha256"`
}
