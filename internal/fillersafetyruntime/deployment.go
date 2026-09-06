// Package fillersafetyruntime owns authority-driven construction of the production spoken-safety
// evidence producer. It cannot grant filler admission, scheduling, or broadcast authority.
package fillersafetyruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

const (
	DeploymentSchemaVersion   = 1
	DeploymentContractVersion = "filler-spoken-safety-runtime-deployment-v1"
)

// Deployment is the small operator-approved execution envelope around one exact certification.
// Models, prompts, schemas, and upstream routes remain owned by the certification authority.
type Deployment struct {
	SchemaVersion              int    `json:"schemaVersion"`
	ContractVersion            string `json:"contractVersion"`
	AuthoritySHA256            string `json:"authoritySha256"`
	MaximumSourceBytes         int64  `json:"maximumSourceBytes"`
	MaximumSourceDurationMS    int64  `json:"maximumSourceDurationMs"`
	AudioMaximumInputTokens    int64  `json:"audioMaximumInputTokens"`
	VideoMaximumInputTokens    int64  `json:"videoMaximumInputTokens"`
	AudioReservationNanoUSD    int64  `json:"audioReservationNanoUsd"`
	VideoReservationNanoUSD    int64  `json:"videoReservationNanoUsd"`
	PerClipBudgetNanoUSD       int64  `json:"perClipBudgetNanoUsd"`
	PerDayBudgetNanoUSD        int64  `json:"perDayBudgetNanoUsd"`
	PerRunBudgetNanoUSD        int64  `json:"perRunBudgetNanoUsd"`
	CertifiedEvidenceExecution bool   `json:"certifiedEvidenceExecution"`
	SHA256                     string `json:"sha256"`
}

// SealDeployment produces the content address after applying the fixed schema identity.
func SealDeployment(deployment Deployment) (Deployment, error) {
	deployment.SchemaVersion = DeploymentSchemaVersion
	deployment.ContractVersion = DeploymentContractVersion
	deployment.SHA256 = deploymentSHA256(deployment)
	if err := validateDeploymentShape(deployment); err != nil {
		return Deployment{}, err
	}
	return deployment, nil
}

func validateDeploymentShape(deployment Deployment) error {
	if deployment.SchemaVersion != DeploymentSchemaVersion || deployment.ContractVersion != DeploymentContractVersion ||
		!validSHA256(deployment.AuthoritySHA256) || !validSHA256(deployment.SHA256) ||
		deployment.SHA256 != deploymentSHA256(deployment) || !deployment.CertifiedEvidenceExecution ||
		deployment.MaximumSourceBytes <= 0 || deployment.MaximumSourceDurationMS <= 0 ||
		deployment.AudioMaximumInputTokens <= 0 || deployment.VideoMaximumInputTokens <= 0 ||
		deployment.AudioReservationNanoUSD <= 0 || deployment.VideoReservationNanoUSD <= 0 ||
		deployment.PerClipBudgetNanoUSD <= 0 || deployment.PerRunBudgetNanoUSD < deployment.PerClipBudgetNanoUSD ||
		deployment.PerDayBudgetNanoUSD < deployment.PerClipBudgetNanoUSD {
		return errors.New("spoken-safety runtime deployment is invalid")
	}
	return nil
}

func deploymentSHA256(deployment Deployment) string {
	deployment.SHA256 = ""
	raw, err := json.Marshal(deployment)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == hex.EncodeToString(decoded)
}
