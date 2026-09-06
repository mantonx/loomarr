// Command filler-temporal-structure-complete-assess-openrouter performs one complete, serial,
// truth-blind complete-video family run over the locked short-versus-long shadow corpus.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
)

type commandConfig struct {
	WindowSetManifestPath string
	PreflightPath         string
	SnapshotPath          string
	Model                 string
	ModelFamily           string
	UpstreamProvider      string
	UpstreamProviderSlug  string
	AssessorID            string
	ReasoningMode         string
	MaximumInputTokens    int64
	ReservationNanoUSD    int64
	MaxRequests           int
	MaxSpendNanoUSD       int64
	LedgerPath            string
	EvidenceRoot          string
	MediaRoot             string
	FFmpegPath            string
	OutputPath            string
	BaseURL               string
	APIKey                string
}

type commandResult struct {
	Cases                         int
	ProviderRequests              int
	ChargedNanoUSD                int64
	AccountedNanoUSD              int64
	UnknownChargeReservations     int
	EstimatedMaximumChargeNanoUSD int64
	ArtifactFileSHA256            string
}

type capabilities struct {
	execute func(context.Context, commandConfig) (commandResult, error)
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, capabilities{execute: execute})) }

func run(args []string, stdout, stderr io.Writer, capability capabilities) int {
	flags := flag.NewFlagSet("filler-temporal-structure-complete-assess-openrouter", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifest := flags.String("window-set", "", "complete public prepared-window manifest")
	preflight := flags.String("preflight", "", "passing immutable provider-free certification preflight")
	snapshot := flags.String("snapshot", "", "fresh immutable OpenRouter capability snapshot")
	model := flags.String("model", "", "concrete video-capable OpenRouter model ID")
	modelFamily := flags.String("model-family", "", "independent model-family identity")
	provider := flags.String("provider", "", "exact upstream provider display name")
	providerSlug := flags.String("provider-slug", "", "exact upstream provider routing slug")
	assessorID := flags.String("assessor-id", "", "identity unique to this complete-video family run")
	reasoningMode := flags.String("reasoning-mode", "", "exact reasoning contract: disabled or provider_required")
	maximumInputTokens := flags.Int64("maximum-input-tokens", 0, "worst-case input-token allowance for route price preflight")
	reservationNanoUSD := flags.Int64("reservation-nanousd", 0, "per-request accounting reservation; not a provider billing cap")
	maxRequests := flags.Int("max-requests", 0, "hard request ceiling; must equal the manifest's complete case count")
	maxSpendNanoUSD := flags.Int64("max-spend-nanousd", 0, "hard aggregate accounting ceiling in nano-USD")
	ledger := flags.String("ledger", "", "durable SQLite ledger path; reused to resume safely")
	evidence := flags.String("evidence", "", "durable private content-addressed call evidence directory")
	mediaRoot := flags.String("media-root", "", "durable private root for canonical complete-video derivatives")
	ffmpeg := flags.String("ffmpeg", "ffmpeg", "ffmpeg executable or absolute path")
	output := flags.String("out", "", "new immutable truth-blind complete-family result JSON")
	baseURL := flags.String("base-url", fillerbakeoff.OpenRouterBaseURL, "OpenRouter API base URL")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	config := commandConfig{
		WindowSetManifestPath: *manifest, PreflightPath: *preflight, SnapshotPath: *snapshot, Model: *model, ModelFamily: *modelFamily,
		UpstreamProvider: *provider, UpstreamProviderSlug: *providerSlug, AssessorID: *assessorID,
		ReasoningMode: *reasoningMode, MaximumInputTokens: *maximumInputTokens,
		ReservationNanoUSD: *reservationNanoUSD, MaxRequests: *maxRequests, MaxSpendNanoUSD: *maxSpendNanoUSD,
		LedgerPath: *ledger, EvidenceRoot: *evidence, MediaRoot: *mediaRoot, FFmpegPath: *ffmpeg,
		OutputPath: *output, BaseURL: *baseURL, APIKey: os.Getenv("OPENROUTER_API_KEY"),
	}
	if config.APIKey == "" || config.WindowSetManifestPath == "" || config.PreflightPath == "" || config.SnapshotPath == "" ||
		config.Model == "" || config.ModelFamily == "" || config.UpstreamProvider == "" ||
		config.UpstreamProviderSlug == "" || config.AssessorID == "" || config.ReasoningMode == "" ||
		config.MaximumInputTokens <= 0 || config.ReservationNanoUSD <= 0 || config.MaxRequests <= 0 ||
		config.MaxSpendNanoUSD <= 0 || config.LedgerPath == "" || config.EvidenceRoot == "" ||
		config.MediaRoot == "" || config.OutputPath == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-complete-assess-openrouter: credential, complete public window set, passing preflight, fresh snapshot, exact route/model identity, positive request/cost ceilings, durable ledger/evidence/media roots, and output are required")
		return 2
	}
	result, err := capability.execute(context.Background(), config)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-complete-assess-openrouter:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-complete-assess-openrouter: assessed %d cases serially in %d provider requests; charged=%d accounted=%d nano-USD unknown=%d; per-request snapshot price bound=%d nano-USD; training=false production=false; sha256 %s; %s\n",
		result.Cases, result.ProviderRequests, result.ChargedNanoUSD, result.AccountedNanoUSD,
		result.UnknownChargeReservations, result.EstimatedMaximumChargeNanoUSD, result.ArtifactFileSHA256, config.OutputPath)
	return 0
}
