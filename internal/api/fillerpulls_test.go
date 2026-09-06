package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/store"
)

type pullBody struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Note          string `json:"note"`
	EstimateClips int    `json:"estimateClips"`
	Plan          []struct {
		CandidateID string `json:"candidateId"`
		SourceID    string `json:"sourceId"`
		RemoteID    string `json:"remoteId"`
		URL         string `json:"url"`
		Dropped     bool   `json:"dropped"`
	} `json:"plan"`
	Rejected []struct {
		RemoteID    string `json:"remoteId"`
		Disposition string `json:"disposition"`
	} `json:"rejected"`
}

func decodePull(t *testing.T, res *http.Response) pullBody {
	t.Helper()
	var b pullBody
	if err := json.NewDecoder(res.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	return b
}

func seedSource(t *testing.T, st store.Store, id, uri string, enabled bool) {
	t.Helper()
	ctx := context.Background()
	src := store.NewFillerSource(id, "archive", uri, id, time.Now().UTC())
	if err := st.UpsertFillerSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	if !enabled {
		if err := st.SetFillerSourceEnabled(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProposeFillerPull_UsesOnlyGeographicallyEligibleSources(t *testing.T) {
	srv, st, _ := newFillerServerWithConfig(t, nil, func(key string) string {
		return map[string]string{"filler.home_country": "US", "filler.home_market": "New York"}[key]
	})
	for _, tc := range []struct {
		id, country, market string
	}{
		{"us-wide", "US", ""},
		{"ny-local", "US", "New York"},
		{"california", "US", "California"},
		{"canadian", "CA", ""},
		{"unknown", "", ""},
	} {
		seedSource(t, st, tc.id, "https://archive.org/details/"+tc.id, true)
		if err := st.SetFillerSourceGeography(t.Context(), tc.id,
			filler.Geography{Country: tc.country, Market: tc.market}); err != nil {
			t.Fatal(err)
		}
	}

	res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{}`, adminToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	body := decodePull(t, res)
	got := map[string]bool{}
	for _, row := range body.Plan {
		got[row.SourceID] = true
	}
	if !got["us-wide"] || !got["ny-local"] || len(got) != 2 {
		t.Fatalf("planned sources = %v, want only US-wide and New York local", got)
	}
}

// ⚠ THE safety property. §10's rule is "the machine proposes, a human commits", and this is what
// makes the first half true: proposing writes a row and downloads NOTHING.
func TestProposeFillerPull_DownloadsNothing(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)

	res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{}`, adminToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	body := decodePull(t, res)

	if body.Status != "pending" {
		t.Errorf("status = %q, want pending", body.Status)
	}
	if len(ff.ingested) != 0 {
		t.Errorf("proposing downloaded %v — the gate exists so that this cannot happen", ff.ingested)
	}
	pulls, err := st.ListPulls(context.Background(), filler.PullPending)
	if err != nil || len(pulls) != 1 {
		t.Fatalf("store holds %d pending pulls (%v), want 1", len(pulls), err)
	}
}

func TestProposeFillerPull_BindsExactRankedCandidatesAndEvidence(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)
	ff.Candidates = []filler.AcquisitionCandidate{
		{Identity: filler.RemoteIdentity{Provider: "archive", SourceID: "classic", RemoteID: "low"}, URL: "https://archive.org/details/low", Title: "Low copy", Height: 480},
		{Identity: filler.RemoteIdentity{Provider: "archive", SourceID: "classic", RemoteID: "hd"}, URL: "https://archive.org/details/hd", Title: "HD reel", Height: 1080},
	}

	res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls",
		`{"reason":"Improve Saturday coverage","intent":{"count":1,"minHeight":720}}`, adminToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	body := decodePull(t, res)
	if len(body.Plan) != 1 || body.Plan[0].RemoteID != "hd" || body.Plan[0].URL != "https://archive.org/details/hd" {
		t.Fatalf("selected plan = %+v, want exact HD item", body.Plan)
	}
	if len(body.Rejected) != 1 || body.Rejected[0].RemoteID != "low" || body.Rejected[0].Disposition != "quality_below_floor" {
		t.Fatalf("rejected evidence = %+v", body.Rejected)
	}
	if len(ff.ingested) != 0 {
		t.Fatalf("proposal downloaded %v", ff.ingested)
	}

	approved := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls/"+body.ID+"/approve", `{}`, adminToken)
	if approved.StatusCode != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", approved.StatusCode)
	}
	if len(ff.ingested) != 1 || ff.ingested[0] != "https://archive.org/details/hd" {
		t.Fatalf("approved ingest = %v, want exact candidate URL", ff.ingested)
	}
}

func TestApproveFillerPull_DropsOneCandidateWithoutDroppingItsSource(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)
	ff.Candidates = []filler.AcquisitionCandidate{
		{Identity: filler.RemoteIdentity{Provider: "archive", SourceID: "classic", RemoteID: "one"}, URL: "https://archive.org/details/one"},
		{Identity: filler.RemoteIdentity{Provider: "archive", SourceID: "classic", RemoteID: "two"}, URL: "https://archive.org/details/two"},
	}
	created := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{"intent":{"count":2}}`, adminToken))
	if len(created.Plan) != 2 {
		t.Fatalf("plan = %+v", created.Plan)
	}
	drop := created.Plan[0]
	res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls/"+created.ID+"/approve",
		`{"dropCandidateIds":["`+drop.CandidateID+`"]}`, adminToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if len(ff.ingested) != 1 || ff.ingested[0] == drop.URL {
		t.Fatalf("ingested = %v, should contain only the other candidate", ff.ingested)
	}
}

// The mock writes an empty state for this precondition; it belongs on the server, because a pull
// composed from a switched-off source is one that can never run, and finding that out AFTER a
// human approved it is the worst moment.
func TestProposeFillerPull_RefusedWhenEverySourceIsOff(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", false)

	res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{}`, adminToken)
	if res.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", res.StatusCode)
	}
	if pulls, _ := st.ListPulls(context.Background(), ""); len(pulls) != 0 {
		t.Errorf("wrote %d pulls that could never run", len(pulls))
	}
}

func TestProposeFillerPull_RejectsUnknownIntentVocabulary(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)

	res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls",
		`{"intent":{"roles":["probably_an_ad"]}}`, adminToken)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", res.StatusCode)
	}
	if pulls, _ := st.ListPulls(t.Context(), ""); len(pulls) != 0 {
		t.Fatalf("invalid intent persisted %d pulls", len(pulls))
	}
}

// The commit point. Approving is the ONLY path that enqueues, and it enqueues through the
// existing ingest job rather than a downloader of its own.
func TestApproveFillerPull_IsTheOnlyPathThatDownloads(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)

	created := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{}`, adminToken))
	if len(ff.ingested) != 0 {
		t.Fatalf("downloaded before approval: %v", ff.ingested)
	}

	res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls/"+created.ID+"/approve",
		`{"note":"no local dealers, no PSAs"}`, adminToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	body := decodePull(t, res)
	if body.Status != "approved" {
		t.Errorf("status = %q, want approved", body.Status)
	}
	if body.Note != "no local dealers, no PSAs" {
		t.Errorf("note = %q — the operator's narrowing instruction was lost", body.Note)
	}
	if len(ff.ingested) != 1 || ff.ingested[0] != "https://archive.org/details/classic" {
		t.Errorf("ingested %v, want the source's uri once", ff.ingested)
	}
	if ff.pullID != created.ID || len(ff.pullTargets) != 1 || ff.pullTargets[0].SourceID != "classic" || ff.pullTargets[0].Kind != "archive" || ff.pullTargets[0].RemoteID != "classic" {
		t.Errorf("pull attribution = id %q targets %+v, want approved pull and exact source", ff.pullID, ff.pullTargets)
	}
}

// A retry after the first decision is durable must not enqueue the same downloads twice.
// Atomic concurrency across two simultaneous reads is tracked separately in #955.
func TestApproveFillerPull_CannotBeApprovedTwice(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)
	created := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{}`, adminToken))

	if res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls/"+created.ID+"/approve", `{}`, adminToken); res.StatusCode != http.StatusOK {
		t.Fatalf("first approve: %d", res.StatusCode)
	}
	if res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls/"+created.ID+"/approve", `{}`, adminToken); res.StatusCode != http.StatusConflict {
		t.Errorf("second approve = %d, want 409", res.StatusCode)
	}
	if len(ff.ingested) != 1 {
		t.Errorf("enqueued %d times, want 1 — an approved pull must not re-fetch", len(ff.ingested))
	}
}

func TestApproveFillerPull_RevalidatesCandidateAgainstOtherQueuedWork(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)
	ff.Candidates = []filler.AcquisitionCandidate{{
		Identity: filler.RemoteIdentity{Provider: "archive", SourceID: "classic", RemoteID: "same"},
		URL:      "https://archive.org/details/same", Title: "Same reel",
	}}
	created := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{}`, adminToken))
	pull, err := st.GetPull(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	other := pull
	other.ID = "other-pull"
	other.CreatedAt = other.CreatedAt.Add(time.Second)
	if err := st.UpsertPull(t.Context(), other); err != nil {
		t.Fatal(err)
	}

	res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls/"+created.ID+"/approve", `{}`, adminToken)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", res.StatusCode)
	}
	if len(ff.ingested) != 0 {
		t.Fatalf("revalidation still ingested %v", ff.ingested)
	}
}

// Dropping a row excludes it from the fetch AND is recorded on the pull. The record has to show
// what was proposed as well as what was agreed to, or "we approved this" loses the half that
// matters.
func TestApproveFillerPull_DroppedRowsAreExcludedButRecorded(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "keep", "https://archive.org/details/keep", true)
	seedSource(t, st, "drop", "https://archive.org/details/drop", true)
	created := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{}`, adminToken))
	if len(created.Plan) != 2 {
		t.Fatalf("plan has %d rows, want 2", len(created.Plan))
	}

	body := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls/"+created.ID+"/approve",
		`{"dropSourceIds":["drop"]}`, adminToken))

	if len(ff.ingested) != 1 || ff.ingested[0] != "https://archive.org/details/keep" {
		t.Errorf("ingested %v, want only the kept source", ff.ingested)
	}
	var sawDropped bool
	for _, r := range body.Plan {
		if r.SourceID == "drop" {
			sawDropped = r.Dropped
		}
	}
	if !sawDropped {
		t.Error("the dropped row is gone from the record — the audit must show what was proposed too")
	}
}

// Approving with everything dropped is refused, not recorded as an approval that fetched
// nothing: in the history those two are indistinguishable.
func TestApproveFillerPull_RefusesAnEmptyCommit(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)
	created := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{}`, adminToken))

	res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls/"+created.ID+"/approve",
		`{"dropSourceIds":["classic"]}`, adminToken)
	if res.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", res.StatusCode)
	}
	if len(ff.ingested) != 0 {
		t.Errorf("ingested %v for an empty commit", ff.ingested)
	}
}

// ⚠ Re-checked at the COMMIT point. A source can be switched off while a pull sits in the queue,
// and approving into it would fetch from something the operator turned off.
func TestApproveFillerPull_RefusesASourceDisabledSinceProposal(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)
	created := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{}`, adminToken))

	if err := st.SetFillerSourceEnabled(context.Background(), "classic", false); err != nil {
		t.Fatal(err)
	}

	res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls/"+created.ID+"/approve", `{}`, adminToken)
	if res.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", res.StatusCode)
	}
	if len(ff.ingested) != 0 {
		t.Errorf("fetched from a switched-off source: %v", ff.ingested)
	}
}

// Dismissing records the decision and downloads nothing. The row is KEPT — the history answers
// what was declined, too.
func TestDismissFillerPull_RecordsAndDownloadsNothing(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)
	created := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{}`, adminToken))

	body := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls/"+created.ID+"/dismiss", `{}`, adminToken))
	if body.Status != "dismissed" {
		t.Errorf("status = %q, want dismissed", body.Status)
	}
	if len(ff.ingested) != 0 {
		t.Errorf("dismissing downloaded %v", ff.ingested)
	}
	if pulls, _ := st.ListPulls(context.Background(), filler.PullDismissed); len(pulls) != 1 {
		t.Errorf("dismissed pulls = %d, want 1 — a decided pull is kept, not deleted", len(pulls))
	}
}

// §19 negatives. These routes decide what gets downloaded, so a member must not reach any of
// them — least of all approve.
func TestFillerPullRoutes_RequireAdmin(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)
	created := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{}`, adminToken))

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/filler/pulls"},
		{http.MethodGet, "/v1/filler/pulls"},
		{http.MethodPost, "/v1/filler/pulls/" + created.ID + "/approve"},
		{http.MethodPost, "/v1/filler/pulls/" + created.ID + "/dismiss"},
	} {
		if res := sourceReq(t, tc.method, srv.URL+tc.path, `{}`, ""); res.StatusCode == http.StatusOK {
			t.Errorf("%s %s succeeded with no credential", tc.method, tc.path)
		}
	}
	if len(ff.ingested) != 0 {
		t.Errorf("an unauthenticated caller caused a download: %v", ff.ingested)
	}
}
