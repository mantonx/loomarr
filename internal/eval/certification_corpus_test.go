//go:build eval

package eval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/catalog"
	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/suggest"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestEmbeddedCertificationCorpusIsFrozenHeldOutAndRepresentative(t *testing.T) {
	corpus, err := LoadEmbeddedCertificationCorpus()
	if err != nil {
		t.Fatal(err)
	}
	if corpus.Version != "planner-certification-v6" {
		t.Fatalf("corpus version = %q, want planner-certification-v6", corpus.Version)
	}
	if corpus.SchemaVersion != 6 {
		t.Fatalf("corpus schema version = %d, want 6", corpus.SchemaVersion)
	}
	if corpus.PromptVersion != suggest.PlannerPromptVersion || corpus.ToolSchemaVersion != suggest.PlannerToolSchemaVersion {
		t.Fatalf("prompt/tool version = %q/%q, want production %q/%q", corpus.PromptVersion, corpus.ToolSchemaVersion, suggest.PlannerPromptVersion, suggest.PlannerToolSchemaVersion)
	}
	if corpus.Split != "certification" {
		t.Fatalf("corpus split = %q, want certification", corpus.Split)
	}
	if len(corpus.Cases) != 28 {
		t.Fatalf("certification scenario families = %d, want 28", len(corpus.Cases))
	}
	if corpus.Fixture.SHA256 == "" {
		t.Fatal("catalog fixture digest is empty")
	}

	seen := make(map[string]bool, len(corpus.Cases))
	axes := make(map[string]bool)
	for _, c := range corpus.Cases {
		if c.ID == "" || seen[c.ID] {
			t.Fatalf("case id %q is blank or duplicated", c.ID)
		}
		seen[c.ID] = true
		if c.Split != corpus.Split {
			t.Fatalf("case %q split = %q, want %q", c.ID, c.Split, corpus.Split)
		}
		if len(c.Variants) != 5 {
			t.Fatalf("case family %q variants = %d, want 5 plus its base Intent", c.ID, len(c.Variants))
		}
		for _, axis := range c.Axes {
			axes[axis] = true
		}
	}
	for _, axis := range []string{
		"tool-routing", "must-include", "must-exclude", "refinement",
		"season-window", "audience-ceiling", "ambiguous", "conflicting",
		"thin-results", "empty-results", "tool-error", "repair-turn",
		"adversarial-fabrication", "network-qualifier", "person-qualifier",
	} {
		if !axes[axis] {
			t.Errorf("certification corpus does not cover %q", axis)
		}
	}
	if slices.Contains(corpus.AllowedTrainingSplits, corpus.Split) {
		t.Fatalf("certification split appears in allowed training splits: %v", corpus.AllowedTrainingSplits)
	}
	if corpus.Thresholds.MinGroundedCompletionRate != 0.95 ||
		corpus.Thresholds.MinCorrectToolOperationRate != 0.90 ||
		corpus.Thresholds.MinSchemaValidityRate != 0.98 ||
		corpus.Thresholds.MinPolicyAccuracyRate != 0.95 ||
		corpus.Thresholds.MinProposalQualityRate != 0.90 ||
		corpus.Thresholds.MinRecoveryRate != 0.80 || corpus.Thresholds.MaxP95ToolCalls != 3 {
		t.Fatalf("certification thresholds drifted: %+v", corpus.Thresholds)
	}
	if corpus.Selection.QualityMargin != 0.02 || validateSelection(corpus.Selection) != nil {
		t.Fatalf("certification selection contract drifted: %+v", corpus.Selection)
	}
}

func TestV6NetworkAndPersonFamiliesPinExactToolRoutesAndEvidence(t *testing.T) {
	cases, err := CertificationCases()
	if err != nil {
		t.Fatal(err)
	}
	wantOperations := map[string]string{
		"network-discovery": "network",
		"cast-discovery":    "cast",
		"creator-discovery": "creator",
	}
	for _, c := range cases {
		if want, ok := wantOperations[c.Name]; ok {
			if c.ExpectedToolOperation != want {
				t.Errorf("case %q operation = %q, want %q", c.Name, c.ExpectedToolOperation, want)
			}
			delete(wantOperations, c.Name)
		}
	}
	if len(wantOperations) != 0 {
		t.Fatalf("missing v6 operation families: %v", wantOperations)
	}

	corpus, err := LoadEmbeddedCertificationCorpus()
	if err != nil {
		t.Fatal(err)
	}
	fixtureBlob, err := certificationFiles.ReadFile(corpus.Fixture.Path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture certificationCatalogFixture
	if err := json.Unmarshal(fixtureBlob, &fixture); err != nil {
		t.Fatal(err)
	}
	wantEvidence := map[string]func(catalog.Candidate) bool{
		"network-discovery": func(c catalog.Candidate) bool { return slices.Equal(c.Networks, []string{"Synthetic Network"}) },
		"cast-discovery":    func(c catalog.Candidate) bool { return slices.Equal(c.Cast, []string{"Synthetic Performer"}) },
		"creator-discovery": func(c catalog.Candidate) bool { return slices.Equal(c.Creators, []string{"Synthetic Filmmaker"}) },
	}
	for _, fixtureCase := range fixture.Cases {
		check, ok := wantEvidence[fixtureCase.ID]
		if !ok {
			continue
		}
		if len(fixtureCase.Responses) != 1 || len(fixtureCase.Responses[0].Candidates) != 1 ||
			!check(fixtureCase.Responses[0].Candidates[0]) {
			t.Errorf("fixture case %q does not pin its resolved evidence: %+v", fixtureCase.ID, fixtureCase.Responses)
		}
		delete(wantEvidence, fixtureCase.ID)
	}
	if len(wantEvidence) != 0 {
		t.Fatalf("missing v6 evidence fixtures: %v", wantEvidence)
	}
}

func TestObservedProviderClassifiesSpecificDiscoveryQualifierRoutes(t *testing.T) {
	observer := &observedProvider{}
	observer.Begin()
	observer.observeToolCalls([]llm.Message{{Role: llm.Assistant, ToolCalls: []llm.ToolCall{
		{Arguments: map[string]any{"media_type": "series", "network": "Synthetic Network"}},
		{Arguments: map[string]any{"media_type": "movie", "cast": []any{"Synthetic Performer"}}},
		{Arguments: map[string]any{"media_type": "movie", "creators": []any{"Synthetic Filmmaker"}}},
		{Arguments: map[string]any{"media_type": "movie", "cast": []any{"Synthetic Performer"}, "creators": []any{"Synthetic Filmmaker"}}},
	}}})

	got := observer.Snapshot(nil)
	if got.ToolCalls != 4 || got.NetworkCalls != 1 || got.CastCalls != 1 || got.CreatorCalls != 1 || got.PeopleCalls != 1 {
		t.Fatalf("specific operation counts = %+v, want one network/cast/creator/combined call", got)
	}
	if got.TitleCalls != 0 || got.GenreCalls != 0 || got.KeywordCalls != 0 {
		t.Fatalf("specific qualifiers leaked into legacy operation counts: %+v", got)
	}
}

func TestCertificationExtensionRejectsUnknownCaseReferences(t *testing.T) {
	corpus := CertificationCorpus{Cases: []CertificationCase{{ID: "known"}}}
	extension := certificationCorpusExtension{
		SchemaVersion: 4, Version: "v4", PromptVersion: "prompt", ToolSchemaVersion: "tool", ScorerVersion: "scorer",
		QualityMetrics:      []string{"proposal_quality"},
		ProposalExpectation: "exact_fixture_candidates_or_declared_abstention",
		Selection: CertificationSelection{QualityMargin: 0.02, Weights: CertificationQualityWeights{
			GroundedCompletion: 0.20, CorrectToolOperation: 0.20, SchemaValidity: 0.10,
			PolicyAccuracy: 0.15, ProposalQuality: 0.25, Recovery: 0.10,
		}},
		RecoveryCases: []string{"unknown"},
	}
	if err := validateCertificationExtension(extension, corpus); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown extension case error = %v", err)
	}
}

func TestOllamaResourceProbeReportsResidentModelRAMAndVRAM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Fatalf("resource probe path = %q, want /api/ps", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"gemma-fixture:9b","size":8589934592,"size_vram":6442450944}]}`))
	}))
	defer server.Close()

	measurement := NewOllamaResourceProbe(server.URL).Measure(context.Background(), ModelIdentity{Model: "gemma-fixture:9b"})
	if measurement.Status != "measured" || measurement.PeakRAMBytes != 2<<30 || measurement.PeakVRAMBytes != 6<<30 {
		t.Fatalf("Ollama resource measurement = %+v", measurement)
	}
}

func TestCertificationScorecardCarriesVersionedContractAndHumanSummary(t *testing.T) {
	config, err := CertificationRunnerConfig(RunnerConfig{
		Profile:   "hermetic-bounded-v1",
		Generator: ModelIdentity{Provider: "ollama", Model: "fixture-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	card := NewRunner(scriptedGenerator{}, config).Run(context.Background(), []Case{{Name: "safe", NoFabrication: true}})
	if card.Contract == nil || card.Contract.CatalogFixtureSHA256 == "" || card.CorpusVersion != "planner-certification-v6" {
		t.Fatalf("scorecard certification contract = %+v", card)
	}
	summary := HumanSummary(card)
	for _, want := range []string{"fixture-model", "budget profile `hermetic-bounded-v1`", "Hard gates", "Quality metrics", "1/1 trials passed"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("human summary missing %q:\n%s", want, summary)
		}
	}
}

func TestCertificationRunnerConfigRequiresSnapshotIdentity(t *testing.T) {
	_, err := CertificationRunnerConfig(RunnerConfig{
		Profile:   "not a fact token",
		Generator: ModelIdentity{Provider: "openrouter", Model: "qwen/qwen3.5-27b"},
	})
	if err == nil || !strings.Contains(err.Error(), "scorecard requires") {
		t.Fatalf("certification snapshot identity error = %v", err)
	}
}

func TestRunnerExecutesCertificationCaseAgainstPinnedCatalogFixture(t *testing.T) {
	provider := testkit.NewLLM(
		testkit.ToolCallResponse("catalog_search", map[string]any{"query": "Synthetic Matrix"}),
		testkit.FinalResponse(`{"picks":[{"mediaType":"movie","tmdbId":10001,"name":"Synthetic Matrix"}]}`),
	)
	generator, observer, err := NewEmbeddedCertificationGenerator(provider)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := CertificationCases()
	if err != nil {
		t.Fatal(err)
	}
	card := NewRunner(generator, RunnerConfig{}).WithObserver(observer).Run(context.Background(), cases[:1])
	if !card.Certified || len(card.Results) != 1 || !card.Results[0].Passed() {
		t.Fatalf("fixture-backed certification result = %+v", card)
	}
	if card.Results[0].ToolCalls != 1 || card.Results[0].CandidatesSurfaced != 1 {
		t.Fatalf("fixture observation = %+v, want one tool call and one surfaced candidate", card.Results[0].Observation)
	}
}

func TestRunnerExecutesV6QualifierFamiliesThroughProductionSuggester(t *testing.T) {
	allCases, err := CertificationCases()
	if err != nil {
		t.Fatal(err)
	}
	config, err := CertificationRunnerConfig(RunnerConfig{
		Profile:   "hermetic-bounded-v1",
		Generator: ModelIdentity{Provider: "ollama", Model: "fixture-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		caseName  string
		arguments map[string]any
		response  string
		calls     func(Observation) int
	}{
		{
			name: "network", caseName: "network-discovery",
			arguments: map[string]any{"media_type": "series", "network": "Synthetic Network"},
			response:  `{"picks":[{"mediaType":"series","tmdbId":10026,"name":"Synthetic Network Drama"}]}`,
			calls:     func(o Observation) int { return o.NetworkCalls },
		},
		{
			name: "cast", caseName: "cast-discovery",
			arguments: map[string]any{"media_type": "movie", "cast": []any{"Synthetic Performer"}},
			response:  `{"picks":[{"mediaType":"movie","tmdbId":10027,"name":"Synthetic Performer Feature"}]}`,
			calls:     func(o Observation) int { return o.CastCalls },
		},
		{
			name: "creator", caseName: "creator-discovery",
			arguments: map[string]any{"media_type": "movie", "creators": []any{"Synthetic Filmmaker"}},
			response:  `{"picks":[{"mediaType":"movie","tmdbId":10028,"name":"Synthetic Filmmaker Feature"}]}`,
			calls:     func(o Observation) int { return o.CreatorCalls },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var selected Case
			for _, c := range allCases {
				if c.Name == tc.caseName {
					selected = c
					break
				}
			}
			if selected.Name == "" {
				t.Fatalf("certification case %q is missing", tc.caseName)
			}
			provider := testkit.NewLLM(
				testkit.ToolCallResponse("catalog_search", tc.arguments),
				testkit.FinalResponse(tc.response),
			)
			generator, observer, err := NewEmbeddedCertificationGenerator(provider)
			if err != nil {
				t.Fatal(err)
			}
			card := NewRunner(generator, config).WithObserver(observer).Run(context.Background(), []Case{selected})
			if !card.Certified || len(card.Results) != 1 || !card.Results[0].Passed() ||
				!card.Results[0].CorrectToolOperation || tc.calls(card.Results[0].Observation) != 1 {
				t.Fatalf("v6 %s certification result = %+v", tc.name, card)
			}
			observation := card.Results[0].Observation
			if observation.ToolCalls != 1 || observation.TitleCalls != 0 || observation.GenreCalls != 0 ||
				observation.KeywordCalls != 0 || observation.PeopleCalls != 0 || observation.CandidatesSurfaced != 1 {
				t.Fatalf("v6 %s structural observation = %+v", tc.name, observation)
			}
		})
	}
}

func TestRunnerScoresWrongCertificationToolRouteAsQuality(t *testing.T) {
	runner := NewRunner(scriptedGenerator{}, RunnerConfig{Contract: &CertificationContract{
		Thresholds: CertificationThresholds{MinCorrectToolOperationRate: 1},
	}}).WithObserver(&scriptedObserver{
		value: Observation{ToolCalls: 1, GenreCalls: 1, GroundingStage: "grounded"},
	})
	card := runner.Run(context.Background(), []Case{{
		Name: "named-title", NoFabrication: true, ExpectedToolOperation: "title",
	}})
	if card.Results[0].FailureStage != "" || !card.Results[0].Passed() {
		t.Fatalf("quality miss became a hard failure: %+v", card.Results[0])
	}
	if card.Certified || card.Assessment == nil || card.Assessment.CorrectToolOperationRate != 0 {
		t.Fatalf("wrong tool route escaped aggregate threshold: %+v", card)
	}
}

func TestCertificationRunnerScoresPolicyProposalAndRecoveryQuality(t *testing.T) {
	proposal := suggest.Proposal{
		Lineup: []suggest.ProposalItem{{MediaType: provision.Movie, TMDBID: 10019, Name: "Synthetic Recovery"}},
		Policy: schedule.ChannelPolicy{ProposalPolicy: schedule.ProposalPolicy{
			Audience: schedule.AudiencePolicy{Ceiling: "TV-Y7"},
		}},
	}
	runner := NewRunner(scriptedGenerator{proposal: proposal}, RunnerConfig{Contract: &CertificationContract{
		Thresholds: CertificationThresholds{
			MinPolicyAccuracyRate: 1, MinProposalQualityRate: 1, MinRecoveryRate: 1,
		},
	}}).WithObserver(&scriptedObserver{value: Observation{
		ModelCalls: 3, ToolCalls: 2, KeywordCalls: 1, TitleCalls: 1, GroundingStage: "grounded",
	}})
	card := runner.Run(context.Background(), []Case{{
		Name: "recovery", NoFabrication: true,
		ExpectedPolicyCeiling: "TV-Y7",
		ExpectedProposalKeys:  []provision.Key{"movie:tmdb:10019"},
		RecoveryExpected:      true,
	}})
	if card.Assessment == nil || !card.Assessment.Passed {
		t.Fatalf("certification assessment = %+v", card.Assessment)
	}
	if card.Assessment.PolicyAccuracyRate != 1 || card.Assessment.ProposalQualityRate != 1 ||
		card.Assessment.RecoveryRate != 1 {
		t.Fatalf("new certification rates = %+v", card.Assessment)
	}
	result := card.Results[0]
	if !result.PolicyAccurate || !result.ProposalQuality || !result.RecoverySuccessful {
		t.Fatalf("new result quality evidence = %+v", result)
	}
	for _, want := range []string{"Policy accuracy: 100.0%", "proposal quality: 100.0%", "recovery: 100.0%"} {
		if summary := HumanSummary(card); !strings.Contains(summary, want) {
			t.Fatalf("human summary missing %q:\n%s", want, summary)
		}
	}
}

func TestCertificationQualityThresholdMissesDoNotBecomeHardFailures(t *testing.T) {
	runner := NewRunner(scriptedGenerator{}, RunnerConfig{Contract: &CertificationContract{
		Thresholds: CertificationThresholds{
			MinPolicyAccuracyRate: 1, MinProposalQualityRate: 1, MinRecoveryRate: 1,
		},
	}}).WithObserver(&scriptedObserver{value: Observation{ModelCalls: 1, ToolCalls: 1, GroundingStage: "grounded"}})
	card := runner.Run(context.Background(), []Case{{
		Name:                  "quality-miss",
		ExpectedPolicyCeiling: "TV-Y7",
		ExpectedProposalKeys:  []provision.Key{"movie:tmdb:10019"},
		RecoveryExpected:      true,
	}})
	if !card.Results[0].Passed() || card.Results[0].FailureStage != "" {
		t.Fatalf("quality misses became a hard failure: %+v", card.Results[0])
	}
	if card.Certified || card.Assessment == nil || len(card.Assessment.Failures) != 3 {
		t.Fatalf("quality threshold misses escaped certification: %+v", card)
	}
}

func TestCertificationCountsOnlyEncounteredRepairRecoveryOpportunities(t *testing.T) {
	observer := &sequenceObserver{values: []Observation{
		{ModelCalls: 3, ToolCalls: 1, GroundingStage: "grounded"},
		{ModelCalls: 2, ToolCalls: 1, GroundingStage: "grounded"},
	}}
	card := NewRunner(scriptedGenerator{proposal: suggest.Proposal{Lineup: []suggest.ProposalItem{{
		MediaType: provision.Movie, TMDBID: 10020, Name: "Synthetic Repair",
	}}}}, RunnerConfig{Contract: &CertificationContract{Thresholds: CertificationThresholds{
		MinRecoveryRate: 1,
	}}}).WithObserver(observer).Run(context.Background(), []Case{
		{Name: "repair-used", TrackRepairRecovery: true},
		{Name: "repair-not-needed", TrackRepairRecovery: true},
	})
	if !card.Results[0].RecoveryExpected || !card.Results[0].RecoverySuccessful {
		t.Fatalf("encountered repair was not scored: %+v", card.Results[0])
	}
	if card.Results[1].RecoveryExpected {
		t.Fatalf("clean first answer was mislabeled as recovery: %+v", card.Results[1])
	}
	if card.Assessment == nil || card.Assessment.RecoveryRate != 1 {
		t.Fatalf("repair recovery assessment = %+v", card.Assessment)
	}
}

func TestCertificationAbstentionPassCarriesNoFailureStage(t *testing.T) {
	runner := NewRunner(scriptedGenerator{err: suggest.ErrNoGroundedTitles}, RunnerConfig{}).WithObserver(&scriptedObserver{
		value: Observation{ToolCalls: 1, KeywordCalls: 1, GroundingStage: "selection_empty"},
	})
	card := runner.Run(context.Background(), []Case{{Name: "empty", NoFabrication: true}})
	if !card.Results[0].Passed() || card.Results[0].FailureStage != "" {
		t.Fatalf("passing abstention retained a failure stage: %+v", card.Results[0])
	}
}

func TestFrozenPlannerCertificationDoesNotRequireLiveScheduleEvidence(t *testing.T) {
	options := withRequiredResourceBudget(CertificationOptions{
		Required: true, FrozenCatalog: true, Trials: 1, GeneratorProvider: "openai",
	})
	if _, err := PrepareCertificationRun(25, options); err != nil {
		t.Fatalf("frozen planner certification required live schedule evidence: %v", err)
	}
}

type scriptedResourceProbe struct{ measurement ResourceMeasurement }

func (p scriptedResourceProbe) Measure(context.Context, ModelIdentity) ResourceMeasurement {
	return p.measurement
}

type sequenceObserver struct {
	values []Observation
	next   int
}

func (*sequenceObserver) Begin() {}

func (o *sequenceObserver) Snapshot(error) Observation {
	value := o.values[o.next]
	o.next++
	return value
}

func TestCertificationScorecardAggregatesLatencyToolCallsAndPeakMemory(t *testing.T) {
	observer := &sequenceObserver{values: []Observation{
		{ToolCalls: 1, GroundingStage: "grounded", generatorCalls: []InferenceCall{{LatencyNanos: 10}}},
		{ToolCalls: 2, GroundingStage: "grounded", generatorCalls: []InferenceCall{{LatencyNanos: 20}}},
		{ToolCalls: 2, GroundingStage: "grounded", generatorCalls: []InferenceCall{{LatencyNanos: 30}}},
		{ToolCalls: 3, GroundingStage: "grounded", generatorCalls: []InferenceCall{{LatencyNanos: 100}}},
	}}
	runner := NewRunner(scriptedGenerator{}, RunnerConfig{Contract: &CertificationContract{
		Thresholds: CertificationThresholds{MaxP95ToolCalls: 2},
	}}).WithObserver(observer).WithResourceProbe(scriptedResourceProbe{measurement: ResourceMeasurement{
		Status: "measured", Source: "fixture", PeakRAMBytes: 8 << 30, PeakVRAMBytes: 6 << 30,
	}})
	card := runner.Run(context.Background(), []Case{
		{Name: "p10", NoFabrication: true}, {Name: "p20", NoFabrication: true},
		{Name: "p30", NoFabrication: true}, {Name: "p100", NoFabrication: true},
	})
	if card.Assessment == nil {
		t.Fatal("certification assessment is missing")
	}
	performance := card.Assessment.Performance
	if performance.GeneratorLatencyP50Nanos != 20 || performance.GeneratorLatencyP95Nanos != 100 {
		t.Fatalf("run latency percentiles = %+v, want p50=20ns p95=100ns", performance)
	}
	if performance.P95ToolCalls != 3 || performance.PeakRAMBytes != 8<<30 || performance.PeakVRAMBytes != 6<<30 {
		t.Fatalf("performance evidence = %+v", performance)
	}
	if card.Certified || len(card.Assessment.Failures) != 1 {
		t.Fatalf("p95 tool-call threshold did not fail certification: %+v", card.Assessment)
	}
	summary := HumanSummary(card)
	for _, want := range []string{"Latency p50/p95: 20ns / 100ns", "Peak RAM/VRAM: 8.00 GiB / 6.00 GiB"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("performance summary missing %q:\n%s", want, summary)
		}
	}
}

func TestCertificationCasesAreExecutableAndHaveHardGates(t *testing.T) {
	cases, err := CertificationCases()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 168 {
		t.Fatalf("executable certification cases = %d, want 168", len(cases))
	}
	abstentions := 0
	policyCases := 0
	proposalCases := 0
	recoveryCases := 0
	intents := make(map[string]bool, len(cases))
	for _, c := range cases {
		if c.Name == "" || c.Intent.Description == "" {
			t.Fatalf("case has blank executable identity or Intent: %+v", c)
		}
		if c.MaxToolCalls <= 0 || c.MaxCandidatesSurfaced <= 0 {
			t.Fatalf("case %q lacks production structural hard gates", c.Name)
		}
		if !c.NoFabrication {
			t.Fatalf("case %q does not hard-gate unsupported ids", c.Name)
		}
		if !c.ExpectGroundedCompletion {
			abstentions++
		}
		if c.ExpectedPolicyCeiling != "" {
			policyCases++
		}
		if len(c.ExpectedProposalKeys) > 0 || c.ExpectedProposalAbstention {
			proposalCases++
		}
		if c.RecoveryExpected {
			recoveryCases++
		}
		if intents[c.Intent.Description] {
			t.Fatalf("Intent %q is duplicated", c.Intent.Description)
		}
		intents[c.Intent.Description] = true
	}
	if abstentions != 18 {
		t.Fatalf("abstention cases = %d, want exactly 18 explicit empty/conflict cases", abstentions)
	}
	if policyCases != 30 || proposalCases != 168 || recoveryCases != 6 {
		t.Fatalf("quality answer coverage: policy=%d proposal=%d recovery=%d, want 30/168/6",
			policyCases, proposalCases, recoveryCases)
	}
}

func TestCertificationFamilySmokeCasesSelectExactlyOneBaseIntentPerFamily(t *testing.T) {
	cases, err := CertificationFamilySmokeCases()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 28 {
		t.Fatalf("family smoke cases = %d, want 28", len(cases))
	}
	seen := make(map[string]bool, len(cases))
	for _, c := range cases {
		if strings.Contains(c.Name, "--") {
			t.Fatalf("smoke case %q is a phrasing variant, want the family's base Intent", c.Name)
		}
		if seen[c.Name] {
			t.Fatalf("smoke case %q is duplicated", c.Name)
		}
		seen[c.Name] = true
	}
}
