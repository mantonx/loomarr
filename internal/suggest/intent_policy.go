package suggest

import (
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/holidayvocab"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/textmatch"
)

// deterministicIntentPolicy is the code-owned interpretation of editorial and
// safety cues. It keeps model-independent policy decisions discoverable as one
// value while the public Suggester interface remains unchanged.
type deterministicIntentPolicy struct {
	episodeSelection        schedule.EpisodeSelection
	explicitAudienceCeiling schedule.Rating
	safetyCeiling           schedule.Rating
	seasonal                schedule.SeasonalPolicy
	sequential              bool
	curated                 bool
	kidsSignal              bool
}

func deriveIntentPolicy(intent Intent) deterministicIntentPolicy {
	return deterministicIntentPolicy{
		episodeSelection:        EpisodeSelectionForIntent(intent),
		explicitAudienceCeiling: intentExplicitAudienceCeiling(intent),
		safetyCeiling:           intentDeterministicSafetyCeiling(intent),
		seasonal:                seasonalPolicyForIntent(intent),
		sequential:              intentRequestsSequential(intent),
		curated:                 intentRequestsCuratedEpisodes(intent),
		kidsSignal:              intentSignalsKids(intent),
	}
}

// multiSeries reports whether a grounded lineup spans more than one DISTINCT series —
// the condition under which syndication (deck-deal intermix) is the sensible default
// ordering rather than sequential (programming-design §5). It counts distinct series by
// provisioning Key (the same identity the scheduler uses for single-series auto-relax and
// the series allowlist), so a re-picked series counts once and two seasons of one show are
// NOT two series. Movies are ignored for the count: a channel is "multi-series" only when
// ≥2 different shows are present. A Key() error (an ungrounded item — shouldn't happen post-
// validation) is skipped defensively rather than counted.
func multiSeries(lineup []ProposalItem) bool {
	seen := map[provision.Key]struct{}{}
	for _, it := range lineup {
		if it.MediaType != provision.Series {
			continue
		}
		k, err := it.Key()
		if err != nil {
			continue
		}
		seen[k] = struct{}{}
		if len(seen) >= 2 {
			return true
		}
	}
	return false
}

func singleSeriesOnly(groups ...[]ProposalItem) bool {
	seen := map[provision.Key]struct{}{}
	for _, group := range groups {
		for _, it := range group {
			if it.MediaType != provision.Series {
				return false
			}
			key, err := it.Key()
			if err != nil {
				continue
			}
			seen[key] = struct{}{}
		}
	}
	return len(seen) == 1
}

func intentRequestsCuratedEpisodes(intent Intent) bool {
	return intentMatchesAnyCue(intent,
		"classic", "classics", "best", "greatest", "favorite", "favorites", "favourite", "favourites",
		"rerun", "reruns", "curated", "highlight", "highlights",
	)
}

func intentRequestsSequential(intent Intent) bool {
	return intentMatchesAnyCue(intent,
		"chronological", "in order", "start to finish", "from the beginning", "episode order", "binge", "marathon",
	)
}

func intentRequestsHolidayEpisodes(intent Intent) bool {
	return intentMatchesAnyCue(intent,
		"holiday episode", "holiday episodes", "holiday special", "holiday specials",
	)
}

func intentMatchesAnyCue(intent Intent, cues ...string) bool {
	hay := affirmativeIntentText(intent)
	for _, cue := range cues {
		if textmatch.ContainsPhrase(hay, cue) {
			return true
		}
	}
	return false
}

// stampEpisodeSelection turns explicit intent into a code-owned per-series
// editorial policy. The model never chooses episode identities. A named holiday
// is more specific than a general highlights cue; explicit narrative ordering
// disables highlights but can still order a holiday-filtered pool sequentially.
// EpisodeSelectionForIntent is the server-owned series policy preview used by
// proposal review and the approval gate. Clients may display it but never choose it.
func EpisodeSelectionForIntent(intent Intent) schedule.EpisodeSelection {
	selection := schedule.EpisodeSelection{Mode: schedule.EpisodeComplete}
	if holidays := namedHolidaysIn(affirmativeIntentText(intent)); len(holidays) > 0 {
		selection = schedule.EpisodeSelection{
			Mode: schedule.EpisodeHoliday, Holidays: holidays,
		}
	} else if intentRequestsHolidayEpisodes(intent) {
		selection.Mode = schedule.EpisodeHoliday
	} else if intentRequestsCuratedEpisodes(intent) && !intentRequestsSequential(intent) {
		selection.Mode = schedule.EpisodeHighlights
	}
	return selection
}

func stampEpisodeSelection(items []ProposalItem, intent Intent) bool {
	selection := deriveIntentPolicy(intent).episodeSelection
	changed := false
	for i := range items {
		grounded := schedule.EpisodeSelection{}
		if items[i].MediaType == provision.Series {
			grounded = selection
		}
		if items[i].EpisodeSelection.Mode != grounded.Mode ||
			!slices.Equal(items[i].EpisodeSelection.Holidays, grounded.Holidays) {
			changed = true
		}
		items[i].EpisodeSelection = grounded
	}
	return changed
}

// eraAdmittingPicks widens a model-proposed year window just far enough to include the
// channel's OWN grounded picks (programming-design §4). It returns the era to persist, never
// nil for a non-empty input.
//
// Caught live: a "Midnight Sci-Fi Horror" proposal carried era.from 1982 AND Alien (1979)
// on its approved lineup, so the §4 enforcer filtered out a title the operator had
// explicitly approved — six on the lineup, four in the guide, nothing naming the other two.
// Extraction and enforcement disagreeing about one proposal is a self-contradiction, and it
// resolves toward the content the operator asked for.
//
// Unlike the audience raise this needs NO bound: a year is a curation choice, never a safety
// property, so there is no era analogue of the kids line to cross. Only picks with a known
// year (>0) participate — an unknown year can't argue for widening anything.
//
// The era still constrains everything NOT picked (backfill, re-curation, filler); only the
// already-approved titles are grandfathered in.
func eraAdmittingPicks(era schedule.Range, groups ...[]ProposalItem) *schedule.Range {
	out := era
	for _, g := range groups {
		for _, it := range g {
			if it.Year <= 0 {
				continue
			}
			// A bound of 0 is "unbounded" to the enforcer, so it never needs widening —
			// stretching it would turn an open end into a closed one and NARROW the era.
			if out.From > 0 && it.Year < out.From {
				out.From = it.Year
			}
			if out.To > 0 && it.Year > out.To {
				out.To = it.Year
			}
		}
	}
	return &out
}

func stricterCeiling(candidate, maximum schedule.Rating) schedule.Rating {
	if maximum == "" {
		return candidate
	}
	candidateRank, candidateOK := candidate.Rank()
	maximumRank, maximumOK := maximum.Rank()
	if !candidateOK || !maximumOK || candidateRank > maximumRank {
		return maximum
	}
	return candidate
}

// intentSignalsKids reports whether the channel intent asks for kids/teen-appropriate
// content — the ONLY case in which a proposed audience ceiling is kept (§4/§8). It scans the
// intent's free text (description/tone/era + must-include terms) for kids/family/teen cues:
// "kids", "family", "cartoons", "all ages", "wholesome", named kids properties, an explicit
// low rating token ("TV-Y", "rated G"), or a kids daypart ("saturday morning"). Deliberately
// broad on the SIGNAL side (a false positive just keeps a ceiling the operator can edit off)
// and silent otherwise — no signal ⇒ adult-default, no ceiling. Case-insensitive substring
// match; a word-boundary check keeps "family" from matching inside an unrelated word.
func intentSignalsKids(intent Intent) bool {
	hay := normalizedIntentText(intent)
	for _, cue := range kidsIntentCues {
		if strings.Contains(hay, cue) {
			return true
		}
	}
	// Explicit low rating tokens the user might type ("rated G", "keep it TV-Y7").
	for _, r := range []string{"tv-y", "tv-g", "rated g", "rated pg"} {
		if strings.Contains(hay, r) {
			return true
		}
	}
	return false
}

// intentExplicitAudienceCeiling extracts only ratings the user actually wrote.
// It deliberately does not infer an adult cap from genre or tone: unqualified
// channels retain the adult-default behavior, while an explicit limit is exact.
func intentExplicitAudienceCeiling(intent Intent) schedule.Rating {
	hay := normalizedIntentText(intent)
	for _, candidate := range []struct {
		cues   []string
		rating string
	}{
		{[]string{"nc-17", "nc17"}, "NC-17"},
		{[]string{"tv-ma"}, "TV-MA"},
		{[]string{"pg-13", "pg13"}, "PG-13"},
		{[]string{"tv-14"}, "TV-14"},
		{[]string{"r-rated", "rated r"}, "R"},
		{[]string{"tv-pg"}, "TV-PG"},
		{[]string{"pg-rated", "rated pg"}, "PG"},
		{[]string{"tv-y7"}, "TV-Y7"},
		{[]string{"tv-y"}, "TV-Y"},
		{[]string{"tv-g"}, "TV-G"},
		{[]string{"g-rated", "rated g"}, "G"},
	} {
		for _, cue := range candidate.cues {
			if strings.Contains(hay, cue) {
				return schedule.NormalizeRating(candidate.rating)
			}
		}
	}
	return ""
}

func normalizedIntentText(intent Intent) string {
	return strings.ToLower(strings.Join([]string{
		intent.Description, intent.Tone, intent.Era, intent.RefineText,
		strings.Join(intent.MustInclude, " "), strings.Join(intent.MustExclude, " "),
	}, " "))
}

func decisionRankQuery(intent Intent) rankQuery {
	request := intentWordSet(intent.Description)
	for term := range intentWordSet(strings.Join(intent.ReferenceTitles, " ")) {
		request[term] = true
	}
	return rankQuery{
		request:     request,
		tone:        intentWordSet(intent.Tone),
		era:         intentWordSet(intent.Era),
		mustInclude: intentWordSet(strings.Join(intent.MustInclude, " ")),
		mustExclude: intentWordSet(strings.Join(intent.MustExclude, " ")),
		refine:      intentWordSet(intent.RefineText),
	}
}

// affirmativeIntentText is the operator's positive editorial request. Negative
// constraints still participate in grounding and ranking, but must never select
// the very episode mode they prohibit.
func affirmativeIntentText(intent Intent) string {
	return strings.ToLower(strings.Join([]string{
		intent.Description, intent.Tone, intent.Era, intent.RefineText,
		strings.Join(intent.MustInclude, " "),
	}, " "))
}

// seasonalPolicyForIntent makes an explicitly named holiday deterministic. A
// holiday channel is exclusive to that holiday; leaving Holidays empty would mean
// every built-in holiday and would make a Christmas request rotate into Halloween.
func seasonalPolicyForIntent(intent Intent) schedule.SeasonalPolicy {
	// A refine can add a timed holiday block to an existing year-round channel.
	// Only treat the refine as a whole-channel identity change when it actually
	// names a channel transformation; ordinary "add Christmas specials" language
	// is represented by grounded holiday rules instead.
	hay := strings.Join([]string{
		intent.Description, intent.Tone, intent.Era, strings.Join(intent.MustInclude, " "),
	}, " ")
	refine := intent.RefineText
	if textmatch.ContainsPhrase(refine, "channel") {
		hay += " " + refine
	}
	holidays := namedHolidaysIn(hay)
	if len(holidays) == 0 {
		return schedule.SeasonalPolicy{}
	}
	return schedule.SeasonalPolicy{Mode: schedule.SeasonalExclusive, Holidays: holidays}
}

func namedHolidaysIn(text string) []string {
	return holidayvocab.MatchIntent(text)
}

// kidsIntentCues are the substrings that mark a kids/teen intent (§4/§8). Lowercased.
// Kept explicit and conservative — each is a phrase a user would only write for a
// child/family audience. "teen"/"teenager" count (a teen ceiling is TV-14, still a
// deliberate guardrail the user asked for); a generic "action"/"drama" does NOT.
var kidsIntentCues = []string{
	"kid", "kids", "child", "children", "family-friendly", "family friendly",
	"for the family", "for families", "cartoon", "all ages", "all-ages", "wholesome",
	"preschool", "toddler", "bluey", "saturday morning", "teen", "teenager", "tween",
	"g-rated", "pg-rated", "clean ", "safe for",
}

func intentDeterministicSafetyCeiling(intent Intent) schedule.Rating {
	hay := normalizedIntentText(intent)
	for _, cue := range []string{
		"kid-safe", "kid safe", "kids-safe", "kids safe", "child-safe", "child safe",
		"safe for kids", "safe for children", "for kids", "for children",
		"young children", "preschool", "toddler", "saturday morning",
	} {
		if strings.Contains(hay, cue) {
			return schedule.NormalizeRating("TV-Y7")
		}
	}
	for _, cue := range []string{
		"family-friendly", "family friendly", "for the family", "for families", "all ages", "all-ages",
	} {
		if strings.Contains(hay, cue) {
			return schedule.NormalizeRating("TV-PG")
		}
	}
	if strings.Contains(hay, "family") {
		for _, safetyCue := range []string{"nothing too", "not too", "never gruesome", "safe", "appropriate"} {
			if strings.Contains(hay, safetyCue) {
				return schedule.NormalizeRating("TV-PG")
			}
		}
	}
	return ""
}

// clampSeasonWindow validates a model-proposed AIRING season window (§8 grounding
// chokepoint). It returns (min, max, true) only for a series with a sane window;
// otherwise (0,0,false) → the pick airs all seasons. Rules: series only (a movie has
// no seasons); at least one bound must be positive (0/0 = "no window", not an empty
// channel); an inverted window (min>max, both set) is nonsense and dropped. A single
// bound is allowed (seasonMin:11 = "11 onward", seasonMax:10 = "through 10"). The
// window only NARROWS a grounded series' expansion — it can never add content.
func clampSeasonWindow(mt provision.MediaType, min, max int) (int, int, bool) {
	if mt != provision.Series {
		return 0, 0, false
	}
	if min < 0 {
		min = 0
	}
	if max < 0 {
		max = 0
	}
	if min == 0 && max == 0 {
		return 0, 0, false // no window proposed
	}
	if min > 0 && max > 0 && min > max {
		return 0, 0, false // inverted → drop (all seasons, never empty)
	}
	return min, max, true
}
