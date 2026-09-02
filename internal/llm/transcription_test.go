package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAI_TranscribeAudioRequestsTimedSegments(t *testing.T) {
	var got transcriptionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"id":"stt-1","model":"openai/whisper-large-v3","text":"Buy now. Call today.","duration":4.2,"segments":[{"start":0.1,"end":1.5,"text":" Buy now. "},{"start":1.5,"end":4.2,"text":"Call today."}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`))
	}))
	defer srv.Close()

	client := NewOpenAI(srv.URL, "chat-model", "secret")
	result, err := client.TranscribeAudio(context.Background(), TranscriptionRequest{
		Model: "openai/whisper-large-v3", Audio: []byte("wav"), Format: "wav", Language: "en",
	})
	if err != nil {
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"text":"words, but no timing"}`))
	}))
	defer srv.Close()

	_, err := NewOpenAI(srv.URL, "stt", "").TranscribeAudio(context.Background(), TranscriptionRequest{Audio: []byte("wav")})
	if err == nil {
		t.Fatal("untimed transcription accepted")
	}
}

func TestOpenAI_TranscribeAudioReadsOpenRouterGenerationHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Generation-Id", "gen-header")
		_, _ = w.Write([]byte(`{"text":"words","segments":[{"start":0,"end":1,"text":"words"}],"usage":{"cost":0.00001}}`))
	}))
	defer srv.Close()
	result, err := NewOpenAIForProvider("openrouter", srv.URL, "stt", "").TranscribeAudio(context.Background(), TranscriptionRequest{Audio: []byte("wav")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Attribution.GenerationID != "gen-header" || result.Attribution.Charge == nil {
		t.Fatalf("attribution = %+v", result.Attribution)
	}
}
