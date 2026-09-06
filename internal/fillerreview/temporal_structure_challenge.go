package fillerreview

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	TemporalStructureChallengeSchemaVersion   = 1
	TemporalStructureChallengeContractVersion = "filler-temporal-structure-challenge-v3"

	TemporalStructureSourceBoundedItem     = "independently_bounded_item"
	TemporalStructureSourceProgrammeParent = "programme_parent"
)

// TemporalStructureChallengeMedia is the construction seam. The builder owns
// blinding, authority, validation, hashing, and atomic publication; an adapter
// owns only media probing and deterministic rendering.
type TemporalStructureChallengeMedia interface {
	Identity() TemporalTruthMediaIdentity
	Probe(context.Context, string) (TemporalTruthVideoInfo, error)
	Render(context.Context, []TemporalStructureRenderSegment, string) (TemporalStructureRenderResult, error)
}

type TemporalStructureRenderSegment struct {
	SourcePath string
	StartMS    int64
	DurationMS int64
}

type TemporalStructureRenderResult struct {
	Video TemporalTruthVideoInfo
	Parts []TemporalStructureRenderedPart
}

type TemporalStructureRenderedPart struct {
	DurationMS int64
}

type TemporalStructureChallengeConfig struct {
	AuthoringPath   string
	PlanReceiptPath string
	SourceRoot      string
	OutputDir       string
	ChallengeID     string
	Seed            string
	GeneratedAt     time.Time
	Media           TemporalStructureChallengeMedia
}

// TemporalStructureChallengeAuthoring is coordinator-private construction
// authority. It must never be copied into an assessor-facing package.
type TemporalStructureChallengeAuthoring struct {
	SchemaVersion   int                                `json:"schemaVersion"`
	ContractVersion string                             `json:"contractVersion"`
	Sources         []TemporalStructureChallengeSource `json:"sources"`
	Cases           []TemporalStructureChallengeCase   `json:"cases"`
}

type TemporalStructureChallengeSource struct {
	ID             string                            `json:"id"`
	Path           string                            `json:"path"`
	SHA256         string                            `json:"sha256"`
	DurationMS     int64                             `json:"durationMs"`
	Provenance     TemporalStructureSourceProvenance `json:"provenance"`
	StandaloneRole fillereval.TemporalRole           `json:"standaloneRole,omitempty"`
}

type TemporalStructureSourceProvenance struct {
	Kind           string    `json:"kind"`
	Authority      string    `json:"authority"`
	Reference      string    `json:"reference"`
	MetadataSHA256 string    `json:"metadataSha256"`
	RetrievedAt    time.Time `json:"retrievedAt"`
}

type TemporalStructureChallengeCase struct {
	ID       string                              `json:"id"`
	Unit     fillereval.UnitKind                 `json:"unit"`
	Role     fillereval.TemporalRole             `json:"role,omitempty"`
	Segments []TemporalStructureChallengeSegment `json:"segments"`
}

type TemporalStructureChallengeSegment struct {
	SourceID   string `json:"sourceId"`
	StartMS    int64  `json:"startMs"`
	DurationMS int64  `json:"durationMs"`
}

// TemporalStructureChallengeManifest is the complete assessor-facing
// surface. It deliberately contains no source identity, construction class,
// role, boundary, tool path, or authoring digest.
type TemporalStructureChallengeManifest struct {
	SchemaVersion              int                                    `json:"schemaVersion"`
	ContractVersion            string                                 `json:"contractVersion"`
	ChallengeID                string                                 `json:"challengeId"`
	GeneratedAt                time.Time                              `json:"generatedAt"`
	Cases                      []TemporalStructureChallengePublicCase `json:"cases"`
	ProductionAdmissionAllowed bool                                   `json:"productionAdmissionAllowed"`
}

type TemporalStructureChallengePublicCase struct {
	Alias   string                    `json:"alias"`
	Video   TemporalTruthEvidenceFile `json:"video"`
	Profile TemporalTruthVideoProfile `json:"profile"`
}

type TemporalStructureChallengeAuthority struct {
	SchemaVersion        int                                       `json:"schemaVersion"`
	ContractVersion      string                                    `json:"contractVersion"`
	ChallengeID          string                                    `json:"challengeId"`
	GeneratedAt          time.Time                                 `json:"generatedAt"`
	AuthoringSHA256      string                                    `json:"authoringSha256"`
	PlanContractVersion  string                                    `json:"planContractVersion"`
	PlanReceiptSHA256    string                                    `json:"planReceiptSha256"`
	SeedSHA256           string                                    `json:"seedSha256"`
	PublicManifestSHA256 string                                    `json:"publicManifestSha256"`
	MediaTools           TemporalTruthMediaIdentity                `json:"mediaTools"`
	Cases                []TemporalStructureChallengeAuthorityCase `json:"cases"`
}

type TemporalStructureChallengeAuthorityCase struct {
	Alias       string                                    `json:"alias"`
	CaseID      string                                    `json:"caseId"`
	Unit        fillereval.UnitKind                       `json:"unit"`
	Role        fillereval.TemporalRole                   `json:"role,omitempty"`
	VideoSHA256 string                                    `json:"videoSha256"`
	JoinTimesMS []int64                                   `json:"joinTimesMs,omitempty"`
	Segments    []TemporalStructureChallengeAuthorityPart `json:"segments"`
}

type TemporalStructureChallengeAuthorityPart struct {
	Ordinal          int                               `json:"ordinal"`
	SourceID         string                            `json:"sourceId"`
	SourcePath       string                            `json:"sourcePath"`
	SourceSHA256     string                            `json:"sourceSha256"`
	SourceDurationMS int64                             `json:"sourceDurationMs"`
	SourceRole       fillereval.TemporalRole           `json:"sourceRole,omitempty"`
	SourceStartMS    int64                             `json:"sourceStartMs"`
	RequestedMS      int64                             `json:"requestedDurationMs"`
	RenderedMS       int64                             `json:"renderedDurationMs"`
	OutputStartMS    int64                             `json:"outputStartMs"`
	OutputEndMS      int64                             `json:"outputEndMs"`
	Provenance       TemporalStructureSourceProvenance `json:"provenance"`
}

type TemporalStructureChallengeResult struct {
	Cases                int
	PublicManifestSHA256 string
	AuthoritySHA256      string
}

type temporalStructurePreparedCase struct {
	spec     TemporalStructureChallengeCase
	alias    string
	order    string
	segments []TemporalStructureRenderSegment
	sources  []TemporalStructureChallengeSource
}

// BuildTemporalStructureChallenge validates independent authority before it
// renders anything, then atomically publishes separate public and private
// roots. The same inputs, seed, media implementation, and timestamp reproduce
// byte-identical authority files.
func BuildTemporalStructureChallenge(ctx context.Context, config TemporalStructureChallengeConfig) (TemporalStructureChallengeResult, error) {
	if err := validateTemporalStructureChallengeConfig(config); err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	authoringRaw, authoring, err := loadTemporalStructureChallengeAuthoring(config.AuthoringPath)
	if err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	receiptRaw, err := os.ReadFile(config.PlanReceiptPath)
	if err != nil {
		return TemporalStructureChallengeResult{}, fmt.Errorf("read challenge plan receipt: %w", err)
	}
	receipt, err := readStrictJSON[TemporalStructureHoldoutReceipt](config.PlanReceiptPath)
	if err != nil {
		return TemporalStructureChallengeResult{}, fmt.Errorf("decode challenge plan receipt: %w", err)
	}
	if receipt.AuthoringSHA256 != hashBytes(authoringRaw) {
		return TemporalStructureChallengeResult{}, fmt.Errorf("challenge plan receipt does not bind authoring bytes")
	}
	if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, nil); err != nil {
		return TemporalStructureChallengeResult{}, fmt.Errorf("validate challenge plan receipt: %w", err)
	}
	return buildTemporalStructureChallenge(ctx, config, authoringRaw, authoring, &receipt, hashBytes(receiptRaw), receipt.ContractVersion)
}

func loadTemporalStructureChallengeAuthoring(path string) ([]byte, TemporalStructureChallengeAuthoring, error) {
	authoringRaw, err := os.ReadFile(path)
	if err != nil {
		return nil, TemporalStructureChallengeAuthoring{}, fmt.Errorf("read challenge authoring: %w", err)
	}
	var authoring TemporalStructureChallengeAuthoring
	decoder := json.NewDecoder(strings.NewReader(string(authoringRaw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&authoring); err != nil {
		return nil, TemporalStructureChallengeAuthoring{}, fmt.Errorf("decode challenge authoring: %w", err)
	}
	return authoringRaw, authoring, nil
}

func buildTemporalStructureChallenge(ctx context.Context, config TemporalStructureChallengeConfig, authoringRaw []byte, authoring TemporalStructureChallengeAuthoring, receipt *TemporalStructureHoldoutReceipt, receiptSHA, planContract string) (TemporalStructureChallengeResult, error) {
	prepared, err := prepareTemporalStructureChallenge(config, authoring)
	if err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	if err := verifyTemporalStructureSources(ctx, config, prepared); err != nil {
		return TemporalStructureChallengeResult{}, err
	}

	stage, err := beginTemporalTruthEvidenceStage(config.OutputDir)
	if err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	defer stage.Cleanup()
	publicRoot := filepath.Join(stage.path, "public")
	privateRoot := filepath.Join(stage.path, "private")
	if err := os.MkdirAll(publicRoot, 0o750); err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		return TemporalStructureChallengeResult{}, err
	}

	manifest := TemporalStructureChallengeManifest{
		SchemaVersion: TemporalStructureChallengeSchemaVersion, ContractVersion: TemporalStructureChallengeContractVersion,
		ChallengeID: config.ChallengeID, GeneratedAt: config.GeneratedAt.UTC(), ProductionAdmissionAllowed: false,
		Cases: make([]TemporalStructureChallengePublicCase, 0, len(prepared)),
	}
	authority := TemporalStructureChallengeAuthority{
		SchemaVersion: TemporalStructureChallengeSchemaVersion, ContractVersion: TemporalStructureChallengeContractVersion,
		ChallengeID: config.ChallengeID, GeneratedAt: config.GeneratedAt.UTC(), AuthoringSHA256: hashBytes(authoringRaw),
		PlanContractVersion: planContract, PlanReceiptSHA256: receiptSHA,
		SeedSHA256: hashBytes([]byte(config.Seed)), MediaTools: config.Media.Identity(),
		Cases: make([]TemporalStructureChallengeAuthorityCase, 0, len(prepared)),
	}
	for _, item := range prepared {
		publicCase, authorityCase, err := buildTemporalStructureChallengeCase(ctx, config, publicRoot, item)
		if err != nil {
			return TemporalStructureChallengeResult{}, fmt.Errorf("build challenge case %q: %w", item.spec.ID, err)
		}
		manifest.Cases = append(manifest.Cases, publicCase)
		authority.Cases = append(authority.Cases, authorityCase)
	}
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	manifestRaw = append(manifestRaw, '\n')
	authority.PublicManifestSHA256 = hashBytes(manifestRaw)
	authorityRaw, err := json.MarshalIndent(authority, "", "  ")
	if err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	authorityRaw = append(authorityRaw, '\n')
	if err := writeTemporalTruthNew(filepath.Join(publicRoot, "manifest.json"), manifestRaw, 0o640); err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	if err := writeTemporalTruthNew(filepath.Join(privateRoot, "authority.json"), authorityRaw, 0o600); err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	if err := auditTemporalStructureChallengeLeakage(publicRoot, authoring, receipt); err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	if err := stage.Publish(); err != nil {
		return TemporalStructureChallengeResult{}, err
	}
	return TemporalStructureChallengeResult{Cases: len(prepared), PublicManifestSHA256: authority.PublicManifestSHA256, AuthoritySHA256: hashBytes(authorityRaw)}, nil
}
