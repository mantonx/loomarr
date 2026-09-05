//go:build eval

package eval

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/suggest"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestRunnerSendsGroundedPolicyAndStructuralEvidenceToJudge(t *testing.T) {
	proposal := suggest.Proposal{
		Lineup: []suggest.ProposalItem{{
			MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix", Year: 1999,
			InLibrary: true, LibraryItemID: "library-matrix", Source: "library-search",
			Rationale: "Grounded cyberpunk anchor", Confidence: 0.93,
			Genres: []string{"Action", "Science Fiction"}, OfficialRating: "R",
		}},
		Policy: schedule.ChannelPolicy{ProposalPolicy: schedule.ProposalPolicy{
			Scope: schedule.ScopePolicy{Era: &schedule.Range{From: 1980, To: 1999}},
			Audience: schedule.AudiencePolicy{
				Ceiling: schedule.Rating("TV-14"), Unrated: schedule.UnratedExclude,
			},
			Ordering: schedule.OrderSyndication,
			Seasonal: schedule.SeasonalPolicy{
				Mode: schedule.SeasonalExclusive, Holidays: []string{"halloween"},
			},
		}},
	}
	observation := Observation{
		ToolCalls: 7, TitleCalls: 1, GenreCalls: 1, KeywordCalls: 1,
		NetworkCalls: 1, CastCalls: 1, CreatorCalls: 1, PeopleCalls: 1,
		CandidatesSurfaced: 17, GroundingStage: "grounded",
	}
	judge := newSemanticRecordingJudge(func(observed JudgeEvidence) error {
		if observed.Request != "late-90s cyberpunk movies" ||
			observed.Rubric != "Grounded, era-accurate science fiction" {
			return fmt.Errorf("request/rubric changed")
		}
		if len(observed.Lineup) != 1 || len(observed.Acquisitions) != 0 {
			return fmt.Errorf("ownership sets = %d/%d", len(observed.Lineup), len(observed.Acquisitions))
		}
		title := observed.Lineup[0]
		if title.Key != "movie:tmdb:603" || title.Name != "The Matrix" || title.Year != 1999 ||
			title.Ownership != JudgeOwnershipLibrary || title.Source != "library-search" ||
			title.Rationale != "Grounded cyberpunk anchor" || title.Confidence == nil || *title.Confidence != 0.93 ||
			!slices.Equal(title.Genres, []string{"Action", "Science Fiction"}) || title.Rating != "R" {
			return fmt.Errorf("grounded title facts changed: %+v", title)
		}
		policy := observed.Policy
		if policy.Scope.Era == nil || *policy.Scope.Era != (schedule.Range{From: 1980, To: 1999}) ||
			policy.Audience.Ceiling != "TV-14" || policy.Audience.Unrated != schedule.UnratedExclude ||
			policy.Ordering != schedule.OrderSyndication || policy.Seasonal.Mode != schedule.SeasonalExclusive ||
			!slices.Equal(policy.Seasonal.Holidays, []string{"halloween"}) {
			return fmt.Errorf("policy facts changed: %+v", policy)
		}
		if observed.Observation.ToolCalls != observation.ToolCalls ||
			observed.Observation.TitleCalls != observation.TitleCalls ||
			observed.Observation.GenreCalls != observation.GenreCalls ||
			observed.Observation.KeywordCalls != observation.KeywordCalls ||
			observed.Observation.NetworkCalls != observation.NetworkCalls ||
			observed.Observation.CastCalls != observation.CastCalls ||
			observed.Observation.CreatorCalls != observation.CreatorCalls ||
			observed.Observation.PeopleCalls != observation.PeopleCalls ||
			observed.Observation.CandidatesSurfaced != observation.CandidatesSurfaced ||
			observed.Observation.GroundingStage != observation.GroundingStage {
			return fmt.Errorf("structural observation changed: %+v", observed.Observation)
		}
		return nil
	})
	caseUnderTest := Case{
		Intent:        Intent{Description: "late-90s cyberpunk movies"},
		JudgeRubric:   "Grounded, era-accurate science fiction",
		MinJudgeScore: 0.8,
	}
	card := NewRunner(scriptedGenerator{proposal: proposal}, RunnerConfig{}).
		WithObserver(&scriptedObserver{value: observation}).
		WithJudge(judge).
		Run(context.Background(), []Case{caseUnderTest})
	if !card.Certified || judge.CallCount() != 1 {
		t.Fatalf("judge did not observe required grounded/policy/structural evidence: result=%+v calls=%d errors=%v",
			card.Results[0], judge.CallCount(), judge.ValidationErrors())
	}
}

func TestRunnerKeepsAcquisitionOwnershipDistinctAtJudge(t *testing.T) {
	proposal := suggest.Proposal{
		Lineup: []suggest.ProposalItem{{
			MediaType: provision.Movie, TMDBID: 603, Name: "Library title", InLibrary: true,
		}},
		Acquisitions: []suggest.ProposalItem{{
			MediaType: provision.Movie, TMDBID: 550, Name: "Acquisition title", InLibrary: false,
		}},
	}
	judge := newSemanticRecordingJudge(func(observed JudgeEvidence) error {
		if len(observed.Lineup) != 1 || len(observed.Acquisitions) != 1 {
			return fmt.Errorf("ownership sets = %d/%d", len(observed.Lineup), len(observed.Acquisitions))
		}
		if lineup := observed.Lineup[0]; lineup.Key != "movie:tmdb:603" ||
			lineup.Name != "Library title" || lineup.Ownership != JudgeOwnershipLibrary {
			return fmt.Errorf("lineup ownership changed: %+v", lineup)
		}
		if acquisition := observed.Acquisitions[0]; acquisition.Key != "movie:tmdb:550" ||
			acquisition.Name != "Acquisition title" || acquisition.Ownership != JudgeOwnershipAcquisition {
			return fmt.Errorf("acquisition ownership changed: %+v", acquisition)
		}
		return nil
	})
	card := NewRunner(scriptedGenerator{proposal: proposal}, RunnerConfig{}).
		WithJudge(judge).
		Run(context.Background(), []Case{{
			Name: "ownership", Intent: Intent{Description: "paired titles"},
			JudgeRubric: "Respect ownership", MinJudgeScore: 0.8,
		}})
	if !card.Certified || judge.CallCount() != 1 {
		t.Fatalf("judge did not observe independent ownership: result=%+v calls=%d errors=%v",
			card.Results[0], judge.CallCount(), judge.ValidationErrors())
	}
}

func TestRunnerGivesJudgeScheduleMaterializerEvidenceNotProposalTitles(t *testing.T) {
	proposal := suggest.Proposal{Lineup: []suggest.ProposalItem{{
		MediaType: provision.Series, TMDBID: 456, LibraryItemID: "series-simpsons",
		Name: "PROPOSAL TITLE IS NOT SCHEDULE EVIDENCE", InLibrary: true,
	}}}
	judge := newSemanticRecordingJudge(func(observed JudgeEvidence) error {
		if len(observed.Lineup) != 1 || observed.Lineup[0].Name != proposal.Lineup[0].Name {
			return fmt.Errorf("grounded proposal identity changed: %+v", observed.Lineup)
		}
		if len(observed.ScheduledPrograms) != 1 {
			return fmt.Errorf("scheduled sample size = %d", len(observed.ScheduledPrograms))
		}
		program := observed.ScheduledPrograms[0]
		if program.Identity != "series:tmdb:456:s01e01" ||
			program.Title != "Simpsons Roasting on an Open Fire" || program.Title == proposal.Lineup[0].Name ||
			program.Season != 1 || program.Episode != 1 || program.Year != 1989 || program.Rating != "TV-PG" ||
			program.CommunityRating != 8.1 || program.Overview != "The family has a difficult Christmas." ||
			!slices.Equal(program.Tags, []string{"christmas", "holiday"}) {
			return fmt.Errorf("materialized episode facts changed: %+v", program)
		}
		return nil
	})
	runner := NewRunner(scriptedGenerator{proposal: proposal}, RunnerConfig{}).
		WithMaterializer(NewFixtureScheduleMaterializer(map[provision.Key]FixtureTitle{
			"series:tmdb:456": {
				LibraryItemID: "series-simpsons", Episodes: []schedule.ResolvedProgram{{
					LibraryItemID: "episode-s01e01", Title: "Simpsons Roasting on an Open Fire",
					DurationMs: 23 * 60 * 1000, Season: 1, Episode: 1, Year: 1989,
					OfficialRating: "TV-PG", CommunityRating: 8.1,
					Overview: "The family has a difficult Christmas.", Tags: []string{"christmas", "holiday"},
				}},
			},
		})).
		WithJudge(judge)

	card := runner.Run(context.Background(), []Case{{
		Name:                     "materialized_judge_evidence",
		Intent:                   Intent{Description: "science-fiction movies"},
		JudgeRubric:              "Judge the programs that will actually air",
		MinJudgeScore:            0.8,
		RequireScheduledPrograms: []string{"series:tmdb:456:s01e01"},
	}})
	if !card.Certified || judge.CallCount() != 1 {
		t.Fatalf("Runner.Run failed: results=%+v judge calls=%d errors=%v",
			card.Results, judge.CallCount(), judge.ValidationErrors())
	}
}

func TestRunnerBoundsEveryNamedJudgeEvidenceDimension(t *testing.T) {
	lineup := make([]suggest.ProposalItem, JudgeMaxTitlesPerOwnership+1)
	acquisitions := make([]suggest.ProposalItem, JudgeMaxTitlesPerOwnership+1)
	fixtures := make(map[provision.Key]FixtureTitle, len(lineup))
	for i := range lineup {
		key := provision.Key(fmt.Sprintf("movie:tmdb:%d", 1000+i))
		lineup[i] = suggest.ProposalItem{MediaType: provision.Movie, TMDBID: 1000 + i,
			Name: fmt.Sprintf("lineup-%02d", i), InLibrary: true, LibraryItemID: fmt.Sprintf("library-%02d", i)}
		fixtures[key] = FixtureTitle{LibraryItemID: fmt.Sprintf("library-%02d", i), DurationMs: 90 * 60 * 1000}
		acquisitions[i] = suggest.ProposalItem{
			MediaType: provision.Movie, TMDBID: 2000 + i,
			Name: fmt.Sprintf("acquisition-%02d", i),
		}
	}
	lineup[0].MediaType = provision.Series
	lineup[0].TMDBID = 456
	lineup[0].LibraryItemID = "series-library"
	delete(fixtures, "movie:tmdb:1000")
	episodes := make([]schedule.ResolvedProgram, JudgeMaxScheduledPrograms+1)
	for i := range episodes {
		episodes[i] = schedule.ResolvedProgram{
			LibraryItemID: fmt.Sprintf("episode-%02d", i+1), Title: fmt.Sprintf("episode-%02d", i+1),
			DurationMs: 22 * 60 * 1000, Season: 1, Episode: i + 1,
		}
	}
	episodes[JudgeMaxScheduledPrograms].Title = "SCHEDULE_BEYOND_CAP"
	fixtures["series:tmdb:456"] = FixtureTitle{LibraryItemID: "series-library", Episodes: episodes}
	lineup[JudgeMaxTitlesPerOwnership].Name = "LINEUP_BEYOND_CAP"
	acquisitions[JudgeMaxTitlesPerOwnership].Name = "ACQUISITION_BEYOND_CAP"
	lineup[0].Rationale = strings.Repeat("x", JudgeMaxTextRunes) + "TEXT_BEYOND_CAP"
	lineup[0].Genres = make([]string, JudgeMaxGenresPerItem+1)
	for i := range lineup[0].Genres {
		lineup[0].Genres[i] = fmt.Sprintf("genre-%02d", i)
	}
	lineup[0].Genres[JudgeMaxGenresPerItem] = "GENRE_BEYOND_CAP"

	collections := make([]string, JudgeMaxPolicyValues+1)
	for i := range collections {
		collections[i] = fmt.Sprintf("collection-%02d", i)
	}
	collections[JudgeMaxPolicyValues] = "POLICY_BEYOND_CAP"
	proposal := suggest.Proposal{
		Lineup: lineup, Acquisitions: acquisitions,
		Policy: schedule.ChannelPolicy{ProposalPolicy: schedule.ProposalPolicy{
			Scope: schedule.ScopePolicy{Collections: collections},
		}},
	}
	judge := newSemanticRecordingJudge(func(observed JudgeEvidence) error {
		if len(observed.Lineup) != JudgeMaxTitlesPerOwnership ||
			len(observed.Acquisitions) != JudgeMaxTitlesPerOwnership ||
			len(observed.ScheduledPrograms) != JudgeMaxScheduledPrograms {
			return fmt.Errorf("bounded evidence sizes = %d/%d/%d", len(observed.Lineup), len(observed.Acquisitions), len(observed.ScheduledPrograms))
		}
		if observed.Lineup[len(observed.Lineup)-1].Name != fmt.Sprintf("lineup-%02d", JudgeMaxTitlesPerOwnership-1) ||
			observed.Acquisitions[len(observed.Acquisitions)-1].Name != fmt.Sprintf("acquisition-%02d", JudgeMaxTitlesPerOwnership-1) {
			return fmt.Errorf("in-cap ownership evidence was dropped")
		}
		first := observed.Lineup[0]
		if first.Rationale != strings.Repeat("x", JudgeMaxTextRunes-1)+"…" ||
			len(first.Genres) != JudgeMaxGenresPerItem || first.Genres[len(first.Genres)-1] != fmt.Sprintf("genre-%02d", JudgeMaxGenresPerItem-1) {
			return fmt.Errorf("text/genre caps changed: %+v", first)
		}
		if len(observed.Policy.Scope.Collections) != JudgeMaxPolicyValues ||
			observed.Policy.Scope.Collections[len(observed.Policy.Scope.Collections)-1] != fmt.Sprintf("collection-%02d", JudgeMaxPolicyValues-1) {
			return fmt.Errorf("policy cap changed: %v", observed.Policy.Scope.Collections)
		}
		lastProgram := observed.ScheduledPrograms[len(observed.ScheduledPrograms)-1]
		if lastProgram.Identity != "series:tmdb:456:s01e24" || lastProgram.Title != "episode-24" {
			return fmt.Errorf("schedule prefix changed: %+v", lastProgram)
		}
		for _, title := range append(slices.Clone(observed.Lineup), observed.Acquisitions...) {
			if title.Name == "LINEUP_BEYOND_CAP" || title.Name == "ACQUISITION_BEYOND_CAP" ||
				slices.Contains(title.Genres, "GENRE_BEYOND_CAP") || title.Rationale == "TEXT_BEYOND_CAP" {
				return fmt.Errorf("out-of-cap title evidence remained: %+v", title)
			}
		}
		if slices.Contains(observed.Policy.Scope.Collections, "POLICY_BEYOND_CAP") ||
			lastProgram.Title == "SCHEDULE_BEYOND_CAP" {
			return fmt.Errorf("out-of-cap policy/schedule evidence remained")
		}
		return nil
	})
	card := NewRunner(scriptedGenerator{proposal: proposal}, RunnerConfig{}).
		WithMaterializer(NewFixtureScheduleMaterializer(fixtures)).
		WithJudge(judge).
		Run(context.Background(), []Case{{
			Name: "bounded_evidence", Intent: Intent{Description: "bounded evidence"},
			JudgeRubric: "Use the bounded facts", MinJudgeScore: 0.8,
			RequireScheduledPrograms: []string{"series:tmdb:456:s01e01"},
		}})
	if !card.Certified || judge.CallCount() != 1 {
		t.Fatalf("judge did not observe every bounded evidence prefix: result=%+v calls=%d errors=%v",
			card.Results[0], judge.CallCount(), judge.ValidationErrors())
	}
}

func TestRunnerFailsOnWrongTypedJudgeEvidenceWithoutRenderer(t *testing.T) {
	proposal := suggest.Proposal{Lineup: []suggest.ProposalItem{{
		MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix", Year: 2000,
	}}}
	judge := newSemanticRecordingJudge(func(evidence JudgeEvidence) error {
		if len(evidence.Lineup) != 1 || evidence.Lineup[0].Year != 1999 {
			return fmt.Errorf("canonical grounded year is missing: %+v", evidence.Lineup)
		}
		return nil
	})
	card := NewRunner(scriptedGenerator{proposal: proposal}, RunnerConfig{}).
		WithJudge(judge).
		Run(context.Background(), []Case{{
			Name: "typed_evidence_rejection", Intent: Intent{Description: "late-90s cyberpunk"},
			JudgeRubric: "Require canonical grounded facts", MinJudgeScore: 0.8,
		}})

	result := card.Results[0]
	if card.Certified || result.FailureStage != FailureStageJudge ||
		result.JudgeNote != semanticJudgeRejected.Reason || judge.CallCount() != 1 {
		t.Fatalf("wrong typed evidence result = %+v judge calls=%d", result, judge.CallCount())
	}
}

func TestJudgeEvidenceCarriesBoundedProposalTraceAndRejectsMismatch(t *testing.T) {
	candidates := make([]suggest.DecisionCandidate, 65)
	for i := range candidates {
		candidates[i] = suggest.DecisionCandidate{Key: fmt.Sprintf("movie:tmdb:%d", i+1), Ownership: "library", Rank: suggest.RankTuple{TieKey: fmt.Sprintf("movie:tmdb:%d", i+1)}, Disposition: suggest.DispositionNotSelected, Reason: suggest.ReasonNotSelected}
	}
	proposal := suggest.Proposal{Trace: suggest.DecisionTrace{Version: suggest.DecisionTraceVersion, SurfacedTotal: 65, RecordedTotal: 65, Truncated: true, Candidates: candidates}}
	evidence, err := NewJudgeEvidence(Case{Intent: Intent{Description: "x"}}, proposal, Observation{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.DecisionTrace.Candidates) != JudgeMaxTraceCandidates || !evidence.DecisionTrace.Truncated {
		t.Fatalf("trace was not bounded: %+v", evidence.DecisionTrace)
	}
	proposal.Trace.Version = suggest.DecisionTraceVersion + 1
	if _, err := NewJudgeEvidence(Case{Intent: Intent{Description: "x"}}, proposal, Observation{}, nil); err == nil {
		t.Fatal("mismatched trace version certified")
	}
}

func TestModelJudgeRejectsTraceMismatchBeforeCallingProvider(t *testing.T) {
	provider := testkit.NewLLM(testkit.FinalResponse(`{"overall":1,"relevance":1,"serendipity":1,"reason":"should not run"}`))
	evidence := JudgeEvidence{
		Rubric:        "grounded evidence only",
		Lineup:        []JudgeTitleEvidence{{Key: "movie:tmdb:603", Name: "The Matrix", Ownership: JudgeOwnershipLibrary}},
		DecisionTrace: suggest.DecisionTrace{Version: suggest.DecisionTraceVersion + 1},
	}
	if _, err := (modelJudge{provider: provider}).Score(context.Background(), evidence); err == nil {
		t.Fatal("model judge accepted a mismatched decision trace")
	}
	if provider.Calls != 0 {
		t.Fatalf("mismatched evidence reached model provider: calls=%d", provider.Calls)
	}
}

func mustJudgeEvidence(t testing.TB, c Case, proposal suggest.Proposal, observation Observation, programs []MaterializedProgram) JudgeEvidence {
	t.Helper()
	evidence, err := NewJudgeEvidence(c, proposal, observation, programs)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}
