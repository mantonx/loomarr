package fillerstructure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	AuthoritySchemaVersion   = 4
	AuthorityContractVersion = "filler-structure-materialization-authority-v4"
)

type AssessorProfile struct {
	ID               string `json:"id"`
	ModelFamily      string `json:"modelFamily"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	ModelDigest      string `json:"modelDigest"`
	CapabilitySHA256 string `json:"capabilitySha256"`
	PromptVersion    string `json:"promptVersion"`
	EvidenceContract string `json:"evidenceContract"`
}

// Authority is the immutable release boundary produced from an external certificate. It names
// only certified stable profiles; source-specific assessment digests remain in each Artifact.
type Authority struct {
	SchemaVersion                   int               `json:"schemaVersion"`
	ContractVersion                 string            `json:"contractVersion"`
	CertificateSHA256               string            `json:"certificateSha256"`
	AssessmentMediaProfileSHA256    string            `json:"assessmentMediaProfileSha256"`
	MinimumSourceDurationMS         int64             `json:"minimumSourceDurationMs"`
	MaximumSourceDurationMS         int64             `json:"maximumSourceDurationMs"`
	MaximumAssessmentMediaBytes     int64             `json:"maximumAssessmentMediaBytes"`
	ReducerVersion                  string            `json:"reducerVersion"`
	BoundaryToleranceMS             int64             `json:"boundaryToleranceMs"`
	Assessors                       []AssessorProfile `json:"assessors"`
	AllowedUnits                    []Unit            `json:"allowedUnits"`
	AllowedRoles                    []Role            `json:"allowedRoles"`
	AutomaticMaterializationAllowed bool              `json:"automaticMaterializationAllowed"`
	SHA256                          string            `json:"sha256"`
}

func Profile(assessor Assessor) AssessorProfile {
	return AssessorProfile{
		ID: assessor.ID, ModelFamily: assessor.ModelFamily, Provider: assessor.Provider,
		Model: assessor.Model, ModelDigest: assessor.ModelDigest,
		CapabilitySHA256: assessor.CapabilitySHA256, PromptVersion: assessor.PromptVersion,
		EvidenceContract: assessor.EvidenceContract,
	}
}

func AuthoritySHA256(authority Authority) string {
	authority.SHA256 = ""
	raw, err := json.Marshal(authority)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
