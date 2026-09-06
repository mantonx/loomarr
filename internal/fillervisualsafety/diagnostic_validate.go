package fillervisualsafety

import (
	"errors"
	"math"
	"slices"
	"strings"
	"time"
)

func SealPortableDiagnosticAuthority(authority PortableDiagnosticAuthority, capability PortableCapability, profile CoverageProfile) (PortableDiagnosticAuthority, error) {
	authority.SchemaVersion = PortableDiagnosticAuthoritySchemaVersion
	authority.ContractVersion = PortableDiagnosticAuthorityContractVersion
	authority.AuthoredAt = authority.AuthoredAt.UTC()
	authority.Thresholds = slices.Clone(authority.Thresholds)
	authority.Cases = clonePortableDiagnosticCases(authority.Cases)
	slices.Sort(authority.Thresholds)
	slices.SortFunc(authority.Cases, func(left, right PortableDiagnosticCase) int {
		return strings.Compare(left.Alias, right.Alias)
	})
	for index := range authority.Cases {
		slices.Sort(authority.Cases[index].Slices)
	}
	authority.SHA256 = PortableDiagnosticAuthoritySHA256(authority)
	if err := ValidatePortableDiagnosticAuthority(authority, capability, profile); err != nil {
		return PortableDiagnosticAuthority{}, err
	}
	return authority, nil
}

func ValidatePortableDiagnosticAuthority(authority PortableDiagnosticAuthority, capability PortableCapability, profile CoverageProfile) error {
	if ValidatePortableCapability(capability) != nil || ValidateCoverageProfile(profile) != nil ||
		authority.SchemaVersion != PortableDiagnosticAuthoritySchemaVersion ||
		authority.ContractVersion != PortableDiagnosticAuthorityContractVersion || authority.AuthoredAt.IsZero() ||
		authority.AuthoredAt.Location() != time.UTC || !validDigest(authority.PolicySHA256) ||
		authority.CapabilitySHA256 != capability.SHA256 || authority.CoverageProfileSHA256 != profile.SHA256 ||
		!validIdentity(authority.ModelID) || !validIdentity(authority.PositiveOutputLabel) ||
		!validDiagnosticScoreTransform(authority.ScoreTransform) || !validIdentity(authority.Implementation) ||
		len(authority.Thresholds) == 0 || len(authority.Thresholds) > MaximumDiagnosticThresholds ||
		math.IsNaN(authority.MaximumCleanFalsePositiveRate) || math.IsInf(authority.MaximumCleanFalsePositiveRate, 0) ||
		authority.MaximumCleanFalsePositiveRate < 0 || authority.MaximumCleanFalsePositiveRate > 1 ||
		len(authority.Cases) == 0 || len(authority.Cases) > MaximumDiagnosticCases || authority.SHA256 == "" ||
		authority.SHA256 != PortableDiagnosticAuthoritySHA256(authority) {
		return errors.New("portable visual diagnostic authority identity is invalid")
	}
	model, ok := diagnosticModel(capability, authority.ModelID)
	positiveIndex := slices.Index(model.OutputLabels, authority.PositiveOutputLabel)
	if !ok || positiveIndex < 0 ||
		authority.ScoreTransform == DiagnosticScoreCumulativeSoftmax && positiveIndex == 0 ||
		!validDiagnosticThresholds(authority.Thresholds) {
		return errors.New("portable visual diagnostic scoring declaration is invalid")
	}
	aliases, sources, families := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	previousAlias := ""
	for _, item := range authority.Cases {
		if !validIdentity(item.Alias) || item.Alias <= previousAlias || ValidateSourceAuthority(item.SourceAuthority) != nil ||
			item.SourceAuthority.PolicySHA256 != authority.PolicySHA256 || item.SourceAuthority.DurationMS > profile.MaximumSourceDurationMS ||
			item.SourceAuthority.MeasuredAt.After(authority.AuthoredAt) || !validIdentity(item.SourceFamilyID) ||
			!validDigest(item.RightsSHA256) || len(item.Slices) == 0 || len(item.Slices) > 16 ||
			!strictDiagnosticIdentities(item.Slices) {
			return errors.New("portable visual diagnostic case identity is invalid")
		}
		previousAlias = item.Alias
		if _, duplicate := aliases[item.Alias]; duplicate {
			return errors.New("portable visual diagnostic repeats a case alias")
		}
		if _, duplicate := sources[item.SourceAuthority.SourceSHA256]; duplicate {
			return errors.New("portable visual diagnostic repeats source content")
		}
		if _, duplicate := families[item.SourceFamilyID]; duplicate {
			return errors.New("portable visual diagnostic is not source-family-disjoint")
		}
		aliases[item.Alias], sources[item.SourceAuthority.SourceSHA256], families[item.SourceFamilyID] = struct{}{}, struct{}{}, struct{}{}
		if !validPortableDiagnosticTruth(item, profile.MinimumCoveredExposureMS) || !validDiagnosticSlices(item.TruthLabel, item.Slices) {
			return errors.New("portable visual diagnostic truth authority is invalid")
		}
	}
	return nil
}

func validDiagnosticScoreTransform(transform string) bool {
	return transform == DiagnosticScoreSoftmax || transform == DiagnosticScoreCumulativeSoftmax
}

func PortableDiagnosticAuthoritySHA256(authority PortableDiagnosticAuthority) string {
	authority.SHA256 = ""
	return digestJSON(authority)
}

func validatePortableDiagnosticRuns(authority PortableDiagnosticAuthority, capability PortableCapability, profile CoverageProfile, runs []PortableDiagnosticRun) error {
	if len(runs) != len(authority.Cases) {
		return errors.New("portable visual diagnostic results are incomplete")
	}
	for index, run := range runs {
		item := authority.Cases[index]
		if run.Alias != item.Alias || ValidateCoveragePlan(run.Plan) != nil || run.Plan.SourceAuthoritySHA256 != item.SourceAuthority.SHA256 ||
			run.Plan.SourceSHA256 != item.SourceAuthority.SourceSHA256 || run.Plan.Profile.SHA256 != profile.SHA256 {
			return errors.New("portable visual diagnostic run identity is invalid")
		}
		switch run.State {
		case DiagnosticRunComplete:
			if run.FailureCode != "" || run.Coverage == nil || run.Inference == nil ||
				ValidateCoverageEvidence(run.Plan, *run.Coverage) != nil ||
				ValidatePortableInferenceEvidence(capability, run.Plan, *run.Coverage, *run.Inference) != nil {
				return errors.New("portable visual diagnostic complete run evidence is invalid")
			}
		case DiagnosticRunCoverageHold, DiagnosticRunWorkerFailed:
			if !validIdentity(run.FailureCode) || run.Coverage != nil || run.Inference != nil {
				return errors.New("portable visual diagnostic failure evidence is invalid")
			}
		default:
			return errors.New("portable visual diagnostic run state is invalid")
		}
	}
	return nil
}

func validPortableDiagnosticTruth(item PortableDiagnosticCase, minimumExposureMS int64) bool {
	switch item.TruthLabel {
	case DiagnosticTruthPositive:
		if !validDigest(item.TruthAuthoritySHA256) || len(item.PositiveIntervals) == 0 {
			return false
		}
		var previousEnd int64
		for index, interval := range item.PositiveIntervals {
			if interval.StartMS < 0 || interval.EndMS <= interval.StartMS || interval.EndMS > item.SourceAuthority.DurationMS ||
				interval.EndMS-interval.StartMS < minimumExposureMS || index > 0 && interval.StartMS < previousEnd {
				return false
			}
			previousEnd = interval.EndMS
		}
		return true
	case DiagnosticTruthClean:
		return validDigest(item.TruthAuthoritySHA256) && len(item.PositiveIntervals) == 0
	case DiagnosticTruthUnresolved:
		return item.TruthAuthoritySHA256 == "" && len(item.PositiveIntervals) == 0
	default:
		return false
	}
}

func validDiagnosticThresholds(values []float64) bool {
	for index, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value >= 1 || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func strictDiagnosticIdentities(values []string) bool {
	if !slices.IsSorted(values) {
		return false
	}
	for index, value := range values {
		if !validIdentity(value) || index > 0 && values[index-1] == value {
			return false
		}
	}
	return true
}

func validDiagnosticSlices(label DiagnosticTruthLabel, values []string) bool {
	positive := []string{
		DiagnosticSliceAnimation, DiagnosticSliceCompilationPlacement, DiagnosticSliceCropLetterbox,
		DiagnosticSliceCuts, DiagnosticSliceDamagedTail, DiagnosticSliceLowLight, DiagnosticSliceMonochrome,
		DiagnosticSliceMultiplePeople, DiagnosticSliceShortExposure, DiagnosticSliceTranscode, DiagnosticSliceVFRCFR,
	}
	clean := []string{
		DiagnosticSliceAdvertising, DiagnosticSliceAnimation, DiagnosticSliceBeach, DiagnosticSliceHistoricalGraphics,
		DiagnosticSliceMedical, DiagnosticSliceProgramme, DiagnosticSliceSkinTone, DiagnosticSliceUnderwear,
		DiagnosticSliceVisuallyBusy,
	}
	for _, value := range values {
		if label == DiagnosticTruthPositive && !slices.Contains(positive, value) ||
			label == DiagnosticTruthClean && !slices.Contains(clean, value) ||
			label == DiagnosticTruthUnresolved && !slices.Contains(append(positive, clean...), value) {
			return false
		}
	}
	return true
}

func diagnosticModel(capability PortableCapability, modelID string) (PortableModelArtifact, bool) {
	for _, model := range capability.Models {
		if model.ID == modelID {
			return model, true
		}
	}
	return PortableModelArtifact{}, false
}

func clonePortableDiagnosticCases(cases []PortableDiagnosticCase) []PortableDiagnosticCase {
	cloned := slices.Clone(cases)
	for index := range cloned {
		cloned[index].Slices = slices.Clone(cloned[index].Slices)
		cloned[index].PositiveIntervals = slices.Clone(cloned[index].PositiveIntervals)
	}
	return cloned
}
