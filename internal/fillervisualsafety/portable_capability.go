package fillervisualsafety

import (
	"errors"
	"slices"
	"strings"
)

const (
	PortableCapabilitySchemaVersion   = 1
	PortableCapabilityContractVersion = "filler-visual-portable-capability-v1"
	MaximumPortableModels             = 4
	MaximumPortableOutputs            = 64
	MaximumPortableInputDimension     = 4_096
	MaximumPortableFrameLatencyMS     = int64(5 * 60 * 1_000)
)

// RuntimeIdentity binds the native inference artifact separately from the
// worker executable that loads it.
type RuntimeIdentity struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	ArtifactSHA256 string `json:"artifactSha256"`
}

// PortableModelArtifact binds upstream weights to the exact exported graph
// and its semantic input and output contract. OutputLabels remain ordered
// because their position assigns meaning to returned logits.
type PortableModelArtifact struct {
	ID             string   `json:"id"`
	SourceRevision string   `json:"sourceRevision"`
	WeightsSHA256  string   `json:"weightsSha256"`
	GraphSHA256    string   `json:"graphSha256"`
	ConfigSHA256   string   `json:"configSha256"`
	ExportSHA256   string   `json:"exportSha256"`
	InputWidth     int      `json:"inputWidth"`
	InputHeight    int      `json:"inputHeight"`
	OutputLabels   []string `json:"outputLabels"`
}

// PortableCapability is a model-agnostic handshake identity. It establishes
// what a local worker can run; it is not a certification or a safety verdict.
type PortableCapability struct {
	SchemaVersion          int                     `json:"schemaVersion"`
	ContractVersion        string                  `json:"contractVersion"`
	Implementation         string                  `json:"implementation"`
	Worker                 ToolIdentity            `json:"worker"`
	Runtime                RuntimeIdentity         `json:"runtime"`
	ExecutionProvider      string                  `json:"executionProvider"`
	ProviderOptionsSHA256  string                  `json:"providerOptionsSha256"`
	PreprocessorSHA256     string                  `json:"preprocessorSha256"`
	EvidenceContract       string                  `json:"evidenceContract"`
	MaximumFrameBytes      int64                   `json:"maximumFrameBytes"`
	MaximumFramesPerSource int                     `json:"maximumFramesPerSource"`
	MaximumFrameLatencyMS  int64                   `json:"maximumFrameLatencyMs"`
	Models                 []PortableModelArtifact `json:"models"`
	SHA256                 string                  `json:"sha256"`
}

// SealPortableCapability defensively copies and canonicalizes an adapter
// handshake so model declaration order cannot change its identity.
func SealPortableCapability(capability PortableCapability) (PortableCapability, error) {
	capability.SchemaVersion = PortableCapabilitySchemaVersion
	capability.ContractVersion = PortableCapabilityContractVersion
	capability.Models = clonePortableModels(capability.Models)
	slices.SortFunc(capability.Models, comparePortableModels)
	capability.SHA256 = PortableCapabilitySHA256(capability)
	if err := ValidatePortableCapability(capability); err != nil {
		return PortableCapability{}, err
	}
	return capability, nil
}

func ValidatePortableCapability(capability PortableCapability) error {
	if capability.SchemaVersion != PortableCapabilitySchemaVersion ||
		capability.ContractVersion != PortableCapabilityContractVersion ||
		!validIdentity(capability.Implementation) || !validTool(capability.Worker) ||
		!validRuntime(capability.Runtime) || !validExecutionProvider(capability.ExecutionProvider) ||
		!validDigest(capability.ProviderOptionsSHA256) || !validDigest(capability.PreprocessorSHA256) ||
		!validIdentity(capability.EvidenceContract) || capability.MaximumFrameBytes <= 0 ||
		capability.MaximumFrameBytes > MaximumFrameBytes || capability.MaximumFramesPerSource <= 0 ||
		capability.MaximumFramesPerSource > MaximumObservations || capability.MaximumFrameLatencyMS <= 0 ||
		capability.MaximumFrameLatencyMS > MaximumPortableFrameLatencyMS || len(capability.Models) == 0 ||
		len(capability.Models) > MaximumPortableModels || capability.SHA256 == "" ||
		capability.SHA256 != PortableCapabilitySHA256(capability) {
		return errors.New("portable visual-safety capability is invalid")
	}
	for index, model := range capability.Models {
		if !validPortableModel(model) || index > 0 && capability.Models[index-1].ID == model.ID {
			return errors.New("portable visual-safety model declaration is invalid")
		}
	}
	return nil
}

func PortableCapabilitySHA256(capability PortableCapability) string {
	capability.SHA256 = ""
	return digestJSON(capability)
}

func validRuntime(runtime RuntimeIdentity) bool {
	return validIdentity(runtime.Name) && validIdentity(runtime.Version) && validDigest(runtime.ArtifactSHA256)
}

func validExecutionProvider(provider string) bool {
	switch provider {
	case "cpu", "coreml", "cuda", "directml":
		return true
	default:
		return false
	}
}

func validPortableModel(model PortableModelArtifact) bool {
	if !validIdentity(model.ID) || !validIdentity(model.SourceRevision) || !validDigest(model.WeightsSHA256) ||
		!validDigest(model.GraphSHA256) || !validDigest(model.ConfigSHA256) || !validDigest(model.ExportSHA256) ||
		model.InputWidth <= 0 || model.InputWidth > MaximumPortableInputDimension ||
		model.InputHeight <= 0 || model.InputHeight > MaximumPortableInputDimension || len(model.OutputLabels) < 2 ||
		len(model.OutputLabels) > MaximumPortableOutputs {
		return false
	}
	seen := make(map[string]struct{}, len(model.OutputLabels))
	for _, label := range model.OutputLabels {
		if !validIdentity(label) {
			return false
		}
		key := strings.ToLower(label)
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func clonePortableModels(models []PortableModelArtifact) []PortableModelArtifact {
	cloned := slices.Clone(models)
	for index := range cloned {
		cloned[index].OutputLabels = slices.Clone(cloned[index].OutputLabels)
	}
	return cloned
}

func comparePortableModels(left, right PortableModelArtifact) int {
	if byID := strings.Compare(left.ID, right.ID); byID != 0 {
		return byID
	}
	return strings.Compare(left.SourceRevision, right.SourceRevision)
}
