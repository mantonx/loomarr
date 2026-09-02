package fillersafety

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/loomarr/loomarr/internal/mediatools"
)

type candidateAudioExtractor interface {
	Extract(context.Context, *CompleteMediaPlan, Candidate) ([]byte, error)
}

type ffmpegCandidateAudioExtractor struct {
	path string
}

var _ candidateAudioExtractor = ffmpegCandidateAudioExtractor{}

func (e ffmpegCandidateAudioExtractor) Extract(ctx context.Context, plan *CompleteMediaPlan, candidate Candidate) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil || !validProposalPlan(plan) || candidate.StartMS < 0 || candidate.EndMS <= candidate.StartMS || candidate.EndMS > plan.Audio.EndMS || candidate.EndMS-candidate.StartMS > maxProposedIntervalMS {
		return nil, fmt.Errorf("spoken-safety candidate extraction input is invalid")
	}
	dir, err := os.MkdirTemp("", "loomarr-spoken-safety-audio-")
	if err != nil {
		return nil, fmt.Errorf("create private spoken-safety audio workspace")
	}
	defer func() { _ = os.RemoveAll(dir) }()
	destination := filepath.Join(dir, "candidate.wav")
	if err := mediatools.ExtractSpanWAV(ctx, e.path, plan.SourcePath, candidate.StartMS, candidate.EndMS, destination); err != nil {
		return nil, fmt.Errorf("extract spoken-safety candidate audio")
	}
	file, err := os.Open(destination)
	if err != nil {
		return nil, fmt.Errorf("open spoken-safety candidate audio")
	}
	defer func() { _ = file.Close() }()
	wav, err := io.ReadAll(io.LimitReader(file, maxCandidateAudioBytes+1))
	if err != nil || len(wav) < 12 || len(wav) > maxCandidateAudioBytes || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, fmt.Errorf("spoken-safety candidate audio is invalid")
	}
	return wav, nil
}
