package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/api"
	"github.com/loomarr/loomarr/internal/binder"
	"github.com/loomarr/loomarr/internal/events"
	"github.com/loomarr/loomarr/internal/proposalworkflow"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/quality"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
	"github.com/loomarr/loomarr/internal/suggest"
	"github.com/loomarr/loomarr/internal/testkit"
)

// fakeSuggest records the LAST intent it was asked to run, so a test can assert the
// WHOLE intent survives the wire — the hand-mirrored body this replaced had silently
// dropped RuntimeTgt, and nothing caught it.
type fakeSuggest struct {
	submits   int
	refines   int
	lastJobID string
	last      suggest.Intent
}

func (f *fakeSuggest) Submit(_ context.Context, intent suggest.Intent, _ string) (string, error) {
	f.submits++
	f.last = intent
	return "job-1", nil
}

func (f *fakeSuggest) Refine(_ context.Context, jobID string, intent suggest.Intent) (string, error) {
	f.refines++
	f.lastJobID = jobID
	f.last = intent
	return jobID, nil // Refine re-runs the same job, so it returns the job id it was given
}

func newSuggestServer(t *testing.T) (*httptest.Server, store.Store, *fakeSuggest) {
	return newSuggestServerWithSettings(t, nil)
}

func newSuggestServerWithSettings(t *testing.T, settings api.SettingsService) (*httptest.Server, store.Store, *fakeSuggest) {
	return newSuggestServerWithSettingsAndDecisionQuality(t, settings, nil)
}

func newSuggestServerWithSettingsAndDecisionQuality(
	t *testing.T,
	settings api.SettingsService,
	decisionQuality api.ProposalDecisionQuality,
) (*httptest.Server, store.Store, *fakeSuggest) {
	t.Helper()
	st := openTestStore(t, t.TempDir()+"/s.db")
	t.Cleanup(func() { _ = st.Close() })
	fs := &fakeSuggest{}
	search := &testkit.SearchService[api.SearchRequest, api.SearchCandidate]{Results: []api.SearchCandidate{{
		MediaType: "movie", TMDBID: 603, Name: "The Matrix", InLibrary: true,
	}}}
	log := slog.New(slog.DiscardHandler)
	chBinder := binder.New(st, nil, nil, log)
	workflow := proposalworkflow.New(st, func() string { return "test-proposal-job" }, time.Now)
	h := api.Router(log, api.Options{
		Store:            st,
		Auth:             testAuthorizer{},
		Log:              log,
		Suggest:          fs,
		Search:           search,
		Events:           events.NewBus(),
		ProposalWorkflow: workflow,
		DecisionQuality:  decisionQuality,
		// No Reconciler wired here (channels isn't under test) — mirrors the
		// composition root's nil-guard: the bind still creates/patches the
		// channel row and just skips the immediate Tunarr reconcile push.
		Approver: suggest.NewApprover(st, chBinder, time.Now),
		Binder:   chBinder,
		Settings: settings,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, st, fs
}

// seedProposal writes a submitted proposal with one acquisition (Speed).
func seedProposal(t *testing.T, st store.Store, id string) {
	seedProposalAt(t, st, id, time.Time{})
}

func seedProposalAt(t *testing.T, st store.Store, id string, created time.Time) {
	t.Helper()
	body := `{"acquisitions":[{"mediaType":"movie","tmdbId":100,"name":"Speed","year":1994}],"trace":{"version":1,"candidates":[{"key":"movie:tmdb:100","name":"Speed","ownership":"acquisition","rank":{"tieKey":"movie:tmdb:100"},"disposition":"selected","reason":"selected"}],"surfacedTotal":1,"recordedTotal":1,"truncated":false}}`
	err := st.CreateProposal(context.Background(), store.Proposal{
		ID: id, JobID: "job-1", Status: "submitted", CreatedBy: "alice", ProposalJSON: body,
		CreatedAt: created,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestApprove_InLibraryPickBecomesAvailable is the regression test for the smoke
// bug: approval only enqueued acquisitions, so an in-library lineup pick never
// became an `available` title Record and the scheduler could not place it (§8
// "the approved lineup feeds the scheduler"). Approval must create an available
// Record (with the library item id) for each in-library pick.
func TestApprove_InLibraryPickBecomesAvailable(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	// A proposal whose lineup has one in-library pick (The Matrix) and no acquisitions.
	body := `{"lineup":[{"mediaType":"movie","tmdbId":603,"name":"The Matrix","year":1999,"inLibrary":true,"libraryItemId":"641641"}],"acquisitions":[]}`
	if err := st.CreateProposal(context.Background(), store.Proposal{
		ID: "p-lib", JobID: "job-lib", Status: "submitted", ProposalJSON: body,
	}); err != nil {
		t.Fatal(err)
	}

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-lib/approve", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve → %d, want 200", resp.StatusCode)
	}

	// The in-library pick is now an `available` title the scheduler can resolve.
	avail, _ := st.ListTitlesByState(context.Background(), provision.Available)
	if len(avail) != 1 {
		t.Fatalf("approve created %d available titles, want 1 (the in-library pick)", len(avail))
	}
	if got := string(avail[0].Key); got != "movie:tmdb:603" {
		t.Errorf("available title key = %q, want movie:tmdb:603", got)
	}
	if avail[0].LibraryID != "641641" {
		t.Errorf("available title libraryID = %q, want 641641 (needed to play + resolve duration)", avail[0].LibraryID)
	}
}

func TestSubmit_AnyAuthenticatedUser(t *testing.T) {
	srv, _, fs := newSuggestServer(t)
	resp := do(t, srv, http.MethodPost, "/v1/proposals", adminToken, `{"description":"90s action"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit → %d, want 200", resp.StatusCode)
	}
	if fs.submits != 1 {
		t.Errorf("submit not invoked: %d", fs.submits)
	}
}

func TestSubmit_UnconfiguredAIFailsBeforeCreatingJob(t *testing.T) {
	srv, _, fs := newSuggestServerWithSettings(t, &fakeSettings{})
	resp := do(t, srv, http.MethodPost, "/v1/proposals", adminToken, `{"description":"90s action"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("submit → %d, want 409 feature-not-configured", resp.StatusCode)
	}
	if fs.submits != 0 {
		t.Fatalf("unconfigured submission created %d jobs, want none", fs.submits)
	}
	defer func() { _ = resp.Body.Close() }()
	var problem struct {
		Type   string `json:"type"`
		Detail string `json:"detail"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Type != "feature_not_configured" || !strings.Contains(problem.Detail, "tool-capable lineup model") {
		t.Errorf("problem = %+v, want actionable feature-not-configured guidance", problem)
	}
}

func TestGetProposalJobClassifiesNoGroundedTitlesWithoutLeakingDiagnostic(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	intent := `{"description":"Classic Simpson Episodes"}`
	now := time.Now()
	err := st.CreateJob(context.Background(), store.Job{
		ID: "job-grounding", Kind: "suggest", Status: "queued", IntentJSON: intent,
		IntentHash: "hash", CreatedBy: "alice", WorkflowVersion: store.ProposalWorkflowVersion,
		Deadline: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimDueJobs(context.Background(), now, time.Minute, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim job: %+v, %v", claimed, err)
	}
	if err := st.CommitSuggestionFailure(context.Background(), "job-grounding", claimed[0].Attempts,
		"suggester: no grounded titles found for this intent at https://private.invalid",
		suggest.FailureCodeNoGroundedTitles, `{"version":1,"terminal":"selection_empty"}`, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	resp := do(t, srv, http.MethodGet, "/v1/proposal-jobs/job-grounding", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get proposal job → %d, want 200", resp.StatusCode)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private.invalid") {
		t.Fatalf("response leaked private diagnostic: %s", body)
	}
	var got struct {
		Intent  suggest.Intent `json:"intent"`
		Failure struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"failure"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Intent.Description != "Classic Simpson Episodes" {
		t.Errorf("intent = %q, want preserved request", got.Intent.Description)
	}
	if got.Failure.Code != suggest.FailureCodeNoGroundedTitles || !strings.Contains(got.Failure.Message, "No grounded titles") {
		t.Errorf("failure = %+v, want grounded-title guidance", got.Failure)
	}
}

func TestGetProposalJobRequiresOwnerOrAdmin(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	if err := st.CreateJob(context.Background(), store.Job{
		ID: "job-owned", Kind: "suggest", Status: "queued", IntentJSON: `{"description":"Comedy"}`,
		IntentHash: "hash", CreatedBy: "alice", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	if resp := do(t, srv, http.MethodGet, "/v1/proposal-jobs/job-owned", memberToken, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unscoped member get → %d, want 403", resp.StatusCode)
	}
	if resp := do(t, srv, http.MethodGet, "/v1/proposal-jobs/job-owned", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous get → %d, want 401", resp.StatusCode)
	}
}

func TestGetProposalProjectsPersistedDecisionTraceForAuthorizedReader(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	body := `{"trace":{"version":1,"surfacedTotal":1,"recordedTotal":1,"truncated":false,"candidates":[{"key":"movie:tmdb:1","ownership":"library","disposition":"selected","reason":"selected"}]}}`
	if err := st.CreateProposal(context.Background(), store.Proposal{ID: "trace-proposal", JobID: "trace-job", Status: "submitted", CreatedBy: "alice", ProposalJSON: body}); err != nil {
		t.Fatal(err)
	}
	resp := do(t, srv, http.MethodGet, "/v1/proposals/trace-proposal", adminToken, "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET proposal = %d, want 200", resp.StatusCode)
	}
	var got api.ProposalDTO
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Proposal.Trace.Version != 1 || len(got.Proposal.Trace.Candidates) != 1 {
		t.Fatalf("proposal trace = %+v", got.Proposal.Trace)
	}
}

// THE APPROVAL GATE (§19): approve requires admin. A member (anonymous here /
// wrong token) gets 403 — and crucially, no title is enqueued.
func TestApprove_RequiresAdmin_NothingEnqueued(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p1")

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p1/approve", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("member approve → %d, want 401", resp.StatusCode)
	}
	// The approval-gate guarantee: NOTHING unapproved reached /v1/titles.
	wanted, _ := st.ListTitlesByState(context.Background(), "wanted")
	if len(wanted) != 0 {
		t.Fatalf("a denied approval still enqueued %d titles — approval gate breached", len(wanted))
	}
	// The proposal is untouched.
	p, _ := st.GetProposal(context.Background(), "p1")
	if p.Status != "submitted" {
		t.Errorf("proposal status changed on a forbidden approve: %s", p.Status)
	}
}

// Admin approve enqueues the acquisitions as wanted titles (the ONLY path from a
// proposal to /v1/titles) and flips the proposal to approved.
func TestApprove_Admin_EnqueuesAcquisitions(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p1")

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p1/approve", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin approve → %d, want 200", resp.StatusCode)
	}
	var body struct {
		Status   string
		Enqueued int
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Status != "approved" || body.Enqueued != 1 {
		t.Errorf("approve body = %+v, want approved/1", body)
	}
	// Speed (tmdb 100) is now a wanted title.
	rec, err := st.GetTitle(context.Background(), "movie:tmdb:100")
	if err != nil {
		t.Fatalf("acquisition not enqueued: %v", err)
	}
	if rec.State != "wanted" {
		t.Errorf("enqueued title state = %s, want wanted", rec.State)
	}
	// The wanted title must be DUE (deadline set, not zero) — else the reconciler's
	// ClaimDueTitles (deadline <= now AND deadline > 0) never claims it and it's
	// never submitted to Seerr (live-smoke bug: approved acquisitions sat forever).
	if rec.Deadline.IsZero() {
		t.Error("enqueued acquisition has a zero deadline — reconciler will never claim it")
	}
	// The proposal is approved + audited.
	p, _ := st.GetProposal(context.Background(), "p1")
	if p.Status != "approved" {
		t.Errorf("proposal status = %s, want approved", p.Status)
	}
}

// Approve is idempotent-ish: re-approving an already-approved proposal 409s
// (can't double-enqueue).
func TestApprove_AlreadyApproved409(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p1")
	_ = do(t, srv, http.MethodPost, "/v1/proposals/p1/approve", adminToken, "")
	resp := do(t, srv, http.MethodPost, "/v1/proposals/p1/approve", adminToken, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-approve → %d, want 409", resp.StatusCode)
	}
}

func TestDeny_RequiresAdmin(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p1")
	resp := do(t, srv, http.MethodPost, "/v1/proposals/p1/deny", "", `{"reason":"no"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("member deny → %d, want 401", resp.StatusCode)
	}
}

func TestDeny_AlreadyApproved409AndPreservesAudit(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p1")
	approved := do(t, srv, http.MethodPost, "/v1/proposals/p1/approve", adminToken, "")
	if approved.StatusCode != http.StatusOK {
		t.Fatalf("approve -> %d", approved.StatusCode)
	}
	before, err := st.GetProposal(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p1/deny", adminToken, `{"reason":"too late"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("deny approved proposal -> %d, want 409", resp.StatusCode)
	}
	after, err := st.GetProposal(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "approved" || after.ApprovedBy != before.ApprovedBy || after.DenyReason != before.DenyReason {
		t.Errorf("denial overwrote approval: before=%+v after=%+v", before, after)
	}
}

func TestDeny_StampsDecisionUpdateTime(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p1")
	before, err := st.GetProposal(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	resp := do(t, srv, http.MethodPost, "/v1/proposals/p1/deny", adminToken, `{"reason":"not a fit"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deny -> %d", resp.StatusCode)
	}
	after, err := st.GetProposal(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "denied" || after.DenyReason != "not a fit" || !after.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("denied proposal = %+v; before updatedAt=%v", after, before.UpdatedAt)
	}
}

func TestDeny_RecordsOnlyCommittedDecisionAsWorkflowOutcome(t *testing.T) {
	qualitySink := &testkit.QualityRecorder{Err: errors.New("ledger unavailable")}
	decisionQuality := quality.NewProposalDecisionRecorder(qualitySink, slog.New(slog.DiscardHandler))
	srv, st, _ := newSuggestServerWithSettingsAndDecisionQuality(t, nil, decisionQuality)
	seedProposal(t, st, "p1")

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p1/deny", adminToken, `{"reason":"not a fit"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deny -> %d", resp.StatusCode)
	}
	observations := qualitySink.Observations()
	if len(observations) != 1 || observations[0].Stage != quality.StageApproval ||
		observations[0].Outcome != quality.OutcomeDeclined || observations[0].At.IsZero() {
		t.Fatalf("denial observations = %+v", observations)
	}

	resp = do(t, srv, http.MethodPost, "/v1/proposals/p1/deny", adminToken, `{"reason":"again"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("repeat deny -> %d, want 409", resp.StatusCode)
	}
	if observations = qualitySink.Observations(); len(observations) != 1 {
		t.Fatalf("refused denial recorded an outcome: %+v", observations)
	}
}

func TestListProposals_ApprovalQueue(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p1")
	seedProposal(t, st, "p2")
	resp := do(t, srv, http.MethodGet, "/v1/proposals?status=submitted", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list → %d", resp.StatusCode)
	}
	var body struct {
		Proposals []struct{ ID, Status string }
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Proposals) != 2 {
		t.Errorf("approval queue = %d, want 2", len(body.Proposals))
	}
}

func TestSearch_AnyAuthenticatedUser(t *testing.T) {
	srv, _, _ := newSuggestServer(t)
	resp := do(t, srv, http.MethodGet, "/v1/search?q=matrix", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search → %d", resp.StatusCode)
	}
	var body struct {
		Candidates []struct {
			Name      string
			InLibrary bool
		}
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Candidates) != 1 || body.Candidates[0].Name != "The Matrix" {
		t.Errorf("search results = %+v", body.Candidates)
	}
}

func TestSearch_RequiresQuery(t *testing.T) {
	srv, _, _ := newSuggestServer(t)
	resp := do(t, srv, http.MethodGet, "/v1/search", adminToken, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("search without q → %d, want 400", resp.StatusCode)
	}
}

func TestEvents_RequiresAuth(t *testing.T) {
	srv, _, _ := newSuggestServer(t)
	// Anonymous → 401.
	resp := do(t, srv, http.MethodGet, "/v1/events", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous /v1/events → %d, want 401", resp.StatusCode)
	}
}

// The WHOLE intent must survive the wire. This is the regression test for a real gap:
// the submit body used to be a hand-mirrored struct that had drifted from
// suggest.Intent — it omitted RuntimeTgt, so `runtimeTargetMin` was unreachable even
// though the suggester feeds it to the LLM prompt and the scorer, and §13 lists a
// runtime target among the constraints a user may set. Typing the body from the domain
// fixed it; this keeps any future Intent field from going missing the same way.
func TestSubmit_CarriesTheWholeIntent(t *testing.T) {
	srv, _, fs := newSuggestServer(t)
	body := `{"description":"90s action movies","era":"1990s","tone":"high-energy",
	          "runtimeTargetMin":180,"maxAcquisitions":7,
	          "mustInclude":["Speed"],"mustExclude":["Cats"]}`

	if resp := do(t, srv, http.MethodPost, "/v1/proposals", adminToken, body); resp.StatusCode != http.StatusOK {
		t.Fatalf("submit → %d, want 200", resp.StatusCode)
	}

	got := fs.last
	if got.Description != "90s action movies" || got.Era != "1990s" || got.Tone != "high-energy" {
		t.Errorf("intent basics lost: %+v", got)
	}
	if got.RuntimeTgt != 180 {
		t.Errorf("runtimeTargetMin = %d, want 180 — the field the old mirror dropped", got.RuntimeTgt)
	}
	if got.MaxAcquire != 7 {
		t.Errorf("maxAcquisitions = %d, want 7", got.MaxAcquire)
	}
	if len(got.MustInclude) != 1 || got.MustInclude[0] != "Speed" {
		t.Errorf("mustInclude = %v, want [Speed]", got.MustInclude)
	}
	if len(got.MustExclude) != 1 || got.MustExclude[0] != "Cats" {
		t.Errorf("mustExclude = %v, want [Cats]", got.MustExclude)
	}
}

// §7: approve → "enqueue acquisitions + create/patch channel". Only the first half was
// ever implemented, which made Loomarr's whole purpose unreachable from the UI — an
// operator could describe a channel, get a grounded lineup, approve it, and no channel
// ever appeared. There is no create-a-channel screen because THIS is meant to be the
// path (§13), so nothing else could close the gap.
//
// Found by the maintainer smoke walking the flow as an operator. The mocked e2e could
// not catch it: it asserts the acquisition is enqueued, which is exactly what the code
// did do.
func TestApprove_CreatesTheChannelTheIntentDescribes(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	body := `{"intent":{"description":"90s Saturday morning cartoons for the kids"},` +
		`"lineup":[{"mediaType":"movie","tmdbId":603,"name":"The Matrix","year":1999,` +
		`"inLibrary":true,"libraryItemId":"641641"}],"acquisitions":[]}`
	if err := st.CreateProposal(context.Background(), store.Proposal{
		ID: "p-ch", JobID: "job-ch", Status: "submitted", ProposalJSON: body,
	}); err != nil {
		t.Fatal(err)
	}

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-ch/approve", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve → %d, want 200", resp.StatusCode)
	}
	var out struct {
		ChannelID string `json:"channelId"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.ChannelID == "" {
		t.Fatal("approve returned no channelId — the UI has nothing to navigate to")
	}

	ch, err := st.GetChannel(context.Background(), out.ChannelID)
	if err != nil {
		t.Fatalf("approve reported a channel that does not exist: %v", err)
	}
	// Bound to the intent, so re-approval can find it again.
	if ch.IntentRef != "job-ch" {
		t.Errorf("channel IntentRef = %q, want job-ch", ch.IntentRef)
	}
	// Named from what the operator typed, not "channel-1".
	if !strings.Contains(ch.Name, "Saturday morning") {
		t.Errorf("channel name = %q, want it derived from the intent", ch.Name)
	}
	// Numbered without the operator having to choose (§7).
	if ch.Number < 1 {
		t.Errorf("channel number = %d, want an auto-allocated positive number", ch.Number)
	}
	// The approved lineup rides onto the channel — a channel with no lineup is dead air,
	// which is the failure §9 exists to prevent.
	if len(ch.Lineup) == 0 {
		t.Error("channel created with an EMPTY lineup — it would play nothing")
	}
}

func TestApprove_PersistsProposalEpisodeSelectionToChannelLineup(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	body := `{"intent":{"description":"Classic Simpsons"},` +
		`"lineup":[{"mediaType":"series","tmdbId":456,"name":"The Simpsons",` +
		`"inLibrary":true,"libraryItemId":"lib-simpsons",` +
		`"episodeSelection":{"mode":"highlights"}}],"acquisitions":[]}`
	if err := st.CreateProposal(context.Background(), store.Proposal{
		ID: "p-episodes", JobID: "job-episodes", Status: "submitted", ProposalJSON: body,
	}); err != nil {
		t.Fatal(err)
	}

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-episodes/approve", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve → %d, want 200", resp.StatusCode)
	}
	var out struct {
		ChannelID string `json:"channelId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	ch, err := st.GetChannel(context.Background(), out.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Lineup) != 1 || ch.Lineup[0].EpisodeSelection.Mode != schedule.EpisodeHighlights {
		t.Fatalf("approved lineup selection = %+v, want highlights", ch.Lineup)
	}
}

func TestApprove_RestampsSearchAddedSeriesFromOriginalIntent(t *testing.T) {
	tests := []struct {
		name       string
		intent     string
		proposalID string
		seriesID   int
		want       schedule.EpisodeSelection
	}{
		{
			name: "classic", intent: "Classic highlights from The Simpsons",
			proposalID: "p-added-classic", seriesID: 456,
			want: schedule.EpisodeSelection{Mode: schedule.EpisodeHighlights},
		},
		{
			name: "named holiday", intent: "Christmas episodes from Bob's Burgers",
			proposalID: "p-added-holiday", seriesID: 32726,
			want: schedule.EpisodeSelection{Mode: schedule.EpisodeHoliday, Holidays: []string{"christmas"}},
		},
		{
			name: "complete", intent: "Watch The Simpsons chronologically from the beginning",
			proposalID: "p-added-complete", seriesID: 456,
			want: schedule.EpisodeSelection{Mode: schedule.EpisodeComplete},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, st, _ := newSuggestServer(t)
			body := fmt.Sprintf(`{"intent":{"description":%q},"lineup":[`+
				`{"mediaType":"movie","tmdbId":603,"name":"The Matrix","inLibrary":true,"libraryItemId":"matrix"}],`+
				`"acquisitions":[]}`, tt.intent)
			if err := st.CreateProposal(context.Background(), store.Proposal{
				ID: tt.proposalID, JobID: "job-" + tt.proposalID, Status: "submitted", ProposalJSON: body,
			}); err != nil {
				t.Fatal(err)
			}
			previewResp := do(t, srv, http.MethodGet, "/v1/proposals/"+tt.proposalID, adminToken, "")
			if previewResp.StatusCode != http.StatusOK {
				t.Fatalf("get proposal → %d, want 200", previewResp.StatusCode)
			}
			var preview struct {
				EpisodeSelectionPreview schedule.EpisodeSelection `json:"episodeSelectionPreview"`
			}
			if err := json.NewDecoder(previewResp.Body).Decode(&preview); err != nil {
				t.Fatal(err)
			}
			if got := preview.EpisodeSelectionPreview; got.Mode != tt.want.Mode ||
				strings.Join(got.Holidays, ",") != strings.Join(tt.want.Holidays, ",") {
				t.Fatalf("pre-approval selection preview = %+v, want %+v", got, tt.want)
			}
			edit := fmt.Sprintf(`{"add":[{"mediaType":"series","tmdbId":%d,"name":"Added Series",`+
				`"inLibrary":false}]}`, tt.seriesID)
			resp := do(t, srv, http.MethodPost, "/v1/proposals/"+tt.proposalID+"/approve", adminToken, edit)
			if resp.StatusCode != http.StatusOK {
				b, _ := io.ReadAll(resp.Body)
				t.Fatalf("approve → %d, want 200: %s", resp.StatusCode, b)
			}
			var out struct {
				ChannelID string `json:"channelId"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			ch, err := st.GetChannel(context.Background(), out.ChannelID)
			if err != nil {
				t.Fatal(err)
			}
			var got schedule.EpisodeSelection
			for _, item := range ch.Lineup {
				if item.Key == provision.Key(fmt.Sprintf("series:tmdb:%d", tt.seriesID)) {
					got = item.EpisodeSelection
				}
			}
			if got.Mode != tt.want.Mode || strings.Join(got.Holidays, ",") != strings.Join(tt.want.Holidays, ",") {
				t.Fatalf("added series selection = %+v, want %+v", got, tt.want)
			}
			approved, err := st.GetProposal(context.Background(), tt.proposalID)
			if err != nil {
				t.Fatal(err)
			}
			var persisted suggest.Proposal
			if err := json.Unmarshal([]byte(approved.ProposalJSON), &persisted); err != nil {
				t.Fatal(err)
			}
			if len(persisted.Acquisitions) != 1 ||
				persisted.Acquisitions[0].EpisodeSelection.Mode != tt.want.Mode ||
				strings.Join(persisted.Acquisitions[0].EpisodeSelection.Holidays, ",") != strings.Join(tt.want.Holidays, ",") {
				t.Fatalf("persisted added series selection = %+v, want %+v", persisted.Acquisitions, tt.want)
			}
		})
	}
}

func TestApprove_ReplacesCraftedSeriesModeFromOriginalIntent(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	body := `{"intent":{"description":"Watch The Simpsons chronologically from start to finish"},` +
		`"lineup":[{"mediaType":"movie","tmdbId":603,"name":"The Matrix",` +
		`"inLibrary":true,"libraryItemId":"matrix"}],` +
		`"acquisitions":[]}`
	if err := st.CreateProposal(context.Background(), store.Proposal{
		ID: "p-crafted-selection", JobID: "job-crafted-selection", Status: "submitted", ProposalJSON: body,
	}); err != nil {
		t.Fatal(err)
	}

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-crafted-selection/approve", adminToken,
		`{"add":[{"mediaType":"series","tmdbId":456,"name":"The Simpsons","inLibrary":false,`+
			`"episodeSelection":{"mode":"holiday","holidays":["christmas"]}}]}`)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("approve → %d, want 200: %s", resp.StatusCode, b)
	}
	var out struct {
		ChannelID string `json:"channelId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	ch, err := st.GetChannel(context.Background(), out.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Lineup) != 2 {
		t.Fatalf("approved lineup = %+v, want series and unchanged movie", ch.Lineup)
	}
	var seriesFound, movieFound bool
	for _, item := range ch.Lineup {
		switch item.Key {
		case "series:tmdb:456":
			seriesFound = true
			if got := item.EpisodeSelection; got.Mode != schedule.EpisodeComplete || len(got.Holidays) != 0 {
				t.Fatalf("crafted series selection survived approval: %+v", got)
			}
		case "movie:tmdb:603":
			movieFound = true
			if got := item.EpisodeSelection; got.Mode != "" || len(got.Holidays) != 0 {
				t.Fatalf("movie episode selection changed: %+v", got)
			}
		}
	}
	if !seriesFound || !movieFound {
		t.Fatalf("approved lineup keys = %+v, want series and movie", ch.Lineup)
	}
}

func TestApprove_RestampsAlternatesFromOriginalIntent(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	body := `{"intent":{"description":"Classic highlights"},` +
		`"lineup":[{"mediaType":"movie","tmdbId":603,"name":"The Matrix",` +
		`"inLibrary":true,"libraryItemId":"matrix"}],"acquisitions":[],` +
		`"alternates":[` +
		`{"mediaType":"series","tmdbId":456,"name":"Missing Series Mode","inLibrary":false},` +
		`{"mediaType":"series","tmdbId":32726,"name":"Crafted Series Mode","inLibrary":false,` +
		`"episodeSelection":{"mode":"holiday","holidays":["christmas"]}},` +
		`{"mediaType":"movie","tmdbId":100,"name":"Crafted Movie Mode","inLibrary":false,` +
		`"episodeSelection":{"mode":"highlights"}}]}`
	if err := st.CreateProposal(context.Background(), store.Proposal{
		ID: "p-alternate-selection", JobID: "job-alternate-selection", Status: "submitted", ProposalJSON: body,
	}); err != nil {
		t.Fatal(err)
	}

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-alternate-selection/approve", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("approve → %d, want 200: %s", resp.StatusCode, b)
	}
	approved, err := st.GetProposal(context.Background(), "p-alternate-selection")
	if err != nil {
		t.Fatal(err)
	}
	var persisted suggest.Proposal
	if err := json.Unmarshal([]byte(approved.ProposalJSON), &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Alternates) != 3 {
		t.Fatalf("persisted alternates = %+v, want three", persisted.Alternates)
	}
	for _, item := range persisted.Alternates[:2] {
		if got := item.EpisodeSelection; got.Mode != schedule.EpisodeHighlights || len(got.Holidays) != 0 {
			t.Fatalf("series alternate %q selection = %+v, want highlights", item.Name, got)
		}
	}
	if got := persisted.Alternates[2].EpisodeSelection; got.Mode != "" || len(got.Holidays) != 0 {
		t.Fatalf("movie alternate retained selector: %+v", got)
	}
}

// A new channel seeds its FILLER era from its PROGRAM scope era (§10 default-from-theme),
// so a "90s" channel gets 90s ads out of the box without the operator touching filler.
func TestApprove_SeedsFillerEraFromScopeEra(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	// The proposal's policy scopes programs to the 1990s — the filler era should follow.
	body := `{"intent":{"description":"90s action"},"policy":{"scope":{"era":{"from":1990,"to":1999}}},` +
		`"lineup":[{"mediaType":"movie","tmdbId":603,"name":"The Matrix","year":1999,` +
		`"inLibrary":true,"libraryItemId":"641641"}],"acquisitions":[]}`
	if err := st.CreateProposal(context.Background(), store.Proposal{
		ID: "p-era", JobID: "job-era", Status: "submitted", ProposalJSON: body,
	}); err != nil {
		t.Fatal(err)
	}
	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-era/approve", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve → %d, want 200", resp.StatusCode)
	}
	var out struct {
		ChannelID string `json:"channelId"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)

	ch, _ := st.GetChannel(context.Background(), out.ChannelID)
	if ch.Policy.Filler == nil || ch.Policy.Filler.Era == nil {
		t.Fatal("new channel got no filler era seeded from its scope era")
	}
	if ch.Policy.Filler.Era.From != 1990 || ch.Policy.Filler.Era.To != 1999 {
		t.Errorf("filler era = %+v, want the scope era 1990–1999", ch.Policy.Filler.Era)
	}
}

// "create/patch" (§7) means re-approving the same intent must not mint a second
// channel — and must not clobber the fields the OPERATOR owns. Name and number are
// ordinary editable fields; silently reverting an edit on re-approve is data loss.
func TestApprove_ReApprovalPatchesRatherThanDuplicating(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	body := `{"intent":{"description":"90s cartoons"},"lineup":[{"mediaType":"movie",` +
		`"tmdbId":603,"name":"The Matrix","year":1999,"inLibrary":true,"libraryItemId":"641641"}],` +
		`"acquisitions":[]}`
	seed := func(id, job string, created time.Time) {
		if err := st.CreateProposal(context.Background(), store.Proposal{
			ID: id, JobID: job, Status: "submitted", ProposalJSON: body, CreatedAt: created,
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("p-a", "job-same", base)
	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-a/approve", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first approve → %d", resp.StatusCode)
	}
	var first struct {
		ChannelID string `json:"channelId"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&first)

	// The operator renames and renumbers it, as §7 says they may.
	ch, _ := st.GetChannel(context.Background(), first.ChannelID)
	ch.Name, ch.Number = "Cartoon Corner", 42
	if _, err := st.SaveChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}

	// A second proposal for the SAME intent (a re-run of that job) is approved.
	seed("p-b", "job-same", base.Add(time.Hour))
	resp = do(t, srv, http.MethodPost, "/v1/proposals/p-b/approve", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second approve → %d", resp.StatusCode)
	}
	var second struct {
		ChannelID string `json:"channelId"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&second)

	if second.ChannelID != first.ChannelID {
		t.Errorf("re-approval made a NEW channel (%s vs %s) — the guide would show two",
			second.ChannelID, first.ChannelID)
	}
	all, _ := st.ListChannels(context.Background())
	if len(all) != 1 {
		t.Errorf("channel count = %d, want 1", len(all))
	}
	again, _ := st.GetChannel(context.Background(), first.ChannelID)
	if again.Name != "Cartoon Corner" || again.Number != 42 {
		t.Errorf("re-approval clobbered operator edits: name=%q number=%d", again.Name, again.Number)
	}
}

// TestRefine_NewerApprovalPatchesChannel is the binding regression for the refine flow (§7).
// A refine re-runs the channel's OWN job, so over its life a single job accumulates
// several APPROVED proposals — the original lineup and each refined one. The channel
// binds on IntentRef (== JobID), and approvedProposalForJob must resolve the *newest*
// approved proposal, not the first ever approved. If it picked the original, approving
// a refine would silently re-apply the pre-refine lineup — the user's change lost.
//
// This is distinct from TestApprove_ReApprovalPatchesRatherThanDuplicating (which
// approves sequentially and never leaves two APPROVED rows to choose between): here
// BOTH proposals are approved and coexist, so the test actually exercises the
// created_at DESC ordering that the binding leans on.
func TestRefine_NewerApprovalPatchesChannel(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	ctx := context.Background()

	// Both proposals belong to the SAME job — as a refine re-run would produce.
	const job = "job-refine"
	mk := func(id, tmdb, name string, created time.Time) {
		body := `{"intent":{"description":"90s action"},"lineup":[{"mediaType":"movie",` +
			`"tmdbId":` + tmdb + `,"name":"` + name + `","year":1999,"inLibrary":true,` +
			`"libraryItemId":"lib-` + tmdb + `"}],"acquisitions":[]}`
		if err := st.CreateProposal(ctx, store.Proposal{
			ID: id, JobID: job, Status: "submitted", ProposalJSON: body, CreatedAt: created,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Explicit timestamps an hour apart so "newest" is a property of the data, not of
	// same-second scheduling luck (created_at persists as epoch seconds).
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk("p-orig", "603", "The Matrix", base)                 // original lineup
	mk("p-refined", "106", "Predator", base.Add(time.Hour)) // the refine result (newer)

	// Approve the ORIGINAL first → binds a channel to the job with The Matrix.
	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-orig/approve", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve original → %d, want 200", resp.StatusCode)
	}
	var first struct {
		ChannelID string `json:"channelId"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&first)
	if first.ChannelID == "" {
		t.Fatal("original approve returned no channelId")
	}

	// Now approve the REFINED proposal. Same job → must patch the SAME channel, and the
	// lineup must become the refined one (Predator), not stay the original (Matrix).
	resp = do(t, srv, http.MethodPost, "/v1/proposals/p-refined/approve", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve refined → %d, want 200", resp.StatusCode)
	}
	var second struct {
		ChannelID string `json:"channelId"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&second)

	// Same channel — a refine shapes the existing channel, it does not mint a new one.
	if second.ChannelID != first.ChannelID {
		t.Fatalf("refine made a NEW channel (%s vs %s) — it must patch the existing one",
			second.ChannelID, first.ChannelID)
	}
	if all, _ := st.ListChannels(ctx); len(all) != 1 {
		t.Errorf("channel count = %d, want 1 (refine must not duplicate)", len(all))
	}

	ch, err := st.GetChannel(ctx, first.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	// Binding preserved so a FURTHER refine still finds this channel.
	if ch.IntentRef != job {
		t.Errorf("IntentRef = %q, want %q (binding must survive refine)", ch.IntentRef, job)
	}
	// The load-bearing assertion: the lineup is the newer approved proposal's, so the
	// refine's change actually took. A single Predator entry, no Matrix.
	if len(ch.Lineup) != 1 {
		t.Fatalf("lineup has %d entries, want 1 (the refined pick)", len(ch.Lineup))
	}
	got := string(ch.Lineup[0].Key)
	if got != "movie:tmdb:106" {
		t.Errorf("channel bound to %q — want the REFINED pick movie:tmdb:106 (Predator), "+
			"not the original Matrix; approvedProposalForJob picked the wrong proposal", got)
	}
}

func TestRefine_OlderProposalCannotRollBackNewerApproval(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	ctx := context.Background()
	const job = "job-stale-refine"
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(id, tmdb, name string, created time.Time) {
		body := `{"intent":{"description":"90s action"},"lineup":[{"mediaType":"movie",` +
			`"tmdbId":` + tmdb + `,"name":"` + name + `","inLibrary":true,"libraryItemId":"lib-` + tmdb + `"}]}`
		if err := st.CreateProposal(ctx, store.Proposal{
			ID: id, JobID: job, Status: "submitted", ProposalJSON: body, CreatedAt: created,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("p-older", "603", "The Matrix", base)
	mk("p-newer", "106", "Predator", base.Add(time.Hour))

	newer := do(t, srv, http.MethodPost, "/v1/proposals/p-newer/approve", adminToken, "")
	if newer.StatusCode != http.StatusOK {
		t.Fatalf("approve newer -> %d", newer.StatusCode)
	}
	var approved struct {
		ChannelID string `json:"channelId"`
	}
	_ = json.NewDecoder(newer.Body).Decode(&approved)
	_ = newer.Body.Close()

	older := do(t, srv, http.MethodPost, "/v1/proposals/p-older/approve", adminToken, "")
	if older.StatusCode != http.StatusConflict {
		t.Fatalf("approve superseded proposal -> %d, want 409", older.StatusCode)
	}
	ch, err := st.GetChannel(ctx, approved.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Lineup) != 1 || ch.Lineup[0].Key != "movie:tmdb:106" {
		t.Fatalf("stale approval rolled channel back: %+v", ch.Lineup)
	}
	p, err := st.GetProposal(ctx, "p-older")
	if err != nil || p.Status != "submitted" {
		t.Fatalf("superseded proposal = (%+v, %v), want submitted", p, err)
	}
	if _, err := st.GetTitle(ctx, "movie:tmdb:603"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("superseded approval inserted title: %v", err)
	}
}

// Channel numbers are auto-allocated as the lowest free one, so the operator never has
// to think about numbering to get on air — and an approval can never collide with a
// channel they numbered by hand.
func TestApprove_AllocatesTheLowestFreeChannelNumber(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	// A hand-made channel already occupies number 1.
	if _, err := st.SaveChannel(context.Background(), store.Channel{
		Channel: schedule.Channel{
			ID: "ch_manual", Name: "Manual", Number: 1, Strategy: schedule.Sequential,
			Status: schedule.StatusBuilding,
		},
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"intent":{"description":"westerns"},"lineup":[{"mediaType":"movie","tmdbId":603,` +
		`"name":"The Matrix","year":1999,"inLibrary":true,"libraryItemId":"641641"}],"acquisitions":[]}`
	if err := st.CreateProposal(context.Background(), store.Proposal{
		ID: "p-n", JobID: "job-n", Status: "submitted", ProposalJSON: body,
	}); err != nil {
		t.Fatal(err)
	}

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-n/approve", adminToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve → %d", resp.StatusCode)
	}
	var out struct {
		ChannelID string `json:"channelId"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	ch, err := st.GetChannel(context.Background(), out.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Number != 2 {
		t.Errorf("allocated number %d, want 2 (1 is taken by the hand-made channel)", ch.Number)
	}
}

// --- V25: edit-before-approve (§7, decision D-K) ---

// A dropped title is NOT acquired. This is the whole feature: an approver who removes a pick
// before approving must not have it enqueued behind their back.
func TestApprove_DroppedTitleIsNotEnqueued(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	body := `{"acquisitions":[
		{"mediaType":"movie","tmdbId":100,"name":"Speed","year":1994},
		{"mediaType":"movie","tmdbId":603,"name":"The Matrix","year":1999,"inLibrary":false}]}`
	if err := st.CreateProposal(context.Background(), store.Proposal{
		ID: "p-drop", JobID: "j", Status: "submitted", ProposalJSON: body,
	}); err != nil {
		t.Fatal(err)
	}

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-drop/approve", adminToken,
		`{"drop":["movie:tmdb:100"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve with edit → %d, want 200", resp.StatusCode)
	}

	if _, err := st.GetTitle(context.Background(), "movie:tmdb:100"); err == nil {
		t.Error("the dropped title was enqueued anyway — the edit did not reach the gate")
	}
	if _, err := st.GetTitle(context.Background(), "movie:tmdb:603"); err != nil {
		t.Errorf("the kept title was not enqueued: %v", err)
	}
}

// An added title goes through the SAME idempotent enqueue as anything the model proposed. An
// admin-added pick is not privileged; it is just another acquisition.
func TestApprove_AddedTitleIsEnqueued(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p-add")

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-add/approve", adminToken,
		`{"add":[{"mediaType":"movie","tmdbId":603,"name":"The Matrix","year":1999,"inLibrary":false}]}`)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("approve with add → %d, want 200: %s", resp.StatusCode, b)
	}
	rec, err := st.GetTitle(context.Background(), "movie:tmdb:603")
	if err != nil {
		t.Fatalf("added title not enqueued: %v", err)
	}
	if rec.State != "wanted" {
		t.Errorf("added title state = %s, want wanted (the same state a model pick gets)", rec.State)
	}
}

// The audit trail persists. `modSummary` is GENERATED server-side — a summary the approver
// types is a claim, one the code writes is a record — and `note` is their message to whoever
// requested it, which is why a request coming back altered is explicable.
func TestApprove_PersistsTheAuditTrail(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p-audit")

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-audit/approve", adminToken,
		`{"drop":["movie:tmdb:100"],"add":[{"mediaType":"movie","tmdbId":603,"name":"The Matrix","year":1999,"inLibrary":false}],"note":"swapped it, we already have Speed"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve → %d, want 200", resp.StatusCode)
	}

	p, err := st.GetProposal(context.Background(), "p-audit")
	if err != nil {
		t.Fatal(err)
	}
	if p.ModSummary != "dropped 1, added 1" {
		t.Errorf("modSummary = %q, want the generated summary of what changed", p.ModSummary)
	}
	if p.Note != "swapped it, we already have Speed" {
		t.Errorf("note = %q, want the approver's message to the requester", p.Note)
	}
	// Not asserting approvedBy here: these tests authenticate with the break-glass TOKEN,
	// which deliberately has no user record (userIDFromHuma returns "" for it). Who approved
	// is covered where a real session exists; what this test is for is that the EDIT survives.
	if p.Status != "approved" {
		t.Errorf("status = %q, want approved", p.Status)
	}
	var approved suggest.Proposal
	if err := json.Unmarshal([]byte(p.ProposalJSON), &approved); err != nil {
		t.Fatal(err)
	}
	if len(approved.Trace.Candidates) != 1 || approved.Trace.Candidates[0].Key != "movie:tmdb:100" || approved.Trace.Candidates[0].Disposition != suggest.DispositionSelected {
		t.Fatalf("approval edit rewrote original decision evidence: %+v", approved.Trace)
	}
}

// The STORED proposal reflects what was actually approved, not what the model first proposed.
// Otherwise the audit trail describes a lineup that never existed.
func TestApprove_StoredProposalReflectsTheEdit(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p-stored")

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-stored/approve", adminToken,
		`{"drop":["movie:tmdb:100"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve → %d, want 200", resp.StatusCode)
	}
	p, err := st.GetProposal(context.Background(), "p-stored")
	if err != nil {
		t.Fatal(err)
	}
	var approved suggest.Proposal
	if err := json.Unmarshal([]byte(p.ProposalJSON), &approved); err != nil {
		t.Fatal(err)
	}
	for _, items := range [][]suggest.ProposalItem{approved.Lineup, approved.Acquisitions, approved.Alternates} {
		for _, item := range items {
			if item.Name == "Speed" {
				t.Errorf("the approved proposal still contains the dropped title: %+v", approved)
			}
		}
	}
}

// An UNMODIFIED approval must be indistinguishable from the pre-edit behaviour: same bytes,
// empty summary. "Approved with modifications: none" is a different and false claim.
func TestApprove_UnmodifiedLeavesTheProposalUntouched(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p-plain")
	before, err := st.GetProposal(context.Background(), "p-plain")
	if err != nil {
		t.Fatal(err)
	}

	if resp := do(t, srv, http.MethodPost, "/v1/proposals/p-plain/approve", adminToken, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve with no body → %d, want 200 — an empty body must still approve", resp.StatusCode)
	}
	after, err := st.GetProposal(context.Background(), "p-plain")
	if err != nil {
		t.Fatal(err)
	}
	if after.ProposalJSON != before.ProposalJSON {
		t.Error("an unmodified approval rewrote the proposal JSON")
	}
	if after.ModSummary != "" {
		t.Errorf("modSummary = %q, want empty for an unmodified approval", after.ModSummary)
	}
}

// The gate is still admin-only WITH a body. An edit is not a way in.
func TestApprove_MemberCannotEditAndApprove(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p-member")

	resp := do(t, srv, http.MethodPost, "/v1/proposals/p-member/approve", "",
		`{"drop":["movie:tmdb:100"],"note":"let me in"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("member approve with an edit → %d, want 401", resp.StatusCode)
	}
	if _, err := st.GetTitle(context.Background(), "movie:tmdb:100"); err == nil {
		t.Error("a member's rejected approval still enqueued a title")
	}
}

// --- V27: approvedAt + bulk approve ------------------------------------------

// The audit rows' ordering key. Nothing recorded WHEN a proposal was approved, so a history
// could list decisions in no verifiable order. Stamped at the ONE chokepoint, so every path
// that approves — human, auto-approve grant, auto-curate, bulk — records a time.
func TestApprove_StampsApprovedAt(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p1")

	before := time.Now().Add(-time.Second)
	resp := do(t, srv, http.MethodPost, "/v1/proposals/p1/approve", adminToken, "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve → %d, want 200", resp.StatusCode)
	}

	p, err := st.GetProposal(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if p.ApprovedAt.IsZero() {
		t.Fatal("approvedAt is zero after approving — an audit row with no time cannot be ordered")
	}
	if p.ApprovedAt.Before(before) {
		t.Errorf("approvedAt = %v, want >= %v", p.ApprovedAt, before)
	}
}

// A proposal that was never approved must carry NO approval time. Emitting the zero time as
// "0001-01-01T00:00:00Z" would put a date on a decision that never happened.
func TestProposalDTO_OmitsApprovedAtWhileUnapproved(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p1")

	resp := do(t, srv, http.MethodGet, "/v1/proposals?status=submitted", adminToken, "")
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "approvedAt") {
		t.Errorf("unapproved proposal carries approvedAt: %s", body)
	}
}

func TestProposalDTO_CarriesApprovedAtOnceApproved(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p1")
	_ = do(t, srv, http.MethodPost, "/v1/proposals/p1/approve", adminToken, "").Body.Close()

	resp := do(t, srv, http.MethodGet, "/v1/proposals?status=approved", adminToken, "")
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Proposals []struct {
			ApprovedAt string `json:"approvedAt"`
		} `json:"proposals"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Proposals) != 1 {
		t.Fatalf("got %d approved proposals, want 1", len(out.Proposals))
	}
	if _, err := time.Parse(time.RFC3339, out.Proposals[0].ApprovedAt); err != nil {
		t.Errorf("approvedAt %q is not RFC3339: %v", out.Proposals[0].ApprovedAt, err)
	}
}

// ⚠ The route-shape check. `POST /v1/proposals/approve` (bulk) and
// `POST /v1/proposals/{id}/approve` (single) are different paths, but a literal segment that
// could be mistaken for an id is worth pinning: if the router ever resolved "approve" as an
// {id}, bulk would 404 as a missing proposal instead of approving anything.
func TestBulkApprove_DoesNotCollideWithSingleApprove(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p1")
	seedProposal(t, st, "p2")

	resp := do(t, srv, http.MethodPost, "/v1/proposals/approve", adminToken, `{"ids":["p1","p2"]}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("bulk approve → %d (%s), want 200 — the literal /approve path must not resolve as {id}", resp.StatusCode, body)
	}
}

// THE phase gate: "bulk approve goes through the same chokepoint". Bulk delegates to the single
// approve handler per id, so everything that makes one approval correct applies unchanged —
// asserted through its OBSERVABLE effects: status flipped, acquisitions enqueued as `wanted`
// titles, and the audit stamps written.
func TestBulkApprove_GoesThroughTheSameGate(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	// DISTINCT titles per proposal. `seedProposal` gives every proposal the same acquisition
	// (tmdbId 100), and enqueue is idempotent by provisioning key — so two seeded proposals
	// would produce ONE wanted title and the count below could not distinguish "both went
	// through the gate" from "one did".
	seedProposalWithTMDB(t, st, "p1", 100, "Speed")
	seedProposalWithTMDB(t, st, "p2", 101, "Heat")

	resp := do(t, srv, http.MethodPost, "/v1/proposals/approve", adminToken, `{"ids":["p1","p2"]}`)
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Approved int `json:"approved"`
		Results  []struct {
			ID        string `json:"id"`
			OK        bool   `json:"ok"`
			Enqueued  int    `json:"enqueued"`
			ChannelID string `json:"channelId"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Approved != 2 {
		t.Fatalf("approved = %d, want 2 (results: %+v)", out.Approved, out.Results)
	}
	channelIDs := map[string]bool{}
	for _, result := range out.Results {
		if !result.OK || result.ChannelID == "" {
			t.Errorf("bulk result lacks committed channel: %+v", result)
		}
		channelIDs[result.ChannelID] = true
	}
	if len(channelIDs) != 2 {
		t.Errorf("bulk approvals committed %d distinct channels, want 2", len(channelIDs))
	}
	if channels, err := st.ListChannels(context.Background()); err != nil || len(channels) != 2 {
		t.Errorf("persisted channels = %d, %v; want 2", len(channels), err)
	}

	for _, id := range []string{"p1", "p2"} {
		p, err := st.GetProposal(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Status != "approved" {
			t.Errorf("%s status = %q, want approved", id, p.Status)
		}
		if p.ApprovedAt.IsZero() {
			t.Errorf("%s has no approvedAt — bulk must stamp the same audit fields as a single approve", id)
		}
	}
	// The gate's real output: acquisitions became `wanted` titles. Seeded proposals carry one
	// acquisition each, so a bulk of two must enqueue two.
	wanted, err := st.ListTitlesByState(context.Background(), provision.Wanted)
	if err != nil {
		t.Fatal(err)
	}
	if len(wanted) != 2 {
		t.Errorf("got %d wanted titles after bulk approving 2 proposals, want 2 — bulk must enqueue through the gate, not bypass it", len(wanted))
	}
}

// One already-handled id must not abort the rest: the approvals that worked are durable, so
// failing the whole call would hide them. But the caller has to learn which failed, or
// "approve 3" silently becoming "approved 2" is invisible.
func TestBulkApprove_PartialFailureReportsPerID(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedProposalAt(t, st, "p1", base)
	seedProposalAt(t, st, "p2", base.Add(time.Hour))
	// p1 is already approved before the bulk call.
	_ = do(t, srv, http.MethodPost, "/v1/proposals/p1/approve", adminToken, "").Body.Close()

	resp := do(t, srv, http.MethodPost, "/v1/proposals/approve", adminToken,
		`{"ids":["p1","missing","p2"]}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk with partial failures → %d, want 200 (failures are data, not a request error)", resp.StatusCode)
	}
	var out struct {
		Approved int `json:"approved"`
		Results  []struct {
			ID    string `json:"id"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Approved != 1 {
		t.Errorf("approved = %d, want 1 (only p2 was still submitted)", out.Approved)
	}
	if len(out.Results) != 3 {
		t.Fatalf("got %d results, want one per requested id", len(out.Results))
	}
	byID := map[string]bool{}
	for _, r := range out.Results {
		byID[r.ID] = r.OK
		if !r.OK && r.Error == "" {
			t.Errorf("%s failed with no reason — the caller cannot tell what went wrong", r.ID)
		}
	}
	if byID["p1"] || byID["missing"] || !byID["p2"] {
		t.Errorf("results = %+v; want p1 and missing failed, p2 approved", out.Results)
	}
}

// §19: the gate is admin-only, and bulk is still the gate. A member must approve NOTHING.
func TestBulkApprove_MemberIsRejected(t *testing.T) {
	srv, st, _ := newSuggestServer(t)
	seedProposal(t, st, "p1")

	for _, tok := range []string{"", "not-the-admin-token"} {
		resp := do(t, srv, http.MethodPost, "/v1/proposals/approve", tok, `{"ids":["p1"]}`)
		if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
			t.Errorf("bulk approve with token %q → %d, want 401/403", tok, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	// And nothing was enqueued — the §19 assertion that matters is the ABSENCE of acquisitions.
	wanted, err := st.ListTitlesByState(context.Background(), provision.Wanted)
	if err != nil {
		t.Fatal(err)
	}
	if len(wanted) != 0 {
		t.Errorf("got %d wanted titles after a rejected bulk approve, want 0", len(wanted))
	}
}

// seedProposalWithTMDB is seedProposal with a caller-chosen title, for tests that count the
// titles an approval enqueues. Enqueue is idempotent by provisioning key, so two proposals
// sharing one acquisition enqueue ONE title — which would silently weaken any such count.
func seedProposalWithTMDB(t *testing.T, st store.Store, id string, tmdbID int, name string) {
	t.Helper()
	body := fmt.Sprintf(
		`{"acquisitions":[{"mediaType":"movie","tmdbId":%d,"name":%q,"year":1994}]}`, tmdbID, name)
	err := st.CreateProposal(context.Background(), store.Proposal{
		ID: id, JobID: "job-" + id, Status: "submitted", CreatedBy: "alice", ProposalJSON: body,
	})
	if err != nil {
		t.Fatal(err)
	}
}
