package fillervisualsafety

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"slices"
	"strings"
)

const (
	PortableFrameRequestSchemaVersion    = 1
	PortableFrameRequestContractVersion  = "filler-visual-portable-frame-request-v1"
	PortableFrameResponseSchemaVersion   = 1
	PortableFrameResponseContractVersion = "filler-visual-portable-frame-response-v1"
	MaximumAbsolutePortableLogit         = 1_000_000
)

type PixelEncoding string

const PixelRGB24 PixelEncoding = "rgb24"

// PortableFrameRequest binds one raw decoded frame to the exact capability and
// complete-source plan. Frame bytes travel in the framed binary payload and
// must reproduce Frame.SHA256 and Frame.Bytes.
type PortableFrameRequest struct {
	SchemaVersion         int           `json:"schemaVersion"`
	ContractVersion       string        `json:"contractVersion"`
	CapabilitySHA256      string        `json:"capabilitySha256"`
	CoveragePlanSHA256    string        `json:"coveragePlanSha256"`
	SourceAuthoritySHA256 string        `json:"sourceAuthoritySha256"`
	SourceSHA256          string        `json:"sourceSha256"`
	Frame                 FrameEvidence `json:"frame"`
	PixelEncoding         PixelEncoding `json:"pixelEncoding"`
	SHA256                string        `json:"sha256"`
}

// PortableModelScores preserves each model's declared output order. Threshold
// and policy interpretation remain in Loomarr and are not delegated to the worker.
type PortableModelScores struct {
	ModelID string    `json:"modelId"`
	Logits  []float64 `json:"logits"`
}

// PortableFrameResponse is a successful raw-logit response. Worker failures
// are transport failures and can never be represented as a successful score.
type PortableFrameResponse struct {
	SchemaVersion    int                   `json:"schemaVersion"`
	ContractVersion  string                `json:"contractVersion"`
	RequestSHA256    string                `json:"requestSha256"`
	CapabilitySHA256 string                `json:"capabilitySha256"`
	FrameSHA256      string                `json:"frameSha256"`
	ElapsedMS        int64                 `json:"elapsedMs"`
	Models           []PortableModelScores `json:"models"`
	SHA256           string                `json:"sha256"`
}

func SealPortableFrameRequest(capability PortableCapability, plan CoveragePlan, frame FrameEvidence, encoding PixelEncoding) (PortableFrameRequest, error) {
	request := PortableFrameRequest{
		SchemaVersion: PortableFrameRequestSchemaVersion, ContractVersion: PortableFrameRequestContractVersion,
		CapabilitySHA256: capability.SHA256, CoveragePlanSHA256: plan.SHA256,
		SourceAuthoritySHA256: plan.SourceAuthoritySHA256, SourceSHA256: plan.SourceSHA256,
		Frame: frame, PixelEncoding: encoding,
	}
	request.SHA256 = PortableFrameRequestSHA256(request)
	if err := ValidatePortableFrameRequest(capability, plan, request); err != nil {
		return PortableFrameRequest{}, err
	}
	return request, nil
}

func ValidatePortableFrameRequest(capability PortableCapability, plan CoveragePlan, request PortableFrameRequest) error {
	if ValidatePortableCapability(capability) != nil || ValidateCoveragePlan(plan) != nil ||
		request.SchemaVersion != PortableFrameRequestSchemaVersion ||
		request.ContractVersion != PortableFrameRequestContractVersion ||
		request.CapabilitySHA256 != capability.SHA256 || request.CoveragePlanSHA256 != plan.SHA256 ||
		request.SourceAuthoritySHA256 != plan.SourceAuthoritySHA256 || request.SourceSHA256 != plan.SourceSHA256 ||
		request.Frame.Ordinal < 0 || request.Frame.Ordinal >= len(plan.Points) ||
		!validFrame(plan, request.Frame.Ordinal, request.Frame) || request.PixelEncoding != PixelRGB24 ||
		len(plan.Points) > capability.MaximumFramesPerSource || request.Frame.Bytes > capability.MaximumFrameBytes ||
		!validRGB24Bytes(request.Frame.Width, request.Frame.Height, request.Frame.Bytes) ||
		request.SHA256 == "" || request.SHA256 != PortableFrameRequestSHA256(request) {
		return errors.New("portable visual-safety frame request is invalid")
	}
	return nil
}

func PortableFrameRequestSHA256(request PortableFrameRequest) string {
	request.SHA256 = ""
	return digestJSON(request)
}

// ValidatePortableFramePayload reproduces the metadata-bound frame before it
// can enter native inference.
func ValidatePortableFramePayload(request PortableFrameRequest, payload []byte) error {
	if int64(len(payload)) != request.Frame.Bytes {
		return errors.New("portable visual-safety frame payload size drifted")
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != request.Frame.SHA256 {
		return errors.New("portable visual-safety frame payload digest drifted")
	}
	return nil
}

func SealPortableFrameResponse(capability PortableCapability, plan CoveragePlan, request PortableFrameRequest, elapsedMS int64, models []PortableModelScores) (PortableFrameResponse, error) {
	response := PortableFrameResponse{
		SchemaVersion: PortableFrameResponseSchemaVersion, ContractVersion: PortableFrameResponseContractVersion,
		RequestSHA256: request.SHA256, CapabilitySHA256: capability.SHA256,
		FrameSHA256: request.Frame.SHA256, ElapsedMS: elapsedMS, Models: clonePortableScores(models),
	}
	slices.SortFunc(response.Models, func(left, right PortableModelScores) int {
		return strings.Compare(left.ModelID, right.ModelID)
	})
	response.SHA256 = PortableFrameResponseSHA256(response)
	if err := ValidatePortableFrameResponse(capability, plan, request, response); err != nil {
		return PortableFrameResponse{}, err
	}
	return response, nil
}

func ValidatePortableFrameResponse(capability PortableCapability, plan CoveragePlan, request PortableFrameRequest, response PortableFrameResponse) error {
	if ValidatePortableFrameRequest(capability, plan, request) != nil ||
		response.SchemaVersion != PortableFrameResponseSchemaVersion ||
		response.ContractVersion != PortableFrameResponseContractVersion || response.RequestSHA256 != request.SHA256 ||
		response.CapabilitySHA256 != capability.SHA256 || response.FrameSHA256 != request.Frame.SHA256 ||
		response.ElapsedMS <= 0 || response.ElapsedMS > capability.MaximumFrameLatencyMS ||
		len(response.Models) != len(capability.Models) || response.SHA256 == "" ||
		response.SHA256 != PortableFrameResponseSHA256(response) {
		return errors.New("portable visual-safety frame response is invalid")
	}
	for index, result := range response.Models {
		model := capability.Models[index]
		if result.ModelID != model.ID || len(result.Logits) != len(model.OutputLabels) {
			return errors.New("portable visual-safety model output does not match capability")
		}
		for _, logit := range result.Logits {
			if math.IsNaN(logit) || math.IsInf(logit, 0) || math.Abs(logit) > MaximumAbsolutePortableLogit {
				return errors.New("portable visual-safety model output is not finite and bounded")
			}
		}
	}
	return nil
}

func PortableFrameResponseSHA256(response PortableFrameResponse) string {
	response.SHA256 = ""
	return digestJSON(response)
}

func validRGB24Bytes(width, height int, bytes int64) bool {
	if width <= 0 || height <= 0 || bytes <= 0 || int64(width) > math.MaxInt64/int64(height) {
		return false
	}
	pixels := int64(width) * int64(height)
	return pixels <= math.MaxInt64/3 && pixels*3 == bytes
}

func clonePortableScores(models []PortableModelScores) []PortableModelScores {
	cloned := slices.Clone(models)
	for index := range cloned {
		cloned[index].Logits = slices.Clone(cloned[index].Logits)
	}
	return cloned
}
