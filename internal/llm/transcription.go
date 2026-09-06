package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Hosted speech-to-text over the OpenAI-compatible transcription endpoint (§10). OpenRouter uses
// the same base URL and bearer key as chat/vision, but this is a distinct capability and model:
// an STT model cannot answer the grounded chat loop, and a chat model does not imply timestamps.
// OpenRouter's transcription endpoint does not currently apply chat provider-routing controls;
// callers must not infer pinned-provider, disabled-fallback, or per-request ZDR authority from a
// route configured on OpenAI. Capability snapshots support diagnostics, not that inference.

type TranscriptionRequest struct {
	Model    string
	Audio    []byte
	Format   string
	Language string
}

type TranscriptionSegment struct {
	StartMs int64
	EndMs   int64
	Text    string
}

type TranscriptionResult struct {
	Segments    []TranscriptionSegment
	Attribution Attribution
}

type transcriptionRequest struct {
	Model                  string             `json:"model"`
	InputAudio             transcriptionAudio `json:"input_audio"`
	Language               string             `json:"language,omitempty"`
	ResponseFormat         string             `json:"response_format"`
	TimestampGranularities []string           `json:"timestamp_granularities"`
}

type transcriptionAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

type transcriptionResponse struct {
	ID                 string             `json:"id"`
	Model              string             `json:"model"`
	Text               string             `json:"text"`
	Duration           float64            `json:"duration"`
	Usage              openAIUsage        `json:"usage"`
	OpenRouterMetadata openRouterMetadata `json:"openrouter_metadata"`
	Segments           []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// TranscribeAudio requests verbose JSON because compilation rescue needs timed utterances. A
// provider that returns only plain text is rejected rather than assigning invented timestamps;
// the caller can retry or fall back to review without manufacturing cut evidence.
func (o *OpenAI) TranscribeAudio(ctx context.Context, req TranscriptionRequest) (TranscriptionResult, error) {
	if len(req.Audio) == 0 {
		return TranscriptionResult{}, fmt.Errorf("transcription request carries no audio")
	}
	started := time.Now()
	model := req.Model
	if model == "" {
		model = o.model
	}
	format := req.Format
	if format == "" {
		format = "wav"
	}
	body, err := json.Marshal(transcriptionRequest{
		Model: model,
		InputAudio: transcriptionAudio{
			Data: base64.StdEncoding.EncodeToString(req.Audio), Format: format,
		},
		Language: req.Language, ResponseFormat: "verbose_json",
		TimestampGranularities: []string{"segment"},
	})
	if err != nil {
		return TranscriptionResult{}, fmt.Errorf("marshal transcription request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.baseURL+"/audio/transcriptions", bytes.NewReader(body))
	if err != nil {
		return TranscriptionResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	o.addMetadataHeader(httpReq)
	if o.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	resp, err := o.http.Do(httpReq)
	if err != nil {
		return TranscriptionResult{}, fmt.Errorf("audio transcription: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(io.LimitReader(resp.Body, 512))
		return TranscriptionResult{}, fmt.Errorf("audio transcription: status %d: %s", resp.StatusCode, strings.TrimSpace(buf.String()))
	}
	var out transcriptionResponse
	if err := decodeOpenAIJSON(resp, &out, "transcription response"); err != nil {
		return TranscriptionResult{}, err
	}
	if out.Error != nil {
		return TranscriptionResult{}, fmt.Errorf("audio transcription: %s", out.Error.Message)
	}
	segments := make([]TranscriptionSegment, 0, len(out.Segments))
	for _, seg := range out.Segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" || seg.End <= seg.Start {
			continue
		}
		segments = append(segments, TranscriptionSegment{
			StartMs: int64(seg.Start * 1000), EndMs: int64(seg.End * 1000), Text: text,
		})
	}
	if len(segments) == 0 && strings.TrimSpace(out.Text) != "" {
		return TranscriptionResult{}, fmt.Errorf("audio transcription returned text without segment timestamps")
	}
	if o.metrics != nil {
		o.metrics.LLMTokens(out.Usage.PromptTokens, out.Usage.CompletionTokens)
	}
	generationID := strings.TrimSpace(out.ID)
	if generationID == "" {
		generationID = strings.TrimSpace(resp.Header.Get("X-Generation-Id"))
	}
	return TranscriptionResult{
		Segments: segments,
		Attribution: attributionFromWire(o.provider, model, generationID, out.Model, out.Usage,
			out.OpenRouterMetadata, []string{"audio", "text"}, time.Since(started)),
	}, nil
}
