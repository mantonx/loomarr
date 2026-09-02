//go:build !windows

package fillersafety

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/testkit/execfixture"
)

const fakeSherpaRuleID = "rule-0123456789abcdef01234567"

func TestSherpaProposerStagesArtifactsAndProjectsOpaqueResults(t *testing.T) {
	runtimeBody := `
case "$*" in *SAFE*) exit 31;; esac
keywords=""
for argument do
  case "$argument" in --keywords-file=*) keywords=${argument#--keywords-file=};; esac
done
[ -n "$keywords" ] || exit 32
grep -q '@rule-0123456789abcdef01234567' "$keywords" || exit 33
printf '%s\n' '{"start_time":0,"keyword":"rule-0123456789abcdef01234567","timestamps":[1,1.12],"tokens":["SAFE","TOKEN"]}'
`
	fixture := newFakeSherpaFixture(t, runtimeBody)
	proposer, err := newSherpaProposerWithContract(context.Background(), fixture.config, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	workspace := proposer.workspace
	t.Cleanup(func() { _ = proposer.Close() })
	if proposer.identity.RuntimeSHA256 != sherpaRuntimeIdentitySHA256(fixture.contract) || proposer.identity.ModelSHA256 != sherpaModelIdentitySHA256(fixture.contract) {
		t.Fatalf("identity does not bind staged manifests: %+v", proposer.identity)
	}
	if err := os.WriteFile(fixture.config.RuntimePath, []byte("changed after construction"), 0o700); err != nil {
		t.Fatal(err)
	}

	plan := fixture.plan(t)
	defer func() { _ = plan.Close() }()
	evidence := runProposal(context.Background(), proposer, proposer.identity, &plan)
	if evidence.ProposalState != ProposalComplete || len(evidence.Candidates) != 1 || evidence.Candidates[0].StartMS != 1_000 || evidence.Candidates[0].EndMS != 1_160 {
		t.Fatalf("unexpected evidence: %+v", evidence)
	}
	if err := proposer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("private workspace survived close: %v", err)
	}
}

func TestKnownSherpaArtifactContractBindsSupportedHost(t *testing.T) {
	t.Parallel()
	contract, err := knownSherpaArtifactContract()
	if err != nil {
		t.Fatal(err)
	}
	if !validSherpaArtifactContract(contract) || contract.platform == "" || contract.runtimeArchiveSHA256 == contract.runtimeSHA256 {
		t.Fatalf("invalid known contract: %+v", contract)
	}
}

func TestSherpaProposerDiscardsPrivateProcessDiagnostics(t *testing.T) {
	fixture := newFakeSherpaFixture(t, `printf '%s\n' 'private restricted diagnostic' >&2; exit 9`)
	proposer, err := newSherpaProposerWithContract(context.Background(), fixture.config, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proposer.Close() }()
	plan := fixture.plan(t)
	defer func() { _ = plan.Close() }()
	request := proposalRequest{
		AuthoritySHA256: plan.AuthoritySHA256, PolicySHA256: plan.PolicySHA256,
		SourceSHA256: plan.SourceSHA256, SourceBytes: plan.SourceBytes, SourcePath: plan.SourcePath,
		DurationMS: plan.Audio.EndMS, FFmpeg: plan.FFmpeg,
	}
	_, err = proposer.Propose(context.Background(), request)
	if err == nil || strings.Contains(err.Error(), "private restricted diagnostic") || strings.Contains(err.Error(), plan.SourcePath) {
		t.Fatalf("unsafe process error: %v", err)
	}
}

func TestSherpaProposerRejectsArtifactDriftBeforeExecution(t *testing.T) {
	fixture := newFakeSherpaFixture(t, `exit 0`)
	fixture.contract.encoderSHA256 = strings.Repeat("f", 64)
	_, err := newSherpaProposerWithContract(context.Background(), fixture.config, fixture.contract)
	if err == nil || strings.Contains(err.Error(), fixture.config.EncoderPath) {
		t.Fatalf("artifact drift error=%v", err)
	}
}

func TestSherpaBoundedOutputDiscardsStderrAndCancelsAtCeiling(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := &sherpaBoundedOutput{limit: 4, cancel: cancel}
	if _, err := sink.Write([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	retained, exceeded := sink.result()
	if !exceeded || len(retained) != 0 {
		t.Fatalf("retained=%q exceeded=%v", retained, exceeded)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("output ceiling did not cancel process context")
	}
}

type fakeSherpaFixture struct {
	config   sherpaProposerConfig
	contract sherpaArtifactContract
	policy   string
	ffmpeg   ToolIdentity
}

func newFakeSherpaFixture(t *testing.T, runtimeBody string) fakeSherpaFixture {
	t.Helper()
	dir := t.TempDir()
	runtimePath := execfixture.POSIX(t, "sherpa-worker", runtimeBody)
	ffmpegPath := execfixture.POSIX(t, "ffmpeg", `for destination do :; done; printf 'RIFF0000WAVE' > "$destination"`)
	paths := map[string]string{
		"runtime": runtimePath,
		"library": filepath.Join(dir, "libonnxruntime.so"),
		"encoder": filepath.Join(dir, "encoder.onnx"),
		"decoder": filepath.Join(dir, "decoder.onnx"),
		"joiner":  filepath.Join(dir, "joiner.onnx"),
		"tokens":  filepath.Join(dir, "tokens.txt"),
	}
	for role, path := range paths {
		if role == "runtime" {
			continue
		}
		contents := []byte(role + " bytes")
		if role == "tokens" {
			contents = []byte("SAFE 0\nTOKEN 1\n")
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	contract := fakeSherpaContract("test/arch", map[string][]byte{
		"runtime": readTestFile(t, runtimePath), "library": readTestFile(t, paths["library"]),
		"encoder": readTestFile(t, paths["encoder"]), "decoder": readTestFile(t, paths["decoder"]),
		"joiner": readTestFile(t, paths["joiner"]), "tokens": readTestFile(t, paths["tokens"]),
	})
	policySHA := strings.Repeat("9", 64)
	authority := acousticKeywordAuthority{
		SchemaVersion: acousticKeywordAuthoritySchemaVersion, ContractVersion: acousticKeywordAuthorityContractVersion,
		PolicySHA256: policySHA, ModelSHA256: sherpaModelIdentitySHA256(contract), BPEModelSHA256: contract.bpeModelSHA256,
		Rules: []acousticKeywordRule{{ID: fakeSherpaRuleID, Variants: [][]string{{"SAFE", "TOKEN"}}}},
	}
	authorityPath := filepath.Join(dir, "authority.json")
	raw, err := json.Marshal(authority)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authorityPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return fakeSherpaFixture{
		config: sherpaProposerConfig{
			RuntimePath: runtimePath, RuntimeLibraryPath: paths["library"], EncoderPath: paths["encoder"],
			DecoderPath: paths["decoder"], JoinerPath: paths["joiner"], TokensPath: paths["tokens"],
			KeywordAuthorityPath: authorityPath, FFmpegPath: ffmpegPath,
		},
		contract: contract, policy: policySHA,
		ffmpeg: ToolIdentity{Version: "test-ffmpeg", BinarySHA256: digestBytes(readTestFile(t, ffmpegPath))},
	}
}

func (fixture fakeSherpaFixture) plan(t *testing.T) CompleteMediaPlan {
	t.Helper()
	contents := []byte("source media")
	sourcePath := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(sourcePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	authority := validSourceAuthority()
	authority.PolicySHA256 = fixture.policy
	authority.SourceSHA256, authority.SourceBytes = sourceIdentity(contents)
	authority.DurationMS = 3_000
	authority.FFmpeg = fixture.ffmpeg
	plan, err := PlanCompleteMedia(context.Background(), SourceRequest{Authority: authority, Path: sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func fakeSherpaContract(platform string, artifacts map[string][]byte) sherpaArtifactContract {
	content := func(role string) []byte {
		if value := artifacts[role]; len(value) != 0 {
			return value
		}
		return []byte(role)
	}
	return sherpaArtifactContract{
		platform: platform, runtimeArchiveSHA256: digestBytes([]byte("runtime archive")),
		runtimeSHA256: digestBytes(content("runtime")), librarySHA256: digestBytes(content("library")),
		modelArchiveSHA256: digestBytes([]byte("model archive")), encoderSHA256: digestBytes(content("encoder")),
		decoderSHA256: digestBytes(content("decoder")), joinerSHA256: digestBytes(content("joiner")),
		tokensSHA256: digestBytes(content("tokens")), bpeModelSHA256: digestBytes([]byte("bpe model")),
	}
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
