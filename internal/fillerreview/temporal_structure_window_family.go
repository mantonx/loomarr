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
	TemporalStructureWindowFamilySchemaVersion   = 3
	TemporalStructureWindowFamilyContractVersion = "filler-temporal-structure-window-family-result-v3"
)

// TemporalStructureWindowFamily is the deliberately small certification seam for one assessor
// family. Implementations own paid calls, replay, evidence persistence, and source-level stitching.
type TemporalStructureWindowFamily interface {
	Profile() fillerstructure.AssessorProfile
	AssessWithEvidence(context.Context, filler.StructureAssessmentWindowMediaSet) (filler.StructureWindowFamilyEvidence, error)
}

type TemporalStructureWindowFamilyConfig struct {
	WindowSetManifestPath    string
	ExpectedCases            int
	CapabilitySnapshotSHA256 string
	Family                   TemporalStructureWindowFamily
	Now                      func() time.Time
}

// TemporalStructureWindowFamilyResult contains blinded answers from exactly one model family.
// Alias is the only case join key; construction truth never enters the family runner.
type TemporalStructureWindowFamilyResult struct {
	SchemaVersion              int                                 `json:"schemaVersion"`
	ContractVersion            string                              `json:"contractVersion"`
	WindowSetManifestSHA256    string                              `json:"windowSetManifestSha256"`
	CapabilitySnapshotSHA256   string                              `json:"capabilitySnapshotSha256"`
	Assessor                   fillerstructure.AssessorProfile     `json:"assessor"`
	CompletedAt                time.Time                           `json:"completedAt"`
	Cases                      []TemporalStructureWindowFamilyCase `json:"cases"`
	CallRecords                int                                 `json:"callRecords"`
	ProviderRequests           int                                 `json:"providerRequests"`
	ChargedNanoUSD             int64                               `json:"chargedNanoUsd"`
	AccountedNanoUSD           int64                               `json:"accountedNanoUsd"`
	UnknownChargeReservations  int                                 `json:"unknownChargeReservations"`
	TrainingAllowed            bool                                `json:"trainingAllowed"`
	ProductionAdmissionAllowed bool                                `json:"productionAdmissionAllowed"`
	SHA256                     string                              `json:"sha256"`
}

type TemporalStructureWindowFamilyCase struct {
	Alias    string                               `json:"alias"`
	Evidence filler.StructureWindowFamilyEvidence `json:"evidence"`
}

// RunTemporalStructureWindowFamily evaluates every case in manifest order. It returns no partial
// result: durable per-window recovery belongs to the supplied family implementation.
func RunTemporalStructureWindowFamily(ctx context.Context, config TemporalStructureWindowFamilyConfig) (TemporalStructureWindowFamilyResult, error) {
	if config.ExpectedCases != TemporalStructureWindowCorpusCases || !reviewSHA256(config.CapabilitySnapshotSHA256) || config.Family == nil || config.Now == nil {
		return TemporalStructureWindowFamilyResult{}, errors.New("window family run requires the complete certification corpus, family, and clock")
	}
	profile := config.Family.Profile()
	if err := fillerstructure.ValidateAssessorProfile(profile); err != nil {
		return TemporalStructureWindowFamilyResult{}, fmt.Errorf("window family profile: %w", err)
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(config.WindowSetManifestPath, config.ExpectedCases)
	if err != nil {
		return TemporalStructureWindowFamilyResult{}, err
	}
	root := filepath.Dir(config.WindowSetManifestPath)
	result := TemporalStructureWindowFamilyResult{
		SchemaVersion: TemporalStructureWindowFamilySchemaVersion, ContractVersion: TemporalStructureWindowFamilyContractVersion,
		WindowSetManifestSHA256: manifestSHA, CapabilitySnapshotSHA256: config.CapabilitySnapshotSHA256, Assessor: profile,
		Cases: make([]TemporalStructureWindowFamilyCase, 0, len(manifest.Cases)),
	}
	for index, item := range manifest.Cases {
		if err := ctx.Err(); err != nil {
			return TemporalStructureWindowFamilyResult{}, err
		}
		prepared := temporalStructureWindowFamilyMedia(root, item)
		evidence, err := config.Family.AssessWithEvidence(ctx, prepared)
		if err != nil {
			return TemporalStructureWindowFamilyResult{}, fmt.Errorf("assess window family case %d (%s): %w", index, item.Alias, err)
		}
		result.Cases = append(result.Cases, TemporalStructureWindowFamilyCase{Alias: item.Alias, Evidence: evidence})
		accumulateTemporalStructureWindowFamilyAccounting(&result, evidence)
	}
	result.CompletedAt = config.Now().UTC()
	result.SHA256 = temporalStructureWindowFamilySHA256(result)
	if err := validateTemporalStructureWindowFamilyResultAgainstManifest(result, manifest, manifestSHA); err != nil {
		return TemporalStructureWindowFamilyResult{}, err
	}
	return result, nil
}

func accumulateTemporalStructureWindowFamilyAccounting(result *TemporalStructureWindowFamilyResult, evidence filler.StructureWindowFamilyEvidence) {
	for _, record := range evidence.CallRecords {
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
}

func temporalStructureWindowFamilyMedia(root string, item TemporalStructureWindowSetPublicCase) filler.StructureAssessmentWindowMediaSet {
	prepared := filler.StructureAssessmentWindowMediaSet{
		Source: item.Source, Authority: item.MediaSet, Windows: make([]filler.StructureAssessmentWindowMedia, 0, len(item.Windows)),
	}
	for ordinal, entry := range item.Windows {
		prepared.Windows = append(prepared.Windows, filler.StructureAssessmentWindowMedia{
			Window: item.MediaSet.Plan.Windows[ordinal], Media: item.MediaSet.Windows[ordinal],
			FullPath: filepath.Join(root, filepath.FromSlash(entry.Path)),
		})
	}
	return prepared
}

func temporalStructureWindowFamilySHA256(result TemporalStructureWindowFamilyResult) string {
	result.SHA256 = ""
	return temporalTruthJSONSHA(result)
}
