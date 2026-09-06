package filler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/mediatools"
)

const (
	structureAssessmentWindowLineageSchemaVersion   = 1
	structureAssessmentWindowLineageContractVersion = "filler-structure-assessment-window-lineage-v1"
	structureAssessmentWindowIndexSchemaVersion     = 1
	structureAssessmentWindowIndexContractVersion   = "filler-structure-assessment-window-index-v1"
)

// StructureAssessmentWindowLineage proves which exact source interval, plan, recipe, and tool
// produced one normalized window. It deliberately contains no machine-local path.
type StructureAssessmentWindowLineage struct {
	SchemaVersion   int                                    `json:"schemaVersion"`
	ContractVersion string                                 `json:"contractVersion"`
	OperationSHA256 string                                 `json:"operationSha256"`
	PlanSHA256      string                                 `json:"planSha256"`
	Source          StructureAssessmentMediaSourceIdentity `json:"source"`
	Window          fillerstructurewindow.Window           `json:"window"`
	Profile         fillerstructuremedia.Profile           `json:"profile"`
	Tool            mediatools.MediaToolIdentity           `json:"tool"`
	Media           StructureAssessmentMediaDerivative     `json:"media"`
	SHA256          string                                 `json:"sha256"`
}

type structureAssessmentWindowOperation struct {
	SchemaVersion   int                                    `json:"schemaVersion"`
	ContractVersion string                                 `json:"contractVersion"`
	PlanSHA256      string                                 `json:"planSha256"`
	Source          StructureAssessmentMediaSourceIdentity `json:"source"`
	Window          fillerstructurewindow.Window           `json:"window"`
	ProfileSHA256   string                                 `json:"profileSha256"`
	Tool            mediatools.MediaToolIdentity           `json:"tool"`
}

type structureAssessmentWindowIndex struct {
	SchemaVersion   int    `json:"schemaVersion"`
	ContractVersion string `json:"contractVersion"`
	OperationSHA256 string `json:"operationSha256"`
	LineageSHA256   string `json:"lineageSha256"`
}

func structureAssessmentWindowOperationSHA256(plan fillerstructurewindow.Plan, source StructureAssessmentMediaSourceIdentity, window fillerstructurewindow.Window, profile fillerstructuremedia.Profile, tool mediatools.MediaToolIdentity) string {
	operation := structureAssessmentWindowOperation{
		SchemaVersion:   structureAssessmentWindowLineageSchemaVersion,
		ContractVersion: structureAssessmentWindowLineageContractVersion,
		PlanSHA256:      plan.SHA256, Source: source, Window: window, ProfileSHA256: profile.SHA256, Tool: tool,
	}
	raw, err := json.Marshal(operation)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func structureAssessmentWindowLineageSHA256(lineage StructureAssessmentWindowLineage) string {
	lineage.SHA256 = ""
	raw, err := json.Marshal(lineage)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validateStructureAssessmentWindowLineage(plan fillerstructurewindow.Plan, lineage StructureAssessmentWindowLineage) error {
	if err := fillerstructurewindow.ValidatePlan(plan); err != nil {
		return err
	}
	if lineage.SchemaVersion != structureAssessmentWindowLineageSchemaVersion ||
		lineage.ContractVersion != structureAssessmentWindowLineageContractVersion ||
		lineage.PlanSHA256 != plan.SHA256 || !structureAssessmentWindowSourceMatchesPlan(lineage.Source, plan) ||
		lineage.Window.Ordinal < 0 || lineage.Window.Ordinal >= len(plan.Windows) ||
		lineage.Window != plan.Windows[lineage.Window.Ordinal] ||
		lineage.Profile != fillerstructuremedia.CanonicalProfile() ||
		lineage.Profile.SHA256 != plan.Profile.AssessmentMediaProfileSHA256 ||
		!isContentHash(lineage.OperationSHA256) || !isContentHash(lineage.SHA256) ||
		lineage.SHA256 != structureAssessmentWindowLineageSHA256(lineage) {
		return errors.New("structure assessment window lineage authority is invalid")
	}
	if err := lineage.Tool.Validate(); err != nil {
		return fmt.Errorf("structure assessment window lineage tool: %w", err)
	}
	windowDuration := lineage.Window.MediaEndMS - lineage.Window.MediaStartMS
	if !isContentHash(lineage.Media.SHA256) || lineage.Media.Bytes <= 0 ||
		lineage.Media.Bytes > plan.Profile.MaximumWindowBytes || lineage.Media.DurationMS <= 0 ||
		absoluteDurationDifference(lineage.Media.DurationMS, windowDuration) > plan.Profile.MaximumTimelineDriftMS {
		return errors.New("structure assessment window lineage media is invalid")
	}
	wantOperation := structureAssessmentWindowOperationSHA256(plan, lineage.Source, lineage.Window, lineage.Profile, lineage.Tool)
	if lineage.OperationSHA256 != wantOperation {
		return errors.New("structure assessment window lineage operation does not reproduce")
	}
	return nil
}

func structureAssessmentWindowSourceMatchesPlan(source StructureAssessmentMediaSourceIdentity, plan fillerstructurewindow.Plan) bool {
	return (source.Role == SplitSourceEvidence || source.Role == SplitSourceLegacyPlayback) &&
		isContentHash(source.ClipHash) && source.SHA256 == plan.Source.SHA256 &&
		source.Bytes == plan.Source.Bytes && source.DurationMS == plan.Source.DurationMS
}

func structureAssessmentWindowLineagePath(clipDir, digest string) string {
	return filepath.Join(clipDir, MediaAssetRootName, structureAssessmentMediaDirName, "window-lineage", digest[:2], digest+".json")
}

func structureAssessmentWindowIndexPath(clipDir, operation string) string {
	return filepath.Join(clipDir, MediaAssetRootName, structureAssessmentMediaDirName, "window-operations", operation[:2], operation+".json")
}

func loadStructureAssessmentWindowIndex(path, operation string) (structureAssessmentWindowIndex, bool, error) {
	raw, err := readBoundedRegularFile(path, structureAssessmentLineageMaximumBytes)
	if errors.Is(err, os.ErrNotExist) {
		return structureAssessmentWindowIndex{}, false, nil
	}
	if err != nil {
		return structureAssessmentWindowIndex{}, false, fmt.Errorf("read structure assessment window index: %w", err)
	}
	var index structureAssessmentWindowIndex
	if err := strictStructureAssessmentJSON(raw, &index); err != nil ||
		index.SchemaVersion != structureAssessmentWindowIndexSchemaVersion ||
		index.ContractVersion != structureAssessmentWindowIndexContractVersion ||
		index.OperationSHA256 != operation || !isContentHash(index.LineageSHA256) {
		return structureAssessmentWindowIndex{}, false, errors.New("structure assessment window index is invalid")
	}
	return index, true, nil
}

func loadStructureAssessmentWindowLineage(path, digest string, plan fillerstructurewindow.Plan) (StructureAssessmentWindowLineage, error) {
	raw, err := readBoundedRegularFile(path, structureAssessmentLineageMaximumBytes)
	if err != nil {
		return StructureAssessmentWindowLineage{}, fmt.Errorf("read structure assessment window lineage: %w", err)
	}
	var lineage StructureAssessmentWindowLineage
	if err := strictStructureAssessmentJSON(raw, &lineage); err != nil {
		return StructureAssessmentWindowLineage{}, fmt.Errorf("decode structure assessment window lineage: %w", err)
	}
	if lineage.SHA256 != digest {
		return StructureAssessmentWindowLineage{}, errors.New("structure assessment window lineage filename does not match content")
	}
	if err := validateStructureAssessmentWindowLineage(plan, lineage); err != nil {
		return StructureAssessmentWindowLineage{}, err
	}
	return lineage, nil
}
