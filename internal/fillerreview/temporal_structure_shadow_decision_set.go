package fillerreview

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

const (
	TemporalStructureShadowDecisionSetSchemaVersion   = 1
	TemporalStructureShadowDecisionSetContractVersion = "filler-temporal-structure-shadow-decision-set-v1"
)

type TemporalStructureWindowShadowDecisionSetConfig struct {
	WindowSetManifestPath string
	FirstFamilyPath       string
	SecondFamilyPath      string
	DecidedAt             time.Time
	OutputPath            string
}

type TemporalStructureShadowDecisionFamily struct {
	Assessor         fillerstructure.AssessorProfile `json:"assessor"`
	ResultSHA256     string                          `json:"resultSha256"`
	ResultFileSHA256 string                          `json:"resultFileSha256"`
}

type TemporalStructureShadowDecisionCase struct {
	Alias    string                   `json:"alias"`
	Artifact fillerstructure.Artifact `json:"artifact"`
}

// TemporalStructureShadowDecisionSet is one truth-blind representation's complete set of
// production reducer artifacts. Family-result identities retain the evidence chain behind it.
type TemporalStructureShadowDecisionSet struct {
	SchemaVersion              int                                     `json:"schemaVersion"`
	ContractVersion            string                                  `json:"contractVersion"`
	WindowSetManifestSHA256    string                                  `json:"windowSetManifestSha256"`
	InputKind                  fillerstructure.AssessmentInputKind     `json:"inputKind"`
	ReducerVersion             string                                  `json:"reducerVersion"`
	BoundaryToleranceMS        int64                                   `json:"boundaryToleranceMs"`
	Families                   []TemporalStructureShadowDecisionFamily `json:"families"`
	DecidedAt                  time.Time                               `json:"decidedAt"`
	Cases                      []TemporalStructureShadowDecisionCase   `json:"cases"`
	TrainingAllowed            bool                                    `json:"trainingAllowed"`
	ProductionAdmissionAllowed bool                                    `json:"productionAdmissionAllowed"`
	SHA256                     string                                  `json:"sha256"`
}

// PublishTemporalStructureWindowShadowDecisionSet projects two complete blinded family runs
// through the production reducer and publishes one immutable window-representation decision set.
func PublishTemporalStructureWindowShadowDecisionSet(config TemporalStructureWindowShadowDecisionSetConfig) (TemporalStructureShadowDecisionSet, string, error) {
	for _, path := range []string{config.WindowSetManifestPath, config.FirstFamilyPath, config.SecondFamilyPath, config.OutputPath} {
		if strings.TrimSpace(path) == "" {
			return TemporalStructureShadowDecisionSet{}, "", errors.New("window shadow decision set requires manifest, two family results, and output paths")
		}
	}
	if config.DecidedAt.IsZero() || config.DecidedAt != config.DecidedAt.UTC() {
		return TemporalStructureShadowDecisionSet{}, "", errors.New("window shadow decision set requires canonical UTC decision time")
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(config.WindowSetManifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		return TemporalStructureShadowDecisionSet{}, "", err
	}
	first, firstFileSHA, err := LoadTemporalStructureWindowFamilyResult(config.FirstFamilyPath, config.WindowSetManifestPath)
	if err != nil {
		return TemporalStructureShadowDecisionSet{}, "", fmt.Errorf("load first window shadow family: %w", err)
	}
	second, secondFileSHA, err := LoadTemporalStructureWindowFamilyResult(config.SecondFamilyPath, config.WindowSetManifestPath)
	if err != nil {
		return TemporalStructureShadowDecisionSet{}, "", fmt.Errorf("load second window shadow family: %w", err)
	}
	if config.DecidedAt.Before(first.CompletedAt) || config.DecidedAt.Before(second.CompletedAt) {
		return TemporalStructureShadowDecisionSet{}, "", errors.New("window shadow decision set predates a family result")
	}
	families := []TemporalStructureShadowDecisionFamily{
		{Assessor: first.Assessor, ResultSHA256: first.SHA256, ResultFileSHA256: firstFileSHA},
		{Assessor: second.Assessor, ResultSHA256: second.SHA256, ResultFileSHA256: secondFileSHA},
	}
	slices.SortFunc(families, func(left, right TemporalStructureShadowDecisionFamily) int {
		return strings.Compare(left.Assessor.ID, right.Assessor.ID)
	})
	set := TemporalStructureShadowDecisionSet{
		SchemaVersion: TemporalStructureShadowDecisionSetSchemaVersion, ContractVersion: TemporalStructureShadowDecisionSetContractVersion,
		WindowSetManifestSHA256: manifestSHA, InputKind: fillerstructure.AssessmentInputWindowMediaSet,
		ReducerVersion: fillerstructure.ReducerContractVersion, BoundaryToleranceMS: fillerstructurewindowcert.BoundaryToleranceMS,
		Families: families, DecidedAt: config.DecidedAt,
		Cases: make([]TemporalStructureShadowDecisionCase, 0, len(manifest.Cases)),
	}
	for index, item := range manifest.Cases {
		artifact, err := temporalStructureWindowShadowArtifact(item, first.Cases[index], second.Cases[index], config.DecidedAt)
		if err != nil {
			return TemporalStructureShadowDecisionSet{}, "", fmt.Errorf("build window shadow case %q: %w", item.Alias, err)
		}
		set.Cases = append(set.Cases, TemporalStructureShadowDecisionCase{Alias: item.Alias, Artifact: artifact})
	}
	slices.SortFunc(set.Cases, func(left, right TemporalStructureShadowDecisionCase) int {
		return strings.Compare(left.Alias, right.Alias)
	})
	set.SHA256 = temporalStructureShadowDecisionSetSHA256(set)
	if err := validateTemporalStructureShadowDecisionSetAgainstManifest(set, manifest, manifestSHA); err != nil {
		return TemporalStructureShadowDecisionSet{}, "", err
	}
	raw, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return TemporalStructureShadowDecisionSet{}, "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return TemporalStructureShadowDecisionSet{}, "", fmt.Errorf("publish window shadow decision set: %w", err)
	}
	return set, hashBytes(raw), nil
}

func temporalStructureWindowShadowArtifact(public TemporalStructureWindowSetPublicCase, first, second TemporalStructureWindowFamilyCase, decidedAt time.Time) (fillerstructure.Artifact, error) {
	if first.Alias != public.Alias || second.Alias != public.Alias ||
		first.Evidence.Stitch.MediaSet.SHA256 != public.MediaSet.SHA256 || second.Evidence.Stitch.MediaSet.SHA256 != public.MediaSet.SHA256 {
		return fillerstructure.Artifact{}, errors.New("window shadow family case drifted from public input")
	}
	input, firstCandidate, err := fillerstructurewindow.ReducerCandidate(first.Evidence.Stitch)
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	secondInput, secondCandidate, err := fillerstructurewindow.ReducerCandidate(second.Evidence.Stitch)
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	if input.SHA256 != secondInput.SHA256 {
		return fillerstructure.Artifact{}, errors.New("window shadow family candidates do not share input")
	}
	return fillerstructure.NewArtifact(fillerstructure.Request{
		Source: public.MediaSet.Plan.Source, Input: input, BoundaryToleranceMS: fillerstructurewindowcert.BoundaryToleranceMS,
		Candidates: []fillerstructure.Candidate{firstCandidate, secondCandidate},
	}, decidedAt)
}

func temporalStructureShadowDecisionSetSHA256(set TemporalStructureShadowDecisionSet) string {
	set.SHA256 = ""
	return temporalTruthJSONSHA(set)
}
