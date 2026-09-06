package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/loomarr/loomarr/cmd/internal/fillerbakeoffio"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/store"
)

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
	mediaRoot, err := filepath.Abs(config.MediaRoot)
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
	if err := validateAuthorizedCompleteRun(manifest, config.MaxRequests, config.ReservationNanoUSD, config.MaxSpendNanoUSD); err != nil {
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
	if preflight.CompleteVideoRequestsPerFamily != len(manifest.Cases) {
		return commandResult{}, errors.New("window preflight complete-video request topology drifted")
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
		return commandResult{}, fmt.Errorf("open durable complete-call ledger: %w", err)
	}
	defer func() { _ = database.Close() }()
	ledger := structureCompleteLedger{store: database, budget: store.InferenceBudget{
		PerClipNanoUSD: config.MaxSpendNanoUSD, PerDayNanoUSD: config.MaxSpendNanoUSD, PerRunNanoUSD: config.MaxSpendNanoUSD,
	}}
	family, err := fillerreview.NewTemporalStructureCompleteOpenRouterFamily(fillerreview.TemporalStructureCompleteOpenRouterFamilyConfig{
		BaseURL: config.BaseURL, APIKey: config.APIKey, Snapshot: snapshot, Model: config.Model,
		ModelFamily: config.ModelFamily, UpstreamProvider: config.UpstreamProvider,
		UpstreamProviderSlug: config.UpstreamProviderSlug, AssessorID: config.AssessorID,
		ReasoningMode: config.ReasoningMode, ReservationNanoUSD: config.ReservationNanoUSD,
		MaximumInputTokens: config.MaximumInputTokens, EvidenceRoot: evidenceRoot, Ledger: ledger,
	})
	if err != nil {
		return commandResult{}, err
	}
	preparer, err := filler.NewFFmpegStructureAssessmentMediaPreparer(filepath.Dir(manifestPath), mediaRoot, config.FFmpegPath)
	if err != nil {
		return commandResult{}, err
	}
	result, err := fillerreview.RunTemporalStructureCompleteFamily(ctx, fillerreview.TemporalStructureCompleteFamilyConfig{
		WindowSetManifestPath: manifestPath, ExpectedCases: fillerreview.TemporalStructureWindowCorpusCases,
		CapabilitySnapshotSHA256: fillerbakeoff.OpenRouterSnapshotSHA256(snapshot),
		Preparer:                 preparer, Family: family.Runtime, Now: time.Now,
	})
	if err != nil {
		return commandResult{}, err
	}
	fileSHA, err := fillerreview.PublishTemporalStructureCompleteFamilyResult(outputPath, manifestPath, result)
	if err != nil {
		return commandResult{}, err
	}
	return commandResult{
		Cases: len(result.Cases), ProviderRequests: result.ProviderRequests, ChargedNanoUSD: result.ChargedNanoUSD,
		AccountedNanoUSD: result.AccountedNanoUSD, UnknownChargeReservations: result.UnknownChargeReservations,
		EstimatedMaximumChargeNanoUSD: family.EstimatedMaximumChargeNanoUSD, ArtifactFileSHA256: fileSHA,
	}, nil
}

func validateAuthorizedCompleteRun(manifest fillerreview.TemporalStructureWindowSetManifest, maxRequests int, reservationNanoUSD, maxSpendNanoUSD int64) error {
	cases := len(manifest.Cases)
	if cases != fillerreview.TemporalStructureWindowCorpusCases || maxRequests != cases {
		return fmt.Errorf("authorized request ceiling is %d; complete-video family requires exactly %d cases", maxRequests, cases)
	}
	if reservationNanoUSD <= 0 || maxSpendNanoUSD <= 0 || reservationNanoUSD > maxSpendNanoUSD {
		return errors.New("per-request reservation exceeds aggregate spend ceiling")
	}
	if int64(cases) > maxSpendNanoUSD/reservationNanoUSD {
		return fmt.Errorf("aggregate spend ceiling cannot reserve all %d required complete-video requests", cases)
	}
	return nil
}

type structureCompleteLedger struct {
	store  store.FillerStructureAssessmentStore
	budget store.InferenceBudget
}

func (l structureCompleteLedger) Reserve(ctx context.Context, reservation fillerstructure.AssessmentReservation) (fillerstructure.AssessmentReservationState, error) {
	return l.store.ReserveStructureAssessment(ctx, reservation, l.budget)
}

func (l structureCompleteLedger) Settle(ctx context.Context, record fillerstructure.AssessmentRecord) error {
	return l.store.SettleStructureAssessment(ctx, record)
}
