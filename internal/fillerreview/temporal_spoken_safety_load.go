package fillerreview

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillereval"
)

type temporalSpokenSafetyLoaded struct {
	corpus            fillereval.Manifest
	corpusCases       map[string]fillereval.Case
	packets           map[string]fillerbakeoff.Packet
	evidence          TemporalTruthEvidenceManifest
	privateMap        TemporalTruthEvidencePrivateMap
	transcripts       map[string]fillerbakeoff.TranscriptArtifact
	structure         TemporalStructureChallengeAuthority
	policy            TemporalSpokenSafetyPolicy
	corpusSHA         string
	packetsSHA        string
	evidenceSHA       string
	privateMapSHA     string
	transcriptSetSHA  string
	transcriptFileSHA string
	structureSHA      string
	authoritySHA      string
	policySHA         string
	engine            fillerbakeoff.TranscriptEngineIdentity
}

func loadTemporalSpokenSafety(config TemporalSpokenSafetyConfig) (temporalSpokenSafetyLoaded, error) {
	for _, path := range []string{config.CorpusManifestPath, config.PacketsPath, config.CorpusRoot, config.EvidenceManifestPath, config.EvidencePrivateMapPath, config.TranscriptSetPath, config.StructureManifestPath, config.StructureAuthorityPath, config.PolicyPath, config.OutputPath} {
		if strings.TrimSpace(path) == "" {
			return temporalSpokenSafetyLoaded{}, fmt.Errorf("spoken-safety projection requires every corpus, authority, policy, transcript, and output path")
		}
	}
	if config.ExpectedCorpusCases <= 0 || config.ExpectedStructureCases <= 0 || config.ProjectedAt.IsZero() || strings.TrimSpace(config.EvidenceVersion) == "" || config.CorpusSplit != fillereval.SplitDevelopment && config.CorpusSplit != fillereval.SplitHoldout {
		return temporalSpokenSafetyLoaded{}, fmt.Errorf("spoken-safety projection requires exact corpus and structure counts, an evidence version, an explicit split, and fixed time")
	}
	corpus, corpusCases, packets, corpusSHA, packetsSHA, err := loadTemporalSpokenSafetyCorpus(config)
	if err != nil {
		return temporalSpokenSafetyLoaded{}, err
	}
	evidence, evidenceSHA, err := LoadTemporalTruthEvidence(config.EvidenceManifestPath)
	if err != nil {
		return temporalSpokenSafetyLoaded{}, err
	}
	privateMap, err := readStrictJSON[TemporalTruthEvidencePrivateMap](config.EvidencePrivateMapPath)
	if err != nil {
		return temporalSpokenSafetyLoaded{}, fmt.Errorf("read spoken-safety evidence map: %w", err)
	}
	if err := validateTemporalHumanEvidenceJoin(evidence, evidenceSHA, privateMap); err != nil {
		return temporalSpokenSafetyLoaded{}, err
	}
	privateMapSHA, err := hashFile(config.EvidencePrivateMapPath)
	if err != nil {
		return temporalSpokenSafetyLoaded{}, err
	}
	transcriptList, err := readStrictHistoryJSONL[fillerbakeoff.TranscriptArtifact](config.TranscriptSetPath)
	if err != nil {
		return temporalSpokenSafetyLoaded{}, fmt.Errorf("read spoken-safety transcripts: %w", err)
	}
	transcriptSetSHA := fillerbakeoff.TranscriptSetSHA256(transcriptList)
	if len(transcriptList) == 0 || !reviewSHA256(transcriptSetSHA) {
		return temporalSpokenSafetyLoaded{}, fmt.Errorf("spoken-safety transcript set identity is invalid")
	}
	transcripts := make(map[string]fillerbakeoff.TranscriptArtifact, len(transcriptList))
	var engine fillerbakeoff.TranscriptEngineIdentity
	for index, artifact := range transcriptList {
		if err := fillerbakeoff.ValidateTranscriptArtifact(artifact); err != nil {
			return temporalSpokenSafetyLoaded{}, fmt.Errorf("spoken-safety transcript %d: %w", index, err)
		}
		if _, duplicate := transcripts[artifact.CaseID]; duplicate {
			return temporalSpokenSafetyLoaded{}, fmt.Errorf("spoken-safety transcript set repeats a case")
		}
		if artifact.GeneratedAt.After(config.ProjectedAt) {
			return temporalSpokenSafetyLoaded{}, fmt.Errorf("spoken-safety transcript set postdates projection")
		}
		if index == 0 {
			engine = artifact.Engine
		} else if !reflect.DeepEqual(engine, artifact.Engine) {
			return temporalSpokenSafetyLoaded{}, fmt.Errorf("spoken-safety transcript set mixes engine identities")
		}
		transcripts[artifact.CaseID] = artifact
	}
	transcriptFileSHA, err := hashFile(config.TranscriptSetPath)
	if err != nil {
		return temporalSpokenSafetyLoaded{}, err
	}
	if err := validateTemporalSpokenSafetyCorpusTranscripts(config, corpusCases, packets, transcripts); err != nil {
		return temporalSpokenSafetyLoaded{}, err
	}
	if err := validateTemporalSpokenSafetyTranscriptJoins(evidence, privateMap, corpusCases, transcripts); err != nil {
		return temporalSpokenSafetyLoaded{}, err
	}
	_, structure, structureSHA, authoritySHA, err := LoadTemporalStructureChallenge(config.StructureManifestPath, config.StructureAuthorityPath, config.ExpectedStructureCases)
	if err != nil {
		return temporalSpokenSafetyLoaded{}, err
	}
	policy, err := readStrictJSON[TemporalSpokenSafetyPolicy](config.PolicyPath)
	if err != nil {
		return temporalSpokenSafetyLoaded{}, fmt.Errorf("read spoken-safety policy: %w", err)
	}
	if err := validateTemporalSpokenSafetyPolicy(policy); err != nil {
		return temporalSpokenSafetyLoaded{}, err
	}
	policySHA, err := hashFile(config.PolicyPath)
	if err != nil {
		return temporalSpokenSafetyLoaded{}, err
	}
	if config.ProjectedAt.Before(corpus.LockedAt) || config.ProjectedAt.Before(evidence.GeneratedAt) || config.ProjectedAt.Before(structure.GeneratedAt) || config.ProjectedAt.Before(policy.GeneratedAt) {
		return temporalSpokenSafetyLoaded{}, fmt.Errorf("spoken-safety projection predates its authority")
	}
	return temporalSpokenSafetyLoaded{
		corpus: corpus, corpusCases: corpusCases, packets: packets,
		evidence: evidence, privateMap: privateMap, transcripts: transcripts, structure: structure, policy: policy,
		corpusSHA: corpusSHA, packetsSHA: packetsSHA,
		evidenceSHA: evidenceSHA, privateMapSHA: privateMapSHA, transcriptSetSHA: transcriptSetSHA,
		transcriptFileSHA: transcriptFileSHA, structureSHA: structureSHA, authoritySHA: authoritySHA, policySHA: policySHA, engine: engine,
	}, nil
}
