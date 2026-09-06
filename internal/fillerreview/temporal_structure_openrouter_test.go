package fillerreview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

const temporalStructureStandaloneResponse = `{"segments":[{"endMs":10000,"role":"commercial","decisiveAtMs":[200],"reason":"one complete product offer"}]}`

func TestRunOpenRouterTemporalStructureRejectsFutureChallengeBeforeRequest(t *testing.T) {
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "future-challenge")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	now := fixture.generatedAt.Add(-time.Second)
	config := temporalStructureOpenRouterTestConfig(manifestPath, filepath.Join(t.TempDir(), "private"), []string{manifest.Cases[0].Alias}, server, now)
	if _, err := RunOpenRouterTemporalStructure(t.Context(), config); err == nil || !strings.Contains(err.Error(), "predates the challenge") {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("provider received %d requests", requests.Load())
	}
}

func TestRunOpenRouterTemporalStructureRejectsReservationBelowSnapshotPriceBound(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "price-bound")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	config := temporalStructureOpenRouterTestConfig(manifestPath, filepath.Join(t.TempDir(), "private"), []string{manifest.Cases[0].Alias}, server, now)
	config.ReservationNanoUSD = 20_000_000
	config.MaxSpendNanoUSD = 20_000_000
	if _, err := RunOpenRouterTemporalStructure(t.Context(), config); err == nil || !strings.Contains(err.Error(), "below the snapshot price bound") {
		t.Fatalf("error=%v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("provider received %d requests", requests.Load())
	}
}

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
	if result.PublicManifestSHA256 != challenge.PublicManifestSHA256 || result.ReasoningMode != TemporalStructureOpenRouterReasoningDisabled || result.MaximumInputTokens != 20_000 || result.EstimatedMaximumChargeNanoUSD != 22_048_000 || result.Requests != 1 || len(result.Assessments) != 1 || result.ProductionAdmissionAllowed || result.Assessments[0].Unit.Kind != fillereval.UnitStandalone || result.Assessments[0].Role.Kind != fillereval.TemporalRoleCommercial || len(result.Assessments[0].Inference.Calls) != 1 || result.Assessments[0].Inference.Calls[0].Axis != "structure" {
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

func TestRunOpenRouterTemporalStructureRecordsChargeAboveReservationAndStops(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "over-reservation")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "generation", "model": "review/vendor-model",
			"choices": []any{map[string]any{"message": map[string]any{"content": temporalStructureStandaloneResponse}}},
			"usage":   map[string]any{"prompt_tokens": 42_781, "completion_tokens": 283, "cost": 0.132588},
			"openrouter_metadata": map[string]any{
				"attempt":   1,
				"endpoints": map[string]any{"available": []any{map[string]any{"provider": "Provider Route", "model": "review/vendor-model", "selected": true}}},
			},
		})
	}))
	defer server.Close()
	checkpointDir := filepath.Join(t.TempDir(), "private")
	config := temporalStructureOpenRouterTestConfig(manifestPath, checkpointDir, []string{manifest.Cases[0].Alias}, server, now)
	config.ReservationNanoUSD = 100_000_000
	config.MaxSpendNanoUSD = 100_000_000
	result, err := RunOpenRouterTemporalStructure(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	attempt := result.Attempts[0]
	assessment := result.Assessments[0]
	if attempt.State != temporalOpenRouterAttemptOverReservation || attempt.ChargedAmountUSD != "0.132588" || attempt.ChargedNanoUSD != 132_588_000 || attempt.ReservedNanoUSD != 100_000_000 || result.ChargedNanoUSD != 132_588_000 || result.ConsumedNanoUSD != 132_588_000 || result.OverReservationNanoUSD != 32_588_000 || assessment.OperationalFailure == nil || assessment.OperationalFailure.Code != fillereval.TemporalFailureProvider || assessment.Unit != nil || assessment.Role != nil || len(assessment.Segments) != 0 {
		t.Fatalf("result=%+v attempt=%+v assessment=%+v", result, attempt, assessment)
	}
	if _, err := RunOpenRouterTemporalStructure(t.Context(), config); err == nil || !strings.Contains(err.Error(), "over-reservation") {
		t.Fatalf("resume error=%v", err)
	}
}

func TestRunOpenRouterTemporalStructureExplicitlyEnablesRequiredReasoning(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "required-reasoning")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	var request openRouterStructuredRequest
	server := newTemporalSuitabilityServer(t, &request, temporalStructureStandaloneResponse)
	defer server.Close()
	config := temporalStructureOpenRouterTestConfig(manifestPath, filepath.Join(t.TempDir(), "private"), []string{manifest.Cases[0].Alias}, server, now)
	config.ReasoningMode = TemporalStructureOpenRouterReasoningRequired
	if _, err := RunOpenRouterTemporalStructure(t.Context(), config); err != nil {
		t.Fatal(err)
	}
	if request.Reasoning == nil || !request.Reasoning.Enabled {
		t.Fatalf("reasoning = %+v", request.Reasoning)
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

func TestRunOpenRouterTemporalStructureTurnsRedundantWholeFileClaimIntoFailure(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "invalid-role")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	server := newTemporalSuitabilityServer(t, nil, `{"unit":"compilation","segments":[{"endMs":10000,"role":"commercial","decisiveAtMs":[200],"reason":"one product offer"}]}`)
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

func TestTemporalStructureOpenRouterWireEnforcesCompleteSegmentShape(t *testing.T) {
	tests := []struct {
		name string
		wire temporalStructureOpenRouterWire
		want string
	}{
		{name: "valid compilation", wire: temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 100, Role: "commercial", DecisiveAtMS: []int64{50}, Reason: "offer"}, {EndMS: 1_000, Role: "promo", DecisiveAtMS: []int64{200}, Reason: "programme promotion"}}}},
		{name: "valid programme with spots", wire: temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 300, Role: "programme_fragment", DecisiveAtMS: []int64{100}, Reason: "programme scene"}, {EndMS: 700, Role: "commercial", DecisiveAtMS: []int64{500}, Reason: "product offer"}, {EndMS: 1_000, Role: "programme_fragment", DecisiveAtMS: []int64{900}, Reason: "programme resumes"}}}},
		{name: "valid unicode character limit", wire: temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 100, Role: "commercial", DecisiveAtMS: []int64{50}, Reason: strings.Repeat("a", 511) + "–"}, {EndMS: 1_000, Role: "promo", DecisiveAtMS: []int64{200}, Reason: "programme promotion"}}}},
		{name: "missing timeline", wire: temporalStructureOpenRouterWire{}, want: "segment plan"},
		{name: "non-increasing end", wire: temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 400, Role: "commercial", DecisiveAtMS: []int64{200}, Reason: "offer"}, {EndMS: 300, Role: "promo", DecisiveAtMS: []int64{250}, Reason: "promotion"}, {EndMS: 1_000, Role: "commercial", DecisiveAtMS: []int64{700}, Reason: "second offer"}}}, want: "segment plan"},
		{name: "duplicate times", wire: temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 1_000, Role: "commercial", DecisiveAtMS: []int64{100, 100}, Reason: "offer"}}}, want: "segment plan"},
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

func TestTemporalStructureAssessmentDerivesWholeFileClaimFromSegmentTimeline(t *testing.T) {
	tests := []struct {
		name     string
		segments []temporalStructureOpenRouterSegmentWire
		unit     fillereval.UnitKind
		role     fillereval.TemporalRole
	}{
		{name: "standalone", segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 1_000, Role: "commercial", DecisiveAtMS: []int64{500}, Reason: "one product offer"}}, unit: fillereval.UnitStandalone, role: fillereval.TemporalRoleCommercial},
		{name: "compilation", segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 400, Role: "commercial", DecisiveAtMS: []int64{200}, Reason: "product offer"}, {EndMS: 1_000, Role: "promo", DecisiveAtMS: []int64{700}, Reason: "programme promotion"}}, unit: fillereval.UnitCompilation},
		{name: "programme excerpt", segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 1_000, Role: "programme_fragment", DecisiveAtMS: []int64{100, 900}, Reason: "dependent programme edges"}}, unit: fillereval.UnitProgrammeExcerpt},
		{name: "programme with spots", segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 300, Role: "programme_fragment", DecisiveAtMS: []int64{100}, Reason: "programme begins"}, {EndMS: 700, Role: "psa", DecisiveAtMS: []int64{500}, Reason: "public-service message"}, {EndMS: 1_000, Role: "programme_fragment", DecisiveAtMS: []int64{900}, Reason: "programme resumes"}}, unit: fillereval.UnitProgrammeSpots},
		{name: "unclear", segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 1_000, Role: "ambiguous", Reason: "insufficient evidence"}}, unit: fillereval.UnitUnclear},
		{name: "unusable", segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 1_000, Role: "unusable", Reason: "corrupt throughout"}}, unit: fillereval.UnitUnusable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := temporalStructureOpenRouterWire{Segments: test.segments}
			if err := validateTemporalStructureOpenRouterWire(wire, 1_000); err != nil {
				t.Fatal(err)
			}
			assessment := temporalStructureAssessmentFromWire("case", wire, time.Unix(1, 0), fillereval.TemporalInferenceCall{Axis: "structure", Attempt: 1, ResponseSHA256: strings.Repeat("a", 64)})
			if assessment.Unit == nil || assessment.Unit.Kind != test.unit {
				t.Fatalf("unit = %+v, want %s", assessment.Unit, test.unit)
			}
			if test.role == "" && assessment.Role != nil || test.role != "" && (assessment.Role == nil || assessment.Role.Kind != test.role) {
				t.Fatalf("role = %+v, want %s", assessment.Role, test.role)
			}
		})
	}
}

func TestTemporalStructureOpenRouterSchemaRequiresCompleteSegmentPlan(t *testing.T) {
	schema := temporalStructureOpenRouterSchema(1_000)
	required, ok := schema["required"].([]string)
	if !ok || !slices.Contains(required, "segments") {
		t.Fatalf("top-level required fields = %#v", schema["required"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %#v", schema["properties"])
	}
	segments, ok := properties["segments"].(map[string]any)
	if !ok || segments["minItems"] != 1 || segments["maxItems"] != nil {
		t.Fatalf("segments schema = %#v", properties["segments"])
	}
	if len(required) != 1 || len(properties) != 1 {
		t.Fatalf("provider schema duplicates derived whole-file fields: %#v", schema)
	}
	item, ok := segments["items"].(map[string]any)
	if !ok {
		t.Fatalf("segment item schema = %#v", segments["items"])
	}
	itemProperties, ok := item["properties"].(map[string]any)
	if !ok || itemProperties["startMs"] != nil {
		t.Fatalf("provider schema duplicates segment boundaries: %#v", item)
	}
	reason, ok := itemProperties["reason"].(map[string]any)
	if !ok || reason["maxLength"] != nil {
		t.Fatalf("provider schema includes an unsupported string-length keyword: %#v", reason)
	}
}

func TestNormalizeTemporalStructureOpenRouterWireSortsMechanicalTimestamps(t *testing.T) {
	wire := temporalStructureOpenRouterWire{
		Segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 1_000, Role: "promo", DecisiveAtMS: []int64{800, 200}, Reason: "programme promotion"}},
	}
	normalizeTemporalStructureOpenRouterWire(&wire)
	if err := validateTemporalStructureOpenRouterWire(wire, 1_000); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(wire.Segments[0].DecisiveAtMS, []int64{200, 800}) {
		t.Fatalf("segment timestamps were not normalized: %+v", wire.Segments)
	}
}

func TestNormalizeTemporalStructureOpenRouterWireCoalescesOnlyAdjacentProgrammeObservations(t *testing.T) {
	wire := temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{
		{EndMS: 4_500, Role: "programme_fragment", DecisiveAtMS: []int64{0, 1_000, 4_800}, Reason: "programme title card"},
		{EndMS: 9_500, Role: "programme_fragment", DecisiveAtMS: []int64{5_000, 9_000}, Reason: "programme title"},
		{EndMS: 20_000, Role: "programme_fragment", DecisiveAtMS: []int64{11_000, 19_500}, Reason: "programme credits"},
		{EndMS: 80_000, Role: "commercial", DecisiveAtMS: []int64{25_000, 75_000}, Reason: "complete product offer"},
		{EndMS: 99_555, Role: "programme_fragment", DecisiveAtMS: []int64{82_000, 98_000}, Reason: "programme resumes"},
	}}
	normalizeTemporalStructureOpenRouterWire(&wire)
	if err := validateTemporalStructureOpenRouterWire(wire, 99_555); err != nil {
		t.Fatal(err)
	}
	if len(wire.Segments) != 3 || wire.Segments[0].EndMS != 20_000 || wire.Segments[1].Role != "commercial" {
		t.Fatalf("normalized segments = %+v", wire.Segments)
	}
	assessment := temporalStructureAssessmentFromWire("case", wire, time.Unix(1, 0), fillereval.TemporalInferenceCall{Axis: "structure", Attempt: 1, ResponseSHA256: strings.Repeat("a", 64)})
	if assessment.Unit == nil || assessment.Unit.Kind != fillereval.UnitProgrammeSpots || !slices.Equal(assessment.Unit.DecisiveAtMS, []int64{20_000, 80_000}) || assessment.Segments[0].StartMS != 0 || assessment.Segments[1].StartMS != 20_000 {
		t.Fatalf("assessment = %+v", assessment)
	}
}

func TestNormalizeTemporalStructureOpenRouterWirePreservesAdjacentSameRoleFiller(t *testing.T) {
	wire := temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{
		{EndMS: 500, Role: "commercial", DecisiveAtMS: []int64{200}, Reason: "first offer"},
		{EndMS: 1_000, Role: "commercial", DecisiveAtMS: []int64{700}, Reason: "second offer"},
	}}
	normalizeTemporalStructureOpenRouterWire(&wire)
	if len(wire.Segments) != 2 {
		t.Fatalf("adjacent filler was coalesced: %+v", wire.Segments)
	}
}

func TestTemporalStructureAssessmentDerivesCompleteCoverageFromExclusiveEnds(t *testing.T) {
	wire := temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{
		{EndMS: 20_000, Role: "programme_fragment", DecisiveAtMS: []int64{1_000, 15_000}, Reason: "programme opening"},
		{EndMS: 79_000, Role: "commercial", DecisiveAtMS: []int64{22_000, 73_000}, Reason: "complete product offer"},
		{EndMS: 99_555, Role: "programme_fragment", DecisiveAtMS: []int64{82_000, 98_000}, Reason: "programme resumes"},
	}}
	if err := validateTemporalStructureOpenRouterWire(wire, 99_555); err != nil {
		t.Fatal(err)
	}
	assessment := temporalStructureAssessmentFromWire("case", wire, time.Unix(1, 0), fillereval.TemporalInferenceCall{Axis: "structure", Attempt: 1, ResponseSHA256: strings.Repeat("a", 64)})
	if assessment.Segments[1].StartMS != 20_000 || assessment.Segments[2].StartMS != 79_000 || assessment.Unit.Kind != fillereval.UnitProgrammeSpots {
		t.Fatalf("assessment = %+v", assessment)
	}
}

func TestRunOpenRouterTemporalStructureTurnsInvalidConditionalRoleIntoFailure(t *testing.T) {
	now := time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC)
	fixture := newTemporalStructureFixture(t)
	root, _ := fixture.build(t, "invalid-role")
	manifestPath := filepath.Join(root, "public", "manifest.json")
	manifest := readStrictTestJSON[TemporalStructureChallengeManifest](t, manifestPath)
	server := newTemporalSuitabilityServer(t, nil, `{"segments":[{"endMs":10000,"role":"not-a-role","decisiveAtMs":[200],"reason":"invalid"}]}`)
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

func TestTemporalStructureOpenRouterWireEnforcesClosedConditionalShape(t *testing.T) {
	tests := []struct {
		name string
		wire temporalStructureOpenRouterWire
		want string
	}{
		{name: "valid compilation", wire: temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 500, Role: "commercial", DecisiveAtMS: []int64{100}, Reason: "offer"}, {EndMS: 1_000, Role: "promo", DecisiveAtMS: []int64{600}, Reason: "promotion"}}}},
		{name: "incomplete timeline", wire: temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 999, Role: "commercial", DecisiveAtMS: []int64{100}, Reason: "offer"}}}, want: "complete source"},
		{name: "invalid conditional role", wire: temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 1_000, Role: "not-a-role", DecisiveAtMS: []int64{100}, Reason: "wrong"}}}, want: "segment 0"},
		{name: "duplicate times", wire: temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{{EndMS: 1_000, Role: "commercial", DecisiveAtMS: []int64{100, 100}, Reason: "offer"}}}, want: "segment 0"},
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
	wire := temporalStructureOpenRouterWire{Segments: []temporalStructureOpenRouterSegmentWire{
		{EndMS: 131_500, Role: "commercial", DecisiveAtMS: []int64{59_000, 102_000, 131_500, 20_150, 20_150}, Reason: "offer"},
		{EndMS: 132_000, Role: "promo", DecisiveAtMS: []int64{300, 100, 300}, Reason: "promotion"},
	}}
	normalizeTemporalStructureOpenRouterWire(&wire)
	if !slices.Equal(wire.Segments[0].DecisiveAtMS, []int64{20_150, 59_000, 102_000, 131_500}) || !slices.Equal(wire.Segments[1].DecisiveAtMS, []int64{100, 300}) {
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
		MaxSpendNanoUSD: int64(len(aliases)) * 100_000_000, ReservationNanoUSD: 100_000_000,
		MaximumInputTokens:   20_000,
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
