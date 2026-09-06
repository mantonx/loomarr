package fillereval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	TemporalAssessmentSchemaVersion   = 1
	TemporalAssessmentContractVersion = "filler-temporal-unit-role-v1"
)

type UnitKind string

const (
	UnitStandalone       UnitKind = "standalone"
	UnitCompilation      UnitKind = "compilation"
	UnitProgrammeExcerpt UnitKind = "programme_excerpt"
	UnitProgrammeSpots   UnitKind = "programme_with_spots"
	UnitUnusable         UnitKind = "unusable"
	UnitUnclear          UnitKind = "unclear"
)

type TemporalRole string

const (
	TemporalRoleCommercial   TemporalRole = "commercial"
	TemporalRolePromo        TemporalRole = "promo"
	TemporalRoleBumper       TemporalRole = "bumper"
	TemporalRolePSA          TemporalRole = "psa"
	TemporalRoleStationID    TemporalRole = "station_id"
	TemporalRoleTrailer      TemporalRole = "trailer"
	TemporalRoleInterstitial TemporalRole = "interstitial"
	TemporalRoleUnclear      TemporalRole = "unclear"
)

// TemporalSegmentRole classifies every interval in a complete source plan. It is deliberately
// wider than TemporalRole, which applies only after a whole source is known to be standalone.
type TemporalSegmentRole string

const (
	TemporalSegmentCommercial        TemporalSegmentRole = "commercial"
	TemporalSegmentPromo             TemporalSegmentRole = "promo"
	TemporalSegmentBumper            TemporalSegmentRole = "bumper"
	TemporalSegmentPSA               TemporalSegmentRole = "psa"
	TemporalSegmentStationID         TemporalSegmentRole = "station_id"
	TemporalSegmentTrailer           TemporalSegmentRole = "trailer"
	TemporalSegmentInterstitial      TemporalSegmentRole = "interstitial"
	TemporalSegmentProgrammeFragment TemporalSegmentRole = "programme_fragment"
	TemporalSegmentNonFiller         TemporalSegmentRole = "non_filler"
	TemporalSegmentAmbiguous         TemporalSegmentRole = "ambiguous"
	TemporalSegmentUnusable          TemporalSegmentRole = "unusable"
)

type TemporalFailureCode string

const (
	TemporalFailureTimeout          TemporalFailureCode = "timeout"
	TemporalFailureProvider         TemporalFailureCode = "provider_error"
	TemporalFailureInvalidResponse  TemporalFailureCode = "invalid_response"
	TemporalFailureEvidence         TemporalFailureCode = "evidence_error"
	TemporalFailureContextExhausted TemporalFailureCode = "context_exhausted"
)

// TemporalCaseSignals is the sealed evidence namespace available for one case.
// Assessment evidence must resolve to one of these opaque identifiers.
type TemporalCaseSignals struct {
	Alias      string           `json:"alias"`
	DurationMS int64            `json:"durationMs"`
	Signals    []TemporalSignal `json:"signals"`
}

type TemporalSignal struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	AtMS int64  `json:"atMs"`
}

type TemporalAssessorIdentity struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ModelFamily   string `json:"modelFamily"`
	ModelDigest   string `json:"modelDigest"`
	PromptVersion string `json:"promptVersion"`
}

type TemporalAssessmentSet struct {
	SchemaVersion   int                      `json:"schemaVersion"`
	ContractVersion string                   `json:"contractVersion"`
	BatchID         string                   `json:"batchId"`
	PackageSHA256   string                   `json:"packageSha256"`
	Assessor        TemporalAssessorIdentity `json:"assessor"`
	Assessments     []TemporalAssessment     `json:"assessments"`
}

type TemporalAssessment struct {
	Alias              string                      `json:"alias"`
	Unit               *UnitAssessment             `json:"unit,omitempty"`
	Role               *RoleAssessment             `json:"role,omitempty"`
	OperationalFailure *TemporalOperationalFailure `json:"operationalFailure,omitempty"`
	Inference          TemporalInference           `json:"inference"`
}

type UnitAssessment struct {
	Kind              UnitKind `json:"kind"`
	DecisiveSignalIDs []string `json:"decisiveSignalIds,omitempty"`
	Reason            string   `json:"reason"`
}

type RoleAssessment struct {
	Kind              TemporalRole `json:"kind"`
	DecisiveSignalIDs []string     `json:"decisiveSignalIds,omitempty"`
	Reason            string       `json:"reason"`
}

type TemporalOperationalFailure struct {
	Code      TemporalFailureCode `json:"code"`
	Detail    string              `json:"detail"`
	Retryable bool                `json:"retryable"`
}

type TemporalInference struct {
	AssessedAt       time.Time               `json:"assessedAt"`
	Attempts         int                     `json:"attempts"`
	LatencyMS        int64                   `json:"latencyMs,omitempty"`
	PromptTokens     int64                   `json:"promptTokens,omitempty"`
	CompletionTokens int64                   `json:"completionTokens,omitempty"`
	Calls            []TemporalInferenceCall `json:"calls"`
}

type TemporalInferenceCall struct {
	Axis               string              `json:"axis"`
	Attempt            int                 `json:"attempt"`
	ResponseSHA256     string              `json:"responseSha256,omitempty"`
	LatencyMS          int64               `json:"latencyMs,omitempty"`
	PromptTokens       int64               `json:"promptTokens,omitempty"`
	CompletionTokens   int64               `json:"completionTokens,omitempty"`
	OperationalFailure TemporalFailureCode `json:"operationalFailure,omitempty"`
}

type TemporalComparisonReport struct {
	SchemaVersion          int                       `json:"schemaVersion"`
	ContractVersion        string                    `json:"contractVersion"`
	BatchID                string                    `json:"batchId"`
	PackageSHA256          string                    `json:"packageSha256"`
	FirstAssessmentSHA256  string                    `json:"firstAssessmentSha256"`
	SecondAssessmentSHA256 string                    `json:"secondAssessmentSha256"`
	FirstAssessor          TemporalAssessorIdentity  `json:"firstAssessor"`
	SecondAssessor         TemporalAssessorIdentity  `json:"secondAssessor"`
	Cases                  int                       `json:"cases"`
	UnitComparable         int                       `json:"unitComparable"`
	UnitAgreement          int                       `json:"unitAgreement"`
	RoleComparable         int                       `json:"roleComparable"`
	RoleAgreement          int                       `json:"roleAgreement"`
	ExactAgreement         int                       `json:"exactAgreement"`
	AdjudicationRequired   int                       `json:"adjudicationRequired"`
	SystemicFailure        bool                      `json:"systemicFailure"`
	Confusions             []TemporalConfusion       `json:"confusions,omitempty"`
	CalibrationCandidates  []TemporalCalibrationCase `json:"calibrationCandidates,omitempty"`
	CaseComparisons        []TemporalCaseComparison  `json:"caseComparisons"`
}

type TemporalConfusion struct {
	Axis    string   `json:"axis"`
	First   string   `json:"first"`
	Second  string   `json:"second"`
	Count   int      `json:"count"`
	Aliases []string `json:"aliases"`
}

type TemporalCalibrationCase struct {
	Alias   string   `json:"alias"`
	Reasons []string `json:"reasons"`
	Strata  []string `json:"strata"`
}

type TemporalCaseComparison struct {
	Alias          string `json:"alias"`
	FirstUnit      string `json:"firstUnit,omitempty"`
	SecondUnit     string `json:"secondUnit,omitempty"`
	FirstRole      string `json:"firstRole,omitempty"`
	SecondRole     string `json:"secondRole,omitempty"`
	FirstFailure   string `json:"firstFailure,omitempty"`
	SecondFailure  string `json:"secondFailure,omitempty"`
	UnitComparable bool   `json:"unitComparable"`
	UnitAgreement  bool   `json:"unitAgreement"`
	RoleComparable bool   `json:"roleComparable"`
	RoleAgreement  bool   `json:"roleAgreement"`
	ExactAgreement bool   `json:"exactAgreement"`
}

func DecodeTemporalAssessmentSet(data []byte) (TemporalAssessmentSet, error) {
	var set TemporalAssessmentSet
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&set); err != nil {
		return TemporalAssessmentSet{}, fmt.Errorf("decode temporal assessment set: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return TemporalAssessmentSet{}, fmt.Errorf("decode temporal assessment set: trailing JSON value")
		}
		return TemporalAssessmentSet{}, fmt.Errorf("decode temporal assessment set trailing value: %w", err)
	}
	return set, nil
}

func ValidateTemporalAssessmentSet(set TemporalAssessmentSet, batchID, packageSHA256 string, cases []TemporalCaseSignals) error {
	if set.SchemaVersion != TemporalAssessmentSchemaVersion {
		return fmt.Errorf("schema version = %d; want %d", set.SchemaVersion, TemporalAssessmentSchemaVersion)
	}
	if set.ContractVersion != TemporalAssessmentContractVersion {
		return fmt.Errorf("contract version = %q; want %q", set.ContractVersion, TemporalAssessmentContractVersion)
	}
	if strings.TrimSpace(batchID) == "" || set.BatchID != batchID {
		return fmt.Errorf("batch id = %q; want %q", set.BatchID, batchID)
	}
	if !validSHA256(packageSHA256) || set.PackageSHA256 != packageSHA256 {
		return fmt.Errorf("package sha256 = %q; want %q", set.PackageSHA256, packageSHA256)
	}
	if err := validateTemporalAssessor(set.Assessor); err != nil {
		return err
	}

	caseByAlias := make(map[string]TemporalCaseSignals, len(cases))
	for i, c := range cases {
		if err := validateTemporalCaseSignals(c); err != nil {
			return fmt.Errorf("cases[%d]: %w", i, err)
		}
		if _, exists := caseByAlias[c.Alias]; exists {
			return fmt.Errorf("duplicate case alias %q", c.Alias)
		}
		caseByAlias[c.Alias] = c
	}
	if len(set.Assessments) != len(cases) {
		return fmt.Errorf("assessments = %d; want exactly %d", len(set.Assessments), len(cases))
	}

	seen := make(map[string]struct{}, len(set.Assessments))
	for i, assessment := range set.Assessments {
		c, exists := caseByAlias[assessment.Alias]
		if !exists {
			return fmt.Errorf("assessments[%d]: unknown case alias %q", i, assessment.Alias)
		}
		if _, duplicate := seen[assessment.Alias]; duplicate {
			return fmt.Errorf("duplicate assessment alias %q", assessment.Alias)
		}
		seen[assessment.Alias] = struct{}{}
		if err := validateTemporalAssessment(assessment, c); err != nil {
			return fmt.Errorf("assessment %q: %w", assessment.Alias, err)
		}
	}
	return nil
}

func CompareTemporalAssessmentSets(first, second TemporalAssessmentSet, cases []TemporalCaseSignals) (TemporalComparisonReport, error) {
	if err := ValidateTemporalAssessmentSet(first, first.BatchID, first.PackageSHA256, cases); err != nil {
		return TemporalComparisonReport{}, fmt.Errorf("first assessment set: %w", err)
	}
	if err := ValidateTemporalAssessmentSet(second, first.BatchID, first.PackageSHA256, cases); err != nil {
		return TemporalComparisonReport{}, fmt.Errorf("second assessment set: %w", err)
	}
	if first.Assessor.ID == second.Assessor.ID {
		return TemporalComparisonReport{}, fmt.Errorf("assessment sets require distinct assessors")
	}
	if strings.EqualFold(first.Assessor.ModelFamily, second.Assessor.ModelFamily) {
		return TemporalComparisonReport{}, fmt.Errorf("assessment sets require distinct model families")
	}

	firstByAlias := temporalAssessmentIndex(first.Assessments)
	secondByAlias := temporalAssessmentIndex(second.Assessments)
	report := TemporalComparisonReport{
		SchemaVersion: TemporalAssessmentSchemaVersion, ContractVersion: TemporalAssessmentContractVersion,
		BatchID: first.BatchID, PackageSHA256: first.PackageSHA256,
		FirstAssessmentSHA256: TemporalAssessmentSetSHA256(first), SecondAssessmentSHA256: TemporalAssessmentSetSHA256(second),
		FirstAssessor: first.Assessor, SecondAssessor: second.Assessor, Cases: len(cases),
	}
	confusions := map[string]*TemporalConfusion{}
	calibrationReasons := map[string]map[string]struct{}{}
	calibrationStrata := map[string]map[string]struct{}{}
	agreementControls := map[string]string{}
	orderedCases := slices.Clone(cases)
	sort.Slice(orderedCases, func(i, j int) bool { return orderedCases[i].Alias < orderedCases[j].Alias })
	for _, c := range orderedCases {
		a, b := firstByAlias[c.Alias], secondByAlias[c.Alias]
		comparison := compareTemporalCase(a, b)
		report.CaseComparisons = append(report.CaseComparisons, comparison)
		if comparison.UnitComparable {
			report.UnitComparable++
		}
		if comparison.UnitAgreement {
			report.UnitAgreement++
		}
		if comparison.RoleComparable {
			report.RoleComparable++
		}
		if comparison.RoleAgreement {
			report.RoleAgreement++
		}
		if comparison.ExactAgreement {
			report.ExactAgreement++
			stratum := "agreement:" + comparison.FirstUnit
			if comparison.FirstRole != "" {
				stratum += ":" + comparison.FirstRole
			}
			if _, exists := agreementControls[stratum]; !exists {
				agreementControls[stratum] = c.Alias
			}
			continue
		}

		report.AdjudicationRequired++
		if comparison.FirstFailure != "" || comparison.SecondFailure != "" {
			addCalibrationValue(calibrationReasons, c.Alias, "operational_failure")
			addCalibrationValue(calibrationStrata, c.Alias, "operational:"+comparison.FirstFailure+":"+comparison.SecondFailure)
		}
		if comparison.UnitComparable && !comparison.UnitAgreement {
			addTemporalConfusion(confusions, "unit", comparison.FirstUnit, comparison.SecondUnit, c.Alias)
		}
		if comparison.RoleComparable && !comparison.RoleAgreement {
			addTemporalConfusion(confusions, "role", comparison.FirstRole, comparison.SecondRole, c.Alias)
		}
	}

	for _, confusion := range confusions {
		slices.Sort(confusion.Aliases)
		report.Confusions = append(report.Confusions, *confusion)
	}
	sort.Slice(report.Confusions, func(i, j int) bool {
		a, b := report.Confusions[i], report.Confusions[j]
		if a.Axis != b.Axis {
			return a.Axis < b.Axis
		}
		if a.First != b.First {
			return a.First < b.First
		}
		return a.Second < b.Second
	})
	for _, confusion := range report.Confusions {
		alias := confusion.Aliases[0]
		addCalibrationValue(calibrationReasons, alias, confusion.Axis+"_disagreement")
		addCalibrationValue(calibrationStrata, alias, confusion.Axis+":"+confusion.First+":"+confusion.Second)
	}
	for stratum, alias := range agreementControls {
		addCalibrationValue(calibrationReasons, alias, "agreement_control")
		addCalibrationValue(calibrationStrata, alias, stratum)
	}
	for alias, reasons := range calibrationReasons {
		values := make([]string, 0, len(reasons))
		for reason := range reasons {
			values = append(values, reason)
		}
		slices.Sort(values)
		strata := make([]string, 0, len(calibrationStrata[alias]))
		for stratum := range calibrationStrata[alias] {
			strata = append(strata, stratum)
		}
		slices.Sort(strata)
		report.CalibrationCandidates = append(report.CalibrationCandidates, TemporalCalibrationCase{Alias: alias, Reasons: values, Strata: strata})
	}
	sort.Slice(report.CalibrationCandidates, func(i, j int) bool {
		return report.CalibrationCandidates[i].Alias < report.CalibrationCandidates[j].Alias
	})
	report.SystemicFailure = report.AdjudicationRequired*100 > report.Cases*15
	for _, confusion := range report.Confusions {
		if confusion.Count*2 > report.AdjudicationRequired {
			report.SystemicFailure = true
		}
	}
	return report, nil
}

func TemporalAssessmentSetSHA256(set TemporalAssessmentSet) string {
	set.Assessments = slices.Clone(set.Assessments)
	for i := range set.Assessments {
		set.Assessments[i] = NormalizeTemporalAssessment(set.Assessments[i])
	}
	sort.Slice(set.Assessments, func(i, j int) bool { return set.Assessments[i].Alias < set.Assessments[j].Alias })
	data, err := json.Marshal(set)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validateTemporalAssessor(assessor TemporalAssessorIdentity) error {
	if strings.TrimSpace(assessor.ID) == "" || strings.TrimSpace(assessor.Provider) == "" || strings.TrimSpace(assessor.Model) == "" || strings.TrimSpace(assessor.ModelFamily) == "" || strings.TrimSpace(assessor.PromptVersion) == "" {
		return fmt.Errorf("assessor id, provider, model, model family, and prompt version are required")
	}
	if !validSHA256(assessor.ModelDigest) {
		return fmt.Errorf("assessor model digest must be a lowercase sha256")
	}
	return nil
}

func validateTemporalCaseSignals(c TemporalCaseSignals) error {
	if strings.TrimSpace(c.Alias) == "" || c.DurationMS <= 0 {
		return fmt.Errorf("alias and positive duration are required")
	}
	if len(c.Signals) == 0 {
		return fmt.Errorf("at least one signal is required")
	}
	seen := make(map[string]struct{}, len(c.Signals))
	for _, signal := range c.Signals {
		if strings.TrimSpace(signal.ID) == "" {
			return fmt.Errorf("signal id is required")
		}
		if signal.Kind != "frame" && signal.Kind != "ocr" && signal.Kind != "transcript" {
			return fmt.Errorf("signal %q has invalid kind %q", signal.ID, signal.Kind)
		}
		if signal.AtMS < 0 || signal.AtMS > c.DurationMS {
			return fmt.Errorf("signal %q timestamp is outside the case", signal.ID)
		}
		if _, exists := seen[signal.ID]; exists {
			return fmt.Errorf("duplicate signal id %q", signal.ID)
		}
		seen[signal.ID] = struct{}{}
	}
	return nil
}

func validateTemporalAssessment(assessment TemporalAssessment, c TemporalCaseSignals) error {
	if err := validateTemporalInference(assessment.Inference); err != nil {
		return err
	}
	if assessment.Inference.AssessedAt.IsZero() || assessment.Inference.Attempts < 1 || assessment.Inference.LatencyMS < 0 || assessment.Inference.PromptTokens < 0 || assessment.Inference.CompletionTokens < 0 {
		return fmt.Errorf("inference requires assessment time, at least one attempt, and non-negative accounting")
	}
	if assessment.OperationalFailure != nil {
		if assessment.Unit != nil || assessment.Role != nil {
			return fmt.Errorf("operational failure cannot carry unit or role claims")
		}
		if !validTemporalFailure(assessment.OperationalFailure.Code) || strings.TrimSpace(assessment.OperationalFailure.Detail) == "" {
			return fmt.Errorf("operational failure requires a closed code and detail")
		}
		if assessment.Inference.Calls[len(assessment.Inference.Calls)-1].OperationalFailure != assessment.OperationalFailure.Code {
			return fmt.Errorf("operational failure must bind the final inference call")
		}
		return nil
	}
	if assessment.Unit == nil {
		return fmt.Errorf("successful assessment requires a unit claim")
	}
	if !validUnitKind(assessment.Unit.Kind) || strings.TrimSpace(assessment.Unit.Reason) == "" {
		return fmt.Errorf("unit requires a closed kind and reason")
	}
	if err := validateTemporalEvidence(assessment.Unit.DecisiveSignalIDs, assessment.Unit.Kind == UnitUnclear, c); err != nil {
		return fmt.Errorf("unit: %w", err)
	}
	if assessment.Unit.Kind == UnitStandalone {
		if assessment.Role == nil {
			return fmt.Errorf("standalone unit requires a role assessment")
		}
		if !validTemporalRole(assessment.Role.Kind) || strings.TrimSpace(assessment.Role.Reason) == "" {
			return fmt.Errorf("role requires a closed kind and reason")
		}
		if err := validateTemporalEvidence(assessment.Role.DecisiveSignalIDs, assessment.Role.Kind == TemporalRoleUnclear, c); err != nil {
			return fmt.Errorf("role: %w", err)
		}
	} else if assessment.Role != nil {
		return fmt.Errorf("only standalone units may carry a role assessment")
	}
	expectedCalls := 1
	if assessment.Unit.Kind == UnitStandalone {
		expectedCalls = 2
	}
	if len(assessment.Inference.Calls) != expectedCalls || assessment.Inference.Calls[len(assessment.Inference.Calls)-1].OperationalFailure != "" {
		return fmt.Errorf("successful assessment has %d inference calls; want %d successful unit/role calls", len(assessment.Inference.Calls), expectedCalls)
	}
	return nil
}

func validateTemporalInference(inference TemporalInference) error {
	if inference.AssessedAt.IsZero() || inference.Attempts < 1 || inference.Attempts != len(inference.Calls) || inference.LatencyMS < 0 || inference.PromptTokens < 0 || inference.CompletionTokens < 0 {
		return fmt.Errorf("inference requires assessment time, one ledger row per attempt, and non-negative accounting")
	}
	var latencyMS, promptTokens, completionTokens int64
	for index, call := range inference.Calls {
		if call.Attempt != index+1 || call.LatencyMS < 0 || call.PromptTokens < 0 || call.CompletionTokens < 0 || (call.Axis != "unit" && call.Axis != "role") || index == 0 && call.Axis != "unit" || index > 0 && call.Axis != "role" {
			return fmt.Errorf("inference call %d has invalid order, axis, or accounting", index+1)
		}
		if call.ResponseSHA256 != "" && !validSHA256(call.ResponseSHA256) {
			return fmt.Errorf("inference call %d response digest is invalid", index+1)
		}
		if call.ResponseSHA256 == "" && call.OperationalFailure == "" {
			return fmt.Errorf("inference call %d requires a response digest or operational failure", index+1)
		}
		if call.OperationalFailure != "" && !validTemporalFailure(call.OperationalFailure) {
			return fmt.Errorf("inference call %d has an invalid operational failure", index+1)
		}
		latencyMS += call.LatencyMS
		promptTokens += call.PromptTokens
		completionTokens += call.CompletionTokens
	}
	if latencyMS != inference.LatencyMS || promptTokens != inference.PromptTokens || completionTokens != inference.CompletionTokens {
		return fmt.Errorf("inference aggregate accounting does not match its call ledger")
	}
	return nil
}

func validateTemporalEvidence(ids []string, mayBeEmpty bool, c TemporalCaseSignals) error {
	if len(ids) == 0 && !mayBeEmpty {
		return fmt.Errorf("at least one decisive signal is required")
	}
	if len(ids) > 4 {
		return fmt.Errorf("at most four decisive signals are allowed")
	}
	known := make(map[string]struct{}, len(c.Signals))
	for _, signal := range c.Signals {
		known[signal.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, exists := known[id]; !exists {
			return fmt.Errorf("unknown signal %q", id)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate decisive signal %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validUnitKind(kind UnitKind) bool {
	return kind == UnitStandalone || kind == UnitCompilation || kind == UnitProgrammeExcerpt || kind == UnitUnusable || kind == UnitUnclear
}

func validTemporalRole(role TemporalRole) bool {
	return role == TemporalRoleCommercial || role == TemporalRolePromo || role == TemporalRoleBumper || role == TemporalRolePSA || role == TemporalRoleStationID || role == TemporalRoleTrailer || role == TemporalRoleInterstitial || role == TemporalRoleUnclear
}

func validTemporalFailure(code TemporalFailureCode) bool {
	return code == TemporalFailureTimeout || code == TemporalFailureProvider || code == TemporalFailureInvalidResponse || code == TemporalFailureEvidence || code == TemporalFailureContextExhausted
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func temporalAssessmentIndex(assessments []TemporalAssessment) map[string]TemporalAssessment {
	indexed := make(map[string]TemporalAssessment, len(assessments))
	for _, assessment := range assessments {
		indexed[assessment.Alias] = assessment
	}
	return indexed
}

func compareTemporalCase(first, second TemporalAssessment) TemporalCaseComparison {
	comparison := TemporalCaseComparison{Alias: first.Alias}
	if first.OperationalFailure != nil {
		comparison.FirstFailure = string(first.OperationalFailure.Code)
	} else {
		comparison.FirstUnit = string(first.Unit.Kind)
		if first.Role != nil {
			comparison.FirstRole = string(first.Role.Kind)
		}
	}
	if second.OperationalFailure != nil {
		comparison.SecondFailure = string(second.OperationalFailure.Code)
	} else {
		comparison.SecondUnit = string(second.Unit.Kind)
		if second.Role != nil {
			comparison.SecondRole = string(second.Role.Kind)
		}
	}
	comparison.UnitComparable = comparison.FirstFailure == "" && comparison.SecondFailure == ""
	comparison.UnitAgreement = comparison.UnitComparable && comparison.FirstUnit == comparison.SecondUnit
	comparison.RoleComparable = comparison.UnitComparable && comparison.FirstUnit == string(UnitStandalone) && comparison.SecondUnit == string(UnitStandalone)
	comparison.RoleAgreement = comparison.RoleComparable && comparison.FirstRole == comparison.SecondRole
	comparison.ExactAgreement = comparison.UnitAgreement && (comparison.FirstUnit != string(UnitStandalone) || comparison.RoleAgreement)
	return comparison
}

func addTemporalConfusion(confusions map[string]*TemporalConfusion, axis, first, second, alias string) {
	key := strings.Join([]string{axis, first, second}, "\x00")
	confusion, exists := confusions[key]
	if !exists {
		confusion = &TemporalConfusion{Axis: axis, First: first, Second: second}
		confusions[key] = confusion
	}
	confusion.Count++
	confusion.Aliases = append(confusion.Aliases, alias)
}

func addCalibrationValue(calibration map[string]map[string]struct{}, alias, value string) {
	values, exists := calibration[alias]
	if !exists {
		values = map[string]struct{}{}
		calibration[alias] = values
	}
	values[value] = struct{}{}
}

// NormalizeTemporalAssessment makes decisive references set-like while
// retaining the model's claim and explanation exactly.
func NormalizeTemporalAssessment(assessment TemporalAssessment) TemporalAssessment {
	if assessment.Unit != nil {
		unit := *assessment.Unit
		unit.DecisiveSignalIDs = sortedUnique(unit.DecisiveSignalIDs)
		assessment.Unit = &unit
	}
	if assessment.Role != nil {
		role := *assessment.Role
		role.DecisiveSignalIDs = sortedUnique(role.DecisiveSignalIDs)
		assessment.Role = &role
	}
	return assessment
}
