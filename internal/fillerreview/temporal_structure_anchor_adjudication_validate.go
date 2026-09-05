package fillerreview

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	temporalStructureAnchorRationaleMaximumBytes   = 1_000
	temporalStructureAnchorObservationMaximumBytes = 500
)

func validateTemporalStructureAnchorAdjudicationSubmission(
	submission TemporalStructureAnchorAdjudicationSubmission,
	comparison TemporalStructureComparisonReport,
	challenge TemporalStructureChallengeAuthority,
	receipt TemporalStructureHoldoutReceipt,
	comparisonSHA string,
	adjudicatedAt time.Time,
) ([]TemporalStructureAnchorAdjudicationCase, error) {
	if submission.SchemaVersion != TemporalStructureAnchorAdjudicationSchemaVersion ||
		submission.ContractVersion != TemporalStructureAnchorAdjudicationSubmissionContract ||
		submission.ChallengeID != challenge.ChallengeID ||
		submission.ComparisonSHA256 != comparisonSHA ||
		strings.TrimSpace(submission.ReviewerID) == "" ||
		submission.ReviewerID != strings.TrimSpace(submission.ReviewerID) ||
		submission.ReviewedAt.IsZero() {
		return nil, fmt.Errorf("anchor adjudication submission identity is invalid")
	}
	if submission.ReviewedAt.Before(comparison.ComparedAt) || adjudicatedAt.Before(submission.ReviewedAt) {
		return nil, fmt.Errorf("anchor adjudication timing predates the opened comparison or review")
	}

	challengeByAlias := make(map[string]TemporalStructureChallengeAuthorityCase, len(challenge.Cases))
	for _, item := range challenge.Cases {
		challengeByAlias[item.Alias] = item
	}
	comparisonByAlias := make(map[string]TemporalStructureCaseComparison, len(comparison.CaseComparisons))
	for _, item := range comparison.CaseComparisons {
		comparisonByAlias[item.Alias] = item
	}
	targetAliases := make([]string, 0, len(comparison.DiagnosticCandidates))
	for _, candidate := range comparison.DiagnosticCandidates {
		item, exists := challengeByAlias[candidate.Alias]
		if !exists {
			return nil, fmt.Errorf("anchor adjudication comparison names unknown diagnostic %q", candidate.Alias)
		}
		if item.Unit == fillereval.UnitStandalone {
			targetAliases = append(targetAliases, candidate.Alias)
		}
	}
	sort.Strings(targetAliases)
	if len(targetAliases) == 0 {
		return nil, fmt.Errorf("anchor adjudication requires at least one standalone diagnostic target")
	}

	submittedAliases := make([]string, len(submission.Cases))
	for index, item := range submission.Cases {
		submittedAliases[index] = item.Alias
	}
	if !sort.StringsAreSorted(submittedAliases) || adjacentDuplicate(submittedAliases) || !slices.Equal(submittedAliases, targetAliases) {
		return nil, fmt.Errorf("anchor adjudication cases must be the exact canonical standalone diagnostic target set")
	}

	anchorsBySource := make(map[string]TemporalStructureHoldoutAnchor, len(receipt.SelectedAnchors))
	for _, anchor := range receipt.SelectedAnchors {
		anchorsBySource[anchor.SourceID] = anchor
	}
	result := make([]TemporalStructureAnchorAdjudicationCase, 0, len(submission.Cases))
	for _, answer := range submission.Cases {
		truth := challengeByAlias[answer.Alias]
		caseComparison, exists := comparisonByAlias[answer.Alias]
		if !exists || caseComparison.Truth.Unit != truth.Unit || caseComparison.Truth.Role != truth.Role {
			return nil, fmt.Errorf("anchor adjudication target %q has inconsistent comparison truth", answer.Alias)
		}
		item, err := validateTemporalStructureAnchorAdjudicationCase(answer, truth, caseComparison.DurationMS, anchorsBySource)
		if err != nil {
			return nil, fmt.Errorf("anchor adjudication target %q: %w", answer.Alias, err)
		}
		result = append(result, item)
	}
	return result, nil
}

func validateTemporalStructureAnchorAdjudicationCase(
	answer TemporalStructureAnchorAdjudicationSubmissionCase,
	truth TemporalStructureChallengeAuthorityCase,
	durationMS int64,
	anchorsBySource map[string]TemporalStructureHoldoutAnchor,
) (TemporalStructureAnchorAdjudicationCase, error) {
	if truth.Unit != fillereval.UnitStandalone || len(truth.Segments) != 1 || durationMS <= 0 {
		return TemporalStructureAnchorAdjudicationCase{}, fmt.Errorf("target is not one bounded standalone construction")
	}
	segment := truth.Segments[0]
	if segment.Provenance.Kind != TemporalStructureSourceBoundedItem ||
		segment.SourceStartMS != 0 || segment.OutputStartMS != 0 ||
		segment.RequestedMS != segment.SourceDurationMS || segment.RenderedMS != segment.SourceDurationMS ||
		segment.OutputEndMS != durationMS || segment.SourceDurationMS != durationMS ||
		segment.SourceRole != truth.Role || !reviewSHA256(segment.SourceSHA256) {
		return TemporalStructureAnchorAdjudicationCase{}, fmt.Errorf("target lacks exact whole-source bounded provenance")
	}
	anchor, exists := anchorsBySource[segment.SourceID]
	if !exists || strings.TrimSpace(anchor.EvidenceAlias) == "" || strings.TrimSpace(anchor.CaseID) == "" ||
		strings.TrimSpace(anchor.FamilyID) == "" || anchor.DurationMS != durationMS || anchor.Role != truth.Role {
		return TemporalStructureAnchorAdjudicationCase{}, fmt.Errorf("target does not bind one selected source anchor")
	}
	if answer.Coverage != TemporalStructureAnchorReviewComplete {
		return TemporalStructureAnchorAdjudicationCase{}, fmt.Errorf("review coverage must be complete audiovisual playback")
	}
	if err := validateTemporalStructureAnchorObservations(answer.Observations, durationMS); err != nil {
		return TemporalStructureAnchorAdjudicationCase{}, err
	}
	if answer.Rationale != strings.TrimSpace(answer.Rationale) || answer.Rationale == "" || len(answer.Rationale) > temporalStructureAnchorRationaleMaximumBytes {
		return TemporalStructureAnchorAdjudicationCase{}, fmt.Errorf("rationale must be trimmed and contain 1-%d bytes", temporalStructureAnchorRationaleMaximumBytes)
	}
	if !validTemporalStructureTimes(answer.DecisiveAtMS, durationMS, answer.Unit == fillereval.UnitUnclear) {
		return TemporalStructureAnchorAdjudicationCase{}, fmt.Errorf("decisive timestamps must be canonical and inside the complete clip")
	}

	original := TemporalStructureTruthLabel{Unit: truth.Unit, Role: truth.Role}
	adjudicated := TemporalStructureTruthLabel{Unit: answer.Unit, Role: answer.Role}
	switch answer.Disposition {
	case TemporalStructureAnchorConfirmed:
		if adjudicated != original {
			return TemporalStructureAnchorAdjudicationCase{}, fmt.Errorf("confirmed original disposition changes the original label")
		}
	case TemporalStructureAnchorStructuralDisqualification:
		if !validHumanUnit(answer.Unit) || answer.Unit == fillereval.UnitStandalone || answer.Role != "" {
			return TemporalStructureAnchorAdjudicationCase{}, fmt.Errorf("structural disqualification requires one closed non-standalone unit and no role")
		}
	case TemporalStructureAnchorRoleCorrection:
		if answer.Unit != fillereval.UnitStandalone || !validHumanRole(answer.Role) || answer.Role == truth.Role {
			return TemporalStructureAnchorAdjudicationCase{}, fmt.Errorf("role correction requires standalone and a different closed role")
		}
	default:
		return TemporalStructureAnchorAdjudicationCase{}, fmt.Errorf("disposition is not a closed value")
	}

	return TemporalStructureAnchorAdjudicationCase{
		Alias: answer.Alias, EvidenceAlias: anchor.EvidenceAlias, CaseID: anchor.CaseID,
		SourceID: segment.SourceID, SourceSHA256: segment.SourceSHA256, FamilyID: anchor.FamilyID,
		DurationMS: durationMS, Coverage: answer.Coverage, Disposition: answer.Disposition,
		Observations: cloneTemporalStructureAnchorObservations(answer.Observations),
		Original:     original, Adjudicated: adjudicated, DecisiveAtMS: slices.Clone(answer.DecisiveAtMS), Rationale: answer.Rationale,
	}, nil
}

func validateTemporalStructureAnchorObservations(value TemporalStructureAnchorObservations, durationMS int64) error {
	if !boundedTemporalStructureAnchorObservation(value.Opening) || !boundedTemporalStructureAnchorObservation(value.Closing) || value.InternalJoins == nil {
		return fmt.Errorf("opening, explicit internal-join list, and closing observations are required")
	}
	previous := int64(-1)
	for _, join := range value.InternalJoins {
		if join.AtMS <= 0 || join.AtMS >= durationMS || join.AtMS <= previous || !boundedTemporalStructureAnchorObservation(join.Observation) {
			return fmt.Errorf("internal-join observations must be unique, sorted, bounded, and described")
		}
		previous = join.AtMS
	}
	return nil
}

func boundedTemporalStructureAnchorObservation(value string) bool {
	return value == strings.TrimSpace(value) && value != "" && len(value) <= temporalStructureAnchorObservationMaximumBytes
}

func cloneTemporalStructureAnchorObservations(value TemporalStructureAnchorObservations) TemporalStructureAnchorObservations {
	value.InternalJoins = slices.Clone(value.InternalJoins)
	return value
}

func adjacentDuplicate(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return true
		}
	}
	return false
}
