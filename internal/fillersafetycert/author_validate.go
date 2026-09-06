package fillersafetycert

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

type validatedReviews struct {
	first       map[string]ReviewAssessment
	second      map[string]ReviewAssessment
	adjudicator map[string]ReviewAssessment
}

func validateAuthorityDraft(draft AuthorityDraft, expectedCases int) error {
	if draft.SchemaVersion != AuthorityDraftSchemaVersion || draft.ContractVersion != AuthorityDraftContractVersion ||
		(draft.ChallengeKind != ChallengeDevelopment && draft.ChallengeKind != ChallengeCertification) ||
		!validSHA256(draft.PolicySHA256) || !validSHA256(draft.ProposerSHA256) ||
		!boundedID(draft.ProposerFamily) || !boundedID(draft.Implementation) || len(draft.Cases) != expectedCases {
		return fmt.Errorf("authority draft identity or exact case count is invalid")
	}
	if err := validateRoute(draft.AudioRoute, "spoken-safety", "native-audio", []string{"audio"}); err != nil {
		return fmt.Errorf("authority draft audio route is invalid")
	}
	if err := validateRoute(draft.VideoRoute, "spoken-safety", "complete-video", []string{"audio", "video"}); err != nil {
		return fmt.Errorf("authority draft video route is invalid")
	}
	previous := ""
	sources, families := map[string]struct{}{}, map[string]struct{}{}
	for _, item := range draft.Cases {
		if !boundedPrivateID(item.CaseID) || item.CaseID <= previous || !boundedPrivateID(item.SourceFamily) ||
			item.SourceAuthority.SourceID != item.CaseID || item.SourceAuthority.PolicySHA256 != draft.PolicySHA256 ||
			item.SourceAuthority.Implementation != draft.Implementation || !validLocale(item.Locale) ||
			!validSHA256(item.TruthProvenanceSHA256) || !validSHA256(item.RightsSHA256) ||
			len(item.Slices) == 0 || len(item.Slices) > 8 || !strictlySorted(item.Slices) {
			return fmt.Errorf("authority draft contains invalid or unsorted case identity")
		}
		if _, err := fillersafety.SourceAuthoritySHA256(item.SourceAuthority); err != nil {
			return fmt.Errorf("authority draft contains invalid source authority")
		}
		if _, duplicate := sources[item.SourceAuthority.SourceSHA256]; duplicate {
			return fmt.Errorf("authority draft repeats source content")
		}
		if _, duplicate := families[item.SourceFamily]; duplicate {
			return fmt.Errorf("authority draft repeats a source family")
		}
		sources[item.SourceAuthority.SourceSHA256], families[item.SourceFamily] = struct{}{}, struct{}{}
		if item.Label == LabelPositive {
			if !validPositiveIntervals(item.PositiveIntervals, item.SourceAuthority.DurationMS) {
				return fmt.Errorf("authority draft contains invalid positive truth")
			}
		} else if item.Label != LabelClean || len(item.PositiveIntervals) != 0 {
			return fmt.Errorf("authority draft contains invalid clean truth")
		}
		previous = item.CaseID
	}
	return nil
}

func validateAuthorityReviews(inputs loadedAuthorityInputs, authoredAt time.Time) (validatedReviews, error) {
	if inputs.first.ReviewerID == inputs.second.ReviewerID {
		return validatedReviews{}, fmt.Errorf("primary authority reviewers must be independent")
	}
	first, err := validateCompleteReview(inputs.first, inputs.draft, inputs.draftSHA, ReviewerPrimary, authoredAt)
	if err != nil {
		return validatedReviews{}, fmt.Errorf("first authority review: %w", err)
	}
	second, err := validateCompleteReview(inputs.second, inputs.draft, inputs.draftSHA, ReviewerPrimary, authoredAt)
	if err != nil {
		return validatedReviews{}, fmt.Errorf("second authority review: %w", err)
	}
	if err := validateReviewModelFamilies(inputs.draft, inputs.first, inputs.second); err != nil {
		return validatedReviews{}, err
	}
	disputed := make([]string, 0)
	for _, item := range inputs.draft.Cases {
		if first[item.CaseID].Decision != second[item.CaseID].Decision {
			disputed = append(disputed, item.CaseID)
		}
	}
	if len(disputed) == 0 {
		if inputs.adjudicator != nil {
			return validatedReviews{}, fmt.Errorf("authority adjudication is forbidden when primaries agree")
		}
		return validatedReviews{first: first, second: second}, nil
	}
	if inputs.adjudicator == nil || inputs.adjudicator.ReviewerID == inputs.first.ReviewerID ||
		inputs.adjudicator.ReviewerID == inputs.second.ReviewerID {
		return validatedReviews{}, fmt.Errorf("disputed authority cases require an independent adjudicator")
	}
	adjudicator, err := validateAdjudication(*inputs.adjudicator, inputs.draft, inputs.draftSHA, disputed, authoredAt)
	if err != nil {
		return validatedReviews{}, fmt.Errorf("authority adjudication: %w", err)
	}
	if err := validateReviewModelFamilies(inputs.draft, inputs.first, inputs.second, *inputs.adjudicator); err != nil {
		return validatedReviews{}, err
	}
	return validatedReviews{first: first, second: second, adjudicator: adjudicator}, nil
}

func validateReviewModelFamilies(draft AuthorityDraft, reviews ...AuthorityReview) error {
	excluded := map[string]struct{}{
		draft.ProposerFamily: {}, draft.AudioRoute.ModelFamily: {}, draft.VideoRoute.ModelFamily: {},
	}
	seen := map[string]struct{}{}
	for _, review := range reviews {
		if review.Method != ReviewerModel {
			continue
		}
		if _, found := excluded[review.ModelFamily]; found {
			return fmt.Errorf("model reviewer is not independent from the evaluated cascade")
		}
		if _, found := seen[review.ModelFamily]; found {
			return fmt.Errorf("model reviewer families are not independent")
		}
		seen[review.ModelFamily] = struct{}{}
	}
	return nil
}

func validateCompleteReview(review AuthorityReview, draft AuthorityDraft, draftSHA, role string, authoredAt time.Time) (map[string]ReviewAssessment, error) {
	if err := validateReviewEnvelope(review, draftSHA, role, authoredAt); err != nil {
		return nil, err
	}
	if len(review.Assessments) != len(draft.Cases) {
		return nil, fmt.Errorf("review does not cover every draft case")
	}
	result := make(map[string]ReviewAssessment, len(review.Assessments))
	for index, assessment := range review.Assessments {
		if assessment.CaseID != draft.Cases[index].CaseID || !validReviewAssessment(assessment, draft.Cases[index]) {
			return nil, fmt.Errorf("review assessments are incomplete, unsorted, or invalid")
		}
		result[assessment.CaseID] = assessment
	}
	if err := validateModelReviewEvidence(review); err != nil {
		return nil, err
	}
	return result, nil
}

func validateAdjudication(review AuthorityReview, draft AuthorityDraft, draftSHA string, disputed []string, authoredAt time.Time) (map[string]ReviewAssessment, error) {
	if err := validateReviewEnvelope(review, draftSHA, ReviewerAdjudicator, authoredAt); err != nil {
		return nil, err
	}
	if len(review.Assessments) != len(disputed) {
		return nil, fmt.Errorf("adjudication must cover exactly the disputed cases")
	}
	draftByID := make(map[string]AuthorityDraftCase, len(draft.Cases))
	for _, item := range draft.Cases {
		draftByID[item.CaseID] = item
	}
	result := make(map[string]ReviewAssessment, len(disputed))
	for index, assessment := range review.Assessments {
		item, ok := draftByID[assessment.CaseID]
		if !ok || assessment.CaseID != disputed[index] || !validReviewAssessment(assessment, item) {
			return nil, fmt.Errorf("adjudication is incomplete, unsorted, or invalid")
		}
		result[assessment.CaseID] = assessment
	}
	if err := validateModelReviewEvidence(review); err != nil {
		return nil, err
	}
	return result, nil
}

func validateReviewEnvelope(review AuthorityReview, draftSHA, role string, authoredAt time.Time) error {
	if review.SchemaVersion != AuthorityReviewSchemaVersion || review.ContractVersion != AuthorityReviewContractVersion ||
		review.DraftSHA256 != draftSHA || !boundedPrivateID(review.ReviewerID) || review.Role != role ||
		!validSHA256(review.EvidenceSHA256) || review.SubmittedAt.IsZero() || review.SubmittedAt.After(authoredAt) {
		return fmt.Errorf("review envelope does not bind the draft, role, identity, or time")
	}
	if review.Method == ReviewerHuman {
		if review.ModelFamily != "" || review.ModelEvidence != nil {
			return fmt.Errorf("human reviewer declares a model family")
		}
	} else if review.Method != ReviewerModel || !boundedID(review.ModelFamily) || review.ModelEvidence == nil {
		return fmt.Errorf("review method or model family is invalid")
	}
	return nil
}

func validReviewAssessment(assessment ReviewAssessment, item AuthorityDraftCase) bool {
	if assessment.Decision != ReviewDecisionVerified && assessment.Decision != ReviewDecisionRejected {
		return false
	}
	if assessment.Decision == ReviewDecisionRejected || item.Label == LabelClean {
		return len(assessment.PositiveIntervals) == 0
	}
	return validPositiveIntervals(assessment.PositiveIntervals, item.SourceAuthority.DurationMS) &&
		slices.Equal(assessment.PositiveIntervals, item.PositiveIntervals)
}

func reviewAttestationSHA256(review AuthorityReview, assessment ReviewAssessment) string {
	raw, err := json.Marshal(struct {
		SchemaVersion   int              `json:"schemaVersion"`
		ContractVersion string           `json:"contractVersion"`
		DraftSHA256     string           `json:"draftSha256"`
		ReviewerID      string           `json:"reviewerId"`
		Role            string           `json:"role"`
		Method          string           `json:"method"`
		ModelFamily     string           `json:"modelFamily,omitempty"`
		EvidenceSHA256  string           `json:"evidenceSha256"`
		SubmittedAt     time.Time        `json:"submittedAt"`
		Assessment      ReviewAssessment `json:"assessment"`
	}{
		SchemaVersion: review.SchemaVersion, ContractVersion: review.ContractVersion,
		DraftSHA256: review.DraftSHA256, ReviewerID: review.ReviewerID, Role: review.Role,
		Method: review.Method, ModelFamily: review.ModelFamily, SubmittedAt: review.SubmittedAt.UTC(),
		EvidenceSHA256: review.EvidenceSHA256,
		Assessment:     assessment,
	})
	if err != nil {
		return ""
	}
	return hashBytes(raw)
}

func boundedPrivateID(value string) bool {
	return boundedID(value) && !strings.Contains(value, "/")
}
