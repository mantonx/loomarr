package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filleradmission"
	"github.com/loomarr/loomarr/internal/fillerdecision"
	"github.com/loomarr/loomarr/internal/store"
)

type decisionListBody[T any] struct {
	Rows  []T `json:"rows"`
	Total int `json:"total"`
}

type reviewWire struct {
	ID, ClipHash, Question string
	ApplicationMode        string   `json:"applicationMode"`
	ReasonCodes            []string `json:"reasonCodes"`
	EvidenceRefs           []string `json:"evidenceRefs"`
}

type diagnosticWire struct {
	ID, ClipHash, Code, Recovery string
	Retryable                    bool
}

type activityWire struct {
	ID, ActionID, DecisionID, ClipHash, Kind string
	ApplicationMode                          string `json:"applicationMode"`
}

func TestFillerDecisionActivityProjectsApplicationMode(t *testing.T) {
	srv, st := newServer(t)
	seedDecisionAPI(t, st)

	res := do(t, srv, http.MethodGet, "/v1/filler/decisions/activity?limit=10", memberToken, "")
	var activity decisionListBody[activityWire]
	decodeDecisionResponse(t, res, &activity)
	if activity.Total != 1 || len(activity.Rows) != 1 || activity.Rows[0].ApplicationMode != "shadow" {
		t.Fatalf("activity application mode = %+v, want one shadow row", activity)
	}
}

func TestFillerDecisionProjectionsSeparateHumanWorkFromDiagnostics(t *testing.T) {
	srv, st := newServer(t)
	seedDecisionAPI(t, st)

	if res := do(t, srv, http.MethodGet, "/v1/filler/decisions/overview", memberToken, ""); res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		t.Fatalf("member overview = %d, want 200", res.StatusCode)
	} else {
		var body struct {
			Healthy, _  bool
			NextAction  string `json:"nextAction"`
			ActionCount int    `json:"actionCount"`
			Counts      struct {
				UnresolvedReviews, Operational, Retryable int
			}
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if body.NextAction != "retry_processing" || body.ActionCount != 1 ||
			body.Counts.UnresolvedReviews != 1 || body.Counts.Operational != 1 {
			t.Fatalf("overview = %+v", body)
		}
	}

	for _, path := range []string{"/v1/filler/decisions/reviews", "/v1/filler/decisions/diagnostics"} {
		res := do(t, srv, http.MethodGet, path, memberToken, "")
		if res.StatusCode != http.StatusForbidden {
			_ = res.Body.Close()
			t.Fatalf("member GET %s = %d, want 403", path, res.StatusCode)
		}
		_ = res.Body.Close()
	}

	res := do(t, srv, http.MethodGet, "/v1/filler/decisions/reviews?limit=10", adminToken, "")
	var reviews decisionListBody[reviewWire]
	decodeDecisionResponse(t, res, &reviews)
	if reviews.Total != 1 || len(reviews.Rows) != 1 || reviews.Rows[0].Question == "" ||
		reviews.Rows[0].ApplicationMode != "shadow" ||
		len(reviews.Rows[0].ReasonCodes) != 1 || len(reviews.Rows[0].EvidenceRefs) != 2 {
		t.Fatalf("reviews = %+v", reviews)
	}
	res = do(t, srv, http.MethodGet, "/v1/filler/decisions/reviews?limit=10", adminToken, "")
	reviewJSON, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || strings.Contains(string(reviewJSON), `"conflicts":null`) || !strings.Contains(string(reviewJSON), `"conflicts":[]`) {
		t.Fatalf("review arrays are not canonical: %s (%v)", reviewJSON, err)
	}
	for _, path := range []string{
		"/v1/filler/decisions/reviews?limit=101",
		"/v1/filler/decisions/reviews?beforeAt=" + url.QueryEscape(time.Date(2026, 8, 25, 5, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)),
	} {
		res = do(t, srv, http.MethodGet, path, adminToken, "")
		if res.StatusCode != http.StatusUnprocessableEntity {
			_ = res.Body.Close()
			t.Fatalf("invalid page %q = %d, want 422", path, res.StatusCode)
		}
		_ = res.Body.Close()
	}

	res = do(t, srv, http.MethodGet, "/v1/filler/decisions/diagnostics?limit=10", adminToken, "")
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("diagnostics status = %d: %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "provider-secret") || strings.Contains(string(raw), "/private/path") || strings.Contains(string(raw), "attribution") {
		t.Fatalf("diagnostics leaked provider/path detail: %s", raw)
	}
	var diagnostics decisionListBody[diagnosticWire]
	if err := json.Unmarshal(raw, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if diagnostics.Total != 1 || len(diagnostics.Rows) != 1 || diagnostics.Rows[0].Code != "provider_unavailable" ||
		diagnostics.Rows[0].Recovery != "configure_provider" || !diagnostics.Rows[0].Retryable {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}

	res = do(t, srv, http.MethodGet, "/v1/filler/decisions/activity?limit=10", memberToken, "")
	var activity decisionListBody[activityWire]
	decodeDecisionResponse(t, res, &activity)
	if activity.Total != 1 || len(activity.Rows) != 1 || activity.Rows[0].Kind != "review_requested" {
		t.Fatalf("activity = %+v", activity)
	}
}

func TestFillerDecisionActionsRequireAdminAndAreIdempotent(t *testing.T) {
	srv, st := newServer(t)
	seedDecisionAPI(t, st)
	body := `{"actionId":"action-1","kind":"admit","reason":"closing card confirms it"}`

	res := do(t, srv, http.MethodPost, "/v1/filler/decisions/review-1/actions", memberToken, body)
	if res.StatusCode != http.StatusForbidden {
		_ = res.Body.Close()
		t.Fatalf("member action = %d, want 403", res.StatusCode)
	}
	_ = res.Body.Close()
	actions, err := st.ListFillerDecisionActions(t.Context(), fillerdecision.ActionFilter{DecisionID: "review-1", Limit: 10})
	if err != nil || actions.Total != 0 {
		t.Fatalf("member mutation changed store: %+v, %v", actions, err)
	}

	for range 2 {
		res = do(t, srv, http.MethodPost, "/v1/filler/decisions/review-1/actions", adminToken, body)
		if res.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			t.Fatalf("admin action = %d: %s", res.StatusCode, raw)
		}
		_ = res.Body.Close()
	}
	actions, err = st.ListFillerDecisionActions(t.Context(), fillerdecision.ActionFilter{DecisionID: "review-1", Limit: 10})
	if err != nil || actions.Total != 1 || actions.Rows[0].ActorID != "api-token" {
		t.Fatalf("idempotent action audit = %+v, %v", actions, err)
	}

	res = do(t, srv, http.MethodGet, "/v1/filler/decisions/reviews?limit=10", adminToken, "")
	var reviews decisionListBody[reviewWire]
	decodeDecisionResponse(t, res, &reviews)
	if reviews.Total != 0 || len(reviews.Rows) != 0 {
		t.Fatalf("resolved review still actionable: %+v", reviews)
	}

	res = do(t, srv, http.MethodGet, "/v1/filler/decisions/activity?limit=10", memberToken, "")
	var activity decisionListBody[activityWire]
	decodeDecisionResponse(t, res, &activity)
	if activity.Total != 2 || activity.Rows[0].Kind != "review_admit" || activity.Rows[0].ActionID != "action-1" ||
		activity.Rows[1].Kind != "review_requested" {
		t.Fatalf("activity did not distinguish automatic and human events: %+v", activity)
	}
}

func TestFillerDecisionAbandonIsMeasurableWithoutResolvingTheReview(t *testing.T) {
	srv, st := newServer(t)
	seedDecisionAPI(t, st)

	res := do(t, srv, http.MethodPost, "/v1/filler/decisions/review-1/actions", adminToken,
		`{"actionId":"skip-1","kind":"abandon","reason":"skip for now"}`)
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		t.Fatalf("abandon = %d: %s", res.StatusCode, raw)
	}
	_ = res.Body.Close()

	res = do(t, srv, http.MethodGet, "/v1/filler/decisions/reviews?limit=10", adminToken, "")
	var reviews decisionListBody[reviewWire]
	decodeDecisionResponse(t, res, &reviews)
	if reviews.Total != 1 {
		t.Fatalf("abandon resolved the review: %+v", reviews)
	}

	res = do(t, srv, http.MethodGet, "/v1/filler/decisions/activity?limit=10", memberToken, "")
	var activity decisionListBody[activityWire]
	decodeDecisionResponse(t, res, &activity)
	if activity.Total != 2 || activity.Rows[0].Kind != "review_abandoned" {
		t.Fatalf("abandon was not measurable: %+v", activity)
	}

	res = do(t, srv, http.MethodPost, "/v1/filler/decisions/review-1/actions", adminToken,
		`{"actionId":"answer-after-skip","kind":"admit","answer":"yes"}`)
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		t.Fatalf("answer after abandon = %d: %s", res.StatusCode, raw)
	}
	_ = res.Body.Close()

	actions, err := st.ListFillerDecisionActions(t.Context(), fillerdecision.ActionFilter{DecisionID: "review-1", Limit: 10})
	if err != nil || actions.Total != 2 {
		t.Fatalf("review action audit = %+v, %v", actions, err)
	}
}

func TestFillerDecisionProjectionsUseLatestOutcomeWithoutErasingHistory(t *testing.T) {
	srv, st := newServer(t)
	at := time.Date(2026, 8, 25, 5, 0, 0, 0, time.UTC)
	for _, record := range []fillerdecision.Record{
		{
			ID: "hold-old", ClipHash: "clip-recovered", EvidenceHash: "evidence-old",
			EvidenceVersion: "e1", SchemaVersion: 1, PolicyVersion: "p1", TaxonomyVersion: "t1",
			ApplicationMode: fillerdecision.ApplicationModeShadow, CreatedAt: at,
			Result: filleradmission.Result{Hold: &filleradmission.Hold{
				Code: filleradmission.HoldProviderUnavailable, Retryable: true,
			}},
		},
		{
			ID: "admit-new", ClipHash: "clip-recovered", EvidenceHash: "evidence-new",
			EvidenceVersion: "e1", SchemaVersion: 1, PolicyVersion: "p1", TaxonomyVersion: "t1",
			ApplicationMode: fillerdecision.ApplicationModeShadow, CreatedAt: at.Add(time.Second),
			Result: filleradmission.Result{Decision: &filleradmission.Decision{
				Verdict: filleradmission.VerdictAdmit, ReasonCodes: []filleradmission.ReasonCode{filleradmission.ReasonEvidenceSatisfied},
			}},
		},
	} {
		if err := st.PutFillerDecision(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}

	res := do(t, srv, http.MethodGet, "/v1/filler/decisions/diagnostics?limit=10", adminToken, "")
	var diagnostics decisionListBody[diagnosticWire]
	decodeDecisionResponse(t, res, &diagnostics)
	if diagnostics.Total != 0 || len(diagnostics.Rows) != 0 {
		t.Fatalf("recovered hold remained in current diagnostics: %+v", diagnostics)
	}

	res = do(t, srv, http.MethodGet, "/v1/filler/decisions/activity?limit=10", memberToken, "")
	var activity decisionListBody[activityWire]
	decodeDecisionResponse(t, res, &activity)
	if activity.Total != 1 || len(activity.Rows) != 1 || activity.Rows[0].Kind != "automatic_admit" {
		t.Fatalf("latest projection erased or mislabeled semantic history: %+v", activity)
	}
}

func seedDecisionAPI(t *testing.T, st store.Store) {
	t.Helper()
	at := time.Date(2026, 8, 25, 5, 0, 0, 0, time.UTC)
	if err := st.PutFillerDecision(t.Context(), fillerdecision.Record{
		ID: "review-1", ClipHash: "clip-review", EvidenceHash: "evidence-review",
		EvidenceVersion: "e1", SchemaVersion: 1, PolicyVersion: "p1", TaxonomyVersion: "t1",
		ApplicationMode: fillerdecision.ApplicationModeShadow, CreatedAt: at,
		Result: filleradmission.Result{Decision: &filleradmission.Decision{
			Verdict:        filleradmission.VerdictReview,
			ReasonCodes:    []filleradmission.ReasonCode{filleradmission.ReasonConflictRecordingDate},
			EvidenceRefs:   []string{"filename-year", "spoken-year"},
			ReviewQuestion: "Which date describes when this clip was recorded?",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutFillerDecision(t.Context(), fillerdecision.Record{
		ID: "hold-1", ClipHash: "clip-hold", EvidenceHash: "evidence-hold",
		EvidenceVersion: "e1", SchemaVersion: 1, PolicyVersion: "p1", TaxonomyVersion: "t1",
		ApplicationMode: fillerdecision.ApplicationModeShadow, CreatedAt: at.Add(time.Second),
		Result: filleradmission.Result{Hold: &filleradmission.Hold{
			Code:   filleradmission.HoldProviderUnavailable,
			Detail: "provider-secret failed while opening /private/path", Retryable: true,
		}},
	}); err != nil {
		t.Fatal(err)
	}
}

func decodeDecisionResponse(t *testing.T, res *http.Response, target any) {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d: %s", res.StatusCode, raw)
	}
	if err := json.NewDecoder(res.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}
