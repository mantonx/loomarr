//go:build eval

package eval

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

const plannerComparisonSchemaVersion = 1

type PlannerComparisonCandidate struct {
	Provider               string  `json:"provider"`
	Model                  string  `json:"model"`
	Certified              bool    `json:"certified"`
	QualityScore           float64 `json:"qualityScore"`
	ResidentFootprintBytes int64   `json:"residentFootprintBytes,omitempty"`
	LatencyP95Nanos        int64   `json:"latencyP95Nanos"`
}

type PlannerModelComparison struct {
	SchemaVersion  int                          `json:"schemaVersion"`
	CorpusVersion  string                       `json:"corpusVersion"`
	QualityMargin  float64                      `json:"qualityMargin"`
	DecisionStatus string                       `json:"decisionStatus"`
	DecisionReason string                       `json:"decisionReason,omitempty"`
	Candidates     []PlannerComparisonCandidate `json:"candidates"`
	BestQuality    PlannerComparisonCandidate   `json:"bestQuality,omitempty"`
	Preferred      PlannerComparisonCandidate   `json:"preferred,omitempty"`
	Advance        []PlannerComparisonCandidate `json:"advance"`
}

func ComparePlannerModels(cards []Scorecard) (PlannerModelComparison, error) {
	if len(cards) < 2 {
		return PlannerModelComparison{}, fmt.Errorf("planner comparison requires at least two scorecards")
	}
	first := cards[0]
	if first.Contract == nil || first.Assessment == nil {
		return PlannerModelComparison{}, fmt.Errorf("planner scorecard 0 lacks its certification contract or assessment")
	}
	selection := first.Contract.Selection
	if err := validateSelection(selection); err != nil {
		return PlannerModelComparison{}, err
	}
	comparison := PlannerModelComparison{
		SchemaVersion: plannerComparisonSchemaVersion,
		CorpusVersion: first.CorpusVersion,
		QualityMargin: selection.QualityMargin,
	}
	for index, card := range cards {
		if card.Contract == nil || card.Assessment == nil {
			return PlannerModelComparison{}, fmt.Errorf("planner scorecard %d lacks its certification contract or assessment", index)
		}
		if !comparableCertification(first, card) {
			return PlannerModelComparison{}, fmt.Errorf("planner scorecard %d does not share the frozen certification identity", index)
		}
		if card.Generator.Provider == "" || card.Generator.Model == "" {
			return PlannerModelComparison{}, fmt.Errorf("planner scorecard %d has a blank model identity", index)
		}
		if err := validateScorecardRunSnapshot(card); err != nil {
			return PlannerModelComparison{}, fmt.Errorf("planner scorecard %d: %w", index, err)
		}
		if err := validateQualityAssessment(*card.Assessment); err != nil {
			return PlannerModelComparison{}, fmt.Errorf("planner scorecard %d: %w", index, err)
		}
		candidate := PlannerComparisonCandidate{
			Provider:        card.Generator.Provider,
			Model:           card.Generator.Model,
			Certified:       card.Certified && card.Assessment.Passed,
			QualityScore:    weightedQuality(*card.Assessment, selection.Weights),
			LatencyP95Nanos: card.Assessment.Performance.GeneratorLatencyP95Nanos,
		}
		if card.Assessment.Performance.ResourceStatus == "measured" {
			if card.Assessment.Performance.PeakRAMBytes < 0 || card.Assessment.Performance.PeakVRAMBytes < 0 ||
				card.Assessment.Performance.PeakRAMBytes > math.MaxInt64-card.Assessment.Performance.PeakVRAMBytes {
				return PlannerModelComparison{}, fmt.Errorf("planner scorecard %d has invalid resident memory evidence", index)
			}
			candidate.ResidentFootprintBytes = card.Assessment.Performance.PeakRAMBytes + card.Assessment.Performance.PeakVRAMBytes
		}
		comparison.Candidates = append(comparison.Candidates, candidate)
	}
	slices.SortFunc(comparison.Candidates, func(a, b PlannerComparisonCandidate) int {
		if a.QualityScore != b.QualityScore {
			if a.QualityScore > b.QualityScore {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Model, b.Model)
	})
	eligible := make([]PlannerComparisonCandidate, 0, len(comparison.Candidates))
	for _, candidate := range comparison.Candidates {
		if candidate.Certified {
			eligible = append(eligible, candidate)
		}
	}
	if len(eligible) == 0 {
		comparison.DecisionStatus = "no_eligible"
		comparison.DecisionReason = "no candidate cleared every certification threshold and hard gate"
		return comparison, nil
	}
	comparison.DecisionStatus = "selected"
	comparison.BestQuality = eligible[0]
	comparison.Preferred = comparison.BestQuality
	for _, candidate := range eligible {
		if comparison.BestQuality.QualityScore-candidate.QualityScore > selection.QualityMargin {
			continue
		}
		if candidate.ResidentFootprintBytes > 0 &&
			(comparison.Preferred.ResidentFootprintBytes == 0 || candidate.ResidentFootprintBytes < comparison.Preferred.ResidentFootprintBytes) {
			comparison.Preferred = candidate
		}
	}
	comparison.Advance = append(comparison.Advance, comparison.BestQuality)
	if comparison.Preferred.Model != comparison.BestQuality.Model || comparison.Preferred.Provider != comparison.BestQuality.Provider {
		comparison.Advance = append(comparison.Advance, comparison.Preferred)
	}
	return comparison, nil
}

func validateSelection(selection CertificationSelection) error {
	if math.IsNaN(selection.QualityMargin) || math.IsInf(selection.QualityMargin, 0) ||
		selection.QualityMargin < 0 || selection.QualityMargin > 1 {
		return fmt.Errorf("planner selection quality margin must be within [0,1]")
	}
	w := selection.Weights
	values := []float64{w.GroundedCompletion, w.CorrectToolOperation, w.SchemaValidity, w.PolicyAccuracy, w.ProposalQuality, w.Recovery}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("planner selection quality weights must be finite and non-negative")
		}
	}
	sum := w.GroundedCompletion + w.CorrectToolOperation + w.SchemaValidity + w.PolicyAccuracy + w.ProposalQuality + w.Recovery
	if math.Abs(sum-1) > 1e-9 {
		return fmt.Errorf("planner selection quality weights must be non-negative and sum to 1")
	}
	return nil
}

func validateQualityAssessment(assessment CertificationAssessment) error {
	values := []float64{
		assessment.GroundedCompletionRate, assessment.CorrectToolOperationRate, assessment.SchemaValidityRate,
		assessment.PolicyAccuracyRate, assessment.ProposalQualityRate, assessment.RecoveryRate,
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return fmt.Errorf("quality rates must be finite and within [0,1]")
		}
	}
	return nil
}

func comparableCertification(first, candidate Scorecard) bool {
	return first.SchemaVersion == candidate.SchemaVersion && first.CorpusVersion == candidate.CorpusVersion &&
		first.Profile == candidate.Profile &&
		first.CallBudget.Cases == candidate.CallBudget.Cases && first.CallBudget.Trials == candidate.CallBudget.Trials &&
		first.CallBudget.MaxGeneratorCalls == candidate.CallBudget.MaxGeneratorCalls &&
		first.CallBudget.MaxJudgeCalls == candidate.CallBudget.MaxJudgeCalls && first.CallBudget.Total == candidate.CallBudget.Total &&
		first.CallBudget.Resource == candidate.CallBudget.Resource &&
		first.Contract.CorpusVersion == candidate.Contract.CorpusVersion &&
		first.Contract.CatalogFixtureSHA256 == candidate.Contract.CatalogFixtureSHA256 &&
		first.Contract.PromptVersion == candidate.Contract.PromptVersion &&
		first.Contract.ToolSchemaVersion == candidate.Contract.ToolSchemaVersion &&
		first.Contract.ScorerVersion == candidate.Contract.ScorerVersion &&
		slices.Equal(first.Contract.HardMetrics, candidate.Contract.HardMetrics) &&
		slices.Equal(first.Contract.QualityMetrics, candidate.Contract.QualityMetrics) &&
		first.Contract.Thresholds == candidate.Contract.Thresholds &&
		first.Contract.Selection == candidate.Contract.Selection
}

func weightedQuality(assessment CertificationAssessment, weights CertificationQualityWeights) float64 {
	return assessment.GroundedCompletionRate*weights.GroundedCompletion +
		assessment.CorrectToolOperationRate*weights.CorrectToolOperation +
		assessment.SchemaValidityRate*weights.SchemaValidity +
		assessment.PolicyAccuracyRate*weights.PolicyAccuracy +
		assessment.ProposalQualityRate*weights.ProposalQuality +
		assessment.RecoveryRate*weights.Recovery
}

func PlannerComparisonSummary(comparison PlannerModelComparison) string {
	var b strings.Builder
	b.WriteString("# Planner stock-model comparison\n\n")
	fmt.Fprintf(&b, "Corpus `%s`; quality margin %.1f%%.\n\n", comparison.CorpusVersion, comparison.QualityMargin*100)
	b.WriteString("| Model | Certified | Quality | Resident footprint | p95 latency |\n")
	b.WriteString("| --- | --- | ---: | ---: | ---: |\n")
	for _, candidate := range comparison.Candidates {
		footprint := "unavailable"
		if candidate.ResidentFootprintBytes > 0 {
			footprint = fmt.Sprintf("%.2f GiB", float64(candidate.ResidentFootprintBytes)/(1<<30))
		}
		latency := "unavailable"
		if candidate.LatencyP95Nanos > 0 {
			latency = time.Duration(candidate.LatencyP95Nanos).String()
		}
		fmt.Fprintf(&b, "| %s | %t | %.3f | %s | %s |\n", candidate.Model, candidate.Certified,
			candidate.QualityScore, footprint, latency)
	}
	if comparison.DecisionStatus == "no_eligible" {
		fmt.Fprintf(&b, "\nNo candidate is eligible: %s.\n", comparison.DecisionReason)
		return b.String()
	}
	fmt.Fprintf(&b, "\nBest quality: `%s`. Preferred: `%s`, the smallest model within the %.1f%% quality margin.\n",
		comparison.BestQuality.Model, comparison.Preferred.Model, comparison.QualityMargin*100)
	return b.String()
}
