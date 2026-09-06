package fillersafetyreview

import (
	"fmt"

	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func publishCompletedReview(
	outputPath string,
	loaded loadedInputs,
	state checkpoint,
	ffmpeg fillersafety.ToolIdentity,
) (Result, error) {
	if len(state.Accepted) != len(loaded.worklist.Cases) || state.CompletedAt.IsZero() {
		return Result{}, fmt.Errorf("model review cannot publish an incomplete checkpoint")
	}
	evidence := fillersafetycert.ModelReviewEvidence{
		SchemaVersion:   fillersafetycert.ModelReviewEvidenceSchemaVersion,
		ContractVersion: fillersafetycert.ModelReviewEvidenceContractVersion,
		PlanSHA256:      loaded.planSHA256, WorklistSHA256: loaded.worklistSHA256,
		PolicySHA256: loaded.policySHA256, SnapshotSHA256: loaded.snapshotSHA256,
		RequestedModel: loaded.plan.Model, ResolvedModel: loaded.plan.ResolvedModel,
		UpstreamProvider: loaded.plan.UpstreamProvider, UpstreamProviderSlug: loaded.plan.UpstreamProviderSlug,
		DisableReasoning: loaded.plan.DisableReasoning, ModelFamily: loaded.plan.ModelFamily,
		PromptSHA256: state.Identity.PromptSHA256, SchemaSHA256: state.Identity.SchemaSHA256,
		FFmpeg: ffmpeg, StartedAt: state.StartedAt, CompletedAt: state.CompletedAt,
		MaximumRequests: loaded.plan.MaximumRequests, MaximumChargeNanoUSD: loaded.plan.MaximumChargeNanoUSD,
		MaximumSpendNanoUSD: loaded.plan.MaximumSpendNanoUSD,
	}
	assessments := make([]fillersafetycert.ReviewAssessment, 0, len(state.Accepted))
	rejected := 0
	for _, accepted := range state.Accepted {
		assessments = append(assessments, accepted.Assessment)
		if accepted.Assessment.Decision == fillersafetycert.ReviewDecisionRejected {
			rejected++
		}
	}
	for _, item := range state.Attempts {
		attemptState := fillersafetycert.ModelReviewAttemptFailed
		if item.State == attemptAccepted {
			attemptState = fillersafetycert.ModelReviewAttemptAccepted
		}
		evidence.Attempts = append(evidence.Attempts, fillersafetycert.ModelReviewAttemptEvidence{
			CaseID: item.CaseID, Attempt: item.Attempt, RequestedAt: item.RequestedAt,
			ReviewedAt: item.ReviewedAt, RequestSHA256: item.RequestSHA256,
			ResponseSHA256: item.ResponseSHA256, GenerationID: item.GenerationID, State: attemptState,
			ObservationSHA256: item.ObservationSHA256, PromptTokens: item.PromptTokens,
			CompletionTokens: item.CompletionTokens, ChargedNanoUSD: item.ChargedNanoUSD,
		})
		evidence.PromptTokens += item.PromptTokens
		evidence.CompletionTokens += item.CompletionTokens
		evidence.ChargedNanoUSD += item.ChargedNanoUSD
	}
	evidence.Requests = len(evidence.Attempts)
	evidenceSHA256, err := fillersafetycert.ModelReviewEvidenceSHA256(evidence)
	if err != nil {
		return Result{}, err
	}
	review := fillersafetycert.AuthorityReview{
		SchemaVersion:   fillersafetycert.AuthorityReviewSchemaVersion,
		ContractVersion: fillersafetycert.AuthorityReviewContractVersion,
		DraftSHA256:     loaded.draftSHA256, ReviewerID: loaded.plan.ReviewerID,
		Role: fillersafetycert.ReviewerPrimary, Method: fillersafetycert.ReviewerModel,
		ModelFamily: loaded.plan.ModelFamily, EvidenceSHA256: evidenceSHA256, ModelEvidence: &evidence,
		SubmittedAt: state.CompletedAt, Assessments: assessments,
	}
	raw, reviewSHA256, err := fillersafetycert.MarshalPrimaryModelReview(loaded.draft, loaded.draftSHA256, review)
	if err != nil {
		return Result{}, fmt.Errorf("validate completed model review: %w", err)
	}
	if err := publishPrivate(outputPath, raw); err != nil {
		return Result{}, err
	}
	return Result{
		Cases: len(assessments), Requests: evidence.Requests, Rejected: rejected,
		PromptTokens: evidence.PromptTokens, CompletionTokens: evidence.CompletionTokens,
		ChargedNanoUSD: evidence.ChargedNanoUSD, ReviewSHA256: reviewSHA256,
	}, nil
}
