package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/testkit/httpfixture"
)

func TestOpenAI_TranscribeAudioRequestsTimedSegments(t *testing.T) {
	transport := httpfixture.NewScriptedTransport(httpfixture.Step{Response: transcriptionHTTPResponse(http.StatusOK, `{"id":"stt-1","model":"openai/whisper-large-v3","text":"Buy now. Call today.","duration":4.2,"segments":[{"start":0.1,"end":1.5,"text":" Buy now. "},{"start":1.5,"end":4.2,"text":"Call today."}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`)})
	client := NewOpenAI("https://openai.invalid/v1", "chat-model", "secret")
	client.http = &http.Client{Transport: transport}
	result, err := client.TranscribeAudio(context.Background(), TranscriptionRequest{
		Model: "openai/whisper-large-v3", Audio: []byte("wav"), Format: "wav", Language: "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := transport.Requests()
	if len(requests) != 1 || requests[0].URL != "https://openai.invalid/v1/audio/transcriptions" || requests[0].Header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("requests = %+v", requests)
	}
	var got transcriptionRequest
	if err := json.Unmarshal(requests[0].Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "openai/whisper-large-v3" || got.ResponseFormat != "verbose_json" {
		t.Fatalf("request = %+v", got)
	}
	if len(got.TimestampGranularities) != 1 || got.TimestampGranularities[0] != "segment" {
		t.Fatalf("timestamp granularities = %v", got.TimestampGranularities)
	}
	if got.InputAudio.Data != "d2F2" || got.InputAudio.Format != "wav" {
		t.Fatalf("audio = %+v", got.InputAudio)
	}
	if len(result.Segments) != 2 || result.Segments[0].StartMs != 100 || result.Segments[1].EndMs != 4200 {
		t.Fatalf("segments = %+v", result.Segments)
	}
	if result.Attribution.Tokens.Prompt != 9 || result.Attribution.Tokens.Completion != 4 || result.Attribution.GenerationID != "stt-1" {
		t.Fatalf("attribution = %+v", result.Attribution)
	}
}

func TestOpenAI_TranscribeAudioRejectsUntimedText(t *testing.T) {
	transport := httpfixture.NewScriptedTransport(httpfixture.Step{Response: transcriptionHTTPResponse(http.StatusOK, `{"text":"words, but no timing"}`)})
	client := NewOpenAI("https://openai.invalid/v1", "stt", "")
	client.http = &http.Client{Transport: transport}
	_, err := client.TranscribeAudio(context.Background(), TranscriptionRequest{Audio: []byte("wav")})
	if err == nil {
		t.Fatal("untimed transcription accepted")
	}
}

func TestOpenAI_TranscribeAudioReadsOpenRouterGenerationHeader(t *testing.T) {
	transport := httpfixture.NewScriptedTransport(httpfixture.Step{Response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Generation-Id": []string{"gen-header"}}, Body: io.NopCloser(strings.NewReader(`{"text":"words","segments":[{"start":0,"end":1,"text":"words"}],"usage":{"cost":0.00001}}`))}})
	client := NewOpenAIForProvider("openrouter", "https://openrouter.ai/api/v1", "stt", "")
	client.http = &http.Client{Transport: transport}
	result, err := client.TranscribeAudio(context.Background(), TranscriptionRequest{Audio: []byte("wav")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Attribution.GenerationID != "gen-header" || result.Attribution.Charge == nil {
		t.Fatalf("attribution = %+v", result.Attribution)
	}
}

func transcriptionHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
