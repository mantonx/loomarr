package suggest

import (
	"cmp"
	"slices"
	"strings"
	"unicode"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/provision"
)

// FeedbackSignal is the ranker's store-independent view of one effective
// explicit household or channel signal. Passive playback never enters this interface.
type FeedbackSignal struct {
	Target provision.Key
	Action FeedbackAction
}

type FeedbackAction string

const (
	FeedbackKeep     FeedbackAction = "keep"
	FeedbackLess     FeedbackAction = "less"
	FeedbackNever    FeedbackAction = "never"
	FeedbackSurprise FeedbackAction = "surprise"
)

type rankedCandidate struct {
	candidate   catalog.Candidate
	key         provision.Key
	relevance   int
	preference  int
	novelty     int
	constraints ConstraintMatches
}

type rankedCandidates struct {
	included []rankedCandidate
	excluded []rankedCandidate
}

// DecisionTrace is bounded, immutable evidence from one original proposal run.
// Callers receive value copies and must not use it as current channel evidence.
type DecisionTrace struct {
	Version       int                 `json:"version"`
	Candidates    []DecisionCandidate `json:"candidates"`
	SurfacedTotal int                 `json:"surfacedTotal"`
	RecordedTotal int                 `json:"recordedTotal"`
	Truncated     bool                `json:"truncated"`
	Terminal      string              `json:"terminal,omitempty"`
}

type DecisionCandidate struct {
	Key         string            `json:"key"`
	Name        string            `json:"name,omitempty"`
	Source      string            `json:"source,omitempty"`
	Ownership   string            `json:"ownership"`
	Rank        RankTuple         `json:"rank,omitempty"`
	Constraints ConstraintMatches `json:"constraints,omitempty"`
	Disposition string            `json:"disposition"`
	Reason      string            `json:"reason"`
}

// ConstraintMatches identifies which safe intent categories contributed at
// least one grounded term to Relevance. It never copies caller-authored terms.
type ConstraintMatches struct {
	Request     bool `json:"request,omitempty"`
	Tone        bool `json:"tone,omitempty"`
	Era         bool `json:"era,omitempty"`
	MustInclude bool `json:"mustInclude,omitempty"`
	MustExclude bool `json:"mustExclude,omitempty"`
	Refine      bool `json:"refine,omitempty"`
}

// RankTuple is the exact lexicographic ordering tuple. It is not a scalar score.
type RankTuple struct {
	Relevance  int    `json:"relevance"`
	Preference int    `json:"preference"`
	Novelty    int    `json:"novelty"`
	TieKey     string `json:"tieKey"`
}

const DecisionTraceVersion = 1
const DecisionTraceMaxCandidates = 64
const rankNoveltyMax = 2

// DecisionTraceMaxTotal bounds all surfaced and recorded facts. Production can
// surface 576 candidates; the remainder is reserved for adjacent decisions.
const DecisionTraceMaxTotal = 1024

const (
	DispositionSelected          = "selected"
	DispositionAlternate         = "alternate"
	DispositionNotSelected       = "not_selected"
	DispositionRefused           = "refused"
	DispositionValidationDropped = "validation_dropped"
	DispositionTerminal          = "terminal"
	ReasonRetrievalEmpty         = "retrieval_empty"
	ReasonSelectionEmpty         = "selection_empty"
	ReasonNever                  = "never"
	ReasonMalformedID            = "malformed_id"
	ReasonNotSurfaced            = "not_surfaced"
	ReasonNoRelevanceEvidence    = "no_relevance_evidence"
	ReasonValidationDropped      = "validation_dropped"
	ReasonAcquisitionCap         = "acquisition_cap"
	ReasonOverCeiling            = "over_ceiling"
	ReasonBudgetExhausted        = "budget_exhausted"
	ReasonNotSelected            = "not_selected"
	TerminalProviderFailure      = "provider_failure"
	TerminalRetrievalFailure     = "retrieval_failure"
	TerminalGenerationFailure    = "generation_failure"
	TerminalMalformedExhausted   = "malformed_exhausted"
)

func (t DecisionTrace) Clone() DecisionTrace {
	t.Candidates = append([]DecisionCandidate(nil), t.Candidates...)
	return t
}

type RankedCandidates struct {
	Candidates []catalog.Candidate
	Trace      DecisionTrace
}

func RankGroundedCandidatesWithTrace(intent string, candidates []catalog.Candidate, signals []FeedbackSignal) RankedCandidates {
	return rankGroundedCandidatesWithTrace(rankQuery{request: intentWordSet(intent)}, candidates, signals)
}

func rankGroundedCandidatesWithTrace(query rankQuery, candidates []catalog.Candidate, signals []FeedbackSignal) RankedCandidates {
	ranked := rankDetailed(query, candidates, signals)
	result := make([]catalog.Candidate, 0, len(ranked.included))
	for _, item := range ranked.included {
		result = append(result, item.candidate)
	}
	recorded := len(ranked.included) + len(ranked.excluded)
	trace := DecisionTrace{Version: DecisionTraceVersion, SurfacedTotal: len(candidates), RecordedTotal: recorded, Candidates: make([]DecisionCandidate, 0, min(recorded, DecisionTraceMaxCandidates))}
	appendDecision := func(item rankedCandidate, disposition, reason string) {
		if len(trace.Candidates) >= DecisionTraceMaxCandidates {
			trace.Truncated = true
			return
		}
		trace.Candidates = append(trace.Candidates, DecisionCandidate{Key: string(item.key), Name: item.candidate.Name, Source: string(item.candidate.Source), Ownership: ownership(item.candidate.InLibrary), Rank: RankTuple{Relevance: item.relevance, Preference: item.preference, Novelty: item.novelty, TieKey: string(item.key)}, Constraints: item.constraints, Disposition: disposition, Reason: reason})
	}
	for _, item := range ranked.included {
		appendDecision(item, DispositionNotSelected, ReasonNotSelected)
	}
	for _, item := range ranked.excluded {
		appendDecision(item, DispositionNotSelected, ReasonNever)
	}
	if len(candidates) == 0 {
		trace.Terminal = ReasonRetrievalEmpty
	}
	return RankedCandidates{Candidates: result, Trace: trace}
}

func ownership(inLibrary bool) string {
	if inLibrary {
		return "library"
	}
	return "acquisition"
}

// RankGroundedCandidates is the pure public discovery test seam required by
// #493. Relevance is the primary sort key, so a taste signal cannot promote an
// unrelated title above a relevant one. Feedback and novelty only order
// candidates within the same relevance band; identity is the final tie-break.
func RankGroundedCandidates(intent string, candidates []catalog.Candidate, signals []FeedbackSignal) []catalog.Candidate {
	return RankGroundedCandidatesWithTrace(intent, candidates, signals).Candidates
}

type rankQuery struct {
	request     map[string]bool
	tone        map[string]bool
	era         map[string]bool
	mustInclude map[string]bool
	mustExclude map[string]bool
	refine      map[string]bool
}

func (q rankQuery) all() map[string]bool {
	all := make(map[string]bool)
	for _, terms := range []map[string]bool{q.request, q.tone, q.era, q.mustInclude, q.mustExclude, q.refine} {
		for term := range terms {
			all[term] = true
		}
	}
	return all
}

func (q rankQuery) match(candidate map[string]bool) ConstraintMatches {
	return ConstraintMatches{
		Request: overlap(q.request, candidate) > 0, Tone: overlap(q.tone, candidate) > 0,
		Era: overlap(q.era, candidate) > 0, MustInclude: overlap(q.mustInclude, candidate) > 0,
		MustExclude: overlap(q.mustExclude, candidate) > 0, Refine: overlap(q.refine, candidate) > 0,
	}
}

func rankDetailed(query rankQuery, candidates []catalog.Candidate, signals []FeedbackSignal) rankedCandidates {
	effective := make(map[provision.Key]FeedbackAction, len(signals))
	lessGenres := map[string]bool{}
	surpriseTargets := map[provision.Key]bool{}
	byKey := make(map[provision.Key]catalog.Candidate, len(candidates))
	for _, candidate := range candidates {
		if key, err := candidate.Key(); err == nil {
			byKey[key] = candidate
		}
	}
	for _, signal := range signals {
		effective[signal.Target] = signal.Action
	}
	for target, action := range effective {
		switch action {
		case FeedbackLess:
			addGenres(lessGenres, byKey[target].Genres)
		case FeedbackSurprise:
			surpriseTargets[target] = true
		}
	}
	result := rankedCandidates{included: make([]rankedCandidate, 0, len(candidates))}
	for _, candidate := range candidates {
		key, err := candidate.Key()
		if err != nil {
			continue
		}
		preference := 0
		switch effective[key] {
		case FeedbackKeep:
			preference += 3
		case FeedbackLess:
			preference -= 4
		}
		for _, genre := range candidate.Genres {
			if lessGenres[normalizeGenre(genre)] && effective[key] != FeedbackSurprise {
				preference--
				break
			}
		}
		novelty := 0
		if !candidate.InLibrary {
			novelty = 1
		}
		relevance, constraints := relevanceForCandidate(query, candidate)
		item := rankedCandidate{candidate: candidate, key: key,
			relevance: relevance, constraints: constraints,
			preference: preference, novelty: novelty}
		if effective[key] == FeedbackNever {
			result.excluded = append(result.excluded, item)
			continue
		}
		result.included = append(result.included, item)
	}
	sortRankedCandidates(result.included)
	if len(surpriseTargets) > 0 {
		result.included = diversifyRankedCandidates(result.included, surpriseTargets)
	}
	slices.SortFunc(result.excluded, func(a, b rankedCandidate) int {
		return cmp.Compare(string(a.key), string(b.key))
	})
	return result
}

func relevanceForCandidate(query rankQuery, candidate catalog.Candidate) (int, ConstraintMatches) {
	candidateTerms := candidateWordSet(candidate)
	return overlap(query.all(), candidateTerms), query.match(candidateTerms)
}

func candidateWordSet(candidate catalog.Candidate) map[string]bool {
	return wordSet(strings.Join([]string{
		candidate.Name,
		candidate.Overview,
		strings.Join(candidate.Genres, " "),
		strings.Join(candidate.Keywords, " "),
	}, " "))
}

var intentStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "article": true, "based": true,
	"build": true, "channel": true, "create": true, "for": true, "from": true,
	"give": true, "like": true, "make": true, "me": true, "of": true,
	"on": true, "please": true, "reference": true, "show": true, "shows": true,
	"the": true, "this": true, "use": true, "using": true, "with": true,
}

// intentWordSet keeps editorial terms while removing the prose and URL syntax
// people use to communicate them. Candidate metadata uses wordSet directly: a
// real title containing "This" must remain searchable even though "use this
// reference" cannot count as evidence that it fits.
func intentWordSet(text string) map[string]bool {
	var prose []string
	for _, field := range strings.Fields(text) {
		lower := strings.ToLower(strings.TrimLeft(field, "([<{\"'"))
		if strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://") {
			continue
		}
		prose = append(prose, field)
	}
	terms := wordSet(strings.Join(prose, " "))
	for stopword := range intentStopwords {
		for normalized := range wordSet(stopword) {
			delete(terms, normalized)
		}
	}
	return terms
}

func sortRankedCandidates(candidates []rankedCandidate) {
	slices.SortFunc(candidates, func(a, b rankedCandidate) int {
		if order := cmp.Compare(b.relevance, a.relevance); order != 0 {
			return order
		}
		if order := cmp.Compare(b.preference, a.preference); order != 0 {
			return order
		}
		if order := cmp.Compare(b.novelty, a.novelty); order != 0 {
			return order
		}
		return cmp.Compare(string(a.key), string(b.key))
	})
}

func diversifyRankedCandidates(candidates []rankedCandidate, surpriseTargets map[provision.Key]bool) []rankedCandidate {
	seenGenres := map[string]bool{}
	for _, candidate := range candidates {
		if surpriseTargets[candidate.key] {
			addGenres(seenGenres, candidate.candidate.Genres)
		}
	}

	out := make([]rankedCandidate, 0, len(candidates))
	for start := 0; start < len(candidates); {
		end := start + 1
		for end < len(candidates) && candidates[end].relevance == candidates[start].relevance && candidates[end].preference == candidates[start].preference {
			end++
		}
		band := append([]rankedCandidate(nil), candidates[start:end]...)
		for len(band) > 0 {
			best := 0
			bestNovelty := diversifiedNovelty(band[0], seenGenres)
			for i := 1; i < len(band); i++ {
				novelty := diversifiedNovelty(band[i], seenGenres)
				if novelty > bestNovelty || (novelty == bestNovelty && band[i].key < band[best].key) {
					best = i
					bestNovelty = novelty
				}
			}
			selected := band[best]
			selected.novelty = bestNovelty
			out = append(out, selected)
			addGenres(seenGenres, selected.candidate.Genres)
			band = append(band[:best], band[best+1:]...)
		}
		start = end
	}
	return out
}

func diversifiedNovelty(candidate rankedCandidate, seenGenres map[string]bool) int {
	for _, genre := range candidate.candidate.Genres {
		normalized := normalizeGenre(genre)
		if normalized != "" && !seenGenres[normalized] {
			return candidate.novelty + 1
		}
	}
	return candidate.novelty
}

func addGenres(dst map[string]bool, genres []string) {
	for _, genre := range genres {
		if normalized := normalizeGenre(genre); normalized != "" {
			dst[normalized] = true
		}
	}
}

func normalizeGenre(genre string) string {
	return strings.ToLower(strings.TrimSpace(genre))
}

func wordSet(text string) map[string]bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := make(map[string]bool, len(words))
	for _, word := range words {
		if strings.HasSuffix(word, "ies") && len(word) > 4 {
			word = strings.TrimSuffix(word, "ies") + "y"
		} else if strings.HasSuffix(word, "s") && len(word) > 3 {
			word = strings.TrimSuffix(word, "s")
		}
		out[word] = true
	}
	return out
}

func overlap(a, b map[string]bool) int {
	n := 0
	for word := range a {
		if b[word] {
			n++
		}
	}
	return n
}
