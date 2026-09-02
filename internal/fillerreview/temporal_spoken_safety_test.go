package fillerreview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filleradmission"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillereval"
)

func TestPublishTemporalSpokenSafetyProjectsOpaqueMatchesAndFailsMissingCoverageClosed(t *testing.T) {
	fixture := newTemporalSpokenSafetyFixture(t)
	output := filepath.Join(t.TempDir(), "spoken-safety.json")
	report, digest, err := PublishTemporalSpokenSafety(fixture.config(output))
	if err != nil {
		t.Fatal(err)
	}
	if report.CorpusSources != 49 || report.Sources != 50 || report.CompleteTranscriptSources != 48 || report.ProhibitedSources != 2 || report.CoverageHoldSources != 3 || report.NoSignalObservedSources != 45 {
		t.Fatalf("source summary = %+v", report)
	}
	unreviewedProhibited := false
	for _, source := range report.SourceDispositions {
		unreviewedProhibited = unreviewedProhibited || source.Disposition == TemporalSpokenSafetyDispositionProhibited && source.EvidenceAlias == ""
	}
	if !unreviewedProhibited {
		t.Fatal("corpus source outside the review sample was not quarantined")
	}
	if report.StructureCases != 3 || report.ProhibitedCases != 2 || report.CoverageHoldCases != 1 || report.NoSignalObservedCases != 0 || report.ProductionAdmissionAllowed {
		t.Fatalf("case summary = %+v", report)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, privateText := range []string{fixture.prohibitedPhrase, fixture.ambiguousPhrase, "ordinary speech"} {
		if bytes.Contains(bytes.ToLower(raw), bytes.ToLower([]byte(privateText))) {
			t.Fatalf("report leaked private transcript/policy text")
		}
	}
	repeat := filepath.Join(t.TempDir(), "repeat.json")
	_, repeatedDigest, err := PublishTemporalSpokenSafety(fixture.config(repeat))
	if err != nil {
		t.Fatal(err)
	}
	if digest != repeatedDigest {
		t.Fatalf("deterministic digest = %s, want %s", repeatedDigest, digest)
	}
}

func TestPublishTemporalSpokenSafetyRejectsTranscriptAndPolicyDrift(t *testing.T) {
	t.Run("transcript", func(t *testing.T) {
		fixture := newTemporalSpokenSafetyFixture(t)
		artifacts, err := readStrictHistoryJSONL[fillerbakeoff.TranscriptArtifact](fixture.transcripts)
		if err != nil {
			t.Fatal(err)
		}
		artifacts[0].Segments[0].Text = "changed"
		writeTemporalSpokenSafetyJSONL(t, fixture.transcripts, artifacts)
		_, _, err = PublishTemporalSpokenSafety(fixture.config(filepath.Join(t.TempDir(), "report.json")))
		if err == nil {
			t.Fatalf("transcript drift error = %v", err)
		}
	})
	t.Run("policy", func(t *testing.T) {
		fixture := newTemporalSpokenSafetyFixture(t)
		policy := readStrictTestJSON[TemporalSpokenSafetyPolicy](t, fixture.policy)
		policy.Rules[1].Variants = append(policy.Rules[1].Variants, strings.ToUpper(fixture.prohibitedPhrase))
		writeTemporalSpokenSafetyJSON(t, fixture.policy, policy)
		_, _, err := PublishTemporalSpokenSafety(fixture.config(filepath.Join(t.TempDir(), "report.json")))
		if err == nil || !strings.Contains(err.Error(), "normalized variant") {
			t.Fatalf("policy drift error = %v", err)
		}
	})
}

func TestTemporalSpokenSafetyMatcherSpansOnlyBoundedAdjacentSegments(t *testing.T) {
	policy := TemporalSpokenSafetyPolicy{
		SchemaVersion: TemporalSpokenSafetyPolicySchemaVersion, ContractVersion: TemporalSpokenSafetyPolicyContractVersion,
		PolicyID: "policy-fixture", GeneratedAt: time.Unix(1, 0).UTC(), MaximumInterSegmentGapMS: 500,
		Rules: []TemporalSpokenSafetyPolicyRule{{ID: "rule-000102030405060708090a0b", Class: TemporalSpokenSafetyMatchProhibited, MatchMode: TemporalSpokenSafetyModeExactWords, Variants: []string{"restricted phrase"}}},
	}
	segments := []fillerbakeoff.TranscriptSegment{{StartMS: 100, EndMS: 300, Text: "Restricted"}, {StartMS: 700, EndMS: 900, Text: "phrase"}}
	if matches := matchTemporalSpokenSafety(policy, segments); len(matches) != 1 || matches[0].StartMS != 100 || matches[0].EndMS != 900 {
		t.Fatalf("bounded matches = %+v", matches)
	}
	segments[1].StartMS = 801
	if matches := matchTemporalSpokenSafety(policy, segments); len(matches) != 0 {
		t.Fatalf("over-gap matches = %+v", matches)
	}
}

func TestTemporalSpokenSafetyMatcherAppliesPrefixOnlyToFinalPolicyToken(t *testing.T) {
	policy := TemporalSpokenSafetyPolicy{
		SchemaVersion: TemporalSpokenSafetyPolicySchemaVersion, ContractVersion: TemporalSpokenSafetyPolicyContractVersion,
		PolicyID: "policy-fixture", GeneratedAt: time.Unix(1, 0).UTC(), MaximumInterSegmentGapMS: 500,
		Rules: []TemporalSpokenSafetyPolicyRule{{ID: "rule-000102030405060708090a0b", Class: TemporalSpokenSafetyMatchProhibited, MatchMode: TemporalSpokenSafetyModeTokenPrefix, Variants: []string{"restricted root"}}},
	}
	segments := []fillerbakeoff.TranscriptSegment{{StartMS: 100, EndMS: 900, Text: "restricted rooted"}}
	if matches := matchTemporalSpokenSafety(policy, segments); len(matches) != 1 {
		t.Fatalf("prefix matches = %+v", matches)
	}
	segments[0].Text = "unrestricted rooted"
	if matches := matchTemporalSpokenSafety(policy, segments); len(matches) != 0 {
		t.Fatalf("non-final prefix matches = %+v", matches)
	}
}

func TestTemporalSpokenSafetyPolicyIDAllowsVersionDigitsButRejectsUppercase(t *testing.T) {
	if !validTemporalSpokenSafetyPolicyID("policy-known-prohibited-spoken-v1") {
		t.Fatal("versioned policy id rejected")
	}
	if validTemporalSpokenSafetyPolicyID("policy-Known-prohibited-spoken-v1") {
		t.Fatal("uppercase policy id accepted")
	}
}

type temporalSpokenSafetyFixture struct {
	corpusManifest, packets, corpusRoot                                           string
	evidence, mapping, transcripts, structureManifest, structureAuthority, policy string
	projectedAt                                                                   time.Time
	prohibitedPhrase, ambiguousPhrase                                             string
}

func newTemporalSpokenSafetyFixture(t *testing.T) temporalSpokenSafetyFixture {
	t.Helper()
	root := t.TempDir()
	evidenceRoot := filepath.Join(root, "evidence")
	publicRoot, privateRoot := filepath.Join(evidenceRoot, "public"), filepath.Join(evidenceRoot, "private")
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	generatedAt := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)
	manifest := TemporalTruthEvidenceManifest{
		SchemaVersion: TemporalTruthEvidenceSchemaVersion, ContractVersion: TemporalTruthEvidenceContractVersion,
		EvidenceVersion: TemporalTruthEvidenceVersion, GeneratedAt: generatedAt, SelectionSHA256: strings.Repeat("1", 64),
		MediaTools: TemporalTruthMediaIdentity{
			FFmpeg:  TemporalTruthToolIdentity{Path: "/fixture/ffmpeg", Version: "fixture", BinarySHA256: strings.Repeat("2", 64)},
			FFprobe: TemporalTruthToolIdentity{Path: "/fixture/ffprobe", Version: "fixture", BinarySHA256: strings.Repeat("3", 64)},
		},
		OCR:    TemporalTruthOCRStatus{Status: "unavailable"},
		Config: TemporalTruthEvidenceSettings{SceneThreshold: 0.3, MaximumFramesPerCase: TemporalEvidenceMaxFrames, MaximumVideoBytes: TemporalTruthMaximumVideoBytes, PerCaseTimeoutMS: 60_000},
	}
	mapping := TemporalTruthEvidencePrivateMap{
		SchemaVersion: TemporalTruthEvidenceSchemaVersion, ContractVersion: TemporalTruthEvidenceContractVersion,
		SelectionSHA256: manifest.SelectionSHA256, DraftSHA256: strings.Repeat("4", 64), DownloadLedgerSHA256: strings.Repeat("5", 64), PacketsSHA256: strings.Repeat("6", 64),
	}
	engine := fillerbakeoff.TranscriptEngineIdentity{Provider: "whisper.cpp", ImplementationVersion: "fixture-v1", BinarySHA256: strings.Repeat("7", 64), Model: "model.bin", ModelSHA256: strings.Repeat("8", 64)}
	prohibitedPhrase, ambiguousPhrase := "restricted phrase", "review phrase"
	corpus := fillereval.Manifest{SchemaVersion: fillereval.SchemaVersion, Kind: fillereval.CorpusDevelopmentSeed, CorpusVersion: "spoken-safety-fixture-v1"}
	corpusRoot := filepath.Join(root, "derivatives")
	if err := os.MkdirAll(corpusRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	var packets []fillerbakeoff.Packet
	var artifacts []fillerbakeoff.TranscriptArtifact
	for index := 0; index < 48; index++ {
		alias, caseID := temporalSpokenSafetyFixtureID("evidence", index), temporalSpokenSafetyFixtureID("case", index)
		duration := int64(10_000 + index*100)
		if index == 1 {
			duration = 12_000
		}
		plan, err := BuildTemporalEvidencePlan(duration, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		caseRoot := filepath.Join(publicRoot, "cases", alias)
		if err := os.MkdirAll(caseRoot, 0o750); err != nil {
			t.Fatal(err)
		}
		videoBytes := []byte("review-video-" + caseID)
		videoPath := filepath.Join(caseRoot, "review.mp4")
		if err := os.WriteFile(videoPath, videoBytes, 0o640); err != nil {
			t.Fatal(err)
		}
		item := TemporalTruthEvidenceCase{
			Alias: alias, DurationMS: duration, Plan: plan,
			Video: TemporalTruthEvidenceFile{Path: temporalTruthRelative(publicRoot, videoPath), SHA256: hashBytes(videoBytes), Bytes: int64(len(videoBytes)), DurationMS: duration, Width: 640, Height: 360},
		}
		for frameIndex, at := range plan.FrameTimesMS {
			frameBytes := []byte("frame-" + caseID + "-" + string(rune('a'+frameIndex)))
			framePath := filepath.Join(caseRoot, "frame-"+temporalSpokenSafetyTwoDigits(frameIndex+1)+".jpg")
			if err := os.WriteFile(framePath, frameBytes, 0o640); err != nil {
				t.Fatal(err)
			}
			item.Frames = append(item.Frames, TemporalTruthEvidenceFrame{ID: "frame-" + temporalSpokenSafetyTwoDigits(frameIndex+1), Path: temporalTruthRelative(publicRoot, framePath), SHA256: hashBytes(frameBytes), Bytes: int64(len(frameBytes)), Width: 640, Height: 360, AtMS: at})
		}
		entry := TemporalTruthEvidencePrivateEntry{Alias: alias, CaseID: caseID, ContentSHA256: strings.Repeat(fmtHexDigit(index%15+1), 64), SourceSHA256: strings.Repeat(fmtHexDigit((index+1)%15+1), 64)}
		packetRoot := filepath.Join(corpusRoot, temporalSpokenSafetyFixtureID("packet", index))
		if err := os.MkdirAll(packetRoot, 0o750); err != nil {
			t.Fatal(err)
		}
		packetVideoPath := filepath.Join(packetRoot, "video.mp4")
		if err := os.WriteFile(packetVideoPath, videoBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		packet := fillerbakeoff.Packet{
			SchemaVersion: fillerbakeoff.PacketSchemaVersion, CaseID: caseID, EvidenceVersion: "fixture-evidence", ContentSHA256: entry.SourceSHA256,
			Signals: []fillerbakeoff.Signal{{
				ID: "video", Kind: string(filleradmission.KindVideo), Path: temporalTruthRelative(corpusRoot, packetVideoPath),
				SHA256: hashBytes(videoBytes), Bytes: int64(len(videoBytes)), DurationMS: duration, Width: 640, Height: 360, ContentTypes: []string{"video/mp4"},
			}},
		}
		if index != 47 {
			text := "ordinary speech"
			switch index {
			case 0:
				text = prohibitedPhrase
			case 1:
				text = ambiguousPhrase
			}
			segments := []fillerbakeoff.TranscriptSegment{{StartMS: 100, EndMS: 1_000, Text: text}}
			audioBytes := []byte("fixture-audio-" + caseID)
			audioPath := filepath.Join(packetRoot, "audio.wav")
			if err := os.WriteFile(audioPath, audioBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			audioSHA := hashBytes(audioBytes)
			packet.Signals = append(packet.Signals, fillerbakeoff.Signal{
				ID: "audio", Kind: string(filleradmission.KindAudio), Path: temporalTruthRelative(corpusRoot, audioPath),
				SHA256: audioSHA, Bytes: int64(len(audioBytes)), DurationMS: duration, ContentTypes: []string{"audio/wav"},
			})
			entry.PacketSHA256 = fillerbakeoff.PacketSHA256(packet)
			textDigest := sha256.Sum256([]byte(text))
			artifact := fillerbakeoff.TranscriptArtifact{
				SchemaVersion: fillerbakeoff.TranscriptSchemaVersion, CaseID: caseID, PacketSHA256: entry.PacketSHA256, EvidenceVersion: "fixture-evidence",
				AudioSignalID: "audio", AudioSHA256: audioSHA, AudioBytes: int64(len(audioBytes)), AudioDurationMS: duration,
				Engine: engine, GeneratedAt: generatedAt.Add(-time.Hour), LatencyMS: 100, Segments: segments, Text: text, TextSHA256: hex.EncodeToString(textDigest[:]),
			}
			entry.TranscriptSHA256 = temporalTruthJSONSHA(artifact)
			item.TranscriptSegments = segments
			artifacts = append(artifacts, artifact)
		} else {
			entry.PacketSHA256 = fillerbakeoff.PacketSHA256(packet)
		}
		corpus.Cases = append(corpus.Cases, fillereval.Case{
			ID: caseID, Split: fillereval.SplitDevelopment, ContentSHA256: entry.SourceSHA256,
			EvidenceSHA256: entry.PacketSHA256, Provenance: fillereval.MediaProvenance{SegmentDurationMS: duration},
		})
		packets = append(packets, packet)
		manifest.Cases = append(manifest.Cases, item)
		mapping.Entries = append(mapping.Entries, entry)
	}
	unreviewedCaseID := "case-unreviewed-prohibited"
	unreviewedDuration := int64(15_000)
	unreviewedRoot := filepath.Join(corpusRoot, "packet-unreviewed")
	if err := os.MkdirAll(unreviewedRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	unreviewedVideo, unreviewedAudio := []byte("unreviewed-video"), []byte("unreviewed-audio")
	unreviewedVideoPath, unreviewedAudioPath := filepath.Join(unreviewedRoot, "video.mp4"), filepath.Join(unreviewedRoot, "audio.wav")
	if err := os.WriteFile(unreviewedVideoPath, unreviewedVideo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unreviewedAudioPath, unreviewedAudio, 0o600); err != nil {
		t.Fatal(err)
	}
	unreviewedPacket := fillerbakeoff.Packet{
		SchemaVersion: fillerbakeoff.PacketSchemaVersion, CaseID: unreviewedCaseID, EvidenceVersion: "fixture-evidence", ContentSHA256: strings.Repeat("e", 64),
		Signals: []fillerbakeoff.Signal{
			{ID: "video", Kind: string(filleradmission.KindVideo), Path: temporalTruthRelative(corpusRoot, unreviewedVideoPath), SHA256: hashBytes(unreviewedVideo), Bytes: int64(len(unreviewedVideo)), DurationMS: unreviewedDuration, Width: 640, Height: 360, ContentTypes: []string{"video/mp4"}},
			{ID: "audio", Kind: string(filleradmission.KindAudio), Path: temporalTruthRelative(corpusRoot, unreviewedAudioPath), SHA256: hashBytes(unreviewedAudio), Bytes: int64(len(unreviewedAudio)), DurationMS: unreviewedDuration, ContentTypes: []string{"audio/wav"}},
		},
	}
	unreviewedPacketSHA := fillerbakeoff.PacketSHA256(unreviewedPacket)
	unreviewedTextSHA := sha256.Sum256([]byte(prohibitedPhrase))
	artifacts = append(artifacts, fillerbakeoff.TranscriptArtifact{
		SchemaVersion: fillerbakeoff.TranscriptSchemaVersion, CaseID: unreviewedCaseID, PacketSHA256: unreviewedPacketSHA, EvidenceVersion: "fixture-evidence",
		AudioSignalID: "audio", AudioSHA256: hashBytes(unreviewedAudio), AudioBytes: int64(len(unreviewedAudio)), AudioDurationMS: unreviewedDuration,
		Engine: engine, GeneratedAt: generatedAt.Add(-time.Hour), LatencyMS: 100,
		Segments: []fillerbakeoff.TranscriptSegment{{StartMS: 100, EndMS: 1_000, Text: prohibitedPhrase}}, Text: prohibitedPhrase, TextSHA256: hex.EncodeToString(unreviewedTextSHA[:]),
	})
	corpus.Cases = append(corpus.Cases, fillereval.Case{
		ID: unreviewedCaseID, Split: fillereval.SplitDevelopment, ContentSHA256: unreviewedPacket.ContentSHA256,
		EvidenceSHA256: unreviewedPacketSHA, Provenance: fillereval.MediaProvenance{SegmentDurationMS: unreviewedDuration},
	})
	packets = append(packets, unreviewedPacket)
	mapping.TranscriptSetSHA256 = fillerbakeoff.TranscriptSetSHA256(artifacts)
	corpusPath, packetsPath := filepath.Join(root, "draft.json"), filepath.Join(root, "packets.jsonl")
	writeTemporalSpokenSafetyJSON(t, corpusPath, corpus)
	writeTemporalSpokenSafetyPacketJSONL(t, packetsPath, packets)
	mapping.DraftSHA256, _ = hashFile(corpusPath)
	mapping.PacketsSHA256, _ = hashFile(packetsPath)
	evidencePath, mappingPath := filepath.Join(publicRoot, "manifest.json"), filepath.Join(privateRoot, "map.json")
	writeTemporalSpokenSafetyJSON(t, evidencePath, manifest)
	writeTemporalSpokenSafetyJSON(t, mappingPath, mapping)
	transcriptPath := filepath.Join(root, "transcripts.jsonl")
	writeTemporalSpokenSafetyJSONL(t, transcriptPath, artifacts)

	structureFixture := newTemporalStructureFixture(t)
	for index := 0; index < 2; index++ {
		source := &structureFixture.authoring.Sources[index]
		evidenceCase := manifest.Cases[index]
		source.SHA256, source.DurationMS, source.Provenance.Reference = evidenceCase.Video.SHA256, evidenceCase.DurationMS, mapping.Entries[index].CaseID
		if index == 0 {
			// Independent ffprobe passes can differ by a container tick even
			// when the exact video bytes are identical.
			source.DurationMS += 20
		}
		path := filepath.Join(structureFixture.root, "sources", source.Path)
		videoBytes, err := os.ReadFile(filepath.Join(publicRoot, filepath.FromSlash(evidenceCase.Video.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, videoBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		structureFixture.media.durationByPath[path] = source.DurationMS
	}
	structureFixture.authoring.Cases[0].Segments[0].DurationMS = structureFixture.authoring.Sources[0].DurationMS
	structureFixture.authoring.Cases[1].Segments[0].DurationMS = structureFixture.authoring.Sources[0].DurationMS
	structureFixture.authoring.Cases[1].Segments[1].DurationMS = structureFixture.authoring.Sources[1].DurationMS
	structureFixture.writeAuthoring(t)
	structureRoot, _ := structureFixture.build(t, "spoken-safety-seed")

	policyPath := filepath.Join(root, "policy.json")
	randomBytes := make([]byte, 24)
	for index := range randomBytes {
		randomBytes[index] = byte(index)
	}
	_, _, err := PublishTemporalSpokenSafetyPolicy(TemporalSpokenSafetyPolicyBuildConfig{
		PolicyID: "policy-fixture", GeneratedAt: generatedAt.Add(2 * time.Hour), MaximumInterSegmentGapMS: 500,
		ProhibitedPhrases: []string{prohibitedPhrase}, AmbiguousPhrases: []string{ambiguousPhrase}, Random: bytes.NewReader(randomBytes), OutputPath: policyPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	return temporalSpokenSafetyFixture{
		corpusManifest: corpusPath, packets: packetsPath, corpusRoot: corpusRoot,
		evidence: evidencePath, mapping: mappingPath, transcripts: transcriptPath,
		structureManifest: filepath.Join(structureRoot, "public", "manifest.json"), structureAuthority: filepath.Join(structureRoot, "private", "authority.json"),
		policy: policyPath, projectedAt: generatedAt.Add(3 * time.Hour), prohibitedPhrase: prohibitedPhrase, ambiguousPhrase: ambiguousPhrase,
	}
}

func (fixture temporalSpokenSafetyFixture) config(output string) TemporalSpokenSafetyConfig {
	return TemporalSpokenSafetyConfig{
		CorpusManifestPath: fixture.corpusManifest, PacketsPath: fixture.packets, CorpusRoot: fixture.corpusRoot,
		CorpusSplit: fillereval.SplitDevelopment, EvidenceVersion: "fixture-evidence", ExpectedCorpusCases: 49,
		EvidenceManifestPath: fixture.evidence, EvidencePrivateMapPath: fixture.mapping, TranscriptSetPath: fixture.transcripts,
		StructureManifestPath: fixture.structureManifest, StructureAuthorityPath: fixture.structureAuthority, ExpectedStructureCases: 3,
		PolicyPath: fixture.policy, ProjectedAt: fixture.projectedAt, OutputPath: output,
	}
}

func writeTemporalSpokenSafetyPacketJSONL(t *testing.T, path string, values []fillerbakeoff.Packet) {
	t.Helper()
	var raw []byte
	for _, value := range values {
		line, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, line...)
		raw = append(raw, '\n')
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func temporalSpokenSafetyFixtureID(prefix string, index int) string {
	return prefix + "-" + temporalSpokenSafetyTwoDigits(index)
}

func temporalSpokenSafetyTwoDigits(value int) string {
	return string([]byte{'0' + byte(value/10), '0' + byte(value%10)})
}

func fmtHexDigit(value int) string {
	const digits = "0123456789abcdef"
	return string(digits[value%len(digits)])
}

func writeTemporalSpokenSafetyJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTemporalSpokenSafetyJSONL(t *testing.T, path string, values []fillerbakeoff.TranscriptArtifact) {
	t.Helper()
	var raw []byte
	for _, value := range values {
		line, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, line...)
		raw = append(raw, '\n')
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
