package fillerreview

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructuremedia"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

// LoadTemporalStructureWindowCorpusMedia re-establishes the complete public/private join and
// verifies every rendered source byte before returning certification input.
func LoadTemporalStructureWindowCorpusMedia(publicManifestPath, authorityPath string, expectedCases int) (TemporalStructureWindowMediaManifest, TemporalStructureWindowMediaAuthority, string, string, error) {
	manifest, manifestSHA, err := LoadTemporalStructureWindowCorpusMediaPublic(publicManifestPath, expectedCases)
	if err != nil {
		return TemporalStructureWindowMediaManifest{}, TemporalStructureWindowMediaAuthority{}, "", "", err
	}
	authorityRaw, err := os.ReadFile(authorityPath)
	if err != nil {
		return TemporalStructureWindowMediaManifest{}, TemporalStructureWindowMediaAuthority{}, "", "", fmt.Errorf("read private window corpus media authority: %w", err)
	}
	authority, err := readStrictJSON[TemporalStructureWindowMediaAuthority](authorityPath)
	if err != nil {
		return TemporalStructureWindowMediaManifest{}, TemporalStructureWindowMediaAuthority{}, "", "", fmt.Errorf("decode private window corpus media authority: %w", err)
	}
	if err := validateTemporalStructureWindowMedia(publicManifestPath, manifest, authority, manifestSHA); err != nil {
		return TemporalStructureWindowMediaManifest{}, TemporalStructureWindowMediaAuthority{}, "", "", err
	}
	return manifest, authority, manifestSHA, hashBytes(authorityRaw), nil
}

// LoadTemporalStructureWindowCorpusMediaPublic validates only the blinded source surface. Paid
// assessor tooling must use this loader so construction truth cannot enter its process.
func LoadTemporalStructureWindowCorpusMediaPublic(publicManifestPath string, expectedCases int) (TemporalStructureWindowMediaManifest, string, error) {
	manifestRaw, err := os.ReadFile(publicManifestPath)
	if err != nil {
		return TemporalStructureWindowMediaManifest{}, "", fmt.Errorf("read public window corpus media manifest: %w", err)
	}
	manifest, err := readStrictJSON[TemporalStructureWindowMediaManifest](publicManifestPath)
	if err != nil {
		return TemporalStructureWindowMediaManifest{}, "", fmt.Errorf("decode public window corpus media manifest: %w", err)
	}
	if err := validateTemporalStructureWindowMediaPublic(filepath.Dir(publicManifestPath), manifest, expectedCases); err != nil {
		return TemporalStructureWindowMediaManifest{}, "", err
	}
	return manifest, hashBytes(manifestRaw), nil
}

func validateTemporalStructureWindowMedia(publicManifestPath string, manifest TemporalStructureWindowMediaManifest, authority TemporalStructureWindowMediaAuthority, manifestSHA string) error {
	if err := validateTemporalStructureWindowMediaPublic(filepath.Dir(publicManifestPath), manifest, len(manifest.Cases)); err != nil {
		return err
	}
	if authority.SchemaVersion != TemporalStructureWindowMediaSchemaVersion || authority.ContractVersion != TemporalStructureWindowMediaContractVersion ||
		authority.RenderedAt != manifest.RenderedAt || authority.RenderedAt != authority.RenderedAt.UTC() ||
		!reviewSHA256(authority.CorpusPlanFileSHA256) || authority.CorpusPlan.SHA256 != manifest.CorpusPlanSHA256 ||
		authority.PublicManifestSHA256 != manifestSHA || len(authority.Cases) != len(manifest.Cases) ||
		authority.TrainingAllowed || authority.ProductionAllowed {
		return errors.New("private window corpus media authority does not bind the public manifest")
	}
	if !reviewSHA256(authority.CorpusPlan.SHA256) || authority.CorpusPlan.SHA256 != temporalStructureWindowCorpusPlanSHA256(authority.CorpusPlan) ||
		authority.CorpusPlan.TrainingAllowed || authority.CorpusPlan.ProductionAllowed || len(authority.CorpusPlan.Cases) != len(authority.Cases) {
		return errors.New("private window corpus construction plan is invalid")
	}
	for name, identity := range map[string]TemporalTruthToolIdentity{"ffmpeg": authority.MediaTools.FFmpeg, "ffprobe": authority.MediaTools.FFprobe} {
		if strings.TrimSpace(identity.Path) == "" || strings.TrimSpace(identity.Version) == "" || !reviewSHA256(identity.BinarySHA256) {
			return fmt.Errorf("private window corpus %s identity is invalid", name)
		}
	}
	publicByAlias := make(map[string]TemporalStructureWindowMediaPublicCase, len(manifest.Cases))
	for _, item := range manifest.Cases {
		publicByAlias[item.Alias] = item
	}
	planByID := make(map[string]TemporalStructureWindowCorpusCase, len(authority.CorpusPlan.Cases))
	for _, item := range authority.CorpusPlan.Cases {
		planByID[item.ID] = item
	}
	seenAliases := make(map[string]struct{}, len(authority.Cases))
	seenCases := make(map[string]struct{}, len(authority.Cases))
	for index, item := range authority.Cases {
		publicCase, publicFound := publicByAlias[item.Alias]
		planCase, planFound := planByID[item.CaseID]
		_, duplicateAlias := seenAliases[item.Alias]
		_, duplicateCase := seenCases[item.CaseID]
		if !publicFound || !planFound || duplicateAlias || duplicateCase {
			return fmt.Errorf("private window corpus case %d has missing or repeated authority", index)
		}
		seenAliases[item.Alias] = struct{}{}
		seenCases[item.CaseID] = struct{}{}
		if err := validateTemporalStructureWindowMediaCase(publicCase, item, planCase); err != nil {
			return fmt.Errorf("private window corpus case %d: %w", index, err)
		}
	}
	return nil
}

func validateTemporalStructureWindowMediaPublic(publicRoot string, manifest TemporalStructureWindowMediaManifest, expectedCases int) error {
	profile := fillerstructuremedia.CanonicalProfile()
	if expectedCases <= 0 || manifest.SchemaVersion != TemporalStructureWindowMediaSchemaVersion ||
		manifest.ContractVersion != TemporalStructureWindowMediaContractVersion || manifest.RenderedAt.IsZero() ||
		manifest.RenderedAt != manifest.RenderedAt.UTC() || !reviewSHA256(manifest.CorpusPlanSHA256) ||
		manifest.AssessmentMediaProfileSHA256 != profile.SHA256 || len(manifest.Cases) != expectedCases ||
		manifest.TrainingAllowed || manifest.ProductionAdmissionAllowed {
		return errors.New("public window corpus media identity, count, or disposition is invalid")
	}
	aliases := make(map[string]struct{}, len(manifest.Cases))
	sources := make(map[string]struct{}, len(manifest.Cases))
	for index, item := range manifest.Cases {
		if len(item.Alias) != len("case-")+24 || !strings.HasPrefix(item.Alias, "case-") || !isLowerHex(item.Alias[len("case-"):]) {
			return fmt.Errorf("public window corpus case %d has invalid alias", index)
		}
		if _, duplicate := aliases[item.Alias]; duplicate {
			return fmt.Errorf("public window corpus repeats alias %q", item.Alias)
		}
		aliases[item.Alias] = struct{}{}
		wantPath := filepath.ToSlash(filepath.Join("cases", item.Alias, "source.mp4"))
		if item.Source.Role != filler.SplitSourceLegacyPlayback || item.Source.Path != wantPath ||
			!reviewSHA256(item.Source.SHA256) || !reviewSHA256(item.Source.ClipHash) || item.Source.Bytes <= 0 ||
			item.Source.Bytes > TemporalStructureWindowMaximumSourceBytes || item.Source.DurationMs <= fillerstructurewindow.PrimarySpanMS ||
			item.Source.DurationMs > fillerstructurewindow.MaximumSourceDurationMS || item.Video.Width != profile.Width || item.Video.Height != profile.Height {
			return fmt.Errorf("public window corpus case %d has invalid source identity", index)
		}
		if _, duplicate := sources[item.Source.SHA256]; duplicate {
			return fmt.Errorf("public window corpus repeats rendered source %q", item.Source.SHA256)
		}
		sources[item.Source.SHA256] = struct{}{}
		wantPlan, err := fillerstructurewindow.NewPlan(fillerstructure.Source{
			SHA256: item.Source.SHA256, Bytes: item.Source.Bytes, DurationMS: item.Source.DurationMs,
		})
		if err != nil || !reflect.DeepEqual(item.Plan, wantPlan) {
			return fmt.Errorf("public window corpus case %d has invalid complete-coverage plan", index)
		}
		fullPath := filepath.Join(publicRoot, filepath.FromSlash(item.Source.Path))
		info, err := os.Lstat(fullPath)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() != item.Source.Bytes {
			return fmt.Errorf("public window corpus case %d source is not the declared regular file", index)
		}
		digest, size, err := filler.FileSHA256(fullPath)
		if err != nil || digest != item.Source.SHA256 || size != item.Source.Bytes {
			return fmt.Errorf("public window corpus case %d source bytes drifted", index)
		}
		clipHash, err := filler.ClipID(fullPath)
		if err != nil || clipHash != item.Source.ClipHash {
			return fmt.Errorf("public window corpus case %d sparse identity drifted", index)
		}
	}
	return nil
}

func validateTemporalStructureWindowMediaCase(publicCase TemporalStructureWindowMediaPublicCase, authority TemporalStructureWindowMediaAuthorityCase, planCase TemporalStructureWindowCorpusCase) error {
	if authority.Alias != publicCase.Alias || authority.CaseID != planCase.ID || len(authority.Parts) != len(planCase.Segments) ||
		len(authority.Truth) != len(planCase.Truth) || len(authority.Parts) < 3 {
		return errors.New("rendered window corpus case cardinality or identity is invalid")
	}
	joins := make([]int64, 0, len(authority.Parts)-1)
	for index, part := range authority.Parts {
		plannedPart := planCase.Segments[index]
		plannedTruth := planCase.Truth[index]
		if part.Ordinal != index || part.SourceID != plannedPart.SourceID || part.SourceStartMS != plannedPart.StartMS ||
			part.RequestedMS != plannedPart.DurationMS || part.RenderedMS <= 0 ||
			absoluteInt64(part.RenderedMS-part.RequestedMS) > fillerstructure.AssessmentMediaMaximumTimelineDriftMS ||
			!reviewSHA256(part.SourceSHA256) || part.SourceDurationMS <= 0 ||
			part.SourceStartMS+part.RequestedMS > part.SourceDurationMS || part.OutputEndMS != part.OutputStartMS+part.RenderedMS ||
			(index == 0 && part.OutputStartMS != 0) || (index > 0 && part.OutputStartMS != authority.Parts[index-1].OutputEndMS) {
			return fmt.Errorf("part %d drifted from construction or render authority", index)
		}
		truth := authority.Truth[index]
		if truth.StartMS != part.OutputStartMS || truth.Role != plannedTruth.Role || truth.EndMS <= truth.StartMS ||
			(index < len(authority.Truth)-1 && truth.EndMS != part.OutputEndMS) {
			return fmt.Errorf("truth segment %d does not derive from its measured part", index)
		}
		if index > 0 {
			joins = append(joins, part.OutputStartMS)
		}
	}
	if absoluteInt64(authority.Parts[len(authority.Parts)-1].OutputEndMS-publicCase.Source.DurationMs) > fillerstructure.AssessmentMediaMaximumTimelineDriftMS ||
		authority.Truth[len(authority.Truth)-1].EndMS != publicCase.Source.DurationMs || !completeTemporalStructureWindowTruth(authority.Truth, publicCase.Source.DurationMs) {
		return errors.New("rendered window corpus truth does not cover the exact source")
	}
	challengeCase := TemporalStructureChallengeAuthorityCase{
		Alias: authority.Alias, CaseID: authority.CaseID, Unit: fillereval.UnitProgrammeSpots,
		VideoSHA256: publicCase.Source.SHA256, JoinTimesMS: joins, Segments: authority.Parts,
	}
	if err := validateTemporalStructureAuthorityCase(challengeCase, publicCase.Source.DurationMs); err != nil {
		return err
	}
	return validateTemporalStructureWindowSeam(authority, planCase)
}

func validateTemporalStructureWindowSeam(authority TemporalStructureWindowMediaAuthorityCase, planCase TemporalStructureWindowCorpusCase) error {
	seam := planCase.TargetSeamMS
	if seam != fillerstructurewindow.PrimarySpanMS || len(authority.Truth) < 3 ||
		authority.Truth[0].Role != fillerstructure.RoleProgrammeFragment ||
		authority.Truth[len(authority.Truth)-1].Role != fillerstructure.RoleProgrammeFragment {
		return errors.New("rendered window corpus seam context is invalid")
	}
	tolerance := fillerstructurewindowcert.BoundaryToleranceMS
	switch planCase.Pattern {
	case TemporalStructureWindowPatternSeamOverlap:
		boundary := authority.ObservedTargetBoundaryMS
		if len(authority.Truth) != 4 || boundary != authority.Truth[1].EndMS || authority.Truth[1].Role != authority.Truth[2].Role ||
			absoluteInt64(boundary-planCase.TargetBoundaryMS) > tolerance ||
			boundary <= seam-fillerstructurewindow.ContextOverlapMS || boundary >= seam+fillerstructurewindow.ContextOverlapMS {
			return errors.New("rendered window corpus overlap case left its planned slice")
		}
	case TemporalStructureWindowPatternSeamPrimaryLeft:
		boundary := authority.ObservedTargetBoundaryMS
		if len(authority.Truth) != 4 || boundary != authority.Truth[1].EndMS || authority.Truth[1].Role != authority.Truth[2].Role ||
			boundary < seam-tolerance || boundary >= seam {
			return errors.New("rendered window corpus left-owner case left its planned slice")
		}
	case TemporalStructureWindowPatternSeamPrimaryRight:
		boundary := authority.ObservedTargetBoundaryMS
		if len(authority.Truth) != 4 || boundary != authority.Truth[1].EndMS || authority.Truth[1].Role != authority.Truth[2].Role ||
			boundary < seam || boundary > seam+tolerance {
			return errors.New("rendered window corpus right-owner case left its planned slice")
		}
	case TemporalStructureWindowPatternCrossingSeam:
		if authority.ObservedTargetBoundaryMS != 0 || len(authority.Truth) != 3 ||
			authority.Truth[1].StartMS >= seam || authority.Truth[1].EndMS <= seam {
			return errors.New("rendered window corpus crossing case no longer crosses the seam")
		}
	case TemporalStructureWindowPatternDurationLowerEdge:
		if authority.ObservedTargetBoundaryMS != 0 || len(authority.Truth) != 3 ||
			planCase.DurationMS != TemporalStructureWindowLowerEdgeDurationMS ||
			publicDurationOutsideEdge(authority.Truth[len(authority.Truth)-1].EndMS, TemporalStructureWindowLowerEdgeDurationMS) {
			return errors.New("rendered window corpus lower-duration case left its planned edge")
		}
	case TemporalStructureWindowPatternDurationUpperEdge:
		if authority.ObservedTargetBoundaryMS != 0 || len(authority.Truth) != 3 ||
			planCase.DurationMS != TemporalStructureWindowUpperEdgeDurationMS ||
			publicDurationOutsideEdge(authority.Truth[len(authority.Truth)-1].EndMS, TemporalStructureWindowUpperEdgeDurationMS) {
			return errors.New("rendered window corpus upper-duration case left its planned edge")
		}
	default:
		return errors.New("rendered window corpus pattern is invalid")
	}
	return nil
}

func publicDurationOutsideEdge(observed, planned int64) bool {
	return absoluteInt64(observed-planned) > fillerstructurewindowcert.BoundaryToleranceMS
}

func completeTemporalStructureWindowTruth(truth []fillerstructure.Segment, durationMS int64) bool {
	next := int64(0)
	for _, segment := range truth {
		if segment.StartMS != next || segment.EndMS <= segment.StartMS || segment.EndMS > durationMS || segment.Role == "" {
			return false
		}
		next = segment.EndMS
	}
	return len(truth) > 0 && next == durationMS
}

func auditTemporalStructureWindowMediaLeakage(publicRoot string, manifestRaw []byte, authoring TemporalStructureChallengeAuthoring, receipt TemporalStructureHoldoutReceipt, plan TemporalStructureWindowCorpusPlan) error {
	publicText := string(manifestRaw)
	err := filepath.WalkDir(publicRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(publicRoot, path)
		if err != nil {
			return err
		}
		publicText += "\n" + filepath.ToSlash(relative)
		return nil
	})
	if err != nil {
		return err
	}
	secrets := make([]string, 0, len(authoring.Sources)*6+len(plan.Cases)*4)
	for _, source := range authoring.Sources {
		secrets = append(secrets, source.ID, source.Path, source.Provenance.Authority, source.Provenance.Reference, source.Provenance.MetadataSHA256)
	}
	for _, anchor := range receipt.SelectedAnchors {
		secrets = append(secrets, anchor.FamilyID)
	}
	for _, item := range plan.Cases {
		secrets = append(secrets, item.ID, item.Pattern)
		secrets = append(secrets, item.FillerFamilyIDs...)
	}
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" && strings.Contains(publicText, secret) {
			return fmt.Errorf("public window corpus media leaks coordinator-private value %q", secret)
		}
	}
	return nil
}
