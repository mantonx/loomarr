package fillerstructurewindowcert

import (
	"encoding/hex"
	"errors"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func NewSuite(cases []Case) (Suite, error) {
	if err := validateCaseInputs(cases); err != nil {
		return Suite{}, err
	}
	suite := Suite{
		SchemaVersion: SuiteSchemaVersion, ContractVersion: SuiteContractVersion,
		BoundaryToleranceMS: BoundaryToleranceMS, Cases: cloneCases(cases),
	}
	suite.HighByteMinimumBytes = highByteMinimum(suite.Cases)
	for index := range suite.Cases {
		derived, err := caseSlices(suite.Cases[index], suite.HighByteMinimumBytes)
		if err != nil {
			return Suite{}, err
		}
		suite.Cases[index].Slices = derived
	}
	suite.SHA256 = SuiteSHA256(suite)
	return suite, ValidateSuite(suite)
}

func ValidateSuite(suite Suite) error {
	if suite.SchemaVersion != SuiteSchemaVersion || suite.ContractVersion != SuiteContractVersion ||
		suite.BoundaryToleranceMS != BoundaryToleranceMS || !validDigest(suite.SHA256) ||
		suite.SHA256 != SuiteSHA256(suite) || len(suite.Cases) < MinimumSliceCases ||
		suite.HighByteMinimumBytes <= 0 || suite.HighByteMinimumBytes != highByteMinimum(suite.Cases) {
		return errors.New("window certification suite identity is invalid")
	}
	if err := validateCaseInputs(suite.Cases); err != nil {
		return err
	}
	counts := make(map[Slice]int, len(requiredSlices))
	previousID := ""
	for _, item := range suite.Cases {
		if !canonicalID(item.ID) || item.ID <= previousID {
			return errors.New("window certification cases are not uniquely ordered")
		}
		previousID = item.ID
		wantSlices, err := caseSlices(item, suite.HighByteMinimumBytes)
		if err != nil || !slices.Equal(item.Slices, wantSlices) {
			return errors.New("window certification case slices do not reproduce")
		}
		for _, slice := range item.Slices {
			counts[slice]++
		}
	}
	for _, slice := range requiredSlices {
		if counts[slice] < MinimumSliceCases {
			return errors.New("window certification suite lacks required slice coverage")
		}
	}
	return nil
}

func validateCaseInputs(cases []Case) error {
	if len(cases) < MinimumSliceCases {
		return errors.New("window certification suite identity is invalid")
	}
	mediaSets := make(map[string]struct{}, len(cases))
	for _, item := range cases {
		if err := fillerstructurewindow.ValidateMediaSet(item.MediaSet); err != nil || len(item.MediaSet.Plan.Windows) < 2 {
			return errors.New("window certification case media set is invalid")
		}
		if _, duplicate := mediaSets[item.MediaSet.SHA256]; duplicate {
			return errors.New("window certification suite repeats a media set")
		}
		mediaSets[item.MediaSet.SHA256] = struct{}{}
		if !completeTimeline(item.Truth, item.MediaSet.Plan.Source.DurationMS) {
			return errors.New("window certification truth is incomplete")
		}
	}
	return nil
}

func caseSlices(item Case, highByteMinimumBytes int64) ([]Slice, error) {
	found := make(map[Slice]struct{}, len(requiredSlices))
	plan := item.MediaSet.Plan
	for _, seamWindow := range plan.Windows[1:] {
		seam := seamWindow.PrimaryStartMS
		for _, boundary := range truthBoundaries(item.Truth) {
			switch {
			case boundary >= seam-BoundaryToleranceMS && boundary < seam:
				found[SliceSeamPrimaryLeft] = struct{}{}
			case boundary >= seam && boundary <= seam+BoundaryToleranceMS:
				found[SliceSeamPrimaryRight] = struct{}{}
			case boundary > seam-plan.Profile.ContextOverlapMS && boundary < seam+plan.Profile.ContextOverlapMS:
				found[SliceSeamOverlap] = struct{}{}
			}
		}
		for _, segment := range item.Truth {
			if segment.StartMS < seam && segment.EndMS > seam {
				found[SliceCrossingSeam] = struct{}{}
			}
		}
	}
	for index := 0; index+1 < len(item.Truth); index++ {
		left, right := item.Truth[index], item.Truth[index+1]
		if left.Role == right.Role {
			found[SliceAdjacentSameRole] = struct{}{}
		}
		if isProgramme(left.Role) != isProgramme(right.Role) && (isFiller(left.Role) || isFiller(right.Role)) {
			found[SliceProgrammeFiller] = struct{}{}
		}
	}
	for _, media := range item.MediaSet.Windows {
		if media.Media.Bytes >= highByteMinimumBytes {
			found[SliceHighByteWindow] = struct{}{}
		}
	}
	for _, evidence := range item.MeasuredEvidence {
		if !validDigest(evidence.EvidenceSHA256) {
			return nil, errors.New("window certification measured slice evidence is invalid")
		}
		switch evidence.Slice {
		case SliceWordlessJoin:
			if evidence.EvidenceContract != WordlessEvidenceContract || evidence.TargetWindowOrdinal != -1 ||
				!slices.Contains(truthBoundaries(item.Truth), evidence.TargetBoundaryMS) {
				return nil, errors.New("window certification wordless-join evidence is invalid")
			}
		case SliceHighMotionWindow:
			if evidence.EvidenceContract != MotionEvidenceContract || evidence.TargetBoundaryMS != 0 ||
				evidence.TargetWindowOrdinal < 0 || evidence.TargetWindowOrdinal >= len(plan.Windows) {
				return nil, errors.New("window certification high-motion evidence is invalid")
			}
		default:
			return nil, errors.New("window certification measured slice evidence is invalid")
		}
		if _, duplicate := found[evidence.Slice]; duplicate {
			return nil, errors.New("window certification measured slice evidence repeats a slice")
		}
		found[evidence.Slice] = struct{}{}
	}
	result := make([]Slice, 0, len(found))
	for _, slice := range requiredSlices {
		if _, ok := found[slice]; ok {
			result = append(result, slice)
		}
	}
	return result, nil
}

func highByteMinimum(cases []Case) int64 {
	windows := make([]int64, 0, len(cases)*2)
	for _, item := range cases {
		for _, media := range item.MediaSet.Windows {
			windows = append(windows, media.Media.Bytes)
		}
	}
	if len(windows) < MinimumSliceCases {
		return 0
	}
	slices.SortFunc(windows, func(left, right int64) int {
		switch {
		case left > right:
			return -1
		case left < right:
			return 1
		default:
			return 0
		}
	})
	return windows[MinimumSliceCases-1]
}

func completeTimeline(segments []fillerstructure.Segment, durationMS int64) bool {
	if len(segments) == 0 {
		return false
	}
	next := int64(0)
	for _, segment := range segments {
		if segment.StartMS != next || segment.EndMS <= segment.StartMS || segment.EndMS > durationMS || !validRole(segment.Role) {
			return false
		}
		next = segment.EndMS
	}
	return next == durationMS
}

func validRole(role fillerstructure.Role) bool {
	return isFiller(role) || role == fillerstructure.RoleProgrammeFragment || role == fillerstructure.RoleNonFiller ||
		role == fillerstructure.RoleAmbiguous || role == fillerstructure.RoleUnusable
}

func isFiller(role fillerstructure.Role) bool {
	return slices.Contains([]fillerstructure.Role{
		fillerstructure.RoleCommercial, fillerstructure.RolePromo, fillerstructure.RoleBumper,
		fillerstructure.RolePSA, fillerstructure.RoleStationID, fillerstructure.RoleTrailer,
		fillerstructure.RoleInterstitial,
	}, role)
}

func isProgramme(role fillerstructure.Role) bool {
	return role == fillerstructure.RoleProgrammeFragment
}

func truthBoundaries(segments []fillerstructure.Segment) []int64 {
	result := make([]int64, 0, len(segments)-1)
	for _, segment := range segments[:len(segments)-1] {
		result = append(result, segment.EndMS)
	}
	return result
}

func cloneCases(cases []Case) []Case {
	result := make([]Case, len(cases))
	for index, item := range cases {
		result[index] = item
		result[index].MediaSet.Plan.Windows = slices.Clone(item.MediaSet.Plan.Windows)
		result[index].MediaSet.Windows = slices.Clone(item.MediaSet.Windows)
		result[index].Truth = slices.Clone(item.Truth)
		result[index].Slices = slices.Clone(item.Slices)
		result[index].MeasuredEvidence = slices.Clone(item.MeasuredEvidence)
	}
	return result
}

func canonicalID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 128
}

func validDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
