package fillerbakeoff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/filleradmission"
	"github.com/loomarr/loomarr/internal/fillereval"
)

const (
	TranscriptSchemaVersion = 1
	maxTranscriptSegments   = 4096
	maxTranscriptTextBytes  = 1 << 20
	maxTranscriptTailMS     = 30_000
)

type TranscriptEngineIdentity struct {
	Provider              string `json:"provider"`
	ImplementationVersion string `json:"implementationVersion"`
	BinarySHA256          string `json:"binarySha256"`
	Model                 string `json:"model"`
	ModelSHA256           string `json:"modelSha256"`
}

type TranscriptSegment struct {
	StartMS int64  `json:"startMs"`
	EndMS   int64  `json:"endMs"`
	Text    string `json:"text"`
}

type TranscriptArtifact struct {
	SchemaVersion int `json:"schemaVersion"`

	CaseID          string `json:"caseId"`
	PacketSHA256    string `json:"packetSha256"`
	EvidenceVersion string `json:"evidenceVersion"`
	AudioSignalID   string `json:"audioSignalId"`
	AudioSHA256     string `json:"audioSha256"`
	AudioBytes      int64  `json:"audioBytes"`
	AudioDurationMS int64  `json:"audioDurationMs"`

	Engine      TranscriptEngineIdentity `json:"engine"`
	GeneratedAt time.Time                `json:"generatedAt"`
	LatencyMS   int64                    `json:"latencyMs"`
	Segments    []TranscriptSegment      `json:"segments"`
	Text        string                   `json:"text"`
	TextSHA256  string                   `json:"textSha256"`
}

type TranscriptEngine interface {
	Identity() TranscriptEngineIdentity
	Transcribe(context.Context, []byte) ([]TranscriptSegment, error)
}

type TranscriptConfig struct {
	Manifest        fillereval.Manifest
	Packets         map[string]Packet
	CorpusRoot      string
	EvaluationSplit fillereval.Split
	EvidenceVersion string
	Engine          TranscriptEngine
	GeneratedAt     time.Time
	PerCaseTimeout  time.Duration
}

// ValidateTranscriptArtifact verifies the self-contained authority of one
// complete packet-audio transcript. Joins to a corpus packet remain the
// caller's responsibility; this check is reusable by later safety evaluators
// that consume the immutable artifact without rebuilding it.
func ValidateTranscriptArtifact(artifact TranscriptArtifact) error {
	if artifact.SchemaVersion != TranscriptSchemaVersion || strings.TrimSpace(artifact.CaseID) == "" || !validSHA256(artifact.PacketSHA256) || strings.TrimSpace(artifact.EvidenceVersion) == "" || strings.TrimSpace(artifact.AudioSignalID) == "" || !validSHA256(artifact.AudioSHA256) || artifact.AudioBytes <= 0 || artifact.AudioDurationMS <= 0 || artifact.GeneratedAt.IsZero() || artifact.LatencyMS < 0 {
		return fmt.Errorf("transcript artifact has invalid schema, source, audio, time, or latency authority")
	}
	if strings.TrimSpace(artifact.Engine.Provider) == "" || strings.TrimSpace(artifact.Engine.ImplementationVersion) == "" || strings.TrimSpace(artifact.Engine.Model) == "" || !validSHA256(artifact.Engine.BinarySHA256) || !validSHA256(artifact.Engine.ModelSHA256) {
		return fmt.Errorf("transcript artifact has incomplete speech engine identity")
	}
	normalized, text, err := normalizeTranscript(artifact.Segments, artifact.AudioDurationMS)
	if err != nil || !slices.Equal(normalized, artifact.Segments) || text != artifact.Text {
		return fmt.Errorf("transcript artifact is not canonical")
	}
	textHash := sha256.Sum256([]byte(artifact.Text))
	if artifact.TextSHA256 != hex.EncodeToString(textHash[:]) {
		return fmt.Errorf("transcript artifact text digest does not match")
	}
	return nil
}

// BuildTranscripts produces one complete, sorted, content-bound transcript set.
// It does not mutate raw packets or call a classifier.
func BuildTranscripts(ctx context.Context, config TranscriptConfig) ([]TranscriptArtifact, error) {
	if config.Engine == nil || strings.TrimSpace(config.CorpusRoot) == "" || strings.TrimSpace(config.EvidenceVersion) == "" || config.GeneratedAt.IsZero() || config.PerCaseTimeout <= 0 {
		return nil, fmt.Errorf("transcript build requires an engine, corpus root, generation time, and positive per-case timeout")
	}
	if config.EvaluationSplit != fillereval.SplitDevelopment && config.EvaluationSplit != fillereval.SplitHoldout {
		return nil, fmt.Errorf("transcript build requires an explicit development or holdout split")
	}
	identity := config.Engine.Identity()
	if strings.TrimSpace(identity.Provider) == "" || strings.TrimSpace(identity.ImplementationVersion) == "" || strings.TrimSpace(identity.Model) == "" || !validSHA256(identity.BinarySHA256) || !validSHA256(identity.ModelSHA256) {
		return nil, fmt.Errorf("transcript engine requires exact provider, implementation, binary, and model identity")
	}
	selected := make(map[string]fillereval.Case)
	for _, c := range config.Manifest.Cases {
		if c.Split == config.EvaluationSplit {
			selected[c.ID] = c
		}
	}
	if len(selected) == 0 || len(config.Packets) != len(selected) {
		return nil, fmt.Errorf("transcript packet set has %d cases, selected split requires %d", len(config.Packets), len(selected))
	}
	caseIDs := make([]string, 0, len(selected))
	for id := range selected {
		caseIDs = append(caseIDs, id)
	}
	slices.Sort(caseIDs)
	artifacts := make([]TranscriptArtifact, 0, len(caseIDs))
	for _, id := range caseIDs {
		packet, ok := config.Packets[id]
		if !ok {
			return nil, fmt.Errorf("case %q has no transcript packet", id)
		}
		if err := ValidatePacketAgainstCase(selected[id], packet, config.EvidenceVersion, config.CorpusRoot); err != nil {
			return nil, err
		}
		audio, hasAudio, err := certifiedAudioSignal(packet)
		if err != nil {
			return nil, err
		}
		if !hasAudio {
			continue
		}
		data, err := readSignalFile(packet.CaseID, audio, config.CorpusRoot, true)
		if err != nil {
			return nil, err
		}
		caseCtx, cancel := context.WithTimeout(ctx, config.PerCaseTimeout)
		started := time.Now()
		segments, transcribeErr := config.Engine.Transcribe(caseCtx, data)
		latency := time.Since(started)
		cancel()
		if transcribeErr != nil {
			return nil, fmt.Errorf("case %q transcript: %w", id, transcribeErr)
		}
		segments, text, err := normalizeTranscript(segments, audio.DurationMS)
		if err != nil {
			return nil, fmt.Errorf("case %q transcript: %w", id, err)
		}
		textHash := sha256.Sum256([]byte(text))
		artifacts = append(artifacts, TranscriptArtifact{
			SchemaVersion: TranscriptSchemaVersion, CaseID: id, PacketSHA256: PacketSHA256(packet), EvidenceVersion: packet.EvidenceVersion,
			AudioSignalID: audio.ID, AudioSHA256: audio.SHA256, AudioBytes: audio.Bytes, AudioDurationMS: audio.DurationMS,
			Engine: identity, GeneratedAt: config.GeneratedAt.UTC(), LatencyMS: max(int64(0), latency.Milliseconds()),
			Segments: segments, Text: text, TextSHA256: hex.EncodeToString(textHash[:]),
		})
	}
	return artifacts, nil
}

// TranscriptSetSHA256 is the exact identity a run carries when it consumes a
// shared transcript set.
func TranscriptSetSHA256(artifacts []TranscriptArtifact) string {
	ordered := slices.Clone(artifacts)
	slices.SortFunc(ordered, func(a, b TranscriptArtifact) int { return strings.Compare(a.CaseID, b.CaseID) })
	data, err := json.Marshal(ordered)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validateTranscriptSet(config Config, selected map[string]fillereval.Case) (map[string]TranscriptArtifact, error) {
	if len(config.Transcripts) == 0 {
		if config.Run.TranscriptSetSHA256 != "" {
			return nil, fmt.Errorf("run names a transcript set but supplies no transcript artifacts")
		}
		return nil, nil
	}
	if !validSHA256(config.Run.TranscriptSetSHA256) || TranscriptSetSHA256(config.Transcripts) != config.Run.TranscriptSetSHA256 {
		return nil, fmt.Errorf("shared transcript set does not match the run identity")
	}
	expected := 0
	for id := range selected {
		_, hasAudio, err := certifiedAudioSignal(config.Packets[id])
		if err != nil {
			return nil, err
		}
		if hasAudio {
			expected++
		}
	}
	if len(config.Transcripts) != expected {
		return nil, fmt.Errorf("shared transcript set has %d cases, selected split has %d certified WAV packets", len(config.Transcripts), expected)
	}
	indexed := make(map[string]TranscriptArtifact, len(config.Transcripts))
	var commonIdentity TranscriptEngineIdentity
	for i, artifact := range config.Transcripts {
		packet, exists := config.Packets[artifact.CaseID]
		if !exists {
			return nil, fmt.Errorf("transcript case %q has no selected raw packet", artifact.CaseID)
		}
		if _, duplicate := indexed[artifact.CaseID]; duplicate {
			return nil, fmt.Errorf("duplicate shared transcript for case %q", artifact.CaseID)
		}
		if slices.ContainsFunc(packet.Signals, func(signal Signal) bool { return signal.ID == "shared-transcript" }) {
			return nil, fmt.Errorf("raw packet %q already contains the derived shared transcript", artifact.CaseID)
		}
		if err := ValidateTranscriptArtifact(artifact); err != nil {
			return nil, fmt.Errorf("transcript case %q: %w", artifact.CaseID, err)
		}
		if artifact.PacketSHA256 != PacketSHA256(packet) || artifact.EvidenceVersion != config.Run.EvidenceVersion || artifact.GeneratedAt.After(config.Run.GeneratedAt) {
			return nil, fmt.Errorf("transcript case %q has invalid schema, packet, evidence, time, or latency binding", artifact.CaseID)
		}
		audio, hasAudio, err := certifiedAudioSignal(packet)
		if err != nil {
			return nil, err
		}
		if !hasAudio {
			return nil, fmt.Errorf("transcript case %q has no certified WAV", artifact.CaseID)
		}
		if artifact.AudioSignalID != audio.ID || artifact.AudioSHA256 != audio.SHA256 || artifact.AudioBytes != audio.Bytes || artifact.AudioDurationMS != audio.DurationMS {
			return nil, fmt.Errorf("transcript case %q does not bind its packet WAV", artifact.CaseID)
		}
		identityData, _ := json.Marshal(artifact.Engine)
		if i == 0 {
			commonIdentity = artifact.Engine
		} else {
			commonData, _ := json.Marshal(commonIdentity)
			if !bytes.Equal(identityData, commonData) {
				return nil, fmt.Errorf("shared transcript set mixes speech engine identities")
			}
		}
		indexed[artifact.CaseID] = artifact
	}
	return indexed, nil
}

func withSharedTranscript(packet Packet, artifact TranscriptArtifact) Packet {
	if artifact.Text == "" {
		return packet
	}
	augmented := packet
	augmented.Signals = slices.Clone(packet.Signals)
	augmented.Signals = append(augmented.Signals, Signal{
		ID: "shared-transcript", Kind: string(filleradmission.KindTranscript), Text: artifact.Text,
	})
	return augmented
}

func certifiedAudioSignal(packet Packet) (Signal, bool, error) {
	var found []Signal
	for _, signal := range packet.Signals {
		if filleradmission.EvidenceKind(signal.Kind) == filleradmission.KindAudio {
			found = append(found, signal)
		}
	}
	if len(found) == 0 {
		return Signal{}, false, nil
	}
	if len(found) != 1 || !slices.Contains(found[0].ContentTypes, "audio/wav") {
		return Signal{}, false, fmt.Errorf("case %q has an ambiguous or uncertified audio signal", packet.CaseID)
	}
	return found[0], true, nil
}

func normalizeTranscript(input []TranscriptSegment, durationMS int64) ([]TranscriptSegment, string, error) {
	if len(input) > maxTranscriptSegments || durationMS <= 0 {
		return nil, "", fmt.Errorf("transcript exceeds segment count or duration bounds")
	}
	segments := make([]TranscriptSegment, 0, len(input))
	textParts := make([]string, 0, len(input))
	var previousEnd int64
	var textBytes int
	lastSpoken := -1
	for i, segment := range input {
		text := strings.TrimSpace(segment.Text)
		if text != "" && !strings.EqualFold(text, "[BLANK_AUDIO]") && segment.EndMS != segment.StartMS {
			lastSpoken = i
		}
	}
	for i, segment := range input {
		segment.Text = strings.TrimSpace(segment.Text)
		if segment.Text == "" || strings.EqualFold(segment.Text, "[BLANK_AUDIO]") || segment.EndMS == segment.StartMS {
			continue
		}
		if segment.EndMS > durationMS {
			// whisper.cpp timestamps whole decoding windows and can extend the
			// final spoken window beyond the measured WAV tail. Only that final
			// overlapping window is clipped; wholly out-of-range or extreme
			// timestamps remain invalid.
			if i != lastSpoken || segment.StartMS >= durationMS || segment.EndMS > durationMS+maxTranscriptTailMS {
				return nil, "", fmt.Errorf("transcript contains invalid or unordered timing")
			}
			segment.EndMS = durationMS
		}
		if segment.StartMS < previousEnd || segment.StartMS < 0 || segment.EndMS <= segment.StartMS {
			return nil, "", fmt.Errorf("transcript contains invalid or unordered timing")
		}
		if len(segment.Text) > maxTranscriptTextBytes-textBytes {
			return nil, "", fmt.Errorf("transcript exceeds text byte ceiling")
		}
		textBytes += len(segment.Text)
		previousEnd = segment.EndMS
		segments = append(segments, segment)
		textParts = append(textParts, segment.Text)
	}
	return segments, strings.Join(textParts, "\n"), nil
}
