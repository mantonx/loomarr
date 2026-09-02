package fillerreview

import (
	"fmt"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/filleradmission"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillereval"
)

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

func validateTemporalSpokenSafetyTranscriptJoins(evidence TemporalTruthEvidenceManifest, privateMap TemporalTruthEvidencePrivateMap, corpusCases map[string]fillereval.Case, transcripts map[string]fillerbakeoff.TranscriptArtifact) error {
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
