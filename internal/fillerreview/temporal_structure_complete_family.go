package fillerreview

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	TemporalStructureCompleteFamilySchemaVersion   = 2
	TemporalStructureCompleteFamilyContractVersion = "filler-temporal-structure-complete-family-result-v2"
)

type TemporalStructureCompleteFamily interface {
	Profile() fillerstructure.AssessorProfile
	AssessWithEvidence(context.Context, filler.StructureAssessmentMedia) (fillerstructure.RecordedAssessment, error)
}

type TemporalStructureCompleteFamilyConfig struct {
	WindowSetManifestPath    string
	ExpectedCases            int
	CapabilitySnapshotSHA256 string
	Preparer                 filler.StructureAssessmentMediaPreparer
	Family                   TemporalStructureCompleteFamily
	Now                      func() time.Time
}

type TemporalStructureCompleteFamilyResult struct {
	SchemaVersion              int                                   `json:"schemaVersion"`
	ContractVersion            string                                `json:"contractVersion"`
	WindowSetManifestSHA256    string                                `json:"windowSetManifestSha256"`
	CapabilitySnapshotSHA256   string                                `json:"capabilitySnapshotSha256"`
	Assessor                   fillerstructure.AssessorProfile       `json:"assessor"`
	CompletedAt                time.Time                             `json:"completedAt"`
	Cases                      []TemporalStructureCompleteFamilyCase `json:"cases"`
	CallRecords                int                                   `json:"callRecords"`
	ProviderRequests           int                                   `json:"providerRequests"`
	ChargedNanoUSD             int64                                 `json:"chargedNanoUsd"`
	AccountedNanoUSD           int64                                 `json:"accountedNanoUsd"`
	UnknownChargeReservations  int                                   `json:"unknownChargeReservations"`
	TrainingAllowed            bool                                  `json:"trainingAllowed"`
	ProductionAdmissionAllowed bool                                  `json:"productionAdmissionAllowed"`
	SHA256                     string                                `json:"sha256"`
}

type TemporalStructureCompleteFamilyCase struct {
	Alias    string                                 `json:"alias"`
	Evidence filler.StructureCompleteFamilyEvidence `json:"evidence"`
}

// RunTemporalStructureCompleteFamily prepares and assesses every public source in manifest order.
// It returns no partial result; the supplied family runtime owns durable per-call recovery.
func RunTemporalStructureCompleteFamily(ctx context.Context, config TemporalStructureCompleteFamilyConfig) (TemporalStructureCompleteFamilyResult, error) {
	if config.ExpectedCases != TemporalStructureWindowCorpusCases || !reviewSHA256(config.CapabilitySnapshotSHA256) || config.Preparer == nil || config.Family == nil || config.Now == nil {
		return TemporalStructureCompleteFamilyResult{}, errors.New("complete family run requires the complete certification corpus, preparer, family, and clock")
	}
	profile := config.Family.Profile()
	if err := fillerstructure.ValidateAssessorProfile(profile); err != nil {
		return TemporalStructureCompleteFamilyResult{}, fmt.Errorf("complete family profile: %w", err)
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(config.WindowSetManifestPath, config.ExpectedCases)
	if err != nil {
		return TemporalStructureCompleteFamilyResult{}, err
	}
	root := filepath.Dir(config.WindowSetManifestPath)
	result := TemporalStructureCompleteFamilyResult{
		SchemaVersion: TemporalStructureCompleteFamilySchemaVersion, ContractVersion: TemporalStructureCompleteFamilyContractVersion,
		WindowSetManifestSHA256: manifestSHA, CapabilitySnapshotSHA256: config.CapabilitySnapshotSHA256, Assessor: profile,
		Cases: make([]TemporalStructureCompleteFamilyCase, 0, len(manifest.Cases)),
	}
	for index, item := range manifest.Cases {
		if err := ctx.Err(); err != nil {
			return TemporalStructureCompleteFamilyResult{}, err
		}
		sourcePath := filepath.Join(root, filepath.FromSlash(item.Source.Path))
		prepared, err := config.Preparer.Prepare(ctx, filler.StructureAssessmentSource{Source: item.Source, FullPath: sourcePath})
		if err != nil {
			return TemporalStructureCompleteFamilyResult{}, fmt.Errorf("prepare complete family case %d (%s): %w", index, item.Alias, err)
		}
		if prepared.Source != item.Source || !filepath.IsAbs(prepared.FullPath) || filepath.Clean(prepared.FullPath) != prepared.FullPath || prepared.FullPath == sourcePath {
			return TemporalStructureCompleteFamilyResult{}, errors.New("complete family preparer drifted public source or derivative path")
		}
		recorded, err := config.Family.AssessWithEvidence(ctx, prepared)
		if err != nil {
			return TemporalStructureCompleteFamilyResult{}, fmt.Errorf("assess complete family case %d (%s): %w", index, item.Alias, err)
		}
		evidence, err := filler.NewStructureCompleteFamilyEvidence(recorded)
		if err != nil {
			return TemporalStructureCompleteFamilyResult{}, fmt.Errorf("close complete family case %d (%s): %w", index, item.Alias, err)
		}
		result.Cases = append(result.Cases, TemporalStructureCompleteFamilyCase{Alias: item.Alias, Evidence: evidence})
		accumulateTemporalStructureCompleteFamilyAccounting(&result, evidence.Record)
	}
	result.CompletedAt = config.Now().UTC()
	result.SHA256 = temporalStructureCompleteFamilySHA256(result)
	if err := validateTemporalStructureCompleteFamilyResultAgainstManifest(result, manifest, manifestSHA); err != nil {
		return TemporalStructureCompleteFamilyResult{}, err
	}
	return result, nil
}

func accumulateTemporalStructureCompleteFamilyAccounting(result *TemporalStructureCompleteFamilyResult, record fillerstructure.AssessmentRecord) {
	result.CallRecords++
	result.ChargedNanoUSD += record.ChargedNanoUSD
	result.AccountedNanoUSD += record.AccountedNanoUSD
	if record.State != fillerstructure.AssessmentRecordHeldBudget {
		result.ProviderRequests++
	}
	if record.State == fillerstructure.AssessmentRecordUnsettled {
		result.UnknownChargeReservations++
	}
}

func temporalStructureCompleteFamilySHA256(result TemporalStructureCompleteFamilyResult) string {
	result.SHA256 = ""
	return temporalTruthJSONSHA(result)
}
