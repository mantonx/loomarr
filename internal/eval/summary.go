//go:build eval

package eval

import (
	"fmt"
	"strings"
	"time"
)

// HumanSummary renders a stable Markdown comparison summary from the same
// Scorecard that is serialized for machines.
func HumanSummary(card Scorecard) string {
	passed := 0
	for _, result := range card.Results {
		if result.Passed() {
			passed++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Planner model certification: %s / %s\n\n", card.Generator.Provider, card.Generator.Model)
	fmt.Fprintf(&b, "Corpus `%s`; %d/%d trials passed; certified: %t.\n\n", card.CorpusVersion, passed, len(card.Results), card.Certified)
	if card.RunSnapshot != nil {
		resolved := card.RunSnapshot.ResolvedModel
		if resolved == "" {
			resolved = "unreported"
		}
		fmt.Fprintf(&b, "Resolved model `%s`; budget profile `%s`; accounting available: %t.\n\n",
			resolved, card.RunSnapshot.BudgetProfile, card.RunSnapshot.AccountingAvailable)
	}
	b.WriteString("## Hard gates\n\n")
	if card.Contract == nil || len(card.Contract.HardMetrics) == 0 {
		b.WriteString("No certification hard-gate manifest attached.\n")
	} else {
		fmt.Fprintf(&b, "%s.\n", strings.Join(card.Contract.HardMetrics, ", "))
	}
	b.WriteString("\n## Quality metrics\n\n")
	if card.Contract == nil || len(card.Contract.QualityMetrics) == 0 {
		b.WriteString("No certification quality-metric manifest attached.\n")
	} else {
		fmt.Fprintf(&b, "%s.\n", strings.Join(card.Contract.QualityMetrics, ", "))
	}
	if card.Assessment != nil {
		fmt.Fprintf(&b, "\nGrounded completion: %.1f%%; correct tool operation: %.1f%%; schema validity: %.1f%%.\n",
			card.Assessment.GroundedCompletionRate*100,
			card.Assessment.CorrectToolOperationRate*100,
			card.Assessment.SchemaValidityRate*100)
		fmt.Fprintf(&b, "Policy accuracy: %.1f%%; proposal quality: %.1f%%; recovery: %.1f%%.\n",
			card.Assessment.PolicyAccuracyRate*100,
			card.Assessment.ProposalQualityRate*100,
			card.Assessment.RecoveryRate*100)
		performance := card.Assessment.Performance
		fmt.Fprintf(&b, "Latency p50/p95: %s / %s; p95 tool calls: %d.\n",
			time.Duration(performance.GeneratorLatencyP50Nanos),
			time.Duration(performance.GeneratorLatencyP95Nanos), performance.P95ToolCalls)
		if performance.ResourceStatus == "measured" {
			fmt.Fprintf(&b, "Peak RAM/VRAM: %.2f GiB / %.2f GiB (%s).\n",
				float64(performance.PeakRAMBytes)/(1<<30),
				float64(performance.PeakVRAMBytes)/(1<<30), performance.ResourceSource)
		} else {
			fmt.Fprintf(&b, "Peak RAM/VRAM: unavailable (%s).\n", performance.ResourceSource)
		}
		for _, failure := range card.Assessment.Failures {
			fmt.Fprintf(&b, "- Threshold failure: %s\n", failure)
		}
	}
	b.WriteString("\n| Case | Trial | Result | Tool calls | Candidates | Theme fit | Judge |\n")
	b.WriteString("| --- | ---: | --- | ---: | ---: | ---: | ---: |\n")
	for _, result := range card.Results {
		status := "PASS"
		if !result.Passed() {
			status = "FAIL"
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %d | %d | %.2f | %.2f |\n",
			result.Case, result.Trial, status, result.ToolCalls, result.CandidatesSurfaced,
			result.ThemeFit, result.JudgeScore)
	}
	return b.String()
}
