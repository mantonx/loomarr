package fillersafetycert

import (
	"strings"
	"testing"
)

func TestMarshalCertificationDraftProducesCanonicalReviewableBytes(t *testing.T) {
	t.Parallel()
	fixture := newAuthorityBuildFixture(t)

	first, firstSHA, err := MarshalCertificationDraft(fixture.draft)
	if err != nil {
		t.Fatal(err)
	}
	second, secondSHA, err := MarshalCertificationDraft(fixture.draft)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || firstSHA != secondSHA || !strings.HasSuffix(string(first), "\n") ||
		firstSHA != hashBytes(first) {
		t.Fatalf("draft was not canonical: first=%s second=%s", firstSHA, secondSHA)
	}
}

func TestMarshalCertificationDraftRequiresFullPreReviewCoverage(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*AuthorityDraft)
	}{
		{name: "development", mutate: func(draft *AuthorityDraft) { draft.ChallengeKind = ChallengeDevelopment }},
		{name: "positive family short", mutate: func(draft *AuthorityDraft) { draft.Cases = draft.Cases[1:] }},
		{name: "unknown clean slice", mutate: func(draft *AuthorityDraft) {
			draft.Cases[len(draft.Cases)-1].Slices = []string{"invented_clean_slice"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newAuthorityBuildFixture(t)
			test.mutate(&fixture.draft)
			if _, _, err := MarshalCertificationDraft(fixture.draft); err == nil {
				t.Fatal("expected incomplete certification draft rejection")
			}
		})
	}
}
