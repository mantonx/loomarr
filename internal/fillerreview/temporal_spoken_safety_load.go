package fillerreview

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/loomarr/loomarr/internal/filleradmission"
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
	if len(transcriptList) == 0 || fillerbakeoff.TranscriptSetSHA256(transcriptList) != privateMap.TranscriptSetSHA256 {
		return temporalSpokenSafetyLoaded{}, fmt.Errorf("spoken-safety transcripts do not bind the evidence authority")
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
	if err := validateTemporalSpokenSafetyTranscriptJoins(evidence, privateMap, corpusCases, packets, transcripts); err != nil {
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
		evidenceSHA: evidenceSHA, privateMapSHA: privateMapSHA, transcriptSetSHA: privateMap.TranscriptSetSHA256,
		transcriptFileSHA: transcriptFileSHA, structureSHA: structureSHA, authoritySHA: authoritySHA, policySHA: policySHA, engine: engine,
	}, nil
}

func loadTemporalSpokenSafetyCorpus(config TemporalSpokenSafetyConfig) (fillereval.Manifest, map[string]fillereval.Case, map[string]fillerbakeoff.Packet, string, string, error) {
	corpus, err := readStrictJSON[fillereval.Manifest](config.CorpusManifestPath)
	if err != nil {
		return fillereval.Manifest{}, nil, nil, "", "", fmt.Errorf("read spoken-safety corpus manifest: %w", err)
	}
	if corpus.SchemaVersion != fillereval.SchemaVersion || strings.TrimSpace(corpus.CorpusVersion) == "" {
		return fillereval.Manifest{}, nil, nil, "", "", fmt.Errorf("spoken-safety corpus manifest identity is invalid")
	}
	corpusCases := make(map[string]fillereval.Case, config.ExpectedCorpusCases)
	for _, item := range corpus.Cases {
		if item.Split != config.CorpusSplit {
			continue
		}
		if strings.TrimSpace(item.ID) == "" || !reviewSHA256(item.ContentSHA256) || !reviewSHA256(item.EvidenceSHA256) || item.Provenance.SegmentDurationMS <= 0 {
			return fillereval.Manifest{}, nil, nil, "", "", fmt.Errorf("spoken-safety corpus contains an invalid selected case")
		}
		if _, duplicate := corpusCases[item.ID]; duplicate {
			return fillereval.Manifest{}, nil, nil, "", "", fmt.Errorf("spoken-safety corpus repeats a selected case")
		}
		corpusCases[item.ID] = item
	}
	if len(corpusCases) != config.ExpectedCorpusCases {
		return fillereval.Manifest{}, nil, nil, "", "", fmt.Errorf("spoken-safety corpus has %d selected cases, expected %d", len(corpusCases), config.ExpectedCorpusCases)
	}
	packetList, err := readStrictHistoryJSONL[fillerbakeoff.Packet](config.PacketsPath)
	if err != nil {
		return fillereval.Manifest{}, nil, nil, "", "", fmt.Errorf("read spoken-safety packets: %w", err)
	}
	packets := make(map[string]fillerbakeoff.Packet, len(packetList))
	for _, packet := range packetList {
		item, exists := corpusCases[packet.CaseID]
		if !exists {
			return fillereval.Manifest{}, nil, nil, "", "", fmt.Errorf("spoken-safety packet set names a case outside the selected corpus")
		}
		if _, duplicate := packets[packet.CaseID]; duplicate {
			return fillereval.Manifest{}, nil, nil, "", "", fmt.Errorf("spoken-safety packet set repeats a case")
		}
		if err := fillerbakeoff.ValidatePacketAgainstCase(item, packet, config.EvidenceVersion, config.CorpusRoot); err != nil {
			return fillereval.Manifest{}, nil, nil, "", "", fmt.Errorf("validate spoken-safety packet: %w", err)
		}
		packets[packet.CaseID] = packet
	}
	if len(packets) != len(corpusCases) {
		return fillereval.Manifest{}, nil, nil, "", "", fmt.Errorf("spoken-safety packet set covers %d/%d selected corpus cases", len(packets), len(corpusCases))
	}
	corpusSHA, err := hashFile(config.CorpusManifestPath)
	if err != nil {
		return fillereval.Manifest{}, nil, nil, "", "", err
	}
	packetsSHA, err := hashFile(config.PacketsPath)
	if err != nil {
		return fillereval.Manifest{}, nil, nil, "", "", err
	}
	return corpus, corpusCases, packets, corpusSHA, packetsSHA, nil
}

func validateTemporalSpokenSafetyCorpusTranscripts(config TemporalSpokenSafetyConfig, cases map[string]fillereval.Case, packets map[string]fillerbakeoff.Packet, transcripts map[string]fillerbakeoff.TranscriptArtifact) error {
	covered := 0
	for caseID, item := range cases {
		packet := packets[caseID]
		_, audio, err := temporalSpokenSafetyPacketSignals(packet)
		if err != nil {
			return err
		}
		artifact, exists := transcripts[caseID]
		if audio == nil {
			if exists {
				return fmt.Errorf("spoken-safety transcript exists without certified corpus audio")
			}
			continue
		}
		covered++
		if !exists || artifact.PacketSHA256 != fillerbakeoff.PacketSHA256(packet) || artifact.EvidenceVersion != config.EvidenceVersion || artifact.AudioSignalID != audio.ID || artifact.AudioSHA256 != audio.SHA256 || artifact.AudioBytes != audio.Bytes || artifact.AudioDurationMS != audio.DurationMS || artifact.CaseID != item.ID {
			return fmt.Errorf("spoken-safety transcript does not bind its corpus packet audio")
		}
	}
	if len(transcripts) != covered {
		return fmt.Errorf("spoken-safety transcript set covers %d/%d certified corpus audio sources", len(transcripts), covered)
	}
	return nil
}

func temporalSpokenSafetyPacketSignals(packet fillerbakeoff.Packet) (*fillerbakeoff.Signal, *fillerbakeoff.Signal, error) {
	var videos, audios []fillerbakeoff.Signal
	for _, signal := range packet.Signals {
		switch filleradmission.EvidenceKind(signal.Kind) {
		case filleradmission.KindVideo:
			videos = append(videos, signal)
		case filleradmission.KindAudio:
			audios = append(audios, signal)
		}
	}
	if len(videos) > 1 || len(audios) > 1 || len(videos) == 0 && len(audios) != 0 {
		return nil, nil, fmt.Errorf("spoken-safety corpus source has ambiguous complete-source media")
	}
	if len(videos) == 0 {
		return nil, nil, nil
	}
	if len(audios) == 0 {
		return &videos[0], nil, nil
	}
	if !slices.Contains(audios[0].ContentTypes, "audio/wav") {
		return nil, nil, fmt.Errorf("spoken-safety corpus audio is not a certified WAV")
	}
	return &videos[0], &audios[0], nil
}

func validateTemporalSpokenSafetyTranscriptJoins(evidence TemporalTruthEvidenceManifest, privateMap TemporalTruthEvidencePrivateMap, corpusCases map[string]fillereval.Case, packets map[string]fillerbakeoff.Packet, transcripts map[string]fillerbakeoff.TranscriptArtifact) error {
	evidenceByAlias := make(map[string]TemporalTruthEvidenceCase, len(evidence.Cases))
	for _, item := range evidence.Cases {
		evidenceByAlias[item.Alias] = item
	}
	for _, mapping := range privateMap.Entries {
		item := evidenceByAlias[mapping.Alias]
		corpusCase, exists := corpusCases[mapping.CaseID]
		if !exists || corpusCase.ContentSHA256 != mapping.SourceSHA256 {
			return fmt.Errorf("spoken-safety review evidence does not bind the selected corpus")
		}
		if absoluteInt64(corpusCase.Provenance.SegmentDurationMS-item.DurationMS) > temporalSpokenSafetyDurationToleranceMS {
			return fmt.Errorf("spoken-safety review evidence duration differs from its selected corpus source")
		}
		artifact, exists := transcripts[mapping.CaseID]
		if mapping.TranscriptSHA256 == "" {
			if exists || len(item.TranscriptSegments) != 0 {
				return fmt.Errorf("spoken-safety no-audio source unexpectedly has transcript evidence")
			}
			continue
		}
		if !exists || temporalTruthJSONSHA(artifact) != mapping.TranscriptSHA256 || artifact.GeneratedAt.After(evidence.GeneratedAt) || absoluteInt64(artifact.AudioDurationMS-item.DurationMS) > temporalSpokenSafetyDurationToleranceMS {
			return fmt.Errorf("spoken-safety transcript does not bind its selected complete source")
		}
		segments, err := temporalTruthTranscriptSegments(artifact.Segments, item.DurationMS)
		if err != nil || !slices.Equal(segments, item.TranscriptSegments) {
			return fmt.Errorf("spoken-safety transcript differs from the evidence authority")
		}
	}
	return nil
}

func validateTemporalSpokenSafetyPolicy(policy TemporalSpokenSafetyPolicy) error {
	if policy.SchemaVersion != TemporalSpokenSafetyPolicySchemaVersion || policy.ContractVersion != TemporalSpokenSafetyPolicyContractVersion || !validTemporalSpokenSafetyPolicyID(policy.PolicyID) || policy.GeneratedAt.IsZero() || policy.MaximumInterSegmentGapMS < 0 || policy.MaximumInterSegmentGapMS > 5_000 || len(policy.Rules) == 0 || len(policy.Rules) > 256 {
		return fmt.Errorf("spoken-safety policy identity, timing, or rule count is invalid")
	}
	seenIDs := map[string]struct{}{}
	seenVariants := map[string]struct{}{}
	for ruleIndex, rule := range policy.Rules {
		if !validTemporalSpokenSafetyRuleID(rule.ID) || rule.Class != TemporalSpokenSafetyMatchProhibited && rule.Class != TemporalSpokenSafetyMatchAmbiguous || rule.MatchMode != TemporalSpokenSafetyModeExactWords && rule.MatchMode != TemporalSpokenSafetyModeTokenPrefix || len(rule.Variants) == 0 || len(rule.Variants) > 16 {
			return fmt.Errorf("spoken-safety policy rule %d is invalid", ruleIndex)
		}
		if _, duplicate := seenIDs[rule.ID]; duplicate {
			return fmt.Errorf("spoken-safety policy repeats a rule id")
		}
		seenIDs[rule.ID] = struct{}{}
		for variantIndex, variant := range rule.Variants {
			tokens := temporalSpokenSafetyWords(variant)
			if len(tokens) == 0 || len(tokens) > 12 || len([]rune(variant)) > 128 {
				return fmt.Errorf("spoken-safety policy rule %d variant %d is invalid", ruleIndex, variantIndex)
			}
			keyBytes, _ := json.Marshal(tokens)
			key := string(keyBytes)
			if _, duplicate := seenVariants[key]; duplicate {
				return fmt.Errorf("spoken-safety policy repeats a normalized variant")
			}
			seenVariants[key] = struct{}{}
		}
	}
	return nil
}

func validTemporalSpokenSafetyPolicyID(value string) bool {
	if len(value) < len("policy-a00") || len(value) > 71 || !strings.HasPrefix(value, "policy-") {
		return false
	}
	for _, r := range value[len("policy-"):] {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validTemporalSpokenSafetyRuleID(value string) bool {
	if len(value) != len("rule-")+24 || !strings.HasPrefix(value, "rule-") {
		return false
	}
	_, err := hex.DecodeString(value[len("rule-"):])
	return err == nil
}

func sortedTemporalSpokenSafetyStrings(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
