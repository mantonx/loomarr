package fillerreview

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const temporalStructureStandaloneResponse = `{"unit":"standalone","unitDecisiveAtMs":[100],"unitReason":"independent opening and close","role":"commercial","roleDecisiveAtMs":[200],"roleReason":"product offer framing"}`

func TestRunOpenRouterTemporalStructureBindsOneAtomicVideoAssessment(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, challenge := fixture.build(t, "direct-video")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	alias := manifest.Cases[0].Alias
	var request openRouterStructuredRequest
	server := newTemporalSuitabilityServer(t, &request, temporalStructureStandaloneResponse)
	defer server.Close()
	checkpointDir := filepath.Join(t.TempDir(), "private")
	config := temporalStructureOpenRouterTestConfig(manifestPath, checkpointDir, []string{alias}, server, now)
	result, err := RunOpenRouterTemporalStructure(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if result.PublicManifestSHA256 != challenge.PublicManifestSHA256 || result.ReasoningMode != TemporalStructureOpenRouterReasoningDisabled || result.Requests != 1 || len(result.Assessments) != 1 || result.ProductionAdmissionAllowed || result.Assessments[0].Unit.Kind != fillereval.UnitStandalone || result.Assessments[0].Role.Kind != fillereval.TemporalRoleCommercial || len(result.Assessments[0].Inference.Calls) != 1 || result.Assessments[0].Inference.Calls[0].Axis != "structure" {
		t.Fatalf("result = %+v", result)
	}
	parts := request.Messages[1].Content
	if len(parts) != 2 || parts[1].Type != "video_url" || parts[1].VideoURL == nil || !strings.HasPrefix(parts[1].VideoURL.URL, "data:video/mp4;base64,") {
		t.Fatalf("structure request parts = %+v", parts)
	}
	if request.MaxTokens != 4_096 || result.Assessor.PromptVersion != TemporalStructureOpenRouterPromptVersion {
		t.Fatalf("request max tokens=%d prompt version=%q", request.MaxTokens, result.Assessor.PromptVersion)
	}
	attempt := result.Attempts[0]
	rawPath := filepath.Join(checkpointDir, filepath.FromSlash(attempt.RawResponsePath))
	info, err := os.Stat(rawPath)
	if err != nil || info.Mode().Perm() != 0o600 || attempt.State != temporalOpenRouterAttemptAccepted || attempt.ResponseSHA256 != result.Assessments[0].Inference.Calls[0].ResponseSHA256 {
		t.Fatalf("raw mode=%v attempt=%+v error=%v", info.Mode(), attempt, err)
	}
}

func TestValidateTemporalStructureOpenRouterResultRejectsEvidenceAccountingDrift(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "result-drift")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	server := newTemporalSuitabilityServer(t, nil, temporalStructureStandaloneResponse)
	defer server.Close()
	result, err := RunOpenRouterTemporalStructure(t.Context(), temporalStructureOpenRouterTestConfig(manifestPath, filepath.Join(t.TempDir(), "private"), []string{manifest.Cases[0].Alias}, server, now))
	if err != nil {
		t.Fatal(err)
	}
	selected := manifest.Cases[:1]
	tests := []struct {
		name   string
		mutate func(*TemporalStructureOpenRouterResult)
		want   string
	}{
		{name: "reasoning", mutate: func(candidate *TemporalStructureOpenRouterResult) { candidate.ReasoningMode = "" }, want: "identity"},
		{name: "prompt", mutate: func(candidate *TemporalStructureOpenRouterResult) { candidate.PromptSHA256 = strings.Repeat("f", 64) }, want: "identity"},
		{name: "model digest", mutate: func(candidate *TemporalStructureOpenRouterResult) {
			candidate.Assessor.ModelDigest = strings.Repeat("f", 64)
		}, want: "identity"},
		{name: "aggregate spend", mutate: func(candidate *TemporalStructureOpenRouterResult) { candidate.ConsumedNanoUSD++ }, want: "aggregate spend"},
		{name: "attempt reservation", mutate: func(candidate *TemporalStructureOpenRouterResult) { candidate.Attempts[0].ReservedNanoUSD++ }, want: "attempt 0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneTemporalStructureOpenRouterResult(t, result)
			test.mutate(&candidate)
			if err := validateTemporalStructureOpenRouterResult(candidate, manifest, selected); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRunOpenRouterTemporalStructureTurnsInvalidConditionalRoleIntoFailure(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "invalid-role")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	server := newTemporalSuitabilityServer(t, nil, `{"unit":"compilation","unitDecisiveAtMs":[100],"unitReason":"join","role":"commercial","roleDecisiveAtMs":[200],"roleReason":"invalid"}`)
	defer server.Close()
	result, err := RunOpenRouterTemporalStructure(t.Context(), temporalStructureOpenRouterTestConfig(manifestPath, filepath.Join(t.TempDir(), "private"), []string{manifest.Cases[0].Alias}, server, now))
	if err != nil {
		t.Fatal(err)
	}
	assessment := result.Assessments[0]
	if assessment.OperationalFailure == nil || assessment.OperationalFailure.Code != fillereval.TemporalFailureInvalidResponse || result.Attempts[0].State != temporalOpenRouterAttemptFailed {
		t.Fatalf("assessment=%+v attempt=%+v", assessment, result.Attempts[0])
	}
}

func TestRunOpenRouterTemporalStructureRequiresSnapshotVideoModality(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "snapshot-modality")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	server := newTemporalSuitabilityServer(t, nil, temporalStructureStandaloneResponse)
	defer server.Close()
	config := temporalStructureOpenRouterTestConfig(manifestPath, filepath.Join(t.TempDir(), "private"), []string{manifest.Cases[0].Alias}, server, now)
	config.Snapshot.Models[0].InputModalities = []string{"image", "text"}
	if _, err := RunOpenRouterTemporalStructure(t.Context(), config); err == nil || !strings.Contains(err.Error(), "text/video") {
		t.Fatalf("error = %v", err)
	}
}

func TestTemporalStructureOpenRouterWireEnforcesClosedConditionalShape(t *testing.T) {
	tests := []struct {
		name string
		wire temporalStructureOpenRouterWire
		want string
	}{
		{name: "valid compilation", wire: temporalStructureOpenRouterWire{Unit: "compilation", UnitDecisiveAtMS: []int64{100}, UnitReason: "join", Role: "none"}},
		{name: "missing standalone role", wire: temporalStructureOpenRouterWire{Unit: "standalone", UnitDecisiveAtMS: []int64{100}, UnitReason: "bounded", Role: "none"}, want: "standalone role"},
		{name: "non standalone role", wire: temporalStructureOpenRouterWire{Unit: "programme_excerpt", UnitDecisiveAtMS: []int64{0}, UnitReason: "cut", Role: "promo", RoleDecisiveAtMS: []int64{1}, RoleReason: "wrong"}, want: "carries role"},
		{name: "duplicate times", wire: temporalStructureOpenRouterWire{Unit: "compilation", UnitDecisiveAtMS: []int64{100, 100}, UnitReason: "join", Role: "none"}, want: "unit claim"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateTemporalStructureOpenRouterWire(test.wire, 1_000)
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestNormalizeTemporalStructureOpenRouterWireSortsAndDeduplicatesEvidenceTimes(t *testing.T) {
	wire := temporalStructureOpenRouterWire{
		UnitDecisiveAtMS: []int64{59_000, 102_000, 131_500, 20_150, 20_150},
		RoleDecisiveAtMS: []int64{300, 100, 300},
	}
	normalizeTemporalStructureOpenRouterWire(&wire)
	if !slices.Equal(wire.UnitDecisiveAtMS, []int64{20_150, 59_000, 102_000, 131_500}) || !slices.Equal(wire.RoleDecisiveAtMS, []int64{100, 300}) {
		t.Fatalf("normalized wire = %+v", wire)
	}
}

func temporalStructureOpenRouterTestConfig(manifestPath, checkpointDir string, aliases []string, server *httptest.Server, now time.Time) TemporalStructureOpenRouterConfig {
	snapshot := openRouterReviewSnapshot(server.URL, now)
	snapshot.Models[0].InputModalities = append(snapshot.Models[0].InputModalities, "video")
	return TemporalStructureOpenRouterConfig{
		PublicManifestPath: manifestPath, CaseAliases: aliases, CheckpointDir: checkpointDir,
		BaseURL: server.URL, APIKey: "test-key", Snapshot: snapshot, Model: "review/vendor-model", ModelFamily: "video-family",
		UpstreamProvider: "Provider Route", UpstreamProviderSlug: "provider/route", AssessorID: "structure-assessor",
		ReasoningMode: TemporalStructureOpenRouterReasoningDisabled,
		ExpectedCases: len(aliases), PerCaseTimeout: time.Second, MaxRequests: len(aliases),
		MaxSpendNanoUSD: int64(len(aliases)) * 2_000_000, MaxChargeNanoUSD: 2_000_000,
		AllowInsecureTestURL: true, Client: server.Client(), Now: func() time.Time { return now },
	}
}

func writeTemporalStructureResult(t *testing.T, path string, result TemporalStructureOpenRouterResult) {
	t.Helper()
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func cloneTemporalStructureOpenRouterResult(t *testing.T, result TemporalStructureOpenRouterResult) TemporalStructureOpenRouterResult {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var clone TemporalStructureOpenRouterResult
	if err := decodeStrictReviewJSON(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
