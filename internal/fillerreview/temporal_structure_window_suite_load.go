package fillerreview

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

type temporalStructureWindowSuiteLoaded struct {
	windowSetManifest     TemporalStructureWindowSetManifest
	windowSetAuthority    TemporalStructureWindowSetAuthority
	windowSetManifestSHA  string
	windowSetAuthoritySHA string
	corpusManifest        TemporalStructureWindowMediaManifest
	corpusAuthority       TemporalStructureWindowMediaAuthority
	corpusManifestSHA     string
	corpusAuthoritySHA    string
	authoring             TemporalStructureChallengeAuthoring
	authoringSHA          string
	receipt               TemporalStructureHoldoutReceipt
	receiptSHA            string
	evidenceManifest      TemporalTruthEvidenceManifest
	evidenceManifestSHA   string
	evidencePrivateMap    TemporalTruthEvidencePrivateMap
	evidencePrivateMapSHA string
}

func loadTemporalStructureWindowSuite(config TemporalStructureWindowSuiteConfig) (temporalStructureWindowSuiteLoaded, error) {
	paths := []string{
		config.WindowSetManifestPath, config.WindowSetAuthorityPath, config.CorpusManifestPath,
		config.CorpusAuthorityPath, config.HoldoutAuthoringPath, config.HoldoutReceiptPath,
		config.EvidenceManifestPath, config.EvidencePrivateMapPath, config.OutputDir,
	}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return temporalStructureWindowSuiteLoaded{}, errors.New("window certification suite requires every prepared-media and locked-evidence path")
		}
	}
	if config.MeasuredAt.IsZero() || config.Motion == nil {
		return temporalStructureWindowSuiteLoaded{}, errors.New("window certification suite requires fixed measurement time and motion measurer")
	}
	windowSetManifest, windowSetAuthority, windowSetManifestSHA, windowSetAuthoritySHA, err := LoadTemporalStructureWindowSet(
		config.WindowSetManifestPath, config.WindowSetAuthorityPath, config.CorpusManifestPath, config.CorpusAuthorityPath,
	)
	if err != nil {
		return temporalStructureWindowSuiteLoaded{}, err
	}
	corpusManifest, corpusAuthority, corpusManifestSHA, corpusAuthoritySHA, err := LoadTemporalStructureWindowCorpusMedia(
		config.CorpusManifestPath, config.CorpusAuthorityPath, TemporalStructureWindowCorpusCases,
	)
	if err != nil {
		return temporalStructureWindowSuiteLoaded{}, err
	}
	authoringRaw, err := os.ReadFile(config.HoldoutAuthoringPath)
	if err != nil {
		return temporalStructureWindowSuiteLoaded{}, fmt.Errorf("read window suite holdout authoring: %w", err)
	}
	receiptRaw, err := os.ReadFile(config.HoldoutReceiptPath)
	if err != nil {
		return temporalStructureWindowSuiteLoaded{}, fmt.Errorf("read window suite holdout receipt: %w", err)
	}
	authoring, err := readStrictJSON[TemporalStructureChallengeAuthoring](config.HoldoutAuthoringPath)
	if err != nil {
		return temporalStructureWindowSuiteLoaded{}, fmt.Errorf("decode window suite holdout authoring: %w", err)
	}
	receipt, err := readStrictJSON[TemporalStructureHoldoutReceipt](config.HoldoutReceiptPath)
	if err != nil {
		return temporalStructureWindowSuiteLoaded{}, fmt.Errorf("decode window suite holdout receipt: %w", err)
	}
	authoringSHA, receiptSHA := hashBytes(authoringRaw), hashBytes(receiptRaw)
	if corpusAuthority.CorpusPlan.HoldoutAuthoringSHA256 != authoringSHA ||
		corpusAuthority.CorpusPlan.HoldoutReceiptSHA256 != receiptSHA || receipt.AuthoringSHA256 != authoringSHA {
		return temporalStructureWindowSuiteLoaded{}, errors.New("window suite holdout authority does not bind rendered corpus")
	}
	if err := validateTemporalStructureHoldoutReceipt(receipt, authoring, nil); err != nil {
		return temporalStructureWindowSuiteLoaded{}, err
	}
	evidenceManifest, evidenceManifestSHA, err := LoadTemporalTruthEvidence(config.EvidenceManifestPath)
	if err != nil {
		return temporalStructureWindowSuiteLoaded{}, err
	}
	evidencePrivateRaw, err := os.ReadFile(config.EvidencePrivateMapPath)
	if err != nil {
		return temporalStructureWindowSuiteLoaded{}, fmt.Errorf("read window suite evidence map: %w", err)
	}
	evidencePrivateMap, err := readStrictJSON[TemporalTruthEvidencePrivateMap](config.EvidencePrivateMapPath)
	if err != nil {
		return temporalStructureWindowSuiteLoaded{}, fmt.Errorf("decode window suite evidence map: %w", err)
	}
	if err := validateTemporalHumanEvidenceJoin(evidenceManifest, evidenceManifestSHA, evidencePrivateMap); err != nil {
		return temporalStructureWindowSuiteLoaded{}, err
	}
	evidencePrivateMapSHA := hashBytes(evidencePrivateRaw)
	if !receiptBindsWindowSuiteEvidence(receipt, evidenceManifestSHA, evidencePrivateMapSHA) {
		return temporalStructureWindowSuiteLoaded{}, errors.New("window suite transcript evidence is not bound by holdout receipt")
	}
	return temporalStructureWindowSuiteLoaded{
		windowSetManifest: windowSetManifest, windowSetAuthority: windowSetAuthority,
		windowSetManifestSHA: windowSetManifestSHA, windowSetAuthoritySHA: windowSetAuthoritySHA,
		corpusManifest: corpusManifest, corpusAuthority: corpusAuthority,
		corpusManifestSHA: corpusManifestSHA, corpusAuthoritySHA: corpusAuthoritySHA,
		authoring: authoring, authoringSHA: authoringSHA, receipt: receipt, receiptSHA: receiptSHA,
		evidenceManifest: evidenceManifest, evidenceManifestSHA: evidenceManifestSHA,
		evidencePrivateMap: evidencePrivateMap, evidencePrivateMapSHA: evidencePrivateMapSHA,
	}, nil
}

func receiptBindsWindowSuiteEvidence(receipt TemporalStructureHoldoutReceipt, evidenceSHA, privateMapSHA string) bool {
	want := map[string]string{"evidence_manifest": evidenceSHA, "evidence_private_map": privateMapSHA}
	for _, input := range receipt.Inputs {
		if digest, ok := want[input.Name]; ok && input.SHA256 == digest {
			delete(want, input.Name)
		}
	}
	return len(want) == 0
}

func LoadTemporalStructureWindowCertificationSuite(path string) (fillerstructurewindowcert.Suite, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fillerstructurewindowcert.Suite{}, "", err
	}
	suite, err := readStrictJSON[fillerstructurewindowcert.Suite](path)
	if err != nil {
		return fillerstructurewindowcert.Suite{}, "", err
	}
	if err := fillerstructurewindowcert.ValidateSuite(suite); err != nil {
		return fillerstructurewindowcert.Suite{}, "", err
	}
	return suite, hashBytes(raw), nil
}
