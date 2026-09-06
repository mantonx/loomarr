package fillervisualsafety

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	visualCorpusNominationAssetsDirectory = "assets"
	visualCorpusNominationRightsDirectory = "rights"
	visualCorpusNominationSetFilename     = "nomination-set.json"
	visualCorpusNominationIncompleteName  = ".incomplete"
)

type completedVisualCorpusNomination struct {
	nomination      string
	subjectStatus   string
	generatedStatus string
	slices          []string
}

func lockVisualCorpusNominations(ctx context.Context, config VisualCorpusNominationLockConfig) (VisualCorpusNominationResult, error) {
	if ctx == nil || ctx.Err() != nil || !validIdentity(config.ReviewedBy) || config.ReviewedAt.IsZero() ||
		config.ReviewedAt.Location() != time.UTC || config.ReviewedAt.Before(config.Worksheet.PreparedAt) ||
		!cleanAbsoluteReviewPath(config.OutputDir) || prospectiveVisualCorpusPathsOverlap(config.Prepare.MediaRoot, config.OutputDir) {
		return VisualCorpusNominationResult{}, errors.New("visual corpus nomination lock configuration is invalid")
	}
	rebuilt, err := prepareVisualCorpusNominationWorksheet(ctx, config.Prepare)
	if err != nil || !sameVisualCorpusNominationWorksheet(rebuilt, config.Worksheet) {
		return VisualCorpusNominationResult{}, errors.New("visual corpus nomination worksheet no longer reproduces")
	}
	completed, err := parseCompletedVisualCorpusNominations(config.Worksheet, config.CompletedCSV)
	if err != nil {
		return VisualCorpusNominationResult{}, err
	}
	parent, err := reserveReviewOutput(config.OutputDir)
	if err != nil {
		return VisualCorpusNominationResult{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(config.OutputDir)
		}
	}()
	if err := writeReviewFile(filepath.Join(config.OutputDir, visualCorpusNominationIncompleteName), []byte("incomplete\n")); err != nil {
		return VisualCorpusNominationResult{}, err
	}
	assetsRoot := filepath.Join(config.OutputDir, visualCorpusNominationAssetsDirectory)
	rightsRoot := filepath.Join(config.OutputDir, visualCorpusNominationRightsDirectory)
	for _, directory := range []string{assetsRoot, rightsRoot} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return VisualCorpusNominationResult{}, errors.New("create visual corpus nomination directory")
		}
	}
	candidates := make([]VisualCorpusDraftCandidate, 0, len(config.Worksheet.Cases))
	excluded := 0
	seenWork := make(map[string]struct{}, len(config.Worksheet.Cases))
	seenFamily := make(map[string]struct{}, len(config.Worksheet.Cases))
	seenIndependence := make(map[string]struct{}, len(config.Worksheet.Cases))
	seenPositiveCreator := make(map[string]struct{}, len(config.Worksheet.Cases))
	for index, row := range config.Worksheet.Cases {
		if err := ctx.Err(); err != nil {
			return VisualCorpusNominationResult{}, fmt.Errorf("lock visual corpus nominations: %w", err)
		}
		if completed[index].nomination == VisualCorpusNominationExclude {
			excluded++
			continue
		}
		candidate, err := publishVisualCorpusNominationCandidate(config, assetsRoot, rightsRoot, row, completed[index])
		if err != nil {
			return VisualCorpusNominationResult{}, err
		}
		if duplicateIdentity(seenWork, candidate.SourceWorkID) || duplicateIdentity(seenFamily, candidate.SourceFamilyID) ||
			duplicateIdentity(seenIndependence, candidate.IndependenceGroupID) {
			return VisualCorpusNominationResult{}, errors.New("visual corpus nominations repeat a source-work independence identity")
		}
		if candidate.Nomination == VisualCorpusNominationPositive && duplicateIdentity(seenPositiveCreator, candidate.CreatorID) {
			return VisualCorpusNominationResult{}, errors.New("visual corpus positive nominations repeat a creator independence identity")
		}
		candidates = append(candidates, candidate)
	}
	set := VisualCorpusNominationSet{
		SchemaVersion: VisualCorpusNominationSetSchemaVersion, ContractVersion: VisualCorpusNominationSetContractVersion,
		WorksheetSHA256: config.Worksheet.SHA256, ReviewDecisionsSHA256: digestJSON(config.CompletedCSV),
		InventorySHA256:       config.Worksheet.InventorySHA256,
		MaterializationSHA256: config.Worksheet.MaterializationSHA256, LockedAt: config.ReviewedAt,
		ReviewedBy: config.ReviewedBy, ReviewedCaseCount: len(config.Worksheet.Cases), ExcludedCaseCount: excluded,
		Candidates: candidates, CandidateModelOutput: false,
		TruthAuthorityCreated: false, TrainingAllowed: false, ProductionUseAllowed: false,
	}
	set.SHA256 = VisualCorpusNominationSetSHA256(set)
	if err := validateVisualCorpusNominationSet(set); err != nil {
		return VisualCorpusNominationResult{}, err
	}
	if err := writeReviewJSON(filepath.Join(config.OutputDir, visualCorpusNominationSetFilename), set); err != nil {
		return VisualCorpusNominationResult{}, err
	}
	if _, err := openVisualCorpusNominationSet(config.OutputDir, true); err != nil {
		return VisualCorpusNominationResult{}, fmt.Errorf("verify visual corpus nomination set: %w", err)
	}
	for _, directory := range []string{assetsRoot, rightsRoot, config.OutputDir} {
		if err := syncReviewDirectory(directory); err != nil {
			return VisualCorpusNominationResult{}, err
		}
	}
	if err := os.Remove(filepath.Join(config.OutputDir, visualCorpusNominationIncompleteName)); err != nil {
		return VisualCorpusNominationResult{}, errors.New("publish visual corpus nomination set")
	}
	if err := syncReviewDirectory(config.OutputDir); err != nil {
		return VisualCorpusNominationResult{}, err
	}
	if err := syncReviewDirectory(parent); err != nil {
		return VisualCorpusNominationResult{}, err
	}
	published = true
	return VisualCorpusNominationResult{
		SetSHA256: set.SHA256, ReviewedCount: set.ReviewedCaseCount,
		CandidateCount: len(set.Candidates), ExcludedCount: set.ExcludedCaseCount,
	}, nil
}

func prospectiveVisualCorpusPathsOverlap(existingRoot, output string) bool {
	resolvedRoot, rootErr := filepath.EvalSymlinks(existingRoot)
	resolvedParent, parentErr := filepath.EvalSymlinks(filepath.Dir(output))
	if rootErr != nil || parentErr != nil {
		return true
	}
	resolvedOutput := filepath.Join(resolvedParent, filepath.Base(output))
	return visualCorpusPathContains(resolvedRoot, resolvedOutput) || visualCorpusPathContains(resolvedOutput, resolvedRoot)
}

func parseCompletedVisualCorpusNominations(worksheet VisualCorpusNominationWorksheet, records [][]string) ([]completedVisualCorpusNomination, error) {
	header := VisualCorpusNominationCSVHeader()
	immutableFields := len(header) - 4
	if len(records) != len(worksheet.Cases)+1 || !slices.Equal(records[0], header) {
		return nil, errors.New("visual corpus nomination CSV header or row count changed")
	}
	completed := make([]completedVisualCorpusNomination, len(worksheet.Cases))
	for index, row := range worksheet.Cases {
		fields := records[index+1]
		if len(fields) != len(header) || !slices.Equal(fields[:immutableFields], ImmutableVisualCorpusNominationCSVRecord(worksheet, row)) {
			return nil, fmt.Errorf("visual corpus nomination CSV row %d changed immutable evidence", index+1)
		}
		nomination := strings.TrimSpace(fields[immutableFields])
		subjectStatus := strings.TrimSpace(fields[immutableFields+1])
		generatedStatus := strings.TrimSpace(fields[immutableFields+2])
		var diagnosticSlices []string
		if err := json.Unmarshal([]byte(fields[immutableFields+3]), &diagnosticSlices); err != nil || diagnosticSlices == nil || len(diagnosticSlices) > 32 {
			return nil, fmt.Errorf("visual corpus nomination CSV row %d has invalid slices_json", index+1)
		}
		if nomination == VisualCorpusNominationExclude {
			if subjectStatus != "" || generatedStatus != "" || len(diagnosticSlices) != 0 {
				return nil, fmt.Errorf("visual corpus nomination CSV row %d has incompatible exclusion judgments", index+1)
			}
			completed[index] = completedVisualCorpusNomination{nomination: nomination, slices: []string{}}
			continue
		}
		if len(diagnosticSlices) == 0 {
			return nil, fmt.Errorf("visual corpus nomination CSV row %d has invalid slices_json", index+1)
		}
		if !slices.IsSorted(diagnosticSlices) || len(slices.Compact(slices.Clone(diagnosticSlices))) != len(diagnosticSlices) {
			return nil, fmt.Errorf("visual corpus nomination CSV row %d slices_json is not sorted and unique", index+1)
		}
		for _, diagnosticSlice := range diagnosticSlices {
			if !validVisualDiagnosticSlice(diagnosticSlice) {
				return nil, fmt.Errorf("visual corpus nomination CSV row %d has an unknown slice", index+1)
			}
		}
		value := completedVisualCorpusNomination{
			nomination: nomination, subjectStatus: subjectStatus,
			generatedStatus: generatedStatus, slices: diagnosticSlices,
		}
		if value.generatedStatus != VisualCorpusGeneratedNo ||
			(value.nomination == VisualCorpusNominationPositive && value.subjectStatus != VisualCorpusSubjectHistoricalAdult) ||
			(value.nomination == VisualCorpusNominationClean && value.subjectStatus != VisualCorpusSubjectNoRiskFound) ||
			(value.nomination != VisualCorpusNominationPositive && value.nomination != VisualCorpusNominationClean) {
			return nil, fmt.Errorf("visual corpus nomination CSV row %d has incompatible or unresolved judgments", index+1)
		}
		completed[index] = value
	}
	return completed, nil
}

func publishVisualCorpusNominationCandidate(config VisualCorpusNominationLockConfig, assetsRoot, rightsRoot string, row VisualCorpusNominationRow, completed completedVisualCorpusNomination) (VisualCorpusDraftCandidate, error) {
	raw, err := readVisualCorpusInput(config.Prepare.MediaRoot, row.LocalFile, row.Asset)
	if err != nil {
		return VisualCorpusDraftCandidate{}, fmt.Errorf("visual corpus nomination %s media changed during lock", row.CaseID)
	}
	mediaType, width, height, perceptual, err := inspectVisualCorpusImage(raw)
	if err != nil || mediaType != row.MediaType || width != row.Width || height != row.Height || perceptual != row.PerceptualHash {
		return VisualCorpusDraftCandidate{}, fmt.Errorf("visual corpus nomination %s image evidence changed during lock", row.CaseID)
	}
	extension := ".png"
	if row.MediaType == "image/jpeg" {
		extension = ".jpg"
	}
	assetRelativePath := path.Join(visualCorpusNominationAssetsDirectory, row.Asset.SHA256[:24]+extension)
	if err := writeReviewFile(filepath.Join(assetsRoot, path.Base(assetRelativePath)), raw); err != nil {
		return VisualCorpusDraftCandidate{}, err
	}
	rightsRelativePath := path.Join(visualCorpusNominationRightsDirectory, digestBytes([]byte(row.CaseID))[:24]+".json")
	evidence := VisualCorpusRightsEvidence{
		SchemaVersion: visualCorpusRightsEvidenceSchemaVersion, Kind: visualCorpusRightsEvidenceKind,
		InventorySHA256: config.Worksheet.InventorySHA256, MaterializationSHA256: config.Worksheet.MaterializationSHA256,
		RightsApprovalSHA256: row.RightsApprovalSHA256, CaseID: row.CaseID, ContentSHA256: row.Asset.SHA256,
		ReviewedAt: config.ReviewedAt, ReviewedBy: config.ReviewedBy, InstitutionID: row.InstitutionID,
		SourceWorkID: row.SourceWorkID, ObjectURL: row.ObjectURL, RightsURL: row.RightsURL, RightsBasis: row.RightsBasis,
		SubjectStatus: completed.subjectStatus, GeneratedStatus: completed.generatedStatus,
		PrivateRetentionAllowed: true, PrivateModelEvaluation: true, TrainingAllowed: false,
		ProductionBroadcastAllowed: false,
	}
	rightsRaw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return VisualCorpusDraftCandidate{}, err
	}
	rightsRaw = append(rightsRaw, '\n')
	if int64(len(rightsRaw)) > MaximumVisualCorpusRightsBytes {
		return VisualCorpusDraftCandidate{}, errors.New("visual corpus nomination rights evidence is oversized")
	}
	if err := writeReviewFile(filepath.Join(rightsRoot, path.Base(rightsRelativePath)), rightsRaw); err != nil {
		return VisualCorpusDraftCandidate{}, err
	}
	candidate := VisualCorpusDraftCandidate{
		CandidateID: row.CaseID, Nomination: completed.nomination, InstitutionID: row.InstitutionID,
		SourceWorkID: row.SourceWorkID, SourceFamilyID: row.SourceFamilyID, IndependenceGroupID: row.IndependenceGroupID,
		CreatorID: row.CreatorID, ObjectURL: row.ObjectURL, RightsURL: row.RightsURL, RightsBasis: row.RightsBasis,
		SubjectStatus: completed.subjectStatus, GeneratedStatus: completed.generatedStatus,
		AssetRelativePath: assetRelativePath, Asset: row.Asset, RightsRelativePath: rightsRelativePath,
		RightsEvidence: VisualCorpusFileIdentity{SHA256: digestBytes(rightsRaw), Bytes: int64(len(rightsRaw))},
		Slices:         slices.Clone(completed.slices),
	}
	if err := validateVisualCorpusDraftCandidate(candidate); err != nil || validateVisualCorpusRightsEvidence(candidate, evidence, config.ReviewedAt) != nil {
		return VisualCorpusDraftCandidate{}, fmt.Errorf("visual corpus nomination %s cannot form a draft candidate", row.CaseID)
	}
	return candidate, nil
}
