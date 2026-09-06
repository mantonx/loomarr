package fillerreview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const temporalStructureChallengeArchiveV1Contract = "filler-temporal-structure-challenge-v1"

type temporalStructureChallengeArchiveV1Manifest struct {
	SchemaVersion              int                                             `json:"schemaVersion"`
	ContractVersion            string                                          `json:"contractVersion"`
	ChallengeID                string                                          `json:"challengeId"`
	GeneratedAt                time.Time                                       `json:"generatedAt"`
	Cases                      []temporalStructureChallengeArchiveV1PublicCase `json:"cases"`
	ProductionAdmissionAllowed *bool                                           `json:"productionAdmissionAllowed"`
}

type temporalStructureChallengeArchiveV1PublicCase struct {
	Alias string                    `json:"alias"`
	Video TemporalTruthEvidenceFile `json:"video"`
}

type temporalStructureChallengeArchiveV1Authority struct {
	SchemaVersion        int                                       `json:"schemaVersion"`
	ContractVersion      string                                    `json:"contractVersion"`
	ChallengeID          string                                    `json:"challengeId"`
	GeneratedAt          time.Time                                 `json:"generatedAt"`
	AuthoringSHA256      string                                    `json:"authoringSha256"`
	SeedSHA256           string                                    `json:"seedSha256"`
	PublicManifestSHA256 string                                    `json:"publicManifestSha256"`
	MediaTools           TemporalTruthMediaIdentity                `json:"mediaTools"`
	Cases                []TemporalStructureChallengeAuthorityCase `json:"cases"`
}

type temporalSuitabilityProjectionLoaded struct {
	manifest      TemporalStructureChallengeManifest
	authority     TemporalStructureChallengeAuthority
	comparison    TemporalSuitabilityComparisonReport
	first         TemporalSuitabilityResult
	second        TemporalSuitabilityResult
	manifestSHA   string
	authoritySHA  string
	comparisonSHA string
	firstSHA      string
	secondSHA     string
}

func loadTemporalSuitabilityProjection(config TemporalSuitabilityProjectionConfig) (temporalSuitabilityProjectionLoaded, error) {
	if strings.TrimSpace(config.PublicManifestPath) == "" || strings.TrimSpace(config.StructureAuthorityPath) == "" || strings.TrimSpace(config.SuitabilityComparisonPath) == "" || strings.TrimSpace(config.FirstResultPath) == "" || strings.TrimSpace(config.SecondResultPath) == "" || strings.TrimSpace(config.OutputPath) == "" || config.ExpectedCases <= 0 || config.ProjectedAt.IsZero() {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("suitability projection requires challenge, comparison, two results, exact count, fixed time, and output")
	}
	manifest, authority, manifestSHA, authoritySHA, err := loadTemporalStructureChallengeForProjection(config.PublicManifestPath, config.StructureAuthorityPath, config.ExpectedCases)
	if err != nil {
		return temporalSuitabilityProjectionLoaded{}, err
	}
	evidence := temporalStructureChallengeSuitabilityEvidence(manifest, authoritySHA)
	comparison, comparisonSHA, err := loadTemporalStructureHoldoutSuitability(config.SuitabilityComparisonPath, evidence, manifestSHA)
	if err != nil {
		return temporalSuitabilityProjectionLoaded{}, err
	}
	first, firstSHA, err := loadTemporalSuitabilityResultAuthority(config.FirstResultPath, evidence, manifestSHA, config.ExpectedCases)
	if err != nil {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("first suitability result: %w", err)
	}
	second, secondSHA, err := loadTemporalSuitabilityResultAuthority(config.SecondResultPath, evidence, manifestSHA, config.ExpectedCases)
	if err != nil {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("second suitability result: %w", err)
	}
	if comparison.FirstResultSHA256 != firstSHA || comparison.SecondResultSHA256 != secondSHA || comparison.FirstAssessor != first.Assessor || comparison.SecondAssessor != second.Assessor || first.SelectionSHA256 != second.SelectionSHA256 {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("suitability projection results do not bind the locked comparison")
	}
	recomputed, err := CompareTemporalSuitabilityResults(first, second, manifestSHA, first.SelectionSHA256, firstSHA, secondSHA, comparison.ComparedAt)
	if err != nil {
		return temporalSuitabilityProjectionLoaded{}, err
	}
	if hashJSON(recomputed) != hashJSON(comparison) {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("suitability projection comparison does not reproduce from its results")
	}
	if config.ProjectedAt.Before(comparison.ComparedAt) || config.ProjectedAt.Before(first.CompletedAt) || config.ProjectedAt.Before(second.CompletedAt) {
		return temporalSuitabilityProjectionLoaded{}, fmt.Errorf("suitability projection predates its authority")
	}
	return temporalSuitabilityProjectionLoaded{
		manifest: manifest, authority: authority, comparison: comparison, first: first, second: second,
		manifestSHA: manifestSHA, authoritySHA: authoritySHA, comparisonSHA: comparisonSHA, firstSHA: firstSHA, secondSHA: secondSHA,
	}, nil
}

func loadTemporalStructureChallengeForProjection(manifestPath, authorityPath string, expectedCases int) (TemporalStructureChallengeManifest, TemporalStructureChallengeAuthority, string, string, error) {
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return TemporalStructureChallengeManifest{}, TemporalStructureChallengeAuthority{}, "", "", fmt.Errorf("read projection challenge manifest: %w", err)
	}
	var header temporalSuitabilityEvidenceHeader
	if err := json.Unmarshal(manifestRaw, &header); err != nil {
		return TemporalStructureChallengeManifest{}, TemporalStructureChallengeAuthority{}, "", "", fmt.Errorf("decode projection challenge header: %w", err)
	}
	if header.ContractVersion != temporalStructureChallengeArchiveV1Contract {
		return LoadTemporalStructureChallenge(manifestPath, authorityPath, expectedCases)
	}
	var archivedManifest temporalStructureChallengeArchiveV1Manifest
	if err := decodeStrictArchivedProjectionJSON(manifestRaw, &archivedManifest); err != nil {
		return TemporalStructureChallengeManifest{}, TemporalStructureChallengeAuthority{}, "", "", fmt.Errorf("decode archived challenge manifest: %w", err)
	}
	if archivedManifest.ProductionAdmissionAllowed == nil || *archivedManifest.ProductionAdmissionAllowed {
		return TemporalStructureChallengeManifest{}, TemporalStructureChallengeAuthority{}, "", "", fmt.Errorf("archived public challenge requires explicit negative production disposition")
	}
	authorityRaw, err := os.ReadFile(authorityPath)
	if err != nil {
		return TemporalStructureChallengeManifest{}, TemporalStructureChallengeAuthority{}, "", "", fmt.Errorf("read archived challenge authority: %w", err)
	}
	var archivedAuthority temporalStructureChallengeArchiveV1Authority
	if err := decodeStrictArchivedProjectionJSON(authorityRaw, &archivedAuthority); err != nil {
		return TemporalStructureChallengeManifest{}, TemporalStructureChallengeAuthority{}, "", "", fmt.Errorf("decode archived challenge authority: %w", err)
	}
	manifest := TemporalStructureChallengeManifest{SchemaVersion: archivedManifest.SchemaVersion, ContractVersion: archivedManifest.ContractVersion, ChallengeID: archivedManifest.ChallengeID, GeneratedAt: archivedManifest.GeneratedAt, Cases: make([]TemporalStructureChallengePublicCase, 0, len(archivedManifest.Cases))}
	for _, item := range archivedManifest.Cases {
		manifest.Cases = append(manifest.Cases, TemporalStructureChallengePublicCase{Alias: item.Alias, Video: item.Video})
	}
	authority := TemporalStructureChallengeAuthority{SchemaVersion: archivedAuthority.SchemaVersion, ContractVersion: archivedAuthority.ContractVersion, ChallengeID: archivedAuthority.ChallengeID, GeneratedAt: archivedAuthority.GeneratedAt, AuthoringSHA256: archivedAuthority.AuthoringSHA256, SeedSHA256: archivedAuthority.SeedSHA256, PublicManifestSHA256: archivedAuthority.PublicManifestSHA256, MediaTools: archivedAuthority.MediaTools, Cases: archivedAuthority.Cases}
	manifestSHA, authoritySHA := hashBytes(manifestRaw), hashBytes(authorityRaw)
	if err := validateTemporalStructureChallenge(filepath.Dir(manifestPath), manifest, authority, manifestSHA, expectedCases, temporalStructureChallengeArchiveV1Contract, false); err != nil {
		return TemporalStructureChallengeManifest{}, TemporalStructureChallengeAuthority{}, "", "", err
	}
	return manifest, authority, manifestSHA, authoritySHA, nil
}

func decodeStrictArchivedProjectionJSON(raw []byte, out any) error {
	if err := rejectDuplicateArchivedProjectionJSONKeys(raw); err != nil {
		return err
	}
	return decodeStrictReviewJSON(raw, out)
}

func rejectDuplicateArchivedProjectionJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := scanArchivedProjectionJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func scanArchivedProjectionJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object member name is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanArchivedProjectionJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanArchivedProjectionJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	_, err = decoder.Token()
	return err
}
