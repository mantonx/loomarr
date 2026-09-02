package fillersafety

import "testing"

func TestHostedCallContractsRejectIncompleteOrFreeFormFacts(t *testing.T) {
	t.Parallel()
	fixture := newOperationFixture(t, []proposedInterval{{StartMS: 100, EndMS: 800}})
	if _, err := fixture.operation.Evaluate(t.Context(), fixture.request); err != nil {
		t.Fatal(err)
	}
	reservation := fixture.repository.Reservations()[0]
	reservationTests := map[string]func(*HostedCallReservation){
		"path-shaped event":  func(value *HostedCallReservation) { value.EventID = "/private/event" },
		"missing candidate":  func(value *HostedCallReservation) { value.CandidateID = "" },
		"unordered modality": func(value *HostedCallReservation) { value.Modalities = []string{"video", "audio"} },
		"invalid version":    func(value *HostedCallReservation) { value.Versions.SchemaSHA256 = "invalid" },
		"zero reservation":   func(value *HostedCallReservation) { value.RequestedNanoUSD = 0 },
		"negative budget":    func(value *HostedCallReservation) { value.Budget.PerRunNanoUSD = -1 },
	}
	for name, mutate := range reservationTests {
		t.Run(name, func(t *testing.T) {
			changed := reservation
			mutate(&changed)
			if err := ValidateHostedCallReservation(changed); err == nil {
				t.Fatal("expected invalid reservation")
			}
		})
	}

	settlement := fixture.repository.Settlements()[0]
	settlementTests := map[string]func(*HostedCallSettlement){
		"missing response":  func(value *HostedCallSettlement) { value.ResponseSHA256 = "" },
		"free form failure": func(value *HostedCallSettlement) { value.Failure = SettlementFailure("private detail") },
		"failure with outcome": func(value *HostedCallSettlement) {
			value.Failure, value.Outcome = FailureTransport, string(AudioAbsent)
		},
		"unknown charge amount": func(value *HostedCallSettlement) {
			value.Failure, value.Outcome, value.ChargeKnown = FailureTransport, "", false
		},
	}
	for name, mutate := range settlementTests {
		t.Run(name, func(t *testing.T) {
			changed := settlement
			mutate(&changed)
			if err := ValidateHostedCallSettlement(changed); err == nil {
				t.Fatal("expected invalid settlement")
			}
		})
	}
}
