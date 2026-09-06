package fillerreview

import (
	"fmt"
	"os"
	"strings"
)

// LoadTemporalStructureShadowDecisionSet replays one complete decision set and its public-manifest
// binding. It opens no private truth.
func LoadTemporalStructureShadowDecisionSet(path, manifestPath string) (TemporalStructureShadowDecisionSet, string, error) {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(manifestPath) == "" {
		return TemporalStructureShadowDecisionSet{}, "", fmt.Errorf("structure shadow decision set load requires result and public manifest paths")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return TemporalStructureShadowDecisionSet{}, "", fmt.Errorf("read structure shadow decision set: %w", err)
	}
	set, err := readStrictJSON[TemporalStructureShadowDecisionSet](path)
	if err != nil {
		return TemporalStructureShadowDecisionSet{}, "", fmt.Errorf("decode structure shadow decision set: %w", err)
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(manifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		return TemporalStructureShadowDecisionSet{}, "", err
	}
	if err := validateTemporalStructureShadowDecisionSetAgainstManifest(set, manifest, manifestSHA); err != nil {
		return TemporalStructureShadowDecisionSet{}, "", err
	}
	return set, hashBytes(raw), nil
}
