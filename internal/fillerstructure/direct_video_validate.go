package fillerstructure

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

func ParseDirectVideoResponse(raw string, durationMS int64) (DirectVideoResponse, DirectVideoAssessment, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var response DirectVideoResponse
	if err := decoder.Decode(&response); err != nil {
		return DirectVideoResponse{}, DirectVideoAssessment{}, fmt.Errorf("decode direct-video response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return DirectVideoResponse{}, DirectVideoAssessment{}, errors.New("decode direct-video response: trailing JSON value")
	}
	NormalizeDirectVideoResponse(&response)
	assessment, err := AssessDirectVideoResponse(response, durationMS)
	if err != nil {
		return DirectVideoResponse{}, DirectVideoAssessment{}, err
	}
	return response, assessment, nil
}

func validateDirectVideoAssessment(assessment DirectVideoAssessment, durationMS int64) error {
	if durationMS <= 0 || len(assessment.Segments) == 0 || len(assessment.Segments) > DirectVideoMaximumSegments ||
		assessment.Segments[0].StartMS != 0 || assessment.Segments[len(assessment.Segments)-1].EndMS != durationMS {
		return errors.New("direct-video segment plan must cover the complete source")
	}
	for index, segment := range assessment.Segments {
		mayBeEmpty := segment.Role == RoleAmbiguous || segment.Role == RoleUnusable
		if segment.EndMS <= segment.StartMS || index > 0 && segment.StartMS != assessment.Segments[index-1].EndMS ||
			!validRole(segment.Role) || strings.TrimSpace(segment.Reason) == "" ||
			!validDirectVideoTimes(segment.DecisiveAtMS, segment.StartMS, segment.EndMS, mayBeEmpty) {
			return fmt.Errorf("direct-video segment %d is invalid", index)
		}
	}
	unit := Unit(assessment.Unit.Kind)
	if !validUnit(unit) || !validDirectVideoReason(assessment.Unit.Reason) ||
		!validDirectVideoTimes(assessment.Unit.DecisiveAtMS, 0, durationMS, unit == UnitUnclear || unit == UnitUnusable) {
		return errors.New("derived direct-video unit is invalid")
	}
	if assessment.Role != nil {
		role := Role(assessment.Role.Kind)
		if !fillerRole(role) || !validDirectVideoReason(assessment.Role.Reason) ||
			!validDirectVideoTimes(assessment.Role.DecisiveAtMS, 0, durationMS, false) {
			return errors.New("derived direct-video role is invalid")
		}
	}
	return nil
}

func validDirectVideoReason(reason string) bool {
	return strings.TrimSpace(reason) != "" && utf8.RuneCountInString(reason) <= DirectVideoMaximumReasonRunes
}

func validDirectVideoTimes(values []int64, startMS, endMS int64, mayBeEmpty bool) bool {
	if len(values) == 0 {
		return mayBeEmpty
	}
	if len(values) > DirectVideoMaximumDecisiveTime || !slices.IsSorted(values) {
		return false
	}
	previous := int64(-1)
	for _, value := range values {
		if value < startMS || value > endMS || value == previous {
			return false
		}
		previous = value
	}
	return true
}
