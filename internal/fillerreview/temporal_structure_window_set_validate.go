package fillerreview

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

// LoadTemporalStructureWindowSet verifies the source corpus, every packaged window, and their
// private truth join. It is the only supported loader for certification assembly.
func LoadTemporalStructureWindowSet(manifestPath, authorityPath, corpusManifestPath, corpusAuthorityPath string) (TemporalStructureWindowSetManifest, TemporalStructureWindowSetAuthority, string, string, error) {
	corpusManifest, corpusAuthority, corpusManifestSHA, corpusAuthoritySHA, err := LoadTemporalStructureWindowCorpusMedia(
		corpusManifestPath, corpusAuthorityPath, TemporalStructureWindowCorpusCases,
	)
	if err != nil {
		return TemporalStructureWindowSetManifest{}, TemporalStructureWindowSetAuthority{}, "", "", err
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(manifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		return TemporalStructureWindowSetManifest{}, TemporalStructureWindowSetAuthority{}, "", "", err
	}
	authorityRaw, err := os.ReadFile(authorityPath)
	if err != nil {
		return TemporalStructureWindowSetManifest{}, TemporalStructureWindowSetAuthority{}, "", "", fmt.Errorf("read private window set authority: %w", err)
	}
	authority, err := readStrictJSON[TemporalStructureWindowSetAuthority](authorityPath)
	if err != nil {
		return TemporalStructureWindowSetManifest{}, TemporalStructureWindowSetAuthority{}, "", "", fmt.Errorf("decode private window set authority: %w", err)
	}
	if err := validateTemporalStructureWindowSet(manifestPath, manifest, authority, manifestSHA, corpusManifest, corpusAuthority, corpusManifestSHA, corpusAuthoritySHA); err != nil {
		return TemporalStructureWindowSetManifest{}, TemporalStructureWindowSetAuthority{}, "", "", err
	}
	return manifest, authority, manifestSHA, hashBytes(authorityRaw), nil
}

// LoadTemporalStructureWindowSetPublic validates the complete blinded media-set surface without
// opening construction truth. Paid assessor tooling must use this loader.
func LoadTemporalStructureWindowSetPublic(manifestPath string, expectedCases int) (TemporalStructureWindowSetManifest, string, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return TemporalStructureWindowSetManifest{}, "", fmt.Errorf("read public window set manifest: %w", err)
	}
	manifest, err := readStrictJSON[TemporalStructureWindowSetManifest](manifestPath)
	if err != nil {
		return TemporalStructureWindowSetManifest{}, "", fmt.Errorf("decode public window set manifest: %w", err)
	}
	if err := validateTemporalStructureWindowSetPublic(filepath.Dir(manifestPath), manifest, expectedCases); err != nil {
		return TemporalStructureWindowSetManifest{}, "", err
	}
	return manifest, hashBytes(raw), nil
}

func validateTemporalStructureWindowSet(manifestPath string, manifest TemporalStructureWindowSetManifest, authority TemporalStructureWindowSetAuthority, manifestSHA string, corpusManifest TemporalStructureWindowMediaManifest, corpusAuthority TemporalStructureWindowMediaAuthority, corpusManifestSHA, corpusAuthoritySHA string) error {
	if err := validateTemporalStructureWindowSetPublic(filepath.Dir(manifestPath), manifest, len(manifest.Cases)); err != nil {
		return err
	}
	if authority.SchemaVersion != TemporalStructureWindowSetSchemaVersion || authority.ContractVersion != TemporalStructureWindowSetContractVersion ||
		authority.PreparedAt != manifest.PreparedAt || authority.PreparedAt != authority.PreparedAt.UTC() ||
		authority.CorpusManifestSHA256 != corpusManifestSHA || authority.CorpusAuthoritySHA256 != corpusAuthoritySHA ||
		authority.PublicManifestSHA256 != manifestSHA || manifest.CorpusManifestSHA256 != corpusManifestSHA ||
		len(authority.Cases) != len(manifest.Cases) || authority.TrainingAllowed || authority.ProductionAllowed {
		return errors.New("private window set authority does not bind its public and source authorities")
	}
	corpusPublicByAlias := make(map[string]TemporalStructureWindowMediaPublicCase, len(corpusManifest.Cases))
	for _, item := range corpusManifest.Cases {
		corpusPublicByAlias[item.Alias] = item
	}
	corpusPrivateByAlias := make(map[string]TemporalStructureWindowMediaAuthorityCase, len(corpusAuthority.Cases))
	for _, item := range corpusAuthority.Cases {
		corpusPrivateByAlias[item.Alias] = item
	}
	setPublicByAlias := make(map[string]TemporalStructureWindowSetPublicCase, len(manifest.Cases))
	for _, item := range manifest.Cases {
		setPublicByAlias[item.Alias] = item
	}
	seenAliases := make(map[string]struct{}, len(authority.Cases))
	seenCases := make(map[string]struct{}, len(authority.Cases))
	for index, item := range authority.Cases {
		setPublic, setFound := setPublicByAlias[item.Alias]
		corpusPublic, corpusPublicFound := corpusPublicByAlias[item.Alias]
		corpusPrivate, corpusPrivateFound := corpusPrivateByAlias[item.Alias]
		_, duplicateAlias := seenAliases[item.Alias]
		_, duplicateCase := seenCases[item.CaseID]
		if !setFound || !corpusPublicFound || !corpusPrivateFound || duplicateAlias || duplicateCase ||
			item.CaseID != corpusPrivate.CaseID || !reflect.DeepEqual(item.Truth, corpusPrivate.Truth) ||
			setPublic.Source != corpusPublic.Source || !reflect.DeepEqual(setPublic.MediaSet.Plan, corpusPublic.Plan) ||
			!completeTemporalStructureWindowTruth(item.Truth, setPublic.Source.DurationMs) {
			return fmt.Errorf("private window set case %d drifted from source corpus authority", index)
		}
		seenAliases[item.Alias] = struct{}{}
		seenCases[item.CaseID] = struct{}{}
	}
	return nil
}

func validateTemporalStructureWindowSetPublic(publicRoot string, manifest TemporalStructureWindowSetManifest, expectedCases int) error {
	if expectedCases <= 0 || manifest.SchemaVersion != TemporalStructureWindowSetSchemaVersion ||
		manifest.ContractVersion != TemporalStructureWindowSetContractVersion || manifest.PreparedAt.IsZero() ||
		manifest.PreparedAt != manifest.PreparedAt.UTC() || !reviewSHA256(manifest.CorpusManifestSHA256) ||
		manifest.AssessmentMediaProfileSHA256 != fillerstructuremedia.CanonicalProfile().SHA256 ||
		len(manifest.Cases) != expectedCases || manifest.TrainingAllowed || manifest.ProductionAdmissionAllowed {
		return errors.New("public window set identity, count, or disposition is invalid")
	}
	aliases := make(map[string]struct{}, len(manifest.Cases))
	mediaSets := make(map[string]struct{}, len(manifest.Cases))
	for index, item := range manifest.Cases {
		if len(item.Alias) != len("case-")+24 || !strings.HasPrefix(item.Alias, "case-") || !isLowerHex(item.Alias[len("case-"):]) {
			return fmt.Errorf("public window set case %d has invalid alias", index)
		}
		if _, duplicate := aliases[item.Alias]; duplicate {
			return fmt.Errorf("public window set repeats alias %q", item.Alias)
		}
		aliases[item.Alias] = struct{}{}
		if _, duplicate := mediaSets[item.MediaSet.SHA256]; duplicate {
			return fmt.Errorf("public window set repeats media set %q", item.MediaSet.SHA256)
		}
		mediaSets[item.MediaSet.SHA256] = struct{}{}
		wantSourcePath := filepath.ToSlash(filepath.Join("cases", item.Alias, "source.mp4"))
		if item.Source.Role != filler.SplitSourceLegacyPlayback || item.Source.Path != wantSourcePath ||
			!reviewSHA256(item.Source.SHA256) || !reviewSHA256(item.Source.ClipHash) || item.Source.Bytes <= 0 ||
			item.Source.Bytes > TemporalStructureWindowMaximumSourceBytes || item.Source.DurationMs <= fillerstructurewindow.PrimarySpanMS ||
			item.MediaSet.Plan.Source.SHA256 != item.Source.SHA256 || item.MediaSet.Plan.Source.Bytes != item.Source.Bytes ||
			item.MediaSet.Plan.Source.DurationMS != item.Source.DurationMs || fillerstructurewindow.ValidateMediaSet(item.MediaSet) != nil ||
			len(item.Windows) != len(item.MediaSet.Windows) {
			return fmt.Errorf("public window set case %d has invalid source or media set", index)
		}
		if err := verifyTemporalStructureWindowSetFile(publicRoot, item.Source.Path, item.Source.SHA256, item.Source.Bytes, item.Source.ClipHash); err != nil {
			return fmt.Errorf("public window set case %d source: %w", index, err)
		}
		for ordinal, window := range item.Windows {
			media := item.MediaSet.Windows[ordinal]
			clean := filepath.Clean(filepath.FromSlash(window.Path))
			if window.Ordinal != ordinal || media.Ordinal != ordinal || window.Path != filepath.ToSlash(clean) || filepath.IsAbs(clean) ||
				clean == "." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) ||
				!pathContainsReview(filepath.Join(filler.MediaAssetRootName, "structure-assessment", "media"), clean) {
				return fmt.Errorf("public window set case %d window %d has unsafe path authority", index, ordinal)
			}
			if err := verifyTemporalStructureWindowSetFile(publicRoot, window.Path, media.Media.SHA256, media.Media.Bytes, ""); err != nil {
				return fmt.Errorf("public window set case %d window %d: %w", index, ordinal, err)
			}
		}
	}
	return nil
}

func verifyTemporalStructureWindowSetFile(root, relative, digest string, size int64, clipHash string) error {
	fullPath := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(fullPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() != size || size <= 0 || size > TemporalStructureWindowMaximumSourceBytes {
		return errors.New("file is not the declared bounded regular file")
	}
	gotDigest, gotSize, err := filler.FileSHA256(fullPath)
	if err != nil || gotDigest != digest || gotSize != size {
		return errors.New("file bytes drifted")
	}
	if clipHash != "" {
		gotClipHash, err := filler.ClipID(fullPath)
		if err != nil || gotClipHash != clipHash {
			return errors.New("file sparse identity drifted")
		}
	}
	return nil
}

func auditTemporalStructureWindowSetLeakage(manifestRaw []byte, corpusAuthority TemporalStructureWindowMediaAuthority) error {
	public := string(manifestRaw)
	secrets := make([]string, 0, len(corpusAuthority.CorpusPlan.Cases)*4)
	for _, item := range corpusAuthority.CorpusPlan.Cases {
		secrets = append(secrets, item.ID, item.Pattern)
		secrets = append(secrets, item.FillerFamilyIDs...)
		for _, truth := range item.Truth {
			secrets = append(secrets, string(truth.Role))
		}
		for _, segment := range item.Segments {
			secrets = append(secrets, segment.SourceID)
		}
	}
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" && strings.Contains(public, secret) {
			return fmt.Errorf("public window set leaks coordinator-private value %q", secret)
		}
	}
	return nil
}

func pathContainsReview(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != "." && !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}
