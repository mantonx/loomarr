package fillerreview

import (
	"fmt"
	"sort"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	temporalStructureBoundaryConstructedJoin = "constructed_join"
	temporalStructureBoundaryParentCutEdge   = "parent_cut_edge"
)

type temporalStructureTruthBoundary struct {
	kind string
	atMS int64
}

type temporalStructureSummaryKey struct {
	assessor string
	unit     fillereval.UnitKind
}

type temporalStructureSliceSummaryKey struct {
	assessor string
	slice    string
}

func buildTemporalStructureComparison(loaded temporalStructureComparisonLoaded, comparedAt time.Time) TemporalStructureComparisonReport {
	report := TemporalStructureComparisonReport{
		SchemaVersion: TemporalStructureComparisonSchemaVersion, ContractVersion: TemporalStructureComparisonContractVersion,
		ChallengeID: loaded.manifest.ChallengeID, PublicManifestSHA256: loaded.publicSHA, PrivateAuthoritySHA256: loaded.authoritySHA,
		ComparedAt: comparedAt, BoundaryTolerancesMS: []int64{TemporalStructureNearBoundaryMS, TemporalStructureBroadBoundaryMS},
		Cases: len(loaded.authority.Cases), ProductionAdmissionAllowed: false,
	}
	summaryByAssessor := make(map[string]*TemporalStructureAssessorSummary, len(loaded.assessments))
	constructionByKey := make(map[temporalStructureSummaryKey]*TemporalStructureConstructionSummary)
	sliceByKey := make(map[temporalStructureSliceSummaryKey]*TemporalStructureConstructionSummary)
	boundaryDistances := make(map[string][]int64)
	constructionDistances := make(map[temporalStructureSummaryKey][]int64)
	sliceDistances := make(map[temporalStructureSliceSummaryKey][]int64)
	for _, loadedAssessment := range loaded.assessments {
		set := loadedAssessment.set
		report.Assessors = append(report.Assessors, TemporalStructureAssessorReference{
			AssessmentSetSHA256: loadedAssessment.fileSHA, RawResultSHA256: set.RawResultSHA256,
			SnapshotFileSHA256: set.SnapshotFileSHA256,
			CapabilitySHA256:   set.CapabilitySnapshotSHA256, CompletedAt: set.CompletedAt, Assessor: set.Assessor,
		})
		summaryByAssessor[set.Assessor.ID] = &TemporalStructureAssessorSummary{AssessorID: set.Assessor.ID, Cases: len(loaded.authority.Cases)}
		for _, unit := range temporalStructureScoredUnits() {
			key := temporalStructureSummaryKey{assessor: set.Assessor.ID, unit: unit}
			constructionByKey[key] = &TemporalStructureConstructionSummary{AssessorID: set.Assessor.ID, TruthUnit: unit}
		}
	}

	authorityCases := append([]TemporalStructureChallengeAuthorityCase(nil), loaded.authority.Cases...)
	sort.Slice(authorityCases, func(i, j int) bool { return authorityCases[i].Alias < authorityCases[j].Alias })
	for _, truthCase := range authorityCases {
		publicCase := temporalStructurePublicCase(loaded.manifest, truthCase.Alias)
		boundaries := temporalStructureTruthBoundaries(truthCase, publicCase.Video.DurationMS)
		truthSegments := temporalStructureTruthSegments(truthCase, publicCase.Video.DurationMS)
		comparison := TemporalStructureCaseComparison{
			Alias: truthCase.Alias, DurationMS: publicCase.Video.DurationMS,
			Truth: TemporalStructureTruthLabel{Unit: truthCase.Unit, Role: truthCase.Role, Slices: append([]string(nil), truthCase.Slices...)}, TruthSegments: truthSegments,
		}
		allExact := true
		for _, loadedAssessment := range loaded.assessments {
			assessment := loadedAssessment.byAlias[truthCase.Alias]
			result := scoreTemporalStructureCase(loadedAssessment.set.Assessor.ID, truthCase, boundaries, truthSegments, assessment)
			comparison.Assessments = append(comparison.Assessments, result)
			allExact = allExact && result.ExactLabelCorrect
			assessorSummary := summaryByAssessor[result.AssessorID]
			constructionSummary := constructionByKey[temporalStructureSummaryKey{assessor: result.AssessorID, unit: truthCase.Unit}]
			accumulateTemporalStructureResult(assessorSummary, constructionSummary, result, assessment, len(boundaries))
			for _, slice := range truthCase.Slices {
				key := temporalStructureSliceSummaryKey{assessor: result.AssessorID, slice: slice}
				summary := sliceByKey[key]
				if summary == nil {
					summary = &TemporalStructureConstructionSummary{AssessorID: result.AssessorID, Slice: slice}
					sliceByKey[key] = summary
				}
				accumulateTemporalStructureConstruction(summary, result, len(boundaries))
			}
			for _, distance := range result.BoundaryDistances {
				boundaryDistances[result.AssessorID] = append(boundaryDistances[result.AssessorID], distance.DistanceMS)
				key := temporalStructureSummaryKey{assessor: result.AssessorID, unit: truthCase.Unit}
				constructionDistances[key] = append(constructionDistances[key], distance.DistanceMS)
				for _, slice := range truthCase.Slices {
					sliceKey := temporalStructureSliceSummaryKey{assessor: result.AssessorID, slice: slice}
					sliceDistances[sliceKey] = append(sliceDistances[sliceKey], distance.DistanceMS)
				}
			}
		}
		if allExact {
			report.AllAssessorsExactCorrect++
		}
		if reasons := temporalStructureDiagnosticReasons(comparison); len(reasons) > 0 {
			report.DiagnosticCandidates = append(report.DiagnosticCandidates, TemporalStructureDiagnosticCandidate{Alias: comparison.Alias, Reasons: reasons})
		}
		report.CaseComparisons = append(report.CaseComparisons, comparison)
	}

	for _, loadedAssessment := range loaded.assessments {
		id := loadedAssessment.set.Assessor.ID
		summary := summaryByAssessor[id]
		summary.Boundary.MedianDistanceMS = temporalStructureMedian(boundaryDistances[id])
		report.AssessorSummaries = append(report.AssessorSummaries, *summary)
		for _, unit := range temporalStructureScoredUnits() {
			key := temporalStructureSummaryKey{assessor: id, unit: unit}
			constructionByKey[key].Boundary.MedianDistanceMS = temporalStructureMedian(constructionDistances[key])
			report.ConstructionSummaries = append(report.ConstructionSummaries, *constructionByKey[key])
		}
	}
	sliceKeys := make([]temporalStructureSliceSummaryKey, 0, len(sliceByKey))
	for key := range sliceByKey {
		sliceKeys = append(sliceKeys, key)
	}
	sort.Slice(sliceKeys, func(i, j int) bool {
		if sliceKeys[i].assessor != sliceKeys[j].assessor {
			return sliceKeys[i].assessor < sliceKeys[j].assessor
		}
		return sliceKeys[i].slice < sliceKeys[j].slice
	})
	for _, key := range sliceKeys {
		sliceByKey[key].Boundary.MedianDistanceMS = temporalStructureMedian(sliceDistances[key])
		report.SliceSummaries = append(report.SliceSummaries, *sliceByKey[key])
	}
	report.PairSummaries = buildTemporalStructurePairSummaries(report.CaseComparisons, loaded.assessments)
	report.Disposition = temporalStructureComparisonDisposition(report.DiagnosticCandidates)
	return report
}

func scoreTemporalStructureCase(assessorID string, truth TemporalStructureChallengeAuthorityCase, boundaries []temporalStructureTruthBoundary, truthSegments []TemporalStructureTruthSegment, assessment TemporalStructureAssessment) TemporalStructureAssessorCaseResult {
	result := TemporalStructureAssessorCaseResult{AssessorID: assessorID}
	if assessment.OperationalFailure != nil {
		result.Prediction.Failure = assessment.OperationalFailure.Code
		return result
	}
	result.Prediction.Unit = assessment.Unit.Kind
	if assessment.Role != nil {
		result.Prediction.Role = assessment.Role.Kind
	}
	for _, segment := range assessment.Segments {
		result.Prediction.Segments = append(result.Prediction.Segments, TemporalStructurePredictedSegment{StartMS: segment.StartMS, EndMS: segment.EndMS, Role: segment.Role})
	}
	result.UnitCorrect = result.Prediction.Unit == truth.Unit
	result.StandaloneClassCorrect = (result.Prediction.Unit == fillereval.UnitStandalone) == (truth.Unit == fillereval.UnitStandalone)
	result.RoleComparable = truth.Unit == fillereval.UnitStandalone && result.Prediction.Unit == fillereval.UnitStandalone
	result.RoleCorrect = result.RoleComparable && result.Prediction.Role == truth.Role
	result.ExactLabelCorrect = result.UnitCorrect && (truth.Unit != fillereval.UnitStandalone || result.RoleCorrect)
	scoreTemporalStructureSegments(&result, truthSegments)
	if result.UnitCorrect && len(boundaries) > 0 {
		for _, boundary := range boundaries {
			nearest, distance := nearestTemporalStructureTime(boundary.atMS, assessment.Unit.DecisiveAtMS)
			result.BoundaryDistances = append(result.BoundaryDistances, TemporalStructureBoundaryDistance{
				Kind: boundary.kind, TruthAtMS: boundary.atMS, NearestDecisiveMS: nearest, DistanceMS: distance,
				Within2000MS: distance <= TemporalStructureNearBoundaryMS, Within5000MS: distance <= TemporalStructureBroadBoundaryMS,
			})
		}
	}
	return result
}

func accumulateTemporalStructureResult(summary *TemporalStructureAssessorSummary, construction *TemporalStructureConstructionSummary, result TemporalStructureAssessorCaseResult, assessment TemporalStructureAssessment, truthBoundaries int) {
	summary.Boundary.TruthTargets += truthBoundaries
	if result.Prediction.Failure != "" {
		summary.OperationalFailures++
		accumulateTemporalStructureConstruction(construction, result, truthBoundaries)
		return
	}
	summary.UnitComparable++
	if assessment.Unit.Kind == fillereval.UnitUnclear {
		summary.SemanticAbstentions++
	}
	if assessment.Unit.Kind == fillereval.UnitUnusable {
		summary.UnusableClaims++
	}
	if result.UnitCorrect {
		summary.ExactUnitCorrect++
	}
	if result.StandaloneClassCorrect {
		summary.StandaloneClassCorrect++
	}
	if result.RoleComparable {
		summary.RoleComparable++
	}
	if result.RoleCorrect {
		summary.RoleCorrect++
	}
	if result.ExactLabelCorrect {
		summary.ExactLabelCorrect++
	}
	if result.CoverageComplete {
		summary.CoverageComplete++
	}
	summary.UnderSplits += result.UnderSplits
	summary.OverSplits += result.OverSplits
	summary.SegmentRoleTargets += result.SegmentRoleTargets
	summary.SegmentRoleCorrect += result.SegmentRoleCorrect
	if result.ExactSegmentPlan {
		summary.ExactSegmentPlans++
	}
	for _, distance := range result.BoundaryDistances {
		summary.Boundary.ComparableTargets++
		construction.Boundary.ComparableTargets++
		if distance.Within2000MS {
			summary.Boundary.Within2000MS++
		}
		if distance.Within5000MS {
			summary.Boundary.Within5000MS++
		}
	}
	accumulateTemporalStructureConstruction(construction, result, truthBoundaries)
}

func accumulateTemporalStructureConstruction(summary *TemporalStructureConstructionSummary, result TemporalStructureAssessorCaseResult, truthBoundaries int) {
	summary.Cases++
	summary.Boundary.TruthTargets += truthBoundaries
	if result.Prediction.Failure != "" {
		summary.OperationalFailures++
		return
	}
	if result.UnitCorrect {
		summary.ExactUnitCorrect++
	}
	if result.StandaloneClassCorrect {
		summary.StandaloneClassCorrect++
	}
	if result.RoleComparable {
		summary.RoleComparable++
	}
	if result.RoleCorrect {
		summary.RoleCorrect++
	}
	if result.ExactLabelCorrect {
		summary.ExactLabelCorrect++
	}
	if result.CoverageComplete {
		summary.CoverageComplete++
	}
	summary.UnderSplits += result.UnderSplits
	summary.OverSplits += result.OverSplits
	summary.SegmentRoleTargets += result.SegmentRoleTargets
	summary.SegmentRoleCorrect += result.SegmentRoleCorrect
	if result.ExactSegmentPlan {
		summary.ExactSegmentPlans++
	}
	for _, distance := range result.BoundaryDistances {
		summary.Boundary.ComparableTargets++
		if distance.Within2000MS {
			summary.Boundary.Within2000MS++
		}
		if distance.Within5000MS {
			summary.Boundary.Within5000MS++
		}
	}
}

func temporalStructureTruthSegments(item TemporalStructureChallengeAuthorityCase, durationMS int64) []TemporalStructureTruthSegment {
	switch item.Unit {
	case fillereval.UnitStandalone, fillereval.UnitCompilation, fillereval.UnitProgrammeSpots:
		segments := make([]TemporalStructureTruthSegment, 0, len(item.Segments))
		for index, part := range item.Segments {
			endMS := part.OutputEndMS
			if index == len(item.Segments)-1 {
				// Container probing may differ from the requested render duration by up to
				// the challenge's validated one-second tolerance. The public media duration
				// is the timeline the assessor actually receives and must cover.
				endMS = durationMS
			}
			role := fillereval.TemporalSegmentRole(part.SourceRole)
			if part.Provenance.Kind == TemporalStructureSourceProgrammeParent {
				role = fillereval.TemporalSegmentProgrammeFragment
			}
			segments = append(segments, TemporalStructureTruthSegment{
				StartMS: part.OutputStartMS, EndMS: endMS,
				Role: role,
			})
		}
		return segments
	case fillereval.UnitProgrammeExcerpt:
		return []TemporalStructureTruthSegment{{StartMS: 0, EndMS: durationMS, Role: fillereval.TemporalSegmentProgrammeFragment}}
	default:
		return []TemporalStructureTruthSegment{{StartMS: 0, EndMS: durationMS, Role: fillereval.TemporalSegmentAmbiguous}}
	}
}

func scoreTemporalStructureSegments(result *TemporalStructureAssessorCaseResult, truth []TemporalStructureTruthSegment) {
	if len(truth) == 0 {
		return
	}
	predicted := result.Prediction.Segments
	result.CoverageComplete = completeTemporalStructureCoverage(predicted, truth[len(truth)-1].EndMS)
	truthCuts := temporalStructureSegmentCutsTruth(truth)
	predictedCuts := temporalStructureSegmentCutsPredicted(predicted)
	matched := matchTemporalStructureCuts(truthCuts, predictedCuts, TemporalStructureNearBoundaryMS)
	result.UnderSplits = len(truthCuts) - matched
	result.OverSplits = len(predictedCuts) - matched
	result.SegmentRoleTargets = len(truth)
	used := make([]bool, len(predicted))
	for _, target := range truth {
		best, bestDistance := -1, int64(^uint64(0)>>1)
		for index, candidate := range predicted {
			if used[index] || candidate.Role != target.Role {
				continue
			}
			startDistance := absoluteInt64(candidate.StartMS - target.StartMS)
			endDistance := absoluteInt64(candidate.EndMS - target.EndMS)
			if startDistance > TemporalStructureNearBoundaryMS || endDistance > TemporalStructureNearBoundaryMS {
				continue
			}
			if distance := startDistance + endDistance; distance < bestDistance {
				best, bestDistance = index, distance
			}
		}
		if best >= 0 {
			used[best] = true
			result.SegmentRoleCorrect++
		}
	}
	result.ExactSegmentPlan = result.CoverageComplete && result.UnderSplits == 0 && result.OverSplits == 0 && result.SegmentRoleCorrect == result.SegmentRoleTargets && len(predicted) == len(truth)
}

func completeTemporalStructureCoverage(segments []TemporalStructurePredictedSegment, durationMS int64) bool {
	if len(segments) == 0 || segments[0].StartMS != 0 || segments[len(segments)-1].EndMS != durationMS {
		return false
	}
	for index, segment := range segments {
		if segment.EndMS <= segment.StartMS || index > 0 && segment.StartMS != segments[index-1].EndMS {
			return false
		}
	}
	return true
}

func temporalStructureSegmentCutsTruth(segments []TemporalStructureTruthSegment) []int64 {
	cuts := make([]int64, 0, len(segments)-1)
	for index := 1; index < len(segments); index++ {
		cuts = append(cuts, segments[index].StartMS)
	}
	return cuts
}

func temporalStructureSegmentCutsPredicted(segments []TemporalStructurePredictedSegment) []int64 {
	cuts := make([]int64, 0, len(segments)-1)
	for index := 1; index < len(segments); index++ {
		cuts = append(cuts, segments[index].StartMS)
	}
	return cuts
}

func matchTemporalStructureCuts(truth, predicted []int64, toleranceMS int64) int {
	truth = append([]int64(nil), truth...)
	predicted = append([]int64(nil), predicted...)
	sort.Slice(truth, func(i, j int) bool { return truth[i] < truth[j] })
	sort.Slice(predicted, func(i, j int) bool { return predicted[i] < predicted[j] })
	truthIndex, predictedIndex := 0, 0
	matched := 0
	for truthIndex < len(truth) && predictedIndex < len(predicted) {
		switch {
		case predicted[predictedIndex] < truth[truthIndex]-toleranceMS:
			predictedIndex++
		case predicted[predictedIndex] > truth[truthIndex]+toleranceMS:
			truthIndex++
		default:
			matched++
			truthIndex++
			predictedIndex++
		}
	}
	return matched
}

func temporalStructureTruthBoundaries(item TemporalStructureChallengeAuthorityCase, durationMS int64) []temporalStructureTruthBoundary {
	switch item.Unit {
	case fillereval.UnitCompilation, fillereval.UnitProgrammeSpots:
		result := make([]temporalStructureTruthBoundary, 0, len(item.JoinTimesMS))
		for _, atMS := range item.JoinTimesMS {
			result = append(result, temporalStructureTruthBoundary{kind: temporalStructureBoundaryConstructedJoin, atMS: atMS})
		}
		return result
	case fillereval.UnitProgrammeExcerpt:
		return []temporalStructureTruthBoundary{{kind: temporalStructureBoundaryParentCutEdge, atMS: 0}, {kind: temporalStructureBoundaryParentCutEdge, atMS: durationMS}}
	default:
		return nil
	}
}

func temporalStructureScoredUnits() []fillereval.UnitKind {
	return []fillereval.UnitKind{fillereval.UnitStandalone, fillereval.UnitCompilation, fillereval.UnitProgrammeExcerpt, fillereval.UnitProgrammeSpots}
}

func nearestTemporalStructureTime(target int64, candidates []int64) (int64, int64) {
	nearest := candidates[0]
	distance := absoluteInt64(nearest - target)
	for _, candidate := range candidates[1:] {
		candidateDistance := absoluteInt64(candidate - target)
		if candidateDistance < distance || candidateDistance == distance && candidate < nearest {
			nearest, distance = candidate, candidateDistance
		}
	}
	return nearest, distance
}

func temporalStructureMedian(values []int64) *int64 {
	if len(values) == 0 {
		return nil
	}
	ordered := append([]int64(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	median := ordered[len(ordered)/2]
	if len(ordered)%2 == 0 {
		median = (ordered[len(ordered)/2-1] + median) / 2
	}
	return &median
}

func temporalStructurePublicCase(manifest TemporalStructureChallengeManifest, alias string) TemporalStructureChallengePublicCase {
	for _, item := range manifest.Cases {
		if item.Alias == alias {
			return item
		}
	}
	panic(fmt.Sprintf("validated temporal structure alias %q disappeared", alias))
}
