package fillerstructure

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
)

func NewAssessmentRecord(input AssessmentRecordInput) (RecordedAssessment, error) {
	record := AssessmentRecord{
		SchemaVersion: AssessmentRecordSchemaVersion, ContractVersion: AssessmentRecordContractVersion,
		Source: input.Source, Media: input.Media, Assessor: input.Assessor,
		MetadataSnapshotSHA256: input.MetadataSnapshotSHA256,
		PromptSHA256:           input.PromptSHA256, SchemaSHA256: input.SchemaSHA256,
		RequestSHA256: input.RequestSHA256, ResponseSHA256: optionalAssessmentDigest(input.RawResponse),
		StructuredOutputSHA256: optionalAssessmentDigest([]byte(input.StructuredOutput)),
		ResolvedProvider:       strings.TrimSpace(input.ResolvedProvider), ResolvedModel: strings.TrimSpace(input.ResolvedModel),
		UpstreamProvider: strings.TrimSpace(input.UpstreamProvider), UpstreamProviderSlug: strings.TrimSpace(input.UpstreamProviderSlug),
		GenerationID: strings.TrimSpace(input.GenerationID), Tokens: input.Tokens,
		RequestedNanoUSD: input.RequestedNanoUSD, ReservedNanoUSD: input.ReservedNanoUSD,
		ChargedAmountUSD: strings.TrimSpace(input.ChargedAmountUSD), ChargedNanoUSD: input.ChargedNanoUSD,
		AccountedNanoUSD: input.AccountedNanoUSD, ChargeKnown: input.ChargeKnown,
		State: input.State, Failure: strings.TrimSpace(input.Failure), AssessedAt: input.AssessedAt.UTC().Round(0),
	}
	if record.State == AssessmentRecordAccepted {
		_, assessment, err := ParseDirectVideoResponse(input.StructuredOutput, input.Source.DurationMS)
		if err != nil {
			return RecordedAssessment{}, err
		}
		record.Result = assessmentResult(assessment)
	}
	record.SHA256 = AssessmentRecordSHA256(record)
	recorded := RecordedAssessment{
		Record: record, RawResponse: bytes.Clone(input.RawResponse), StructuredOutput: input.StructuredOutput,
	}
	return recorded, ValidateRecordedAssessment(recorded)
}

func ValidateRecordedAssessment(recorded RecordedAssessment) error {
	if err := ValidateAssessmentRecord(recorded.Record); err != nil {
		return err
	}
	if len(recorded.RawResponse) > AssessmentMaximumResponseBytes || len(recorded.StructuredOutput) > AssessmentMaximumResponseBytes {
		return errors.New("filler structure assessment record: response exceeds the byte ceiling")
	}
	if optionalAssessmentDigest(recorded.RawResponse) != recorded.Record.ResponseSHA256 ||
		optionalAssessmentDigest([]byte(recorded.StructuredOutput)) != recorded.Record.StructuredOutputSHA256 {
		return errors.New("filler structure assessment record: supplied response bytes do not match")
	}
	if recorded.Record.State == AssessmentRecordAccepted {
		_, assessment, err := ParseDirectVideoResponse(recorded.StructuredOutput, recorded.Record.Source.DurationMS)
		if err != nil || !reflect.DeepEqual(assessmentResult(assessment), recorded.Record.Result) {
			return errors.New("filler structure assessment record: parsed result does not reproduce")
		}
	}
	return nil
}

func (record AssessmentRecord) Candidate() (Candidate, error) {
	if err := ValidateAssessmentRecord(record); err != nil {
		return Candidate{}, err
	}
	assessor := Assessor{
		ID: record.Assessor.ID, ModelFamily: record.Assessor.ModelFamily, Provider: record.Assessor.Provider,
		Model: record.Assessor.Model, ModelDigest: record.Assessor.ModelDigest,
		CapabilitySHA256: record.Assessor.CapabilitySHA256, PromptVersion: record.Assessor.PromptVersion,
		EvidenceContract: record.Assessor.EvidenceContract, AssessmentSHA256: record.SHA256,
	}
	input, err := NewCompleteVideoInput(record.Source, record.Media)
	if err != nil {
		return Candidate{}, err
	}
	if record.State != AssessmentRecordAccepted {
		return NewCandidate(record.Source, input.SHA256, assessor, record.Failure, nil)
	}
	candidate, err := NewCandidate(record.Source, input.SHA256, assessor, "", record.Result.Segments)
	if err != nil {
		return Candidate{}, err
	}
	if candidate.Unit != record.Result.Unit || candidate.Role != record.Result.Role ||
		!slices.Equal(candidate.Segments, record.Result.Segments) {
		return Candidate{}, errors.New("filler structure assessment record claims do not reproduce from timeline")
	}
	return candidate, nil
}

func AssessmentRecordSHA256(record AssessmentRecord) string {
	record.SHA256 = ""
	raw, err := json.Marshal(record)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func assessmentResult(assessment DirectVideoAssessment) *AssessmentResult {
	result := &AssessmentResult{Unit: Unit(assessment.Unit.Kind)}
	if assessment.Role != nil {
		result.Role = Role(assessment.Role.Kind)
	}
	for _, segment := range assessment.Segments {
		result.Segments = append(result.Segments, Segment{StartMS: segment.StartMS, EndMS: segment.EndMS, Role: segment.Role})
	}
	return result
}

func optionalAssessmentDigest(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
