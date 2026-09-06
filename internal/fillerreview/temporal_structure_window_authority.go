package fillerreview

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

type TemporalStructureWindowAuthorityConfig struct {
	WindowSetManifestPath           string
	WindowCertificationPath         string
	CompleteDecisionSetPath         string
	WindowDecisionSetPath           string
	ShortLongShadowPath             string
	ReviewerID                      string
	ReviewedAt                      time.Time
	AutomaticMaterializationAllowed bool
	OutputPath                      string
}

// PublishTemporalStructureWindowAuthority issues the separately reviewed release boundary for
// only the exact source, media, unit, role, and assessor envelope proven by a passing shadow.
func PublishTemporalStructureWindowAuthority(config TemporalStructureWindowAuthorityConfig) (fillerstructurewindow.MaterializationAuthority, string, error) {
	for _, path := range []string{config.WindowSetManifestPath, config.WindowCertificationPath, config.CompleteDecisionSetPath, config.WindowDecisionSetPath, config.ShortLongShadowPath, config.OutputPath} {
		if strings.TrimSpace(path) == "" {
			return fillerstructurewindow.MaterializationAuthority{}, "", errors.New("window materialization authority requires manifest, certificate, decision sets, shadow, and output paths")
		}
	}
	if strings.TrimSpace(config.ReviewerID) != config.ReviewerID || config.ReviewerID == "" || len(config.ReviewerID) > 128 ||
		config.ReviewedAt.IsZero() || config.ReviewedAt != config.ReviewedAt.UTC() || !config.AutomaticMaterializationAllowed {
		return fillerstructurewindow.MaterializationAuthority{}, "", errors.New("window materialization authority requires bounded reviewer identity, canonical review time, and explicit permission")
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(config.WindowSetManifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		return fillerstructurewindow.MaterializationAuthority{}, "", err
	}
	certificate, certificateFileSHA, err := loadTemporalStructureWindowCertification(config.WindowCertificationPath)
	if err != nil {
		return fillerstructurewindow.MaterializationAuthority{}, "", err
	}
	if err := requirePassingWindowCertification(certificate); err != nil {
		return fillerstructurewindow.MaterializationAuthority{}, "", err
	}
	complete, completeFileSHA, err := LoadTemporalStructureShadowDecisionSet(config.CompleteDecisionSetPath, config.WindowSetManifestPath)
	if err != nil {
		return fillerstructurewindow.MaterializationAuthority{}, "", err
	}
	windows, windowsFileSHA, err := LoadTemporalStructureShadowDecisionSet(config.WindowDecisionSetPath, config.WindowSetManifestPath)
	if err != nil {
		return fillerstructurewindow.MaterializationAuthority{}, "", err
	}
	shadow, err := readStrictJSON[TemporalStructureShortLongShadowArtifact](config.ShortLongShadowPath)
	if err != nil {
		return fillerstructurewindow.MaterializationAuthority{}, "", fmt.Errorf("decode short-long shadow: %w", err)
	}
	if err := ValidateTemporalStructureShortLongShadowArtifact(shadow); err != nil {
		return fillerstructurewindow.MaterializationAuthority{}, "", err
	}
	if shadow.WindowSetManifestSHA256 != manifestSHA || shadow.WindowCertificationSHA256 != certificate.SHA256 ||
		shadow.WindowCertificationFileSHA256 != certificateFileSHA || shadow.CompleteDecisionSetSHA256 != complete.SHA256 ||
		shadow.CompleteDecisionSetFileSHA256 != completeFileSHA || shadow.WindowDecisionSetSHA256 != windows.SHA256 ||
		shadow.WindowDecisionSetFileSHA256 != windowsFileSHA ||
		config.ReviewedAt.Before(shadow.Report.ComparedAt) || shadow.Report.Status != fillerstructurewindowcert.ShadowStatusPassed ||
		shadow.Report.PassedCases != TemporalStructureWindowCorpusCases || shadow.Report.FailedCases != 0 ||
		len(shadow.Report.FailureCodes) != 0 || shadow.Report.NextAction != "issue_separately_reviewed_long_reel_materialization_authority" {
		return fillerstructurewindow.MaterializationAuthority{}, "", errors.New("window materialization authority requires the exact complete passing shadow lineage")
	}
	if !sameWindowCertificationFamilies(certificate.Families, windows.Families) {
		return fillerstructurewindow.MaterializationAuthority{}, "", errors.New("window materialization authority family lineage drifted")
	}
	aliases, cases, err := bindTemporalStructureShortLongCases(manifest, complete, windows)
	if err != nil {
		return fillerstructurewindow.MaterializationAuthority{}, "", err
	}
	replayed, err := fillerstructurewindowcert.CompareShortLong(manifestSHA, certificate.SHA256, aliases, cases, shadow.Report.ComparedAt)
	if err != nil || !reflect.DeepEqual(replayed, shadow.Report) {
		return fillerstructurewindow.MaterializationAuthority{}, "", errors.New("window materialization shadow does not replay from its decision sets")
	}
	authority, err := temporalStructureWindowMaterializationAuthority(config, certificate, shadow, manifest, windows)
	if err != nil {
		return fillerstructurewindow.MaterializationAuthority{}, "", err
	}
	raw, err := json.MarshalIndent(authority, "", "  ")
	if err != nil {
		return fillerstructurewindow.MaterializationAuthority{}, "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return fillerstructurewindow.MaterializationAuthority{}, "", fmt.Errorf("publish window materialization authority: %w", err)
	}
	return authority, hashBytes(raw), nil
}

func temporalStructureWindowMaterializationAuthority(config TemporalStructureWindowAuthorityConfig, certificate TemporalStructureWindowCertificationArtifact, shadow TemporalStructureShortLongShadowArtifact, manifest TemporalStructureWindowSetManifest, windows TemporalStructureShadowDecisionSet) (fillerstructurewindow.MaterializationAuthority, error) {
	minimumDuration, maximumDuration := int64(0), int64(0)
	maximumBytes, maximumWindows := int64(0), 0
	unitSet := make(map[fillerstructure.Unit]struct{})
	roleSet := make(map[fillerstructure.Role]struct{})
	publicByAlias := make(map[string]TemporalStructureWindowSetPublicCase, len(manifest.Cases))
	for _, item := range manifest.Cases {
		publicByAlias[item.Alias] = item
	}
	for _, item := range windows.Cases {
		decision := item.Artifact.Decision
		public, ok := publicByAlias[item.Alias]
		if !ok || decision.Status != fillerstructure.StatusConfirmed || decision.Source.SHA256 != public.Source.SHA256 {
			return fillerstructurewindow.MaterializationAuthority{}, errors.New("window materialization authority cannot include a held or drifted case")
		}
		if minimumDuration == 0 || decision.Source.DurationMS < minimumDuration {
			minimumDuration = decision.Source.DurationMS
		}
		maximumDuration = max(maximumDuration, decision.Source.DurationMS)
		maximumWindows = max(maximumWindows, len(decision.Input.Items))
		unitSet[decision.Unit] = struct{}{}
		for _, media := range decision.Input.Items {
			maximumBytes = max(maximumBytes, media.Bytes)
		}
		for _, segment := range decision.Segments {
			roleSet[segment.Role] = struct{}{}
		}
	}
	units := make([]fillerstructure.Unit, 0, len(unitSet))
	for unit := range unitSet {
		units = append(units, unit)
	}
	roles := make([]fillerstructure.Role, 0, len(roleSet))
	for role := range roleSet {
		roles = append(roles, role)
	}
	slices.Sort(units)
	slices.Sort(roles)
	profiles := make([]fillerstructure.AssessorProfile, 0, len(windows.Families))
	for _, family := range windows.Families {
		profiles = append(profiles, family.Assessor)
	}
	authority := fillerstructurewindow.MaterializationAuthority{
		SchemaVersion:             fillerstructurewindow.MaterializationAuthoritySchemaVersion,
		ContractVersion:           fillerstructurewindow.MaterializationAuthorityContractVersion,
		WindowCertificationSHA256: certificate.SHA256, ShortLongShadowSHA256: shadow.SHA256,
		WindowProfileSHA256:          fillerstructurewindow.CanonicalProfile().SHA256,
		AssessmentMediaProfileSHA256: manifest.AssessmentMediaProfileSHA256,
		MinimumSourceDurationMS:      minimumDuration, MaximumSourceDurationMS: maximumDuration,
		MaximumWindowBytes: maximumBytes, MaximumWindows: maximumWindows,
		ReducerVersion: windows.ReducerVersion, BoundaryToleranceMS: windows.BoundaryToleranceMS,
		Assessors: profiles, AllowedUnits: units, AllowedRoles: roles,
		ReviewerID: config.ReviewerID, ReviewedAt: config.ReviewedAt,
		AutomaticMaterializationAllowed: config.AutomaticMaterializationAllowed,
	}
	authority.SHA256 = fillerstructurewindow.MaterializationAuthoritySHA256(authority)
	if err := fillerstructurewindow.ValidateMaterializationAuthority(authority); err != nil {
		return fillerstructurewindow.MaterializationAuthority{}, err
	}
	return authority, nil
}
