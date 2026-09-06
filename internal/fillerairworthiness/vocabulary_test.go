package fillerairworthiness

import (
	"slices"
	"testing"
)

func TestVocabularyIsClosedSortedAndUniquelyOwned(t *testing.T) {
	t.Parallel()
	flags := Vocabulary()
	if len(flags) != 27 || !slices.IsSorted(flags) {
		t.Fatalf("vocabulary length/sort = %d/%v", len(flags), slices.IsSorted(flags))
	}
	for index, flag := range flags {
		if index > 0 && flags[index-1] == flag {
			t.Fatalf("duplicate flag %q", flag)
		}
		owners := flagOwners(flag)
		if len(owners) == 0 {
			t.Fatalf("flag %q has no evidence owner", flag)
		}
		for _, owner := range owners {
			if !validAxis(owner) {
				t.Fatalf("flag %q has invalid owner %q", flag, owner)
			}
		}
	}
	flags[0] = Flag("mutated")
	if Vocabulary()[0] == flags[0] {
		t.Fatal("Vocabulary returned mutable package storage")
	}
}

func TestAxisProfileRejectsUnknownDuplicateOrForeignCertification(t *testing.T) {
	t.Parallel()
	base := testProfiles()[0]
	tests := []struct {
		name   string
		mutate func(*AxisProfile)
	}{
		{"unknown", func(profile *AxisProfile) { profile.CertifiedFlags = append(profile.CertifiedFlags, Flag("unknown")) }},
		{"duplicate", func(profile *AxisProfile) { profile.CertifiedFlags = append(profile.CertifiedFlags, FlagAdultNudity) }},
		{"foreign", func(profile *AxisProfile) { profile.CertifiedFlags = append(profile.CertifiedFlags, FlagProfanity) }},
		{"invalid digest", func(profile *AxisProfile) { profile.CertificationSHA256 = "invalid" }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			profile := base
			profile.CertifiedFlags = slices.Clone(base.CertifiedFlags)
			test.mutate(&profile)
			if _, err := normalizeAxisProfile(profile); err == nil {
				t.Fatal("invalid profile normalized")
			}
		})
	}
}
