// Command filler-temporal-structure-assess-openrouter performs serial,
// budget-bounded direct-video assessment of a blinded structure challenge.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/loomarr/loomarr/cmd/internal/fillerbakeoffio"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillerreview"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

type aliasList []string

func (values *aliasList) String() string { return strings.Join(*values, ",") }
func (values *aliasList) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("empty alias")
	}
	*values = append(*values, value)
	return nil
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filler-temporal-structure-assess-openrouter", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifest := flags.String("challenge", "", "verified public temporal-structure challenge manifest")
	var aliases aliasList
	flags.Var(&aliases, "alias", "one blinded alias to assess; repeat; omit for the complete challenge")
	snapshotPath := flags.String("snapshot", "", "fresh immutable OpenRouter capability snapshot")
	model := flags.String("model", "", "concrete video-capable OpenRouter model ID")
	modelFamily := flags.String("model-family", "", "model family identity")
	provider := flags.String("provider", "", "exact upstream provider display name")
	providerSlug := flags.String("provider-slug", "", "exact upstream provider routing slug")
	assessorID := flags.String("assessor-id", "", "identity unique to this challenge run")
	reasoningMode := flags.String("reasoning-mode", "", "exact reasoning contract: disabled or provider_required")
	expectedCases := flags.Int("expected-cases", 0, "exact selected case count")
	perCaseTimeout := flags.Duration("per-case-timeout", 10*time.Minute, "hard timeout for each serial video")
	maxRequests := flags.Int("max-requests", 0, "hard paid request ceiling; must equal expected cases")
	maxSpendNanoUSD := flags.Int64("max-spend-nanousd", 0, "hard total paid spend ceiling in nano-USD")
	reservationNanoUSD := flags.Int64("reservation-nanousd", 0, "per-request accounting reservation in nano-USD; not a provider-enforced cap")
	maximumInputTokens := flags.Int64("maximum-input-tokens", 0, "declared worst-case provider input-token allowance used for price preflight")
	baseURL := flags.String("base-url", fillerbakeoff.OpenRouterBaseURL, "OpenRouter API base URL")
	output := flags.String("out", "", "new immutable raw result JSON path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" || *manifest == "" || *snapshotPath == "" || *model == "" || *modelFamily == "" || *provider == "" || *providerSlug == "" || *assessorID == "" || *reasoningMode == "" || *expectedCases <= 0 || *maxRequests <= 0 || *maxSpendNanoUSD <= 0 || *reservationNanoUSD <= 0 || *maximumInputTokens <= 0 || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-assess-openrouter: credential, public challenge, snapshot, exact route/model identity, positive case/request/cost ceilings, assessor, and output are required")
		return 2
	}
	snapshot, err := fillerbakeoffio.ReadStrictJSON[fillerbakeoff.OpenRouterSnapshot](*snapshotPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-assess-openrouter: read snapshot:", err)
		return 1
	}
	result, err := fillerreview.RunOpenRouterTemporalStructure(context.Background(), fillerreview.TemporalStructureOpenRouterConfig{
		PublicManifestPath: *manifest, CaseAliases: aliases, CheckpointDir: *output + ".private",
		BaseURL: *baseURL, APIKey: apiKey, Snapshot: snapshot, Model: *model, ModelFamily: *modelFamily,
		UpstreamProvider: *provider, UpstreamProviderSlug: *providerSlug, AssessorID: *assessorID,
		ReasoningMode: *reasoningMode, ExpectedCases: *expectedCases, PerCaseTimeout: *perCaseTimeout,
		MaxRequests: *maxRequests, MaxSpendNanoUSD: *maxSpendNanoUSD, ReservationNanoUSD: *reservationNanoUSD,
		MaximumInputTokens: *maximumInputTokens,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-assess-openrouter: assess:", err)
		return 1
	}
	if err := fillerbakeoffio.WriteImmutableJSON(*output, ".filler-temporal-structure-openrouter-*", result); err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-assess-openrouter: publish:", err)
		return 1
	}
	failures := 0
	for _, assessment := range result.Assessments {
		if assessment.OperationalFailure != nil {
			failures++
		}
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-assess-openrouter: assessed %d cases in %d requests; failures=%d; charged %d nano-USD; productionAdmissionAllowed=%t; %s\n", len(result.Assessments), result.Requests, failures, result.ChargedNanoUSD, result.ProductionAdmissionAllowed, *output)
	return 0
}
