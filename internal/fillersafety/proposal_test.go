package fillersafety

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeAcousticProposer struct {
	output  proposalOutput
	err     error
	request proposalRequest
	calls   int
}

func (f *fakeAcousticProposer) Propose(_ context.Context, request proposalRequest) (proposalOutput, error) {
	f.calls++
	f.request = request
	return f.output, f.err
}

func TestRunProposalNormalizesAndBindsCandidates(t *testing.T) {
	t.Parallel()
	plan := proposalTestPlan(t)
	identity := validProposerIdentityFixture()
	adapter := &fakeAcousticProposer{output: proposalOutput{
		Identity: identity,
		Complete: true,
		Candidates: []proposedInterval{
			{StartMS: 2_000, EndMS: 2_500},
			{StartMS: 100, EndMS: 800},
			{StartMS: 100, EndMS: 700},
		},
	}}

	got := runProposal(context.Background(), adapter, identity, plan)
	if got.ProposalState != ProposalComplete || len(got.Candidates) != 3 || adapter.calls != 1 {
		t.Fatalf("evidence=%+v calls=%d", got, adapter.calls)
	}
	wantIntervals := []proposedInterval{{StartMS: 100, EndMS: 700}, {StartMS: 100, EndMS: 800}, {StartMS: 2_000, EndMS: 2_500}}
	for index, want := range wantIntervals {
		candidate := got.Candidates[index]
		if candidate.StartMS != want.StartMS || candidate.EndMS != want.EndMS || candidate.ID != proposalCandidateID(plan.AuthoritySHA256, want) {
			t.Fatalf("candidate[%d]=%+v", index, candidate)
		}
	}
	if adapter.request.SourcePath != plan.SourcePath || adapter.request.AuthoritySHA256 != plan.AuthoritySHA256 || adapter.request.DurationMS != plan.Audio.EndMS {
		t.Fatalf("request did not bind verified plan: %+v", adapter.request)
	}
}

func TestRunProposalAcceptsCompleteNoCandidateOutput(t *testing.T) {
	t.Parallel()
	plan := proposalTestPlan(t)
	identity := validProposerIdentityFixture()
	got := runProposal(context.Background(), &fakeAcousticProposer{output: proposalOutput{Identity: identity, Complete: true}}, identity, plan)
	if got.ProposalState != ProposalComplete || len(got.Candidates) != 0 || got.Candidates == nil || got.Audio == nil || got.Video != VideoNotRun {
		t.Fatalf("unexpected no-candidate evidence: %+v", got)
	}
}

func TestRunProposalFailsClosedOnInvalidAdapterOutput(t *testing.T) {
	t.Parallel()
	identity := validProposerIdentityFixture()
	tooMany := make([]proposedInterval, maxProposedCandidates+1)
	for index := range tooMany {
		tooMany[index] = proposedInterval{StartMS: int64(index), EndMS: int64(index + 1)}
	}
	tests := []struct {
		name   string
		output proposalOutput
		err    error
	}{
		{name: "crash", output: proposalOutput{Identity: identity, Complete: true}, err: errors.New("private restricted value")},
		{name: "partial", output: proposalOutput{Identity: identity, Candidates: []proposedInterval{{StartMS: 10, EndMS: 20}}}},
		{name: "stale runtime", output: proposalOutput{Identity: mutateProposerIdentity(identity, "runtime"), Complete: true}},
		{name: "stale model", output: proposalOutput{Identity: mutateProposerIdentity(identity, "model"), Complete: true}},
		{name: "duplicate", output: proposalOutput{Identity: identity, Complete: true, Candidates: []proposedInterval{{StartMS: 10, EndMS: 20}, {StartMS: 10, EndMS: 20}}}},
		{name: "negative start", output: proposalOutput{Identity: identity, Complete: true, Candidates: []proposedInterval{{StartMS: -1, EndMS: 20}}}},
		{name: "inverted", output: proposalOutput{Identity: identity, Complete: true, Candidates: []proposedInterval{{StartMS: 20, EndMS: 10}}}},
		{name: "past source", output: proposalOutput{Identity: identity, Complete: true, Candidates: []proposedInterval{{StartMS: 10, EndMS: 30_001}}}},
		{name: "candidate ceiling", output: proposalOutput{Identity: identity, Complete: true, Candidates: tooMany}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := runProposal(context.Background(), &fakeAcousticProposer{output: test.output, err: test.err}, identity, proposalTestPlan(t))
			if got.ProposalState != ProposalFailed || len(got.Candidates) != 0 || len(got.Audio) != 0 || got.Video != VideoNotRun {
				t.Fatalf("invalid output escaped: %+v", got)
			}
			raw, err := json.Marshal(got)
			if err != nil || strings.Contains(string(raw), "private restricted value") {
				t.Fatalf("evidence leaked adapter details: %s err=%v", raw, err)
			}
		})
	}
}

func TestRunProposalDoesNotInvokeAdapterWithInvalidInputs(t *testing.T) {
	t.Parallel()
	identity := validProposerIdentityFixture()
	plan := proposalTestPlan(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name     string
		ctx      context.Context
		identity proposerIdentity
		plan     *CompleteMediaPlan
	}{
		{name: "nil context", identity: identity, plan: plan},
		{name: "cancelled context", ctx: cancelled, identity: identity, plan: plan},
		{name: "invalid authority", ctx: context.Background(), identity: proposerIdentity{}, plan: plan},
		{name: "nil plan", ctx: context.Background(), identity: identity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := &fakeAcousticProposer{}
			got := runProposal(test.ctx, adapter, test.identity, test.plan)
			if got.ProposalState != ProposalFailed || adapter.calls != 0 {
				t.Fatalf("evidence=%+v calls=%d", got, adapter.calls)
			}
		})
	}
}

func proposalTestPlan(t *testing.T) *CompleteMediaPlan {
	t.Helper()
	contents := []byte("complete source")
	path := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	authority := validSourceAuthority()
	authority.SourceSHA256, authority.SourceBytes = sourceIdentity(contents)
	plan, err := PlanCompleteMedia(context.Background(), SourceRequest{Authority: authority, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plan.Close() })
	return &plan
}

func validProposerIdentityFixture() proposerIdentity {
	return proposerIdentity{
		Implementation: "sherpa-kws-v1",
		Platform:       "linux/arm64",
		RuntimeVersion: "sherpa-1.12.28",
		RuntimeSHA256:  strings.Repeat("1", 64),
		ModelSHA256:    strings.Repeat("2", 64),
		ConfigSHA256:   strings.Repeat("3", 64),
	}
}

func mutateProposerIdentity(identity proposerIdentity, field string) proposerIdentity {
	if field == "runtime" {
		identity.RuntimeSHA256 = strings.Repeat("4", 64)
	} else {
		identity.ModelSHA256 = strings.Repeat("5", 64)
	}
	return identity
}
