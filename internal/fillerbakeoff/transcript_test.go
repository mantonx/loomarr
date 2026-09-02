package fillerbakeoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filleradmission"
	"github.com/loomarr/loomarr/internal/fillereval"
)

type transcriptFixture struct {
	identity TranscriptEngineIdentity
	segments []TranscriptSegment
	inputs   [][]byte
}

func (f *transcriptFixture) Identity() TranscriptEngineIdentity { return f.identity }

func (f *transcriptFixture) Transcribe(_ context.Context, audio []byte) ([]TranscriptSegment, error) {
	f.inputs = append(f.inputs, append([]byte(nil), audio...))
	return append([]TranscriptSegment(nil), f.segments...), nil
}

func TestBuildTranscriptsBindsVerifiedAudioAndCanonicalTimedText(t *testing.T) {
	root := t.TempDir()
	audio := []byte("audio fixture")
	if err := os.WriteFile(filepath.Join(root, "audio.wav"), audio, 0o600); err != nil {
		t.Fatal(err)
	}
	audioHash := sha256.Sum256(audio)
	packet := Packet{
		SchemaVersion: PacketSchemaVersion, CaseID: "case-1", EvidenceVersion: "evidence-1", ContentSHA256: strings.Repeat("a", 64),
		Signals: []Signal{{ID: "audio", Kind: string(filleradmission.KindAudio), Path: "audio.wav", SHA256: hex.EncodeToString(audioHash[:]), Bytes: int64(len(audio)), DurationMS: 4_000, ContentTypes: []string{"audio/wav"}}},
	}
	manifest := fillereval.Manifest{SchemaVersion: fillereval.SchemaVersion, Kind: fillereval.CorpusDevelopmentSeed, CorpusVersion: "fixture", Cases: []fillereval.Case{{
		ID: "case-1", Split: fillereval.SplitDevelopment, Cluster: "case-1", ContentSHA256: packet.ContentSHA256, EvidenceSHA256: PacketSHA256(packet), Source: "fixture", License: "CC0-1.0",
	}}}
	engine := &transcriptFixture{
		identity: TranscriptEngineIdentity{Provider: "whisper.cpp", ImplementationVersion: "v1.9.1", BinarySHA256: strings.Repeat("b", 64), Model: "ggml-small.en.bin", ModelSHA256: strings.Repeat("c", 64)},
		segments: []TranscriptSegment{
			{StartMS: 100, EndMS: 1_500, Text: " Buy now. "},
			{StartMS: 1_500, EndMS: 4_000, Text: "Call today."},
			{StartMS: 4_000, EndMS: 4_000, Text: "unsupported zero-width decoder output"},
			// Captured whisper.cpp output can append this non-speech sentinel beyond
			// the measured WAV duration; it is not semantic transcript evidence.
			{StartMS: 5_000, EndMS: 7_360, Text: "[BLANK_AUDIO]"},
		},
	}
	generatedAt := time.Date(2026, 8, 27, 18, 30, 0, 0, time.UTC)
	artifacts, err := BuildTranscripts(context.Background(), TranscriptConfig{
		Manifest: manifest, Packets: map[string]Packet{"case-1": packet}, CorpusRoot: root,
		EvaluationSplit: fillereval.SplitDevelopment, EvidenceVersion: "evidence-1", Engine: engine, GeneratedAt: generatedAt, PerCaseTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || len(engine.inputs) != 1 || string(engine.inputs[0]) != string(audio) {
		t.Fatalf("artifacts = %+v, inputs = %q", artifacts, engine.inputs)
	}
	got := artifacts[0]
	if got.SchemaVersion != TranscriptSchemaVersion || got.CaseID != "case-1" || got.PacketSHA256 != PacketSHA256(packet) || got.AudioSHA256 != hex.EncodeToString(audioHash[:]) || got.GeneratedAt != generatedAt {
		t.Fatalf("artifact identity = %+v", got)
	}
	if got.Text != "Buy now.\nCall today." || got.TextSHA256 != "2e56a42e5aadfd1ca5cff207d459fc705e84fc2943d953bf628921c6e69879e3" || len(got.Segments) != 2 {
		t.Fatalf("artifact transcript = %+v", got)
	}
}

func TestBuildTranscriptsRejectsChangedAudioBeforeSpeechEngine(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "audio.wav"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	packet := Packet{
		SchemaVersion: PacketSchemaVersion, CaseID: "case-1", EvidenceVersion: "evidence-1", ContentSHA256: strings.Repeat("a", 64),
		Signals: []Signal{{ID: "audio", Kind: string(filleradmission.KindAudio), Path: "audio.wav", SHA256: "6aa5eb98606e33141d843112db8f782839f1bf7ba00191965776c37f0b63fb96", Bytes: int64(len("audio fixture")), DurationMS: 4_000, ContentTypes: []string{"audio/wav"}}},
	}
	manifest := fillereval.Manifest{SchemaVersion: fillereval.SchemaVersion, Kind: fillereval.CorpusDevelopmentSeed, CorpusVersion: "fixture", Cases: []fillereval.Case{{
		ID: "case-1", Split: fillereval.SplitDevelopment, Cluster: "case-1", ContentSHA256: packet.ContentSHA256, EvidenceSHA256: PacketSHA256(packet), Source: "fixture", License: "CC0-1.0",
	}}}
	engine := &transcriptFixture{identity: TranscriptEngineIdentity{Provider: "whisper.cpp", ImplementationVersion: "v1.9.1", BinarySHA256: strings.Repeat("b", 64), Model: "ggml-small.en.bin", ModelSHA256: strings.Repeat("c", 64)}}
	_, err := BuildTranscripts(context.Background(), TranscriptConfig{Manifest: manifest, Packets: map[string]Packet{"case-1": packet}, CorpusRoot: root, EvaluationSplit: fillereval.SplitDevelopment, EvidenceVersion: "evidence-1", Engine: engine, GeneratedAt: time.Now(), PerCaseTimeout: time.Minute})
	if err == nil || !strings.Contains(err.Error(), "does not match packet") || len(engine.inputs) != 0 {
		t.Fatalf("err = %v, engine calls = %d", err, len(engine.inputs))
	}
}

func TestNormalizeTranscriptClampsFinalSpokenSegmentToMeasuredAudio(t *testing.T) {
	segments, text, err := normalizeTranscript([]TranscriptSegment{
		{StartMS: 53_360, EndMS: 60_400, Text: "See the new camera."},
		{StartMS: 60_400, EndMS: 65_600, Text: "Enjoy your pictures."},
	}, 64_017)
	if err != nil {
		t.Fatal(err)
	}
	if text != "See the new camera.\nEnjoy your pictures." || len(segments) != 2 || segments[1].EndMS != 64_017 {
		t.Fatalf("segments = %+v, text = %q", segments, text)
	}
}

func TestValidateTranscriptArtifactRejectsNonCanonicalOrUnboundEvidence(t *testing.T) {
	artifact := TranscriptArtifact{
		SchemaVersion: TranscriptSchemaVersion, CaseID: "case-1", PacketSHA256: strings.Repeat("a", 64),
		EvidenceVersion: "evidence-1", AudioSignalID: "audio", AudioSHA256: strings.Repeat("b", 64),
		AudioBytes: 100, AudioDurationMS: 2_000, GeneratedAt: time.Unix(1, 0).UTC(),
		Engine:   TranscriptEngineIdentity{Provider: "whisper.cpp", ImplementationVersion: "v1", BinarySHA256: strings.Repeat("c", 64), Model: "model.bin", ModelSHA256: strings.Repeat("d", 64)},
		Segments: []TranscriptSegment{{StartMS: 100, EndMS: 500, Text: "spoken text"}}, Text: "spoken text",
	}
	digest := sha256.Sum256([]byte(artifact.Text))
	artifact.TextSHA256 = hex.EncodeToString(digest[:])
	if err := ValidateTranscriptArtifact(artifact); err != nil {
		t.Fatal(err)
	}
	artifact.Segments[0].Text = " changed "
	if err := ValidateTranscriptArtifact(artifact); err == nil {
		t.Fatal("non-canonical transcript accepted")
	}
}

func TestBuildTranscriptsOmitsSelectedCaseWithoutCertifiedWAV(t *testing.T) {
	packet := Packet{
		SchemaVersion: PacketSchemaVersion, CaseID: "case-1", EvidenceVersion: "evidence-1", ContentSHA256: strings.Repeat("a", 64),
		Signals: []Signal{{ID: "filename", Kind: string(filleradmission.KindFilename), Text: "silent.mov"}},
	}
	manifest := fillereval.Manifest{Cases: []fillereval.Case{{
		ID: "case-1", Split: fillereval.SplitDevelopment, ContentSHA256: packet.ContentSHA256, EvidenceSHA256: PacketSHA256(packet),
	}}}
	engine := &transcriptFixture{identity: TranscriptEngineIdentity{Provider: "whisper.cpp", ImplementationVersion: "v1.9.2", BinarySHA256: strings.Repeat("b", 64), Model: "ggml-base.en.bin", ModelSHA256: strings.Repeat("c", 64)}}
	artifacts, err := BuildTranscripts(context.Background(), TranscriptConfig{
		Manifest: manifest, Packets: map[string]Packet{"case-1": packet}, CorpusRoot: t.TempDir(),
		EvaluationSplit: fillereval.SplitDevelopment, EvidenceVersion: "evidence-1", Engine: engine,
		GeneratedAt: time.Now(), PerCaseTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 0 || len(engine.inputs) != 0 {
		t.Fatalf("artifacts = %+v, engine calls = %d", artifacts, len(engine.inputs))
	}
}
