//go:build eval

package eval

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/quality"
	"github.com/loomarr/loomarr/internal/suggest"
	"github.com/loomarr/loomarr/internal/testkit"
)

func TestRunnerRejectsUnboundedOrInvalidProviderChargeText(t *testing.T) {
	amountMarker := strings.Repeat("9", InferenceMaxChargeAmountRunes+1) + "PRIVATE_AMOUNT"
	generatorLLM := testkit.NewLLM(llm.Response{Attribution: llm.Attribution{
		Charge: &llm.Money{Amount: amountMarker, Currency: "USD"},
	}})
	observed := &observedProvider{inner: generatorLLM}
	judgeLLM := testkit.NewLLM(llm.Response{
		Content: `{"overall":0.9,"relevance":0.9,"serendipity":0.8,"reason":"Grounded."}`,
		Attribution: llm.Attribution{Charge: &llm.Money{
			Amount: "0.0042", Currency: "NOT-A-CURRENCY-PRIVATE",
		}},
	})
	proposal := suggest.Proposal{Lineup: []suggest.ProposalItem{{
		MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix",
	}}}
	card := NewRunner(providerGenerator{provider: observed, proposal: proposal}, RunnerConfig{}).
		WithObserver(observed).
		WithJudge(modelJudge{provider: judgeLLM}).
		Run(context.Background(), []Case{{Name: "invalid_charge", JudgeRubric: "Relevant science fiction"}})

	result := card.Results[0]
	if !card.Certified {
		t.Fatalf("invalid optional charge metadata changed semantic certification: %+v", result)
	}
	for role, call := range map[string]InferenceCall{
		"generator": result.GeneratorCalls[0],
		"judge":     result.JudgeCalls[0],
	} {
		if call.ChargeStatus != InferenceChargeInvalid || call.Charge != (InferenceCharge{}) {
			t.Errorf("%s invalid charge = %+v", role, call)
		}
	}
	blob, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"PRIVATE_AMOUNT", "NOT-A-CURRENCY-PRIVATE"} {
		if strings.Contains(string(blob), forbidden) {
			t.Fatalf("scorecard retained invalid provider charge text %q: %s", forbidden, blob)
		}
	}
}

func TestRunnerRecordsIndependentGeneratorAndJudgeCallAttribution(t *testing.T) {
	generatorLLM := testkit.NewLLM(llm.Response{Attribution: llm.Attribution{
		RequestedProvider: "openrouter", RequestedModel: "generator-requested",
		ResolvedProvider: "Generator Route", ResolvedModel: "generator-resolved",
		Tokens: llm.TokenUsage{Prompt: 11, Completion: 12, Reasoning: 13, Cached: 14,
			CacheWrite: 15, Image: 16, Audio: 17, Video: 18},
		Charge:   &llm.Money{Amount: "0.012300", Currency: "USD"},
		Attempts: 2, Latency: 1500 * time.Millisecond,
	}})
	observed := &observedProvider{inner: generatorLLM}
	judgeLLM := testkit.NewLLM(llm.Response{
		Content: `{"overall":0.9,"relevance":0.9,"serendipity":0.8,"reason":"Grounded."}`,
		Attribution: llm.Attribution{
			RequestedProvider: "openai", RequestedModel: "judge-requested",
			ResolvedProvider: "Judge Route", ResolvedModel: "judge-resolved",
			Tokens:   llm.TokenUsage{Prompt: 21, Completion: 22},
			Charge:   &llm.Money{Amount: "0.0042", Currency: "USD"},
			Attempts: 1, Latency: 250 * time.Millisecond,
		},
	})
	proposal := suggest.Proposal{Lineup: []suggest.ProposalItem{{
		MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix",
	}}}
	runner := NewRunner(providerGenerator{provider: observed, proposal: proposal}, RunnerConfig{
		Generator: ModelIdentity{Provider: "configured-generator", Model: "configured-generator-model"},
		Judge:     ModelIdentity{Provider: "configured-judge", Model: "configured-judge-model"},
	}).WithObserver(observed).WithJudge(modelJudge{provider: judgeLLM})

	card := runner.Run(context.Background(), []Case{{Name: "attributed", JudgeRubric: "Relevant science fiction"}})
	result := card.Results[0]
	if !card.Certified || len(result.GeneratorCalls) != 1 || len(result.JudgeCalls) != 1 {
		t.Fatalf("attributed result = %+v", result)
	}
	generator := result.GeneratorCalls[0]
	if generator.RequestedProvider != "openrouter" || generator.RequestedModel != "generator-requested" ||
		generator.ResolvedProvider != "Generator Route" || generator.ResolvedModel != "generator-resolved" ||
		generator.Tokens != (InferenceTokens{Prompt: 11, Completion: 12, Reasoning: 13, Cached: 14,
			CacheWrite: 15, Image: 16, Audio: 17, Video: 18}) ||
		generator.Charge != (InferenceCharge{Amount: "0.012300", Currency: "USD"}) || generator.ChargeStatus != InferenceChargeReported ||
		generator.Attempts != 2 || generator.LatencyNanos != int64(1500*time.Millisecond) {
		t.Fatalf("generator attribution = %+v", generator)
	}
	judge := result.JudgeCalls[0]
	if judge.RequestedProvider != "openai" || judge.RequestedModel != "judge-requested" ||
		judge.ResolvedProvider != "Judge Route" || judge.ResolvedModel != "judge-resolved" ||
		judge.Tokens.Prompt != 21 || judge.Tokens.Completion != 22 ||
		judge.Charge != (InferenceCharge{Amount: "0.0042", Currency: "USD"}) || judge.ChargeStatus != InferenceChargeReported ||
		judge.Attempts != 1 || judge.LatencyNanos != int64(250*time.Millisecond) {
		t.Fatalf("judge attribution = %+v", judge)
	}
}

func TestRunnerKeepsMissingCallAttributionExplicitInsteadOfInferringIt(t *testing.T) {
	generatorLLM := testkit.NewLLM(llm.Response{})
	observed := &observedProvider{inner: generatorLLM}
	judgeLLM := testkit.NewLLM(testkit.FinalResponse(
		`{"overall":0.9,"relevance":0.9,"serendipity":0.8,"reason":"Grounded."}`,
	))
	proposal := suggest.Proposal{Lineup: []suggest.ProposalItem{{
		MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix",
	}}}
	runner := NewRunner(providerGenerator{provider: observed, proposal: proposal}, RunnerConfig{
		Generator: ModelIdentity{Provider: "configured-generator", Model: "configured-generator-model"},
		Judge:     ModelIdentity{Provider: "configured-judge", Model: "configured-judge-model"},
	}).WithObserver(observed).WithJudge(modelJudge{provider: judgeLLM})

	result := runner.Run(context.Background(), []Case{{
		Name: "missing_attribution", JudgeRubric: "Relevant science fiction",
	}}).Results[0]
	if len(result.GeneratorCalls) != 1 || len(result.JudgeCalls) != 1 {
		t.Fatalf("missing attribution calls = generator %v judge %v", result.GeneratorCalls, result.JudgeCalls)
	}
	wantMissing := InferenceCall{ChargeStatus: InferenceChargeMissing}
	if result.GeneratorCalls[0] != wantMissing || result.JudgeCalls[0] != wantMissing {
		t.Fatalf("missing attribution was inferred: generator %+v judge %+v", result.GeneratorCalls[0], result.JudgeCalls[0])
	}
}

func TestRunnerStopsProviderWorkWhenRunOrSuiteBudgetIsExhausted(t *testing.T) {
	for name, tc := range map[string]struct {
		budget             ResourceBudget
		wantGeneratorCalls int
	}{
		"per-run": {
			budget:             ResourceBudget{MaxTokensPerRun: 10, MaxSpendPerRun: "0.005", MaxTokensPerSuite: 100, MaxSpendPerSuite: "1.00"},
			wantGeneratorCalls: 2,
		},
		"suite": {
			budget:             ResourceBudget{MaxTokensPerRun: 100, MaxSpendPerRun: "1.00", MaxTokensPerSuite: 12, MaxSpendPerSuite: "0.005"},
			wantGeneratorCalls: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			response := llm.Response{Attribution: llm.Attribution{
				Tokens: llm.TokenUsage{Prompt: 8, Completion: 5},
				Charge: &llm.Money{Amount: "0.006", Currency: "USD"},
			}}
			generatorLLM := testkit.NewLLM(response, response)
			observed := &observedProvider{inner: generatorLLM}
			judgeLLM := testkit.NewLLM(testkit.FinalResponse(
				`{"overall":0.9,"relevance":0.9,"serendipity":0.8,"reason":"Must not run."}`,
			))
			proposal := suggest.Proposal{Lineup: []suggest.ProposalItem{{
				MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix",
			}}}
			card := NewRunner(providerGenerator{provider: observed, proposal: proposal}, RunnerConfig{
				ResourceBudget: tc.budget,
			}).WithObserver(observed).WithJudge(modelJudge{provider: judgeLLM}).Run(context.Background(), []Case{
				{Name: "first", JudgeRubric: "Relevant science fiction"},
				{Name: "second", JudgeRubric: "Relevant science fiction"},
			})

			if card.Certified || len(card.Results) != 2 {
				t.Fatalf("budgeted scorecard = %+v", card)
			}
			for _, result := range card.Results {
				if result.FailureStage != FailureStageBudgetExhausted {
					t.Errorf("%s stage = %q, want budget_exhausted", result.Case, result.FailureStage)
				}
			}
			if generatorLLM.Calls != tc.wantGeneratorCalls || judgeLLM.Calls != 0 {
				t.Fatalf("provider calls after exhaustion = generator %d judge %d", generatorLLM.Calls, judgeLLM.Calls)
			}
			if card.FailureCounts[FailureStageBudgetExhausted] != 2 {
				t.Fatalf("budget failure counts = %v", card.FailureCounts)
			}
		})
	}
}

func TestRunnerEnforcesPerRunAndSuiteCallBudgetsBeforeProviders(t *testing.T) {
	for name, tc := range map[string]struct {
		budget             ResourceBudget
		wantGeneratorCalls int
	}{
		"per-run stops judge but resets for the next run": {
			budget:             ResourceBudget{MaxCallsPerRun: 1, MaxCallsPerSuite: 4},
			wantGeneratorCalls: 2,
		},
		"suite stops judge and every subsequent run": {
			budget:             ResourceBudget{MaxCallsPerRun: 2, MaxCallsPerSuite: 1},
			wantGeneratorCalls: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			generatorLLM := testkit.NewLLM(llm.Response{}, llm.Response{})
			observed := &observedProvider{inner: generatorLLM}
			judgeLLM := testkit.NewLLM(testkit.FinalResponse(
				`{"overall":0.9,"relevance":0.9,"serendipity":0.8,"reason":"Must not run."}`,
			))
			proposal := suggest.Proposal{Lineup: []suggest.ProposalItem{{
				MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix",
			}}}
			card := NewRunner(providerGenerator{provider: observed, proposal: proposal}, RunnerConfig{
				ResourceBudget: tc.budget,
			}).WithObserver(observed).WithJudge(modelJudge{provider: judgeLLM}).Run(context.Background(), []Case{
				{Name: "first", JudgeRubric: "Relevant science fiction"},
				{Name: "second", JudgeRubric: "Relevant science fiction"},
			})

			if card.Certified || generatorLLM.Calls != tc.wantGeneratorCalls || judgeLLM.Calls != 0 {
				t.Fatalf("call budget result = certified %v generator %d judge %d", card.Certified, generatorLLM.Calls, judgeLLM.Calls)
			}
			if card.FailureCounts[FailureStageBudgetExhausted] != 2 {
				t.Fatalf("call budget failures = %v", card.FailureCounts)
			}
			for _, result := range card.Results {
				if result.FailureStage != FailureStageBudgetExhausted {
					t.Errorf("%s stage = %q", result.Case, result.FailureStage)
				}
			}
		})
	}
}

func TestRunnerEnforcesGeneratorBudgetAtEveryProviderCallBoundary(t *testing.T) {
	proposal := suggest.Proposal{Lineup: []suggest.ProposalItem{{
		MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix",
	}}}
	for _, tc := range []struct {
		name      string
		responses []llm.Response
		calls     int
		budget    ResourceBudget
		wantCalls int
		wantPass  bool
	}{
		{
			name:      "exact call ceiling completes",
			responses: []llm.Response{{}, {}}, calls: 2,
			budget:    ResourceBudget{MaxCallsPerRun: 2, MaxCallsPerSuite: 10},
			wantCalls: 2, wantPass: true,
		},
		{
			name:      "next tool-loop call cannot start",
			responses: []llm.Response{{}, {}, {}}, calls: 3,
			budget:    ResourceBudget{MaxCallsPerRun: 2, MaxCallsPerSuite: 10},
			wantCalls: 2,
		},
		{
			name: "exact token and spend ceiling blocks next call",
			responses: []llm.Response{
				{Attribution: llm.Attribution{RequestedProvider: "openrouter", Tokens: llm.TokenUsage{Prompt: 4, Completion: 6}, Charge: &llm.Money{Amount: "0.50", Currency: "USD"}}},
				{},
			},
			calls: 2,
			budget: ResourceBudget{
				MaxCallsPerRun: 10, MaxCallsPerSuite: 10,
				MaxTokensPerRun: 10, MaxTokensPerSuite: 100,
				MaxSpendPerRun: "0.50", MaxSpendPerSuite: "5.00",
			},
			wantCalls: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := testkit.NewLLM(tc.responses...)
			observed := &observedProvider{inner: provider}
			card := NewRunner(providerGenerator{provider: observed, proposal: proposal, calls: tc.calls}, RunnerConfig{
				ResourceBudget: tc.budget,
			}).WithObserver(observed).Run(context.Background(), []Case{{Name: "provider_boundary"}})

			if provider.Calls != tc.wantCalls {
				t.Fatalf("provider calls = %d, want %d; result %+v", provider.Calls, tc.wantCalls, card.Results[0])
			}
			if tc.wantPass {
				if !card.Certified {
					t.Fatalf("exact boundary did not certify: %+v", card.Results[0])
				}
				return
			}
			if card.Certified || card.Results[0].FailureStage != FailureStageBudgetExhausted {
				t.Fatalf("exhausted boundary result = %+v", card.Results[0])
			}
		})
	}
}

func TestRunnerTreatsMissingGeneratorUsageAsStickyPerCall(t *testing.T) {
	proposal := suggest.Proposal{Lineup: []suggest.ProposalItem{{
		MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix",
	}}}
	reported := llm.Attribution{
		RequestedProvider: "openrouter", Tokens: llm.TokenUsage{Prompt: 3, Completion: 2},
		Charge: &llm.Money{Amount: "0.10", Currency: "USD"},
	}
	for _, tc := range []struct {
		name   string
		second llm.Attribution
	}{
		{name: "missing tokens", second: llm.Attribution{RequestedProvider: "openrouter", Charge: &llm.Money{Amount: "0.10", Currency: "USD"}}},
		{name: "missing hosted spend", second: llm.Attribution{RequestedProvider: "openrouter", Tokens: llm.TokenUsage{Prompt: 3, Completion: 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := testkit.NewLLM(
				llm.Response{Attribution: reported},
				llm.Response{Attribution: tc.second},
			)
			observed := &observedProvider{inner: provider}
			card := NewRunner(providerGenerator{provider: observed, proposal: proposal, calls: 2}, RunnerConfig{
				ResourceBudget: ResourceBudget{
					MaxCallsPerRun: 4, MaxCallsPerSuite: 4,
					MaxTokensPerRun: 100, MaxTokensPerSuite: 100,
					MaxSpendPerRun: "1.00", MaxSpendPerSuite: "1.00",
				},
			}).WithObserver(observed).Run(context.Background(), []Case{{Name: "mixed_usage"}})

			if provider.Calls != 2 || card.Certified || card.Results[0].FailureStage != FailureStageBudgetExhausted {
				t.Fatalf("mixed usage result = calls %d result %+v", provider.Calls, card.Results[0])
			}
		})
	}
}

func TestRunnerLatchesMissingHostedUsageAcrossLaterTrials(t *testing.T) {
	provider := testkit.NewLLM(llm.Response{Attribution: llm.Attribution{
		RequestedProvider: "openrouter",
	}})
	observed := &observedProvider{inner: provider}
	judgeProvider := testkit.NewLLM(testkit.FinalResponse(
		`{"overall":0.9,"relevance":0.9,"serendipity":0.8,"reason":"Must not run."}`,
	))
	proposal := suggest.Proposal{Lineup: []suggest.ProposalItem{{
		MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix",
	}}}
	card := NewRunner(providerGenerator{provider: observed, proposal: proposal}, RunnerConfig{
		Trials: 2,
		ResourceBudget: ResourceBudget{
			MaxCallsPerRun: 2, MaxCallsPerSuite: 4,
			MaxTokensPerRun: 100, MaxTokensPerSuite: 200,
			MaxSpendPerRun: "1.00", MaxSpendPerSuite: "2.00",
		},
	}).WithObserver(observed).WithJudge(modelJudge{provider: judgeProvider}).Run(context.Background(), []Case{{
		Name: "missing_usage_latches_suite", JudgeRubric: "Relevant science fiction",
	}})

	if provider.Calls != 1 || judgeProvider.Calls != 0 {
		t.Fatalf("provider calls after missing usage = generator %d judge %d, want 1/0", provider.Calls, judgeProvider.Calls)
	}
	if len(card.Results) != 2 {
		t.Fatalf("results = %d, want two trials", len(card.Results))
	}
	if first := card.Results[0]; first.FailureStage != FailureStageBudgetExhausted ||
		!strings.Contains(strings.Join(first.Failures, " "), "usage is missing") {
		t.Fatalf("first missing-usage result = %+v", first)
	}
	if later := card.Results[1]; later.FailureStage != FailureStageBudgetExhausted ||
		later.ModelCalls != 0 || later.ToolCalls != 0 || len(later.GeneratorCalls) != 0 || len(later.JudgeCalls) != 0 {
		t.Fatalf("later trial was not stopped before provider/tool work: %+v", later)
	}
}

func TestRunnerKeepsGenerationAsFirstFailureWhenUsageIsAlsoUnknown(t *testing.T) {
	provider := testkit.NewLLM()
	provider.Delay = time.Second
	observed := &observedProvider{inner: provider}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	card := NewRunner(providerGenerator{provider: observed}, RunnerConfig{
		ResourceBudget: ResourceBudget{
			MaxCallsPerRun: 2, MaxCallsPerSuite: 2,
			MaxTokensPerRun: 100, MaxTokensPerSuite: 100,
			MaxSpendPerRun: "1.00", MaxSpendPerSuite: "1.00",
		},
	}).WithObserver(observed).Run(ctx, []Case{{Name: "provider_error_with_unknown_usage"}})

	result := card.Results[0]
	if result.FailureStage != FailureStageGeneration ||
		card.FailureCounts[FailureStageGeneration] != 1 ||
		card.FailureCounts[FailureStageBudgetExhausted] != 0 {
		t.Fatalf("first-failure accounting = stage %q counts %v result %+v", result.FailureStage, card.FailureCounts, result)
	}
	joined := strings.Join(result.Failures, " ")
	if !strings.Contains(joined, "grounding failed") || !strings.Contains(joined, "usage is missing") {
		t.Fatalf("failures = %v, want generation diagnosis plus latched budget uncertainty", result.Failures)
	}
}

func TestRunnerKeepsLocalOllamaCallsExplicitlyNonBilled(t *testing.T) {
	response := llm.Response{Attribution: llm.Attribution{
		RequestedProvider: "ollama", Tokens: llm.TokenUsage{Prompt: 3, Completion: 2},
	}}
	provider := testkit.NewLLM(response, response)
	observed := &observedProvider{inner: provider}
	proposal := suggest.Proposal{Lineup: []suggest.ProposalItem{{MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix"}}}
	card := NewRunner(providerGenerator{provider: observed, proposal: proposal, calls: 2}, RunnerConfig{
		ResourceBudget: ResourceBudget{
			MaxCallsPerRun: 2, MaxCallsPerSuite: 2,
			MaxTokensPerRun: 10, MaxTokensPerSuite: 10,
			MaxSpendPerRun: "0.01", MaxSpendPerSuite: "0.01",
		},
	}).WithObserver(observed).Run(context.Background(), []Case{{Name: "local_non_billed"}})
	if !card.Certified || provider.Calls != 2 || card.ResourceUsage.Spend != "0" {
		t.Fatalf("local non-billed result = calls %d usage %+v result %+v", provider.Calls, card.ResourceUsage, card.Results[0])
	}
}

func TestRunnerBoundsAndScrubsGeneratorCallAttribution(t *testing.T) {
	bounds := suggest.ProductionBounds()
	responses := make([]llm.Response, bounds.MaxModelCalls+1)
	for i := range responses {
		responses[i] = llm.Response{
			Content: "PRIVATE_PROVIDER_PAYLOAD",
			Attribution: llm.Attribution{
				RequestedProvider: "openrouter", RequestedModel: "requested",
				ResolvedProvider: "route", ResolvedModel: "resolved",
				Modalities: []string{"PRIVATE_MODALITY_MARKER"}, GenerationID: "PRIVATE_GENERATION_ID",
			},
		}
	}
	responses[0].Attribution.RequestedModel = strings.Repeat("x", InferenceMaxIdentityRunes) + "IDENTITY_BEYOND_CAP"
	provider := testkit.NewLLM(responses...)
	observed := &observedProvider{inner: provider}
	proposal := suggest.Proposal{Lineup: []suggest.ProposalItem{{
		MediaType: provision.Movie, TMDBID: 603, Name: "The Matrix",
	}}}
	card := NewRunner(providerGenerator{
		provider: observed, proposal: proposal, calls: len(responses),
	}, RunnerConfig{}).WithObserver(observed).Run(context.Background(), []Case{{Name: "bounded"}})
	result := card.Results[0]
	if len(result.GeneratorCalls) != bounds.MaxModelCalls {
		t.Fatalf("generator call evidence = %d, want bound %d", len(result.GeneratorCalls), bounds.MaxModelCalls)
	}
	if card.Certified || result.ModelCalls != bounds.MaxModelCalls+1 ||
		result.FailureStage != FailureStageStructuralBudget || card.FailureCounts[FailureStageStructuralBudget] != 1 {
		t.Fatalf("generator call overrun = %+v counts %v", result, card.FailureCounts)
	}
	blob, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"PRIVATE_PROVIDER_PAYLOAD", "PRIVATE_MODALITY_MARKER", "PRIVATE_GENERATION_ID", "IDENTITY_BEYOND_CAP"} {
		if strings.Contains(string(blob), forbidden) {
			t.Fatalf("scorecard attribution leaked %q: %s", forbidden, blob)
		}
	}
}

func TestScorecardShapeExcludesCredentials(t *testing.T) {
	t.Setenv("LLM_API_KEY", "never-serialize-this-secret")
	t.Setenv("LOOMARR_EVAL_JUDGE_API_KEY", "never-serialize-this-judge-secret")
	scorecard := Scorecard{
		SchemaVersion: scorecardSchemaVersion, CorpusVersion: corpusVersion,
		Generator: ModelIdentity{Provider: "openai", Model: "example/generator"},
		Judge:     ModelIdentity{Provider: "openai", Model: "example/judge"},
		CallBudget: CallBudget{Resource: ResourceBudget{
			MaxCallsPerRun: 25, MaxCallsPerSuite: 150,
		}}, Results: []Result{{
			Case: "holiday", RelevanceScore: 0.8, SerendipityScore: 0.7,
		}},
	}
	blob, err := json.Marshal(scorecard)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{os.Getenv("LLM_API_KEY"), os.Getenv("LOOMARR_EVAL_JUDGE_API_KEY")} {
		if strings.Contains(string(blob), secret) {
			t.Fatal("scorecard metadata contains an LLM credential")
		}
	}
	for _, field := range []string{"relevanceScore", "serendipityScore"} {
		if !strings.Contains(string(blob), field) {
			t.Errorf("scorecard is missing quality dimension %q: %s", field, blob)
		}
	}
	var shape map[string]any
	if err := json.Unmarshal(blob, &shape); err != nil {
		t.Fatal(err)
	}
	callBudget := shape["callBudget"].(map[string]any)
	resource := callBudget["resource"].(map[string]any)
	if resource["maxCallsPerRun"] != float64(25) || resource["maxCallsPerSuite"] != float64(150) {
		t.Fatalf("schema-v7 declared call limits = %v", resource)
	}
}

func TestScorecardRunSnapshotBindsResolvedRouteBudgetAndApplication(t *testing.T) {
	inner := testkit.NewLLM(llm.Response{Attribution: llm.Attribution{
		RequestedProvider: "openrouter", RequestedModel: "qwen/qwen3.5-27b",
		ResolvedProvider: "Qwen", ResolvedModel: "qwen/qwen3.5-27b-20260901",
		Tokens: llm.TokenUsage{Prompt: 10, Completion: 5},
		Charge: &llm.Money{Amount: "0.01", Currency: "USD"},
	}})
	provider := &observedProvider{inner: inner}
	runner := NewRunner(providerGenerator{provider: provider}, RunnerConfig{
		Profile:   "hosted-bounded-v1",
		Generator: ModelIdentity{Provider: "openrouter", Model: "qwen/qwen3.5-27b"},
		ResourceBudget: ResourceBudget{
			MaxCallsPerRun: 25, MaxCallsPerSuite: 25,
			MaxTokensPerRun: 1000, MaxTokensPerSuite: 1000,
			MaxSpendPerRun: "1.00", MaxSpendPerSuite: "1.00",
		},
	}).WithObserver(provider)

	card := runner.Run(context.Background(), []Case{{Name: "snapshot"}})

	if card.RunSnapshot == nil {
		t.Fatal("scorecard run snapshot is missing")
	}
	snapshot := *card.RunSnapshot
	if snapshot.CorpusVersion != card.CorpusVersion || snapshot.RequestedModel != "qwen/qwen3.5-27b" ||
		snapshot.ResolvedModel != "qwen/qwen3.5-27b-20260901" || snapshot.Provider != quality.ProviderOpenRouter ||
		snapshot.BudgetProfile != "hosted-bounded-v1" || snapshot.ApplicationVersion == "" ||
		!snapshot.AccountingAvailable || snapshot.CreatedAt != card.GeneratedAt.Truncate(time.Second) {
		t.Fatalf("run snapshot = %+v", snapshot)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("run snapshot validation: %v", err)
	}
}

func TestScorecardRunSnapshotKeepsMissingOrMixedResolutionExplicit(t *testing.T) {
	card := Scorecard{
		SchemaVersion: scorecardSchemaVersion,
		CorpusVersion: "planner-certification-v5",
		GeneratedAt:   time.Unix(1_800_000_000, 123).UTC(),
		Profile:       "local-bounded-v1",
		Generator:     ModelIdentity{Provider: "ollama", Model: "qwen3.5:27b"},
		Results: []Result{{GeneratorCalls: []InferenceCall{
			{ResolvedModel: "qwen3.5:27b-a"},
			{ResolvedModel: "qwen3.5:27b-b"},
		}}},
	}

	snapshot := buildScorecardRunSnapshot(card, false)
	if snapshot == nil || snapshot.ResolvedModel != "" || snapshot.AccountingAvailable {
		t.Fatalf("mixed-resolution snapshot = %+v", snapshot)
	}
}

func TestPrepareCertificationRunRequiresPerRunAndSuiteCallCeilings(t *testing.T) {
	base := withRequiredResourceBudget(CertificationOptions{
		Required: true, LiveSchedule: true, Trials: 3, GeneratorProvider: "openai",
		MaxCallsPerRun: "25", MaxCallsPerSuite: "150",
	})
	for name, mutate := range map[string]func(*CertificationOptions){
		"missing run":       func(o *CertificationOptions) { o.MaxCallsPerRun = "" },
		"invalid run":       func(o *CertificationOptions) { o.MaxCallsPerRun = "many" },
		"under run":         func(o *CertificationOptions) { o.MaxCallsPerRun = "24" },
		"overflow run":      func(o *CertificationOptions) { o.MaxCallsPerRun = "9223372036854775808" },
		"missing suite":     func(o *CertificationOptions) { o.MaxCallsPerSuite = "" },
		"invalid suite":     func(o *CertificationOptions) { o.MaxCallsPerSuite = "many" },
		"under suite total": func(o *CertificationOptions) { o.MaxCallsPerSuite = "149" },
	} {
		t.Run(name, func(t *testing.T) {
			options := base
			mutate(&options)
			budget, err := PrepareCertificationRun(2, options)
			if err == nil {
				t.Fatalf("required certification accepted call ceilings run=%q suite=%q", options.MaxCallsPerRun, options.MaxCallsPerSuite)
			}
			if budget.Cases != 2 || budget.Trials != 3 || budget.Total != 150 {
				t.Fatalf("rejected preflight budget = %+v, want complete safe estimate", budget)
			}
		})
	}

	budget, err := PrepareCertificationRun(2, base)
	if err != nil {
		t.Fatal(err)
	}
	if budget.Resource.MaxCallsPerRun != 25 || budget.Resource.MaxCallsPerSuite != 150 {
		t.Fatalf("declared call limits = %+v", budget.Resource)
	}
}

func TestPrepareCertificationRunRequiresDeclaredTokenAndSpendBudgets(t *testing.T) {
	base := withRequiredResourceBudget(CertificationOptions{
		Required: true, LiveSchedule: true, Trials: 1, GeneratorProvider: "openai",
	})
	for name, mutate := range map[string]func(*CertificationOptions){
		"missing run tokens":   func(o *CertificationOptions) { o.MaxTokensPerRun = "" },
		"invalid run spend":    func(o *CertificationOptions) { o.MaxSpendPerRun = "unlimited" },
		"missing suite tokens": func(o *CertificationOptions) { o.MaxTokensPerSuite = "" },
		"invalid suite spend":  func(o *CertificationOptions) { o.MaxSpendPerSuite = "-1" },
		"suite below run":      func(o *CertificationOptions) { o.MaxTokensPerSuite = "99" },
	} {
		t.Run(name, func(t *testing.T) {
			options := base
			mutate(&options)
			if _, err := PrepareCertificationRun(1, options); err == nil {
				t.Fatalf("required certification accepted invalid resource budget: %+v", options)
			}
		})
	}
}

func TestParseEvaluationTrialsRejectsInvalidRequiredValues(t *testing.T) {
	for name, raw := range map[string]string{
		"invalid":  "many",
		"zero":     "0",
		"negative": "-1",
		"overflow": "9223372036854775808",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseEvaluationTrials(true, raw); err == nil {
				t.Fatalf("required certification accepted LOOMARR_EVAL_TRIALS=%q", raw)
			}
		})
	}
	if got, err := ParseEvaluationTrials(true, ""); err != nil || got != 3 {
		t.Fatalf("required default trials = %d err=%v, want 3", got, err)
	}
	if got, err := ParseEvaluationTrials(true, "4"); err != nil || got != 4 {
		t.Fatalf("required explicit trials = %d err=%v, want 4", got, err)
	}
}

func TestPrepareCertificationRunRejectsArithmeticOverflow(t *testing.T) {
	budget, err := PrepareCertificationRun(2, CertificationOptions{
		Trials: int(^uint(0) >> 1), GeneratorProvider: "openai",
	})
	if err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("overflowing budget = %+v err=%v, want explicit overflow error", budget, err)
	}
}

func TestPrepareCertificationRunRequiresExplicitLocalInferenceOptIn(t *testing.T) {
	cases := []Case{{Name: "one"}}
	base := withRequiredResourceBudget(CertificationOptions{
		Required: true, LiveSchedule: true, Trials: 1,
	})
	for _, provider := range []string{"", "ollama"} {
		options := base
		options.GeneratorProvider = provider
		if _, err := PrepareCertificationRun(len(cases), options); err == nil {
			t.Fatalf("required certification accepted local provider %q without LOOMARR_EVAL_ALLOW_LOCAL=1", provider)
		}
		options.AllowLocal = true
		if _, err := PrepareCertificationRun(len(cases), options); err != nil {
			t.Fatalf("required certification rejected opted-in local provider %q: %v", provider, err)
		}
	}
	hosted := base
	hosted.GeneratorProvider = "openai"
	if _, err := PrepareCertificationRun(len(cases), hosted); err != nil {
		t.Fatalf("hosted certification required a local inference opt-in: %v", err)
	}
	hosted.JudgeProvider = "ollama"
	if _, err := PrepareCertificationRun(len(cases), hosted); err == nil {
		t.Fatal("required certification accepted a local judge without LOOMARR_EVAL_ALLOW_LOCAL=1")
	}
}

func withRequiredResourceBudget(options CertificationOptions) CertificationOptions {
	if options.MaxCallsPerRun == "" {
		options.MaxCallsPerRun = "25"
	}
	if options.MaxCallsPerSuite == "" {
		options.MaxCallsPerSuite = "1000"
	}
	options.MaxTokensPerRun = "100"
	options.MaxSpendPerRun = "1.00"
	options.MaxTokensPerSuite = "1000"
	options.MaxSpendPerSuite = "10.00"
	return options
}
