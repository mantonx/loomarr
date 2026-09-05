package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFirstTransportChunkIgnoresEmptyDeliveryChunks(t *testing.T) {
	chunks := make(chan []byte, 2)
	chunks <- nil
	chunks <- []byte("transport")

	got, err := firstTransportChunk(t.Context(), chunks, time.Second)
	if err != nil || string(got) != "transport" {
		t.Fatalf("firstTransportChunk = %q, %v; want transport", got, err)
	}
}

func TestFirstTransportChunkClassifiesStartupFailures(t *testing.T) {
	closed := make(chan []byte)
	close(closed)
	if _, err := firstTransportChunk(t.Context(), closed, time.Second); !errors.Is(err, errPlayoutStartupEnded) {
		t.Fatalf("closed presentation error = %v, want %v", err, errPlayoutStartupEnded)
	}

	if _, err := firstTransportChunk(t.Context(), make(chan []byte), time.Millisecond); !errors.Is(err, errPlayoutStartupTimeout) {
		t.Fatalf("silent presentation error = %v, want %v", err, errPlayoutStartupTimeout)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := firstTransportChunk(ctx, make(chan []byte), time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled presentation error = %v, want context cancellation", err)
	}
}
