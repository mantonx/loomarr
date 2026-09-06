package filler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	StructureRoleEvidenceSchemaVersion   = 2
	StructureRoleEvidenceContractVersion = "filler-structure-role-evidence-v2"
	structureRoleReasonMaximumBytes      = 2_048
)

// StructureRoleTokenUsage is the complete provider-reported token envelope for one bounded
// role assessment. Zero is retained as unavailable-or-zero; it is never estimated.
type StructureRoleTokenUsage struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Reasoning  int `json:"reasoning"`
	Cached     int `json:"cached"`
	CacheWrite int `json:"cacheWrite"`
	Image      int `json:"image"`
	Audio      int `json:"audio"`
	Video      int `json:"video"`
}

// StructureRoleCharge retains a provider-reported charge exactly as text. nil on the evidence
// means the provider did not report a charge; Loomarr does not turn that absence into zero cost.
type StructureRoleCharge struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// StructureRoleEvidence binds one semantic role judgement to exact source bytes, one interval,
// either ordered frame bytes or one bounded video derivative, the prompt/response, and the
// provider's attribution envelope.
type StructureRoleEvidence struct {
	SchemaVersion     int                     `json:"schemaVersion"`
	ContractVersion   string                  `json:"contractVersion"`
	Source            SplitSourceAsset        `json:"source"`
	StartMs           int64                   `json:"startMs"`
	EndMs             int64                   `json:"endMs"`
	Role              StructureSegmentRole    `json:"role"`
	Reason            string                  `json:"reason"`
	FrameSHA256       []string                `json:"frameSha256"`
	VideoSHA256       string                  `json:"videoSha256,omitempty"`
	PromptVersion     string                  `json:"promptVersion"`
	PromptSHA256      string                  `json:"promptSha256"`
	RequestSHA256     string                  `json:"requestSha256"`
	ResponseSHA256    string                  `json:"responseSha256"`
	RequestedProvider string                  `json:"requestedProvider"`
	ResolvedProvider  string                  `json:"resolvedProvider"`
	RequestedModel    string                  `json:"requestedModel"`
	ResolvedModel     string                  `json:"resolvedModel"`
	Modalities        []string                `json:"modalities"`
	Tokens            StructureRoleTokenUsage `json:"tokens"`
	Charge            *StructureRoleCharge    `json:"charge,omitempty"`
	LatencyMs         int64                   `json:"latencyMs"`
	Attempts          int                     `json:"attempts"`
	GenerationID      string                  `json:"generationId"`
	AssessedAt        time.Time               `json:"assessedAt"`
	SHA256            string                  `json:"sha256"`
}

type StructureRoleEvidenceInput struct {
	Source            SplitSourceAsset
	StartMs           int64
	EndMs             int64
	Role              StructureSegmentRole
	Reason            string
	Frames            [][]byte
	Video             []byte
	PromptVersion     string
	Prompt            string
	Response          string
	RequestedProvider string
	ResolvedProvider  string
	RequestedModel    string
	ResolvedModel     string
	Modalities        []string
	Tokens            StructureRoleTokenUsage
	Charge            *StructureRoleCharge
	LatencyMs         int64
	Attempts          int
	GenerationID      string
	AssessedAt        time.Time
}

// NewStructureRoleEvidence is the only constructor for model-authored role evidence. It hashes
// raw frames and the raw response immediately; those potentially large bytes never enter a split
// proposal, while their ordered identities remain bound to the judgement.
func NewStructureRoleEvidence(input StructureRoleEvidenceInput) (StructureRoleEvidence, error) {
	if strings.TrimSpace(input.Prompt) == "" || strings.TrimSpace(input.Response) == "" {
		return StructureRoleEvidence{}, errors.New("source structure: role evidence prompt and response are required")
	}
	if (len(input.Frames) == 0) == (len(input.Video) == 0) {
		return StructureRoleEvidence{}, errors.New("source structure: role evidence requires exactly one frame or video input")
	}
	frames := make([]string, 0, len(input.Frames))
	for _, frame := range input.Frames {
		if len(frame) == 0 {
			return StructureRoleEvidence{}, errors.New("source structure: role evidence contains an empty frame")
		}
		frames = append(frames, structureBytesSHA256(frame))
	}
	promptSHA := structureBytesSHA256([]byte(input.Prompt))
	evidence := StructureRoleEvidence{
		SchemaVersion: StructureRoleEvidenceSchemaVersion, ContractVersion: StructureRoleEvidenceContractVersion,
		Source: input.Source, StartMs: input.StartMs, EndMs: input.EndMs, Role: input.Role,
		Reason: strings.TrimSpace(input.Reason), FrameSHA256: frames,
		PromptVersion: strings.TrimSpace(input.PromptVersion), PromptSHA256: promptSHA,
		VideoSHA256:       structureOptionalBytesSHA256(input.Video),
		ResponseSHA256:    structureBytesSHA256([]byte(input.Response)),
		RequestedProvider: strings.TrimSpace(input.RequestedProvider), ResolvedProvider: strings.TrimSpace(input.ResolvedProvider),
		RequestedModel: strings.TrimSpace(input.RequestedModel), ResolvedModel: strings.TrimSpace(input.ResolvedModel),
		Modalities: slices.Clone(input.Modalities), Tokens: input.Tokens, Charge: cloneStructureRoleCharge(input.Charge),
		LatencyMs: input.LatencyMs, Attempts: input.Attempts, GenerationID: strings.TrimSpace(input.GenerationID),
		AssessedAt: input.AssessedAt.UTC(),
	}
	slices.Sort(evidence.Modalities)
	evidence.Modalities = slices.Compact(evidence.Modalities)
	evidence.RequestSHA256 = StructureRoleRequestSHA256(evidence)
	evidence.SHA256 = StructureRoleEvidenceSHA256(evidence)
	if err := ValidateStructureRoleEvidence(evidence); err != nil {
		return StructureRoleEvidence{}, err
	}
	return evidence, nil
}

func NewStructureRoleObservation(id string, evidence StructureRoleEvidence) (StructureObservation, error) {
	if err := ValidateStructureRoleEvidence(evidence); err != nil {
		return StructureObservation{}, err
	}
	return StructureObservation{
		ID: strings.TrimSpace(id), Kind: ObservationSegmentRole, Effect: ObservationContextOnly,
		StartMs: evidence.StartMs, EndMs: evidence.EndMs,
		Producer:       fmt.Sprintf("%s/%s@%s", evidence.ResolvedProvider, evidence.ResolvedModel, evidence.PromptVersion),
		EvidenceSHA256: evidence.SHA256, RoleEvidence: &evidence,
	}, nil
}

func ValidateStructureRoleEvidence(evidence StructureRoleEvidence) error {
	if evidence.SchemaVersion != StructureRoleEvidenceSchemaVersion || evidence.ContractVersion != StructureRoleEvidenceContractVersion {
		return errors.New("source structure: unsupported role evidence contract")
	}
	if err := evidence.Source.validate(); err != nil || evidence.StartMs < 0 || evidence.EndMs <= evidence.StartMs || evidence.EndMs > evidence.Source.DurationMs {
		return errors.New("source structure: role evidence source or interval is invalid")
	}
	if !validStructureSegmentRole(evidence.Role) || evidence.Reason != strings.TrimSpace(evidence.Reason) || evidence.Reason == "" || len(evidence.Reason) > structureRoleReasonMaximumBytes {
		return errors.New("source structure: role evidence claim is invalid")
	}
	frameEvidence := len(evidence.FrameSHA256) > 0 && evidence.VideoSHA256 == ""
	videoEvidence := len(evidence.FrameSHA256) == 0 && isContentHash(evidence.VideoSHA256)
	if !frameEvidence && !videoEvidence || len(evidence.FrameSHA256) > VisionKeyframes {
		return errors.New("source structure: role evidence media binding is invalid")
	}
	for _, digest := range evidence.FrameSHA256 {
		if !isContentHash(digest) {
			return errors.New("source structure: role evidence frame digest is invalid")
		}
	}
	if evidence.PromptVersion != strings.TrimSpace(evidence.PromptVersion) || evidence.PromptVersion == "" || !isContentHash(evidence.PromptSHA256) || !isContentHash(evidence.RequestSHA256) || !isContentHash(evidence.ResponseSHA256) {
		return errors.New("source structure: role evidence request identity is invalid")
	}
	if evidence.RequestSHA256 != StructureRoleRequestSHA256(evidence) {
		return errors.New("source structure: role evidence request digest does not match")
	}
	canonicalModalities := slices.Compact(slices.Clone(evidence.Modalities))
	mediaModalityValid := frameEvidence && slices.Contains(evidence.Modalities, "image") && !slices.Contains(evidence.Modalities, "video") || videoEvidence && slices.Contains(evidence.Modalities, "video") && !slices.Contains(evidence.Modalities, "image")
	if evidence.RequestedProvider != strings.TrimSpace(evidence.RequestedProvider) || evidence.ResolvedProvider != strings.TrimSpace(evidence.ResolvedProvider) || evidence.RequestedModel != strings.TrimSpace(evidence.RequestedModel) || evidence.ResolvedModel != strings.TrimSpace(evidence.ResolvedModel) || evidence.RequestedProvider == "" || evidence.ResolvedProvider == "" || evidence.RequestedModel == "" || evidence.ResolvedModel == "" || !slices.IsSorted(evidence.Modalities) || !slices.Equal(evidence.Modalities, canonicalModalities) || !slices.Contains(evidence.Modalities, "text") || !mediaModalityValid {
		return errors.New("source structure: role evidence provider identity is incomplete")
	}
	if evidence.Attempts < 1 || evidence.LatencyMs < 0 || !validStructureRoleTokens(evidence.Tokens) || evidence.AssessedAt.IsZero() {
		return errors.New("source structure: role evidence accounting is invalid")
	}
	if evidence.GenerationID != strings.TrimSpace(evidence.GenerationID) {
		return errors.New("source structure: role evidence generation identity is not canonical")
	}
	if evidence.Charge != nil && (evidence.Charge.Amount != strings.TrimSpace(evidence.Charge.Amount) || evidence.Charge.Currency != strings.TrimSpace(evidence.Charge.Currency) || evidence.Charge.Amount == "" || evidence.Charge.Currency == "") {
		return errors.New("source structure: role evidence charge is incomplete")
	}
	if evidence.SHA256 == "" || evidence.SHA256 != StructureRoleEvidenceSHA256(evidence) {
		return errors.New("source structure: role evidence digest does not match")
	}
	return nil
}

func StructureRoleRequestSHA256(evidence StructureRoleEvidence) string {
	request := struct {
		Source        SplitSourceAsset `json:"source"`
		StartMs       int64            `json:"startMs"`
		EndMs         int64            `json:"endMs"`
		FrameSHA256   []string         `json:"frameSha256"`
		VideoSHA256   string           `json:"videoSha256,omitempty"`
		PromptVersion string           `json:"promptVersion"`
		PromptSHA256  string           `json:"promptSha256"`
	}{evidence.Source, evidence.StartMs, evidence.EndMs, evidence.FrameSHA256, evidence.VideoSHA256, evidence.PromptVersion, evidence.PromptSHA256}
	raw, err := json.Marshal(request)
	if err != nil {
		return ""
	}
	return structureBytesSHA256(raw)
}

func StructureRoleEvidenceSHA256(evidence StructureRoleEvidence) string {
	evidence.SHA256 = ""
	raw, err := json.Marshal(evidence)
	if err != nil {
		return ""
	}
	return structureBytesSHA256(raw)
}

func structureBytesSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func structureOptionalBytesSHA256(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	return structureBytesSHA256(raw)
}

func validStructureRoleTokens(tokens StructureRoleTokenUsage) bool {
	return tokens.Prompt >= 0 && tokens.Completion >= 0 && tokens.Reasoning >= 0 && tokens.Cached >= 0 &&
		tokens.CacheWrite >= 0 && tokens.Image >= 0 && tokens.Audio >= 0 && tokens.Video >= 0
}

func cloneStructureRoleCharge(charge *StructureRoleCharge) *StructureRoleCharge {
	if charge == nil {
		return nil
	}
	clone := *charge
	clone.Amount, clone.Currency = strings.TrimSpace(clone.Amount), strings.TrimSpace(clone.Currency)
	return &clone
}

func cloneStructureRoleEvidence(evidence StructureRoleEvidence) StructureRoleEvidence {
	evidence.FrameSHA256 = slices.Clone(evidence.FrameSHA256)
	evidence.Modalities = slices.Clone(evidence.Modalities)
	evidence.Charge = cloneStructureRoleCharge(evidence.Charge)
	return evidence
}
