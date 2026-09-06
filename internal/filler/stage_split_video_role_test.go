package filler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/llm"
	"github.com/loomarr/loomarr/internal/mediatools"
)

type fixedVideoDeriver struct {
	result mediatools.HostedVideoDerivative
	err    error
	file   string
	span   [2]int64
}

func (d *fixedVideoDeriver) HostedVideoIn(_ context.Context, file string, startMS, endMS int64) (mediatools.HostedVideoDerivative, error) {
	d.file, d.span = file, [2]int64{startMS, endMS}
	return d.result, d.err
}

type fixedVideoProvider struct {
	response llm.Response
	err      error
	calls    int
	prompt   string
	input    llm.VideoInput
}

func (p *fixedVideoProvider) AskAboutVideo(_ context.Context, prompt string, input llm.VideoInput) (llm.Response, error) {
	p.calls++
	p.prompt, p.input = prompt, input
	return p.response, p.err
}

func TestDirectVideoRoleEscalatorReturnsExactAttributedEvidence(t *testing.T) {
	video := []byte("bounded-mp4")
	deriver := &fixedVideoDeriver{result: mediatools.HostedVideoDerivative{
		MP4: video, MIMEType: "video/mp4", SHA256: structureBytesSHA256(video), StartMS: 10_000, EndMS: 40_000,
	}}
	provider := &fixedVideoProvider{response: llm.Response{
		Content: `{"role":"commercial","reason":"a product is demonstrated, priced, and sold"}`,
		Attribution: llm.Attribution{
			RequestedProvider: "openrouter", ResolvedProvider: "openrouter", RequestedModel: "video-model", ResolvedModel: "video-model",
			Modalities: []string{"text", "video", "audio"}, Tokens: llm.TokenUsage{Prompt: 20, Completion: 8, Video: 5, Audio: 2},
			Charge: &llm.Money{Amount: "0.003", Currency: "USD"}, Latency: 250 * time.Millisecond, Attempts: 1, GenerationID: "generation-1",
		},
	}}
	at := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	evidence, err := NewDirectVideoRoleEscalator(deriver, provider).EscalateRole(
		context.Background(), structureSource(60_000), "/evidence/master.mp4",
		SplitSegment{StartMs: 10_000, EndMs: 40_000}, at,
	)
	if err != nil {
		t.Fatal(err)
	}
	if evidence == nil || evidence.Role != SegmentRoleCommercial || evidence.VideoSHA256 != structureBytesSHA256(video) || len(evidence.FrameSHA256) != 0 || evidence.AssessedAt != at || evidence.Charge == nil || evidence.Charge.Amount != "0.003" {
		t.Fatalf("evidence = %+v", evidence)
	}
	if deriver.file != "/evidence/master.mp4" || deriver.span != [2]int64{10_000, 40_000} || provider.calls != 1 || provider.input.DurationMS != 30_000 || string(provider.input.Data) != string(video) || !strings.Contains(provider.prompt, "complete visual sequence") {
		t.Fatalf("deriver=%+v provider=%+v", deriver, provider)
	}
}

func TestDirectVideoRoleEscalatorFailsClosedBeforeOrAfterProvider(t *testing.T) {
	video := []byte("bounded-mp4")
	validDerivative := mediatools.HostedVideoDerivative{MP4: video, MIMEType: "video/mp4", SHA256: structureBytesSHA256(video), StartMS: 0, EndMS: 30_000}
	tests := []struct {
		name     string
		deriver  mediatools.HostedVideoDerivative
		response string
		segment  SplitSegment
		wantErr  string
		wantNil  bool
	}{
		{name: "overlong span", deriver: validDerivative, response: `{}`, segment: SplitSegment{StartMs: 0, EndMs: mediatools.HostedVideoMaxDurationMS + 1}, wantErr: "span is invalid"},
		{name: "derivative hash drift", deriver: mediatools.HostedVideoDerivative{MP4: video, MIMEType: "video/mp4", SHA256: strings.Repeat("a", 64), StartMS: 0, EndMS: 30_000}, response: `{}`, segment: SplitSegment{StartMs: 0, EndMs: 30_000}, wantErr: "derivative authority"},
		{name: "unknown output field", deriver: validDerivative, response: `{"role":"commercial","reason":"sale","confidence":99}`, segment: SplitSegment{StartMs: 0, EndMs: 30_000}, wantErr: "decode direct-video role"},
		{name: "unsupported role", deriver: validDerivative, response: `{"role":"advert-ish","reason":"unclear"}`, segment: SplitSegment{StartMs: 0, EndMs: 30_000}, wantNil: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deriver := &fixedVideoDeriver{result: test.deriver}
			provider := &fixedVideoProvider{response: llm.Response{Content: test.response, Attribution: llm.Attribution{
				RequestedProvider: "openrouter", ResolvedProvider: "openrouter", RequestedModel: "video-model", ResolvedModel: "video-model",
				Modalities: []string{"text", "video"}, Attempts: 1,
			}}}
			evidence, err := NewDirectVideoRoleEscalator(deriver, provider).EscalateRole(context.Background(), structureSource(120_000), "source.mp4", test.segment, time.Now())
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("evidence=%+v error=%v, want %q", evidence, err, test.wantErr)
			}
			if test.wantNil && (err != nil || evidence != nil) {
				t.Fatalf("evidence=%+v error=%v, want unresolved claim", evidence, err)
			}
			if test.name == "overlong span" && (deriver.file != "" || provider.calls != 0) {
				t.Fatal("invalid span reached media or provider")
			}
		})
	}
}

func TestDirectVideoRoleEscalatorPreservesOperationalFailure(t *testing.T) {
	deriver := &fixedVideoDeriver{err: errors.New("encoder unavailable")}
	provider := &fixedVideoProvider{}
	_, err := NewDirectVideoRoleEscalator(deriver, provider).EscalateRole(
		context.Background(), structureSource(60_000), "source.mp4", SplitSegment{StartMs: 0, EndMs: 30_000}, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "encoder unavailable") || provider.calls != 0 {
		t.Fatalf("error=%v calls=%d", err, provider.calls)
	}
}
