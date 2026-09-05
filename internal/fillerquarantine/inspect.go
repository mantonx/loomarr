package fillerquarantine

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillerreference"
	"github.com/loomarr/loomarr/internal/fillerreview"
)

// Inspect re-establishes acquisition and prior-holdout authority, reopens all
// local media, and returns one deterministic quarantine-only report.
func Inspect(ctx context.Context, config Config) (Report, error) {
	if err := validateConfig(config); err != nil {
		return Report{}, err
	}
	inventoryRaw, err := os.ReadFile(config.InventoryPath)
	if err != nil {
		return Report{}, fmt.Errorf("read inventory: %w", err)
	}
	inventory, err := fillercorpus.DecodeInventoryBytes(inventoryRaw)
	if err != nil {
		return Report{}, err
	}
	ledgerRaw, err := os.ReadFile(config.DownloadLedgerPath)
	if err != nil {
		return Report{}, fmt.Errorf("read download ledger: %w", err)
	}
	ledger, err := fillercorpus.DecodeDownloadLedgerBytes(ledgerRaw)
	if err != nil {
		return Report{}, err
	}
	inventorySHA := fillercorpus.InventorySHA256(inventoryRaw)
	if err := fillercorpus.ValidateQuarantineDownloadLedger(inventory, inventorySHA, ledger); err != nil {
		return Report{}, err
	}
	_, authority, publicSHA, authoritySHA, err := fillerreview.LoadTemporalStructureChallenge(
		config.PriorPublicManifestPath, config.PriorAuthorityPath, config.ExpectedPriorCases,
	)
	if err != nil {
		return Report{}, fmt.Errorf("load prior holdout: %w", err)
	}
	if config.GeneratedAt.Before(ledger.GeneratedAt) || config.GeneratedAt.Before(authority.GeneratedAt) {
		return Report{}, fmt.Errorf("inspection time predates its acquisition or prior-holdout authority")
	}

	report := Report{
		SchemaVersion: SchemaVersion, ContractVersion: ContractVersion, GeneratedAt: config.GeneratedAt.UTC(),
		Inputs: InputIdentity{
			InventorySHA256: inventorySHA, DownloadLedgerSHA256: fillercorpus.InventorySHA256(ledgerRaw),
			PriorPublicManifestSHA256: publicSHA, PriorAuthoritySHA256: authoritySHA,
		},
		MediaTools: config.Media.Identity(), Ceilings: Ceilings{MaxMediaWallTimeMS: config.MaxMediaWallTime.Milliseconds()}, Algorithm: fillerreference.DuplicateAlgorithm,
		Authority: AuthorityDisposition{CopyAndStorage: true, LocalTechnicalInspection: true},
	}
	mediaCtx, cancelMedia := context.WithTimeout(ctx, config.MaxMediaWallTime)
	defer cancelMedia()

	prior, priorFingerprints, unavailable, err := inspectPriorSources(mediaCtx, config, authority)
	if err != nil {
		return Report{}, err
	}
	report.PriorSources = prior
	report.Summary.PriorSources = len(prior)
	report.Summary.UnavailablePriorSources = unavailable

	cases, candidateFingerprints, err := inspectCandidates(mediaCtx, config, inventory, ledger)
	if err != nil {
		return Report{}, err
	}
	report.Cases = cases
	if err := compareCandidates(mediaCtx, &report, candidateFingerprints); err != nil {
		return Report{}, err
	}
	if err := comparePriorExposure(mediaCtx, &report, candidateFingerprints, priorFingerprints, unavailable != 0); err != nil {
		return Report{}, err
	}
	applyExactExposure(&report, authority)
	finalize(&report)
	if err := Validate(report); err != nil {
		return Report{}, fmt.Errorf("validate quarantine inspection: %w", err)
	}
	return report, nil
}

func validateConfig(config Config) error {
	if config.InventoryPath == "" || config.DownloadLedgerPath == "" || config.DownloadRoot == "" || config.PriorPublicManifestPath == "" || config.PriorAuthorityPath == "" || config.PriorSourceRoot == "" || config.ExpectedPriorCases <= 0 || config.MaxMediaWallTime.Milliseconds() <= 0 || config.GeneratedAt.IsZero() || config.Media == nil {
		return fmt.Errorf("inventory, ledger, download root, prior holdout, source root, expected case count, media wall-time ceiling, generated time, and media adapter are required")
	}
	identity := config.Media.Identity()
	for name, tool := range map[string]fillerreview.TemporalTruthToolIdentity{"ffmpeg": identity.FFmpeg, "ffprobe": identity.FFprobe} {
		if strings.TrimSpace(tool.Path) == "" || strings.TrimSpace(tool.Version) == "" || !validSHA256(tool.BinarySHA256) {
			return fmt.Errorf("%s identity is invalid", name)
		}
	}
	return nil
}
