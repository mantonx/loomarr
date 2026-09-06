package fillerstructure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"
)

const (
	ArtifactSchemaVersion   = 4
	ArtifactContractVersion = "filler-structure-decision-artifact-v4"
	ReducerContractVersion  = "filler-structure-agreement-v2"
)

// Artifact is the durable production evidence for one complete-timeline reduction. It retains
// every candidate inside Decision so validation can replay the exact provider-neutral policy.
type Artifact struct {
	SchemaVersion       int       `json:"schemaVersion"`
	ContractVersion     string    `json:"contractVersion"`
	ReducerVersion      string    `json:"reducerVersion"`
	BoundaryToleranceMS int64     `json:"boundaryToleranceMs"`
	Decision            Decision  `json:"decision"`
	DecidedAt           time.Time `json:"decidedAt"`
	SHA256              string    `json:"sha256"`
}

func NewArtifact(request Request, decidedAt time.Time) (Artifact, error) {
	if decidedAt.IsZero() {
		return Artifact{}, errors.New("filler structure artifact: decision time is required")
	}
	decision := Reduce(request)
	if slices.Contains(decision.ReasonCodes, ReasonInvalidCandidate) {
		return Artifact{}, errors.New("filler structure artifact: candidates are invalid")
	}
	artifact := Artifact{
		SchemaVersion: ArtifactSchemaVersion, ContractVersion: ArtifactContractVersion,
		ReducerVersion: ReducerContractVersion, BoundaryToleranceMS: request.BoundaryToleranceMS,
		Decision: decision, DecidedAt: decidedAt.UTC().Round(0),
	}
	artifact.SHA256 = ArtifactSHA256(artifact)
	return artifact, ValidateArtifact(artifact)
}

func ValidateArtifact(artifact Artifact) error {
	if artifact.SchemaVersion != ArtifactSchemaVersion || artifact.ContractVersion != ArtifactContractVersion || artifact.ReducerVersion != ReducerContractVersion {
		return errors.New("filler structure artifact: unsupported contract")
	}
	if artifact.BoundaryToleranceMS < 0 || artifact.DecidedAt.IsZero() || artifact.DecidedAt != artifact.DecidedAt.UTC() {
		return errors.New("filler structure artifact: invalid policy or decision time")
	}
	if !digest(artifact.SHA256) || artifact.SHA256 != ArtifactSHA256(artifact) {
		return errors.New("filler structure artifact: digest does not match")
	}
	reduced := Reduce(Request{
		Source: artifact.Decision.Source, Input: artifact.Decision.Input, BoundaryToleranceMS: artifact.BoundaryToleranceMS,
		Candidates: artifact.Decision.Candidates,
	})
	if slices.Contains(reduced.ReasonCodes, ReasonInvalidCandidate) || !reflect.DeepEqual(reduced, artifact.Decision) {
		return errors.New("filler structure artifact: decision does not reproduce")
	}
	return nil
}

func ArtifactSHA256(artifact Artifact) string {
	artifact.SHA256 = ""
	raw, err := json.Marshal(artifact)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
