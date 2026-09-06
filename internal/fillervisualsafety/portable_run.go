package fillervisualsafety

import (
	"errors"
	"slices"
)

const (
	PortableInferenceSchemaVersion   = 1
	PortableInferenceContractVersion = "filler-visual-portable-inference-v1"
)

// PortableInferenceEvidence retains raw model outputs without interpreting
// them as policy. It is valid only beside the exact complete-decode evidence.
type PortableInferenceEvidence struct {
	SchemaVersion          int                     `json:"schemaVersion"`
	ContractVersion        string                  `json:"contractVersion"`
	CapabilitySHA256       string                  `json:"capabilitySha256"`
	CoveragePlanSHA256     string                  `json:"coveragePlanSha256"`
	CoverageEvidenceSHA256 string                  `json:"coverageEvidenceSha256"`
	SourceAuthoritySHA256  string                  `json:"sourceAuthoritySha256"`
	SourceSHA256           string                  `json:"sourceSha256"`
	Responses              []PortableFrameResponse `json:"responses"`
	SHA256                 string                  `json:"sha256"`
}

// PortableExecution is one all-or-nothing complete decode and inference run.
type PortableExecution struct {
	Coverage  CoverageEvidence
	Inference PortableInferenceEvidence
}

func SealPortableInferenceEvidence(capability PortableCapability, plan CoveragePlan, coverage CoverageEvidence, responses []PortableFrameResponse) (PortableInferenceEvidence, error) {
	evidence := PortableInferenceEvidence{
		SchemaVersion: PortableInferenceSchemaVersion, ContractVersion: PortableInferenceContractVersion,
		CapabilitySHA256: capability.SHA256, CoveragePlanSHA256: plan.SHA256,
		CoverageEvidenceSHA256: coverage.SHA256, SourceAuthoritySHA256: plan.SourceAuthoritySHA256,
		SourceSHA256: plan.SourceSHA256, Responses: clonePortableResponses(responses),
	}
	evidence.SHA256 = PortableInferenceEvidenceSHA256(evidence)
	if err := ValidatePortableInferenceEvidence(capability, plan, coverage, evidence); err != nil {
		return PortableInferenceEvidence{}, err
	}
	return evidence, nil
}

func ValidatePortableInferenceEvidence(capability PortableCapability, plan CoveragePlan, coverage CoverageEvidence, evidence PortableInferenceEvidence) error {
	if ValidatePortableCapability(capability) != nil || ValidateCoverageEvidence(plan, coverage) != nil ||
		evidence.SchemaVersion != PortableInferenceSchemaVersion ||
		evidence.ContractVersion != PortableInferenceContractVersion || evidence.CapabilitySHA256 != capability.SHA256 ||
		evidence.CoveragePlanSHA256 != plan.SHA256 || evidence.CoverageEvidenceSHA256 != coverage.SHA256 ||
		evidence.SourceAuthoritySHA256 != plan.SourceAuthoritySHA256 || evidence.SourceSHA256 != plan.SourceSHA256 ||
		len(evidence.Responses) != len(coverage.Frames) || evidence.SHA256 == "" ||
		evidence.SHA256 != PortableInferenceEvidenceSHA256(evidence) {
		return errors.New("portable visual-safety inference evidence is invalid")
	}
	for index, frame := range coverage.Frames {
		request, err := SealPortableFrameRequest(capability, plan, frame, PixelRGB24)
		if err != nil || ValidatePortableFrameResponse(capability, plan, request, evidence.Responses[index]) != nil {
			return errors.New("portable visual-safety inference evidence is incomplete")
		}
	}
	return nil
}

func PortableInferenceEvidenceSHA256(evidence PortableInferenceEvidence) string {
	evidence.SHA256 = ""
	return digestJSON(evidence)
}

func clonePortableResponses(responses []PortableFrameResponse) []PortableFrameResponse {
	cloned := slices.Clone(responses)
	for index := range cloned {
		cloned[index].Models = clonePortableScores(cloned[index].Models)
	}
	return cloned
}
