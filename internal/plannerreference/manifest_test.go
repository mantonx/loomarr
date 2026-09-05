package plannerreference

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/quality"
)

func TestBuildManifestCanonicalizesAndBindsReferenceHostEvidence(t *testing.T) {
	card, captured, evidence, generatedAt := validFixture(t)
	artifact, err := BuildManifest(rawInputs(t, card, captured, evidence, generatedAt))
	if err != nil {
		t.Fatal(err)
	}
	again, err := BuildManifest(rawInputs(t, card, captured, evidence, generatedAt))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact.JSON, again.JSON) || artifact.SHA256 != again.SHA256 {
		t.Fatal("identical raw inputs did not produce an identical manifest")
	}
	want, err := os.ReadFile("testdata/planner-reference-host-v1.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact.JSON, want) {
		t.Fatalf("canonical manifest drifted\nwant:\n%s\ngot:\n%s", want, artifact.JSON)
	}
	const wantDigest = "46c78edcd8b08dfac708631561a4bc2f1f2b3ed526f334ecab4c1085aa5c16c4"
	if artifact.SHA256 != wantDigest {
		t.Fatalf("canonical manifest digest = %q, want %q", artifact.SHA256, wantDigest)
	}
	sum := sha256.Sum256(artifact.JSON)
	if artifact.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("artifact digest = %q, want SHA-256 of canonical JSON", artifact.SHA256)
	}
	if !bytes.HasSuffix(artifact.JSON, []byte("\n")) {
		t.Fatal("canonical manifest lacks terminal newline")
	}
	text := string(artifact.JSON)
	for _, want := range []string{
		`"contract": "planner-reference-host-v1"`,
		`"generatorProvider": "ollama"`,
		`"generatorModel": "hf.co/loomarr/gemma:Q4_K_M"`,
		`"physicalUnifiedMemoryBytes": 68719476736`,
		`"kind": "huggingface-model.json"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("manifest missing %s", want)
		}
	}
	if strings.Contains(text, "/Users/") || strings.Contains(text, "unrelated-resident-model") ||
		strings.Contains(text, "raw capture") {
		t.Fatal("manifest leaked raw capture content or local paths")
	}
}

func TestBuildManifestDistinguishesArchivedV11FromSnapshotV12(t *testing.T) {
	card, captured, evidence, generatedAt := validFixture(t)
	var document map[string]any
	if err := json.Unmarshal(card, &document); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{11, 12} {
		document["schemaVersion"] = version
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		_, err = BuildManifest(rawInputs(t, raw, captured, evidence, generatedAt))
		if version == 11 && err != nil {
			t.Fatalf("archived schema-v11 without snapshot: %v", err)
		}
		if version == 12 && (err == nil || !strings.Contains(err.Error(), "lacks its quality run snapshot")) {
			t.Fatalf("schema-v12 without snapshot error = %v", err)
		}
	}
}

func TestBuildManifestAcceptsAndBindsScorecardV12RunSnapshot(t *testing.T) {
	card, captured, evidence, generatedAt := validFixture(t)
	var document map[string]any
	if err := json.Unmarshal(card, &document); err != nil {
		t.Fatal(err)
	}
	snapshot := quality.RunSnapshot{
		SchemaVersion:  quality.RunSnapshotSchemaVersion,
		CorpusVersion:  "planner-certification-v3",
		RequestedModel: captured.Model.Tag, ResolvedModel: captured.Model.Tag,
		Provider: quality.ProviderOllama, BudgetProfile: captured.Protocol.Profile,
		ApplicationVersion: "v0.1.0", AccountingAvailable: true,
		CreatedAt: captured.StartedAt.Add(time.Minute),
	}
	snapshot.ID = quality.RunSnapshotID(snapshot)
	document["schemaVersion"] = float64(12)
	document["runSnapshot"] = snapshot
	card, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}

	artifact, err := BuildManifest(rawInputs(t, card, captured, evidence, generatedAt))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(artifact.JSON, []byte(`"schemaVersion": 12`)) {
		t.Fatalf("reference manifest did not retain scorecard schema 12: %s", artifact.JSON)
	}
}

func TestBuildManifestRejectsSemanticallyUnrelatedRawEvidenceEvenWhenRehashed(t *testing.T) {
	card, captured, evidence, generatedAt := validFixture(t)
	tests := map[string]struct {
		kind   string
		mutate func([]byte) []byte
		want   string
	}{
		"ollama inventory digest": {
			kind: "ollama-list.json",
			mutate: func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(strings.Repeat("a", 64)), []byte(strings.Repeat("9", 64)), 1)
			},
			want: "ollama-list.json",
		},
		"source revision": {
			kind: "huggingface-model.json",
			mutate: func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(strings.Repeat("b", 40)), []byte(strings.Repeat("8", 40)), 1)
			},
			want: "huggingface-model.json",
		},
		"local GGUF": {
			kind: "gguf-sha256.txt",
			mutate: func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(strings.Repeat("c", 64)), []byte(strings.Repeat("6", 64)), 1)
			},
			want: "gguf-sha256.txt",
		},
		"Ollama version": {
			kind:   "ollama-version.json",
			mutate: func(raw []byte) []byte { return bytes.Replace(raw, []byte("0.15.1"), []byte("0.14.0"), 1) },
			want:   "ollama-version.json",
		},
		"show request": {
			kind: "ollama-show-request.json",
			mutate: func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`hf.co/loomarr/gemma:Q4_K_M`), []byte(`other/model:Q4_K_M`), 1)
			},
			want: "ollama-show-request.json",
		},
		"preload context": {
			kind: "ollama-load-request.json",
			mutate: func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"num_ctx":8192`), []byte(`"num_ctx":4096`), 1)
			},
			want: "ollama-load-request.json",
		},
		"show template": {
			kind: "ollama-show.json",
			mutate: func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"template":"template"`), []byte(`"template":"other"`), 1)
			},
			want: "ollama-show.json",
		},
		"after residency": {
			kind: "ollama-ps-after.json",
			mutate: func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"size_vram":10737418240`), []byte(`"size_vram":9663676416`), 1)
			},
			want: "ollama-ps-after.json",
		},
		"after context": {
			kind: "ollama-ps-after.json",
			mutate: func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"context_length":8192`), []byte(`"context_length":4096`), 1)
			},
			want: "ollama-ps-after.json",
		},
		"cold selected model": {
			kind: "ollama-ps-cold-before.json",
			mutate: func([]byte) []byte {
				return []byte(`{"models":[{"name":"hf.co/loomarr/gemma:Q4_K_M","model":"hf.co/loomarr/gemma:Q4_K_M"}]}`)
			},
			want: "ollama-ps-cold-before.json",
		},
		"warm residency": {
			kind: "ollama-ps-warm-before.json",
			mutate: func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"size_vram":10737418240`), []byte(`"size_vram":9663676416`), 1)
			},
			want: "ollama-ps-warm-before.json",
		},
		"macOS build": {
			kind:   "sw-vers.txt",
			mutate: func(raw []byte) []byte { return bytes.Replace(raw, []byte("26A123"), []byte("26A999"), 1) },
			want:   "sw-vers.txt",
		},
		"host architecture": {
			kind:   "uname.txt",
			mutate: func([]byte) []byte { return []byte("x86_64\n") },
			want:   "uname.txt",
		},
		"host memory": {
			kind:   "sysctl-hw-memsize.txt",
			mutate: func([]byte) []byte { return []byte("34359738368\n") },
			want:   "sysctl-hw-memsize.txt",
		},
		"host chip": {
			kind:   "system-profiler.json",
			mutate: func(raw []byte) []byte { return bytes.Replace(raw, []byte("Apple M5 Pro"), []byte("Apple M4 Pro"), 1) },
			want:   "system-profiler.json",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			mutatedCapture := captured
			mutatedEvidence := cloneEvidence(evidence)
			mutatedEvidence[test.kind] = test.mutate(mutatedEvidence[test.kind])
			mutatedCapture.Evidence = evidenceReferences(mutatedEvidence)
			_, err := BuildManifest(rawInputs(t, card, mutatedCapture, mutatedEvidence, generatedAt))
			assertErrorContains(t, err, test.want)
		})
	}
}

func TestBuildManifestRejectsArtifactScorecardAndEvidenceDrift(t *testing.T) {
	card, captured, evidence, generatedAt := validFixture(t)

	t.Run("scorecard bytes", func(t *testing.T) {
		mutated := append(bytes.Clone(card), ' ')
		_, err := BuildManifest(RawInputs{
			Scorecard: mutated, Capture: encodeCapture(t, card, captured),
			Evidence: evidence, GeneratedAt: generatedAt,
		})
		assertErrorContains(t, err, "scorecard digest")
	})
	t.Run("scorecard model", func(t *testing.T) {
		mutated := bytes.Replace(card, []byte(`"hf.co/loomarr/gemma:Q4_K_M"`), []byte(`"other:model"`), 1)
		_, err := BuildManifest(rawInputs(t, mutated, captured, evidence, generatedAt))
		assertErrorContains(t, err, "scorecard profile or generator")
	})
	t.Run("scorecard profile", func(t *testing.T) {
		mutated := bytes.Replace(card, []byte(`"m5-pro-gemma"`), []byte(`"another-profile"`), 1)
		_, err := BuildManifest(rawInputs(t, mutated, captured, evidence, generatedAt))
		assertErrorContains(t, err, "scorecard profile or generator")
	})
	t.Run("raw evidence", func(t *testing.T) {
		mutated := cloneEvidence(evidence)
		mutated["ollama-show.json"] = []byte("changed")
		_, err := BuildManifest(rawInputs(t, card, captured, mutated, generatedAt))
		assertErrorContains(t, err, "digest or byte count")
	})
	t.Run("missing evidence", func(t *testing.T) {
		mutated := cloneEvidence(evidence)
		delete(mutated, "uname.txt")
		_, err := BuildManifest(rawInputs(t, card, captured, mutated, generatedAt))
		assertErrorContains(t, err, "incomplete")
	})
}

func TestBuildManifestRejectsMutableOrInconsistentModelAndHostEvidence(t *testing.T) {
	card, captured, evidence, generatedAt := validFixture(t)
	tests := map[string]struct {
		mutate func(*capture)
		want   string
	}{
		"mutable tag":       {func(c *capture) { c.Model.Tag = "gemma-latest" }, "explicit immutable tag"},
		"source revision":   {func(c *capture) { c.Model.SourceRevision = "main" }, "sourceRevision"},
		"gguf path":         {func(c *capture) { c.Model.GGUFFile = "../model.gguf" }, "bounded basename"},
		"quantization":      {func(c *capture) { c.Model.Quantization = "q4 maybe" }, "quantization"},
		"context mismatch":  {func(c *capture) { c.Protocol.ContextLength = 16384 }, "context/output"},
		"host architecture": {func(c *capture) { c.Runtime.Architecture = "amd64" }, "arm64"},
		"host memory":       {func(c *capture) { c.Runtime.PhysicalUnifiedMemoryBytes = 32 << 30 }, "64..512 GiB"},
		"cold resident":     {func(c *capture) { c.Residency.ColdBefore.SelectedModelResident = true }, "selected model was absent"},
		"warm absent":       {func(c *capture) { c.Residency.WarmBefore.SelectedModelResident = false }, "warmBefore"},
		"after absent":      {func(c *capture) { c.Residency.After.SelectedModelResident = false }, "after"},
		"cold starts":       {func(c *capture) { c.Protocol.ColdStarts = 0 }, "cold-start"},
		"warm-up loads":     {func(c *capture) { c.Protocol.WarmupLoads = 0 }, "warm-up load"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			mutated := captured
			test.mutate(&mutated)
			_, err := BuildManifest(rawInputs(t, card, mutated, evidence, generatedAt))
			assertErrorContains(t, err, test.want)
		})
	}
}

func TestBuildManifestRejectsMalformedAdversarialAndOverBoundInputs(t *testing.T) {
	card, captured, evidence, generatedAt := validFixture(t)
	validCapture := encodeCapture(t, card, captured)

	tests := map[string]struct {
		card    []byte
		capture []byte
		want    string
	}{
		"unknown capture field": {
			card: card, capture: bytes.Replace(validCapture, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1), want: "unknown field",
		},
		"duplicate capture field": {
			card: card, capture: bytes.Replace(validCapture, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"schemaVersion":1`), 1), want: "duplicate object key",
		},
		"trailing capture": {card: card, capture: append(bytes.Clone(validCapture), []byte(`{}`)...), want: "trailing JSON value"},
		"duplicate scorecard field": {
			card:    bytes.Replace(card, []byte(`"schemaVersion":10`), []byte(`"schemaVersion":10,"schemaVersion":10`), 1),
			capture: validCapture, want: "duplicate object key",
		},
		"trailing scorecard": {card: append(bytes.Clone(card), []byte(`{}`)...), capture: validCapture, want: "trailing JSON value"},
		"over-bound capture": {card: card, capture: bytes.Repeat([]byte("x"), maxCaptureBytes+1), want: "capture exceeds"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := BuildManifest(RawInputs{Scorecard: test.card, Capture: test.capture, Evidence: evidence, GeneratedAt: generatedAt})
			assertErrorContains(t, err, test.want)
		})
	}

	t.Run("duplicate raw evidence field", func(t *testing.T) {
		mutatedCapture := captured
		mutatedEvidence := cloneEvidence(evidence)
		mutatedEvidence["ollama-version.json"] = []byte(`{"version":"0.15.1","version":"0.15.1"}`)
		mutatedCapture.Evidence = evidenceReferences(mutatedEvidence)
		_, err := BuildManifest(rawInputs(t, card, mutatedCapture, mutatedEvidence, generatedAt))
		assertErrorContains(t, err, "duplicate object key")
	})
	t.Run("over-bound raw evidence", func(t *testing.T) {
		mutatedCapture := captured
		mutatedEvidence := cloneEvidence(evidence)
		mutatedEvidence["ollama-show.json"] = bytes.Repeat([]byte("x"), maxEvidenceBytes+1)
		mutatedCapture.Evidence = evidenceReferences(mutatedEvidence)
		_, err := BuildManifest(rawInputs(t, card, mutatedCapture, mutatedEvidence, generatedAt))
		assertErrorContains(t, err, "evidence ollama-show.json exceeds")
	})
}

func validFixture(t *testing.T) ([]byte, capture, map[string][]byte, time.Time) {
	t.Helper()
	const (
		modelTag = "hf.co/loomarr/gemma:Q4_K_M"
		ram      = int64(2 << 30)
		vram     = int64(10 << 30)
	)
	card := []byte(`{"schemaVersion":10,"corpusVersion":"planner-certification-v3","profile":"m5-pro-gemma","generator":{"provider":"ollama","model":"` + modelTag + `"},"contract":{"corpusVersion":"planner-certification-v3","catalogFixtureSha256":"` + strings.Repeat("1", 64) + `","promptVersion":"planner-prompt-v1","toolSchemaVersion":"planner-tools-v1","scorerVersion":"planner-scorer-v3"},"assessment":{"performance":{"resourceStatus":"measured","resourceSource":"ollama:/api/ps","peakRamBytes":2147483648,"peakVramBytes":10737418240}},"cases":[{"case":"one","trials":3},{"case":"two","trials":3}],"certified":false}`)
	captured := capture{
		SchemaVersion: 1, Contract: contractVersion, RunID: "m5-pro-gemma-q4",
		StartedAt:   time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC),
		Model: modelCapture{
			Tag: modelTag, OllamaDigest: strings.Repeat("a", 64),
			SourceRepository: "loomarr/gemma-gguf", SourceRevision: strings.Repeat("b", 40),
			GGUFFile: "gemma-Q4_K_M.gguf", GGUFSHA256: strings.Repeat("c", 64),
			Quantization: "Q4_K_M", ContextLength: 8192, TemplateSHA256: sha256Text("template"),
			ModelfileSHA256: sha256Text("FROM /Users/test/sha256-" + strings.Repeat("c", 64) + "\nPARAMETER num_ctx 8192\n"),
			LicenseID:       "Gemma", LicenseSHA256: sha256Text("Gemma"),
		},
		Runtime: runtimeCapture{
			OllamaVersion: "0.15.1", MacOSVersion: "27.0", MacOSBuild: "26A123",
			Architecture: "arm64", HardwareModel: "Macmini11,1", Chip: "Apple M5 Pro",
			PhysicalUnifiedMemoryBytes: 64 << 30,
		},
		Protocol: protocolCapture{
			Profile: "m5-pro-gemma", ContextLength: 8192, MaxOutputTokens: 2048,
			Temperature: 0.2, ColdStarts: 1, WarmupLoads: 1, MeasuredWarmTrials: 3,
		},
		Residency: residencyCapture{
			ColdBefore: selectedResidency{},
			WarmBefore: selectedResidency{SelectedModelResident: true, Model: modelTag, OllamaDigest: strings.Repeat("a", 64), RAMBytes: ram, VRAMBytes: vram},
			After:      selectedResidency{SelectedModelResident: true, Model: modelTag, OllamaDigest: strings.Repeat("a", 64), RAMBytes: ram, VRAMBytes: vram},
		},
	}
	evidence := validEvidence(t, captured)
	captured.Evidence = evidenceReferences(evidence)
	return card, captured, evidence, time.Date(2026, 10, 15, 15, 1, 0, 0, time.UTC)
}

func validEvidence(t *testing.T, captured capture) map[string][]byte {
	t.Helper()
	model := captured.Model
	return map[string][]byte{
		"huggingface-model.json":     []byte(`{"id":"` + model.SourceRepository + `","sha":"` + model.SourceRevision + `","cardData":{"license":"` + model.LicenseID + `"},"siblings":[{"rfilename":"` + model.GGUFFile + `","lfs":{"sha256":"` + model.GGUFSHA256 + `"}}]}`),
		"gguf-sha256.txt":            []byte(model.GGUFSHA256 + `  /Users/test/` + model.GGUFFile + "\n"),
		"ollama-version.json":        []byte(`{"version":"` + captured.Runtime.OllamaVersion + `"}`),
		"ollama-list.json":           []byte(`{"models":[{"name":"` + model.Tag + `","model":"` + model.Tag + `","digest":"` + model.OllamaDigest + `","details":{"quantization_level":"` + model.Quantization + `"}}]}`),
		"ollama-load-request.json":   []byte(`{"model":"` + model.Tag + `","prompt":"","stream":false,"keep_alive":"30m","options":{"num_ctx":8192}}`),
		"ollama-show.json":           []byte(`{"license":"Gemma","modelfile":"FROM /Users/test/sha256-` + model.GGUFSHA256 + `\nPARAMETER num_ctx 8192\n","template":"template","details":{"quantization_level":"Q4_K_M"}}`),
		"ollama-show-request.json":   []byte(`{"model":"` + model.Tag + `"}`),
		"ollama-ps-cold-before.json": []byte(`{"models":[{"name":"unrelated-resident-model:Q4_K_M","model":"unrelated-resident-model:Q4_K_M","digest":"` + strings.Repeat("7", 64) + `","size":1024,"size_vram":1024,"context_length":8192}]}`),
		"ollama-ps-warm-before.json": []byte(`{"models":[{"name":"` + model.Tag + `","model":"` + model.Tag + `","digest":"` + model.OllamaDigest + `","size":12884901888,"size_vram":10737418240,"context_length":8192}]}`),
		"ollama-ps-after.json":       []byte(`{"models":[{"name":"` + model.Tag + `","model":"` + model.Tag + `","digest":"` + model.OllamaDigest + `","size":12884901888,"size_vram":10737418240,"context_length":8192}]}`),
		"sw-vers.txt":                []byte("ProductName:\t\tmacOS\nProductVersion:\t\t27.0\nBuildVersion:\t\t26A123\n"),
		"system-profiler.json":       []byte(`{"SPHardwareDataType":[{"machine_model":"Macmini11,1","chip_type":"Apple M5 Pro"}]}`),
		"sysctl-hw-memsize.txt":      []byte("68719476736\n"),
		"uname.txt":                  []byte("arm64\n"),
	}
}

func evidenceReferences(evidence map[string][]byte) []evidenceReference {
	declared := make([]evidenceReference, 0, len(evidence))
	for i := len(requiredEvidenceKinds) - 1; i >= 0; i-- {
		kind := requiredEvidenceKinds[i]
		raw := evidence[kind]
		sum := sha256.Sum256(raw)
		declared = append(declared, evidenceReference{Kind: kind, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(raw))})
	}
	return declared
}

func sha256Text(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func rawInputs(t *testing.T, card []byte, captured capture, evidence map[string][]byte, generatedAt time.Time) RawInputs {
	t.Helper()
	return RawInputs{Scorecard: card, Capture: encodeCapture(t, card, captured), Evidence: evidence, GeneratedAt: generatedAt}
}

func encodeCapture(t *testing.T, card []byte, captured capture) []byte {
	t.Helper()
	sum := sha256.Sum256(card)
	captured.ScorecardSHA256 = hex.EncodeToString(sum[:])
	captured.ScorecardBytes = int64(len(card))
	raw, err := json.Marshal(captured)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func cloneEvidence(source map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(source))
	for key, value := range source {
		out[key] = bytes.Clone(value)
	}
	return out
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}
