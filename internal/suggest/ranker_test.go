package suggest

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/provision"
)

func TestRankGroundedCandidatesAppliesExplicitFeedbackDeterministically(t *testing.T) {
	candidates := []catalog.Candidate{
		{MediaType: provision.Movie, TMDBID: 1, Name: "Obvious Space Adventure", Genres: []string{"Science Fiction"}, InLibrary: true},
		{MediaType: provision.Movie, TMDBID: 2, Name: "Quiet Space Discovery", Genres: []string{"Science Fiction"}},
		{MediaType: provision.Movie, TMDBID: 3, Name: "Forbidden Space Film", Genres: []string{"Science Fiction"}},
		{MediaType: provision.Movie, TMDBID: 4, Name: "Grounded Mystery", Genres: []string{"Mystery"}},
	}
	signals := []FeedbackSignal{
		{Target: "movie:tmdb:1", Action: FeedbackLess},
		{Target: "movie:tmdb:3", Action: FeedbackNever},
		{Target: "movie:tmdb:2", Action: FeedbackSurprise},
	}
	first := RankGroundedCandidates("space discoveries", candidates, signals)
	second := RankGroundedCandidates("space discoveries", candidates, signals)
	if !slices.EqualFunc(first, second, func(a, b catalog.Candidate) bool { return a.TMDBID == b.TMDBID }) {
		t.Fatalf("identical inputs produced different ranking: %+v / %+v", first, second)
	}
	var ids []int
	for _, candidate := range first {
		ids = append(ids, candidate.TMDBID)
	}
	if slices.Contains(ids, 3) {
		t.Fatalf("never target survived ranking: %v", ids)
	}
	if len(ids) < 2 || ids[0] != 2 || ids[1] != 1 {
		t.Fatalf("feedback ranking = %v, want surprise discovery before demoted related anchor", ids)
	}
	trace := RankGroundedCandidatesWithTrace("space discoveries", candidates, signals).Trace
	foundNever := false
	for _, decision := range trace.Candidates {
		if decision.Key == "movie:tmdb:3" {
			foundNever = decision.Disposition == DispositionNotSelected && decision.Reason == ReasonNever
		}
	}
	if !foundNever || trace.RecordedTotal != len(candidates) || trace.Terminal != "" {
		t.Fatalf("never exclusion was not preserved as a bounded decision: %+v", trace)
	}
}

func TestRankGroundedCandidatesSurpriseDiversifiesWithinRelevanceBand(t *testing.T) {
	candidates := []catalog.Candidate{
		{MediaType: provision.Movie, TMDBID: 100, Name: "Space Anchor", Genres: []string{"Science Fiction"}, InLibrary: true},
		{MediaType: provision.Movie, TMDBID: 200, Name: "Space Familiar", Genres: []string{"Science Fiction"}},
		{MediaType: provision.Movie, TMDBID: 300, Name: "Space Western", Genres: []string{"Western"}},
		{MediaType: provision.Movie, TMDBID: 400, Name: "Unrelated Western", Genres: []string{"Western"}},
	}
	baseline := candidateIDs(RankGroundedCandidates("space", candidates, nil))
	if baseline[0] != 200 {
		t.Fatalf("baseline = %v, fixture must put the familiar canonical tie first", baseline)
	}

	signals := []FeedbackSignal{{Target: "movie:tmdb:100", Action: FeedbackSurprise}}
	ranked := RankGroundedCandidatesWithTrace("space", candidates, signals)
	got := candidateIDs(ranked.Candidates)
	if got[0] != 300 || got[1] != 200 || got[2] != 100 {
		t.Fatalf("surprise ranking = %v, want diverse neighbor before familiar and marked titles", got)
	}
	if got[3] != 400 {
		t.Fatalf("surprise ranking = %v, irrelevant diversity crossed the relevance floor", got)
	}
	if ranked.Trace.Candidates[0].Rank.Novelty != rankNoveltyMax {
		t.Fatalf("diverse candidate trace = %+v, want bounded novelty/diversity term", ranked.Trace.Candidates[0].Rank)
	}
	if err := ValidateDecisionTrace(ranked.Trace); err != nil {
		t.Fatalf("surprise trace rejected by shared validator: %v", err)
	}
}

func TestRankGroundedCandidatesLessDemotesGroundedGenreNeighbors(t *testing.T) {
	candidates := []catalog.Candidate{
		{MediaType: provision.Movie, TMDBID: 100, Name: "Night Anchor", Genres: []string{"Science Fiction"}},
		{MediaType: provision.Movie, TMDBID: 200, Name: "Night Neighbor", Genres: []string{"Science Fiction"}},
		{MediaType: provision.Movie, TMDBID: 300, Name: "Night Mystery", Genres: []string{"Mystery"}},
	}
	got := candidateIDs(RankGroundedCandidates("night", candidates,
		[]FeedbackSignal{{Target: "movie:tmdb:100", Action: FeedbackLess}}))
	if !slices.Equal(got, []int{300, 200, 100}) {
		t.Fatalf("less ranking = %v, want unrelated, related demotion, exact demotion", got)
	}
}

func TestRankGroundedCandidatesProducesByteIdenticalResult(t *testing.T) {
	candidates := []catalog.Candidate{
		{MediaType: provision.Movie, TMDBID: 100, Name: "Space Anchor", Genres: []string{"Science Fiction"}, InLibrary: true},
		{MediaType: provision.Movie, TMDBID: 200, Name: "Space Western", Genres: []string{"Western"}},
		{MediaType: provision.Movie, TMDBID: 300, Name: "Space Mystery", Genres: []string{"Mystery"}},
	}
	signals := []FeedbackSignal{{Target: "movie:tmdb:100", Action: FeedbackSurprise}}
	want, err := json.Marshal(RankGroundedCandidatesWithTrace("space", candidates, signals))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		input := append([]catalog.Candidate(nil), candidates...)
		if i%2 == 1 {
			slices.Reverse(input)
		}
		got, err := json.Marshal(RankGroundedCandidatesWithTrace("space", input, signals))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("run %d was not byte-identical:\n%s\n%s", i, want, got)
		}
	}
}

func candidateIDs(candidates []catalog.Candidate) []int {
	ids := make([]int, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.TMDBID)
	}
	return ids
}

func TestRankGroundedCandidatesWithTracePublishesExactLexicographicTupleAndBound(t *testing.T) {
	candidates := make([]catalog.Candidate, 0, DecisionTraceMaxCandidates+1)
	for i := 0; i < DecisionTraceMaxCandidates+1; i++ {
		candidates = append(candidates, catalog.Candidate{MediaType: provision.Movie, TMDBID: i + 1, Name: "same", Genres: []string{"same"}})
	}
	got := RankGroundedCandidatesWithTrace("same", candidates, nil)
	if !got.Trace.Truncated || got.Trace.SurfacedTotal != DecisionTraceMaxCandidates+1 || len(got.Trace.Candidates) != DecisionTraceMaxCandidates {
		t.Fatalf("trace bounds = %+v", got.Trace)
	}
	if err := ValidateDecisionTrace(got.Trace); err != nil {
		t.Fatalf("ranker emitted trace rejected by shared validator: %v", err)
	}
	for i, item := range got.Trace.Candidates {
		if item.Rank.TieKey != item.Key || item.Rank.Relevance != 1 || item.Rank.Preference != 0 || item.Rank.Novelty != 1 {
			t.Fatalf("trace[%d] = %+v; tuple must be independently reconstructable", i, item)
		}
		if i > 0 && got.Trace.Candidates[i-1].Rank.TieKey >= item.Rank.TieKey {
			t.Fatalf("tie keys not stable: %+v", got.Trace.Candidates)
		}
	}
}

func TestRankGroundedCandidatesWithTraceClassifiesRetrievalEmpty(t *testing.T) {
	got := RankGroundedCandidatesWithTrace("nothing", nil, nil)
	if got.Trace.Version != DecisionTraceVersion || got.Trace.Terminal != ReasonRetrievalEmpty || got.Trace.SurfacedTotal != 0 || got.Trace.RecordedTotal != 0 {
		t.Fatalf("empty retrieval trace = %+v", got.Trace)
	}
}

func TestDecisionRankQueryRecordsMatchedConstraintCategoriesWithoutRawTerms(t *testing.T) {
	intent := Intent{
		Description: "science fiction", Tone: "energetic", Era: "1990s",
		MustInclude: []string{"matrix"}, MustExclude: []string{"comedy"}, RefineText: "more sequels",
	}
	candidate := catalog.Candidate{
		MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix sequel",
		Overview: "An energetic 1990s science fiction story with more sequels",
	}
	got := rankGroundedCandidatesWithTrace(decisionRankQuery(intent), []catalog.Candidate{candidate}, nil).Trace.Candidates[0]
	want := ConstraintMatches{Request: true, Tone: true, Era: true, MustInclude: true, Refine: true}
	if got.Constraints != want || got.Rank.Relevance != 7 {
		t.Fatalf("constraint evidence = %+v relevance=%d, want %+v relevance=7", got.Constraints, got.Rank.Relevance, want)
	}
}

func TestDecisionRankQueryDoesNotTreatRequestScaffoldingAsEvidence(t *testing.T) {
	intent := Intent{Description: "Please build a channel like this reference"}
	candidate := catalog.Candidate{
		MediaType: provision.Movie, TMDBID: 604, Name: "Unrelated Mechanics",
		Overview: "They build this machine for a difficult job.",
	}

	got := rankGroundedCandidatesWithTrace(decisionRankQuery(intent), []catalog.Candidate{candidate}, nil).Trace.Candidates[0]
	if got.Rank.Relevance != 0 || got.Constraints.Request {
		t.Fatalf("request scaffolding became relevance evidence: terms=%v decision=%+v", decisionRankQuery(intent).request, got)
	}
}

func TestMembershipEvidenceGateTargetsNamedSetsWithoutHardcodingOneBlock(t *testing.T) {
	for _, description := range []string{
		"make a channel for FFS like the 90s",
		"shows from NBN",
		"the XYZ programming block",
		"lineup from Friday Family Showcase",
	} {
		if !requiresMembershipEvidence(Intent{Description: description}) {
			t.Fatalf("named-set request was not gated: %q", description)
		}
	}
	for _, description := range []string{
		"UFO movies",
		"feel-good sci-fi",
		"1990s family comedies",
	} {
		if requiresMembershipEvidence(Intent{Description: description}) {
			t.Fatalf("ordinary theme request was over-gated: %q", description)
		}
	}
}

func TestMergeDecisionTracePreservesPreTruncationTotalsAndLatestDuplicateFacts(t *testing.T) {
	dst := DecisionTrace{Version: DecisionTraceVersion, SurfacedTotal: 1, RecordedTotal: 1,
		Candidates: []DecisionCandidate{{Key: "movie:tmdb:603", Source: string(catalog.ScopeAdjacent)}}}
	src := DecisionTrace{Version: DecisionTraceVersion, SurfacedTotal: 70, RecordedTotal: 70, Truncated: true,
		Candidates: []DecisionCandidate{
			{Key: "movie:tmdb:603", Source: string(catalog.ScopeAll)},
			{Key: "movie:tmdb:604", Source: string(catalog.ScopeAll)},
		}}
	mergeDecisionTrace(&dst, &src)
	if dst.SurfacedTotal != 71 || dst.RecordedTotal != 71 || !dst.Truncated || len(dst.Candidates) != 2 {
		t.Fatalf("merged bounds = %+v", dst)
	}
	if dst.Candidates[0].Source != string(catalog.ScopeAll) {
		t.Fatalf("latest decision facts did not replace adjacency facts: %+v", dst.Candidates[0])
	}

	full := DecisionTrace{Version: DecisionTraceVersion, RecordedTotal: DecisionTraceMaxCandidates,
		Candidates: make([]DecisionCandidate, DecisionTraceMaxCandidates)}
	traceDecision(&full, DecisionCandidate{Disposition: DispositionValidationDropped, Reason: ReasonMalformedID})
	if full.RecordedTotal != DecisionTraceMaxCandidates+1 || !full.Truncated {
		t.Fatalf("truncated decision total = %+v", full)
	}
}
