package fillervisualsafety_test

import (
	"crypto/sha256"
	"fmt"
	"math"
	"testing"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestPortableFrameProtocolBindsRawFrameAndEveryModelOutput(t *testing.T) {
	t.Parallel()

	capability, plan, frame, payload := portableProtocolFixture(t)
	request, err := fillervisualsafety.SealPortableFrameRequest(capability, plan, frame, fillervisualsafety.PixelRGB24)
	if err != nil {
		t.Fatalf("SealPortableFrameRequest() error = %v", err)
	}
	if err := fillervisualsafety.ValidatePortableFramePayload(request, payload); err != nil {
		t.Fatalf("ValidatePortableFramePayload() error = %v", err)
	}
	response, err := fillervisualsafety.SealPortableFrameResponse(capability, plan, request, 17, []fillervisualsafety.PortableModelScores{
		{ModelID: "marqo-nsfw-384", Logits: []float64{1.25, -0.5}},
		{ModelID: "freepik-nsfw", Logits: []float64{-1, 0.25, 1.5, 3}},
	})
	if err != nil {
		t.Fatalf("SealPortableFrameResponse() error = %v", err)
	}
	if response.Models[0].ModelID != "freepik-nsfw" || response.Models[1].ModelID != "marqo-nsfw-384" {
		t.Fatalf("model results are not canonical: %+v", response.Models)
	}
	if err := fillervisualsafety.ValidatePortableFrameResponse(capability, plan, request, response); err != nil {
		t.Fatalf("ValidatePortableFrameResponse() error = %v", err)
	}
	if request.SHA256 != fillervisualsafety.PortableFrameRequestSHA256(request) ||
		response.SHA256 != fillervisualsafety.PortableFrameResponseSHA256(response) {
		t.Fatal("portable frame protocol digest does not reproduce")
	}
}

func TestPortableFrameRequestRejectsCoverageAndResourceDrift(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*fillervisualsafety.PortableCapability, *fillervisualsafety.FrameEvidence, *fillervisualsafety.PixelEncoding){
		"wrong ordinal": func(_ *fillervisualsafety.PortableCapability, frame *fillervisualsafety.FrameEvidence, _ *fillervisualsafety.PixelEncoding) {
			frame.Ordinal++
		},
		"wrong timestamp": func(_ *fillervisualsafety.PortableCapability, frame *fillervisualsafety.FrameEvidence, _ *fillervisualsafety.PixelEncoding) {
			frame.RequestedMS++
		},
		"frame exceeds worker": func(capability *fillervisualsafety.PortableCapability, frame *fillervisualsafety.FrameEvidence, _ *fillervisualsafety.PixelEncoding) {
			capability.MaximumFrameBytes = frame.Bytes - 1
		},
		"plan exceeds worker": func(capability *fillervisualsafety.PortableCapability, _ *fillervisualsafety.FrameEvidence, _ *fillervisualsafety.PixelEncoding) {
			capability.MaximumFramesPerSource = 1
		},
		"unsupported encoding": func(_ *fillervisualsafety.PortableCapability, _ *fillervisualsafety.FrameEvidence, encoding *fillervisualsafety.PixelEncoding) {
			*encoding = "jpeg"
		},
		"non-rgb byte count": func(_ *fillervisualsafety.PortableCapability, frame *fillervisualsafety.FrameEvidence, _ *fillervisualsafety.PixelEncoding) {
			frame.Bytes--
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			capability, plan, frame, _ := portableProtocolFixture(t)
			encoding := fillervisualsafety.PixelRGB24
			mutate(&capability, &frame, &encoding)
			capability.SHA256 = fillervisualsafety.PortableCapabilitySHA256(capability)
			if _, err := fillervisualsafety.SealPortableFrameRequest(capability, plan, frame, encoding); err == nil {
				t.Fatal("SealPortableFrameRequest() error = nil")
			}
		})
	}
}

func TestPortableFramePayloadRejectsSizeAndDigestDrift(t *testing.T) {
	t.Parallel()

	capability, plan, frame, payload := portableProtocolFixture(t)
	request, err := fillervisualsafety.SealPortableFrameRequest(capability, plan, frame, fillervisualsafety.PixelRGB24)
	if err != nil {
		t.Fatal(err)
	}
	if err := fillervisualsafety.ValidatePortableFramePayload(request, payload[:len(payload)-1]); err == nil {
		t.Fatal("short payload was accepted")
	}
	payload[0] ^= 0xff
	if err := fillervisualsafety.ValidatePortableFramePayload(request, payload); err == nil {
		t.Fatal("digest-drifted payload was accepted")
	}
}

func TestPortableFrameResponseRejectsMissingDuplicateAndNonFiniteOutputs(t *testing.T) {
	t.Parallel()

	capability, plan, frame, _ := portableProtocolFixture(t)
	request, err := fillervisualsafety.SealPortableFrameRequest(capability, plan, frame, fillervisualsafety.PixelRGB24)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]fillervisualsafety.PortableModelScores{
		"missing": {
			{ModelID: "marqo-nsfw-384", Logits: []float64{1, 0}},
		},
		"duplicate": {
			{ModelID: "marqo-nsfw-384", Logits: []float64{1, 0}},
			{ModelID: "marqo-nsfw-384", Logits: []float64{1, 0}},
		},
		"wrong output count": {
			{ModelID: "freepik-nsfw", Logits: []float64{1, 0}},
			{ModelID: "marqo-nsfw-384", Logits: []float64{1, 0}},
		},
		"non-finite": {
			{ModelID: "freepik-nsfw", Logits: []float64{math.NaN(), 0, 0, 0}},
			{ModelID: "marqo-nsfw-384", Logits: []float64{1, 0}},
		},
	}
	for name, models := range tests {
		name, models := name, models
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := fillervisualsafety.SealPortableFrameResponse(capability, plan, request, 1, models); err == nil {
				t.Fatal("SealPortableFrameResponse() error = nil")
			}
		})
	}
	if _, err := fillervisualsafety.SealPortableFrameResponse(capability, plan, request, capability.MaximumFrameLatencyMS+1, validPortableScores()); err == nil {
		t.Fatal("over-budget latency was accepted")
	}
}

func portableProtocolFixture(t *testing.T) (fillervisualsafety.PortableCapability, fillervisualsafety.CoveragePlan, fillervisualsafety.FrameEvidence, []byte) {
	t.Helper()
	capability, err := fillervisualsafety.SealPortableCapability(portableCapabilityInput())
	if err != nil {
		t.Fatal(err)
	}
	authority := visualAuthority(t, 3_000)
	plan, err := fillervisualsafety.PlanCoverage(authority, visualProfile(t))
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, plan.Video.Width*plan.Video.Height*3)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	digest := sha256.Sum256(payload)
	frame := fillervisualsafety.FrameEvidence{
		Ordinal: plan.Points[0].Ordinal, RequestedMS: plan.Points[0].RequestedMS, ObservedMS: plan.Points[0].RequestedMS,
		SHA256: fmt.Sprintf("%x", digest), Bytes: int64(len(payload)), Width: plan.Video.Width, Height: plan.Video.Height,
	}
	return capability, plan, frame, payload
}

func validPortableScores() []fillervisualsafety.PortableModelScores {
	return []fillervisualsafety.PortableModelScores{
		{ModelID: "freepik-nsfw", Logits: []float64{0, 0, 0, 0}},
		{ModelID: "marqo-nsfw-384", Logits: []float64{0, 0}},
	}
}
