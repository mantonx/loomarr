package fillerstructurewindowcert

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	ShadowReportSchemaVersion   = 2
	ShadowReportContractVersion = "filler-structure-short-long-shadow-v2"
	ShadowRequiredCases         = 28
)

type ShadowCase struct {
	Alias          string                   `json:"alias"`
	CompleteVideo  fillerstructure.Artifact `json:"completeVideo"`
	WindowMediaSet fillerstructure.Artifact `json:"windowMediaSet"`
}

type ShadowProfilePair struct {
	ModelFamily    string                          `json:"modelFamily"`
	CompleteVideo  fillerstructure.AssessorProfile `json:"completeVideo"`
	WindowMediaSet fillerstructure.AssessorProfile `json:"windowMediaSet"`
}

type ShadowCaseResult struct {
	Alias          string                   `json:"alias"`
	CompleteVideo  fillerstructure.Artifact `json:"completeVideo"`
	WindowMediaSet fillerstructure.Artifact `json:"windowMediaSet"`
	FailureCodes   []string                 `json:"failureCodes,omitempty"`
	Passed         bool                     `json:"passed"`
}

// ShadowReport is self-contained so validation can replay both reducer decisions rather than
// trusting summary counts or detached artifact digests.
type ShadowReport struct {
	SchemaVersion                   int                 `json:"schemaVersion"`
	ContractVersion                 string              `json:"contractVersion"`
	ComparedAt                      time.Time           `json:"comparedAt"`
	WindowSetManifestSHA256         string              `json:"windowSetManifestSha256"`
	WindowCertificationSHA256       string              `json:"windowCertificationSha256"`
	ReducerVersion                  string              `json:"reducerVersion"`
	BoundaryToleranceMS             int64               `json:"boundaryToleranceMs"`
	Profiles                        []ShadowProfilePair `json:"profiles"`
	ExpectedAliases                 []string            `json:"expectedAliases"`
	Cases                           []ShadowCaseResult  `json:"cases"`
	PassedCases                     int                 `json:"passedCases"`
	FailedCases                     int                 `json:"failedCases"`
	FailureCodes                    []string            `json:"failureCodes,omitempty"`
	Status                          string              `json:"status"`
	TrainingAllowed                 bool                `json:"trainingAllowed"`
	AutomaticMaterializationAllowed bool                `json:"automaticMaterializationAllowed"`
	NextAction                      string              `json:"nextAction"`
	SHA256                          string              `json:"sha256"`
}

func ShadowReportSHA256(report ShadowReport) string {
	report.SHA256 = ""
	raw, err := json.Marshal(report)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
