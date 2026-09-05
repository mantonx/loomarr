//go:build eval

package eval

import (
	"strings"
	"testing"
	"time"
)

func TestComparePlannerModelsChoosesSmallestWithinQualityMargin(t *testing.T) {
	selection := CertificationSelection{
		QualityMargin: 0.02,
		Weights: CertificationQualityWeights{
			GroundedCompletion: 0.20, CorrectToolOperation: 0.20, SchemaValidity: 0.10,
			PolicyAccuracy: 0.15, ProposalQuality: 0.25, Recovery: 0.10,
		},
	}
	card := func(model string, quality, footprint float64, certified bool) Scorecard {
		return Scorecard{
			SchemaVersion: 10, CorpusVersion: "planner-certification-v3", Certified: certified,
			Generator: ModelIdentity{Provider: "ollama", Model: model},
			Contract: &CertificationContract{
				CorpusVersion: "planner-certification-v3", CatalogFixtureSHA256: "fixture",
				PromptVersion: "prompt", ToolSchemaVersion: "tool", ScorerVersion: "scorer",
				Selection: selection,
			},
			Assessment: &CertificationAssessment{
				Passed: certified, GroundedCompletionRate: quality, CorrectToolOperationRate: quality,
				SchemaValidityRate: quality, PolicyAccuracyRate: quality,
				ProposalQualityRate: quality, RecoveryRate: quality,
				Performance: PerformanceSummary{ResourceStatus: "measured", PeakRAMBytes: int64(footprint * (1 << 30)),
					GeneratorLatencyP95Nanos: int64(2 * time.Second)},
			},
		}
	}
	comparison, err := ComparePlannerModels([]Scorecard{
		card("gemma", 0.98, 10, true),
		card("qwen", 0.97, 7, true),
		card("mistral", 0.90, 20, true),
		card("unsafe", 1.00, 5, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if comparison.BestQuality.Model != "gemma" || comparison.Preferred.Model != "qwen" {
		t.Fatalf("comparison winners = %+v", comparison)
	}
	if len(comparison.Advance) != 2 || comparison.Advance[0].Model != "gemma" || comparison.Advance[1].Model != "qwen" {
		t.Fatalf("advanced candidates = %+v", comparison.Advance)
	}
	for _, want := range []string{"gemma", "qwen", "10.00 GiB", "2s", "smallest model within the 2.0% quality margin"} {
		if summary := PlannerComparisonSummary(comparison); !strings.Contains(summary, want) {
			t.Fatalf("comparison summary missing %q:\n%s", want, summary)
		}
	}
}

func TestComparePlannerModelsRecordsNoEligibleDecision(t *testing.T) {
	selection := CertificationSelection{QualityMargin: 0.02, Weights: CertificationQualityWeights{
		GroundedCompletion: 0.20, CorrectToolOperation: 0.20, SchemaValidity: 0.10,
		PolicyAccuracy: 0.15, ProposalQuality: 0.25, Recovery: 0.10,
	}}
	card := func(model string) Scorecard {
		return Scorecard{
			SchemaVersion: 10, CorpusVersion: "planner-certification-v3",
			Generator: ModelIdentity{Provider: "ollama", Model: model},
			Contract: &CertificationContract{
				CorpusVersion: "planner-certification-v3", CatalogFixtureSHA256: "fixture",
				PromptVersion: "prompt", ToolSchemaVersion: "tool", ScorerVersion: "scorer", Selection: selection,
			},
			Assessment: &CertificationAssessment{GroundedCompletionRate: 0.5, CorrectToolOperationRate: 0.5,
				SchemaValidityRate: 0.5, PolicyAccuracyRate: 0.5, ProposalQualityRate: 0.5, RecoveryRate: 0.5},
		}
	}
	comparison, err := ComparePlannerModels([]Scorecard{card("gemma"), card("qwen")})
	if err != nil {
		t.Fatal(err)
	}
	if comparison.DecisionStatus != "no_eligible" || comparison.DecisionReason == "" || comparison.Preferred.Model != "" {
		t.Fatalf("no-eligible comparison = %+v", comparison)
	}
	if summary := PlannerComparisonSummary(comparison); !strings.Contains(summary, "No candidate is eligible") {
		t.Fatalf("no-eligible summary:\n%s", summary)
	}
}

func TestComparePlannerModelsRejectsDifferentMetricContracts(t *testing.T) {
	selection := CertificationSelection{QualityMargin: 0.02, Weights: CertificationQualityWeights{
		GroundedCompletion: 0.20, CorrectToolOperation: 0.20, SchemaValidity: 0.10,
		PolicyAccuracy: 0.15, ProposalQuality: 0.25, Recovery: 0.10,
	}}
	card := func(model string) Scorecard {
		return Scorecard{
			SchemaVersion: 10, CorpusVersion: "planner-certification-v3",
			Generator:  ModelIdentity{Provider: "ollama", Model: model},
			CallBudget: CallBudget{Cases: 150, Trials: 1, MaxGeneratorCalls: 3600, MaxJudgeCalls: 150, Total: 3750},
			Contract: &CertificationContract{
				CorpusVersion: "planner-certification-v3", CatalogFixtureSHA256: "fixture",
				PromptVersion: "prompt", ToolSchemaVersion: "tool", ScorerVersion: "scorer",
				HardMetrics: []string{"grounding"}, QualityMetrics: []string{"proposal_quality"},
				Thresholds: CertificationThresholds{MinProposalQualityRate: 0.9}, Selection: selection,
			},
			Assessment: &CertificationAssessment{GroundedCompletionRate: 1, CorrectToolOperationRate: 1,
				SchemaValidityRate: 1, PolicyAccuracyRate: 1, ProposalQualityRate: 1, RecoveryRate: 1},
		}
	}
	first, second := card("gemma"), card("qwen")
	second.Contract.Thresholds.MinProposalQualityRate = 0.8
	if _, err := ComparePlannerModels([]Scorecard{first, second}); err == nil || !strings.Contains(err.Error(), "frozen certification identity") {
		t.Fatalf("different threshold contract error = %v", err)
	}
	second = card("qwen")
	second.CallBudget.Resource.MaxTokensPerSuite = 1
	if _, err := ComparePlannerModels([]Scorecard{first, second}); err == nil || !strings.Contains(err.Error(), "frozen certification identity") {
		t.Fatalf("different resource budget error = %v", err)
	}
	first, second = card("gemma"), card("qwen")
	first.Profile, second.Profile = "bounded-v1", "bounded-v2"
	if _, err := ComparePlannerModels([]Scorecard{first, second}); err == nil || !strings.Contains(err.Error(), "frozen certification identity") {
		t.Fatalf("different budget profile error = %v", err)
	}
	first, second = card("gemma"), card("qwen")
	first.SchemaVersion, second.SchemaVersion = 11, 11
	if _, err := ComparePlannerModels([]Scorecard{first, second}); err != nil {
		t.Fatalf("archived schema-v11 comparison error = %v", err)
	}
	second.SchemaVersion = 12
	if _, err := ComparePlannerModels([]Scorecard{first, second}); err == nil || !strings.Contains(err.Error(), "frozen certification identity") {
		t.Fatalf("mixed schema-v11/v12 comparison error = %v", err)
	}
	first.SchemaVersion = 12
	if _, err := ComparePlannerModels([]Scorecard{first, second}); err == nil || !strings.Contains(err.Error(), "lacks its run snapshot") {
		t.Fatalf("missing schema-v12 snapshot error = %v", err)
	}
}
