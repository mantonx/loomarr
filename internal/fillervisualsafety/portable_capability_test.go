package fillervisualsafety_test

import (
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func TestPortableCapabilityCanonicalizesModelsWithoutChangingOutputOrder(t *testing.T) {
	t.Parallel()

	input := portableCapabilityInput()
	input.Models[0], input.Models[1] = input.Models[1], input.Models[0]
	got, err := fillervisualsafety.SealPortableCapability(input)
	if err != nil {
		t.Fatalf("SealPortableCapability() error = %v", err)
	}
	if got.Models[0].ID != "freepik-nsfw" || got.Models[1].ID != "marqo-nsfw-384" {
		t.Fatalf("model order = %q, %q", got.Models[0].ID, got.Models[1].ID)
	}
	if got.Models[0].OutputLabels[0] != "neutral" || got.Models[0].OutputLabels[3] != "high" {
		t.Fatalf("semantic output order changed: %v", got.Models[0].OutputLabels)
	}
	input.Models[0].OutputLabels[0] = "mutated"
	if got.Models[1].OutputLabels[0] != "NSFW" {
		t.Fatalf("sealed capability aliases caller labels: %v", got.Models[1].OutputLabels)
	}
	if err := fillervisualsafety.ValidatePortableCapability(got); err != nil {
		t.Fatalf("ValidatePortableCapability() error = %v", err)
	}
	if got.SHA256 != fillervisualsafety.PortableCapabilitySHA256(got) {
		t.Fatal("portable capability digest does not reproduce")
	}
}

func TestPortableCapabilityRejectsAmbiguousOrIncompleteArtifacts(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*fillervisualsafety.PortableCapability){
		"duplicate model": func(value *fillervisualsafety.PortableCapability) {
			value.Models[1] = value.Models[0]
		},
		"duplicate model identity": func(value *fillervisualsafety.PortableCapability) {
			value.Models[1].ID = value.Models[0].ID
		},
		"duplicate output label": func(value *fillervisualsafety.PortableCapability) {
			value.Models[0].OutputLabels = []string{"neutral", "neutral"}
		},
		"missing source revision": func(value *fillervisualsafety.PortableCapability) {
			value.Models[0].SourceRevision = ""
		},
		"missing export recipe": func(value *fillervisualsafety.PortableCapability) {
			value.Models[0].ExportSHA256 = ""
		},
		"unbounded frame bytes": func(value *fillervisualsafety.PortableCapability) {
			value.MaximumFrameBytes = fillervisualsafety.MaximumFrameBytes + 1
		},
		"unbounded source frames": func(value *fillervisualsafety.PortableCapability) {
			value.MaximumFramesPerSource = fillervisualsafety.MaximumObservations + 1
		},
		"unbounded frame latency": func(value *fillervisualsafety.PortableCapability) {
			value.MaximumFrameLatencyMS = fillervisualsafety.MaximumPortableFrameLatencyMS + 1
		},
		"unbounded model input": func(value *fillervisualsafety.PortableCapability) {
			value.Models[0].InputWidth = fillervisualsafety.MaximumPortableInputDimension + 1
		},
		"mutable network provider": func(value *fillervisualsafety.PortableCapability) {
			value.ExecutionProvider = "https://example.invalid/infer"
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := portableCapabilityInput()
			mutate(&input)
			if _, err := fillervisualsafety.SealPortableCapability(input); err == nil {
				t.Fatal("SealPortableCapability() error = nil")
			}
		})
	}
}

func TestPortableCapabilityDigestBindsSemanticOutputOrder(t *testing.T) {
	t.Parallel()

	left, err := fillervisualsafety.SealPortableCapability(portableCapabilityInput())
	if err != nil {
		t.Fatalf("SealPortableCapability(left) error = %v", err)
	}
	rightInput := portableCapabilityInput()
	rightInput.Models[0].OutputLabels[0], rightInput.Models[0].OutputLabels[1] =
		rightInput.Models[0].OutputLabels[1], rightInput.Models[0].OutputLabels[0]
	right, err := fillervisualsafety.SealPortableCapability(rightInput)
	if err != nil {
		t.Fatalf("SealPortableCapability(right) error = %v", err)
	}
	if left.SHA256 == right.SHA256 {
		t.Fatal("capability digest ignored semantic output order")
	}
}

func portableCapabilityInput() fillervisualsafety.PortableCapability {
	return fillervisualsafety.PortableCapability{
		Implementation: "portable-worker-v1",
		Worker: fillervisualsafety.ToolIdentity{
			Name: "loomarr-visual-worker", Version: "development-v1", ExecutableSHA256: repeatedDigest("a"),
		},
		Runtime: fillervisualsafety.RuntimeIdentity{
			Name: "onnxruntime", Version: "1.29.0", ArtifactSHA256: repeatedDigest("b"),
		},
		ExecutionProvider:      "cpu",
		ProviderOptionsSHA256:  repeatedDigest("c"),
		PreprocessorSHA256:     repeatedDigest("d"),
		EvidenceContract:       "portable-raw-logits-v1",
		MaximumFrameBytes:      24 << 20,
		MaximumFramesPerSource: 3_600,
		MaximumFrameLatencyMS:  30_000,
		Models: []fillervisualsafety.PortableModelArtifact{
			{
				ID: "marqo-nsfw-384", SourceRevision: "0c26ec22111b83f106d72a55f611ec35962bcb65",
				WeightsSHA256: repeatedDigest("e"), GraphSHA256: repeatedDigest("f"), ConfigSHA256: repeatedDigest("1"),
				ExportSHA256: repeatedDigest("2"), InputWidth: 384, InputHeight: 384,
				OutputLabels: []string{"NSFW", "SFW"},
			},
			{
				ID: "freepik-nsfw", SourceRevision: "15b85477e4fd2000db76ae9aae0f89a72f95e2e3",
				WeightsSHA256: repeatedDigest("3"), GraphSHA256: repeatedDigest("4"), ConfigSHA256: repeatedDigest("5"),
				ExportSHA256: repeatedDigest("6"), InputWidth: 448, InputHeight: 448,
				OutputLabels: []string{"neutral", "low", "medium", "high"},
			},
		},
	}
}

func repeatedDigest(character string) string {
	return strings.Repeat(character, 64)
}
