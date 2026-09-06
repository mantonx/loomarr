package fillervisualsafety_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestPortableWireRoundTripsSerialRequestsAndResponse(t *testing.T) {
	t.Parallel()

	capability, plan, frame, payload := portableProtocolFixture(t)
	request, err := fillervisualsafety.SealPortableFrameRequest(capability, plan, frame, fillervisualsafety.PixelRGB24)
	if err != nil {
		t.Fatal(err)
	}
	var requests bytes.Buffer
	if err := fillervisualsafety.WritePortableFrameRequest(&requests, request, payload); err != nil {
		t.Fatalf("WritePortableFrameRequest(first) error = %v", err)
	}
	if err := fillervisualsafety.WritePortableFrameRequest(&requests, request, payload); err != nil {
		t.Fatalf("WritePortableFrameRequest(second) error = %v", err)
	}
	for index := 0; index < 2; index++ {
		gotRequest, gotPayload, err := fillervisualsafety.ReadPortableFrameRequest(&requests, capability.MaximumFrameBytes)
		if err != nil {
			t.Fatalf("ReadPortableFrameRequest(%d) error = %v", index, err)
		}
		if gotRequest.SHA256 != request.SHA256 || !bytes.Equal(gotPayload, payload) {
			t.Fatalf("request %d drifted across wire", index)
		}
	}
	if requests.Len() != 0 {
		t.Fatalf("wire retained %d unread bytes", requests.Len())
	}

	response, err := fillervisualsafety.SealPortableFrameResponse(capability, plan, request, 2, validPortableScores())
	if err != nil {
		t.Fatal(err)
	}
	var responses bytes.Buffer
	if err := fillervisualsafety.WritePortableFrameResponse(&responses, response); err != nil {
		t.Fatalf("WritePortableFrameResponse() error = %v", err)
	}
	gotResponse, err := fillervisualsafety.ReadPortableFrameResponse(&responses)
	if err != nil {
		t.Fatalf("ReadPortableFrameResponse() error = %v", err)
	}
	if gotResponse.SHA256 != response.SHA256 || responses.Len() != 0 {
		t.Fatal("response drifted across wire")
	}
}

func TestPortableWireRejectsMalformedOrOversizedHeadersBeforeAllocation(t *testing.T) {
	t.Parallel()

	tests := map[string]func([]byte){
		"wrong magic":      func(header []byte) { header[0] = 'X' },
		"wrong version":    func(header []byte) { header[4]++ },
		"wrong kind":       func(header []byte) { header[5] = 2 },
		"nonzero reserved": func(header []byte) { header[6] = 1 },
		"empty metadata":   func(header []byte) { binary.BigEndian.PutUint32(header[8:12], 0) },
		"oversized metadata": func(header []byte) {
			binary.BigEndian.PutUint32(header[8:12], fillervisualsafety.MaximumPortableMetadataBytes+1)
		},
		"oversized payload": func(header []byte) { binary.BigEndian.PutUint32(header[12:16], 65) },
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			header := portableRequestHeader(2, 64)
			mutate(header)
			if _, _, err := fillervisualsafety.ReadPortableFrameRequest(bytes.NewReader(header), 64); err == nil {
				t.Fatal("ReadPortableFrameRequest() error = nil")
			}
		})
	}
}

func TestPortableWireRejectsUnknownMetadataAndResponsePayload(t *testing.T) {
	t.Parallel()

	unknown := []byte(`{"unknown":true}`)
	header := portableRequestHeader(len(unknown), 0)
	if _, _, err := fillervisualsafety.ReadPortableFrameRequest(bytes.NewReader(append(header, unknown...)), 1); err == nil {
		t.Fatal("unknown request metadata was accepted")
	}

	responseHeader := portableRequestHeader(2, 1)
	responseHeader[5] = 2
	responseWire := append(responseHeader, '{', '}', 0)
	if _, err := fillervisualsafety.ReadPortableFrameResponse(bytes.NewReader(responseWire)); err == nil {
		t.Fatal("response payload was accepted")
	}
}

func TestPortableWireRejectsTruncatedPayload(t *testing.T) {
	t.Parallel()

	capability, plan, frame, payload := portableProtocolFixture(t)
	request, err := fillervisualsafety.SealPortableFrameRequest(capability, plan, frame, fillervisualsafety.PixelRGB24)
	if err != nil {
		t.Fatal(err)
	}
	var wire bytes.Buffer
	if err := fillervisualsafety.WritePortableFrameRequest(&wire, request, payload); err != nil {
		t.Fatal(err)
	}
	raw := wire.Bytes()
	if _, _, err := fillervisualsafety.ReadPortableFrameRequest(bytes.NewReader(raw[:len(raw)-1]), capability.MaximumFrameBytes); err == nil {
		t.Fatal("truncated payload was accepted")
	}
}

func portableRequestHeader(metadataBytes, payloadBytes int) []byte {
	header := make([]byte, 16)
	copy(header[0:4], []byte("LVSP"))
	header[4] = fillervisualsafety.PortableWireProtocolVersion
	header[5] = 1
	binary.BigEndian.PutUint32(header[8:12], uint32(metadataBytes))
	binary.BigEndian.PutUint32(header[12:16], uint32(payloadBytes))
	return header
}
