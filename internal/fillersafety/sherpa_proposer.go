package fillersafety

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/loomarr/loomarr/internal/mediatools"
)

var _ acousticProposer = (*sherpaProposer)(nil)

func (p *sherpaProposer) Propose(ctx context.Context, request proposalRequest) (proposalOutput, error) {
	if p == nil {
		return proposalOutput{Candidates: []proposedInterval{}}, fmt.Errorf("spoken-safety acoustic proposal input is invalid")
	}
	failed := proposalOutput{Identity: p.identity, Candidates: []proposedInterval{}}
	if ctx == nil || ctx.Err() != nil || !validProposerIdentity(p.identity) || !validSherpaProposalRequest(request) || request.PolicySHA256 != p.policySHA {
		return failed, fmt.Errorf("spoken-safety acoustic proposal input is invalid")
	}
	ffmpegPath, err := exec.LookPath(mediatools.FFmpegOr(p.ffmpeg))
	if err != nil || fileSHA256(ffmpegPath) != request.FFmpeg.BinarySHA256 {
		return failed, fmt.Errorf("spoken-safety acoustic proposal media tool is invalid")
	}
	runDir, err := os.MkdirTemp(p.workspace, "run-")
	if err != nil {
		return failed, fmt.Errorf("create spoken-safety acoustic proposal workspace")
	}
	defer func() { _ = os.RemoveAll(filepath.Clean(runDir)) }()
	wavPath := filepath.Join(runDir, "source.wav")
	if err := mediatools.ExtractSpanWAV(ctx, ffmpegPath, request.SourcePath, 0, request.DurationMS, wavPath); err != nil {
		return failed, fmt.Errorf("extract complete spoken-safety acoustic proposal audio")
	}
	raw, err := runSherpaKeywordSpotter(ctx, p, wavPath, request.DurationMS)
	if err != nil {
		return failed, err
	}
	intervals, err := parseSherpaResults(raw, request.DurationMS, p.variants)
	if err != nil {
		return failed, err
	}
	return proposalOutput{Identity: p.identity, Complete: true, Candidates: intervals}, nil
}

func validSherpaProposalRequest(request proposalRequest) bool {
	return validSHA256(request.AuthoritySHA256) && validSHA256(request.PolicySHA256) && validSHA256(request.SourceSHA256) && request.SourceBytes > 0 && request.DurationMS > 0 && request.DurationMS <= maxSherpaSourceMS && filepath.IsAbs(request.SourcePath) && validToolIdentity(request.FFmpeg)
}

func fileSHA256(path string) string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 512<<20 {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, 512<<20+1))
	if err != nil || written != info.Size() {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}
