//go:build eval

package eval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/quality"
	"github.com/loomarr/loomarr/internal/suggest"
)

const (
	scorecardSchemaVersion = 12
	corpusVersion          = "2026-08-27.8"
)

// Generator is the one external seam the behavioral evaluator needs: production
// supplies the real grounded Suggester, while hermetic tests supply a scripted
// adapter. Everything after the Proposal remains deterministic Loomarr code.
type Generator interface {
	Suggest(context.Context, suggest.Intent) (suggest.Proposal, error)
}

// RunnerConfig identifies one reproducible evaluation profile. Credentials and
// provider payloads never enter it or the scorecard.
type RunnerConfig struct {
	Trials         int
	Profile        string
	Generator      ModelIdentity
	Judge          ModelIdentity
	ResourceBudget ResourceBudget
	Contract       *CertificationContract
}

// CertificationContract identifies every versioned input that makes a planner
// model score comparable and keeps hard gates separate from quality metrics.
type CertificationContract struct {
	CorpusVersion        string                  `json:"corpusVersion"`
	CatalogFixtureSHA256 string                  `json:"catalogFixtureSha256"`
	PromptVersion        string                  `json:"promptVersion"`
	ToolSchemaVersion    string                  `json:"toolSchemaVersion"`
	ScorerVersion        string                  `json:"scorerVersion"`
	HardMetrics          []string                `json:"hardMetrics"`
	QualityMetrics       []string                `json:"qualityMetrics"`
	Thresholds           CertificationThresholds `json:"thresholds"`
	Selection            CertificationSelection  `json:"selection"`
}

type CertificationSelection struct {
	QualityMargin float64                     `json:"qualityMargin"`
	Weights       CertificationQualityWeights `json:"weights"`
}

type CertificationQualityWeights struct {
	GroundedCompletion   float64 `json:"groundedCompletion"`
	CorrectToolOperation float64 `json:"correctToolOperation"`
	SchemaValidity       float64 `json:"schemaValidity"`
	PolicyAccuracy       float64 `json:"policyAccuracy"`
	ProposalQuality      float64 `json:"proposalQuality"`
	Recovery             float64 `json:"recovery"`
}

type CertificationThresholds struct {
	MinGroundedCompletionRate   float64 `json:"minGroundedCompletionRate"`
	MinCorrectToolOperationRate float64 `json:"minCorrectToolOperationRate"`
	MinSchemaValidityRate       float64 `json:"minSchemaValidityRate"`
	MinPolicyAccuracyRate       float64 `json:"minPolicyAccuracyRate"`
	MinProposalQualityRate      float64 `json:"minProposalQualityRate"`
	MinRecoveryRate             float64 `json:"minRecoveryRate"`
	MaxP95ToolCalls             int     `json:"maxP95ToolCalls"`
}

type CertificationAssessment struct {
	Passed                   bool               `json:"passed"`
	GroundedCompletionRate   float64            `json:"groundedCompletionRate"`
	CorrectToolOperationRate float64            `json:"correctToolOperationRate"`
	SchemaValidityRate       float64            `json:"schemaValidityRate"`
	PolicyAccuracyRate       float64            `json:"policyAccuracyRate"`
	ProposalQualityRate      float64            `json:"proposalQualityRate"`
	RecoveryRate             float64            `json:"recoveryRate"`
	Failures                 []string           `json:"failures"`
	Performance              PerformanceSummary `json:"performance"`
}

type PerformanceSummary struct {
	MeasuredRuns             int    `json:"measuredRuns"`
	GeneratorLatencyP50Nanos int64  `json:"generatorLatencyP50Nanos"`
	GeneratorLatencyP95Nanos int64  `json:"generatorLatencyP95Nanos"`
	P95ToolCalls             int    `json:"p95ToolCalls"`
	ResourceStatus           string `json:"resourceStatus"`
	ResourceSource           string `json:"resourceSource,omitempty"`
	PeakRAMBytes             int64  `json:"peakRamBytes,omitempty"`
	PeakVRAMBytes            int64  `json:"peakVramBytes,omitempty"`
}

type ResourceMeasurement struct {
	Status        string
	Source        string
	PeakRAMBytes  int64
	PeakVRAMBytes int64
}

type ResourceProbe interface {
	Measure(context.Context, ModelIdentity) ResourceMeasurement
}

// ModelIdentity names one inference role without credentials or endpoint data.
type ModelIdentity struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// FailureStage is the closed vocabulary for the first boundary that failed a trial.
type FailureStage string

const (
	FailureStageRetrieval        FailureStage = "retrieval"
	FailureStageGeneration       FailureStage = "generation"
	FailureStageDeterministic    FailureStage = "deterministic"
	FailureStageStructuralBudget FailureStage = "structural_budget"
	FailureStageSchedule         FailureStage = "schedule"
	FailureStageJudge            FailureStage = "judge"
	FailureStageBudgetExhausted  FailureStage = "budget_exhausted"
)

// Scorecard is the versioned machine-readable result of one Runner execution.
type Scorecard struct {
	SchemaVersion int                      `json:"schemaVersion"`
	CorpusVersion string                   `json:"corpusVersion"`
	GeneratedAt   time.Time                `json:"generatedAt"`
	Profile       string                   `json:"profile"`
	Generator     ModelIdentity            `json:"generator"`
	Judge         ModelIdentity            `json:"judge"`
	CallBudget    CallBudget               `json:"callBudget"`
	ResourceUsage ResourceUsage            `json:"resourceUsage"`
	RunSnapshot   *quality.RunSnapshot     `json:"runSnapshot,omitempty"`
	Certified     bool                     `json:"certified"`
	FailureCounts map[FailureStage]int     `json:"failureCounts"`
	Results       []Result                 `json:"results"`
	Cases         []CaseSummary            `json:"cases"`
	Contract      *CertificationContract   `json:"contract,omitempty"`
	Assessment    *CertificationAssessment `json:"assessment,omitempty"`
}

// CaseSummary makes stochastic stability visible rather than collapsing several
// trials into one last-write-wins result.
type CaseSummary struct {
	Case        string     `json:"case"`
	Trials      int        `json:"trials"`
	Passed      int        `json:"passed"`
	PassRate    float64    `json:"passRate"`
	Overall     ScoreRange `json:"overall"`
	Relevance   ScoreRange `json:"relevance"`
	Serendipity ScoreRange `json:"serendipity"`
}

type ScoreRange struct {
	Min    float64 `json:"min"`
	Median float64 `json:"median"`
	Max    float64 `json:"max"`
}

// Judge is the subjective scoring seam. It receives bounded audit evidence after
// deterministic gates; it never decides those requirements. Production supplies
// an LLM-backed adapter and hermetic tests a scripted one.
type Judge interface {
	Score(context.Context, JudgeEvidence) (JudgeScores, error)
}

// Observer records bounded structural evidence around one Generator trial.
type Observer interface {
	Begin()
	Snapshot(error) Observation
}

type resourceBoundaryObserver interface {
	beginResourceRun(ResourceBudget, *resourceAccumulator, *resourceAccumulator)
}

var errProviderBudgetExhausted = errors.New("evaluation provider budget exhausted")

type providerResourceLedger struct {
	limits ResourceBudget
	run    *resourceAccumulator
	suite  *resourceAccumulator
}

func (l *providerResourceLedger) beforeCall() string {
	return resourceBudgetBeforeNextCall(l.limits, l.run, l.suite, true)
}

func (l *providerResourceLedger) afterCall(call InferenceCall) string {
	return consumeResourceCalls(l.limits, l.run, l.suite, []InferenceCall{call})
}

// Runner owns evaluation from grounded generation through deterministic gates.
// Later schedule and judge evidence deepen this same interface rather than
// creating parallel exploratory/certification paths.
type Runner struct {
	generator     Generator
	materializer  ScheduleMaterializer
	judge         Judge
	observer      Observer
	resourceProbe ResourceProbe
	config        RunnerConfig
}

func (r *Runner) WithMaterializer(materializer ScheduleMaterializer) *Runner {
	r.materializer = materializer
	return r
}

func (r *Runner) WithJudge(judge Judge) *Runner {
	r.judge = judge
	return r
}

func (r *Runner) WithObserver(observer Observer) *Runner {
	r.observer = observer
	return r
}

func (r *Runner) WithResourceProbe(probe ResourceProbe) *Runner {
	r.resourceProbe = probe
	return r
}

func NewRunner(generator Generator, config RunnerConfig) *Runner {
	if config.Trials <= 0 {
		config.Trials = 1
	}
	return &Runner{generator: generator, config: config}
}

// Run evaluates every case serially for the configured number of trials.
func (r *Runner) Run(ctx context.Context, cases []Case) Scorecard {
	callBudget, budgetErr := computeCallBudget(len(cases), r.config.Trials)
	callBudget.Resource = r.config.ResourceBudget
	suiteUsage := newResourceAccumulator()
	peakMeasurement := ResourceMeasurement{Status: "unavailable"}
	cardCorpusVersion := corpusVersion
	if r.config.Contract != nil && r.config.Contract.CorpusVersion != "" {
		cardCorpusVersion = r.config.Contract.CorpusVersion
	}
	card := Scorecard{
		SchemaVersion: scorecardSchemaVersion,
		CorpusVersion: cardCorpusVersion,
		GeneratedAt:   time.Now().UTC(),
		Profile:       r.config.Profile,
		Generator:     r.config.Generator,
		Judge:         r.config.Judge,
		CallBudget:    callBudget,
		Contract:      r.config.Contract,
		Certified:     len(cases) > 0,
		FailureCounts: map[FailureStage]int{
			FailureStageRetrieval:        0,
			FailureStageGeneration:       0,
			FailureStageDeterministic:    0,
			FailureStageStructuralBudget: 0,
			FailureStageSchedule:         0,
			FailureStageJudge:            0,
			FailureStageBudgetExhausted:  0,
		},
	}
	if budgetErr != nil {
		card.Certified = false
		return card
	}
	for _, c := range cases {
		passed := 0
		var overall, relevance, serendipity []float64
		for trial := 1; trial <= r.config.Trials; trial++ {
			result := Result{
				Case: c.Name, Trial: trial,
				GroundedCompletionExpected: c.ExpectGroundedCompletion,
				ToolOperationExpected:      c.ExpectedToolOperation != "",
				PolicyAccuracyExpected:     c.ExpectedPolicyCeiling != "",
				ProposalQualityExpected:    len(c.ExpectedProposalKeys) > 0 || c.ExpectedProposalAbstention,
				RecoveryExpected:           c.RecoveryExpected,
				GeneratorCalls:             make([]InferenceCall, 0), JudgeCalls: make([]InferenceCall, 0),
			}
			runUsage := newResourceAccumulator()
			boundaryBudget := false
			var prop suggest.Proposal
			var err error
			var materializedPrograms []MaterializedProgram
			if r.observer != nil {
				r.observer.Begin()
			}
			if resourceBudgetEnabled(r.config.ResourceBudget) {
				boundaryObserver, ok := r.observer.(resourceBoundaryObserver)
				if !ok {
					result.addFailures(FailureStageBudgetExhausted, "budget_exhausted: generator provider-boundary observation is required to enforce resource ceilings")
				} else {
					boundaryObserver.beginResourceRun(r.config.ResourceBudget, runUsage, suiteUsage)
					boundaryBudget = true
				}
			}
			if result.Passed() {
				if budgetMessage := resourceBudgetBeforeNextCall(r.config.ResourceBudget, runUsage, suiteUsage, true); budgetMessage != "" {
					result.addFailures(FailureStageBudgetExhausted, budgetMessage)
				}
			}
			if result.Passed() {
				prop, err = r.generator.Suggest(ctx, mapIntent(c.Intent))
				result.Lineup = len(prop.Lineup)
				result.Acquisitions = len(prop.Acquisitions)
				result.GroundedCompletion = result.Lineup+result.Acquisitions > 0
				result.SchemaValid = err == nil || errors.Is(err, suggest.ErrNoGroundedTitles)
				result.Ceiling = string(prop.Policy.Audience.Ceiling)
				result.PolicyAccurate = c.ExpectedPolicyCeiling == "" || result.Ceiling == c.ExpectedPolicyCeiling
				result.ProposalQuality = proposalQualityMatches(c, prop, err)
				result.ThemeFit = prop.Scores.ThemeFit
				result.Failures = deterministicChecks(c, prop, err)
				if !result.Passed() {
					result.FailureStage = FailureStageDeterministic
					if err != nil {
						result.FailureStage = FailureStageGeneration
					}
				}
				if r.observer != nil {
					result.Observation = r.observer.Snapshot(err)
					if err != nil && !errors.Is(err, errProviderBudgetExhausted) && !result.Passed() {
						result.FailureStage = groundingFailureStage(result.GroundingStage)
					}
					if result.generatorBudgetErr != "" {
						if errors.Is(err, errProviderBudgetExhausted) {
							// The generator surfaced only the ledger refusal, so budget is the
							// first failed boundary rather than a generation diagnosis.
							result.FailureStage = FailureStageBudgetExhausted
							result.Failures = append(result.Failures, result.generatorBudgetErr)
						} else {
							// A real retrieval/generation error remains the first diagnosis,
							// while the same call's uncertain usage still latches the ledger.
							result.addFailures(FailureStageBudgetExhausted, result.generatorBudgetErr)
						}
					}
					result.GeneratorCalls = slices.Clone(result.generatorCalls)
					if result.ModelCalls > suggest.ProductionBounds().MaxModelCalls {
						result.addFailures(FailureStageStructuralBudget,
							fmt.Sprintf("model calls %d > production bound %d", result.ModelCalls, suggest.ProductionBounds().MaxModelCalls))
					}
					if c.MaxToolCalls > 0 && result.ToolCalls > c.MaxToolCalls {
						result.addFailures(FailureStageStructuralBudget,
							fmt.Sprintf("tool calls %d > budget %d", result.ToolCalls, c.MaxToolCalls))
					}
					if c.MaxCandidatesSurfaced > 0 && result.CandidatesSurfaced > c.MaxCandidatesSurfaced {
						result.addFailures(FailureStageStructuralBudget,
							fmt.Sprintf("candidates surfaced %d > budget %d", result.CandidatesSurfaced, c.MaxCandidatesSurfaced))
					}
					result.CorrectToolOperation = expectedToolOperationFailure(c.ExpectedToolOperation, result.Observation) == ""
					encounteredRepair := c.TrackRepairRecovery && result.ModelCalls > result.ToolCalls+1
					result.RecoveryExpected = c.RecoveryExpected || encounteredRepair
					result.RecoverySuccessful = !result.RecoveryExpected ||
						(result.GroundedCompletion && result.SchemaValid && (!c.RecoveryExpected || result.ToolCalls >= 2))
				}
				if !boundaryBudget {
					if budgetMessage := consumeGeneratorObservation(r.config.ResourceBudget, runUsage, suiteUsage, result.Observation); budgetMessage != "" {
						result.addFailures(FailureStageBudgetExhausted, budgetMessage)
					}
				}
			}
			if err == nil && requiresSchedule(c) {
				if r.materializer == nil {
					result.addFailures(FailureStageSchedule, "schedule materializer is not configured")
				} else {
					programs, scheduleErr := r.materializer.Materialize(ctx, c, prop)
					if scheduleErr != nil {
						result.addFailures(FailureStageSchedule, "schedule materialization failed: "+scheduleErr.Error())
					} else {
						materializedPrograms = programs
						result.ScheduledPrograms = materializedProgramIdentities(programs)
						result.addFailures(FailureStageSchedule, scheduledChecks(c, programs)...)
					}
				}
			}
			if result.Passed() && c.JudgeRubric != "" && r.judge == nil {
				result.JudgeError = "judge is not configured"
				result.addFailures(FailureStageJudge, result.JudgeError)
			}
			if result.Passed() && c.JudgeRubric != "" {
				evidence, evidenceErr := NewJudgeEvidence(c, prop, result.Observation, materializedPrograms)
				if evidenceErr != nil {
					result.JudgeError = evidenceErr.Error()
					result.addFailures(FailureStageJudge, result.JudgeError)
				}
				if evidenceErr == nil {
					if budgetMessage := resourceBudgetBeforeNextCall(r.config.ResourceBudget, runUsage, suiteUsage, true); budgetMessage != "" {
						result.addFailures(FailureStageBudgetExhausted, budgetMessage)
					}
				}
				if evidenceErr == nil && result.Passed() {
					scores, judgeErr := r.judge.Score(ctx, evidence)
					result.JudgeCalls = append(result.JudgeCalls, scrubAttribution(scores.Attribution))
					if budgetMessage := consumeResourceCalls(r.config.ResourceBudget, runUsage, suiteUsage, result.JudgeCalls); budgetMessage != "" {
						result.addFailures(FailureStageBudgetExhausted, budgetMessage)
					}
					if judgeErr == nil {
						judgeErr = validateJudgeScores(scores)
					}
					if judgeErr != nil {
						result.JudgeError = judgeErr.Error()
						result.addFailures(FailureStageJudge, result.JudgeError)
					} else {
						result.JudgeScore = scores.Overall
						result.RelevanceScore = scores.Relevance
						result.SerendipityScore = scores.Serendipity
						result.JudgeNote = scores.Reason
						overall = append(overall, scores.Overall)
						if c.MinJudgeScore > 0 && scores.Overall < c.MinJudgeScore {
							result.addFailures(FailureStageJudge,
								fmt.Sprintf("judge score %.2f < required %.2f: %s", scores.Overall, c.MinJudgeScore, scores.Reason))
						}
						if c.MinRelevanceScore > 0 && scores.Relevance < c.MinRelevanceScore {
							result.addFailures(FailureStageJudge,
								fmt.Sprintf("relevance score %.2f < required %.2f: %s", scores.Relevance, c.MinRelevanceScore, scores.Reason))
						}
						if c.MinSerendipityScore > 0 && scores.Serendipity < c.MinSerendipityScore {
							result.addFailures(FailureStageJudge,
								fmt.Sprintf("serendipity score %.2f < required %.2f: %s", scores.Serendipity, c.MinSerendipityScore, scores.Reason))
						}
						if scores.Relevance >= 0 {
							relevance = append(relevance, scores.Relevance)
						}
						if scores.Serendipity >= 0 {
							serendipity = append(serendipity, scores.Serendipity)
						}
					}
				}
			}
			card.ResourceUsage = suiteUsage.usage()
			card.Results = append(card.Results, result)
			if r.resourceProbe != nil {
				peakMeasurement = maxResourceMeasurement(peakMeasurement, r.resourceProbe.Measure(ctx, r.config.Generator))
			}
			if result.Passed() {
				passed++
			} else {
				card.Certified = false
				if result.FailureStage != "" {
					card.FailureCounts[result.FailureStage]++
				}
			}
		}
		card.Cases = append(card.Cases, CaseSummary{
			Case: c.Name, Trials: r.config.Trials, Passed: passed,
			PassRate: float64(passed) / float64(r.config.Trials),
			Overall:  scoreRange(overall), Relevance: scoreRange(relevance), Serendipity: scoreRange(serendipity),
		})
	}
	if r.config.Contract != nil {
		assessment := assessCertification(card.Results, r.config.Contract.Thresholds, peakMeasurement)
		card.Assessment = &assessment
		if !assessment.Passed {
			card.Certified = false
		}
	}
	card.RunSnapshot = buildScorecardRunSnapshot(card, resourceBudgetEnabled(r.config.ResourceBudget) && suiteUsage.uncertain == "" && suiteUsage.calls > 0)
	return card
}

func maxResourceMeasurement(current, sample ResourceMeasurement) ResourceMeasurement {
	if sample.Status != "measured" {
		if current.Source == "" {
			current.Source = sample.Source
		}
		return current
	}
	if current.Status != "measured" {
		current = sample
	}
	if sample.PeakRAMBytes > current.PeakRAMBytes {
		current.PeakRAMBytes = sample.PeakRAMBytes
	}
	if sample.PeakVRAMBytes > current.PeakVRAMBytes {
		current.PeakVRAMBytes = sample.PeakVRAMBytes
	}
	return current
}

func assessCertification(results []Result, thresholds CertificationThresholds, measurement ResourceMeasurement) CertificationAssessment {
	assessment := CertificationAssessment{Passed: true}
	groundedExpected, grounded := 0, 0
	toolExpected, correctTool := 0, 0
	policyExpected, policyAccurate := 0, 0
	proposalExpected, proposalQuality := 0, 0
	recoveryExpected, recoverySuccessful := 0, 0
	validSchema := 0
	var runLatencies []int64
	toolCalls := make([]int, 0, len(results))
	for _, result := range results {
		if result.GroundedCompletionExpected {
			groundedExpected++
			if result.GroundedCompletion {
				grounded++
			}
		}
		if result.ToolOperationExpected {
			toolExpected++
			if result.CorrectToolOperation {
				correctTool++
			}
		}
		if result.SchemaValid {
			validSchema++
		}
		if result.PolicyAccuracyExpected {
			policyExpected++
			if result.PolicyAccurate {
				policyAccurate++
			}
		}
		if result.ProposalQualityExpected {
			proposalExpected++
			if result.ProposalQuality {
				proposalQuality++
			}
		}
		if result.RecoveryExpected {
			recoveryExpected++
			if result.RecoverySuccessful {
				recoverySuccessful++
			}
		}
		toolCalls = append(toolCalls, result.ToolCalls)
		var runLatency int64
		latencyKnown := len(result.GeneratorCalls) > 0
		for _, call := range result.GeneratorCalls {
			if call.LatencyNanos <= 0 || runLatency > math.MaxInt64-call.LatencyNanos {
				latencyKnown = false
				break
			}
			runLatency += call.LatencyNanos
		}
		if latencyKnown {
			runLatencies = append(runLatencies, runLatency)
		}
	}
	assessment.GroundedCompletionRate = fractionOrOne(grounded, groundedExpected)
	assessment.CorrectToolOperationRate = fractionOrOne(correctTool, toolExpected)
	assessment.SchemaValidityRate = fractionOrOne(validSchema, len(results))
	assessment.PolicyAccuracyRate = fractionOrOne(policyAccurate, policyExpected)
	assessment.ProposalQualityRate = fractionOrOne(proposalQuality, proposalExpected)
	assessment.RecoveryRate = fractionOrOne(recoverySuccessful, recoveryExpected)
	assessment.Performance = performanceSummary(runLatencies, toolCalls, measurement)
	if assessment.GroundedCompletionRate < thresholds.MinGroundedCompletionRate {
		assessment.Failures = append(assessment.Failures, fmt.Sprintf("grounded completion rate %.3f < %.3f", assessment.GroundedCompletionRate, thresholds.MinGroundedCompletionRate))
	}
	if assessment.CorrectToolOperationRate < thresholds.MinCorrectToolOperationRate {
		assessment.Failures = append(assessment.Failures, fmt.Sprintf("correct tool operation rate %.3f < %.3f", assessment.CorrectToolOperationRate, thresholds.MinCorrectToolOperationRate))
	}
	if assessment.SchemaValidityRate < thresholds.MinSchemaValidityRate {
		assessment.Failures = append(assessment.Failures, fmt.Sprintf("schema validity rate %.3f < %.3f", assessment.SchemaValidityRate, thresholds.MinSchemaValidityRate))
	}
	if assessment.PolicyAccuracyRate < thresholds.MinPolicyAccuracyRate {
		assessment.Failures = append(assessment.Failures, fmt.Sprintf("policy accuracy rate %.3f < %.3f", assessment.PolicyAccuracyRate, thresholds.MinPolicyAccuracyRate))
	}
	if assessment.ProposalQualityRate < thresholds.MinProposalQualityRate {
		assessment.Failures = append(assessment.Failures, fmt.Sprintf("proposal quality rate %.3f < %.3f", assessment.ProposalQualityRate, thresholds.MinProposalQualityRate))
	}
	if assessment.RecoveryRate < thresholds.MinRecoveryRate {
		assessment.Failures = append(assessment.Failures, fmt.Sprintf("recovery rate %.3f < %.3f", assessment.RecoveryRate, thresholds.MinRecoveryRate))
	}
	if thresholds.MaxP95ToolCalls > 0 && assessment.Performance.P95ToolCalls > thresholds.MaxP95ToolCalls {
		assessment.Failures = append(assessment.Failures, fmt.Sprintf("p95 tool calls %d > %d", assessment.Performance.P95ToolCalls, thresholds.MaxP95ToolCalls))
	}
	assessment.Passed = len(assessment.Failures) == 0
	return assessment
}

func proposalQualityMatches(c Case, proposal suggest.Proposal, err error) bool {
	if c.ExpectedProposalAbstention {
		return len(allItems(proposal)) == 0 && errors.Is(err, suggest.ErrNoGroundedTitles)
	}
	if len(c.ExpectedProposalKeys) == 0 {
		return true
	}
	actual := make(map[provision.Key]bool, len(allItems(proposal)))
	for _, item := range allItems(proposal) {
		key, keyErr := item.Key()
		if keyErr != nil {
			return false
		}
		actual[key] = true
	}
	if len(actual) != len(c.ExpectedProposalKeys) {
		return false
	}
	for _, expected := range c.ExpectedProposalKeys {
		if !actual[expected] {
			return false
		}
	}
	return true
}

func performanceSummary(runLatencies []int64, toolCalls []int, measurement ResourceMeasurement) PerformanceSummary {
	slices.Sort(runLatencies)
	slices.Sort(toolCalls)
	return PerformanceSummary{
		MeasuredRuns:             len(runLatencies),
		GeneratorLatencyP50Nanos: percentileInt64(runLatencies, 0.50),
		GeneratorLatencyP95Nanos: percentileInt64(runLatencies, 0.95),
		P95ToolCalls:             percentileInt(toolCalls, 0.95),
		ResourceStatus:           measurement.Status, ResourceSource: measurement.Source,
		PeakRAMBytes: measurement.PeakRAMBytes, PeakVRAMBytes: measurement.PeakVRAMBytes,
	}
}

func percentileInt64(sorted []int64, percentile float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	return sorted[max(index, 0)]
}

func percentileInt(sorted []int, percentile float64) int {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	return sorted[max(index, 0)]
}

func fractionOrOne(numerator, denominator int) float64 {
	if denominator == 0 {
		return 1
	}
	return float64(numerator) / float64(denominator)
}

func expectedToolOperationFailure(expected string, observation Observation) string {
	var calls int
	switch expected {
	case "":
		return ""
	case "title":
		calls = observation.TitleCalls
	case "genre":
		calls = observation.GenreCalls
	case "keyword":
		calls = observation.KeywordCalls
	case "network":
		calls = observation.NetworkCalls
	case "cast":
		calls = observation.CastCalls
	case "creator":
		calls = observation.CreatorCalls
	default:
		return fmt.Sprintf("unknown expected tool operation %q", expected)
	}
	if calls == 0 {
		return fmt.Sprintf("expected %s catalog operation was not used", expected)
	}
	return ""
}

func groundingFailureStage(groundingStage string) FailureStage {
	switch groundingStage {
	case "no_tool_call", "retrieval_empty":
		return FailureStageRetrieval
	default:
		return FailureStageGeneration
	}
}

func validateJudgeScores(scores JudgeScores) error {
	for _, score := range []struct {
		name  string
		value float64
	}{
		{name: "overall", value: scores.Overall},
		{name: "relevance", value: scores.Relevance},
		{name: "serendipity", value: scores.Serendipity},
	} {
		if math.IsNaN(score.value) || math.IsInf(score.value, 0) || score.value < 0 || score.value > 1 {
			return fmt.Errorf("invalid judge evidence: %s score must be finite and within 0..1", score.name)
		}
	}
	if strings.TrimSpace(scores.Reason) == "" {
		return fmt.Errorf("invalid judge evidence: reason must be non-blank")
	}
	return nil
}

func requiresSchedule(c Case) bool {
	return len(c.RequireScheduledPrograms) > 0 || len(c.ForbidScheduledPrograms) > 0 || len(c.RequireScheduledSequence) > 0 ||
		(c.TitleEvidence == TitleEvidenceScheduled && (len(c.RequireTitles) > 0 || len(c.ForbidTitles) > 0))
}

func scoreRange(values []float64) ScoreRange {
	if len(values) == 0 {
		return ScoreRange{}
	}
	values = slices.Clone(values)
	slices.Sort(values)
	middle := len(values) / 2
	median := values[middle]
	if len(values)%2 == 0 {
		median = (values[middle-1] + values[middle]) / 2
	}
	return ScoreRange{Min: values[0], Median: median, Max: values[len(values)-1]}
}

type resourceAccumulator struct {
	calls     int
	tokens    int
	spend     exactDecimal
	uncertain string
}

func newResourceAccumulator() *resourceAccumulator {
	return &resourceAccumulator{spend: zeroDecimal()}
}

func (a *resourceAccumulator) usage() ResourceUsage {
	return ResourceUsage{Calls: a.calls, Tokens: a.tokens, Spend: a.spend.String()}
}

func consumeResourceCalls(limits ResourceBudget, run, suite *resourceAccumulator, calls []InferenceCall) string {
	if !resourceBudgetEnabled(limits) {
		return ""
	}
	runCalls, ok := checkedAdd(run.calls, len(calls))
	if !ok {
		return latchResourceUncertainty(run, suite, "budget_exhausted: per-run call usage overflow")
	}
	suiteCalls, ok := checkedAdd(suite.calls, len(calls))
	if !ok {
		return latchResourceUncertainty(run, suite, "budget_exhausted: suite call usage overflow")
	}
	run.calls, suite.calls = runCalls, suiteCalls
	for _, call := range calls {
		tokens, ok := checkedAdd(call.Tokens.Prompt, call.Tokens.Completion)
		if !ok {
			return latchResourceUncertainty(run, suite, "budget_exhausted: provider token usage is invalid or overflowing")
		}
		if tokenBudgetEnabled(limits) && tokens == 0 {
			return latchResourceUncertainty(run, suite, "budget_exhausted: provider token usage is missing")
		}
		runTokens, ok := checkedAdd(run.tokens, tokens)
		if !ok {
			return latchResourceUncertainty(run, suite, "budget_exhausted: per-run token usage overflow")
		}
		suiteTokens, ok := checkedAdd(suite.tokens, tokens)
		if !ok {
			return latchResourceUncertainty(run, suite, "budget_exhausted: suite token usage overflow")
		}
		run.tokens, suite.tokens = runTokens, suiteTokens
		if call.ChargeStatus == InferenceChargeReported {
			if call.Charge.Currency != "USD" {
				return latchResourceUncertainty(run, suite, "budget_exhausted: non-USD provider charge cannot satisfy the declared USD budget")
			}
			charge, valid := parseExactDecimal(call.Charge.Amount)
			if !valid {
				return latchResourceUncertainty(run, suite, "budget_exhausted: provider charge is invalid")
			}
			run.spend = run.spend.add(charge)
			suite.spend = suite.spend.add(charge)
		} else if spendBudgetEnabled(limits) && call.RequestedProvider != "ollama" {
			return latchResourceUncertainty(run, suite, "budget_exhausted: provider spend attribution is missing or invalid")
		}
	}
	return resourceBudgetOverLimit(limits, run, suite)
}

func consumeGeneratorObservation(limits ResourceBudget, run, suite *resourceAccumulator, observation Observation) string {
	if !resourceBudgetEnabled(limits) {
		return ""
	}
	if observation.generatorUsageErr != "" {
		return latchResourceUncertainty(run, suite, "budget_exhausted: "+observation.generatorUsageErr)
	}
	runCalls, ok := checkedAdd(run.calls, observation.ModelCalls)
	if !ok {
		return latchResourceUncertainty(run, suite, "budget_exhausted: per-run call usage overflow")
	}
	suiteCalls, ok := checkedAdd(suite.calls, observation.ModelCalls)
	if !ok {
		return latchResourceUncertainty(run, suite, "budget_exhausted: suite call usage overflow")
	}
	run.calls, suite.calls = runCalls, suiteCalls
	if tokenBudgetEnabled(limits) && !observation.generatorTokenKnown {
		return latchResourceUncertainty(run, suite, "budget_exhausted: generator token usage is missing")
	}
	if spendBudgetEnabled(limits) && !observation.generatorSpendKnown {
		return latchResourceUncertainty(run, suite, "budget_exhausted: generator spend attribution is missing or invalid")
	}
	runTokens, ok := checkedAdd(run.tokens, observation.generatorTokens)
	if !ok {
		return latchResourceUncertainty(run, suite, "budget_exhausted: per-run token usage overflow")
	}
	suiteTokens, ok := checkedAdd(suite.tokens, observation.generatorTokens)
	if !ok {
		return latchResourceUncertainty(run, suite, "budget_exhausted: suite token usage overflow")
	}
	run.tokens, suite.tokens = runTokens, suiteTokens
	if observation.generatorSpend.coefficient != nil {
		run.spend = run.spend.add(observation.generatorSpend)
		suite.spend = suite.spend.add(observation.generatorSpend)
	}
	return resourceBudgetOverLimit(limits, run, suite)
}

func resourceBudgetBeforeNextCall(limits ResourceBudget, run, suite *resourceAccumulator, includeRun bool) string {
	if !resourceBudgetEnabled(limits) {
		return ""
	}
	if suite.uncertain != "" {
		return suite.uncertain
	}
	if limits.MaxCallsPerSuite > 0 && suite.calls >= limits.MaxCallsPerSuite {
		return "budget_exhausted: suite call ceiling reached before provider call"
	}
	if limits.MaxTokensPerSuite > 0 && suite.tokens >= limits.MaxTokensPerSuite {
		return "budget_exhausted: suite token ceiling reached before provider call"
	}
	if limits.MaxSpendPerSuite != "" {
		maxSuiteSpend, valid := parseExactDecimal(limits.MaxSpendPerSuite)
		if !valid {
			return "budget_exhausted: suite spend ceiling is invalid"
		}
		if suite.spend.cmp(maxSuiteSpend) >= 0 {
			return "budget_exhausted: suite spend ceiling reached before provider call"
		}
	}
	if includeRun {
		if limits.MaxCallsPerRun > 0 && run.calls >= limits.MaxCallsPerRun {
			return "budget_exhausted: per-run call ceiling reached before provider call"
		}
		if limits.MaxTokensPerRun > 0 && run.tokens >= limits.MaxTokensPerRun {
			return "budget_exhausted: per-run token ceiling reached before provider call"
		}
		if limits.MaxSpendPerRun != "" {
			maxRunSpend, valid := parseExactDecimal(limits.MaxSpendPerRun)
			if !valid {
				return "budget_exhausted: per-run spend ceiling is invalid"
			}
			if run.spend.cmp(maxRunSpend) >= 0 {
				return "budget_exhausted: per-run spend ceiling reached before provider call"
			}
		}
	}
	return ""
}

func latchResourceUncertainty(run, suite *resourceAccumulator, message string) string {
	if run.uncertain == "" {
		run.uncertain = message
	}
	if suite.uncertain == "" {
		suite.uncertain = message
	}
	return message
}

func resourceBudgetOverLimit(limits ResourceBudget, run, suite *resourceAccumulator) string {
	if (limits.MaxCallsPerRun > 0 && run.calls > limits.MaxCallsPerRun) ||
		(limits.MaxCallsPerSuite > 0 && suite.calls > limits.MaxCallsPerSuite) {
		return "budget_exhausted: provider calls exceeded a declared ceiling"
	}
	if (limits.MaxTokensPerRun > 0 && run.tokens > limits.MaxTokensPerRun) ||
		(limits.MaxTokensPerSuite > 0 && suite.tokens > limits.MaxTokensPerSuite) {
		return "budget_exhausted: provider token usage exceeded a declared ceiling"
	}
	if limits.MaxSpendPerRun != "" {
		maxRunSpend, valid := parseExactDecimal(limits.MaxSpendPerRun)
		if !valid || run.spend.cmp(maxRunSpend) > 0 {
			return "budget_exhausted: provider spend exceeded an invalid or declared per-run ceiling"
		}
	}
	if limits.MaxSpendPerSuite != "" {
		maxSuiteSpend, valid := parseExactDecimal(limits.MaxSpendPerSuite)
		if !valid || suite.spend.cmp(maxSuiteSpend) > 0 {
			return "budget_exhausted: provider spend exceeded an invalid or declared suite ceiling"
		}
	}
	return ""
}

func resourceBudgetEnabled(limits ResourceBudget) bool {
	return limits.MaxCallsPerRun > 0 || limits.MaxCallsPerSuite > 0 ||
		limits.MaxTokensPerRun > 0 || limits.MaxSpendPerRun != "" ||
		limits.MaxTokensPerSuite > 0 || limits.MaxSpendPerSuite != ""
}

func tokenBudgetEnabled(limits ResourceBudget) bool {
	return limits.MaxTokensPerRun > 0 || limits.MaxTokensPerSuite > 0
}

func spendBudgetEnabled(limits ResourceBudget) bool {
	return limits.MaxSpendPerRun != "" || limits.MaxSpendPerSuite != ""
}
