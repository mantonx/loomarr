package fillerstructurewindow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	MaterializationAuthoritySchemaVersion   = 1
	MaterializationAuthorityContractVersion = "filler-structure-window-materialization-authority-v1"
)

// MaterializationAuthority is the separately reviewed release boundary for long-reel decisions.
// It may create held child work; it is never broadcast-admission or training authority.
type MaterializationAuthority struct {
	SchemaVersion                   int                               `json:"schemaVersion"`
	ContractVersion                 string                            `json:"contractVersion"`
	WindowCertificationSHA256       string                            `json:"windowCertificationSha256"`
	ShortLongShadowSHA256           string                            `json:"shortLongShadowSha256"`
	WindowProfileSHA256             string                            `json:"windowProfileSha256"`
	AssessmentMediaProfileSHA256    string                            `json:"assessmentMediaProfileSha256"`
	MinimumSourceDurationMS         int64                             `json:"minimumSourceDurationMs"`
	MaximumSourceDurationMS         int64                             `json:"maximumSourceDurationMs"`
	MaximumWindowBytes              int64                             `json:"maximumWindowBytes"`
	MaximumWindows                  int                               `json:"maximumWindows"`
	ReducerVersion                  string                            `json:"reducerVersion"`
	BoundaryToleranceMS             int64                             `json:"boundaryToleranceMs"`
	Assessors                       []fillerstructure.AssessorProfile `json:"assessors"`
	AllowedUnits                    []fillerstructure.Unit            `json:"allowedUnits"`
	AllowedRoles                    []fillerstructure.Role            `json:"allowedRoles"`
	ReviewerID                      string                            `json:"reviewerId"`
	ReviewedAt                      time.Time                         `json:"reviewedAt"`
	TrainingAllowed                 bool                              `json:"trainingAllowed"`
	ProductionAdmissionAllowed      bool                              `json:"productionAdmissionAllowed"`
	AutomaticMaterializationAllowed bool                              `json:"automaticMaterializationAllowed"`
	SHA256                          string                            `json:"sha256"`
}

func MaterializationAuthoritySHA256(authority MaterializationAuthority) string {
	authority.SHA256 = ""
	raw, err := json.Marshal(authority)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
