// Command filler-temporal-structure-window-assess-openrouter performs one complete, serial,
// truth-blind assessor-family run over the locked long-reel window corpus.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/loomarr/loomarr/cmd/internal/fillerbakeoffio"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/store"
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
	OutputPath            string
	BaseURL               string
	APIKey                string
}

type commandResult struct {
	Cases                         int
	Windows                       int
	CompleteStitches              int
	HeldStitches                  int
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

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, capabilities{execute: execute}))
}

func run(args []string, stdout, stderr io.Writer, capability capabilities) int {
	flags := flag.NewFlagSet("filler-temporal-structure-window-assess-openrouter", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifest := flags.String("window-set", "", "complete public prepared-window manifest")
	preflight := flags.String("preflight", "", "passing immutable provider-free certification preflight")
	snapshot := flags.String("snapshot", "", "fresh immutable OpenRouter capability snapshot")
	model := flags.String("model", "", "concrete video-capable OpenRouter model ID")
	modelFamily := flags.String("model-family", "", "independent model-family identity")
	provider := flags.String("provider", "", "exact upstream provider display name")
	providerSlug := flags.String("provider-slug", "", "exact upstream provider routing slug")
	assessorID := flags.String("assessor-id", "", "identity unique to this family run")
	reasoningMode := flags.String("reasoning-mode", "", "exact reasoning contract: disabled or provider_required")
	maximumInputTokens := flags.Int64("maximum-input-tokens", 0, "worst-case input-token allowance for route price preflight")
	reservationNanoUSD := flags.Int64("reservation-nanousd", 0, "per-request accounting reservation; not a provider billing cap")
	maxRequests := flags.Int("max-requests", 0, "hard request ceiling; must equal the manifest's complete window count")
	maxSpendNanoUSD := flags.Int64("max-spend-nanousd", 0, "hard aggregate accounting ceiling in nano-USD")
	ledger := flags.String("ledger", "", "durable SQLite ledger path; reused to resume safely")
	evidence := flags.String("evidence", "", "durable private content-addressed evidence directory")
	output := flags.String("out", "", "new immutable truth-blind family result JSON")
	baseURL := flags.String("base-url", fillerbakeoff.OpenRouterBaseURL, "OpenRouter API base URL")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	config := commandConfig{
		WindowSetManifestPath: *manifest, PreflightPath: *preflight, SnapshotPath: *snapshot, Model: *model, ModelFamily: *modelFamily,
		UpstreamProvider: *provider, UpstreamProviderSlug: *providerSlug, AssessorID: *assessorID,
		ReasoningMode: *reasoningMode, MaximumInputTokens: *maximumInputTokens,
		ReservationNanoUSD: *reservationNanoUSD, MaxRequests: *maxRequests, MaxSpendNanoUSD: *maxSpendNanoUSD,
		LedgerPath: *ledger, EvidenceRoot: *evidence, OutputPath: *output, BaseURL: *baseURL,
		APIKey: os.Getenv("OPENROUTER_API_KEY"),
	}
	if config.APIKey == "" || config.WindowSetManifestPath == "" || config.PreflightPath == "" || config.SnapshotPath == "" ||
		config.Model == "" || config.ModelFamily == "" || config.UpstreamProvider == "" ||
		config.UpstreamProviderSlug == "" || config.AssessorID == "" || config.ReasoningMode == "" ||
		config.MaximumInputTokens <= 0 || config.ReservationNanoUSD <= 0 || config.MaxRequests <= 0 ||
		config.MaxSpendNanoUSD <= 0 || config.LedgerPath == "" || config.EvidenceRoot == "" || config.OutputPath == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-assess-openrouter: credential, complete public window set, passing preflight, fresh snapshot, exact route/model identity, positive request/cost ceilings, durable ledger/evidence, and output are required")
		return 2
	}
	result, err := capability.execute(context.Background(), config)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-assess-openrouter:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-window-assess-openrouter: assessed %d cases/%d windows serially in %d provider requests; complete=%d held=%d; charged=%d accounted=%d nano-USD unknown=%d; per-request snapshot price bound=%d nano-USD; training=false production=false; sha256 %s; %s\n",
		result.Cases, result.Windows, result.ProviderRequests, result.CompleteStitches, result.HeldStitches,
		result.ChargedNanoUSD, result.AccountedNanoUSD, result.UnknownChargeReservations,
		result.EstimatedMaximumChargeNanoUSD, result.ArtifactFileSHA256, config.OutputPath)
	return 0
}

func execute(ctx context.Context, config commandConfig) (commandResult, error) {
	manifestPath, err := filepath.Abs(config.WindowSetManifestPath)
	if err != nil {
		return commandResult{}, err
	}
	snapshotPath, err := filepath.Abs(config.SnapshotPath)
	if err != nil {
		return commandResult{}, err
	}
	ledgerPath, err := filepath.Abs(config.LedgerPath)
	if err != nil {
		return commandResult{}, err
	}
	evidenceRoot, err := filepath.Abs(config.EvidenceRoot)
	if err != nil {
		return commandResult{}, err
	}
	outputPath, err := filepath.Abs(config.OutputPath)
	if err != nil {
		return commandResult{}, err
	}
	if _, err := os.Lstat(outputPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return commandResult{}, errors.New("output path must not already exist")
	}
	manifest, manifestSHA, err := fillerreview.LoadTemporalStructureWindowSetPublic(manifestPath, fillerreview.TemporalStructureWindowCorpusCases)
	if err != nil {
		return commandResult{}, err
	}
	windows, err := validateAuthorizedWindowRun(manifest, config.MaxRequests, config.ReservationNanoUSD, config.MaxSpendNanoUSD)
	if err != nil {
		return commandResult{}, err
	}
	preflightPath, err := filepath.Abs(config.PreflightPath)
	if err != nil {
		return commandResult{}, err
	}
	preflight, _, err := fillerreview.LoadTemporalStructureWindowPreflight(preflightPath, manifestSHA)
	if err != nil {
		return commandResult{}, err
	}
	if preflight.WindowRequestsPerFamily != windows {
		return commandResult{}, errors.New("window preflight request topology drifted")
	}
	snapshot, err := fillerbakeoffio.ReadStrictJSON[fillerbakeoff.OpenRouterSnapshot](snapshotPath)
	if err != nil {
		return commandResult{}, fmt.Errorf("read snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		return commandResult{}, err
	}
	database, err := store.Open(ctx, "sqlite://"+ledgerPath, true)
	if err != nil {
		return commandResult{}, fmt.Errorf("open durable window-call ledger: %w", err)
	}
	defer func() { _ = database.Close() }()
	ledger := structureWindowLedger{
		store: database,
		budget: store.InferenceBudget{
			PerClipNanoUSD: config.MaxSpendNanoUSD, PerDayNanoUSD: config.MaxSpendNanoUSD, PerRunNanoUSD: config.MaxSpendNanoUSD,
		},
	}
	family, err := fillerreview.NewTemporalStructureWindowOpenRouterFamily(fillerreview.TemporalStructureWindowOpenRouterFamilyConfig{
		BaseURL: config.BaseURL, APIKey: config.APIKey, Snapshot: snapshot, Model: config.Model,
		ModelFamily: config.ModelFamily, UpstreamProvider: config.UpstreamProvider,
		UpstreamProviderSlug: config.UpstreamProviderSlug, AssessorID: config.AssessorID,
		ReasoningMode: config.ReasoningMode, ReservationNanoUSD: config.ReservationNanoUSD,
		MaximumInputTokens: config.MaximumInputTokens, EvidenceRoot: evidenceRoot, Ledger: ledger,
	})
	if err != nil {
		return commandResult{}, err
	}
	result, err := fillerreview.RunTemporalStructureWindowFamily(ctx, fillerreview.TemporalStructureWindowFamilyConfig{
		WindowSetManifestPath: manifestPath, ExpectedCases: fillerreview.TemporalStructureWindowCorpusCases,
		CapabilitySnapshotSHA256: fillerbakeoff.OpenRouterSnapshotSHA256(snapshot),
		Family:                   family.Runtime, Now: time.Now,
	})
	if err != nil {
		return commandResult{}, err
	}
	fileSHA, err := fillerreview.PublishTemporalStructureWindowFamilyResult(outputPath, manifestPath, result)
	if err != nil {
		return commandResult{}, err
	}
	summary := commandResult{
		Cases: len(result.Cases), Windows: windows,
		ProviderRequests: result.ProviderRequests, ChargedNanoUSD: result.ChargedNanoUSD,
		AccountedNanoUSD: result.AccountedNanoUSD, UnknownChargeReservations: result.UnknownChargeReservations,
		EstimatedMaximumChargeNanoUSD: family.EstimatedMaximumChargeNanoUSD, ArtifactFileSHA256: fileSHA,
	}
	for _, item := range result.Cases {
		switch item.Evidence.Stitch.Status {
		case fillerstructurewindow.StitchComplete:
			summary.CompleteStitches++
		case fillerstructurewindow.StitchHeld:
			summary.HeldStitches++
		}
	}
	return summary, nil
}

func validateAuthorizedWindowRun(manifest fillerreview.TemporalStructureWindowSetManifest, maxRequests int, reservationNanoUSD, maxSpendNanoUSD int64) (int, error) {
	windows := 0
	for _, item := range manifest.Cases {
		if len(item.Windows) == 0 {
			return 0, errors.New("public window set has an invalid request count")
		}
		windows += len(item.Windows)
	}
	if len(manifest.Cases) != fillerreview.TemporalStructureWindowCorpusCases || maxRequests != windows {
		return 0, fmt.Errorf("authorized request ceiling is %d; complete family requires exactly %d windows", maxRequests, windows)
	}
	if reservationNanoUSD <= 0 || maxSpendNanoUSD <= 0 || reservationNanoUSD > maxSpendNanoUSD {
		return 0, errors.New("per-request reservation exceeds aggregate spend ceiling")
	}
	if int64(windows) > maxSpendNanoUSD/reservationNanoUSD {
		return 0, fmt.Errorf("aggregate spend ceiling cannot reserve all %d required window requests", windows)
	}
	return windows, nil
}

type structureWindowLedger struct {
	store  store.FillerStructureAssessmentStore
	budget store.InferenceBudget
}

func (l structureWindowLedger) Reserve(ctx context.Context, reservation fillerstructurewindow.CallReservation) (fillerstructurewindow.CallReservationState, error) {
	return l.store.ReserveStructureWindowCall(ctx, reservation, l.budget)
}

func (l structureWindowLedger) Settle(ctx context.Context, record fillerstructurewindow.CallRecord) error {
	return l.store.SettleStructureWindowCall(ctx, record)
}
