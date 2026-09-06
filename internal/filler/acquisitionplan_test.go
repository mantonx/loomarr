package filler_test

import (
	"reflect"
	"testing"

	"github.com/loomarr/loomarr/internal/filler"
)

func candidate(source, id string, year, height int, license string) filler.AcquisitionCandidate {
	return filler.AcquisitionCandidate{
		Identity: filler.RemoteIdentity{Provider: "archive", SourceID: source, RemoteID: id},
		URL:      "https://archive.org/details/" + id, Title: id, License: license,
		ObservedYear: year, Height: height,
	}
}

func TestPlanAcquisition_IsDeterministicAndDiverse(t *testing.T) {
	intent := filler.AcquisitionIntent{Count: 3, Rights: filler.RightsPreferDeclared}
	input := []filler.AcquisitionCandidate{
		candidate("a", "third", 1992, 1080, ""),
		candidate("a", "first", 1990, 480, "cc-by"),
		candidate("b", "second", 1990, 720, "cc-by"),
		candidate("a", "fourth", 1993, 2160, "cc-by"),
	}
	want := []string{"fourth", "second", "first"}
	for run := 0; run < 2; run++ {
		plan, err := filler.PlanAcquisition(intent, input, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(plan.Selected))
		for i, decision := range plan.Selected {
			got[i] = decision.Candidate.Identity.RemoteID
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("selected = %v, want %v", got, want)
		}
		if len(plan.Rejected) != 1 || plan.Rejected[0].Disposition != filler.CandidateRankedBelowLimit {
			t.Fatalf("rejected = %+v", plan.Rejected)
		}
	}
}

func TestPlanAcquisition_UnknownNeverSatisfiesHardConstraints(t *testing.T) {
	base := candidate("classic", "unknown", 0, 0, "")
	checks := []struct {
		name   string
		intent filler.AcquisitionIntent
		want   filler.CandidateDisposition
	}{
		{"rights", filler.AcquisitionIntent{Rights: filler.RightsRequireDeclared}, filler.CandidateRightsUnknown},
		{"era", filler.AcquisitionIntent{EraStart: 1980, EraEnd: 1989}, filler.CandidateEraUnknown},
		{"duration", filler.AcquisitionIntent{MaxDurationMS: 120_000}, filler.CandidateDurationUnknown},
		{"quality", filler.AcquisitionIntent{MinHeight: 480}, filler.CandidateQualityUnknown},
		{"role", filler.AcquisitionIntent{Roles: []filler.Kind{filler.Commercial}}, filler.CandidateRoleUnknown},
		{"audience", filler.AcquisitionIntent{Audiences: []filler.Audience{filler.Kids}}, filler.CandidateAudienceUnknown},
		{"taxonomy", filler.AcquisitionIntent{TaxonomyGaps: []string{"toys"}}, filler.CandidateTaxonomyUnknown},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := filler.PlanAcquisition(tc.intent, []filler.AcquisitionCandidate{base}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Selected) != 0 || len(plan.Rejected) != 1 || plan.Rejected[0].Disposition != tc.want {
				t.Fatalf("plan = %+v, want rejection %s", plan, tc.want)
			}
		})
	}
}

func TestPlanAcquisition_ExplainsPriorStateAndDuplicates(t *testing.T) {
	first := candidate("classic", "same", 1992, 480, "cc-by")
	key := first.Identity.Key()
	plan, err := filler.PlanAcquisition(filler.AcquisitionIntent{Count: 5}, []filler.AcquisitionCandidate{
		first,
		first,
		candidate("classic", "queued", 1992, 480, "cc-by"),
		candidate("classic", "declined", 1992, 480, "cc-by"),
	}, map[string]filler.ExistingRemoteState{
		key: filler.RemoteCatalogued,
		candidate("classic", "queued", 0, 0, "").Identity.Key():   filler.RemoteQueued,
		candidate("classic", "declined", 0, 0, "").Identity.Key(): filler.RemoteDeclined,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[filler.CandidateDisposition]int{
		filler.CandidateAlreadyCatalogued:  1,
		filler.CandidateDuplicateRemote:    1,
		filler.CandidateAlreadyQueued:      1,
		filler.CandidatePreviouslyDeclined: 1,
	}
	got := map[filler.CandidateDisposition]int{}
	for _, decision := range plan.Rejected {
		got[decision.Disposition]++
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rejections = %v, want %v", got, want)
	}
}

func TestDefaultAcquisitionIntent_NamesTheCoverageProjection(t *testing.T) {
	intent := filler.DefaultAcquisitionIntent(filler.PoolReport{Channels: []filler.ChannelCoverage{{
		Name: "Saturday Mornings", Report: filler.CoverageReport{Level: filler.MatchBumperCard},
	}}}, filler.Geography{Country: "us", Market: "new york"})
	if intent.CatalogReason != "Improve filler coverage for Saturday Mornings; its current match level is bumper_card." {
		t.Fatalf("reason = %q", intent.CatalogReason)
	}
	if intent.Geography.Country != "US" || intent.Count != 12 || intent.Version != filler.AcquisitionIntentVersion {
		t.Fatalf("intent = %+v", intent)
	}
}

func TestPlanAcquisition_RejectsConstraintsOutsideTheClosedVocabulary(t *testing.T) {
	for _, intent := range []filler.AcquisitionIntent{
		{Count: -1},
		{Count: 51},
		{EraStart: 1799},
		{EraEnd: 2201},
		{MinHeight: 4321},
	} {
		if _, err := filler.PlanAcquisition(intent, nil, nil); err == nil {
			t.Errorf("PlanAcquisition(%+v) succeeded, want validation error", intent)
		}
	}
}

func TestAcquisitionIntentFamily_IgnoresPresentationButKeepsSelectionConstraints(t *testing.T) {
	base := filler.AcquisitionIntent{EraStart: 1990, EraEnd: 1999, Count: 2, CatalogReason: "first"}
	presentationChange := filler.AcquisitionIntent{EraStart: 1990, EraEnd: 1999, Count: 40, CatalogReason: "second"}
	constraintChange := filler.AcquisitionIntent{EraStart: 1980, EraEnd: 1989, Count: 2, CatalogReason: "first"}
	if base.FamilyKey() != presentationChange.FamilyKey() {
		t.Fatal("count/reason changed the semantic intent family")
	}
	if base.FamilyKey() == constraintChange.FamilyKey() {
		t.Fatal("era constraints did not change the semantic intent family")
	}
}
