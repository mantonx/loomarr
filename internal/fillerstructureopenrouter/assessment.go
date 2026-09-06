package fillerstructureopenrouter

import (
	"errors"
	"fmt"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/openroutermedia"
)

func (a *Assessor) recordAssessment(media filler.StructureAssessmentMedia, assessedAt time.Time, reservation fillerstructure.AssessmentReservationState, result openroutermedia.Result, callErr error) (fillerstructure.RecordedAssessment, error) {
	input := fillerstructure.AssessmentRecordInput{
		Source: fillerstructure.Source{SHA256: media.Source.SHA256, Bytes: media.Source.Bytes, DurationMS: media.Source.DurationMs},
		Media:  media.Assessment, Assessor: a.config.Profile,
		MetadataSnapshotSHA256: a.config.MetadataSnapshotSHA256,
		PromptSHA256:           fillerstructure.DirectVideoPromptSHA256(media.Source.DurationMs),
		SchemaSHA256:           fillerstructure.DirectVideoSchemaSHA256(media.Source.DurationMs),
		RequestSHA256:          result.RequestSHA256, RawResponse: result.RawResponse,
		StructuredOutput: result.StructuredOutput,
		UpstreamProvider: a.config.UpstreamProvider, UpstreamProviderSlug: a.config.UpstreamProviderSlug,
		GenerationID: result.GenerationID,
		Tokens: fillerstructure.AssessmentTokenUsage{
			Prompt: result.PromptTokens, Completion: result.CompletionTokens,
		},
		RequestedNanoUSD: a.config.ReservationNanoUSD,
		AssessedAt:       assessedAt,
	}
	if reservation == fillerstructure.AssessmentReservationHeldBudget {
		if !errors.Is(callErr, errReservationHeld) || len(result.RawResponse) != 0 {
			return fillerstructure.RecordedAssessment{}, fmt.Errorf("filler structure OpenRouter budget hold is inconsistent")
		}
		input.State, input.Failure = fillerstructure.AssessmentRecordHeldBudget, fillerstructure.AssessmentFailureBudget
		return a.newAssessmentRecord(input)
	}
	if reservation != fillerstructure.AssessmentReservationAccepted {
		return fillerstructure.RecordedAssessment{}, fmt.Errorf("filler structure OpenRouter reservation was not accepted")
	}
	input.ReservedNanoUSD = a.config.ReservationNanoUSD
	input.ChargeKnown = result.ChargeKnown
	input.ChargedAmountUSD, input.ChargedNanoUSD = result.ChargedAmountUSD, result.ChargedNanoUSD
	if result.ChargeKnown {
		input.AccountedNanoUSD = result.ChargedNanoUSD
	} else {
		input.AccountedNanoUSD = a.config.ReservationNanoUSD
	}

	switch {
	case result.OverReservationNanoUSD > 0 || errors.Is(callErr, openroutermedia.ErrChargeExceedsReservation):
		input.State, input.Failure = fillerstructure.AssessmentRecordOverReservation, fillerstructure.AssessmentFailureOverReservation
	case callErr != nil && !result.ChargeKnown:
		input.State = fillerstructure.AssessmentRecordUnsettled
		if len(result.RawResponse) == 0 {
			input.Failure = fillerstructure.AssessmentFailureTransport
		} else {
			input.Failure = fillerstructure.AssessmentFailureUnsettled
		}
	case errors.Is(callErr, openroutermedia.ErrRouteMismatch):
		input.State, input.Failure = fillerstructure.AssessmentRecordFailed, fillerstructure.AssessmentFailureRouteMismatch
	case callErr != nil:
		input.State, input.Failure = fillerstructure.AssessmentRecordFailed, fillerstructure.AssessmentFailureProvider
	case !validStructuredOutput(result.StructuredOutput, media.Source.DurationMs):
		input.State, input.Failure = fillerstructure.AssessmentRecordFailed, fillerstructure.AssessmentFailureInvalidResponse
		input.ResolvedProvider, input.ResolvedModel = a.config.Profile.Provider, a.config.ResolvedModel
	default:
		input.State = fillerstructure.AssessmentRecordAccepted
		input.ResolvedProvider, input.ResolvedModel = a.config.Profile.Provider, a.config.ResolvedModel
	}
	return a.newAssessmentRecord(input)
}

func (a *Assessor) newAssessmentRecord(input fillerstructure.AssessmentRecordInput) (fillerstructure.RecordedAssessment, error) {
	recorded, err := fillerstructure.NewAssessmentRecord(input)
	if err != nil {
		return fillerstructure.RecordedAssessment{}, fmt.Errorf("build filler structure OpenRouter assessment record: %w", err)
	}
	return recorded, nil
}

func validStructuredOutput(output string, durationMS int64) bool {
	_, _, err := fillerstructure.ParseDirectVideoResponse(output, durationMS)
	return err == nil
}
