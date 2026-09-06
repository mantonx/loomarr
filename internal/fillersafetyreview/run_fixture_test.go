package fillersafetyreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
	"github.com/loomarr/loomarr/internal/fillersafetycorpus"
	"github.com/loomarr/loomarr/internal/openroutermedia"
	"github.com/loomarr/loomarr/internal/testkit/httpfixture"
)

const testRuleID = "rule-000000000000000000000001"
const testReviewBaseURL = "https://reviewer.test/api/v1"

type reviewTestEndpoint struct {
	baseURL string
	client  *http.Client
}

func newReviewTestEndpoint(handler http.Handler) reviewTestEndpoint {
	return reviewTestEndpoint{
		baseURL: testReviewBaseURL,
		client: &http.Client{Transport: httpfixture.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			return recorder.Result(), nil
		})},
	}
}

type reviewFixture struct {
	root, planPath, checkpointDir, outputPath string
	config                                    Config
	now                                       time.Time
}

func newReviewFixture(t *testing.T, baseURL string) reviewFixture {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "assembled")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	policy := fillersafety.Policy{
		SchemaVersion: fillersafety.PolicySchemaVersion, ContractVersion: fillersafety.PolicyContractVersion,
		PolicyID: "policy-review-test", GeneratedAt: now.Add(-4 * time.Hour), MaximumInterSegmentGapMS: 250,
		Rules: []fillersafety.PolicyRule{{
			ID: testRuleID, Class: fillersafety.PolicyClassProhibited,
			MatchMode: fillersafety.PolicyModeExactWords, Variants: []string{"restricted token"},
		}},
	}
	policyRaw := marshalPrivateJSON(t, filepath.Join(root, "policy.json"), policy)
	policySHA := testHash(policyRaw)
	truthRaw := writePrivateTestFile(t, filepath.Join(root, "evidence", "truth.json"), []byte("private truth evidence"))
	rightsRaw := writePrivateTestFile(t, filepath.Join(root, "evidence", "rights.json"), []byte("private rights evidence"))
	positiveSlices := []string{
		fillersafetycert.SliceAccentLocale, fillersafetycert.SliceClipping,
		fillersafetycert.SliceCodecTransform, fillersafetycert.SliceDerivativeCompilation,
		fillersafetycert.SliceMusicOverlap, fillersafetycert.SliceNoise,
		fillersafetycert.SlicePartialToken, fillersafetycert.SlicePhoneticConfusable,
		fillersafetycert.SlicePlacement, fillersafetycert.SliceQuietSpeech,
		fillersafetycert.SliceSpeedPitch,
	}
	cleanSlices := []string{
		fillersafetycert.SliceMusicOnly, fillersafetycert.SliceNearMatch,
		fillersafetycert.SliceTargetLocale, fillersafetycert.SliceWordless,
	}
	draft := fillersafetycert.AuthorityDraft{
		SchemaVersion:   fillersafetycert.AuthorityDraftSchemaVersion,
		ContractVersion: fillersafetycert.AuthorityDraftContractVersion,
		ChallengeKind:   fillersafetycert.ChallengeCertification, PolicySHA256: policySHA,
		ProposerSHA256: testFixtureSHA(1), ProposerFamily: "evaluated-proposer",
		Implementation: "spoken-safety-evaluator-v1",
		AudioRoute:     fixtureRoute("native-audio", []string{"audio"}, "evaluated-audio", 10),
		VideoRoute:     fixtureRoute("complete-video", []string{"audio", "video"}, "evaluated-video", 20),
	}
	worklist := fillersafetycorpus.ReviewWorklist{
		SchemaVersion:   fillersafetycorpus.ReviewWorklistSchemaVersion,
		ContractVersion: fillersafetycorpus.ReviewWorklistContractVersion,
		AssembledAt:     now.Add(-2 * time.Hour), PolicyPath: "policy.json", PolicySHA256: policySHA,
	}
	for index := 0; index < fillersafetycert.MinimumPositiveFamilies+fillersafetycert.MinimumCleanFamilies; index++ {
		caseID := fmt.Sprintf("case-%03d", index+1)
		relative := filepath.ToSlash(filepath.Join("cases", caseID, "source.mp4"))
		source := []byte(fmt.Sprintf("source-media-%03d", index+1))
		writePrivateTestFile(t, filepath.Join(root, filepath.FromSlash(relative)), source)
		sourceSHA := testHash(source)
		authority := fillersafety.SourceAuthority{
			SchemaVersion: fillersafety.SourceAuthoritySchemaVersion, PolicySHA256: policySHA,
			Implementation: draft.Implementation, SourceID: caseID, SourceSHA256: sourceSHA,
			SourceBytes: int64(len(source)), DurationMS: 3_000, HasAudio: true, HasVideo: true,
			MeasuredAt: now.Add(-3 * time.Hour),
			FFmpeg:     fillersafety.ToolIdentity{Version: "ffmpeg 7.1", BinarySHA256: testFixtureSHA(30)},
			FFprobe:    fillersafety.ToolIdentity{Version: "ffprobe 7.1", BinarySHA256: testFixtureSHA(31)},
		}
		authoritySHA, err := fillersafety.SourceAuthoritySHA256(authority)
		if err != nil {
			t.Fatal(err)
		}
		draftCase := fillersafetycert.AuthorityDraftCase{
			CaseID: caseID, SourcePath: relative, SourceAuthority: authority,
			SourceFamily:        fmt.Sprintf("speaker-%03d", index+1),
			TruthProvenancePath: "evidence/truth.json", TruthProvenanceSHA256: testHash(truthRaw),
			RightsPath: "evidence/rights.json", RightsSHA256: testHash(rightsRaw), Locale: "en-US",
		}
		workCase := fillersafetycorpus.ReviewWorklistCase{
			CaseID: caseID, SourcePath: relative, SourceSHA256: sourceSHA,
			SourceAuthoritySHA256: authoritySHA, SourceBytes: int64(len(source)), DurationMS: 3_000,
			TruthProvenancePath: "evidence/truth.json", TruthProvenanceSHA256: testHash(truthRaw),
			TruthProvenanceBytes: int64(len(truthRaw)), RightsPath: "evidence/rights.json",
			RightsSHA256: testHash(rightsRaw), RightsBytes: int64(len(rightsRaw)), Locale: "en-US",
		}
		if index < fillersafetycert.MinimumPositiveFamilies {
			draftCase.Label = fillersafetycert.LabelPositive
			draftCase.Slices = []string{positiveSlices[index%len(positiveSlices)]}
			draftCase.PositiveIntervals = []fillersafetycert.PositiveInterval{{RuleID: testRuleID, StartMS: 500, EndMS: 1_500}}
			workCase.Claim = fillersafetycorpus.PreparedCohortKindPositiveCandidate
			workCase.Slices = append([]string(nil), draftCase.Slices...)
			workCase.PositiveIntervals = []fillersafetycorpus.PreparedPositiveInterval{{RuleID: testRuleID, StartMS: 500, EndMS: 1_500}}
		} else {
			draftCase.Label = fillersafetycert.LabelClean
			draftCase.Slices = []string{cleanSlices[(index-fillersafetycert.MinimumPositiveFamilies)%len(cleanSlices)]}
			workCase.Claim = fillersafetycorpus.PreparedCohortKindCleanCandidate
			workCase.Slices = append([]string(nil), draftCase.Slices...)
		}
		draft.Cases = append(draft.Cases, draftCase)
		worklist.Cases = append(worklist.Cases, workCase)
	}
	draftRaw, draftSHA, err := fillersafetycert.MarshalCertificationDraft(draft)
	if err != nil {
		t.Fatal(err)
	}
	writePrivateTestFile(t, filepath.Join(root, "draft.json"), draftRaw)
	worklist.DraftSHA256 = draftSHA
	worklistRaw := marshalPrivateJSON(t, filepath.Join(root, "primary-review-one.json"), worklist)
	snapshot := fixtureSnapshot(baseURL, now.Add(-time.Hour))
	snapshotRaw := marshalPrivateJSON(t, filepath.Join(root, "reviewer-snapshot.json"), snapshot)
	plan := Plan{
		SchemaVersion: PlanSchemaVersion, ContractVersion: PlanContractVersion,
		Draft: testAuthority("draft.json", draftRaw), Worklist: testAuthority("primary-review-one.json", worklistRaw),
		Snapshot: testAuthority("reviewer-snapshot.json", snapshotRaw), ReviewerID: "primary-model-one",
		ModelFamily: "independent-review-family", Model: "vendor/reviewer-model",
		ResolvedModel: "vendor/reviewer-model-2026", UpstreamProvider: "Pinned Provider",
		UpstreamProviderSlug: "pinned-provider", DisableReasoning: true,
		ExpectedCases: len(draft.Cases), MaximumRequests: len(draft.Cases) + 1,
		MaximumChargeNanoUSD: 2_000_000, MaximumSpendNanoUSD: int64(len(draft.Cases)+1) * 2_000_000,
		MaximumInputBytes: 64 << 20, MaximumAudioBytes: 1 << 20,
		PerCaseTimeoutMS: 30_000, MaximumWallTimeMS: 600_000,
	}
	planPath := filepath.Join(parent, "review-plan.json")
	marshalPrivateJSON(t, planPath, plan)
	fixture := reviewFixture{
		root: root, planPath: planPath, checkpointDir: filepath.Join(parent, "checkpoint"),
		outputPath: filepath.Join(parent, "output", "review.json"), now: now,
	}
	fixture.config = Config{
		PlanPath: planPath, InputRoot: root, APIKey: "secret", FFmpegPath: "fake-ffmpeg",
		CheckpointDirectory: fixture.checkpointDir, OutputPath: fixture.outputPath,
	}
	return fixture
}

func (fixture reviewFixture) runtime(client *http.Client, baseURL string) reviewRuntime {
	return reviewRuntime{
		baseURL: baseURL, client: client, now: func() time.Time { return fixture.now },
		call: openroutermedia.Call, extract: audioExtractFunc(func(context.Context, string, fillersafety.ToolIdentity, *fillersafety.CompleteMediaPlan, int64) ([]byte, error) {
			return []byte("RIFF0000WAVE"), nil
		}),
		identify: func(context.Context, string) (fillersafety.ToolIdentity, string, error) {
			return fillersafety.ToolIdentity{Version: "ffmpeg 7.1", BinarySHA256: testFixtureSHA(90)}, "/fake/ffmpeg", nil
		},
	}
}

func fixtureRoute(rung string, modalities []string, family string, seed int) fillersafetycert.RouteAuthority {
	return fillersafetycert.RouteAuthority{
		Role: "spoken-safety", Rung: rung, Modalities: modalities,
		RequestedProvider: "openrouter", RequestedModel: "vendor/evaluated",
		ResolvedProvider: "openrouter", ResolvedModel: "vendor/evaluated-2026",
		UpstreamProvider: "provider", ModelFamily: family, CapabilitySHA256: testFixtureSHA(seed),
		PromptSHA256: testFixtureSHA(seed + 1), SchemaSHA256: testFixtureSHA(seed + 2),
	}
}

func fixtureSnapshot(baseURL string, retrievedAt time.Time) fillerbakeoff.OpenRouterSnapshot {
	return fixtureSnapshotForModel(baseURL, retrievedAt, "vendor/reviewer-model", "vendor/reviewer-model-2026")
}

func fixtureSnapshotForModel(baseURL string, retrievedAt time.Time, model, resolvedModel string) fillerbakeoff.OpenRouterSnapshot {
	return fillerbakeoff.OpenRouterSnapshot{
		SchemaVersion: fillerbakeoff.OpenRouterSnapshotSchemaVersion, SourceBaseURL: baseURL,
		RetrievedAt: retrievedAt, Requests: 3, ResponseBytes: 1024,
		Models: []fillerbakeoff.OpenRouterModelSnapshot{{
			ID: model, CanonicalSlug: resolvedModel,
			Name: "Reviewer Model", Created: 1, InputModalities: []string{"audio", "text"},
			OutputModalities: []string{"text"}, Endpoints: []fillerbakeoff.OpenRouterEndpointSnapshot{{
				Name: "Pinned Provider", ModelID: model, ProviderName: "Pinned Provider",
				ProviderSlug: "pinned-provider", Quantization: "fp16", ContextLength: 128_000,
				MaxCompletionTokens: 4096, MaxPromptTokens: 128_000,
				SupportedParameters: []string{"response_format", "structured_outputs"},
				Pricing:             map[string]string{"completion": "0.000001", "prompt": "0.000001"},
				Status:              0, ZDR: true,
			}},
		}},
	}
}

func marshalPrivateJSON(t *testing.T, path string, value any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	return writePrivateTestFile(t, path, raw)
}

func writePrivateTestFile(t *testing.T, path string, raw []byte) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func testAuthority(path string, raw []byte) fillersafetycorpus.FileAuthority {
	return fillersafetycorpus.FileAuthority{Path: path, SHA256: testHash(raw), Bytes: int64(len(raw))}
}

func testHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func testFixtureSHA(seed int) string { return fmt.Sprintf("%064x", seed) }
