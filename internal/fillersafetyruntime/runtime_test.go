package fillersafetyruntime

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerairworthiness"
	"github.com/loomarr/loomarr/internal/fillerairworthinessprojection"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
	"github.com/loomarr/loomarr/internal/mediatools"
	"github.com/loomarr/loomarr/internal/testkit/operationfixture"
	"github.com/loomarr/loomarr/internal/testkit/recordfixture"
)

type runtimeRepository = operationfixture.Repository[fillersafety.LedgerRun, fillersafety.LedgerEvent, fillersafety.HostedCallReservation, fillersafety.HostedCallSettlement]

type fixedSourceInspector struct {
	inspection sourceInspection
	calls      int
}

func (i *fixedSourceInspector) Inspect(context.Context, string, string, mediatools.MediaToolIdentity) (sourceInspection, error) {
	i.calls++
	return i.inspection, nil
}

type operationEvaluator func(context.Context, fillersafety.EvaluationRequest) (fillersafety.EvaluationReport, error)

func (evaluate operationEvaluator) Evaluate(ctx context.Context, request fillersafety.EvaluationRequest) (fillersafety.EvaluationReport, error) {
	return evaluate(ctx, request)
}

func TestRuntimeBuildsExactSourceAuthorityAndReusesDurableRunTime(t *testing.T) {
	fixture := newRuntimeFixture(t)
	firstAt := fixture.now
	report, err := fixture.runtime.EvaluateSpokenSafety(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	requests := fixture.operation.Inputs()
	if report.Run.ID != fixture.request.OperationSHA256 || len(requests) != 1 || fixture.snapshotCalls != 1 {
		t.Fatalf("report=%+v requests=%d snapshots=%d", report, len(requests), fixture.snapshotCalls)
	}
	if len(fixture.operationConfigs) != 1 ||
		fixture.operationConfigs[0].Audio.Model != fixture.authority.AudioRoute.RequestedModel ||
		fixture.operationConfigs[0].Audio.ProviderSlug != fixture.authority.AudioRoute.UpstreamProviderSlug ||
		fixture.operationConfigs[0].Video.CapabilitySHA256 != fixture.authority.VideoRoute.CapabilitySHA256 ||
		fixture.operationConfigs[0].Budget.PerClipNanoUSD != fixture.runtime.config.Deployment.PerClipBudgetNanoUSD {
		t.Fatalf("operation config = %+v", fixture.operationConfigs)
	}
	first := requests[0]
	if first.StartedAt != firstAt || first.Source.Authority.MeasuredAt != firstAt ||
		first.Source.Authority.SourceID != fixture.request.Subject.SHA256 ||
		first.Source.Authority.SourceSHA256 != fixture.request.Subject.EvidenceSHA256 ||
		first.Source.Authority.SourceBytes != fixture.request.Subject.EvidenceBytes ||
		first.Source.Authority.DurationMS != fixture.request.Subject.DurationMS ||
		first.CertificationSHA256 != fixture.authoritySHA256 || first.Source.Path != fixture.request.EvidencePath {
		t.Fatalf("evaluation request = %+v", first)
	}

	durableAt := firstAt.Add(-time.Hour)
	if _, err := fixture.repository.BeginSpokenSafetyRun(context.Background(), fillersafety.LedgerRun{ID: fixture.request.OperationSHA256, CreatedAt: durableAt}); err != nil {
		t.Fatal(err)
	}
	fixture.now = firstAt.Add(3 * time.Hour)
	fixture.runtime.config.Now = func() time.Time { return fixture.now }
	if _, err := fixture.runtime.EvaluateSpokenSafety(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	requests = fixture.operation.Inputs()
	if len(requests) != 2 || fixture.snapshotCalls != 1 ||
		requests[1].StartedAt != durableAt ||
		requests[1].Source.Authority.MeasuredAt != durableAt || fixture.inspector.calls != 2 {
		t.Fatalf("retry request=%+v snapshots=%d inspections=%d", requests[1], fixture.snapshotCalls, fixture.inspector.calls)
	}
}

func TestRuntimeRejectsRouteAndMediaDriftBeforeOperation(t *testing.T) {
	t.Run("capability", func(t *testing.T) {
		fixture := newRuntimeFixture(t)
		fixture.snapshot.Models[0].Endpoints[0].Quantization = "changed"
		if _, err := fixture.runtime.EvaluateSpokenSafety(context.Background(), fixture.request); err == nil {
			t.Fatal("runtime accepted a route outside certification")
		}
		if fixture.operation.Calls() != 0 {
			t.Fatal("operation ran after route drift")
		}
	})

	t.Run("duration", func(t *testing.T) {
		fixture := newRuntimeFixture(t)
		fixture.inspector.inspection.Probe.DurationMs++
		if _, err := fixture.runtime.EvaluateSpokenSafety(context.Background(), fixture.request); err == nil {
			t.Fatal("runtime accepted remeasured duration drift")
		}
		if fixture.snapshotCalls != 0 || fixture.operation.Calls() != 0 {
			t.Fatal("route or operation ran after media drift")
		}
	})
}

type runtimeFixture struct {
	runtime          *Runtime
	repository       *runtimeRepository
	inspector        *fixedSourceInspector
	operation        *recordfixture.Recorder[fillersafety.EvaluationRequest, fillersafety.EvaluationReport]
	request          filler.SpokenSafetyProducerRequest
	authoritySHA256  string
	authority        fillersafetycert.Authority
	now              time.Time
	snapshot         fillerbakeoff.OpenRouterSnapshot
	snapshotCalls    int
	operationConfigs []fillersafety.OpenRouterEvaluationConfig
}

func newRuntimeFixture(t *testing.T) *runtimeFixture {
	t.Helper()
	now := time.Date(2026, 9, 4, 21, 0, 0, 0, time.UTC)
	policy := fillersafety.Policy{
		SchemaVersion: fillersafety.PolicySchemaVersion, ContractVersion: fillersafety.PolicyContractVersion,
		PolicyID: "policy-runtime-v1", GeneratedAt: now.Add(-24 * time.Hour), MaximumInterSegmentGapMS: 500,
		Rules: []fillersafety.PolicyRule{{
			ID: "rule-0123456789abcdef01234567", Class: fillersafety.PolicyClassProhibited,
			MatchMode: fillersafety.PolicyModeExactWords, Variants: []string{"private fixture"},
		}},
	}
	profile, err := fillersafety.OpenRouterRuntimeProfile(policy)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runtimeSnapshot(now)
	_, capability, err := fillerbakeoff.OpenRouterAssessorIdentity(snapshot, "vendor/model", "Pinned Provider", "pinned/provider", fillersafetycert.ReasoningDisabled)
	if err != nil {
		t.Fatal(err)
	}
	route := fillersafetycert.RouteAuthority{
		Role: "spoken-safety", RequestedProvider: "openrouter", RequestedModel: "vendor/model",
		ResolvedProvider: "openrouter", ResolvedModel: "vendor/model-2026",
		UpstreamProvider: "Pinned Provider", UpstreamProviderSlug: "pinned/provider",
		ReasoningMode: fillersafetycert.ReasoningDisabled, ModelFamily: "vendor-family",
		CapabilitySHA256: capability,
	}
	audio, video := route, route
	audio.Rung, audio.Modalities, audio.PromptSHA256, audio.SchemaSHA256 = "native-audio", profile.Audio.Modalities, profile.Audio.PromptSHA256, profile.Audio.SchemaSHA256
	video.Rung, video.Modalities, video.PromptSHA256, video.SchemaSHA256 = "complete-video", profile.Video.Modalities, profile.Video.PromptSHA256, profile.Video.SchemaSHA256
	authoritySHA := strings.Repeat("c", 64)
	authority := fillersafetycert.Authority{
		SchemaVersion: fillersafetycert.SchemaVersion, ContractVersion: fillersafetycert.ContractVersion,
		AuthoredAt: now.Add(-time.Hour), ChallengeKind: fillersafetycert.ChallengeCertification,
		PolicySHA256: profile.PolicySHA256, ProposerSHA256: profile.ProposerSHA256,
		ProposerFamily: "deterministic-windows", Implementation: profile.EvaluationImplementation,
		AudioRoute: audio, VideoRoute: video,
		Cases: []fillersafetycert.AuthorityCase{{SourceBytes: 4_096, DurationMS: 30_000}},
	}
	projection, err := fillerairworthinessprojection.SealSpokenAuthority(fillerairworthinessprojection.SpokenAuthority{
		PolicySHA256: profile.PolicySHA256, CertificationSHA256: authoritySHA,
		ProposerSHA256: profile.ProposerSHA256, EvaluationImplementation: profile.EvaluationImplementation,
		Rules: []fillerairworthinessprojection.Rule{{
			ID: policy.Rules[0].ID, Flag: fillerairworthiness.FlagSlurOrDegradingLanguage,
			Severity: fillerairworthiness.SeverityHigh, Context: fillerairworthiness.ContextPresence,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := SealDeployment(Deployment{
		AuthoritySHA256: authoritySHA, MaximumSourceBytes: 4_096, MaximumSourceDurationMS: 30_000,
		AudioMaximumInputTokens: 1_000, VideoMaximumInputTokens: 1_000,
		AudioReservationNanoUSD: 10_000_000, VideoReservationNanoUSD: 10_000_000,
		PerClipBudgetNanoUSD: 20_000_000, PerRunBudgetNanoUSD: 20_000_000, PerDayBudgetNanoUSD: 200_000_000,
		CertifiedEvidenceExecution: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := mediatools.MediaToolIdentity{Name: "ffmpeg", Version: "ffmpeg fixture", ExecutableSHA256: strings.Repeat("d", 64)}
	inspector := &fixedSourceInspector{inspection: sourceInspection{
		Probe: filler.Probed{DurationMs: 30_000}, FFmpeg: tool,
		FFprobe: mediatools.MediaToolIdentity{Name: "ffprobe", Version: "ffprobe fixture", ExecutableSHA256: strings.Repeat("e", 64)},
	}}
	repository := &runtimeRepository{
		State:         &operationfixture.State[fillersafety.LedgerRun, fillersafety.LedgerEvent]{},
		RunMatches:    func(run fillersafety.LedgerRun, id string) bool { return run.ID == id },
		ValidateRun:   fillersafety.ValidateLedgerRun,
		ConflictError: fillersafety.ErrLedgerConflict,
		ValidateEvent: func(event fillersafety.LedgerEvent) error {
			_, err := fillersafety.CanonicalLedgerEvent(event)
			return err
		},
		ReserveFunc: func(context.Context, fillersafety.HostedCallReservation) (fillersafety.LedgerEvent, error) {
			return fillersafety.LedgerEvent{}, nil
		},
		SettleFunc: func(context.Context, fillersafety.HostedCallSettlement) (fillersafety.LedgerEvent, error) {
			return fillersafety.LedgerEvent{}, nil
		},
	}
	operation := &recordfixture.Recorder[fillersafety.EvaluationRequest, fillersafety.EvaluationReport]{Respond: func(request fillersafety.EvaluationRequest) (fillersafety.EvaluationReport, error) {
		return fillersafety.EvaluationReport{Run: fillersafety.LedgerRun{ID: request.RunID}}, nil
	}}
	fixture := &runtimeFixture{
		repository: repository, inspector: inspector, operation: operation, authoritySHA256: authoritySHA,
		authority: authority,
		now:       now, snapshot: snapshot,
		request: filler.SpokenSafetyProducerRequest{
			OperationSHA256: strings.Repeat("f", 64),
			Subject: fillerairworthinessprojection.Subject{
				SHA256: strings.Repeat("a", 64), EvidenceSHA256: strings.Repeat("b", 64), EvidenceBytes: 4_096, DurationMS: 30_000,
			},
			EvidencePath: "/private/evidence.mp4", EvidenceTool: tool,
		},
	}
	config := Config{
		Projection: projection, Policy: policy, Deployment: deployment, Repository: repository,
		APIKey: "private", FFmpegPath: "ffmpeg", Client: http.DefaultClient,
		Now: func() time.Time { return fixture.now },
		FetchSnapshot: func(context.Context, fillerbakeoff.OpenRouterSnapshotConfig) (fillerbakeoff.OpenRouterSnapshot, error) {
			fixture.snapshotCalls++
			return fixture.snapshot, nil
		},
	}
	runtime, err := newRuntimeWithAuthority(config, authority, authoritySHA, inspector, "https://openrouter.example/api/v1")
	if err != nil {
		t.Fatal(err)
	}
	runtime.build = func(config fillersafety.OpenRouterEvaluationConfig) (fillersafety.EvaluationOperation, fillersafety.RuntimeProfile, error) {
		fixture.operationConfigs = append(fixture.operationConfigs, config)
		return operationEvaluator(func(_ context.Context, request fillersafety.EvaluationRequest) (fillersafety.EvaluationReport, error) {
			return operation.Call(request)
		}), profile, nil
	}
	fixture.runtime = runtime
	return fixture
}

func runtimeSnapshot(at time.Time) fillerbakeoff.OpenRouterSnapshot {
	return fillerbakeoff.OpenRouterSnapshot{
		SchemaVersion: fillerbakeoff.OpenRouterSnapshotSchemaVersion,
		SourceBaseURL: fillerbakeoff.OpenRouterBaseURL, RetrievedAt: at, Requests: 3, ResponseBytes: 100,
		Models: []fillerbakeoff.OpenRouterModelSnapshot{{
			ID: "vendor/model", CanonicalSlug: "vendor/model-2026", Name: "Model", Created: 1,
			InputModalities: []string{"audio", "text", "video"}, OutputModalities: []string{"text"},
			Endpoints: []fillerbakeoff.OpenRouterEndpointSnapshot{{
				Name: "Pinned Provider | model", ModelID: "vendor/model", ProviderName: "Pinned Provider", ProviderSlug: "pinned/provider",
				Quantization: "fp16", ContextLength: 8_192, MaxCompletionTokens: 4_096, MaxPromptTokens: 4_096,
				SupportedParameters: []string{"reasoning", "response_format", "structured_outputs"},
				Pricing:             map[string]string{"completion": "0.000002", "prompt": "0.000001"}, ZDR: true,
			}},
		}},
	}
}
