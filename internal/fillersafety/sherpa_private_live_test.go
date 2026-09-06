//go:build !windows

package fillersafety

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateLiveSherpaAdapter(t *testing.T) {
	root := os.Getenv("LOOMARR_PRIVATE_SHERPA_ROOT")
	model := os.Getenv("LOOMARR_PRIVATE_SHERPA_MODEL")
	if root == "" || model == "" {
		t.Skip("private sherpa artifacts are not configured")
	}
	contract, err := knownSherpaArtifactContract()
	if err != nil {
		t.Fatal(err)
	}
	tokens := firstPrivateKeywordTokens(t, filepath.Join(model, "test_wavs", "test_keywords.txt"))
	authority := acousticKeywordAuthority{
		SchemaVersion: acousticKeywordAuthoritySchemaVersion, ContractVersion: acousticKeywordAuthorityContractVersion,
		PolicySHA256: strings.Repeat("9", 64), ModelSHA256: sherpaModelIdentitySHA256(contract), BPEModelSHA256: contract.bpeModelSHA256,
		Rules: []acousticKeywordRule{{ID: fakeSherpaRuleID, Variants: [][]string{tokens}}},
	}
	authorityPath := filepath.Join(t.TempDir(), "authority.json")
	raw, err := json.Marshal(authority)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authorityPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	proposer, err := newSherpaProposerWithContract(context.Background(), sherpaProposerConfig{
		RuntimePath: filepath.Join(root, "bin", "sherpa-onnx-keyword-spotter"), RuntimeLibraryPath: filepath.Join(root, "lib", "libonnxruntime.dylib"),
		EncoderPath: filepath.Join(model, "encoder-epoch-12-avg-2-chunk-16-left-64.int8.onnx"), DecoderPath: filepath.Join(model, "decoder-epoch-12-avg-2-chunk-16-left-64.onnx"),
		JoinerPath: filepath.Join(model, "joiner-epoch-12-avg-2-chunk-16-left-64.int8.onnx"), TokensPath: filepath.Join(model, "tokens.txt"),
		KeywordAuthorityPath: authorityPath, FFmpegPath: ffmpeg,
	}, contract)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proposer.Close() }()
	source := filepath.Join(model, "test_wavs", "0.wav")
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	output, err := proposer.Propose(context.Background(), proposalRequest{
		AuthoritySHA256: strings.Repeat("8", 64), PolicySHA256: authority.PolicySHA256,
		SourceSHA256: fileSHA256(source), SourceBytes: info.Size(), SourcePath: source, DurationMS: 60_000,
		FFmpeg: ToolIdentity{Version: "private-live", BinarySHA256: fileSHA256(ffmpeg)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !output.Complete || len(output.Candidates) == 0 {
		t.Fatalf("real adapter completed without the model's known test hit")
	}
}

func firstPrivateKeywordTokens(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatal("private test keyword authority is empty")
	}
	var tokens []string
	for _, field := range strings.Fields(scanner.Text()) {
		if strings.HasPrefix(field, ":") || strings.HasPrefix(field, "#") || strings.HasPrefix(field, "@") {
			break
		}
		tokens = append(tokens, field)
	}
	if len(tokens) == 0 {
		t.Fatal("private test keyword authority has no tokens")
	}
	return tokens
}
