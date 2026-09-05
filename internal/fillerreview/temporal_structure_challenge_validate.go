package fillerreview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/loomarr/loomarr/internal/fillereval"
)

// LoadTemporalStructureChallenge re-establishes the complete public/private
// authority join from disk. Comparison and assessment tooling should use this
// loader instead of decoding either artifact directly.
func LoadTemporalStructureChallenge(publicManifestPath, authorityPath string, expectedCases int) (TemporalStructureChallengeManifest, TemporalStructureChallengeAuthority, string, string, error) {
	manifest, manifestSHA, err := LoadTemporalStructureChallengePublic(publicManifestPath, expectedCases)
	if err != nil {
		return TemporalStructureChallengeManifest{}, TemporalStructureChallengeAuthority{}, "", "", err
	}
	authorityRaw, err := os.ReadFile(authorityPath)
	if err != nil {
		return TemporalStructureChallengeManifest{}, TemporalStructureChallengeAuthority{}, "", "", fmt.Errorf("read private challenge authority: %w", err)
	}
	authority, err := readStrictJSON[TemporalStructureChallengeAuthority](authorityPath)
	if err != nil {
		return TemporalStructureChallengeManifest{}, TemporalStructureChallengeAuthority{}, "", "", fmt.Errorf("decode private challenge authority: %w", err)
	}
	authoritySHA := hashBytes(authorityRaw)
	if err := validateTemporalStructureChallenge(filepath.Dir(publicManifestPath), manifest, authority, manifestSHA, expectedCases); err != nil {
		return TemporalStructureChallengeManifest{}, TemporalStructureChallengeAuthority{}, "", "", err
	}
	return manifest, authority, manifestSHA, authoritySHA, nil
}

// LoadTemporalStructureChallengePublic validates the complete assessor-facing
// surface without opening private construction authority. Paid model runners
// must use this loader so truth labels cannot enter their process.
func LoadTemporalStructureChallengePublic(publicManifestPath string, expectedCases int) (TemporalStructureChallengeManifest, string, error) {
	manifestRaw, err := os.ReadFile(publicManifestPath)
	if err != nil {
		return TemporalStructureChallengeManifest{}, "", fmt.Errorf("read public challenge manifest: %w", err)
	}
	manifest, err := readStrictJSON[TemporalStructureChallengeManifest](publicManifestPath)
	if err != nil {
		return TemporalStructureChallengeManifest{}, "", fmt.Errorf("decode public challenge manifest: %w", err)
	}
	manifestSHA := hashBytes(manifestRaw)
	if _, err := validateTemporalStructureChallengePublic(filepath.Dir(publicManifestPath), manifest, expectedCases); err != nil {
		return TemporalStructureChallengeManifest{}, "", err
	}
	return manifest, manifestSHA, nil
}

func validateTemporalStructureChallenge(publicRoot string, manifest TemporalStructureChallengeManifest, authority TemporalStructureChallengeAuthority, manifestSHA string, expectedCases int) error {
	publicByAlias, err := validateTemporalStructureChallengePublic(publicRoot, manifest, expectedCases)
	if err != nil {
		return err
	}

	if authority.SchemaVersion != manifest.SchemaVersion || authority.ContractVersion != manifest.ContractVersion || authority.ChallengeID != manifest.ChallengeID || !authority.GeneratedAt.Equal(manifest.GeneratedAt) || !reviewSHA256(authority.AuthoringSHA256) || !validTemporalStructureHoldoutContract(authority.PlanContractVersion) || !reviewSHA256(authority.PlanReceiptSHA256) || !reviewSHA256(authority.SeedSHA256) || authority.PublicManifestSHA256 != manifestSHA || len(authority.Cases) != expectedCases {
		return fmt.Errorf("private challenge authority does not bind the public manifest")
	}
	for name, identity := range map[string]TemporalTruthToolIdentity{"ffmpeg": authority.MediaTools.FFmpeg, "ffprobe": authority.MediaTools.FFprobe} {
		if strings.TrimSpace(identity.Path) == "" || strings.TrimSpace(identity.Version) == "" || !reviewSHA256(identity.BinarySHA256) {
			return fmt.Errorf("private challenge %s identity is invalid", name)
		}
	}
	authorityAliases := make(map[string]struct{}, expectedCases)
	caseIDs := make(map[string]struct{}, expectedCases)
	for index, item := range authority.Cases {
		publicCase, exists := publicByAlias[item.Alias]
		if !exists || publicCase.Video.SHA256 != item.VideoSHA256 {
			return fmt.Errorf("private challenge case %d does not bind a public video", index)
		}
		if _, duplicate := authorityAliases[item.Alias]; duplicate {
			return fmt.Errorf("private challenge repeats alias %q", item.Alias)
		}
		if strings.TrimSpace(item.CaseID) == "" {
			return fmt.Errorf("private challenge case %d has no case id", index)
		}
		if _, duplicate := caseIDs[item.CaseID]; duplicate {
			return fmt.Errorf("private challenge repeats case id %q", item.CaseID)
		}
		authorityAliases[item.Alias] = struct{}{}
		caseIDs[item.CaseID] = struct{}{}
		if err := validateTemporalStructureAuthorityCase(item, publicCase.Video.DurationMS); err != nil {
			return fmt.Errorf("private challenge case %d: %w", index, err)
		}
	}
	return nil
}

func validateTemporalStructureChallengePublic(publicRoot string, manifest TemporalStructureChallengeManifest, expectedCases int) (map[string]TemporalStructureChallengePublicCase, error) {
	if expectedCases <= 0 || manifest.SchemaVersion != TemporalStructureChallengeSchemaVersion || manifest.ContractVersion != TemporalStructureChallengeContractVersion || manifest.ChallengeID == "" || manifest.GeneratedAt.IsZero() || manifest.ProductionAdmissionAllowed || len(manifest.Cases) != expectedCases {
		return nil, fmt.Errorf("public challenge identity, count, or production disposition is invalid")
	}
	publicByAlias := make(map[string]TemporalStructureChallengePublicCase, expectedCases)
	for index, item := range manifest.Cases {
		if len(item.Alias) != len("case-")+24 || !strings.HasPrefix(item.Alias, "case-") || !isLowerHex(item.Alias[len("case-"):]) {
			return nil, fmt.Errorf("public challenge case %d has invalid alias", index)
		}
		if _, duplicate := publicByAlias[item.Alias]; duplicate {
			return nil, fmt.Errorf("public challenge repeats alias %q", item.Alias)
		}
		expectedPath := filepath.ToSlash(filepath.Join("cases", item.Alias, "video.mp4"))
		if item.Video.Path != expectedPath || item.Video.DurationMS <= 0 || item.Video.Width <= 0 || item.Video.Height <= 0 || item.Video.Bytes <= 0 || item.Video.Bytes > TemporalTruthMaximumVideoBytes || !reviewSHA256(item.Video.SHA256) {
			return nil, fmt.Errorf("public challenge case %d has invalid video authority", index)
		}
		if err := verifyTemporalTruthEvidenceFile(publicRoot, item.Video, TemporalTruthMaximumVideoBytes); err != nil {
			return nil, fmt.Errorf("public challenge case %d: %w", index, err)
		}
		publicByAlias[item.Alias] = item
	}
	return publicByAlias, nil
}

func validateTemporalStructureAuthorityCase(item TemporalStructureChallengeAuthorityCase, outputDurationMS int64) error {
	if len(item.Segments) == 0 {
		return fmt.Errorf("segments are required")
	}
	for index, part := range item.Segments {
		if part.Ordinal != index || strings.TrimSpace(part.SourceID) == "" || part.SourcePath == "" || !reviewSHA256(part.SourceSHA256) || part.SourceDurationMS <= 0 || part.SourceStartMS < 0 || part.RequestedMS <= 0 || part.RenderedMS <= 0 || part.SourceStartMS+part.RequestedMS > part.SourceDurationMS || absoluteInt64(part.RequestedMS-part.RenderedMS) > 1_000 {
			return fmt.Errorf("segment %d has invalid source or render authority", index)
		}
		if part.OutputStartMS < 0 || part.OutputEndMS != part.OutputStartMS+part.RenderedMS || index > 0 && part.OutputStartMS != item.Segments[index-1].OutputEndMS {
			return fmt.Errorf("segment %d has discontinuous output authority", index)
		}
		if part.Provenance.Kind != TemporalStructureSourceBoundedItem && part.Provenance.Kind != TemporalStructureSourceProgrammeParent || strings.TrimSpace(part.Provenance.Authority) == "" || strings.TrimSpace(part.Provenance.Reference) == "" || !reviewSHA256(part.Provenance.MetadataSHA256) || part.Provenance.RetrievedAt.IsZero() {
			return fmt.Errorf("segment %d has invalid provenance", index)
		}
		if (part.Provenance.Kind == TemporalStructureSourceBoundedItem && !validTemporalStructureRole(part.SourceRole)) || (part.Provenance.Kind == TemporalStructureSourceProgrammeParent && part.SourceRole != "") {
			return fmt.Errorf("segment %d has invalid source-role authority", index)
		}
	}
	if absoluteInt64(item.Segments[len(item.Segments)-1].OutputEndMS-outputDurationMS) > 1_000 {
		return fmt.Errorf("segments do not bind public output duration")
	}
	switch item.Unit {
	case fillereval.UnitStandalone:
		part := item.Segments[0]
		if len(item.Segments) != 1 || !validTemporalStructureRole(item.Role) || item.Role != part.SourceRole || part.Provenance.Kind != TemporalStructureSourceBoundedItem || part.SourceStartMS != 0 || part.RequestedMS != part.SourceDurationMS || len(item.JoinTimesMS) != 0 {
			return fmt.Errorf("standalone authority is not one whole bounded item")
		}
	case fillereval.UnitCompilation:
		if len(item.Segments) < 2 || item.Role != "" || len(item.JoinTimesMS) != len(item.Segments)-1 {
			return fmt.Errorf("compilation authority has invalid cardinality, role, or joins")
		}
		for index, part := range item.Segments {
			if part.Provenance.Kind != TemporalStructureSourceBoundedItem || part.SourceStartMS != 0 || part.RequestedMS != part.SourceDurationMS || index > 0 && item.JoinTimesMS[index-1] != part.OutputStartMS {
				return fmt.Errorf("compilation segment %d is not a whole bounded item at its asserted join", index)
			}
		}
	case fillereval.UnitProgrammeExcerpt:
		part := item.Segments[0]
		if len(item.Segments) != 1 || item.Role != "" || part.Provenance.Kind != TemporalStructureSourceProgrammeParent || part.SourceStartMS < 5_000 || part.SourceStartMS+part.RequestedMS > part.SourceDurationMS-5_000 || len(item.JoinTimesMS) != 0 {
			return fmt.Errorf("programme excerpt authority is not one interior parent cut")
		}
	default:
		return fmt.Errorf("unit %q has no provenance-grounded authority", item.Unit)
	}
	return nil
}

func isLowerHex(value string) bool {
	for _, character := range value {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return value != ""
}
