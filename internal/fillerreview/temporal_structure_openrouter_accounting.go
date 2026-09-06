package fillerreview

import (
	"fmt"
	"math"

	"github.com/loomarr/loomarr/internal/fillereval"
)

type temporalStructureAccounting struct {
	charged         int64
	consumed        int64
	unknown         int
	overReservation int64
}

func validateTemporalStructureAttemptAccounting(attempt TemporalStructureOpenRouterAttempt, reservation int64) (int64, error) {
	if attempt.ReservedNanoUSD != reservation || attempt.ChargedNanoUSD < 0 {
		return 0, fmt.Errorf("reservation or charge is invalid")
	}
	known := attempt.State == temporalOpenRouterAttemptAccepted || attempt.State == temporalOpenRouterAttemptFailed || attempt.State == temporalOpenRouterAttemptOverReservation
	unknown := attempt.State == temporalOpenRouterAttemptReserved || attempt.State == temporalOpenRouterAttemptUnsettled
	settled, chargeErr := fillereval.USDToNanoCeil(attempt.ChargedAmountUSD)
	if !known && !unknown {
		return 0, fmt.Errorf("state is invalid")
	}
	if known && (attempt.ChargedAmountUSD == "" || chargeErr != nil || settled != attempt.ChargedNanoUSD || attempt.ResponseSHA256 == "" || attempt.RawResponsePath == "") {
		return 0, fmt.Errorf("known settlement is invalid")
	}
	if unknown && (attempt.ChargedAmountUSD != "" || attempt.ChargedNanoUSD != 0) {
		return 0, fmt.Errorf("unknown settlement contains a charge")
	}
	if attempt.State == temporalOpenRouterAttemptAccepted && attempt.OperationalFailure != "" {
		return 0, fmt.Errorf("accepted settlement has a failure")
	}
	if (attempt.State == temporalOpenRouterAttemptFailed || attempt.State == temporalOpenRouterAttemptUnsettled) && !validTemporalOpenRouterFailure(attempt.OperationalFailure) {
		return 0, fmt.Errorf("failed settlement has no valid failure")
	}
	if attempt.State == temporalOpenRouterAttemptOverReservation {
		if attempt.ChargedNanoUSD <= attempt.ReservedNanoUSD || attempt.OperationalFailure != fillereval.TemporalFailureProvider {
			return 0, fmt.Errorf("over-reservation settlement is not explicit")
		}
	} else if known && attempt.ChargedNanoUSD > attempt.ReservedNanoUSD {
		return 0, fmt.Errorf("ordinary settlement exceeds its reservation")
	}
	if attempt.State == temporalOpenRouterAttemptReserved && (attempt.ResponseSHA256 != "" || attempt.RawResponsePath != "" || attempt.GenerationID != "" || attempt.OperationalFailure != "") {
		return 0, fmt.Errorf("reserved attempt contains response state")
	}
	if unknown {
		return attempt.ReservedNanoUSD, nil
	}
	return attempt.ChargedNanoUSD, nil
}

func summarizeTemporalStructureAccounting(attempts []TemporalStructureOpenRouterAttempt, reservation, authorized int64) (temporalStructureAccounting, error) {
	var summary temporalStructureAccounting
	for index, attempt := range attempts {
		cost, err := validateTemporalStructureAttemptAccounting(attempt, reservation)
		if err != nil {
			return temporalStructureAccounting{}, fmt.Errorf("attempt %d: %w", index, err)
		}
		if attempt.State == temporalOpenRouterAttemptOverReservation {
			if index != len(attempts)-1 || summary.consumed > authorized-reservation {
				return temporalStructureAccounting{}, fmt.Errorf("attempt %d did not stop after an authorized reservation overran", index)
			}
			summary.overReservation += attempt.ChargedNanoUSD - attempt.ReservedNanoUSD
		} else if summary.consumed > authorized-cost {
			return temporalStructureAccounting{}, fmt.Errorf("attempt %d exceeds authorized spend", index)
		}
		if cost > math.MaxInt64-summary.consumed || attempt.ChargedNanoUSD > math.MaxInt64-summary.charged {
			return temporalStructureAccounting{}, fmt.Errorf("attempt %d overflows accounting", index)
		}
		summary.consumed += cost
		summary.charged += attempt.ChargedNanoUSD
		if attempt.State == temporalOpenRouterAttemptUnsettled || attempt.State == temporalOpenRouterAttemptReserved {
			summary.unknown++
		}
	}
	return summary, nil
}
