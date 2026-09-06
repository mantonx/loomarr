package fillerstructurewindow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func NewRecordedAssessment(input CallRecordInput) (RecordedAssessment, error) {
	failure := strings.TrimSpace(input.Failure)
	assessmentInput := AssessmentInput{
		MediaSet: input.MediaSet, WindowOrdinal: input.WindowOrdinal, Assessor: input.Assessor,
		Failure: failure, AssessedAt: input.AssessedAt,
	}
	if input.State == fillerstructure.AssessmentRecordAccepted {
		segments, err := ParseDirectVideoResponse(input.MediaSet, input.WindowOrdinal, input.StructuredOutput)
		if err != nil {
			return RecordedAssessment{}, err
		}
		assessmentInput.Segments = segments
	}
	assessment, err := NewAssessment(assessmentInput)
	if err != nil {
		return RecordedAssessment{}, err
	}
	record := CallRecord{
		SchemaVersion: CallRecordSchemaVersion, ContractVersion: CallRecordContractVersion,
		MediaSet: input.MediaSet, WindowOrdinal: input.WindowOrdinal, Assessor: input.Assessor,
		MetadataSnapshotSHA256: input.MetadataSnapshotSHA256,
		PromptSHA256:           input.PromptSHA256, SchemaSHA256: input.SchemaSHA256,
		RequestSHA256: input.RequestSHA256, ResponseSHA256: optionalCallDigest(input.RawResponse),
		StructuredOutputSHA256: optionalCallDigest([]byte(input.StructuredOutput)), AssessmentSHA256: assessment.SHA256,
		ResolvedProvider: strings.TrimSpace(input.ResolvedProvider), ResolvedModel: strings.TrimSpace(input.ResolvedModel),
		UpstreamProvider: strings.TrimSpace(input.UpstreamProvider), UpstreamProviderSlug: strings.TrimSpace(input.UpstreamProviderSlug),
		GenerationID: strings.TrimSpace(input.GenerationID), Tokens: input.Tokens,
		RequestedNanoUSD: input.RequestedNanoUSD, ReservedNanoUSD: input.ReservedNanoUSD,
		ChargedAmountUSD: strings.TrimSpace(input.ChargedAmountUSD), ChargedNanoUSD: input.ChargedNanoUSD,
		AccountedNanoUSD: input.AccountedNanoUSD, ChargeKnown: input.ChargeKnown,
		State: input.State, Failure: failure, AssessedAt: input.AssessedAt.UTC().Round(0),
	}
	record.SHA256 = CallRecordSHA256(record)
	recorded := RecordedAssessment{
		Record: record, Assessment: assessment, RawResponse: bytes.Clone(input.RawResponse),
		StructuredOutput: input.StructuredOutput,
	}
	return recorded, ValidateRecordedAssessment(recorded)
}

func CallRecordSHA256(record CallRecord) string {
	record.SHA256 = ""
	raw, err := json.Marshal(record)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func optionalCallDigest(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
