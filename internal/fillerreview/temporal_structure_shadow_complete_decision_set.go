package fillerreview

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

type TemporalStructureCompleteShadowDecisionSetConfig struct {
	WindowSetManifestPath string
	FirstFamilyPath       string
	SecondFamilyPath      string
	DecidedAt             time.Time
	OutputPath            string
}

// PublishTemporalStructureCompleteShadowDecisionSet projects two complete-video family artifacts
// through the production reducer without opening private construction truth.
func PublishTemporalStructureCompleteShadowDecisionSet(config TemporalStructureCompleteShadowDecisionSetConfig) (TemporalStructureShadowDecisionSet, string, error) {
	for _, path := range []string{config.WindowSetManifestPath, config.FirstFamilyPath, config.SecondFamilyPath, config.OutputPath} {
		if strings.TrimSpace(path) == "" {
			return TemporalStructureShadowDecisionSet{}, "", errors.New("complete shadow decision set requires manifest, two family results, and output paths")
		}
	}
	if config.DecidedAt.IsZero() || config.DecidedAt != config.DecidedAt.UTC() {
		return TemporalStructureShadowDecisionSet{}, "", errors.New("complete shadow decision set requires canonical UTC decision time")
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(config.WindowSetManifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		return TemporalStructureShadowDecisionSet{}, "", err
	}
	first, firstFileSHA, err := LoadTemporalStructureCompleteFamilyResult(config.FirstFamilyPath, config.WindowSetManifestPath)
	if err != nil {
		return TemporalStructureShadowDecisionSet{}, "", fmt.Errorf("load first complete shadow family: %w", err)
	}
	second, secondFileSHA, err := LoadTemporalStructureCompleteFamilyResult(config.SecondFamilyPath, config.WindowSetManifestPath)
	if err != nil {
		return TemporalStructureShadowDecisionSet{}, "", fmt.Errorf("load second complete shadow family: %w", err)
	}
	if config.DecidedAt.Before(first.CompletedAt) || config.DecidedAt.Before(second.CompletedAt) {
		return TemporalStructureShadowDecisionSet{}, "", errors.New("complete shadow decision set predates a family result")
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
		WindowSetManifestSHA256: manifestSHA, InputKind: fillerstructure.AssessmentInputCompleteVideo,
		ReducerVersion: fillerstructure.ReducerContractVersion, BoundaryToleranceMS: fillerstructurewindowcert.BoundaryToleranceMS,
		Families: families, DecidedAt: config.DecidedAt,
		Cases: make([]TemporalStructureShadowDecisionCase, 0, len(manifest.Cases)),
	}
	for index, item := range manifest.Cases {
		artifact, err := temporalStructureCompleteShadowArtifact(item, first.Cases[index], second.Cases[index], config.DecidedAt)
		if err != nil {
			return TemporalStructureShadowDecisionSet{}, "", fmt.Errorf("build complete shadow case %q: %w", item.Alias, err)
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
		return TemporalStructureShadowDecisionSet{}, "", fmt.Errorf("publish complete shadow decision set: %w", err)
	}
	return set, hashBytes(raw), nil
}

func temporalStructureCompleteShadowArtifact(public TemporalStructureWindowSetPublicCase, first, second TemporalStructureCompleteFamilyCase, decidedAt time.Time) (fillerstructure.Artifact, error) {
	if first.Alias != public.Alias || second.Alias != public.Alias {
		return fillerstructure.Artifact{}, errors.New("complete shadow family case drifted from public input")
	}
	firstCandidate, err := first.Evidence.Record.Candidate()
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	secondCandidate, err := second.Evidence.Record.Candidate()
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	input, err := fillerstructure.NewCompleteVideoInput(first.Evidence.Record.Source, first.Evidence.Record.Media)
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	secondInput, err := fillerstructure.NewCompleteVideoInput(second.Evidence.Record.Source, second.Evidence.Record.Media)
	if err != nil {
		return fillerstructure.Artifact{}, err
	}
	if input.SHA256 != secondInput.SHA256 {
		return fillerstructure.Artifact{}, errors.New("complete shadow family candidates do not share exact media")
	}
	return fillerstructure.NewArtifact(fillerstructure.Request{
		Source: input.Source, Input: input, BoundaryToleranceMS: fillerstructurewindowcert.BoundaryToleranceMS,
		Candidates: []fillerstructure.Candidate{firstCandidate, secondCandidate},
	}, decidedAt)
}
