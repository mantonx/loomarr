package fillersafety

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCompleteAudioWindowProposerCoversEverySourceMillisecondExactlyOnce(t *testing.T) {
	t.Parallel()
	proposer, identity := newCompleteAudioWindowProposer()
	if !validProposerIdentity(identity) || identity.Kind != proposerDeterministic || identity.Platform != "" ||
		identity.RuntimeVersion != "" || identity.RuntimeSHA256 != "" || identity.ModelSHA256 != "" ||
		identity.ConfigSHA256 != "3c8ee411b7881ee78918b0c8ff3780ada4779f56e8618b889b500a10e652aba8" {
		t.Fatalf("identity=%+v", identity)
	}
	for _, durationMS := range []int64{1, completeAudioWindowMS, completeAudioWindowMS + 1, completeAudioWindowMS*3 + 127} {
		t.Run(durationTestName(durationMS), func(t *testing.T) {
			request := validCompleteAudioWindowRequestFixture(durationMS)
			output, err := proposer(context.Background(), request)
			if err != nil || !output.Complete || output.Identity != identity {
				t.Fatalf("output=%+v err=%v", output, err)
			}
			startMS := int64(0)
			for _, interval := range output.Candidates {
				if interval.StartMS != startMS || interval.EndMS <= interval.StartMS ||
					interval.EndMS-interval.StartMS > completeAudioWindowMS {
					t.Fatalf("duration=%d intervals=%+v", durationMS, output.Candidates)
				}
				startMS = interval.EndMS
			}
			if startMS != durationMS {
				t.Fatalf("duration=%d ended=%d intervals=%+v", durationMS, startMS, output.Candidates)
			}
		})
	}
}

func TestCompleteAudioWindowProposerFlowsThroughCanonicalCandidateIdentity(t *testing.T) {
	t.Parallel()
	plan := proposalTestPlan(t)
	proposer, identity := newCompleteAudioWindowProposer()
	evidence := runProposal(context.Background(), proposer, identity, plan)
	if evidence.ProposalState != ProposalComplete || len(evidence.Candidates) != 2 ||
		evidence.Candidates[0].StartMS != 0 || evidence.Candidates[0].EndMS != completeAudioWindowMS ||
		evidence.Candidates[1].StartMS != completeAudioWindowMS || evidence.Candidates[1].EndMS != plan.Audio.EndMS {
		t.Fatalf("evidence=%+v", evidence)
	}
	for _, candidate := range evidence.Candidates {
		want := proposalCandidateID(plan.AuthoritySHA256, proposedInterval{StartMS: candidate.StartMS, EndMS: candidate.EndMS})
		if candidate.ID != want {
			t.Fatalf("candidate=%+v want id=%s", candidate, want)
		}
	}
}

func TestCompleteAudioWindowProposerRejectsRatherThanTruncates(t *testing.T) {
	t.Parallel()
	proposer, _ := newCompleteAudioWindowProposer()
	maximum := validCompleteAudioWindowRequestFixture(maxCompleteAudioWindowMS)
	if output, err := proposer(context.Background(), maximum); err != nil || !output.Complete || len(output.Candidates) != maxProposedCandidates || output.Candidates[len(output.Candidates)-1].EndMS != maxCompleteAudioWindowMS {
		t.Fatalf("maximum output candidates=%d complete=%v err=%v", len(output.Candidates), output.Complete, err)
	}
	request := validCompleteAudioWindowRequestFixture(maxCompleteAudioWindowMS + 1)
	if output, err := proposer(context.Background(), request); !errors.Is(err, ErrEvaluationInvalid) || output.Complete || len(output.Candidates) != 0 {
		t.Fatalf("output=%+v err=%v", output, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	request.DurationMS = completeAudioWindowMS
	if _, err := proposer(cancelled, request); !errors.Is(err, ErrEvaluationInvalid) {
		t.Fatalf("cancelled err=%v", err)
	}
}

func TestDeterministicProposerIdentityRejectsExternalArtifacts(t *testing.T) {
	t.Parallel()
	_, identity := newCompleteAudioWindowProposer()
	identity.ModelSHA256 = strings.Repeat("e", 64)
	if validProposerIdentity(identity) {
		t.Fatalf("deterministic identity accepted a model artifact: %+v", identity)
	}
	identity = completeAudioWindowIdentity()
	identity.Kind = proposerKind("future")
	if validProposerIdentity(identity) {
		t.Fatalf("identity accepted an unknown strategy kind: %+v", identity)
	}
}

func validCompleteAudioWindowRequestFixture(durationMS int64) proposalRequest {
	return proposalRequest{
		AuthoritySHA256: strings.Repeat("a", 64), PolicySHA256: strings.Repeat("b", 64),
		SourceSHA256: strings.Repeat("c", 64), SourceBytes: 1024,
		SourcePath: "/private/source.mp4", DurationMS: durationMS,
		FFmpeg: ToolIdentity{Version: "ffmpeg-8", BinarySHA256: strings.Repeat("d", 64)},
	}
}

func durationTestName(durationMS int64) string {
	switch durationMS {
	case 1:
		return "one_millisecond"
	case completeAudioWindowMS:
		return "one_window"
	case completeAudioWindowMS + 1:
		return "one_window_plus_tail"
	default:
		return "several_windows"
	}
}
