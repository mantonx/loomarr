package fillervisualsafety

import (
	"errors"
	"math"
	"slices"
)

const (
	CoverageProfileSchemaVersion    = 1
	CoverageProfileContractVersion  = "filler-visual-coverage-profile-v1"
	CoveragePlanSchemaVersion       = 1
	CoveragePlanContractVersion     = "filler-visual-coverage-plan-v1"
	CoverageEvidenceSchemaVersion   = 1
	CoverageEvidenceContractVersion = "filler-visual-coverage-evidence-v1"
	MaximumObservations             = 10_000
	MaximumFrameBytes               = int64(64 << 20)
)

// CoverageProfile declares what a deterministic sampling plan may claim. The
// display floor is certification authority, not a value inferred by production.
type CoverageProfile struct {
	SchemaVersion            int    `json:"schemaVersion"`
	ContractVersion          string `json:"contractVersion"`
	Implementation           string `json:"implementation"`
	MaximumSourceDurationMS  int64  `json:"maximumSourceDurationMs"`
	ObservationIntervalMS    int64  `json:"observationIntervalMs"`
	MaximumTimestampDriftMS  int64  `json:"maximumTimestampDriftMs"`
	MaximumObservations      int    `json:"maximumObservations"`
	MinimumCoveredExposureMS int64  `json:"minimumCoveredExposureMs"`
	SHA256                   string `json:"sha256"`
}

// CoveragePoint is one exact source-relative frame request.
type CoveragePoint struct {
	Ordinal     int   `json:"ordinal"`
	RequestedMS int64 `json:"requestedMs"`
}

// CoveragePlan covers the complete source from its first through last representable millisecond.
type CoveragePlan struct {
	SchemaVersion         int                 `json:"schemaVersion"`
	ContractVersion       string              `json:"contractVersion"`
	SourceAuthoritySHA256 string              `json:"sourceAuthoritySha256"`
	SourceSHA256          string              `json:"sourceSha256"`
	DurationMS            int64               `json:"durationMs"`
	Video                 VideoStreamIdentity `json:"video"`
	Profile               CoverageProfile     `json:"profile"`
	Points                []CoveragePoint     `json:"points"`
	MaximumPlannedGapMS   int64               `json:"maximumPlannedGapMs"`
	SHA256                string              `json:"sha256"`
}

// FrameEvidence binds one requested observation to the decoded frame bytes and timestamp.
type FrameEvidence struct {
	Ordinal     int    `json:"ordinal"`
	RequestedMS int64  `json:"requestedMs"`
	ObservedMS  int64  `json:"observedMs"`
	SHA256      string `json:"sha256"`
	Bytes       int64  `json:"bytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

// CoverageEvidence proves that every planned frame was decoded from the complete source.
type CoverageEvidence struct {
	SchemaVersion         int             `json:"schemaVersion"`
	ContractVersion       string          `json:"contractVersion"`
	PlanSHA256            string          `json:"planSha256"`
	SourceAuthoritySHA256 string          `json:"sourceAuthoritySha256"`
	Decoder               ToolIdentity    `json:"decoder"`
	CompleteDecode        bool            `json:"completeDecode"`
	Frames                []FrameEvidence `json:"frames"`
	MaximumObservedGapMS  int64           `json:"maximumObservedGapMs"`
	SHA256                string          `json:"sha256"`
}

// SealCoverageProfile canonicalizes and validates a certification-selected profile.
func SealCoverageProfile(profile CoverageProfile) (CoverageProfile, error) {
	profile.SchemaVersion = CoverageProfileSchemaVersion
	profile.ContractVersion = CoverageProfileContractVersion
	profile.SHA256 = CoverageProfileSHA256(profile)
	if err := ValidateCoverageProfile(profile); err != nil {
		return CoverageProfile{}, err
	}
	return profile, nil
}

func ValidateCoverageProfile(profile CoverageProfile) error {
	if profile.SchemaVersion != CoverageProfileSchemaVersion || profile.ContractVersion != CoverageProfileContractVersion ||
		!validIdentity(profile.Implementation) || profile.MaximumSourceDurationMS <= 0 ||
		profile.MaximumSourceDurationMS > MaximumSourceDurationMS || profile.ObservationIntervalMS <= 0 ||
		profile.MaximumTimestampDriftMS < 0 || profile.MaximumTimestampDriftMS > profile.ObservationIntervalMS/2 ||
		profile.MaximumObservations <= 0 || profile.MaximumObservations > MaximumObservations ||
		profile.ObservationIntervalMS > math.MaxInt64-2*profile.MaximumTimestampDriftMS ||
		profile.MinimumCoveredExposureMS <= profile.ObservationIntervalMS+2*profile.MaximumTimestampDriftMS ||
		profile.SHA256 == "" || profile.SHA256 != CoverageProfileSHA256(profile) {
		return errors.New("visual-safety coverage profile is invalid")
	}
	return nil
}

func CoverageProfileSHA256(profile CoverageProfile) string {
	profile.SHA256 = ""
	return digestJSON(profile)
}

// PlanCoverage deterministically covers the authority's complete video timeline.
func PlanCoverage(authority SourceAuthority, profile CoverageProfile) (CoveragePlan, error) {
	if ValidateSourceAuthority(authority) != nil || ValidateCoverageProfile(profile) != nil ||
		authority.DurationMS > profile.MaximumSourceDurationMS {
		return CoveragePlan{}, errors.New("visual-safety coverage input is invalid")
	}
	points, maximumGap, err := coveragePoints(authority.Video.FirstFrameMS, authority.Video.LastFrameMS, profile)
	if err != nil {
		return CoveragePlan{}, err
	}
	plan := CoveragePlan{
		SchemaVersion: CoveragePlanSchemaVersion, ContractVersion: CoveragePlanContractVersion,
		SourceAuthoritySHA256: authority.SHA256, SourceSHA256: authority.SourceSHA256,
		DurationMS: authority.DurationMS, Video: authority.Video,
		Profile: profile, Points: points, MaximumPlannedGapMS: maximumGap,
	}
	plan.SHA256 = CoveragePlanSHA256(plan)
	if err := ValidateCoveragePlan(plan); err != nil {
		return CoveragePlan{}, err
	}
	return plan, nil
}

func ValidateCoveragePlan(plan CoveragePlan) error {
	if plan.SchemaVersion != CoveragePlanSchemaVersion || plan.ContractVersion != CoveragePlanContractVersion ||
		!validDigest(plan.SourceAuthoritySHA256) || !validDigest(plan.SourceSHA256) || plan.DurationMS <= 0 ||
		plan.Video.DurationMS != plan.DurationMS || plan.Video.Index < 0 || !validIdentity(plan.Video.Codec) ||
		plan.Video.Width <= 0 || plan.Video.Height <= 0 || plan.Video.FirstFrameMS < 0 ||
		plan.Video.LastFrameMS < plan.Video.FirstFrameMS || plan.Video.LastFrameMS >= plan.DurationMS ||
		plan.Video.FrameRateNumerator <= 0 ||
		plan.Video.FrameRateDenominator <= 0 || plan.Video.TimeBaseNumerator <= 0 || plan.Video.TimeBaseDenominator <= 0 ||
		ValidateCoverageProfile(plan.Profile) != nil || plan.DurationMS > plan.Profile.MaximumSourceDurationMS {
		return errors.New("visual-safety coverage plan identity is invalid")
	}
	want, maximumGap, err := coveragePoints(plan.Video.FirstFrameMS, plan.Video.LastFrameMS, plan.Profile)
	if err != nil || !slices.Equal(want, plan.Points) || plan.MaximumPlannedGapMS != maximumGap ||
		plan.MaximumPlannedGapMS >= plan.Profile.MinimumCoveredExposureMS || plan.SHA256 == "" ||
		plan.SHA256 != CoveragePlanSHA256(plan) {
		return errors.New("visual-safety coverage plan does not reproduce")
	}
	return nil
}

func CoveragePlanSHA256(plan CoveragePlan) string {
	plan.SHA256 = ""
	return digestJSON(plan)
}

// SealCoverageEvidence binds complete decoded output to its plan.
func SealCoverageEvidence(plan CoveragePlan, decoder ToolIdentity, frames []FrameEvidence, completeDecode bool) (CoverageEvidence, error) {
	maximumGap, err := validateFrames(plan, frames)
	if err != nil || !completeDecode || !validTool(decoder) {
		return CoverageEvidence{}, errors.New("visual-safety coverage evidence is incomplete")
	}
	evidence := CoverageEvidence{
		SchemaVersion: CoverageEvidenceSchemaVersion, ContractVersion: CoverageEvidenceContractVersion,
		PlanSHA256: plan.SHA256, SourceAuthoritySHA256: plan.SourceAuthoritySHA256,
		Decoder: decoder, CompleteDecode: true, Frames: slices.Clone(frames), MaximumObservedGapMS: maximumGap,
	}
	evidence.SHA256 = CoverageEvidenceSHA256(evidence)
	if err := ValidateCoverageEvidence(plan, evidence); err != nil {
		return CoverageEvidence{}, err
	}
	return evidence, nil
}

func ValidateCoverageEvidence(plan CoveragePlan, evidence CoverageEvidence) error {
	if ValidateCoveragePlan(plan) != nil || evidence.SchemaVersion != CoverageEvidenceSchemaVersion ||
		evidence.ContractVersion != CoverageEvidenceContractVersion || evidence.PlanSHA256 != plan.SHA256 ||
		evidence.SourceAuthoritySHA256 != plan.SourceAuthoritySHA256 || !validTool(evidence.Decoder) || !evidence.CompleteDecode {
		return errors.New("visual-safety coverage evidence identity is invalid")
	}
	maximumGap, err := validateFrames(plan, evidence.Frames)
	if err != nil || maximumGap != evidence.MaximumObservedGapMS ||
		maximumGap >= plan.Profile.MinimumCoveredExposureMS || evidence.SHA256 == "" ||
		evidence.SHA256 != CoverageEvidenceSHA256(evidence) {
		return errors.New("visual-safety coverage evidence does not reproduce")
	}
	return nil
}

func CoverageEvidenceSHA256(evidence CoverageEvidence) string {
	evidence.SHA256 = ""
	return digestJSON(evidence)
}

func coveragePoints(firstFrameMS, lastFrameMS int64, profile CoverageProfile) ([]CoveragePoint, int64, error) {
	if firstFrameMS < 0 || lastFrameMS < firstFrameMS || profile.ObservationIntervalMS <= 0 {
		return nil, 0, errors.New("visual-safety coverage geometry is invalid")
	}
	span := lastFrameMS - firstFrameMS
	count := int(span/profile.ObservationIntervalMS) + 1
	if span%profile.ObservationIntervalMS != 0 {
		count++
	}
	if count > profile.MaximumObservations || count > MaximumObservations {
		return nil, 0, errors.New("visual-safety coverage exceeds its observation ceiling")
	}
	points := make([]CoveragePoint, 0, count)
	for at := firstFrameMS; at < lastFrameMS; at += profile.ObservationIntervalMS {
		// A terminal edge inside the grid point's two-sided drift window can
		// legitimately resolve to the same physical frame. Keep the measured
		// terminal edge and omit that overlapping grid point instead.
		if at != firstFrameMS && lastFrameMS-at <= 2*profile.MaximumTimestampDriftMS {
			break
		}
		points = append(points, CoveragePoint{Ordinal: len(points), RequestedMS: at})
	}
	if len(points) == 0 || points[len(points)-1].RequestedMS != lastFrameMS {
		points = append(points, CoveragePoint{Ordinal: len(points), RequestedMS: lastFrameMS})
	}
	maximumGap := int64(0)
	for index := 1; index < len(points); index++ {
		maximumGap = max(maximumGap, points[index].RequestedMS-points[index-1].RequestedMS)
	}
	return points, maximumGap, nil
}

func validateFrames(plan CoveragePlan, frames []FrameEvidence) (int64, error) {
	if ValidateCoveragePlan(plan) != nil || len(frames) != len(plan.Points) {
		return 0, errors.New("visual-safety frame evidence is incomplete")
	}
	maximumGap := int64(0)
	var previous int64
	for index, frame := range frames {
		if !validFrame(plan, index, frame) {
			return 0, errors.New("visual-safety frame evidence is invalid")
		}
		if index > 0 {
			if frame.ObservedMS <= previous {
				return 0, errors.New("visual-safety frame evidence is not ordered")
			}
			maximumGap = max(maximumGap, frame.ObservedMS-previous)
		}
		previous = frame.ObservedMS
	}
	if len(frames) > 0 {
		maximumGap = max(maximumGap, frames[0].ObservedMS-plan.Video.FirstFrameMS)
		maximumGap = max(maximumGap, plan.Video.LastFrameMS-frames[len(frames)-1].ObservedMS)
	}
	return maximumGap, nil
}

func validFrame(plan CoveragePlan, index int, frame FrameEvidence) bool {
	if index < 0 || index >= len(plan.Points) {
		return false
	}
	point := plan.Points[index]
	return frame.Ordinal == point.Ordinal && frame.RequestedMS == point.RequestedMS && frame.ObservedMS >= 0 &&
		frame.ObservedMS < plan.DurationMS && absoluteDifference(frame.ObservedMS, frame.RequestedMS) <= plan.Profile.MaximumTimestampDriftMS &&
		validDigest(frame.SHA256) && frame.Bytes > 0 && frame.Bytes <= MaximumFrameBytes &&
		frame.Width == plan.Video.Width && frame.Height == plan.Video.Height
}

func absoluteDifference(left, right int64) int64 {
	if left >= right {
		return left - right
	}
	return right - left
}
