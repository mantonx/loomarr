package fillersafetycert

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// MarshalPrimaryModelReview validates one exhaustive independent model review
// against the canonical certification draft and returns private canonical
// bytes. It does not compare a sibling review or establish corpus truth.
func MarshalPrimaryModelReview(
	draft AuthorityDraft,
	draftSHA256 string,
	review AuthorityReview,
) ([]byte, string, error) {
	_, canonicalDraftSHA256, err := MarshalCertificationDraft(draft)
	if err != nil {
		return nil, "", err
	}
	if draftSHA256 != canonicalDraftSHA256 || review.Method != ReviewerModel {
		return nil, "", fmt.Errorf("primary model review does not bind the canonical certification draft")
	}
	if _, err := validateCompleteReview(
		review,
		draft,
		draftSHA256,
		ReviewerPrimary,
		review.SubmittedAt,
	); err != nil {
		return nil, "", err
	}
	if err := validateReviewModelFamilies(draft, review); err != nil {
		return nil, "", err
	}
	raw, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		return nil, "", err
	}
	raw = append(raw, '\n')
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}
