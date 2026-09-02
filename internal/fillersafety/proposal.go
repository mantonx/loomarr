package fillersafety

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
)

const maxProposedCandidates = 4096

type proposerIdentity struct {
	Implementation string
	Platform       string
	RuntimeVersion string
	RuntimeSHA256  string
	ModelSHA256    string
	ConfigSHA256   string
}

type proposalRequest struct {
	AuthoritySHA256 string
	SourceSHA256    string
	SourceBytes     int64
	SourcePath      string
	DurationMS      int64
}

type proposedInterval struct {
	StartMS int64
	EndMS   int64
}

type proposalOutput struct {
	Identity   proposerIdentity
	Complete   bool
	Candidates []proposedInterval
}

type acousticProposer interface {
	Propose(context.Context, proposalRequest) (proposalOutput, error)
}

func runProposal(ctx context.Context, proposer acousticProposer, expected proposerIdentity, plan *CompleteMediaPlan) Evidence {
	failed := Evidence{ProposalState: ProposalFailed, Candidates: []Candidate{}, Audio: []AudioAssessment{}, Video: VideoNotRun}
	if ctx == nil || ctx.Err() != nil || proposer == nil || !validProposerIdentity(expected) || !validProposalPlan(plan) {
		return failed
	}
	output, err := proposer.Propose(ctx, proposalRequest{
		AuthoritySHA256: plan.AuthoritySHA256,
		SourceSHA256:    plan.SourceSHA256,
		SourceBytes:     plan.SourceBytes,
		SourcePath:      plan.SourcePath,
		DurationMS:      plan.Audio.EndMS,
	})
	if err != nil || !output.Complete || output.Identity != expected || len(output.Candidates) > maxProposedCandidates {
		return failed
	}

	intervals := slices.Clone(output.Candidates)
	slices.SortFunc(intervals, func(first, second proposedInterval) int {
		if first.StartMS != second.StartMS {
			return intCompare(first.StartMS, second.StartMS)
		}
		return intCompare(first.EndMS, second.EndMS)
	})
	candidates := make([]Candidate, 0, len(intervals))
	for index, interval := range intervals {
		if interval.StartMS < 0 || interval.EndMS <= interval.StartMS || interval.EndMS > plan.Audio.EndMS {
			return failed
		}
		if index > 0 && interval == intervals[index-1] {
			return failed
		}
		candidates = append(candidates, Candidate{
			ID:      proposalCandidateID(plan.AuthoritySHA256, interval),
			StartMS: interval.StartMS,
			EndMS:   interval.EndMS,
		})
	}
	return Evidence{ProposalState: ProposalComplete, Candidates: candidates, Audio: []AudioAssessment{}, Video: VideoNotRun}
}

func proposalCandidateID(authoritySHA256 string, interval proposedInterval) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("spoken-safety-candidate\x00%s\x00%d\x00%d", authoritySHA256, interval.StartMS, interval.EndMS)))
	return fmt.Sprintf("candidate-%x", sum[:12])
}

func intCompare(first, second int64) int {
	switch {
	case first < second:
		return -1
	case first > second:
		return 1
	default:
		return 0
	}
}
