package api_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/fillerairworthiness"
	"github.com/loomarr/loomarr/internal/store"
)

type fakeFillerScreeningService struct {
	summary filler.SegmentScreeningSummary
	hash    string
	path    string
}

func (s *fakeFillerScreeningService) ReadSegmentScreeningSummary(_ context.Context, hash, path string) (filler.SegmentScreeningSummary, error) {
	s.hash, s.path = hash, path
	return s.summary, nil
}

func TestFillerScreeningReturnsOneExactBrowserSafeFiveAxisProjection(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout, err := filler.NewLayout(filepath.Join(root, "clips"), filepath.Join(root, "watch"))
	if err != nil {
		t.Fatal(err)
	}
	st := openTestStore(t, filepath.Join(root, "api.db"))
	t.Cleanup(func() { _ = st.Close() })
	hash := strings.Repeat("a", 64)
	path := filepath.Join("aa", "child.mp4")
	if err := st.UpsertClip(t.Context(), store.Clip{Clip: filler.Clip{
		Hash: hash, Path: path, Name: "Screened child", Kind: filler.Commercial, DurationMs: 30_000,
	}}); err != nil {
		t.Fatal(err)
	}
	service := &fakeFillerScreeningService{summary: apiScreeningSummaryFixture(t, hash)}
	rights, err := filler.NewFillerRightsRegistry(st)
	if err != nil {
		t.Fatal(err)
	}
	recordedAt := time.Date(2026, time.September, 4, 20, 30, 0, 0, time.UTC)
	grant, err := filler.NewFillerRightsGrant(
		*service.summary.RightsScope, filler.FillerRightsAuthorized, filler.FillerRightsWithdrawalClear,
		strings.Repeat("9", 64), "reviewer-1", recordedAt, nil, nil, "", recordedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := rights.Record(t.Context(), grant); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.Router(slog.New(slog.DiscardHandler), api.Options{
		Store: st, Auth: testAuthorizer{}, Log: slog.New(slog.DiscardHandler),
		FillerLayout: layout, FillerScreening: service, FillerRights: rights,
	}))
	t.Cleanup(server.Close)

	response := do(t, server, http.MethodGet, "/v1/filler/screening?hash="+hash, adminToken, "")
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("screening → %d, want 200", response.StatusCode)
	}
	var body api.FillerScreeningDTO
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State != "available" || body.ClipHash != hash || body.Outcome != "pass" ||
		len(body.Axes) != 5 || body.Axes[0].Axis != "visual_safety" ||
		body.Axes[4].Axis != "playback_integrity" || body.Airworthiness == nil ||
		body.Airworthiness.Verdict != "pass" || body.Airworthiness.SubjectSHA256 != service.summary.SubjectSHA256 ||
		body.Airworthiness.SchemaVersion != fillerairworthiness.DecisionSchemaVersion ||
		body.Airworthiness.ContractVersion != fillerairworthiness.DecisionContractVersion ||
		body.Airworthiness.PolicyVersion == "" || body.Airworthiness.VocabularyVersion == "" ||
		len(body.Airworthiness.EvidenceSHA256s) != 3 || body.Airworthiness.AuthoritySHA256 == "" ||
		body.Airworthiness.DecisionSHA256 == "" || body.RightsReview == nil ||
		body.RightsReview.SourceID != service.summary.RightsScope.SourceID ||
		body.RightsReview.AcquisitionID != service.summary.RightsScope.AcquisitionID ||
		body.RightsReview.SourceMasterSHA256 != service.summary.RightsScope.SourceMasterSHA256 ||
		body.RightsReview.PolicySHA256 != service.summary.RightsScope.PolicySHA256 ||
		body.RightsReview.Use != filler.FillerBroadcastUse || !body.RightsReview.CanRecord ||
		body.RightsReview.CurrentGrant == nil || body.RightsReview.CurrentGrant.SHA256 != grant.SHA256 ||
		service.hash != hash ||
		service.path != filepath.Join(layout.ClipDir(), path) {
		t.Fatalf("screening body=%+v service=%+v", body, service)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private/path", "rawEvidence", "providerResponse", "transcript", "ocrText"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("browser projection leaked %q: %s", forbidden, raw)
		}
	}

	member := do(t, server, http.MethodGet, "/v1/filler/screening?hash="+hash, memberToken, "")
	defer func() { _ = member.Body.Close() }()
	if member.StatusCode != http.StatusForbidden {
		t.Fatalf("member screening → %d, want 403", member.StatusCode)
	}
	missing := do(t, server, http.MethodGet, "/v1/filler/screening?hash="+strings.Repeat("b", 64), adminToken, "")
	defer func() { _ = missing.Body.Close() }()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing screening → %d, want 404", missing.StatusCode)
	}
}

func apiScreeningSummaryFixture(t *testing.T, clipHash string) filler.SegmentScreeningSummary {
	t.Helper()
	subject := strings.Repeat("b", 64)
	decision := apiAirworthinessDecisionFixture(t, subject)
	axes := []filler.SegmentScreeningAxis{
		filler.ScreenVisualSafety, filler.ScreenSpokenSafety, filler.ScreenWrittenSafety,
		filler.ScreenRights, filler.ScreenPlayback,
	}
	summaries := make([]filler.SegmentScreeningAxisSummary, 0, len(axes))
	for index, axis := range axes {
		summaries = append(summaries, filler.SegmentScreeningAxisSummary{
			Axis: axis, Outcome: filler.ScreenPass, ReasonCode: "policy_clear",
			EvidenceSHA256: strings.Repeat(string(rune('1'+index)), 64),
		})
	}
	summary := filler.SegmentScreeningSummary{
		State: filler.ScreeningSummaryAvailable, ClipHash: clipHash,
		SubjectSHA256: subject, EvidenceSHA256: strings.Repeat("8", 64), Outcome: filler.ScreenPass,
		Axes: summaries, Airworthiness: &decision,
		RightsScope: &filler.FillerRightsScope{
			SourceID: "archive:commercials", AcquisitionID: "acq-17",
			SourceMasterSHA256: strings.Repeat("9", 64), PolicySHA256: strings.Repeat("4", 64),
			Use: filler.FillerBroadcastUse,
		},
		AssessedAt: time.Date(2026, time.September, 4, 21, 0, 0, 0, time.UTC),
	}
	if err := filler.ValidateSegmentScreeningSummary(summary); err != nil {
		t.Fatal(err)
	}
	return summary
}

func apiAirworthinessDecisionFixture(t *testing.T, subject string) fillerairworthiness.Decision {
	t.Helper()
	axes := []fillerairworthiness.Axis{
		fillerairworthiness.AxisVisual, fillerairworthiness.AxisSpoken, fillerairworthiness.AxisWritten,
	}
	profiles := make([]fillerairworthiness.AxisProfile, 0, len(axes))
	document := fillerairworthiness.Document{
		SchemaVersion: fillerairworthiness.EvidenceSchemaVersion, ContractVersion: fillerairworthiness.EvidenceContractVersion,
		SubjectSHA256: subject, DurationMS: 30_000,
	}
	for index, axis := range axes {
		flags := make([]fillerairworthiness.Flag, 0)
		for _, flag := range fillerairworthiness.Vocabulary() {
			if slices.Contains(fillerairworthiness.AxesForFlag(flag), axis) {
				flags = append(flags, flag)
			}
		}
		profile, err := fillerairworthiness.NormalizeAxisProfile(fillerairworthiness.AxisProfile{
			Axis: axis, EvidenceContract: "fixture-evidence-v1",
			PolicySHA256: strings.Repeat(string(rune('a'+index)), 64), CertificationSHA256: strings.Repeat("d", 64),
			ImplementationSHA256: strings.Repeat("e", 64), CertifiedFlags: flags,
		})
		if err != nil {
			t.Fatal(err)
		}
		profiles = append(profiles, profile)
		document.Axes = append(document.Axes, fillerairworthiness.AxisEvidence{
			SubjectSHA256: subject, Profile: profile, Coverage: fillerairworthiness.CoverageComplete,
			EvidenceSHA256: strings.Repeat(string(rune('5'+index)), 64), Observations: []fillerairworthiness.Observation{},
		})
	}
	evaluator, err := fillerairworthiness.New(fillerairworthiness.ProfileAllAges, profiles)
	if err != nil {
		t.Fatal(err)
	}
	decision := evaluator.Evaluate(document)
	if err := fillerairworthiness.ValidateDecision(decision); err != nil {
		t.Fatal(err)
	}
	return decision
}
