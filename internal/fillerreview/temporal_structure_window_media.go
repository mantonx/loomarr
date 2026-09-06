package fillerreview

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/mediatools"
)

const (
	TemporalStructureWindowMediaSchemaVersion   = 1
	TemporalStructureWindowMediaContractVersion = "filler-temporal-structure-window-media-v1"
	TemporalStructureWindowMaximumSourceBytes   = mediatools.ConditioningMaxSnapshotBytes
)

// TemporalStructureWindowCorpusMedia is the one media seam used by the atomic corpus renderer.
// The production adapter uses ffmpeg; tests use an in-memory-authority adapter at the same seam.
type TemporalStructureWindowCorpusMedia interface {
	TemporalStructureChallengeMedia
	Decode(context.Context, string) error
}

type TemporalStructureWindowMediaConfig struct {
	PlanPath             string
	HoldoutAuthoringPath string
	HoldoutReceiptPath   string
	SourceRoot           string
	Seed                 string
	RenderedAt           time.Time
	OutputDir            string
	Media                TemporalStructureWindowCorpusMedia
}

type TemporalStructureWindowMediaManifest struct {
	SchemaVersion                int                                      `json:"schemaVersion"`
	ContractVersion              string                                   `json:"contractVersion"`
	RenderedAt                   time.Time                                `json:"renderedAt"`
	CorpusPlanSHA256             string                                   `json:"corpusPlanSha256"`
	AssessmentMediaProfileSHA256 string                                   `json:"assessmentMediaProfileSha256"`
	Cases                        []TemporalStructureWindowMediaPublicCase `json:"cases"`
	TrainingAllowed              bool                                     `json:"trainingAllowed"`
	ProductionAdmissionAllowed   bool                                     `json:"productionAdmissionAllowed"`
}

type TemporalStructureWindowMediaPublicCase struct {
	Alias  string                       `json:"alias"`
	Source filler.SplitSourceAsset      `json:"source"`
	Plan   fillerstructurewindow.Plan   `json:"plan"`
	Video  TemporalStructureWindowVideo `json:"video"`
}

type TemporalStructureWindowVideo struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type TemporalStructureWindowMediaAuthority struct {
	SchemaVersion        int                                         `json:"schemaVersion"`
	ContractVersion      string                                      `json:"contractVersion"`
	RenderedAt           time.Time                                   `json:"renderedAt"`
	CorpusPlanFileSHA256 string                                      `json:"corpusPlanFileSha256"`
	CorpusPlan           TemporalStructureWindowCorpusPlan           `json:"corpusPlan"`
	PublicManifestSHA256 string                                      `json:"publicManifestSha256"`
	MediaTools           TemporalTruthMediaIdentity                  `json:"mediaTools"`
	Cases                []TemporalStructureWindowMediaAuthorityCase `json:"cases"`
	TrainingAllowed      bool                                        `json:"trainingAllowed"`
	ProductionAllowed    bool                                        `json:"productionAllowed"`
}

type TemporalStructureWindowMediaAuthorityCase struct {
	Alias                    string                                    `json:"alias"`
	CaseID                   string                                    `json:"caseId"`
	ObservedTargetBoundaryMS int64                                     `json:"observedTargetBoundaryMs,omitempty"`
	Parts                    []TemporalStructureChallengeAuthorityPart `json:"parts"`
	Truth                    []fillerstructure.Segment                 `json:"truth"`
}

type TemporalStructureWindowMediaResult struct {
	Cases                  int
	PublicManifestSHA256   string
	PrivateAuthoritySHA256 string
}

type temporalStructureWindowPreparedCase struct {
	planCase TemporalStructureWindowCorpusCase
	media    temporalStructurePreparedCase
}

// BuildTemporalStructureWindowCorpusMedia renders the fixed plan and publishes a complete public
// source surface plus its private known-truth authority. It performs no model call and grants no
// training or production authority.
func BuildTemporalStructureWindowCorpusMedia(ctx context.Context, config TemporalStructureWindowMediaConfig) (TemporalStructureWindowMediaResult, error) {
	plan, planRaw, authoring, receipt, prepared, err := prepareTemporalStructureWindowCorpusMedia(config)
	if err != nil {
		return TemporalStructureWindowMediaResult{}, err
	}
	mediaCases := make([]temporalStructurePreparedCase, len(prepared))
	for index := range prepared {
		mediaCases[index] = prepared[index].media
	}
	if err := verifyTemporalStructureSources(ctx, TemporalStructureChallengeConfig{SourceRoot: config.SourceRoot, Media: config.Media}, mediaCases); err != nil {
		return TemporalStructureWindowMediaResult{}, fmt.Errorf("verify window corpus sources: %w", err)
	}
	stage, err := beginTemporalTruthEvidenceStage(config.OutputDir)
	if err != nil {
		return TemporalStructureWindowMediaResult{}, err
	}
	defer stage.Cleanup()
	publicRoot, privateRoot := filepath.Join(stage.path, "public"), filepath.Join(stage.path, "private")
	if err := os.MkdirAll(publicRoot, 0o750); err != nil {
		return TemporalStructureWindowMediaResult{}, err
	}
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		return TemporalStructureWindowMediaResult{}, err
	}

	manifest := TemporalStructureWindowMediaManifest{
		SchemaVersion: TemporalStructureWindowMediaSchemaVersion, ContractVersion: TemporalStructureWindowMediaContractVersion,
		RenderedAt: config.RenderedAt.UTC(), CorpusPlanSHA256: plan.SHA256,
		AssessmentMediaProfileSHA256: fillerstructuremedia.CanonicalProfile().SHA256,
		Cases:                        make([]TemporalStructureWindowMediaPublicCase, 0, len(prepared)),
	}
	authority := TemporalStructureWindowMediaAuthority{
		SchemaVersion: TemporalStructureWindowMediaSchemaVersion, ContractVersion: TemporalStructureWindowMediaContractVersion,
		RenderedAt: config.RenderedAt.UTC(), CorpusPlanFileSHA256: hashBytes(planRaw), CorpusPlan: plan,
		MediaTools: config.Media.Identity(), Cases: make([]TemporalStructureWindowMediaAuthorityCase, 0, len(prepared)),
	}
	for _, item := range prepared {
		publicCase, authorityCase, err := buildTemporalStructureWindowMediaCase(ctx, config.Media, publicRoot, item)
		if err != nil {
			return TemporalStructureWindowMediaResult{}, fmt.Errorf("render window corpus case %q: %w", item.planCase.ID, err)
		}
		manifest.Cases = append(manifest.Cases, publicCase)
		authority.Cases = append(authority.Cases, authorityCase)
	}
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return TemporalStructureWindowMediaResult{}, err
	}
	manifestRaw = append(manifestRaw, '\n')
	authority.PublicManifestSHA256 = hashBytes(manifestRaw)
	authorityRaw, err := json.MarshalIndent(authority, "", "  ")
	if err != nil {
		return TemporalStructureWindowMediaResult{}, err
	}
	authorityRaw = append(authorityRaw, '\n')
	manifestPath := filepath.Join(publicRoot, "manifest.json")
	if err := writeTemporalTruthNew(manifestPath, manifestRaw, 0o640); err != nil {
		return TemporalStructureWindowMediaResult{}, err
	}
	if err := writeTemporalTruthNew(filepath.Join(privateRoot, "authority.json"), authorityRaw, 0o600); err != nil {
		return TemporalStructureWindowMediaResult{}, err
	}
	if err := auditTemporalStructureWindowMediaLeakage(publicRoot, manifestRaw, authoring, receipt, plan); err != nil {
		return TemporalStructureWindowMediaResult{}, err
	}
	if err := validateTemporalStructureWindowMedia(manifestPath, manifest, authority, authority.PublicManifestSHA256); err != nil {
		return TemporalStructureWindowMediaResult{}, err
	}
	if err := stage.Publish(); err != nil {
		return TemporalStructureWindowMediaResult{}, err
	}
	return TemporalStructureWindowMediaResult{
		Cases: len(prepared), PublicManifestSHA256: authority.PublicManifestSHA256,
		PrivateAuthoritySHA256: hashBytes(authorityRaw),
	}, nil
}

func prepareTemporalStructureWindowCorpusMedia(config TemporalStructureWindowMediaConfig) (TemporalStructureWindowCorpusPlan, []byte, TemporalStructureChallengeAuthoring, TemporalStructureHoldoutReceipt, []temporalStructureWindowPreparedCase, error) {
	if strings.TrimSpace(config.PlanPath) == "" || strings.TrimSpace(config.HoldoutAuthoringPath) == "" ||
		strings.TrimSpace(config.HoldoutReceiptPath) == "" || strings.TrimSpace(config.SourceRoot) == "" ||
		strings.TrimSpace(config.Seed) == "" || config.RenderedAt.IsZero() || strings.TrimSpace(config.OutputDir) == "" || config.Media == nil {
		return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil,
			fmt.Errorf("window corpus rendering requires plan, locked authoring and receipt, source root, private seed, fixed render time, output, and media adapter")
	}
	planRaw, err := os.ReadFile(config.PlanPath)
	if err != nil {
		return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil, fmt.Errorf("read window corpus plan: %w", err)
	}
	plan, err := readStrictJSON[TemporalStructureWindowCorpusPlan](config.PlanPath)
	if err != nil {
		return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil, fmt.Errorf("decode window corpus plan: %w", err)
	}
	authoringRaw, err := os.ReadFile(config.HoldoutAuthoringPath)
	if err != nil {
		return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil, fmt.Errorf("read window corpus authoring: %w", err)
	}
	receiptRaw, err := os.ReadFile(config.HoldoutReceiptPath)
	if err != nil {
		return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil, fmt.Errorf("read window corpus receipt: %w", err)
	}
	authoring, err := readStrictJSON[TemporalStructureChallengeAuthoring](config.HoldoutAuthoringPath)
	if err != nil {
		return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil, fmt.Errorf("decode window corpus authoring: %w", err)
	}
	receipt, err := readStrictJSON[TemporalStructureHoldoutReceipt](config.HoldoutReceiptPath)
	if err != nil {
		return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil, fmt.Errorf("decode window corpus receipt: %w", err)
	}
	if hashBytes(authoringRaw) != plan.HoldoutAuthoringSHA256 || hashBytes(receiptRaw) != plan.HoldoutReceiptSHA256 ||
		hashBytes(authoringRaw) != receipt.AuthoringSHA256 {
		return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil, fmt.Errorf("window corpus plan does not bind supplied authority bytes")
	}
	if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, nil); err != nil {
		return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil, err
	}
	if err := validateTemporalStructureWindowCorpusPlan(plan, authoring, receipt, config.Seed); err != nil {
		return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil, err
	}
	sources := make(map[string]TemporalStructureChallengeSource, len(authoring.Sources))
	for index, source := range authoring.Sources {
		if err := validateTemporalStructureSource(config.SourceRoot, source); err != nil {
			return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil, fmt.Errorf("window corpus source %d: %w", index, err)
		}
		if _, duplicate := sources[source.ID]; duplicate {
			return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil, fmt.Errorf("window corpus repeats source %q", source.ID)
		}
		sources[source.ID] = source
	}
	prepared := make([]temporalStructureWindowPreparedCase, 0, len(plan.Cases))
	challengeConfig := TemporalStructureChallengeConfig{SourceRoot: config.SourceRoot, Seed: config.Seed}
	for _, item := range plan.Cases {
		mediaCase, err := prepareTemporalStructureCase(challengeConfig, TemporalStructureChallengeCase{
			ID: item.ID, Unit: fillereval.UnitProgrammeSpots, Segments: item.Segments,
		}, sources)
		if err != nil {
			return TemporalStructureWindowCorpusPlan{}, nil, TemporalStructureChallengeAuthoring{}, TemporalStructureHoldoutReceipt{}, nil, fmt.Errorf("prepare window corpus case %q: %w", item.ID, err)
		}
		mediaCase.alias = "case-" + temporalStructureBlindValue(config.Seed, "window-alias:"+item.ID)[:24]
		mediaCase.order = temporalStructureBlindValue(config.Seed, "window-order:"+item.ID)
		prepared = append(prepared, temporalStructureWindowPreparedCase{planCase: item, media: mediaCase})
	}
	sort.Slice(prepared, func(i, j int) bool { return prepared[i].media.order < prepared[j].media.order })
	return plan, planRaw, authoring, receipt, prepared, nil
}
