package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/notifications"
	"github.com/loomarr/loomarr/internal/provision"
	"github.com/loomarr/loomarr/internal/schedule"
)

// Jobs and proposals (§8): the suggester job lifecycle, the scheduled-job table behind
// the cron runner, and the proposal queues the approval gate reads.
//
// ⚠ The three scheduled-job tests sit TOGETHER here. `ScheduledJobPaused` used to live ~900
// lines from its two siblings, in a file ordered by when each test was written.

func sampleJob(id, hash string, deadline, createdAt time.Time) Job {
	return Job{
		ID: id, Kind: "suggest", Status: "queued",
		IntentJSON: `{"description":"90s action"}`, IntentHash: hash,
		CreatedBy: "user-1", WorkflowVersion: ProposalWorkflowVersion,
		Deadline: deadline, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

func approvalChannel(id, intentRef string, number int) Channel {
	ch := Channel{}
	ch.ID = id
	ch.IntentRef = intentRef
	ch.Name = "Approved " + id
	ch.Number = number
	ch.Strategy = schedule.Sequential
	ch.Status = schedule.StatusBuilding
	ch.ReconcileDeadline = time.Unix(1_800_000_000, 0).UTC()
	return ch
}

func testJobRoundTrip(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	want := sampleJob("job-1", "hash-abc", now, now)
	want.FailureCode = "no_grounded_titles"
	if err := s.CreateJob(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "queued" || got.IntentHash != "hash-abc" || got.CreatedBy != "user-1" || got.FailureCode != "no_grounded_titles" {
		t.Errorf("job round-trip mismatch: %+v", got)
	}
	// Update transitions status.
	got.Status = "done"
	got.UpdatedAt = now
	if err := s.UpdateJob(ctx, got); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetJob(ctx, "job-1")
	if after.Status != "done" {
		t.Errorf("update didn't persist status: %s", after.Status)
	}
	if _, err := s.GetJob(ctx, "nope"); err != ErrNotFound {
		t.Errorf("GetJob(missing) = %v, want ErrNotFound", err)
	}
}

func testProposalJobListScope(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	aliceOld := sampleJob("alice-old", "h1", now, now.Add(-time.Minute))
	aliceOld.CreatedBy = "alice"
	aliceNew := sampleJob("alice-new", "h2", now, now)
	aliceNew.CreatedBy = "alice"
	bob := sampleJob("bob", "h3", now, now.Add(time.Minute))
	bob.CreatedBy = "bob"
	maintenance := sampleJob("recurate", "h4", now, now.Add(2*time.Minute))
	maintenance.CreatedBy, maintenance.Kind = "alice", "recurate"
	for _, job := range []Job{aliceOld, aliceNew, bob, maintenance} {
		if err := s.CreateJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	owned, err := s.ListProposalJobIDsByCreator(ctx, "alice", 100)
	if err != nil || len(owned) != 2 || owned[0] != "alice-new" || owned[1] != "alice-old" {
		t.Fatalf("alice Proposal Jobs = %v, %v", owned, err)
	}
	all, err := s.ListProposalJobIDs(ctx, 2)
	if err != nil || len(all) != 2 || all[0] != "bob" || all[1] != "alice-new" {
		t.Fatalf("bounded Proposal Jobs = %v, %v", all, err)
	}
}

func testClaimDueJobs(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	// Seed two real running attempts first: one lease remains current, the other
	// expires and must be interrupted when this test's main claim recovers it.
	running := sampleJob("running", "h3", now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	_ = s.CreateJob(ctx, running)
	if got, err := s.ClaimDueJobs(ctx, now.Add(-2*time.Hour), 3*time.Hour, 1); err != nil || len(got) != 1 {
		t.Fatalf("seed unexpired running Attempt = %+v, %v", got, err)
	}
	orphaned := sampleJob("orphaned", "h4", now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	_ = s.CreateJob(ctx, orphaned)
	if got, err := s.ClaimDueJobs(ctx, now.Add(-2*time.Hour), 30*time.Minute, 1); err != nil || len(got) != 1 {
		t.Fatalf("seed expired running Attempt = %+v, %v", got, err)
	}

	_ = s.CreateJob(ctx, sampleJob("due", "h1", now.Add(-time.Hour), now))
	future := sampleJob("future", "h2", now.Add(time.Hour), now)
	_ = s.CreateJob(ctx, future)

	claimed, err := s.ClaimDueJobs(ctx, now, time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 2 {
		t.Fatalf("ClaimDueJobs = %d, want due + orphaned: %+v", len(claimed), claimed)
	}
	for _, job := range claimed {
		wantAttempt := 1
		if job.ID == "orphaned" {
			wantAttempt = 2
		}
		if job.Status != "running" || job.Attempts != wantAttempt || !job.UpdatedAt.Equal(now) {
			t.Fatalf("claimed job was not atomically started: %+v", job)
		}
	}
	orphanedAttempts, err := s.ListProposalJobAttempts(ctx, "orphaned")
	if err != nil || len(orphanedAttempts) != 2 {
		t.Fatalf("orphaned Attempt history = %+v, %v", orphanedAttempts, err)
	}
	if orphanedAttempts[0].Status != "interrupted" || !orphanedAttempts[0].CompletedAt.Equal(now) ||
		orphanedAttempts[1].Status != "running" || !orphanedAttempts[1].StartedAt.Equal(now) {
		t.Fatalf("recovered Attempt history = %+v", orphanedAttempts)
	}
	gotRunning, err := s.GetJob(ctx, "running")
	if err != nil {
		t.Fatal(err)
	}
	if gotRunning.Status != "running" || gotRunning.Attempts != 1 {
		t.Fatalf("unexpired running job was disturbed: %+v", gotRunning)
	}
	// Leased: second claim returns nothing.
	again, _ := s.ClaimDueJobs(ctx, now, time.Minute, 10)
	if len(again) != 0 {
		t.Errorf("re-claim returned %d leased jobs, want 0", len(again))
	}
}

func testClaimJobsConcurrent(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	const n = 20
	for i := 0; i < n; i++ {
		_ = s.CreateJob(ctx, sampleJob("job-"+string(rune('a'+i)), "h", now.Add(-time.Hour), now))
	}
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				batch, err := s.ClaimDueJobs(ctx, now, time.Minute, 3)
				if err != nil || len(batch) == 0 {
					return
				}
				mu.Lock()
				for _, j := range batch {
					seen[j.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != n {
		t.Errorf("claimed %d distinct jobs, want %d", len(seen), n)
	}
	for id, c := range seen {
		if c != 1 {
			t.Errorf("job %s claimed %d times, want 1", id, c)
		}
	}
}

func testSuggestionSuccessAtomic(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	job := sampleJob("job-success", "hash-success", now, now)
	job.FailureTraceJSON = `{"version":1,"terminal":"budget_exhausted"}`
	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimDueJobs(ctx, now, time.Minute, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim success job = %+v, %v", claimed, err)
	}
	job = claimed[0]
	proposal := Proposal{
		ID: "proposal-success", JobID: job.ID, Status: "submitted", CreatedBy: job.CreatedBy,
		ProposalJSON: `{"lineup":[]}`, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CommitSuggestionSuccess(ctx, job.ID, job.Attempts, proposal, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	gotJob, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotJob.Status != "done" || !gotJob.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("completed job = status %q updated %v", gotJob.Status, gotJob.UpdatedAt)
	}
	if gotJob.FailureTraceJSON != "" {
		t.Fatalf("successful job retained failure trace: %q", gotJob.FailureTraceJSON)
	}
	gotProposal, err := s.GetProposal(ctx, proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotProposal.JobID != job.ID || gotProposal.Status != "submitted" {
		t.Fatalf("completed proposal = %+v", gotProposal)
	}
	if attempts, err := s.ListProposalJobAttempts(ctx, job.ID); err != nil || len(attempts) != 1 ||
		attempts[0].Status != "succeeded" || !attempts[0].CompletedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("successful Attempt history = %+v, %v", attempts, err)
	}

	// A non-running job cannot acquire a proposal. The insert and guarded transition
	// are one transaction, so the failed transition leaves no orphan proposal behind.
	queued := sampleJob("job-queued", "hash-queued", now, now)
	queued.Deadline = now.Add(time.Hour)
	if err := s.CreateJob(ctx, queued); err != nil {
		t.Fatal(err)
	}
	orphan := proposal
	orphan.ID = "proposal-orphan"
	orphan.JobID = queued.ID
	if err := s.CommitSuggestionSuccess(ctx, queued.ID, queued.Attempts, orphan, now); !errors.Is(err, ErrJobNotRunning) {
		t.Fatalf("commit from queued = %v, want ErrJobNotRunning", err)
	}
	if _, err := s.GetProposal(ctx, orphan.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled-back proposal lookup = %v, want ErrNotFound", err)
	}

	owned := sampleJob("job-owned", "hash-owned", now, now)
	if err := s.CreateJob(ctx, owned); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.ClaimDueJobs(ctx, now, time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != owned.ID {
		t.Fatalf("claim owned job = %+v, %v", claimed, err)
	}
	owned = claimed[0]
	foreign := proposal
	foreign.ID = "proposal-foreign"
	foreign.JobID = owned.ID
	foreign.CreatedBy = "other-user"
	if err := s.CommitSuggestionSuccess(ctx, owned.ID, owned.Attempts, foreign, now); !errors.Is(err, ErrJobOwnershipMismatch) {
		t.Fatalf("commit with foreign proposal = %v, want ErrJobOwnershipMismatch", err)
	}
	if _, err := s.GetProposal(ctx, foreign.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign proposal lookup = %v, want ErrNotFound", err)
	}

	failed := sampleJob("job-failed", "hash-failed", now, now)
	if err := s.CreateJob(ctx, failed); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.ClaimDueJobs(ctx, now, time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != failed.ID {
		t.Fatalf("claim failing job = %+v, %v", claimed, err)
	}
	failed = claimed[0]
	if err := s.CommitSuggestionFailure(ctx, failed.ID, failed.Attempts,
		"provider unavailable", "generation_failed", `{"version":1,"terminal":"selection_empty"}`, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	gotFailed, err := s.GetJob(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotFailed.Status != "failed" || gotFailed.LastError != "provider unavailable" ||
		gotFailed.FailureCode != "generation_failed" || gotFailed.Attempts != 1 {
		t.Fatalf("failed job lifecycle = %+v", gotFailed)
	}
	if gotFailed.FailureTraceJSON != `{"version":1,"terminal":"selection_empty"}` {
		t.Fatalf("failure trace round-trip = %q", gotFailed.FailureTraceJSON)
	}
	if attempts, err := s.ListProposalJobAttempts(ctx, failed.ID); err != nil || len(attempts) != 1 ||
		attempts[0].Status != "failed" || attempts[0].FailureCode != "generation_failed" ||
		!attempts[0].CompletedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("failed Attempt history = %+v, %v", attempts, err)
	}

	// An old worker may finish after a refine was claimed as a new execution.
	// The attempt token makes both terminal transitions lose without rewriting it.
	stale := sampleJob("job-stale", "hash-old", now, now)
	if err := s.CreateJob(ctx, stale); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.ClaimDueJobs(ctx, now, time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != stale.ID {
		t.Fatalf("claim stale job = %+v, %v", claimed, err)
	}
	stale = claimed[0]
	// Let Attempt 1's lease expire. Recovery, not an unguarded whole-row write,
	// creates Attempt 2 and makes every late Attempt-1 result stale.
	claimed, err = s.ClaimDueJobs(ctx, now.Add(2*time.Minute), time.Minute, 10)
	if err != nil || len(claimed) == 0 {
		t.Fatalf("claim replacement execution = %+v, %v", claimed, err)
	}
	var replacement Job
	for _, candidate := range claimed {
		if candidate.ID == stale.ID {
			replacement = candidate
			break
		}
	}
	if replacement.ID == "" || replacement.Attempts != 2 {
		t.Fatalf("replacement execution was not claimed: %+v", claimed)
	}
	staleProposal := proposal
	staleProposal.ID = "proposal-stale"
	staleProposal.JobID = stale.ID
	if err := s.CommitSuggestionSuccess(ctx, stale.ID, stale.Attempts, staleProposal, now.Add(3*time.Minute)); !errors.Is(err, ErrJobNotRunning) {
		t.Fatalf("stale success = %v, want ErrJobNotRunning", err)
	}
	if err := s.CommitSuggestionFailure(ctx, stale.ID, stale.Attempts,
		"old failure", "generation_failed", "", now.Add(3*time.Minute)); !errors.Is(err, ErrJobNotRunning) {
		t.Fatalf("stale failure = %v, want ErrJobNotRunning", err)
	}
	winner, err := s.GetJob(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if winner.Status != "running" || winner.Attempts != 2 || winner.IntentHash != "hash-old" || winner.LastError != "" {
		t.Fatalf("replacement execution was overwritten: %+v", winner)
	}
	if _, err := s.GetProposal(ctx, staleProposal.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale proposal lookup = %v, want ErrNotFound", err)
	}
	if attempts, err := s.ListProposalJobAttempts(ctx, stale.ID); err != nil || len(attempts) != 2 ||
		attempts[0].Status != "interrupted" || attempts[1].Status != "running" {
		t.Fatalf("stale recovery Attempt history = %+v, %v", attempts, err)
	}
}

func testSuggestionRequeueCAS(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	job := sampleJob("job-requeue", "hash-original", now, now)
	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimDueJobs(ctx, now, time.Minute, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim original job = %+v, %v", claimed, err)
	}
	job = claimed[0]
	first := Proposal{
		ID: "proposal-first", JobID: job.ID, Status: "submitted", CreatedBy: job.CreatedBy,
		ProposalJSON: `{"lineup":[{"name":"First"}]}`, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CommitSuggestionSuccess(ctx, job.ID, job.Attempts, first, now); err != nil {
		t.Fatal(err)
	}

	if err := s.RequeueSuggestionJob(ctx, job.ID, 1, "suggest", `{"description":"first refine"}`,
		"hash-refine-a", now, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RequeueSuggestionJob(ctx, job.ID, 1, "suggest", `{"description":"stale refine"}`,
		"hash-refine-b", now, now); !errors.Is(err, ErrJobNotTerminal) {
		t.Fatalf("stale concurrent requeue = %v, want ErrJobNotTerminal", err)
	}
	queued, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != "queued" || queued.Attempts != 1 || queued.IntentHash != "hash-refine-a" {
		t.Fatalf("stale requeue overwrote winner: %+v", queued)
	}

	claimed, err = s.ClaimDueJobs(ctx, now, time.Minute, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim refined job = %+v, %v", claimed, err)
	}
	if claimed[0].Attempts != 2 || claimed[0].Status != "running" {
		t.Fatalf("refined execution token = %+v", claimed[0])
	}
	if err := s.RequeueSuggestionJob(ctx, job.ID, 2, "suggest", `{}`, "active-overwrite", now, now); !errors.Is(err, ErrJobNotTerminal) {
		t.Fatalf("active requeue = %v, want ErrJobNotTerminal", err)
	}

	second := Proposal{
		ID: "proposal-second", JobID: job.ID, Status: "submitted", CreatedBy: job.CreatedBy,
		ProposalJSON: `{"lineup":[{"name":"Second"}]}`, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CommitSuggestionSuccess(ctx, job.ID, 2, second, now); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.GetProposalJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Proposal == nil || snapshot.Proposal.ID != second.ID {
		t.Fatalf("same-second refine selected wrong proposal: %+v", snapshot.Proposal)
	}
	if !snapshot.Proposal.CreatedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("refine proposal timestamp = %v, want monotonic %v", snapshot.Proposal.CreatedAt, now.Add(time.Second))
	}
	if attempts, err := s.ListProposalJobAttempts(ctx, job.ID); err != nil || len(attempts) != 2 ||
		attempts[0].Status != "succeeded" || attempts[1].Status != "succeeded" {
		t.Fatalf("refine Attempt history = %+v, %v", attempts, err)
	}
}

func testCloneSuggestionSuccess(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	source := sampleJob("job-source", "hash-shared", now, now)
	source.Status = "done"
	if err := s.CreateJob(ctx, source); err != nil {
		t.Fatal(err)
	}
	sourceProposal := Proposal{
		ID: "proposal-source", JobID: source.ID, Status: "approved", CreatedBy: source.CreatedBy,
		ApprovedBy: "admin", ProposalJSON: `{"lineup":[{"name":"The Matrix"}],"trace":{"version":1,"candidates":[{"key":"movie:tmdb:603","ownership":"library","disposition":"selected","reason":"selected"}],"surfacedTotal":1,"recordedTotal":1,"truncated":false}}`,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateProposal(ctx, sourceProposal); err != nil {
		t.Fatal(err)
	}

	cloneJob := sampleJob("job-clone", source.IntentHash, now.Add(time.Minute), now.Add(time.Minute))
	cloneJob.Status = "done"
	cloneJob.CreatedBy = "bob"
	cloneJob.FailureTraceJSON = `{"version":1,"terminal":"provider_failure"}`
	clone, err := s.CloneSuggestionSuccess(ctx, source.ID, cloneJob, "proposal-clone")
	if err != nil {
		t.Fatal(err)
	}
	if clone.ID != "proposal-clone" || clone.JobID != cloneJob.ID || clone.Status != "submitted" {
		t.Fatalf("cloned proposal identity = %+v", clone)
	}
	if clone.CreatedBy != "bob" || clone.ApprovedBy != "" || clone.ProposalJSON != sourceProposal.ProposalJSON {
		t.Fatalf("cloned proposal lifecycle/payload = %+v", clone)
	}
	var roundTrip struct {
		Trace struct {
			Version    int `json:"version"`
			Candidates []struct {
				Key string `json:"key"`
			} `json:"candidates"`
		} `json:"trace"`
	}
	if err := json.Unmarshal([]byte(clone.ProposalJSON), &roundTrip); err != nil || roundTrip.Trace.Version != 1 || len(roundTrip.Trace.Candidates) != 1 || roundTrip.Trace.Candidates[0].Key != "movie:tmdb:603" {
		t.Fatalf("cloned trace did not round-trip: %+v %v", roundTrip, err)
	}
	gotJob, err := s.GetJob(ctx, cloneJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotJob.Status != "done" || gotJob.CreatedBy != "bob" {
		t.Fatalf("cloned job = %+v", gotJob)
	}
	if gotJob.FailureTraceJSON != "" {
		t.Fatalf("cloned job inherited failed-job trace: %q", gotJob.FailureTraceJSON)
	}

	// A source job without a proposal is not a cache hit, and the attempted clone
	// leaves no half-created caller-owned job behind.
	emptySource := sampleJob("job-empty-source", "hash-empty", now, now)
	emptySource.Status = "done"
	if err := s.CreateJob(ctx, emptySource); err != nil {
		t.Fatal(err)
	}
	missingClone := sampleJob("job-missing-clone", emptySource.IntentHash, now, now)
	missingClone.Status = "done"
	if _, err := s.CloneSuggestionSuccess(ctx, emptySource.ID, missingClone, "proposal-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clone without source proposal = %v, want ErrNotFound", err)
	}
	if _, err := s.GetJob(ctx, missingClone.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("half-created cloned job lookup = %v, want ErrNotFound", err)
	}

	for _, tc := range []struct {
		name   string
		status string
		kind   string
		hash   string
	}{
		{name: "running", status: "running", kind: "suggest", hash: source.IntentHash},
		{name: "recurate", status: "done", kind: "recurate", hash: source.IntentHash},
		{name: "different intent", status: "done", kind: "suggest", hash: "different-hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guarded := source
			guarded.ID = "job-guarded-" + tc.name
			guarded.Status = tc.status
			guarded.Kind = tc.kind
			guarded.IntentHash = tc.hash
			if err := s.CreateJob(ctx, guarded); err != nil {
				t.Fatal(err)
			}
			guardedProposal := sourceProposal
			guardedProposal.ID = "proposal-guarded-" + tc.name
			guardedProposal.JobID = guarded.ID
			if err := s.CreateProposal(ctx, guardedProposal); err != nil {
				t.Fatal(err)
			}
			candidate := cloneJob
			candidate.ID = "job-candidate-" + tc.name
			if _, err := s.CloneSuggestionSuccess(ctx, guarded.ID, candidate, "proposal-candidate-"+tc.name); !errors.Is(err, ErrNotFound) {
				t.Fatalf("guarded clone = %v, want ErrNotFound", err)
			}
		})
	}
}

func testProposalJobFirstLiveMonotonic(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	job := sampleJob("job-first-live", "hash-first-live", now, now)
	job.Status = "done"
	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}

	channel := approvalChannel("channel-first-live", job.ID, 199)
	channel = mustSaveChannel(t, s, channel)
	before, err := s.GetProposalJob(ctx, job.ID)
	if err != nil || before.Job.ReachedLive {
		t.Fatalf("building Journey snapshot = (%+v, %v)", before, err)
	}

	channel.Status = schedule.StatusLive
	channel = mustSaveChannel(t, s, channel)
	live, err := s.GetProposalJob(ctx, job.ID)
	if err != nil || !live.Job.ReachedLive || live.Channel == nil || live.Channel.Status != schedule.StatusLive {
		t.Fatalf("live Journey snapshot = (%+v, %v)", live, err)
	}

	channel.Status = schedule.StatusPaused
	channel = mustSaveChannel(t, s, channel)
	paused, err := s.GetProposalJob(ctx, job.ID)
	if err != nil || !paused.Job.ReachedLive || paused.Channel == nil || paused.Channel.Status != schedule.StatusPaused {
		t.Fatalf("paused-after-live Journey snapshot = (%+v, %v)", paused, err)
	}

	driftedJob := sampleJob("job-first-drifted", "hash-first-drifted", now, now)
	driftedJob.Status = "done"
	if err := s.CreateJob(ctx, driftedJob); err != nil {
		t.Fatal(err)
	}
	driftedChannel := approvalChannel("channel-first-drifted", driftedJob.ID, 200)
	driftedChannel = mustSaveChannel(t, s, driftedChannel)
	driftedChannel.Status = schedule.StatusDrifted
	driftedChannel = mustSaveChannel(t, s, driftedChannel)
	drifted, err := s.GetProposalJob(ctx, driftedJob.ID)
	if err != nil || !drifted.Job.ReachedLive || drifted.Channel == nil || drifted.Channel.Status != schedule.StatusDrifted {
		t.Fatalf("drifted-first-live Journey snapshot = (%+v, %v)", drifted, err)
	}
}

func testProposalJobSnapshot(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	job := sampleJob("job-snapshot", "hash-snapshot", now, now)
	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	older := Proposal{
		ID: "proposal-older", JobID: job.ID, Status: "approved", CreatedBy: job.CreatedBy,
		ApprovedBy: "admin", ProposalJSON: `{"name":"older"}`, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateProposal(ctx, older); err != nil {
		t.Fatal(err)
	}

	active, err := s.GetProposalJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active.Job.Status != "queued" || active.Proposal != nil {
		t.Fatalf("active snapshot exposed an old proposal: %+v", active)
	}

	newer := older
	newer.ID = "proposal-newer"
	newer.Status = "denied"
	newer.ApprovedBy = ""
	newer.DenyReason = "not this time"
	newer.ProposalJSON = `{"name":"newer"}`
	newer.CreatedAt = now.Add(time.Minute)
	newer.UpdatedAt = newer.CreatedAt
	if err := s.CreateProposal(ctx, newer); err != nil {
		t.Fatal(err)
	}
	job.Status = "done"
	job.UpdatedAt = newer.UpdatedAt
	if err := s.UpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}

	done, err := s.GetProposalJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Proposal == nil || done.Proposal.ID != newer.ID || done.Proposal.Status != "denied" {
		t.Fatalf("done snapshot did not select newest proposal: %+v", done)
	}

	other := sampleJob("job-other-snapshot", "hash-other-snapshot", now, now)
	other.Status = "done"
	if err := s.CreateJob(ctx, other); err != nil {
		t.Fatal(err)
	}
	otherSnapshot, err := s.GetProposalJob(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if otherSnapshot.Proposal != nil {
		t.Fatalf("snapshot joined another job's proposal: %+v", otherSnapshot)
	}
	if _, err := s.GetProposalJob(ctx, "missing-job"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing snapshot = %v, want ErrNotFound", err)
	}
}

func testJobCacheByHash(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	cached := sampleJob("cached", "hash-X", now, now)
	cached.Status = "done"
	if err := s.CreateJob(ctx, cached); err != nil {
		t.Fatal(err)
	}
	queued := sampleJob("queued", "hash-X", now.Add(time.Second), now.Add(time.Second))
	if err := s.CreateJob(ctx, queued); err != nil {
		t.Fatal(err)
	}
	failed := sampleJob("failed", "hash-X", now.Add(2*time.Second), now.Add(2*time.Second))
	failed.Status = "failed"
	if err := s.CreateJob(ctx, failed); err != nil {
		t.Fatal(err)
	}

	// A search within TTL finds the most recent successful job. Newer queued or
	// failed attempts must not shadow reusable content from that success.
	got, err := s.FindJobByIntentHash(ctx, "hash-X", now.Add(-24*time.Hour))
	if err != nil || got.ID != "cached" {
		t.Fatalf("FindJobByIntentHash = %q,%v want cached", got.ID, err)
	}
	// A search with `since` after the job's creation misses (TTL expired).
	if _, err := s.FindJobByIntentHash(ctx, "hash-X", now.Add(time.Hour)); err != ErrNotFound {
		t.Errorf("expired cache lookup = %v, want ErrNotFound", err)
	}
	// A different hash misses.
	if _, err := s.FindJobByIntentHash(ctx, "hash-other", now.Add(-24*time.Hour)); err != ErrNotFound {
		t.Errorf("miss lookup = %v, want ErrNotFound", err)
	}
}

// testScheduledJobRoundTrip: upsert creates then updates a job's state row; list + get read
// it back; a missing row is ErrNotFound.
func testScheduledJobRoundTrip(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()

	if _, err := s.GetScheduledJob(ctx, "nope"); err != ErrNotFound {
		t.Errorf("missing scheduled job = %v, want ErrNotFound", err)
	}
	if err := s.UpsertScheduledJob(ctx, ScheduledJob{Name: "reconcile", NextRun: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Update in place (same name) — last_result + next_run change.
	next := now.Add(5 * time.Minute)
	if err := s.UpsertScheduledJob(ctx, ScheduledJob{
		Name: "reconcile", LastRun: now, LastResult: "ok", NextRun: next, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetScheduledJob(ctx, "reconcile")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastResult != "ok" || !got.NextRun.Equal(next) || !got.LastRun.Equal(now) {
		t.Errorf("round-tripped scheduled job = %+v, want ok/next=%v/last=%v", got, next, now)
	}
	all, _ := s.ListScheduledJobs(ctx)
	if len(all) != 1 || all[0].Name != "reconcile" {
		t.Errorf("list = %+v, want one 'reconcile'", all)
	}
}

// testClaimDueScheduledJobs: only due rows (next_run <= now) are claimed, and claiming leases
// next_run forward so a second claim returns nothing until rescheduled.
func testClaimDueScheduledJobs(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	_ = s.UpsertScheduledJob(ctx, ScheduledJob{Name: "due", NextRun: now.Add(-time.Minute), UpdatedAt: now})
	_ = s.UpsertScheduledJob(ctx, ScheduledJob{Name: "future", NextRun: now.Add(time.Hour), UpdatedAt: now})

	claimed, err := s.ClaimDueScheduledJobs(ctx, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].Name != "due" {
		t.Fatalf("ClaimDueScheduledJobs = %d, want just 'due': %+v", len(claimed), claimed)
	}
	// Leased forward → an immediate re-claim returns nothing.
	again, _ := s.ClaimDueScheduledJobs(ctx, now, time.Minute)
	if len(again) != 0 {
		t.Errorf("re-claim returned %d leased jobs, want 0", len(again))
	}
}

// testScheduledJobPaused: the pause flag persists, survives an ordinary state write, and keeps
// the job out of the due-claim (§18.1). One suite, both dialects — the claim SQL differs
// (guarded UPDATE vs FOR UPDATE SKIP LOCKED) and both must skip paused rows.
func testScheduledJobPaused(t *testing.T, newStore NewStoreFunc) {
	ctx := context.Background()
	s := newStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()

	// Due NOW, not paused: the control case — without it, "did not run" proves nothing.
	if err := s.UpsertScheduledJob(ctx, ScheduledJob{Name: "reconcile", NextRun: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScheduledJobPaused(ctx, "reconcile", true); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetScheduledJob(ctx, "reconcile")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Paused {
		t.Fatal("pause did not persist")
	}

	// ⚠ An ordinary state write must NOT clear it. This runs after every execution, so if
	// `paused` rode in UpsertScheduledJob's DO UPDATE list, the next run would silently resume
	// a job the operator paused.
	if err := s.UpsertScheduledJob(ctx, ScheduledJob{
		Name: "reconcile", LastResult: "ok", LastRun: now, NextRun: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetScheduledJob(ctx, "reconcile"); !got.Paused {
		t.Error("a routine state write cleared paused — it must be absent from ON CONFLICT DO UPDATE")
	}

	// ⚠ The behaviour: a paused row is never claimed, even though it is due.
	due, err := s.ClaimDueScheduledJobs(ctx, now.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range due {
		if j.Name == "reconcile" {
			t.Error("a paused job was claimed; it would then run on its schedule")
		}
	}

	// Resuming makes it claimable again, or pause is a one-way door.
	if err := s.SetScheduledJobPaused(ctx, "reconcile", false); err != nil {
		t.Fatal(err)
	}
	due, err = s.ClaimDueScheduledJobs(ctx, now.Add(2*time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, j := range due {
		if j.Name == "reconcile" {
			found = true
		}
	}
	if !found {
		t.Error("a resumed job was still not claimed")
	}

	// Pausing a job that has never run creates the row, so a task can be paused before its
	// first execution rather than only after it has already gone off once.
	if err := s.SetScheduledJobPaused(ctx, "never-ran", true); err != nil {
		t.Fatal(err)
	}
	if got, err = s.GetScheduledJob(ctx, "never-ran"); err != nil || !got.Paused {
		t.Errorf("pausing an unseen job = (%+v, %v), want a created paused row", got, err)
	}
}

func testProposalQueues(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	mk := func(id, status, creator string) Proposal {
		return Proposal{ID: id, JobID: "job-" + id, Status: status, CreatedBy: creator,
			ProposalJSON: `{"lineup":[]}`, CreatedAt: now, UpdatedAt: now}
	}
	p1Seed := mk("p1", "submitted", "alice")
	p1Seed.ProposalJSON = `{"lineup":[],"trace":{"version":1,"candidates":[],"surfacedTotal":0,"recordedTotal":0,"truncated":false}}`
	_ = s.CreateProposal(ctx, p1Seed)
	_ = s.CreateProposal(ctx, mk("p2", "submitted", "bob"))
	_ = s.CreateProposal(ctx, mk("p3", "approved", "alice"))

	// The approval queue = submitted proposals.
	sub, err := s.ListProposalsByStatus(ctx, "submitted")
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 2 {
		t.Errorf("submitted queue = %d, want 2", len(sub))
	}
	// My proposals = by creator.
	aliceProps, _ := s.ListProposalsByCreator(ctx, "alice")
	if len(aliceProps) != 2 {
		t.Errorf("alice's proposals = %d, want 2", len(aliceProps))
	}
	// Approve p1: status + approved_by persist (survives restart — it's in the store).
	p1, _ := s.GetProposal(ctx, "p1")
	p1.Status = "approved"
	p1.ApprovedBy = "admin"
	p1.UpdatedAt = now
	if _, err := s.CommitProposalApproval(ctx, ProposalApproval{
		Proposal: p1,
		Channel:  approvalChannel("ch-p1", p1.JobID, 91),
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetProposal(ctx, "p1")
	if after.Status != "approved" || after.ApprovedBy != "admin" || after.ProposalJSON != p1Seed.ProposalJSON {
		t.Errorf("approve didn't persist: %+v", after)
	}
	if _, err := s.GetProposal(ctx, "missing"); err != ErrNotFound {
		t.Errorf("GetProposal(missing) = %v, want ErrNotFound", err)
	}
}

func testProposalApprovalAtomic(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	proposal := Proposal{
		ID: "approval", JobID: "job-approval", Status: "submitted", CreatedBy: "member",
		ProposalJSON: `{"lineup":[]}`, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if err := s.CreateProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}

	existing := provision.Record{
		Key: "movie:tmdb:1", Title: provision.Title{MediaType: provision.Movie, TMDBID: 1, Name: "Existing"},
		State: provision.Downloading, Attempts: 3, LastError: "keep me",
	}
	if err := s.UpsertTitle(ctx, existing); err != nil {
		t.Fatal(err)
	}
	available := provision.Record{
		Key: "movie:tmdb:2", Title: provision.Title{MediaType: provision.Movie, TMDBID: 2, Name: "Available"},
		State: provision.Available, LibraryID: "library-2",
	}
	wanted := provision.Record{
		Key: "movie:tmdb:3", Title: provision.Title{MediaType: provision.Movie, TMDBID: 3, Name: "Wanted"},
		State: provision.Wanted, Deadline: now,
	}
	proposal.Status = "approved"
	proposal.ApprovedBy = "admin"
	proposal.ApprovedAt = now
	proposal.UpdatedAt = now
	enqueued, err := s.CommitProposalApproval(ctx, ProposalApproval{
		Proposal: proposal,
		Titles: []provision.Record{
			{Key: existing.Key, Title: provision.Title{MediaType: provision.Movie, TMDBID: 1}, State: provision.Wanted, Deadline: now},
			available, wanted, wanted,
		},
		Channel: approvalChannel("ch-approval", proposal.JobID, 101),
	})
	if err != nil {
		t.Fatal(err)
	}
	if enqueued != 1 {
		t.Fatalf("enqueued = %d, want 1 newly inserted wanted title", enqueued)
	}
	got, err := s.GetProposal(ctx, proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "approved" || got.ApprovedBy != "admin" || !got.ApprovedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Errorf("approved proposal = %+v", got)
	}
	preserved, err := s.GetTitle(ctx, existing.Key)
	if err != nil {
		t.Fatal(err)
	}
	if preserved.State != provision.Downloading || preserved.Attempts != 3 || preserved.LastError != "keep me" {
		t.Errorf("existing title was overwritten: %+v", preserved)
	}
	if got, err := s.GetTitle(ctx, available.Key); err != nil || got.State != provision.Available || got.LibraryID != "library-2" {
		t.Errorf("available title = (%+v, %v)", got, err)
	}
	if got, err := s.GetTitle(ctx, wanted.Key); err != nil || got.State != provision.Wanted || !got.Deadline.Equal(now) {
		t.Errorf("wanted title = (%+v, %v)", got, err)
	}
	bound, err := s.GetChannel(ctx, "ch-approval")
	if err != nil || bound.IntentRef != proposal.JobID || bound.Number != 101 {
		t.Errorf("approved channel = (%+v, %v)", bound, err)
	}
	for _, reference := range []struct {
		kind notifications.ReferenceKind
		id   string
	}{
		{notifications.ReferenceTitle, string(existing.Key)},
		{notifications.ReferenceTitle, string(available.Key)},
		{notifications.ReferenceTitle, string(wanted.Key)},
		{notifications.ReferenceChannel, bound.ID},
	} {
		recipients, err := s.ListNotificationReferenceRecipients(ctx, reference.kind, reference.id)
		if err != nil || len(recipients) != 1 || recipients[0] != "member" {
			t.Errorf("notification provenance %s/%s = %+v, %v", reference.kind, reference.id, recipients, err)
		}
	}

	loser := Proposal{ID: "denied", JobID: "job-denied", Status: "denied", ProposalJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateProposal(ctx, loser); err != nil {
		t.Fatal(err)
	}
	loser.Status = "approved"
	if _, err := s.CommitProposalApproval(ctx, ProposalApproval{
		Proposal: loser,
		Titles:   []provision.Record{{Key: "movie:tmdb:99", Title: provision.Title{MediaType: provision.Movie, TMDBID: 99}, State: provision.Wanted, Deadline: now}},
		Channel:  approvalChannel("ch-denied", loser.JobID, 102),
	}); !errors.Is(err, ErrProposalNotSubmitted) {
		t.Fatalf("approve denied proposal = %v, want ErrProposalNotSubmitted", err)
	}
	if _, err := s.GetTitle(ctx, "movie:tmdb:99"); !errors.Is(err, ErrNotFound) {
		t.Errorf("losing approval inserted a title: %v", err)
	}
	if _, err := s.GetChannel(ctx, "ch-denied"); !errors.Is(err, ErrNotFound) {
		t.Errorf("losing approval inserted a channel: %v", err)
	}
	missing := Proposal{ID: "missing", JobID: "job-missing", Status: "approved"}
	if _, err := s.CommitProposalApproval(ctx, ProposalApproval{
		Proposal: missing,
		Channel:  approvalChannel("ch-missing", missing.JobID, 103),
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("approve missing proposal = %v, want ErrNotFound", err)
	}

	invalid := Proposal{ID: "invalid", JobID: "job-invalid", Status: "submitted", ProposalJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateProposal(ctx, invalid); err != nil {
		t.Fatal(err)
	}
	invalid.Status = "approved"
	if _, err := s.CommitProposalApproval(ctx, ProposalApproval{Proposal: invalid, Titles: []provision.Record{{
		Key: "movie:tmdb:404", Title: provision.Title{MediaType: provision.Movie, TMDBID: 404}, State: provision.Wanted,
	}}, Channel: approvalChannel("ch-invalid", invalid.JobID, 104)}); err == nil {
		t.Fatal("approval accepted a wanted title with no deadline")
	}
	if got, err := s.GetProposal(ctx, invalid.ID); err != nil || got.Status != "submitted" {
		t.Errorf("invalid approval changed proposal = (%+v, %v)", got, err)
	}
	if _, err := s.GetTitle(ctx, "movie:tmdb:404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("invalid approval inserted title: %v", err)
	}
	if _, err := s.GetChannel(ctx, "ch-invalid"); !errors.Is(err, ErrNotFound) {
		t.Errorf("invalid approval inserted channel: %v", err)
	}

	invalidChannelProposal := Proposal{
		ID: "invalid-channel", JobID: "job-invalid-channel", Status: "submitted",
		ProposalJSON: `{}`, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateProposal(ctx, invalidChannelProposal); err != nil {
		t.Fatal(err)
	}
	invalidChannelProposal.Status = "approved"
	validTitle := provision.Record{
		Key: "movie:tmdb:405", Title: provision.Title{MediaType: provision.Movie, TMDBID: 405},
		State: provision.Wanted, Deadline: now,
	}
	invalidChannel := approvalChannel("ch-invalid-channel", invalidChannelProposal.JobID, 105)
	invalidChannel.Number = 0
	if _, err := s.CommitProposalApproval(ctx, ProposalApproval{
		Proposal: invalidChannelProposal,
		Titles:   []provision.Record{validTitle},
		Channel:  invalidChannel,
	}); err == nil {
		t.Fatal("approval accepted an invalid channel")
	}
	if got, err := s.GetProposal(ctx, invalidChannelProposal.ID); err != nil || got.Status != "submitted" {
		t.Errorf("invalid-channel approval changed proposal = (%+v, %v)", got, err)
	}
	if _, err := s.GetTitle(ctx, validTitle.Key); !errors.Is(err, ErrNotFound) {
		t.Errorf("invalid-channel approval inserted title: %v", err)
	}
	if _, err := s.GetChannel(ctx, invalidChannel.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("invalid-channel approval inserted channel: %v", err)
	}

	invalidPolicy := approvalChannel("ch-invalid-policy", invalidChannelProposal.JobID, 106)
	invalidPolicy.Policy.Ordering = schedule.OrderingMode("not-an-order")
	if _, err := s.CommitProposalApproval(ctx, ProposalApproval{
		Proposal: invalidChannelProposal,
		Titles:   []provision.Record{validTitle},
		Channel:  invalidPolicy,
	}); err == nil {
		t.Fatal("approval accepted an invalid channel policy")
	}
	if got, err := s.GetProposal(ctx, invalidChannelProposal.ID); err != nil || got.Status != "submitted" {
		t.Errorf("invalid-policy approval changed proposal = (%+v, %v)", got, err)
	}
	if _, err := s.GetTitle(ctx, validTitle.Key); !errors.Is(err, ErrNotFound) {
		t.Errorf("invalid-policy approval inserted title: %v", err)
	}
	if _, err := s.GetChannel(ctx, invalidPolicy.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("invalid-policy approval inserted channel: %v", err)
	}
}

// A suggestion job owns one channel. If stale callers plan two different channel rows for the
// same intent, the database constraint is the final arbiter and the losing approval must roll
// back its proposal CAS and title inserts along with the rejected channel write.
func testProposalApprovalSameIntentConflict(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	const jobID = "job-shared-intent"

	seed := func(id string, createdAt time.Time) Proposal {
		p := Proposal{ID: id, JobID: jobID, Status: "submitted", ProposalJSON: `{}`,
			CreatedAt: createdAt, UpdatedAt: createdAt}
		if err := s.CreateProposal(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	first, second := seed("same-intent-first", now), seed("same-intent-second", now.Add(time.Second))
	first.Status, first.ApprovedBy = "approved", "admin-a"
	if _, err := s.CommitProposalApproval(ctx, ProposalApproval{
		Proposal: first,
		Channel:  approvalChannel("ch-same-intent-first", jobID, 140),
	}); err != nil {
		t.Fatalf("first approval: %v", err)
	}

	second.Status, second.ApprovedBy = "approved", "admin-b"
	loserTitle := provision.Record{
		Key: "movie:tmdb:141", Title: provision.Title{MediaType: provision.Movie, TMDBID: 141},
		State: provision.Wanted, Deadline: now,
	}
	if _, err := s.CommitProposalApproval(ctx, ProposalApproval{
		Proposal: second,
		Titles:   []provision.Record{loserTitle},
		Channel:  approvalChannel("ch-same-intent-second", jobID, 141),
	}); !errors.Is(err, ErrChannelConflict) {
		t.Fatalf("second approval = %v, want ErrChannelConflict", err)
	}

	gotSecond, err := s.GetProposal(ctx, second.ID)
	if err != nil || gotSecond.Status != "submitted" {
		t.Errorf("losing proposal = (%+v, %v), want submitted", gotSecond, err)
	}
	if _, err := s.GetTitle(ctx, loserTitle.Key); !errors.Is(err, ErrNotFound) {
		t.Errorf("same-intent loser inserted title: %v", err)
	}
	if _, err := s.GetChannel(ctx, "ch-same-intent-second"); !errors.Is(err, ErrNotFound) {
		t.Errorf("same-intent loser inserted channel: %v", err)
	}
	winner, err := s.GetChannelByIntentRef(ctx, jobID)
	if err != nil || winner.ID != "ch-same-intent-first" {
		t.Errorf("intent winner = (%+v, %v)", winner, err)
	}

	// The partial index excludes the empty value: hand-made and detached channels can coexist.
	for i, id := range []string{"empty-intent-a", "empty-intent-b"} {
		ch := approvalChannel(id, "unused", 142+i)
		ch.IntentRef = ""
		if _, err := s.SaveChannel(ctx, ch); err != nil {
			t.Errorf("empty intent_ref channel %s: %v", id, err)
		}
	}
}

func testProposalApprovalStaleChannel(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	const jobID = "job-stale-channel"

	planned := approvalChannel("ch-stale-approval", jobID, 145)
	planned = mustSaveChannel(t, s, planned)
	winner := planned
	winner.Name = "concurrent operator edit"
	winner = mustSaveChannel(t, s, winner)

	p := Proposal{
		ID: "proposal-stale-channel", JobID: jobID, Status: "submitted", ProposalJSON: `{}`,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateProposal(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Status, p.ApprovedBy = "approved", "admin"
	title := provision.Record{
		Key: "movie:tmdb:145", Title: provision.Title{MediaType: provision.Movie, TMDBID: 145},
		State: provision.Wanted, Deadline: now,
	}
	planned.Name = "stale approval"
	if _, err := s.CommitProposalApproval(ctx, ProposalApproval{
		Proposal: p, Titles: []provision.Record{title}, Channel: planned,
	}); !errors.Is(err, ErrChannelStale) {
		t.Fatalf("stale approval = %v, want ErrChannelStale", err)
	}

	gotProposal, err := s.GetProposal(ctx, p.ID)
	if err != nil || gotProposal.Status != "submitted" {
		t.Fatalf("proposal after stale approval = (%+v, %v), want submitted", gotProposal, err)
	}
	if _, err := s.GetTitle(ctx, title.Key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale approval inserted title: %v", err)
	}
	gotChannel, err := s.GetChannel(ctx, winner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotChannel.Name != winner.Name || gotChannel.Revision != winner.Revision {
		t.Fatalf("stale approval changed channel: name=%q revision=%d", gotChannel.Name, gotChannel.Revision)
	}
}

func testProposalApprovalSuperseded(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	const jobID = "job-superseded-approval"

	older := Proposal{
		ID: "proposal-older", JobID: jobID, Status: "submitted", ProposalJSON: `{}`,
		CreatedAt: now, UpdatedAt: now,
	}
	newer := Proposal{
		ID: "proposal-newer", JobID: jobID, Status: "approved", ProposalJSON: `{}`,
		// Equal timestamps are deliberately ambiguous at the persisted precision and
		// therefore fail closed rather than allowing either proposal to roll the other back.
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateProposal(ctx, older); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProposal(ctx, newer); err != nil {
		t.Fatal(err)
	}
	current := mustSaveChannel(t, s, approvalChannel("ch-superseded", jobID, 146))

	older.Status = "approved"
	older.ApprovedBy = "admin"
	olderChannel := current
	olderChannel.Name = "older lineup"
	title := provision.Record{
		Key: "movie:tmdb:146", Title: provision.Title{MediaType: provision.Movie, TMDBID: 146},
		State: provision.Wanted, Deadline: now,
	}
	if _, err := s.CommitProposalApproval(ctx, ProposalApproval{
		Proposal: older, Titles: []provision.Record{title}, Channel: olderChannel,
	}); !errors.Is(err, ErrProposalSuperseded) {
		t.Fatalf("older approval = %v, want ErrProposalSuperseded", err)
	}
	gotProposal, err := s.GetProposal(ctx, older.ID)
	if err != nil || gotProposal.Status != "submitted" {
		t.Fatalf("older proposal after rejection = (%+v, %v), want submitted", gotProposal, err)
	}
	if _, err := s.GetTitle(ctx, title.Key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("superseded approval inserted title: %v", err)
	}
	gotChannel, err := s.GetChannel(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotChannel.Name != current.Name || gotChannel.Revision != current.Revision {
		t.Fatalf("superseded approval changed channel: name=%q revision=%d", gotChannel.Name, gotChannel.Revision)
	}
}

func testProposalDecisionConcurrent(t *testing.T, newStore NewStoreFunc) {
	t.Run("ApproveApprove", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		now := time.Unix(1_800_000_000, 0).UTC()
		seed := Proposal{ID: "race-approve", JobID: "job", Status: "submitted", ProposalJSON: `{}`, CreatedAt: now, UpdatedAt: now}
		if err := s.CreateProposal(ctx, seed); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		type outcome struct {
			actor, key string
			err        error
		}
		out := make(chan outcome, 2)
		for i, actor := range []string{"admin-a", "admin-b"} {
			key := provision.Key([]string{"movie:tmdb:10", "movie:tmdb:20"}[i])
			channelID := []string{"ch-race-a", "ch-race-b"}[i]
			go func() {
				<-start
				p := seed
				p.Status, p.ApprovedBy, p.ProposalJSON = "approved", actor, `{"winner":"`+actor+`"}`
				_, err := s.CommitProposalApproval(ctx, ProposalApproval{Proposal: p, Titles: []provision.Record{{
					Key: key, Title: provision.Title{MediaType: provision.Movie, TMDBID: map[string]int{"admin-a": 10, "admin-b": 20}[actor]}, State: provision.Wanted, Deadline: now,
				}}, Channel: approvalChannel(channelID, p.JobID, 110+i)})
				out <- outcome{actor: actor, key: string(key), err: err}
			}()
		}
		close(start)
		first, second := <-out, <-out
		results := []outcome{first, second}
		wins := 0
		for _, result := range results {
			if result.err == nil {
				wins++
				got, _ := s.GetProposal(ctx, seed.ID)
				if got.ApprovedBy != result.actor || got.ProposalJSON != `{"winner":"`+result.actor+`"}` {
					t.Errorf("proposal does not match winner %s: %+v", result.actor, got)
				}
				if _, err := s.GetTitle(ctx, provision.Key(result.key)); err != nil {
					t.Errorf("winner title %s missing: %v", result.key, err)
				}
			} else if !errors.Is(result.err, ErrProposalNotSubmitted) {
				t.Errorf("loser error = %v", result.err)
			} else if _, err := s.GetTitle(ctx, provision.Key(result.key)); !errors.Is(err, ErrNotFound) {
				t.Errorf("loser title %s exists: %v", result.key, err)
			}
		}
		if wins != 1 {
			t.Errorf("successful approvals = %d, want 1", wins)
		}
		channels, err := s.ListChannels(ctx)
		if err != nil || len(channels) != 1 || channels[0].IntentRef != seed.JobID {
			t.Errorf("winning approval channels = (%+v, %v), want exactly one bound channel", channels, err)
		}
	})

	t.Run("ApproveDeny", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		now := time.Unix(1_800_000_000, 0).UTC()
		seed := Proposal{ID: "race-decision", JobID: "job", Status: "submitted", ProposalJSON: `{}`, CreatedAt: now, UpdatedAt: now}
		if err := s.CreateProposal(ctx, seed); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			p := seed
			p.Status, p.ApprovedBy = "approved", "approver"
			_, err := s.CommitProposalApproval(ctx, ProposalApproval{Proposal: p, Titles: []provision.Record{{
				Key: "movie:tmdb:30", Title: provision.Title{MediaType: provision.Movie, TMDBID: 30}, State: provision.Wanted, Deadline: now,
			}}, Channel: approvalChannel("ch-race-decision", p.JobID, 120)})
			errs <- err
		}()
		go func() {
			<-start
			p := seed
			p.Status, p.ApprovedBy, p.DenyReason = "denied", "denier", "no"
			errs <- s.CommitProposalDenial(ctx, p)
		}()
		close(start)
		a, b := <-errs, <-errs
		if (a == nil) == (b == nil) {
			t.Fatalf("decision errors = (%v, %v), want one winner", a, b)
		}
		loser := a
		if loser == nil {
			loser = b
		}
		if !errors.Is(loser, ErrProposalNotSubmitted) {
			t.Errorf("loser error = %v", loser)
		}
		got, err := s.GetProposal(ctx, seed.ID)
		if err != nil || (got.Status != "approved" && got.Status != "denied") {
			t.Errorf("terminal proposal = (%+v, %v)", got, err)
		}
		_, titleErr := s.GetTitle(ctx, "movie:tmdb:30")
		_, channelErr := s.GetChannel(ctx, "ch-race-decision")
		if got.Status == "approved" && titleErr != nil {
			t.Errorf("approved winner has no acquisition: %v", titleErr)
		}
		if got.Status == "approved" && channelErr != nil {
			t.Errorf("approved winner has no channel: %v", channelErr)
		}
		if got.Status == "denied" && !errors.Is(titleErr, ErrNotFound) {
			t.Errorf("denied winner has acquisition: %v", titleErr)
		}
		if got.Status == "denied" && !errors.Is(channelErr, ErrNotFound) {
			t.Errorf("denied winner has channel: %v", channelErr)
		}
	})
}

func testProposalApprovalOverlappingTitles(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	for _, id := range []string{"overlap-a", "overlap-b"} {
		if err := s.CreateProposal(ctx, Proposal{ID: id, JobID: id, Status: "submitted", ProposalJSON: `{}`, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	record := func(id int) provision.Record {
		return provision.Record{
			Key:   provision.Key("movie:tmdb:" + string(rune('0'+id))),
			Title: provision.Title{MediaType: provision.Movie, TMDBID: id},
			State: provision.Wanted, Deadline: now,
		}
	}
	one, two := record(1), record(2)
	start := make(chan struct{})
	type result struct {
		err error
	}
	results := make(chan result, 2)
	for i, tc := range []struct {
		id     string
		titles []provision.Record
	}{
		{id: "overlap-a", titles: []provision.Record{one, two}},
		{id: "overlap-b", titles: []provision.Record{two, one}},
	} {
		go func() {
			<-start
			p, err := s.GetProposal(ctx, tc.id)
			if err == nil {
				p.Status = "approved"
				_, err = s.CommitProposalApproval(ctx, ProposalApproval{
					Proposal: p,
					Titles:   tc.titles,
					Channel:  approvalChannel("ch-"+tc.id, p.JobID, 130+i),
				})
			}
			results <- result{err: err}
		}()
	}
	close(start)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil {
		t.Fatalf("overlapping approvals = (%v, %v), want both committed", a.err, b.err)
	}
	for _, rec := range []provision.Record{one, two} {
		if _, err := s.GetTitle(ctx, rec.Key); err != nil {
			t.Errorf("overlapping title %s missing: %v", rec.Key, err)
		}
	}
}

// testLookupByNonID pins the two "find the row by something other than its id" queries (V41).
//
// ⚠ Both replaced a full-table read plus a linear walk in Go, so the properties that matter are
// the ones a scan gave for free and a WHERE clause has to be told: not-found is ErrNotFound
// (not a zero struct), the filter actually discriminates, and — for proposals — NEWEST wins.
func testLookupByNonID(t *testing.T, newStore NewStoreFunc) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()

	// --- channels by intent_ref ---
	mk := func(id, intentRef string, number int) Channel {
		ch := Channel{}
		ch.ID = id
		ch.IntentRef = intentRef
		ch.Name = "Channel " + id
		ch.Number = number
		ch.Strategy = schedule.Sequential
		ch.Status = schedule.StatusLive
		return ch
	}
	for _, ch := range []Channel{mk("c1", "job-a", 1), mk("c2", "job-b", 2), mk("c3", "", 3)} {
		if _, err := s.SaveChannel(ctx, ch); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SaveChannel(ctx, mk("c4", "job-b", 4)); !errors.Is(err, ErrChannelConflict) {
		t.Errorf("duplicate intent write = %v, want ErrChannelConflict", err)
	}
	if _, err := s.SaveChannel(ctx, mk("c5", "job-c", 2)); !errors.Is(err, ErrChannelConflict) {
		t.Errorf("duplicate number write = %v, want ErrChannelConflict", err)
	}

	got, err := s.GetChannelByIntentRef(ctx, "job-b")
	if err != nil {
		t.Fatalf("by intent ref: %v", err)
	}
	if got.ID != "c2" {
		t.Errorf("intent job-b resolved to %q, want c2 — the filter is not discriminating", got.ID)
	}
	// ⚠ ErrNotFound, NOT a zero Channel. The former scan returned an empty struct for "no
	// match", whose blank ID reads as a valid channel to a caller that forgets to check.
	if _, err := s.GetChannelByIntentRef(ctx, "job-nope"); err != ErrNotFound {
		t.Errorf("unknown intent = %v, want ErrNotFound", err)
	}
	// A channel with NO intent ref must not answer an empty-string lookup as if it matched
	// something meaningful — "" is the default for every hand-made channel.
	if ch, err := s.GetChannelByIntentRef(ctx, ""); err == nil && ch.ID != "c3" {
		t.Errorf(`empty intent ref resolved to %q, want the un-bound channel or ErrNotFound`, ch.ID)
	}

	// --- proposals: newest approved for a job ---
	older := Proposal{
		ID: "p-old", JobID: "job-a", Status: "approved", ProposalJSON: `{"rationale":"first"}`,
		CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now,
	}
	newer := Proposal{
		ID: "p-new", JobID: "job-a", Status: "approved", ProposalJSON: `{"rationale":"refined"}`,
		CreatedAt: now, UpdatedAt: now,
	}
	// Same job, still SUBMITTED — must never be returned for an "approved" ask, however new.
	pending := Proposal{
		ID: "p-pending", JobID: "job-a", Status: "submitted", ProposalJSON: `{}`,
		CreatedAt: now.Add(time.Hour), UpdatedAt: now,
	}
	// A different job's approved proposal — the row a job_id-blind query would wrongly return.
	other := Proposal{
		ID: "p-other", JobID: "job-b", Status: "approved", ProposalJSON: `{}`,
		CreatedAt: now.Add(2 * time.Hour), UpdatedAt: now,
	}
	// Inserted oldest-last so a query relying on insertion order rather than created_at fails.
	for _, p := range []Proposal{other, pending, newer, older} {
		if err := s.CreateProposal(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	// ⚠ NEWEST wins, and it is load-bearing: a refine re-runs the channel's own job, so the
	// channel must bind to the LATEST approved lineup rather than the original one.
	p, err := s.NewestProposalByStatusForJob(ctx, "job-a", "approved")
	if err != nil {
		t.Fatalf("newest approved: %v", err)
	}
	if p.ID != "p-new" {
		t.Errorf("newest approved for job-a = %q, want p-new — a refine would bind the ORIGINAL lineup", p.ID)
	}
	// The status filter holds even though the submitted one is newer still.
	if p.Status != "approved" {
		t.Errorf("returned a %q proposal for an approved-only ask — the §8 gate leaks", p.Status)
	}
	// The job filter holds even though another job has a newer approved proposal.
	if p.JobID != "job-a" {
		t.Errorf("returned job %q, want job-a", p.JobID)
	}
	if _, err := s.NewestProposalByStatusForJob(ctx, "job-none", "approved"); err != ErrNotFound {
		t.Errorf("unknown job = %v, want ErrNotFound", err)
	}
	// A job with only a submitted proposal has no approved one — the binder refuses to build.
	if _, err := s.NewestProposalByStatusForJob(ctx, "job-b", "denied"); err != ErrNotFound {
		t.Errorf("status with no rows = %v, want ErrNotFound", err)
	}
}
