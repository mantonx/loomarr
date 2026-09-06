package fillersafetycert

import (
	"bytes"
	"strings"
	"testing"
)

func TestMarshalPrimaryModelReviewAcceptsRejectedCleanControl(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)
	review := fixture.first
	attachFixtureModelEvidence(&review, "independent-review-model")
	review.Assessments[MinimumPositiveFamilies].Decision = ReviewDecisionRejected

	first, firstSHA, err := MarshalPrimaryModelReview(fixture.draft, review.DraftSHA256, review)
	if err != nil {
		t.Fatal(err)
	}
	second, secondSHA, err := MarshalPrimaryModelReview(fixture.draft, review.DraftSHA256, review)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || firstSHA != secondSHA || len(firstSHA) != 64 {
		t.Fatalf("review bytes or digest changed: %s/%s", firstSHA, secondSHA)
	}
}

func TestMarshalPrimaryModelReviewRejectsEvaluatedModelFamily(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)
	review := fixture.first
	attachFixtureModelEvidence(&review, fixture.draft.AudioRoute.ModelFamily)

	if _, _, err := MarshalPrimaryModelReview(fixture.draft, review.DraftSHA256, review); err == nil ||
		!strings.Contains(err.Error(), "not independent") {
		t.Fatalf("err=%v", err)
	}
}
