package suggest_test

// Property tests for the deterministic scorer (score.go: score, themeFit,
// eraBalance, composite — §8 "not pure vibes"). These are seeded plain-Go loops
// (math/rand with a fixed source so any failure reproduces), NOT a framework.
// They assert the structural invariants of the scorer over many random intents +
// ProposalItem sets, complementing the fixture-driven TestScoring_Deterministic
// (suggester_test.go) which pins one concrete pipeline run.
//
// The unexported score functions are reached via the test-only bridge in
// export_score_test.go.

import (
	"math/rand"
	"testing"

	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/suggest"
)

// randWords is a small vocabulary the generators draw from so terms sometimes hit
// and sometimes miss the haystack (exercising both themeFit branches).
var randWords = []string{
	"action", "sci-fi", "comedy", "noir", "high-energy", "1990s", "matrix",
	"speed", "rock", "thriller", "drama", "cozy", "space", "heist", "the",
	"of", "a", "channel", "movies", "", "xyzzy", "quux",
}

func pickWord(r *rand.Rand) string { return randWords[r.Intn(len(randWords))] }

// randIntent builds an arbitrary Intent from the seeded source.
func randIntent(r *rand.Rand) suggest.Intent {
	nMust := r.Intn(3)
	must := make([]string, nMust)
	for i := range must {
		must[i] = pickWord(r)
	}
	return suggest.Intent{
		Description: pickWord(r) + " " + pickWord(r) + " " + pickWord(r),
		Era:         pickWord(r),
		Tone:        pickWord(r),
		MustInclude: must,
	}
}

// randItem builds an arbitrary ProposalItem. Year is sometimes 0 (unknown) so the
// eraBalance "no year info" branch is hit; genres/overview/rationale/name draw from
// the shared vocabulary so themeFit sometimes matches.
func randItem(r *rand.Rand) suggest.ProposalItem {
	var year int
	switch r.Intn(3) {
	case 0:
		year = 0 // unknown year
	default:
		year = 1950 + r.Intn(80) // 1950..2029
	}
	nGenres := r.Intn(3)
	genres := make([]string, nGenres)
	for i := range genres {
		genres[i] = pickWord(r)
	}
	return suggest.ProposalItem{
		MediaType: provision.Movie,
		TMDBID:    r.Intn(100000),
		Name:      pickWord(r) + " " + pickWord(r),
		Year:      year,
		Genres:    genres,
		Overview:  pickWord(r) + " " + pickWord(r),
		Rationale: pickWord(r),
	}
}

// randItems builds n arbitrary items.
func randItems(r *rand.Rand, n int) []suggest.ProposalItem {
	if n == 0 {
		return nil
	}
	items := make([]suggest.ProposalItem, n)
	for i := range items {
		items[i] = randItem(r)
	}
	return items
}

const propIters = 2000

func in01(v float64) bool { return v >= 0 && v <= 1 }

// TestScore_AllSubScoresInUnitInterval: for arbitrary intents + lineup/acquisition
// sets, every sub-score AND Overall stays within [0,1]. A score outside the unit
// interval breaks ranking comparability (§8) and downstream weighting.
func TestScore_AllSubScoresInUnitInterval(t *testing.T) {
	r := rand.New(rand.NewSource(0xC0FFEE))
	for i := 0; i < propIters; i++ {
		intent := randIntent(r)
		nLine := r.Intn(8)
		nAcq := r.Intn(8)
		lineup := randItems(r, nLine)
		acq := randItems(r, nAcq)

		s := suggest.ScoreForTest(intent, lineup, acq)
		if !in01(s.ThemeFit) {
			t.Fatalf("iter %d: ThemeFit out of [0,1]: %v (intent=%+v)", i, s.ThemeFit, intent)
		}
		if !in01(s.AvailabilityRatio) {
			t.Fatalf("iter %d: AvailabilityRatio out of [0,1]: %v", i, s.AvailabilityRatio)
		}
		if !in01(s.EraBalance) {
			t.Fatalf("iter %d: EraBalance out of [0,1]: %v", i, s.EraBalance)
		}
		if !in01(s.Overall) {
			t.Fatalf("iter %d: Overall out of [0,1]: %v (scores=%+v)", i, s.Overall, s)
		}
	}
}

// TestScore_Deterministic: the same inputs always yield identical Scores. This is
// the core §8 guarantee ("same intent + same picks → same Scores"); here it is
// checked over many random inputs, not just one pinned lineup.
func TestScore_Deterministic(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for i := 0; i < propIters; i++ {
		intent := randIntent(r)
		lineup := randItems(r, r.Intn(8))
		acq := randItems(r, r.Intn(8))

		a := suggest.ScoreForTest(intent, lineup, acq)
		b := suggest.ScoreForTest(intent, lineup, acq)
		if a != b {
			t.Fatalf("iter %d: scoring not deterministic: %+v vs %+v", i, a, b)
		}
		// Sub-functions are individually deterministic too.
		if suggest.ThemeFitForTest(intent, lineup, acq) != suggest.ThemeFitForTest(intent, lineup, acq) {
			t.Fatalf("iter %d: themeFit not deterministic", i)
		}
		if suggest.EraBalanceForTest(intent, lineup, acq) != suggest.EraBalanceForTest(intent, lineup, acq) {
			t.Fatalf("iter %d: eraBalance not deterministic", i)
		}
	}
}

// TestScore_AvailabilityRatioExact: AvailabilityRatio == len(lineup)/(len(lineup)+
// len(acquisitions)) exactly, for arbitrary non-empty splits. This is the
// "playable right now" signal (§8/§9); the ratio must reflect the actual split.
func TestScore_AvailabilityRatioExact(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for i := 0; i < propIters; i++ {
		nLine := r.Intn(8)
		nAcq := r.Intn(8)
		if nLine+nAcq == 0 {
			continue // empty handled by its own test
		}
		intent := randIntent(r)
		lineup := randItems(r, nLine)
		acq := randItems(r, nAcq)

		s := suggest.ScoreForTest(intent, lineup, acq)
		want := float64(nLine) / float64(nLine+nAcq)
		if s.AvailabilityRatio != want {
			t.Fatalf("iter %d: AvailabilityRatio = %v, want %v (line=%d acq=%d)",
				i, s.AvailabilityRatio, want, nLine, nAcq)
		}
	}
}

// TestScore_EmptyInputZeroScores: no items at all → the zero Scores value (every
// field 0). score() short-circuits before computing anything, so an empty proposal
// carries no misleading non-zero signals.
func TestScore_EmptyInputZeroScores(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	for i := 0; i < 200; i++ {
		intent := randIntent(r) // intent varies; emptiness is about items
		got := suggest.ScoreForTest(intent, nil, nil)
		if got != (suggest.Scores{}) {
			t.Fatalf("iter %d: empty input should give zero Scores, got %+v", i, got)
		}
	}
	// Explicit empty (non-nil) slices behave the same as nil.
	if got := suggest.ScoreForTest(suggest.Intent{Description: "action"}, []suggest.ProposalItem{}, []suggest.ProposalItem{}); got != (suggest.Scores{}) {
		t.Fatalf("empty (non-nil) slices should give zero Scores, got %+v", got)
	}
}

func TestThemeFitDoesNotUseModelRationaleAsEvidence(t *testing.T) {
	got := suggest.ThemeFitForTest(
		suggest.Intent{Description: "friday family showcase"},
		[]suggest.ProposalItem{{
			Name: "Orbital Detectives", Genres: []string{"Science Fiction"},
			Rationale: "A perfect match for the Friday Family Showcase.",
		}},
		nil,
	)
	if got != 0 {
		t.Fatalf("model-authored rationale manufactured theme evidence: got %v", got)
	}
}

// TestComposite_WeightsSumToOne: the composite weights sum to exactly 1.0, so a
// proposal that maxes every sub-score gets Overall == 1.0 exactly (and an all-zero
// one gets 0.0). This pins the invariant the design doc calls out ("Weights sum to
// 1.0 so Overall stays in [0,1]") via the constants' observable behavior.
func TestComposite_WeightsSumToOne(t *testing.T) {
	// Known-max input: every sub-score at 1.0 ⇒ Overall must be exactly 1.0.
	if got := suggest.CompositeForTest(suggest.Scores{ThemeFit: 1, AvailabilityRatio: 1, EraBalance: 1}); got != 1.0 {
		t.Fatalf("all-max sub-scores should composite to exactly 1.0 (weights must sum to 1); got %v", got)
	}
	// Known-min input: all zero ⇒ 0.0.
	if got := suggest.CompositeForTest(suggest.Scores{}); got != 0.0 {
		t.Fatalf("all-zero sub-scores should composite to 0.0; got %v", got)
	}
	// Isolating each weight: a single sub-score at 1.0 (others 0) reveals that
	// weight; the three revealed weights must sum to exactly 1.0.
	wTheme := suggest.CompositeForTest(suggest.Scores{ThemeFit: 1})
	wAvail := suggest.CompositeForTest(suggest.Scores{AvailabilityRatio: 1})
	wEra := suggest.CompositeForTest(suggest.Scores{EraBalance: 1})
	if wTheme+wAvail+wEra != 1.0 {
		t.Fatalf("isolated weights must sum to exactly 1.0; got theme=%v avail=%v era=%v sum=%v",
			wTheme, wAvail, wEra, wTheme+wAvail+wEra)
	}
	// Theme-first ordering (maintainer decision, §8): matching the ask dominates,
	// availability strong second, era a light tiebreaker.
	if !(wTheme > wAvail && wAvail > wEra) {
		t.Fatalf("weights should be theme > availability > era; got theme=%v avail=%v era=%v", wTheme, wAvail, wEra)
	}
}

// TestComposite_BoundedForRandomSubScores: for arbitrary sub-scores in [0,1], the
// composite is a convex combination and therefore also in [0,1]. Because the
// weights are non-negative and sum to 1, the result is bounded by the min and max
// of the inputs.
func TestComposite_BoundedForRandomSubScores(t *testing.T) {
	r := rand.New(rand.NewSource(0xBEEF))
	for i := 0; i < propIters; i++ {
		s := suggest.Scores{
			ThemeFit:          r.Float64(),
			AvailabilityRatio: r.Float64(),
			EraBalance:        r.Float64(),
		}
		got := suggest.CompositeForTest(s)
		if !in01(got) {
			t.Fatalf("iter %d: composite out of [0,1]: %v (in=%+v)", i, got, s)
		}
		// Convexity: result lies within [min, max] of the three sub-scores.
		lo, hi := s.ThemeFit, s.ThemeFit
		for _, v := range []float64{s.AvailabilityRatio, s.EraBalance} {
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		const eps = 1e-12
		if got < lo-eps || got > hi+eps {
			t.Fatalf("iter %d: composite %v not within [min,max]=[%v,%v] of sub-scores %+v", i, got, lo, hi, s)
		}
	}
}

// TestThemeFit_NoTermsIsNeutralMax: an intent with no significant terms (only
// stopwords / short words) has nothing to fit against, so themeFit is 1 (neutral-
// max) regardless of the items — the documented behavior in score.go.
func TestThemeFit_NoTermsIsNeutralMax(t *testing.T) {
	// "a channel of the" → all stopwords/short → zero terms.
	intent := suggest.Intent{Description: "a channel of the"}
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 200; i++ {
		items := randItems(r, 1+r.Intn(5))
		if got := suggest.ThemeFitForTest(intent, items, nil); got != 1 {
			t.Fatalf("iter %d: no-term intent should give themeFit 1, got %v", i, got)
		}
	}
}

// TestEraBalance_SingleOrZeroItemIsNeutral: with 0 or 1 total items eraBalance is
// neutral (1) — there is no spread to reward or penalize.
func TestEraBalance_SingleOrZeroItemIsNeutral(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	for i := 0; i < 200; i++ {
		intent := randIntent(r)
		// zero items
		if got := suggest.EraBalanceForTest(intent, nil, nil); got != 1 {
			t.Fatalf("iter %d: eraBalance of 0 items should be 1, got %v", i, got)
		}
		// exactly one item (either side)
		one := randItems(r, 1)
		if got := suggest.EraBalanceForTest(intent, one, nil); got != 1 {
			t.Fatalf("iter %d: eraBalance of 1 item should be 1, got %v", i, got)
		}
	}
}

// FuzzScore_Invariants: native Go fuzzing over the scorer's structural invariants.
// The corpus bytes are decoded into a small random-ish intent + item set; every
// run must keep all scores in [0,1], keep AvailabilityRatio exact, and stay
// deterministic. Fuzzing complements the seeded loops by letting the engine search
// for adversarial byte sequences.
func FuzzScore_Invariants(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(1))
	f.Add(int64(0xC0FFEE))
	f.Add(int64(-5))
	f.Fuzz(func(t *testing.T, seed int64) {
		r := rand.New(rand.NewSource(seed))
		intent := randIntent(r)
		nLine := r.Intn(6)
		nAcq := r.Intn(6)
		lineup := randItems(r, nLine)
		acq := randItems(r, nAcq)

		s := suggest.ScoreForTest(intent, lineup, acq)
		for _, v := range []float64{s.ThemeFit, s.AvailabilityRatio, s.EraBalance, s.Overall} {
			if !in01(v) {
				t.Fatalf("score out of [0,1]: %+v (seed=%d)", s, seed)
			}
		}
		if nLine+nAcq == 0 {
			if s != (suggest.Scores{}) {
				t.Fatalf("empty input should give zero Scores, got %+v (seed=%d)", s, seed)
			}
		} else {
			want := float64(nLine) / float64(nLine+nAcq)
			if s.AvailabilityRatio != want {
				t.Fatalf("AvailabilityRatio = %v, want %v (seed=%d)", s.AvailabilityRatio, want, seed)
			}
		}
		if again := suggest.ScoreForTest(intent, lineup, acq); again != s {
			t.Fatalf("non-deterministic: %+v vs %+v (seed=%d)", s, again, seed)
		}
	})
}
