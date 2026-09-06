package fillerstructurewindowopenrouter

import (
	"errors"
	"fmt"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/openroutermedia"
)

func (a *Assessor) recordAssessment(set fillerstructurewindow.MediaSet, ordinal int, assessedAt time.Time, reservation fillerstructurewindow.CallReservationState, result openroutermedia.Result, callErr error) (fillerstructurewindow.RecordedAssessment, error) {
	window := set.Plan.Windows[ordinal]
	durationMS := window.MediaEndMS - window.MediaStartMS
	input := fillerstructurewindow.CallRecordInput{
		MediaSet: set, WindowOrdinal: ordinal, Assessor: a.config.Profile,
		MetadataSnapshotSHA256: a.config.MetadataSnapshotSHA256,
		PromptSHA256:           fillerstructurewindow.DirectVideoPromptSHA256(durationMS),
		SchemaSHA256:           fillerstructurewindow.DirectVideoSchemaSHA256(durationMS),
		RequestSHA256:          result.RequestSHA256, RawResponse: result.RawResponse, StructuredOutput: result.StructuredOutput,
		UpstreamProvider: a.config.UpstreamProvider, UpstreamProviderSlug: a.config.UpstreamProviderSlug,
		GenerationID:     result.GenerationID,
		Tokens:           fillerstructure.AssessmentTokenUsage{Prompt: result.PromptTokens, Completion: result.CompletionTokens},
		RequestedNanoUSD: a.config.ReservationNanoUSD, AssessedAt: assessedAt,
	}
	if reservation == fillerstructurewindow.CallReservationHeldBudget {
		if !errors.Is(callErr, errReservationHeld) || len(result.RawResponse) != 0 {
			return fillerstructurewindow.RecordedAssessment{}, errors.New("filler structure window OpenRouter budget hold is inconsistent")
		}
		input.State, input.Failure = fillerstructure.AssessmentRecordHeldBudget, fillerstructure.AssessmentFailureBudget
		return a.newAssessmentRecord(input)
	}
	if reservation != fillerstructurewindow.CallReservationAccepted {
		return fillerstructurewindow.RecordedAssessment{}, errors.New("filler structure window OpenRouter reservation was not accepted")
	}
	input.ReservedNanoUSD = a.config.ReservationNanoUSD
	input.ChargeKnown, input.ChargedAmountUSD, input.ChargedNanoUSD = result.ChargeKnown, result.ChargedAmountUSD, result.ChargedNanoUSD
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
	case !validStructuredOutput(set, ordinal, result.StructuredOutput):
		input.State, input.Failure = fillerstructure.AssessmentRecordFailed, fillerstructure.AssessmentFailureInvalidResponse
		input.ResolvedProvider, input.ResolvedModel = a.config.Profile.Provider, a.config.ResolvedModel
	default:
		input.State = fillerstructure.AssessmentRecordAccepted
		input.ResolvedProvider, input.ResolvedModel = a.config.Profile.Provider, a.config.ResolvedModel
	}
	return a.newAssessmentRecord(input)
}

func (a *Assessor) newAssessmentRecord(input fillerstructurewindow.CallRecordInput) (fillerstructurewindow.RecordedAssessment, error) {
	recorded, err := fillerstructurewindow.NewRecordedAssessment(input)
	if err != nil {
		return fillerstructurewindow.RecordedAssessment{}, fmt.Errorf("build filler structure window OpenRouter assessment record: %w", err)
	}
	return recorded, nil
}

func validStructuredOutput(set fillerstructurewindow.MediaSet, ordinal int, output string) bool {
	_, err := fillerstructurewindow.ParseDirectVideoResponse(set, ordinal, output)
	return err == nil
}
