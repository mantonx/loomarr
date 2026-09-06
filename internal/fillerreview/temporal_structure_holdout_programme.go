package fillerreview

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillerreference"
)

const TemporalStructureHoldoutProgrammeInventoryContract = "filler-temporal-structure-programme-inventory-v2"

const temporalStructureProgrammeEvidenceMaxBytes = 16 << 20

func loadTemporalStructureHoldoutProgrammeInventory(path, sourceRoot string, reference fillerreference.Audit, plannedAt time.Time) (TemporalStructureHoldoutProgrammeInventory, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return TemporalStructureHoldoutProgrammeInventory{}, "", err
	}
	inventory, err := readStrictJSON[TemporalStructureHoldoutProgrammeInventory](path)
	if err != nil {
		return TemporalStructureHoldoutProgrammeInventory{}, "", err
	}
	if inventory.SchemaVersion != TemporalStructureHoldoutSchemaVersion || inventory.ContractVersion != TemporalStructureHoldoutProgrammeInventoryContract || inventory.GeneratedAt.IsZero() || plannedAt.Before(inventory.GeneratedAt) || len(inventory.Sources) < temporalStructureHoldoutParentSources {
		return TemporalStructureHoldoutProgrammeInventory{}, "", fmt.Errorf("temporal structure holdout programme inventory is incomplete")
	}
	seenIDs, seenPaths, seenSHA := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	seenProvenance := map[string]struct{}{}
	for _, source := range inventory.Sources {
		if source.Provenance.Kind != TemporalStructureSourceProgrammeParent || source.StandaloneRole != "" || source.DurationMS < 120_000 || strings.TrimSpace(source.ID) == "" || !reviewSHA256(source.SHA256) || strings.TrimSpace(source.Path) == "" || strings.TrimSpace(source.Provenance.Authority) == "" || strings.TrimSpace(source.Provenance.ItemID) == "" || strings.TrimSpace(source.Provenance.Reference) == "" || strings.TrimSpace(source.Provenance.SourceRecordPath) == "" || !reviewSHA256(source.Provenance.SourceRecordSHA256) || !reviewSHA256(source.Provenance.MetadataSHA256) || source.Provenance.RetrievedAt.IsZero() || inventory.GeneratedAt.Before(source.Provenance.RetrievedAt) {
			return TemporalStructureHoldoutProgrammeInventory{}, "", fmt.Errorf("temporal structure holdout programme source %q is invalid", source.ID)
		}
		resolved, err := resolveRegularFileWithin(sourceRoot, source.Path)
		if err != nil {
			return TemporalStructureHoldoutProgrammeInventory{}, "", fmt.Errorf("temporal structure holdout programme source %q path: %w", source.ID, err)
		}
		digest, err := hashFile(resolved)
		if err != nil || digest != source.SHA256 {
			return TemporalStructureHoldoutProgrammeInventory{}, "", fmt.Errorf("temporal structure holdout programme source %q bytes drift", source.ID)
		}
		if _, duplicate := seenIDs[source.ID]; duplicate {
			return TemporalStructureHoldoutProgrammeInventory{}, "", fmt.Errorf("temporal structure holdout programme inventory repeats an id")
		}
		if _, duplicate := seenPaths[resolved]; duplicate {
			return TemporalStructureHoldoutProgrammeInventory{}, "", fmt.Errorf("temporal structure holdout programme inventory repeats a path")
		}
		if _, duplicate := seenSHA[source.SHA256]; duplicate {
			return TemporalStructureHoldoutProgrammeInventory{}, "", fmt.Errorf("temporal structure holdout programme inventory repeats source bytes")
		}
		provenanceID, err := validateTemporalStructureProgrammeSourceRecord(sourceRoot, source, resolved)
		if err != nil {
			return TemporalStructureHoldoutProgrammeInventory{}, "", err
		}
		if programmeSourceMatchesReference(source, reference) {
			return TemporalStructureHoldoutProgrammeInventory{}, "", fmt.Errorf("temporal structure holdout programme parent repeats bounded filler lineage")
		}
		if _, duplicate := seenProvenance[provenanceID]; duplicate {
			return TemporalStructureHoldoutProgrammeInventory{}, "", fmt.Errorf("temporal structure holdout programme inventory repeats a provenance parent")
		}
		seenIDs[source.ID], seenPaths[resolved], seenSHA[source.SHA256] = struct{}{}, struct{}{}, struct{}{}
		seenProvenance[provenanceID] = struct{}{}
	}
	return inventory, hashBytes(raw), nil
}

func validateTemporalStructureProgrammeSourceRecord(sourceRoot string, source TemporalStructureChallengeSource, mediaPath string) (string, error) {
	provenance := source.Provenance
	_, recordRaw, err := readRegularFileWithin(sourceRoot, provenance.SourceRecordPath)
	if err != nil {
		return "", fmt.Errorf("temporal structure holdout programme source %q source record: %w", source.ID, err)
	}
	if hashBytes(recordRaw) != provenance.SourceRecordSHA256 {
		return "", fmt.Errorf("temporal structure holdout programme source %q source record bytes drift", source.ID)
	}
	record, err := fillercorpus.DecodeInventoryBytes(recordRaw)
	if err != nil {
		return "", fmt.Errorf("temporal structure holdout programme source %q has unsupported source record: %w", source.ID, err)
	}
	var matched *fillercorpus.InventoryCase
	for index := range record.Cases {
		item := &record.Cases[index]
		if strings.TrimSpace(item.Authority) == strings.TrimSpace(provenance.Authority) && strings.TrimSpace(item.ItemID) == strings.TrimSpace(provenance.ItemID) {
			if matched != nil {
				return "", fmt.Errorf("temporal structure holdout programme source %q has ambiguous source-record identity", source.ID)
			}
			matched = item
		}
	}
	if matched == nil {
		return "", fmt.Errorf("temporal structure holdout programme source %q is absent from its source record", source.ID)
	}
	reference, err := canonicalProgrammeReference(provenance.Reference)
	if err != nil {
		return "", fmt.Errorf("temporal structure holdout programme source %q reference: %w", source.ID, err)
	}
	itemReference, err := canonicalProgrammeReference(matched.ItemURL)
	if err != nil || reference != itemReference {
		return "", fmt.Errorf("temporal structure holdout programme source %q reference does not bind its source record", source.ID)
	}
	if _, err := canonicalProgrammeReference(matched.MetadataURL); err != nil || !matched.MetadataRetrievedAt.Equal(provenance.RetrievedAt) || matched.MetadataSHA256 != provenance.MetadataSHA256 {
		return "", fmt.Errorf("temporal structure holdout programme source %q metadata identity does not bind its source record", source.ID)
	}
	_, metadataRaw, err := readRegularFileWithin(sourceRoot, matched.MetadataCache)
	if err != nil || hashBytes(metadataRaw) != provenance.MetadataSHA256 {
		return "", fmt.Errorf("temporal structure holdout programme source %q metadata bytes drift", source.ID)
	}
	representation := matched.Representation
	representationPath, err := resolveRegularFileWithin(sourceRoot, representation.Path)
	if err != nil || representationPath != mediaPath || representation.SHA256 != source.SHA256 || representation.Bytes <= 0 || representation.DurationMS != source.DurationMS {
		return "", fmt.Errorf("temporal structure holdout programme source %q media identity does not bind its source record", source.ID)
	}
	info, err := os.Stat(mediaPath)
	if err != nil || info.Size() != representation.Bytes {
		return "", fmt.Errorf("temporal structure holdout programme source %q media bytes do not bind its source record", source.ID)
	}
	return strings.TrimSpace(provenance.Authority) + "\x00" + reference, nil
}

func canonicalProgrammeReference(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Fragment != "" {
		return "", fmt.Errorf("must be a canonical HTTPS URL")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String(), nil
}

func readRegularFileWithin(root, relative string) (string, []byte, error) {
	resolved, err := resolveRegularFileWithin(root, relative)
	if err != nil {
		return "", nil, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, temporalStructureProgrammeEvidenceMaxBytes+1))
	if err != nil {
		return "", nil, err
	}
	if len(raw) > temporalStructureProgrammeEvidenceMaxBytes {
		return "", nil, fmt.Errorf("evidence file exceeds %d bytes", temporalStructureProgrammeEvidenceMaxBytes)
	}
	return resolved, raw, nil
}

func resolveRegularFileWithin(root, relative string) (string, error) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(relative) == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("missing root or relative path")
	}
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(rootAbsolute, filepath.Clean(relative))
	rel, err := filepath.Rel(rootAbsolute, joined)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes source root")
	}
	current := rootAbsolute
	components := strings.Split(rel, string(filepath.Separator))
	for index, component := range components {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("path contains a symlink")
		}
		if index < len(components)-1 && !info.IsDir() {
			return "", fmt.Errorf("path component is not a directory")
		}
		if index == len(components)-1 && !info.Mode().IsRegular() {
			return "", fmt.Errorf("not a regular file")
		}
	}
	return joined, nil
}

func programmeSourceMatchesReference(source TemporalStructureChallengeSource, reference fillerreference.Audit) bool {
	sourcePath, sourcePathOK := canonicalRelativeProgrammePath(source.Path)
	for _, item := range reference.Cases {
		referencePath, referencePathOK := canonicalRelativeProgrammePath(item.SourceLocalFile)
		if item.ContentSHA256 == source.SHA256 ||
			(strings.TrimSpace(item.Source) == strings.TrimSpace(source.Provenance.Authority) && strings.TrimSpace(item.SourceItemID) == strings.TrimSpace(source.Provenance.ItemID)) ||
			(sourcePathOK && referencePathOK && referencePath == sourcePath) {
			return true
		}
	}
	return false
}

func canonicalRelativeProgrammePath(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || filepath.IsAbs(trimmed) {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(trimmed))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(clean), true
}
