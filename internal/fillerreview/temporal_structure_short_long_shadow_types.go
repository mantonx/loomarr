package fillerreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructurewindowcert"
)

const (
	TemporalStructureShortLongShadowSchemaVersion   = 1
	TemporalStructureShortLongShadowContractVersion = "filler-temporal-structure-short-long-shadow-v1"
)

type TemporalStructureShortLongShadowConfig struct {
	WindowSetManifestPath   string
	WindowCertificationPath string
	CompleteDecisionSetPath string
	WindowDecisionSetPath   string
	ComparedAt              time.Time
	OutputPath              string
}

// TemporalStructureShortLongShadowArtifact retains the immutable lineage that was validated
// before producing the embedded, self-contained representation-comparison report.
type TemporalStructureShortLongShadowArtifact struct {
	SchemaVersion                   int                                    `json:"schemaVersion"`
	ContractVersion                 string                                 `json:"contractVersion"`
	WindowSetManifestSHA256         string                                 `json:"windowSetManifestSha256"`
	WindowCertificationSHA256       string                                 `json:"windowCertificationSha256"`
	WindowCertificationFileSHA256   string                                 `json:"windowCertificationFileSha256"`
	CompleteDecisionSetSHA256       string                                 `json:"completeDecisionSetSha256"`
	CompleteDecisionSetFileSHA256   string                                 `json:"completeDecisionSetFileSha256"`
	WindowDecisionSetSHA256         string                                 `json:"windowDecisionSetSha256"`
	WindowDecisionSetFileSHA256     string                                 `json:"windowDecisionSetFileSha256"`
	Report                          fillerstructurewindowcert.ShadowReport `json:"report"`
	TrainingAllowed                 bool                                   `json:"trainingAllowed"`
	ProductionAdmissionAllowed      bool                                   `json:"productionAdmissionAllowed"`
	AutomaticMaterializationAllowed bool                                   `json:"automaticMaterializationAllowed"`
	SHA256                          string                                 `json:"sha256"`
}

func temporalStructureShortLongShadowSHA256(artifact TemporalStructureShortLongShadowArtifact) string {
	artifact.SHA256 = ""
	raw, err := json.Marshal(artifact)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
