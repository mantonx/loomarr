package fillerstructurewindowopenrouter

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func TestCertifiedRuntimeRefreshesMetadataAndRequiresReviewedProfiles(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	snapshot, authority, deployment := certifiedRuntimeFixture(t, now)
	fetches := 0
	runtime, err := NewCertifiedRuntime(CertifiedRuntimeConfig{
		Authority: authority, Deployment: deployment, APIKey: "secret",
		SourceRoot: t.TempDir(), MediaRoot: t.TempDir(), EvidenceRoot: t.TempDir(), FFmpegPath: "ffmpeg",
		Ledger: runtimeNoopLedger{}, Now: func() time.Time { return now },
		FetchSnapshot: func(_ context.Context, config fillerbakeoff.OpenRouterSnapshotConfig) (fillerbakeoff.OpenRouterSnapshot, error) {
			fetches++
			if len(config.Models) != 2 || config.Models[0] != "vendor/model-a" || config.Models[1] != "vendor/model-b" || config.APIKey != "secret" {
				t.Fatalf("snapshot config=%+v", config)
			}
			value := snapshot
			value.RetrievedAt = config.RetrievedAt
			return value, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := runtime.freshSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.assessors(first, now); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.freshSnapshot(t.Context()); err != nil || fetches != 1 {
		t.Fatalf("cached snapshot fetches=%d error=%v", fetches, err)
	}
	now = now.Add(snapshotRefreshAge)
	if _, err := runtime.freshSnapshot(t.Context()); err != nil || fetches != 2 {
		t.Fatalf("refreshed snapshot fetches=%d error=%v", fetches, err)
	}

	drifted := first
	drifted.Models = append([]fillerbakeoff.OpenRouterModelSnapshot(nil), first.Models...)
	drifted.Models[0].CanonicalSlug += "-different"
	if _, err := runtime.assessors(drifted, first.RetrievedAt); err == nil || !strings.Contains(err.Error(), "profiles") {
		t.Fatalf("profile drift error=%v", err)
	}
}

func TestCertifiedRuntimeRejectsOutOfEnvelopeSourceBeforeMetadata(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	_, authority, deployment := certifiedRuntimeFixture(t, now)
	fetched := false
	runtime, err := NewCertifiedRuntime(CertifiedRuntimeConfig{
		Authority: authority, Deployment: deployment, APIKey: "secret",
		SourceRoot: t.TempDir(), MediaRoot: t.TempDir(), EvidenceRoot: t.TempDir(), FFmpegPath: "ffmpeg",
		Ledger: runtimeNoopLedger{}, Now: func() time.Time { return now },
		FetchSnapshot: func(context.Context, fillerbakeoff.OpenRouterSnapshotConfig) (fillerbakeoff.OpenRouterSnapshot, error) {
			fetched = true
			return fillerbakeoff.OpenRouterSnapshot{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Assess(t.Context(), filler.StructureAssessmentSource{Source: filler.SplitSourceAsset{
		SHA256: strings.Repeat("a", 64), ClipHash: strings.Repeat("a", 64), Bytes: 1_000,
		DurationMs: authority.MinimumSourceDurationMS - 1,
	}})
	if err == nil || fetched {
		t.Fatalf("error=%v fetched=%v", err, fetched)
	}
}

type capturedRuntimePreparer struct {
	calls    int
	prepared filler.StructureAssessmentWindowMediaSet
	err      error
}

func (p *capturedRuntimePreparer) PrepareWindows(context.Context, filler.StructureAssessmentSource, fillerstructurewindow.Plan) (filler.StructureAssessmentWindowMediaSet, error) {
	p.calls++
	return p.prepared, p.err
}

func TestCertifiedRuntimePreflightsMediaBeforeProviderMetadata(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	_, authority, deployment := certifiedRuntimeFixture(t, now)
	sourceRoot := t.TempDir()
	fetched := false
	runtime, err := NewCertifiedRuntime(CertifiedRuntimeConfig{
		Authority: authority, Deployment: deployment, APIKey: "secret",
		SourceRoot: sourceRoot, MediaRoot: t.TempDir(), EvidenceRoot: t.TempDir(), FFmpegPath: "ffmpeg",
		Ledger: runtimeNoopLedger{}, Now: func() time.Time { return now },
		FetchSnapshot: func(context.Context, fillerbakeoff.OpenRouterSnapshotConfig) (fillerbakeoff.OpenRouterSnapshot, error) {
			fetched = true
			return fillerbakeoff.OpenRouterSnapshot{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	preparer := &capturedRuntimePreparer{err: errors.New("media preflight failed")}
	runtime.preparer = preparer
	_, err = runtime.Assess(t.Context(), filler.StructureAssessmentSource{
		Source: filler.SplitSourceAsset{
			Role: filler.SplitSourceLegacyPlayback, SHA256: strings.Repeat("a", 64), ClipHash: strings.Repeat("b", 64),
			Bytes: 1_000, DurationMs: authority.MaximumSourceDurationMS, Path: "source.mp4",
		},
		FullPath: filepath.Join(sourceRoot, "source.mp4"),
	})
	if err == nil || preparer.calls != 1 || fetched {
		t.Fatalf("error=%v preparer calls=%d metadata fetched=%v", err, preparer.calls, fetched)
	}
}

func TestCertifiedRuntimeChecksReviewedMediaEnvelopeBeforeProviderMetadata(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	_, authority, deployment := certifiedRuntimeFixture(t, now)
	authority.MaximumWindowBytes = 999
	authority.SHA256 = fillerstructurewindow.MaterializationAuthoritySHA256(authority)
	deployment.AuthoritySHA256 = authority.SHA256
	deployment.SHA256 = DeploymentSHA256(deployment)
	sourceRoot := t.TempDir()
	input, prepared := certifiedRuntimePreparedFixture(t, sourceRoot, authority.MaximumSourceDurationMS)
	fetched := false
	runtime, err := NewCertifiedRuntime(CertifiedRuntimeConfig{
		Authority: authority, Deployment: deployment, APIKey: "secret",
		SourceRoot: sourceRoot, MediaRoot: t.TempDir(), EvidenceRoot: t.TempDir(), FFmpegPath: "ffmpeg",
		Ledger: runtimeNoopLedger{}, Now: func() time.Time { return now },
		FetchSnapshot: func(context.Context, fillerbakeoff.OpenRouterSnapshotConfig) (fillerbakeoff.OpenRouterSnapshot, error) {
			fetched = true
			return fillerbakeoff.OpenRouterSnapshot{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	preparer := &capturedRuntimePreparer{prepared: prepared}
	runtime.preparer = preparer
	if _, err := runtime.Assess(t.Context(), input); err == nil || preparer.calls != 1 || fetched {
		t.Fatalf("error=%v preparer calls=%d metadata fetched=%v", err, preparer.calls, fetched)
	}
}

func certifiedRuntimePreparedFixture(t *testing.T, sourceRoot string, durationMS int64) (filler.StructureAssessmentSource, filler.StructureAssessmentWindowMediaSet) {
	t.Helper()
	source := filler.SplitSourceAsset{
		Role: filler.SplitSourceLegacyPlayback, SHA256: strings.Repeat("a", 64), ClipHash: strings.Repeat("b", 64),
		Bytes: 1_000, DurationMs: durationMS, Path: "source.mp4",
	}
	input := filler.StructureAssessmentSource{Source: source, FullPath: filepath.Join(sourceRoot, source.Path)}
	plan, err := fillerstructurewindow.NewPlan(fillerstructure.Source{SHA256: source.SHA256, Bytes: source.Bytes, DurationMS: durationMS})
	if err != nil {
		t.Fatal(err)
	}
	identities := make([]fillerstructure.AssessmentMedia, len(plan.Windows))
	for ordinal, window := range plan.Windows {
		identities[ordinal] = fillerstructure.AssessmentMedia{
			SHA256: strings.Repeat(string(rune('1'+ordinal)), 64), Bytes: 1_000,
			DurationMS:    window.MediaEndMS - window.MediaStartMS,
			ProfileSHA256: plan.Profile.AssessmentMediaProfileSHA256, LineageSHA256: strings.Repeat(string(rune('a'+ordinal)), 64),
		}
	}
	set, err := fillerstructurewindow.NewMediaSet(plan, identities)
	if err != nil {
		t.Fatal(err)
	}
	prepared := filler.StructureAssessmentWindowMediaSet{Source: source, Authority: set}
	for ordinal, window := range plan.Windows {
		prepared.Windows = append(prepared.Windows, filler.StructureAssessmentWindowMedia{
			Window: window, Media: set.Windows[ordinal], FullPath: filepath.Join(t.TempDir(), "window.mp4"),
		})
	}
	return input, prepared
}

type runtimeNoopLedger struct{}

func (runtimeNoopLedger) Reserve(context.Context, fillerstructurewindow.CallReservation) (fillerstructurewindow.CallReservationState, error) {
	return fillerstructurewindow.CallReservationAccepted, nil
}

func (runtimeNoopLedger) Settle(context.Context, fillerstructurewindow.CallRecord) error { return nil }

func certifiedRuntimeFixture(t *testing.T, now time.Time) (fillerbakeoff.OpenRouterSnapshot, fillerstructurewindow.MaterializationAuthority, Deployment) {
	t.Helper()
	snapshot := fillerbakeoff.OpenRouterSnapshot{
		SchemaVersion: fillerbakeoff.OpenRouterSnapshotSchemaVersion, SourceBaseURL: fillerbakeoff.OpenRouterBaseURL,
		RetrievedAt: now, Requests: 4, ResponseBytes: 1_000,
	}
	deployment := Deployment{
		SchemaVersion: DeploymentSchemaVersion, ContractVersion: DeploymentContractVersion,
		PerSourceBudgetNanoUSD: 144_000_000, PerDayBudgetNanoUSD: 1_440_000_000,
		AutomaticAssessmentAllowed: true,
	}
	for _, suffix := range []string{"a", "b"} {
		modelID := "vendor/model-" + suffix
		provider := "Provider " + strings.ToUpper(suffix)
		providerSlug := "provider/" + suffix
		snapshot.Models = append(snapshot.Models, fillerbakeoff.OpenRouterModelSnapshot{
			ID: modelID, CanonicalSlug: modelID + "-20260914", Name: "Model " + suffix, Created: 1,
			InputModalities: []string{"text", "video"}, OutputModalities: []string{"text"},
			Endpoints: []fillerbakeoff.OpenRouterEndpointSnapshot{{
				Name: provider + " endpoint", ModelID: modelID, ProviderName: provider, ProviderSlug: providerSlug,
				Quantization: "fp16", ContextLength: 32_768, MaxCompletionTokens: MaximumOutputTokens,
				MaxPromptTokens: 20_000, SupportedParameters: []string{"reasoning", "response_format", "structured_outputs"},
				Pricing: map[string]string{"prompt": "0.000001", "completion": "0.000002"}, Status: 0, ZDR: true,
			}},
		})
		deployment.Families = append(deployment.Families, DeploymentFamily{
			AssessorID: "assessor-" + suffix, ModelFamily: "family-" + suffix, Model: modelID,
			UpstreamProvider: provider, UpstreamProviderSlug: providerSlug, ReasoningMode: ReasoningDisabled,
			MaximumInputTokens: 20_000, ReservationNanoUSD: 24_000_000,
		})
	}
	windowProfile := fillerstructurewindow.CanonicalProfile()
	authority := fillerstructurewindow.MaterializationAuthority{
		SchemaVersion:             fillerstructurewindow.MaterializationAuthoritySchemaVersion,
		ContractVersion:           fillerstructurewindow.MaterializationAuthorityContractVersion,
		WindowCertificationSHA256: strings.Repeat("1", 64), ShortLongShadowSHA256: strings.Repeat("2", 64),
		WindowProfileSHA256: windowProfile.SHA256, AssessmentMediaProfileSHA256: windowProfile.AssessmentMediaProfileSHA256,
		MinimumSourceDurationMS: 120_001, MaximumSourceDurationMS: 300_000,
		MaximumWindowBytes: 16 << 20, MaximumWindows: 3,
		ReducerVersion: fillerstructure.ReducerContractVersion, BoundaryToleranceMS: 2_000,
		AllowedUnits: []fillerstructure.Unit{fillerstructure.UnitCompilation},
		AllowedRoles: []fillerstructure.Role{fillerstructure.RoleCommercial},
		ReviewerID:   "maintainer", ReviewedAt: now, AutomaticMaterializationAllowed: true,
	}
	for _, family := range deployment.Families {
		modelDigest, capabilitySHA, err := fillerbakeoff.OpenRouterAssessorIdentity(
			snapshot, family.Model, family.UpstreamProvider, family.UpstreamProviderSlug, family.ReasoningMode,
		)
		if err != nil {
			t.Fatal(err)
		}
		authority.Assessors = append(authority.Assessors, fillerstructure.AssessorProfile{
			ID: family.AssessorID, ModelFamily: family.ModelFamily, Provider: "openrouter", Model: family.Model,
			ModelDigest: modelDigest, CapabilitySHA256: capabilitySHA,
			PromptVersion: fillerstructurewindow.DirectVideoPromptVersion, EvidenceContract: fillerstructurewindow.CallRecordContractVersion,
		})
	}
	authority.SHA256 = fillerstructurewindow.MaterializationAuthoritySHA256(authority)
	deployment.AuthoritySHA256 = authority.SHA256
	deployment.SHA256 = DeploymentSHA256(deployment)
	return snapshot, authority, deployment
}
