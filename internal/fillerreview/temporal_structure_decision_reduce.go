package fillerreview

import (
	"strings"

	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	temporalStructureDecisionReasonAgreement          = fillerstructure.ReasonAgreement
	temporalStructureDecisionReasonOperationalFailure = fillerstructure.ReasonOperationalFailure
	temporalStructureDecisionReasonUnitDisagreement   = fillerstructure.ReasonUnitDisagreement
	temporalStructureDecisionReasonUnsupportedUnit    = fillerstructure.ReasonUnsupportedUnit
	temporalStructureDecisionReasonRoleDisagreement   = fillerstructure.ReasonRoleDisagreement
	temporalStructureDecisionReasonIntervalCount      = fillerstructure.ReasonIntervalCount
	temporalStructureDecisionReasonIntervalRole       = fillerstructure.ReasonIntervalRole
	temporalStructureDecisionReasonBoundary           = fillerstructure.ReasonBoundary
	temporalStructureDecisionReasonUnresolvedInterval = fillerstructure.ReasonUnresolvedInterval
)

type temporalStructureDecisionCandidate struct {
	assessor         fillereval.TemporalAssessorIdentity
	capabilitySHA    string
	evidenceContract string
	assessmentSHA    string
	assessment       TemporalStructureAssessment
}

func reduceTemporalStructureDecision(alias, sourceSHA string, sourceBytes, durationMS int64, profileSHA, lineageSHA string, candidates []temporalStructureDecisionCandidate) TemporalStructureCaseDecision {
	source := fillerstructure.Source{SHA256: sourceSHA, Bytes: sourceBytes, DurationMS: durationMS}
	media := fillerstructure.AssessmentMedia{SHA256: sourceSHA, Bytes: sourceBytes, DurationMS: durationMS, ProfileSHA256: profileSHA, LineageSHA256: lineageSHA}
	input, _ := fillerstructure.NewCompleteVideoInput(source, media)
	request := fillerstructure.Request{Source: source, Input: input, BoundaryToleranceMS: TemporalStructureNearBoundaryMS}
	for _, candidate := range candidates {
		request.Candidates = append(request.Candidates, temporalStructureCoreCandidate(source, input.SHA256, candidate))
	}
	reduced := fillerstructure.Reduce(request)
	decision := TemporalStructureCaseDecision{
		Alias: alias, DurationMS: durationMS, Status: string(reduced.Status),
		ReasonCodes: reduced.ReasonCodes, Unit: fillereval.UnitKind(reduced.Unit), Role: fillereval.TemporalRole(reduced.Role),
	}
	for _, candidate := range reduced.Candidates {
		decision.Candidates = append(decision.Candidates, TemporalStructureDecisionCandidateObservation{
			AssessorID: candidate.Assessor.ID, ModelFamily: candidate.Assessor.ModelFamily,
			Failure: fillereval.TemporalFailureCode(candidate.Failure), Unit: fillereval.UnitKind(candidate.Unit), Role: fillereval.TemporalRole(candidate.Role),
			Segments: temporalStructureDecisionObservedSegments(candidate.Segments),
		})
	}
	for _, segment := range reduced.Segments {
		decision.Segments = append(decision.Segments, TemporalStructureDecisionSegment{
			StartMS: segment.StartMS, EndMS: segment.EndMS, Disposition: string(segment.Disposition),
			Role: fillereval.TemporalSegmentRole(segment.Role),
		})
	}
	return decision
}

func temporalStructureCoreCandidate(source fillerstructure.Source, inputSHA256 string, candidate temporalStructureDecisionCandidate) fillerstructure.Candidate {
	identity := candidate.assessor
	result := fillerstructure.Candidate{
		Source: source, InputSHA256: inputSHA256,
		Assessor: fillerstructure.Assessor{
			ID: identity.ID, ModelFamily: strings.ToLower(strings.TrimSpace(identity.ModelFamily)),
			Provider: identity.Provider, Model: identity.Model, ModelDigest: identity.ModelDigest,
			CapabilitySHA256: candidate.capabilitySHA, PromptVersion: identity.PromptVersion,
			EvidenceContract: candidate.evidenceContract, AssessmentSHA256: candidate.assessmentSHA,
		},
	}
	if candidate.assessment.OperationalFailure != nil {
		result.Failure = string(candidate.assessment.OperationalFailure.Code)
		return result
	}
	result.Unit = fillerstructure.Unit(candidate.assessment.Unit.Kind)
	if candidate.assessment.Role != nil {
		result.Role = fillerstructure.Role(candidate.assessment.Role.Kind)
	}
	for _, segment := range candidate.assessment.Segments {
		result.Segments = append(result.Segments, fillerstructure.Segment{
			StartMS: segment.StartMS, EndMS: segment.EndMS, Role: fillerstructure.Role(segment.Role),
		})
	}
	return result
}

func temporalStructureDecisionObservedSegments(segments []fillerstructure.Segment) []TemporalStructurePredictedSegment {
	result := make([]TemporalStructurePredictedSegment, 0, len(segments))
	for _, segment := range segments {
		result = append(result, TemporalStructurePredictedSegment{
			StartMS: segment.StartMS, EndMS: segment.EndMS, Role: fillereval.TemporalSegmentRole(segment.Role),
		})
	}
	return result
}

func temporalStructureDecisionDisposition(role fillereval.TemporalSegmentRole) string {
	return string(fillerstructure.DispositionForRole(fillerstructure.Role(role)))
}
