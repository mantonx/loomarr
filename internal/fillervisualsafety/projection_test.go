package fillervisualsafety_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestProjectQuarantinesEveryMappedDerivative(t *testing.T) {
	authority, plan, coverage := visualEvidenceFixture(t)
	portable := visualObservation(t, authority, coverage, fillervisualsafety.ProducerPortable, fillervisualsafety.ObservationNoSignal)
	apple := visualObservation(t, authority, coverage, fillervisualsafety.ProducerAppleSCA, fillervisualsafety.ObservationProhibited)
	result := fillervisualsafety.Reduce(authority, coverage, plan, []fillervisualsafety.Observation{portable, apple})
	set := derivativeSet(t, authority)

	projection, err := fillervisualsafety.Project(authority, result, set)
	if err != nil {
		t.Fatal(err)
	}
	if err := fillervisualsafety.ValidateProjection(projection, authority, result, set); err != nil {
		t.Fatal(err)
	}
	if projection.SourceDisposition != fillervisualsafety.DispositionQuarantine || projection.ProductionAdmissionAllowed || len(projection.Derivatives) != 3 {
		t.Fatalf("unexpected source projection: %#v", projection)
	}
	for _, derivative := range projection.Derivatives {
		if derivative.Disposition != fillervisualsafety.DispositionQuarantine {
			t.Fatalf("positive source escaped derivative quarantine: %#v", derivative)
		}
	}
}

func TestProjectNoSignalStillGrantsNoAdmission(t *testing.T) {
	authority, plan, coverage := visualEvidenceFixture(t)
	portable := visualObservation(t, authority, coverage, fillervisualsafety.ProducerPortable, fillervisualsafety.ObservationNoSignal)
	result := fillervisualsafety.Reduce(authority, coverage, plan, []fillervisualsafety.Observation{portable})
	set := derivativeSet(t, authority)

	projection, err := fillervisualsafety.Project(authority, result, set)
	if err != nil {
		t.Fatal(err)
	}
	if projection.SourceDisposition != fillervisualsafety.DispositionNoSignal || projection.ProductionAdmissionAllowed {
		t.Fatalf("visual negative gained admission authority: %#v", projection)
	}
}

func TestProjectRejectsIncompleteOrDriftedMappings(t *testing.T) {
	authority, plan, coverage := visualEvidenceFixture(t)
	portable := visualObservation(t, authority, coverage, fillervisualsafety.ProducerPortable, fillervisualsafety.ObservationNoSignal)
	result := fillervisualsafety.Reduce(authority, coverage, plan, []fillervisualsafety.Observation{portable})

	for _, test := range []struct {
		name   string
		mutate func(fillervisualsafety.DerivativeSet) fillervisualsafety.DerivativeSet
	}{
		{name: "incomplete", mutate: func(set fillervisualsafety.DerivativeSet) fillervisualsafety.DerivativeSet {
			set.Complete = false
			return set
		}},
		{name: "wrong source", mutate: func(set fillervisualsafety.DerivativeSet) fillervisualsafety.DerivativeSet {
			set.SourceSHA256 = strings.Repeat("f", 64)
			return set
		}},
		{name: "missing derivative", mutate: func(set fillervisualsafety.DerivativeSet) fillervisualsafety.DerivativeSet {
			set.Derivatives = set.Derivatives[:2]
			return set
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			set := test.mutate(derivativeSet(t, authority))
			if _, err := fillervisualsafety.Project(authority, result, set); err == nil {
				t.Fatal("expected mapping drift to fail")
			}
		})
	}
}

func TestDerivativeSetCanonicalizesOrderAndRejectsDuplicateIDs(t *testing.T) {
	authority := visualAuthority(t, 3_050)
	set := derivativeSetInput(authority)
	slices.Reverse(set.Derivatives)
	sealed, err := fillervisualsafety.SealDerivativeSet(set)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Derivatives[0].ID != "child-1" || sealed.Derivatives[2].ID != "child-3" {
		t.Fatalf("derivative set is not canonical: %#v", sealed.Derivatives)
	}

	set = derivativeSetInput(authority)
	set.Derivatives[1].ID = set.Derivatives[0].ID
	if _, err := fillervisualsafety.SealDerivativeSet(set); err == nil {
		t.Fatal("expected repeated derivative identity to fail")
	}
}

func derivativeSet(t *testing.T, authority fillervisualsafety.SourceAuthority) fillervisualsafety.DerivativeSet {
	t.Helper()
	set, err := fillervisualsafety.SealDerivativeSet(derivativeSetInput(authority))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func derivativeSetInput(authority fillervisualsafety.SourceAuthority) fillervisualsafety.DerivativeSet {
	items := make([]fillervisualsafety.DerivativeBinding, 3)
	for index := range items {
		items[index] = fillervisualsafety.DerivativeBinding{
			ID: "child-" + string(rune('1'+index)), SHA256: strings.Repeat(string(rune('c'+index)), 64), Bytes: int64(1_000 + index),
			SourceAuthoritySHA256: authority.SHA256, SourceSHA256: authority.SourceSHA256,
		}
	}
	return fillervisualsafety.DerivativeSet{
		SourceAuthoritySHA256: authority.SHA256, SourceSHA256: authority.SourceSHA256,
		Complete: true, Derivatives: items,
	}
}
