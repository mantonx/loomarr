//go:build eval

// Package eval is Loomarr's semantic-evaluation harness (a §14 Go test binary, NOT
// a service). It runs a curated corpus of real channel intents through the REAL
// suggester against a REAL configured LLM + catalog, and scores each proposal with
// (a) deterministic assertions the mock can't make — grounding held, the policy is
// on-ladder, the extracted ceiling matches the intent, availability is sane — and
// (b) an optional judge-model score for the subjective "is this a good lineup?"
// question.
//
// It is gated behind the `eval` build tag so it NEVER runs under comprehensive verification (which
// stays hermetic, §19). Run it with `make eval` (or `go test -tags=eval ./internal/eval/`)
// with LLM_* + LIBRARY_* + TMDB_API_KEY configured. The same corpus doubles as the
// live-smoke script.
package eval

import (
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/suggest"
)

const (
	classicSimpsonsViewerRequest   = "Classic Simpsons reruns from the golden era, curated for variety"
	simpsonsChristmasViewerRequest = "Christmas episodes of The Simpsons already in my library"
)

// TitleEvidenceScope identifies which public Runner output owns a case's exact
// title assertions.
type TitleEvidenceScope string

const (
	TitleEvidenceGrounded  TitleEvidenceScope = "grounded"
	TitleEvidenceScheduled TitleEvidenceScope = "scheduled"
)

// Case is one evaluation: an intent plus the properties its grounded proposal must
// satisfy. The deterministic checks are hard gates; the judge rubric guides the
// LLM-judge score (0..1). A field left zero/empty is not asserted.
type Case struct {
	Name       string
	TemplateID string // non-empty for an exact shipped starter-template Intent
	Intent     Intent

	// --- deterministic expectations (checked without a judge) ---

	// MinGrounded counts owned lineup and outside-library acquisitions together.
	// Use it when ownership is irrelevant to the request. MinLineup and
	// MinAcquisitions assert a specific side of that boundary when required.
	MinGrounded     int
	MinLineup       int
	MinAcquisitions int
	// NoFabrication asserts every grounded pick has a real, resolvable id (the
	// grounding guarantee) — regardless of how many, or whether the intent made
	// sense. Use for adversarial intents where the count is unpredictable but the
	// no-fabrication invariant must hold.
	NoFabrication bool
	// RequireKeys are exact grounded identities that must survive into the
	// actionable Proposal. Named user constraints belong here rather than only in
	// judge prose or a non-empty count assertion.
	RequireKeys []provision.Key
	ForbidKeys  []provision.Key
	// RequireTitles and ForbidTitles compare normalized complete titles against
	// the explicitly selected grounded Proposal or materialized schedule evidence.
	TitleEvidence         TitleEvidenceScope
	RequireTitles         []string
	ForbidTitles          []string
	MinMovies             int
	MinSeries             int
	AllowedMediaTypes     []provision.MediaType
	MinDistinctGenres     int
	MaxToolCalls          int
	MaxCandidatesSurfaced int
	// ExpectedToolOperation scores the routing decision for the frozen
	// certification fixture as title, genre, keyword, network, cast, or creator.
	ExpectedToolOperation string
	// ExpectGroundedCompletion contributes to the certification quality rate;
	// explicit abstention cases leave it false.
	ExpectGroundedCompletion bool
	// ExpectedPolicyCeiling and ExpectedProposalKeys are frozen quality answers,
	// not hard gates. ExpectedProposalAbstention requires a clean, explicit
	// no-grounded-title outcome instead of an empty malformed Proposal.
	ExpectedPolicyCeiling      string
	ExpectedProposalKeys       []provision.Key
	ExpectedProposalAbstention bool
	// RecoveryExpected marks an injected failure the candidate must recover from.
	RecoveryExpected    bool
	TrackRepairRecovery bool
	// RequireScheduledPrograms are stable concrete program identities observed
	// after episode expansion, filtering, grouping, and ordering. Episode identities
	// append :sXXeYY to their series Key.
	RequireScheduledPrograms []string
	ForbidScheduledPrograms  []string
	RequireScheduledSequence []string
	// ExpectCeiling, if set, is the audience ceiling the suggester MUST have
	// extracted from the intent ("TV-Y7" for a kids intent). "" = don't assert.
	ExpectCeiling string
	// ForbidRatingsAbove, if set, asserts NO grounded item carries a rating above
	// this on the ladder — the safety property ("a kids intent must never surface an
	// adult title in the lineup"). Requires the catalog to carry ratings.
	ForbidRatingsAbove string
	// ForbidGenres / ForbidTitleTerms are deterministic negative constraints.
	// Matching is case-insensitive; any grounded pick that carries one fails.
	ForbidGenres     []string
	ForbidTitleTerms []string
	// ExpectEraWithin, if set (from,to), asserts every grounded item with a known
	// year falls within [from-slack, to+slack]. 0,0 = don't assert.
	ExpectEraFrom, ExpectEraTo int
	// MinThemeFit: the deterministic themeFit score must be at least this (the
	// proposal is actually on-theme, not just non-empty). 0 = don't assert.
	MinThemeFit float64
	// ExpectSeasonalMode, if set, is the seasonal mode the intent implies.
	ExpectSeasonalMode     string
	ExpectSeasonalHolidays []string
	// ExpectOrdering pins the programming texture implied by the request, such as
	// curated single-series reruns versus an explicit chronological binge.
	ExpectOrdering string

	// --- judge rubric (subjective, scored by the judge model 0..1) ---

	// JudgeRubric describes what a GOOD proposal for this intent looks like, for the
	// judge model to grade against. Empty = skip the judge for this case.
	JudgeRubric string
	// MinJudgeScore: the judge's 0..1 score must be at least this to pass (only when
	// the judge runs). 0 = judge is advisory (scored + reported, never fails).
	MinJudgeScore float64
	// Relevance and serendipity are separate because a merely surprising lineup
	// can be nonsense, while a perfectly literal lineup can feel stale. Each floor
	// applies only when the judge runs; 0 leaves that dimension advisory.
	MinRelevanceScore   float64
	MinSerendipityScore float64
}

// Intent mirrors suggest.Intent's fields (kept local so the corpus is pure data;
// the harness maps it onto suggest.Intent).
type Intent struct {
	Description string
	Era         string
	Tone        string
	RuntimeTgt  int
	MustInclude []string
	MustExclude []string
	MaxAcquire  int
}

// Corpus is the durable set of evaluation cases. It spans the axes the suggester +
// ChannelPolicy must handle: themed discovery, kids-safety extraction, era binding,
// seasonality, must-include grounding, and an adversarial "unsatisfiable" intent.
// Add a case here to lock in a behavior; the same cases drive the live smoke.
var Corpus = withProductionStructuralBounds([]Case{
	{
		Name:       "template_saturday_cartoons",
		TemplateID: "saturday-cartoons",
		Intent: Intent{Description: "Saturday-morning cartoons like I watched as a kid — bright, silly, kid-safe",
			Era: "1990s", Tone: "playful"},
		// The headline safety case: a kids intent must extract a kids ceiling AND the
		// lineup must contain nothing above it (fail-closed audience, end to end).
		MinLineup:          1,
		ExpectCeiling:      "TV-Y7",
		ForbidRatingsAbove: "TV-Y7",
		ExpectEraFrom:      1990, ExpectEraTo: 1999,
		MinThemeFit: 0.5,
		JudgeRubric: "A good result is a set of genuine 1990s animated kids shows/movies. " +
			"Penalize any adult-oriented, violent, or clearly non-kids title; penalize non-90s titles.",
		MinJudgeScore:     0.6,
		MinRelevanceScore: 0.65, MinSerendipityScore: 0.35,
	},
	{
		Name:       "template_cozy_mystery",
		TemplateID: "cozy-mystery",
		Intent: Intent{Description: "Gentle small-town mysteries for a rainy evening — nothing gruesome",
			Tone: "cozy"},
		MinGrounded:  1,
		MinThemeFit:  0.5,
		ForbidGenres: []string{"Horror"},
		JudgeRubric: "A good result is gentle, cozy mystery programming. Penalize graphic violence, gore, horror, " +
			"grim serial-killer stories, or titles that are not mysteries.",
		MinJudgeScore:     0.65,
		MinRelevanceScore: 0.7, MinSerendipityScore: 0.35,
	},
	{
		Name:       "template_late_night_scifi",
		TemplateID: "late-night-scifi",
		Intent: Intent{Description: "Weird, atmospheric science fiction for after midnight",
			Tone: "moody"},
		MinLineup:   1,
		MinThemeFit: 0.5,
		JudgeRubric: "A good result is atmospheric, strange, or cerebral science fiction suitable for late-night viewing. " +
			"Penalize ordinary action films and titles with no science-fiction connection.",
		MinJudgeScore:     0.6,
		MinRelevanceScore: 0.65, MinSerendipityScore: 0.4,
	},
	{
		Name:       "template_action_marathon",
		TemplateID: "action-marathon",
		Intent: Intent{Description: "Back-to-back action movies, high energy, keep it PG-13",
			Tone: "high energy"},
		MinLineup:           1,
		MinMovies:           1,
		AllowedMediaTypes:   []provision.MediaType{provision.Movie},
		ForbidRatingsAbove:  "PG-13",
		MinThemeFit:         0.5,
		JudgeRubric:         "A good result is a fast, high-energy action-movie marathon. Penalize slow dramas, non-action titles, and content inappropriate for a PG-13 ceiling.",
		MinJudgeScore:       0.6,
		MinRelevanceScore:   0.65,
		MinSerendipityScore: 0.35,
	},
	{
		Name:              "explicit_negative_constraint",
		Intent:            Intent{Description: "gentle mysteries with no horror; exclude Saw", MustExclude: []string{"Saw"}},
		MinGrounded:       1,
		ForbidGenres:      []string{"Horror"},
		TitleEvidence:     TitleEvidenceGrounded,
		ForbidTitles:      []string{"Saw"},
		ForbidTitleTerms:  []string{"Saw"},
		ForbidKeys:        []provision.Key{"movie:tmdb:176"},
		JudgeRubric:       "A good result is a gentle mystery lineup with no horror, gore, or Saw title.",
		MinJudgeScore:     0.65,
		MinRelevanceScore: 0.7,
	},
	{
		Name:   "must_include_grounding",
		Intent: Intent{Description: "sci-fi movies", MustInclude: []string{"The Matrix"}},
		// Grounding must surface The Matrix (a real, well-known title) — it's the
		// canary that the catalog + grounding loop actually find named titles.
		MinLineup:     1,
		RequireKeys:   []provision.Key{"movie:tmdb:603"},
		TitleEvidence: TitleEvidenceGrounded,
		RequireTitles: []string{"The Matrix"},
		JudgeRubric:   "A good result is science-fiction films and MUST include The Matrix (it was explicitly requested).",
	},
	{
		Name: "classic_single_series_curated",
		Intent: Intent{
			Description: classicSimpsonsViewerRequest,
			MustInclude: []string{"The Simpsons"}, MaxAcquire: 1,
		},
		MinGrounded:    1,
		ExpectOrdering: "syndication",
	},
	{
		Name:   "family_movie_night",
		Intent: Intent{Description: "family movie night, nothing too scary or mature"},
		// A softer safety case: "family" implies a PG-ish ceiling; assert the lineup
		// carries nothing above TV-14 even if the exact ceiling varies.
		MinGrounded:         1,
		ForbidRatingsAbove:  "TV-14",
		JudgeRubric:         "A good result is broadly family-appropriate films. Penalize R-rated / TV-MA titles.",
		MinJudgeScore:       0.5,
		MinRelevanceScore:   0.6,
		MinSerendipityScore: 0.3,
	},
	{
		Name: "holiday_christmas_cozy_discovery",
		Intent: Intent{
			Description: "A cozy Christmas movie channel with beloved favorites and a few warm discoveries",
			Tone:        "cozy", MaxAcquire: 4,
		},
		MinAcquisitions:        1,
		ExpectSeasonalMode:     "exclusive",
		ExpectSeasonalHolidays: []string{"christmas"},
		MinThemeFit:            0.55,
		JudgeRubric: "A good result is unmistakably Christmas programming with a coherent cozy tone. " +
			"It should balance recognizable anchors with relevant less-obvious discoveries; random winter films are not serendipity.",
		MinJudgeScore: 0.65, MinRelevanceScore: 0.7, MinSerendipityScore: 0.55,
	},
	{
		Name: "holiday_family_halloween",
		Intent: Intent{
			Description: "Playful Halloween movies for families, spooky but never gruesome",
			Tone:        "playful", MaxAcquire: 3,
		},
		MinLineup:              1,
		ExpectCeiling:          "TV-PG",
		ForbidRatingsAbove:     "TV-PG",
		ExpectSeasonalMode:     "exclusive",
		ExpectSeasonalHolidays: []string{"halloween"},
		JudgeRubric: "A good result is clearly Halloween-themed, playful, and family-safe. " +
			"Reward clever animated or supernatural discoveries; penalize gore, adult horror, and generic family films with no Halloween connection.",
		MinJudgeScore: 0.7, MinRelevanceScore: 0.75, MinSerendipityScore: 0.4,
	},
	{
		Name: "holiday_thanksgiving_comedy",
		Intent: Intent{
			Description: "Thanksgiving comedies about chaotic families, travel disasters, and coming home",
			Tone:        "funny", MaxAcquire: 4,
		},
		MinAcquisitions:        1,
		ExpectSeasonalMode:     "exclusive",
		ExpectSeasonalHolidays: []string{"thanksgiving"},
		JudgeRubric: "A good result fits Thanksgiving through the holiday itself or its family, travel, and homecoming themes. " +
			"Reward apt discoveries beyond the single most famous title; penalize unrelated broad comedies.",
		MinJudgeScore: 0.65, MinRelevanceScore: 0.7, MinSerendipityScore: 0.5,
	},
	{
		Name: "holiday_new_years_eve",
		Intent: Intent{
			Description: "A stylish New Year's Eve channel for the countdown: parties, fresh starts, and midnight",
			Tone:        "celebratory", MaxAcquire: 4,
		},
		MinAcquisitions:        1,
		ExpectSeasonalMode:     "exclusive",
		ExpectSeasonalHolidays: []string{"newyear"},
		JudgeRubric: "A good result has a real New Year's Eve, countdown, midnight-party, or fresh-start connection. " +
			"Reward a varied but coherent mix; penalize movies selected merely because they are glamorous or set in winter.",
		MinJudgeScore: 0.65, MinRelevanceScore: 0.7, MinSerendipityScore: 0.5,
	},
	{
		Name: "holiday_valentines_offbeat_romance",
		Intent: Intent{
			Description: "An offbeat Valentine's Day channel: witty romances and unexpected love stories, not syrupy melodrama",
			Tone:        "witty", MaxAcquire: 4,
		},
		MinAcquisitions:        1,
		ExpectSeasonalMode:     "exclusive",
		ExpectSeasonalHolidays: []string{"valentines"},
		JudgeRubric: "A good result is suitable for Valentine's viewing while honoring the witty, offbeat qualifier. " +
			"Reward surprising but defensible love stories; penalize generic melodrama and novelty with no romantic connection.",
		MinJudgeScore: 0.65, MinRelevanceScore: 0.7, MinSerendipityScore: 0.55,
	},
	{
		Name: "context_rainy_late_night",
		Intent: Intent{
			Description: "It's a rainy late night; build a channel of atmospheric mysteries and quiet thrillers",
			Tone:        "moody", MaxAcquire: 4,
		},
		MinAcquisitions: 1,
		JudgeRubric: "A good result suits both the rainy mood and late-night viewing while remaining a coherent mystery/thriller channel. " +
			"Reward subtle, defensible discoveries; penalize generic blockbusters, bright daytime fare, and randomness mistaken for mood.",
		MinJudgeScore: 0.65, MinRelevanceScore: 0.7, MinSerendipityScore: 0.5,
	},
	{
		Name: "context_sunday_morning_family",
		Intent: Intent{
			Description: "Easy Sunday-morning viewing for the family: gentle nature, travel, and food programs",
			Tone:        "calm", MaxAcquire: 4,
		},
		MinAcquisitions:    1,
		MinDistinctGenres:  2,
		ExpectCeiling:      "TV-PG",
		ForbidRatingsAbove: "TV-PG",
		JudgeRubric: "A good result feels calm, welcoming, and appropriate for shared Sunday-morning viewing, across nature, travel, or food. " +
			"Reward a pleasantly varied set; penalize intense survival shows, adult travelogues, or unrelated family films.",
		MinJudgeScore: 0.65, MinRelevanceScore: 0.7, MinSerendipityScore: 0.45,
	},
	{
		Name: "nonsense_intent_no_fabrication",
		// The adversarial SAFETY case. A nonsense intent — the live eval showed the
		// model latches onto a real word in it ("creatures") and grounds real owned
		// titles, which is NOT a bug: the grounding guarantee is that every returned
		// id is REAL (the catalog surfaced it), never fabricated. So we assert the
		// invariant that actually matters — NO FABRICATION — not "must be empty".
		// (Declining a searchable-but-nonsense intent is a product decision the
		// suggester doesn't make today; asserting emptiness here would be wrong.)
		Intent:        Intent{Description: "movies about the quxzptl migration patterns of nonexistent creatures"},
		NoFabrication: true, // every grounded pick must have a real, resolvable id
	},
})

func withProductionStructuralBounds(cases []Case) []Case {
	bounds := suggest.ProductionBounds()
	for i := range cases {
		cases[i].MaxToolCalls = bounds.MaxToolCalls
		cases[i].MaxCandidatesSurfaced = bounds.MaxCandidatesSurfaced
	}
	return cases
}

// fixtureScheduleCorpus is synthetic evidence used only by hermetic Runner
// contract tests. Live certification derives different cases from a verified
// ScheduleEvidenceSnapshot and must never consume these identities.
var fixtureScheduleCorpus = []Case{
	{
		Name: "schedule_classic_simpsons_highlights",
		Intent: Intent{
			Description: classicSimpsonsViewerRequest,
			MustInclude: []string{"The Simpsons"},
		},
		MinLineup:      1,
		RequireKeys:    []provision.Key{"series:tmdb:456"},
		ExpectOrdering: "syndication",
		RequireScheduledPrograms: []string{
			"series:tmdb:456:s01e02", "series:tmdb:456:s01e04",
			"series:tmdb:456:s01e06", "series:tmdb:456:s01e08",
		},
		ForbidScheduledPrograms: []string{"series:tmdb:456:s01e01"},
	},
	{
		Name: "schedule_movie_franchise_release_order",
		Intent: Intent{
			Description: "Play the owned Indiana Jones movies together in release order",
			MustInclude: []string{"Raiders of the Lost Ark", "Indiana Jones and the Temple of Doom", "Indiana Jones and the Last Crusade"},
		},
		MinLineup:                3,
		MinMovies:                3,
		RequireKeys:              []provision.Key{"movie:tmdb:85", "movie:tmdb:87", "movie:tmdb:89"},
		RequireScheduledPrograms: []string{"movie:tmdb:85", "movie:tmdb:87", "movie:tmdb:89"},
		RequireScheduledSequence: []string{"movie:tmdb:85", "movie:tmdb:87", "movie:tmdb:89"},
	},
	{
		Name: "schedule_owned_simpsons_christmas",
		Intent: Intent{
			Description: simpsonsChristmasViewerRequest,
			MustInclude: []string{"The Simpsons"},
		},
		MinLineup:              1,
		RequireKeys:            []provision.Key{"series:tmdb:456"},
		ExpectSeasonalMode:     "exclusive",
		ExpectSeasonalHolidays: []string{"christmas"},
		RequireScheduledPrograms: []string{
			"series:tmdb:456:s02e02", "series:tmdb:456:s02e03", "series:tmdb:456:s02e04",
		},
		ForbidScheduledPrograms: []string{"series:tmdb:456:s02e01"},
	},
}
