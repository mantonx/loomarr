package fillerreview

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

// PublishTemporalStructureShortLongShadow verifies a passing private window certificate and the
// exact family lineage of both truth-blind reducer representations before comparing every case.
func PublishTemporalStructureShortLongShadow(config TemporalStructureShortLongShadowConfig) (TemporalStructureShortLongShadowArtifact, string, error) {
	for _, path := range []string{config.WindowSetManifestPath, config.WindowCertificationPath, config.CompleteDecisionSetPath, config.WindowDecisionSetPath, config.OutputPath} {
		if strings.TrimSpace(path) == "" {
			return TemporalStructureShortLongShadowArtifact{}, "", errors.New("short-long shadow requires manifest, window certificate, both decision sets, and output paths")
		}
	}
	if config.ComparedAt.IsZero() || config.ComparedAt != config.ComparedAt.UTC() {
		return TemporalStructureShortLongShadowArtifact{}, "", errors.New("short-long shadow requires canonical UTC comparison time")
	}
	manifest, manifestSHA, err := LoadTemporalStructureWindowSetPublic(config.WindowSetManifestPath, TemporalStructureWindowCorpusCases)
	if err != nil {
		return TemporalStructureShortLongShadowArtifact{}, "", err
	}
	certificate, certificateFileSHA, err := loadTemporalStructureWindowCertification(config.WindowCertificationPath)
	if err != nil {
		return TemporalStructureShortLongShadowArtifact{}, "", err
	}
	if err := requirePassingWindowCertification(certificate); err != nil {
		return TemporalStructureShortLongShadowArtifact{}, "", err
	}
	if certificate.WindowSetManifestSHA256 != manifestSHA || config.ComparedAt.Before(certificate.Report.CertifiedAt) {
		return TemporalStructureShortLongShadowArtifact{}, "", errors.New("short-long shadow window certificate authority or time drifted")
	}
	complete, completeFileSHA, err := LoadTemporalStructureShadowDecisionSet(config.CompleteDecisionSetPath, config.WindowSetManifestPath)
	if err != nil {
		return TemporalStructureShortLongShadowArtifact{}, "", fmt.Errorf("load complete-video decision set: %w", err)
	}
	windows, windowsFileSHA, err := LoadTemporalStructureShadowDecisionSet(config.WindowDecisionSetPath, config.WindowSetManifestPath)
	if err != nil {
		return TemporalStructureShortLongShadowArtifact{}, "", fmt.Errorf("load window decision set: %w", err)
	}
	if complete.InputKind != fillerstructure.AssessmentInputCompleteVideo || windows.InputKind != fillerstructure.AssessmentInputWindowMediaSet ||
		config.ComparedAt.Before(complete.DecidedAt) || config.ComparedAt.Before(windows.DecidedAt) {
		return TemporalStructureShortLongShadowArtifact{}, "", errors.New("short-long shadow decision representation or time drifted")
	}
	if !sameWindowCertificationFamilies(certificate.Families, windows.Families) {
		return TemporalStructureShortLongShadowArtifact{}, "", errors.New("short-long shadow window decision set does not descend from the certified families")
	}
	expectedAliases, cases, err := bindTemporalStructureShortLongCases(manifest, complete, windows)
	if err != nil {
		return TemporalStructureShortLongShadowArtifact{}, "", err
	}
	report, err := fillerstructurewindowcert.CompareShortLong(manifestSHA, certificate.SHA256, expectedAliases, cases, config.ComparedAt)
	if err != nil {
		return TemporalStructureShortLongShadowArtifact{}, "", err
	}
	artifact := TemporalStructureShortLongShadowArtifact{
		SchemaVersion: TemporalStructureShortLongShadowSchemaVersion, ContractVersion: TemporalStructureShortLongShadowContractVersion,
		WindowSetManifestSHA256: manifestSHA, WindowCertificationSHA256: certificate.SHA256,
		WindowCertificationFileSHA256: certificateFileSHA,
		CompleteDecisionSetSHA256:     complete.SHA256, CompleteDecisionSetFileSHA256: completeFileSHA,
		WindowDecisionSetSHA256: windows.SHA256, WindowDecisionSetFileSHA256: windowsFileSHA,
		Report: report,
	}
	artifact.SHA256 = temporalStructureShortLongShadowSHA256(artifact)
	if err := ValidateTemporalStructureShortLongShadowArtifact(artifact); err != nil {
		return TemporalStructureShortLongShadowArtifact{}, "", err
	}
	raw, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return TemporalStructureShortLongShadowArtifact{}, "", err
	}
	raw = append(raw, '\n')
	if err := writeTemporalTruthNew(config.OutputPath, raw, 0o600); err != nil {
		return TemporalStructureShortLongShadowArtifact{}, "", fmt.Errorf("publish short-long shadow: %w", err)
	}
	return artifact, hashBytes(raw), nil
}

func loadTemporalStructureWindowCertification(path string) (TemporalStructureWindowCertificationArtifact, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return TemporalStructureWindowCertificationArtifact{}, "", fmt.Errorf("read window certification: %w", err)
	}
	artifact, err := readStrictJSON[TemporalStructureWindowCertificationArtifact](path)
	if err != nil {
		return TemporalStructureWindowCertificationArtifact{}, "", fmt.Errorf("decode window certification: %w", err)
	}
	if err := ValidateTemporalStructureWindowCertificationArtifact(artifact); err != nil {
		return TemporalStructureWindowCertificationArtifact{}, "", err
	}
	return artifact, hashBytes(raw), nil
}

func bindTemporalStructureShortLongCases(manifest TemporalStructureWindowSetManifest, complete, windows TemporalStructureShadowDecisionSet) ([]string, []fillerstructurewindowcert.ShadowCase, error) {
	completeByAlias := make(map[string]fillerstructure.Artifact, len(complete.Cases))
	windowByAlias := make(map[string]fillerstructure.Artifact, len(windows.Cases))
	for _, item := range complete.Cases {
		completeByAlias[item.Alias] = item.Artifact
	}
	for _, item := range windows.Cases {
		windowByAlias[item.Alias] = item.Artifact
	}
	aliases := make([]string, 0, len(manifest.Cases))
	cases := make([]fillerstructurewindowcert.ShadowCase, 0, len(manifest.Cases))
	for _, item := range manifest.Cases {
		short, shortOK := completeByAlias[item.Alias]
		long, longOK := windowByAlias[item.Alias]
		if !shortOK || !longOK {
			return nil, nil, errors.New("short-long shadow decision sets do not cover the public corpus")
		}
		aliases = append(aliases, item.Alias)
		cases = append(cases, fillerstructurewindowcert.ShadowCase{Alias: item.Alias, CompleteVideo: short, WindowMediaSet: long})
	}
	slices.Sort(aliases)
	slices.SortFunc(cases, func(left, right fillerstructurewindowcert.ShadowCase) int {
		return strings.Compare(left.Alias, right.Alias)
	})
	return aliases, cases, nil
}
