package fillerreview

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func verifyTemporalStructureSources(ctx context.Context, config TemporalStructureChallengeConfig, cases []temporalStructurePreparedCase) error {
	verified := make(map[string]struct{})
	for _, item := range cases {
		for index, source := range item.sources {
			if _, exists := verified[source.ID]; exists {
				continue
			}
			path := item.segments[index].SourcePath
			digest, err := hashFile(path)
			if err != nil || digest != source.SHA256 {
				return fmt.Errorf("source %q content hash mismatch", source.ID)
			}
			probe, err := config.Media.Probe(ctx, path)
			if err != nil || absoluteInt64(probe.DurationMS-source.DurationMS) > 1_000 || !probe.HasAudio {
				return fmt.Errorf("source %q duration or required-audio authority mismatch", source.ID)
			}
			verified[source.ID] = struct{}{}
		}
	}
	return nil
}

func prepareTemporalStructureChallenge(config TemporalStructureChallengeConfig, authoring TemporalStructureChallengeAuthoring) ([]temporalStructurePreparedCase, error) {
	if authoring.SchemaVersion != TemporalStructureChallengeSchemaVersion || authoring.ContractVersion != TemporalStructureChallengeContractVersion || len(authoring.Sources) == 0 || len(authoring.Cases) == 0 {
		return nil, fmt.Errorf("challenge authoring has invalid identity or empty sources/cases")
	}
	sources := make(map[string]TemporalStructureChallengeSource, len(authoring.Sources))
	for index, source := range authoring.Sources {
		if err := validateTemporalStructureSource(config.SourceRoot, source); err != nil {
			return nil, fmt.Errorf("source %d: %w", index, err)
		}
		if _, duplicate := sources[source.ID]; duplicate {
			return nil, fmt.Errorf("source %d repeats id %q", index, source.ID)
		}
		sources[source.ID] = source
	}
	prepared := make([]temporalStructurePreparedCase, 0, len(authoring.Cases))
	caseIDs := make(map[string]struct{}, len(authoring.Cases))
	aliases := make(map[string]struct{}, len(authoring.Cases))
	for index, item := range authoring.Cases {
		if _, duplicate := caseIDs[item.ID]; duplicate {
			return nil, fmt.Errorf("case %d repeats id %q", index, item.ID)
		}
		caseIDs[item.ID] = struct{}{}
		preparedCase, err := prepareTemporalStructureCase(config, item, sources)
		if err != nil {
			return nil, fmt.Errorf("case %d: %w", index, err)
		}
		if _, duplicate := aliases[preparedCase.alias]; duplicate {
			return nil, fmt.Errorf("case %d produces duplicate blinded alias", index)
		}
		aliases[preparedCase.alias] = struct{}{}
		prepared = append(prepared, preparedCase)
	}
	sort.Slice(prepared, func(first, second int) bool { return prepared[first].order < prepared[second].order })
	return prepared, nil
}

func prepareTemporalStructureCase(config TemporalStructureChallengeConfig, item TemporalStructureChallengeCase, sources map[string]TemporalStructureChallengeSource) (temporalStructurePreparedCase, error) {
	if strings.TrimSpace(item.ID) == "" || len(item.Segments) == 0 {
		return temporalStructurePreparedCase{}, fmt.Errorf("id and segments are required")
	}
	result := temporalStructurePreparedCase{spec: item}
	for index, segment := range item.Segments {
		source, exists := sources[segment.SourceID]
		if !exists || segment.StartMS < 0 || segment.DurationMS <= 0 || segment.StartMS+segment.DurationMS > source.DurationMS {
			return temporalStructurePreparedCase{}, fmt.Errorf("segment %d has unknown source or invalid bounds", index)
		}
		result.sources = append(result.sources, source)
		result.segments = append(result.segments, TemporalStructureRenderSegment{
			SourcePath: filepath.Join(config.SourceRoot, filepath.FromSlash(source.Path)), StartMS: segment.StartMS, DurationMS: segment.DurationMS,
		})
	}
	switch item.Unit {
	case fillereval.UnitStandalone:
		if len(item.Segments) != 1 || !wholeBoundedTemporalSource(item.Segments[0], result.sources[0]) || item.Role == "" || item.Role != result.sources[0].StandaloneRole || !validTemporalStructureRole(item.Role) {
			return temporalStructurePreparedCase{}, fmt.Errorf("standalone requires one whole bounded source and its authority-bound role")
		}
	case fillereval.UnitCompilation:
		if len(item.Segments) < 2 || item.Role != "" {
			return temporalStructurePreparedCase{}, fmt.Errorf("compilation requires at least two whole bounded sources and no role")
		}
		for index := range item.Segments {
			if !wholeBoundedTemporalSource(item.Segments[index], result.sources[index]) {
				return temporalStructurePreparedCase{}, fmt.Errorf("compilation segment %d is not one whole bounded item", index)
			}
		}
	case fillereval.UnitProgrammeExcerpt:
		source := result.sources[0]
		segment := item.Segments[0]
		if len(item.Segments) != 1 || item.Role != "" || source.Provenance.Kind != TemporalStructureSourceProgrammeParent || segment.StartMS < 5_000 || segment.StartMS+segment.DurationMS > source.DurationMS-5_000 {
			return temporalStructurePreparedCase{}, fmt.Errorf("programme excerpt requires one interior cut with five-second parent margins")
		}
	default:
		return temporalStructurePreparedCase{}, fmt.Errorf("unit %q has no provenance-grounded construction", item.Unit)
	}
	result.alias = "case-" + temporalStructureBlindValue(config.Seed, "alias:"+item.ID)[:24]
	result.order = temporalStructureBlindValue(config.Seed, "order:"+item.ID)
	return result, nil
}

func validateTemporalStructureChallengeConfig(config TemporalStructureChallengeConfig) error {
	if strings.TrimSpace(config.AuthoringPath) == "" || strings.TrimSpace(config.PlanReceiptPath) == "" || strings.TrimSpace(config.SourceRoot) == "" || strings.TrimSpace(config.OutputDir) == "" || strings.TrimSpace(config.ChallengeID) == "" || strings.TrimSpace(config.Seed) == "" || config.GeneratedAt.IsZero() || config.Media == nil {
		return fmt.Errorf("authoring, plan receipt, source root, output, challenge id, seed, fixed generation time, and media adapter are required")
	}
	return nil
}

func validateTemporalStructureSource(root string, source TemporalStructureChallengeSource) error {
	if strings.TrimSpace(source.ID) == "" || source.Path == "" || filepath.IsAbs(source.Path) || strings.Contains(filepath.ToSlash(source.Path), "../") || !reviewSHA256(source.SHA256) || source.DurationMS <= 0 {
		return fmt.Errorf("source identity, relative path, content hash, or duration is invalid")
	}
	if source.Provenance.Kind != TemporalStructureSourceBoundedItem && source.Provenance.Kind != TemporalStructureSourceProgrammeParent {
		return fmt.Errorf("source provenance kind is invalid")
	}
	if strings.TrimSpace(source.Provenance.Authority) == "" || strings.TrimSpace(source.Provenance.Reference) == "" || !reviewSHA256(source.Provenance.MetadataSHA256) || source.Provenance.RetrievedAt.IsZero() {
		return fmt.Errorf("source provenance is incomplete")
	}
	if source.Provenance.Kind == TemporalStructureSourceBoundedItem {
		if !validTemporalStructureRole(source.StandaloneRole) {
			return fmt.Errorf("bounded item requires an authority-bound standalone role")
		}
	} else if source.StandaloneRole != "" {
		return fmt.Errorf("programme parent cannot assert a standalone role")
	}
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	resolvedPath, err := filepath.Abs(filepath.Join(resolvedRoot, filepath.FromSlash(source.Path)))
	if err != nil || resolvedPath == resolvedRoot || !strings.HasPrefix(resolvedPath, resolvedRoot+string(os.PathSeparator)) {
		return fmt.Errorf("source path escapes source root")
	}
	return nil
}

func wholeBoundedTemporalSource(segment TemporalStructureChallengeSegment, source TemporalStructureChallengeSource) bool {
	return source.Provenance.Kind == TemporalStructureSourceBoundedItem && segment.StartMS == 0 && segment.DurationMS == source.DurationMS
}

func validTemporalStructureRole(role fillereval.TemporalRole) bool {
	return slices.Contains([]fillereval.TemporalRole{
		fillereval.TemporalRoleCommercial, fillereval.TemporalRolePromo, fillereval.TemporalRoleTrailer,
		fillereval.TemporalRoleBumper, fillereval.TemporalRolePSA, fillereval.TemporalRoleStationID,
		fillereval.TemporalRoleInterstitial,
	}, role)
}

func temporalStructureBlindValue(seed, value string) string {
	mac := hmac.New(sha256.New, []byte(seed))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func auditTemporalStructureChallengeLeakage(publicRoot string, authoring TemporalStructureChallengeAuthoring, receipt *TemporalStructureHoldoutReceipt) error {
	var public []byte
	err := filepath.WalkDir(publicRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		public = append(public, raw...)
		return nil
	})
	if err != nil {
		return err
	}
	secrets := []string{string(fillereval.UnitStandalone), string(fillereval.UnitCompilation), string(fillereval.UnitProgrammeExcerpt)}
	for _, source := range authoring.Sources {
		secrets = append(secrets, source.ID, source.Path, source.Provenance.Authority, source.Provenance.Reference, source.Provenance.MetadataSHA256)
	}
	for _, item := range authoring.Cases {
		secrets = append(secrets, item.ID)
	}
	if receipt != nil {
		secrets = append(secrets, receipt.SeedSHA256, receipt.AuthoringSHA256)
		for _, input := range receipt.Inputs {
			secrets = append(secrets, input.SHA256)
		}
		for _, anchor := range receipt.SelectedAnchors {
			secrets = append(secrets, anchor.EvidenceAlias, anchor.CaseID, anchor.FamilyID, anchor.RankSHA256)
		}
		for _, provenance := range receipt.FutureTrainingExclusion.ProgrammeProvenance {
			secrets = append(secrets, provenance.Authority, provenance.Reference)
		}
	}
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" && strings.Contains(string(public), secret) {
			return fmt.Errorf("public challenge leaks coordinator-private value %q", secret)
		}
	}
	return nil
}

func absoluteInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
