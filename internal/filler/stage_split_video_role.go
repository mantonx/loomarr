package filler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/mediatools"
)

const structureVideoRolePromptVersion = "filler-segment-video-role-v1"

// SegmentRoleEscalator is the split stage's complete interface to temporal model evidence.
// The implementation owns derivative extraction, request limits, strict decoding, and evidence
// attribution; the caller knows only that an exact unresolved span may gain one role observation.
type SegmentRoleEscalator interface {
	EscalateRole(context.Context, SplitSourceAsset, string, SplitSegment, time.Time) (*StructureRoleEvidence, error)
}

// DirectVideoRoleEscalator uses the already-bounded hosted-video derivative and provider seams.
// It is constructed independently of SegmentVision so a certified route can be enabled without
// changing the frame/taxonomy provider.
type DirectVideoRoleEscalator struct {
	deriver  mediatools.HostedVideoDeriver
	provider llm.VideoProvider
}

func NewDirectVideoRoleEscalator(deriver mediatools.HostedVideoDeriver, provider llm.VideoProvider) *DirectVideoRoleEscalator {
	return &DirectVideoRoleEscalator{deriver: deriver, provider: provider}
}

func (e *DirectVideoRoleEscalator) EscalateRole(ctx context.Context, source SplitSourceAsset, file string, segment SplitSegment, assessedAt time.Time) (*StructureRoleEvidence, error) {
	if e == nil || e.deriver == nil || e.provider == nil {
		return nil, fmt.Errorf("source structure: direct-video role escalation is unavailable")
	}
	if err := source.validate(); err != nil || strings.TrimSpace(file) == "" || segment.StartMs < 0 || segment.EndMs <= segment.StartMs || segment.EndMs > source.DurationMs || segment.EndMs-segment.StartMs > mediatools.HostedVideoMaxDurationMS {
		return nil, fmt.Errorf("source structure: direct-video role span is invalid")
	}
	derivative, err := e.deriver.HostedVideoIn(ctx, file, segment.StartMs, segment.EndMs)
	if err != nil {
		return nil, err
	}
	if derivative.StartMS != segment.StartMs || derivative.EndMS != segment.EndMs || derivative.MIMEType != "video/mp4" || len(derivative.MP4) == 0 || len(derivative.MP4) > llm.MaxHostedVideoBytes || derivative.SHA256 != structureBytesSHA256(derivative.MP4) {
		return nil, fmt.Errorf("source structure: direct-video derivative authority is invalid")
	}
	prompt := structureVideoRolePrompt()
	response, err := e.provider.AskAboutVideo(ctx, prompt, llm.VideoInput{
		Data: derivative.MP4, MIMEType: derivative.MIMEType, DurationMS: segment.EndMs - segment.StartMs,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Role   string `json:"role"`
		Reason string `json:"reason"`
	}
	decoder := json.NewDecoder(strings.NewReader(response.Content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return nil, fmt.Errorf("source structure: decode direct-video role: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("source structure: direct-video role contains trailing JSON")
	}
	role := StructureSegmentRole(strings.TrimSpace(out.Role))
	if !validStructureSegmentRole(role) || strings.TrimSpace(out.Reason) == "" {
		return nil, nil
	}
	var charge *StructureRoleCharge
	if response.Attribution.Charge != nil {
		charge = &StructureRoleCharge{Amount: response.Attribution.Charge.Amount, Currency: response.Attribution.Charge.Currency}
	}
	tokens := response.Attribution.Tokens
	evidence, err := NewStructureRoleEvidence(StructureRoleEvidenceInput{
		Source: source, StartMs: segment.StartMs, EndMs: segment.EndMs, Role: role, Reason: out.Reason,
		Video: derivative.MP4, PromptVersion: structureVideoRolePromptVersion, Prompt: prompt, Response: response.Content,
		RequestedProvider: response.Attribution.RequestedProvider, ResolvedProvider: response.Attribution.ResolvedProvider,
		RequestedModel: response.Attribution.RequestedModel, ResolvedModel: response.Attribution.ResolvedModel,
		Modalities: response.Attribution.Modalities,
		Tokens: StructureRoleTokenUsage{
			Prompt: tokens.Prompt, Completion: tokens.Completion, Reasoning: tokens.Reasoning,
			Cached: tokens.Cached, CacheWrite: tokens.CacheWrite, Image: tokens.Image, Audio: tokens.Audio, Video: tokens.Video,
		},
		Charge: charge, LatencyMs: response.Attribution.Latency.Milliseconds(), Attempts: response.Attribution.Attempts,
		GenerationID: response.Attribution.GenerationID, AssessedAt: assessedAt,
	})
	if err != nil {
		return nil, err
	}
	return &evidence, nil
}

func structureVideoRolePrompt() string {
	return `Classify the exact bounded video as one role. Use the complete visual sequence and spoken audio; do not infer from filename or duration. Return JSON only: {"role":"commercial|promo|bumper|station_id|psa|trailer|programme_fragment|non_filler|ambiguous|unusable","reason":"brief evidence-grounded explanation"}. Use ambiguous when the supplied video cannot distinguish roles and unusable only when media failure prevents assessment.`
}

var _ SegmentRoleEscalator = (*DirectVideoRoleEscalator)(nil)
