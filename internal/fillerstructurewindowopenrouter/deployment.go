package fillerstructurewindowopenrouter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

const (
	DeploymentSchemaVersion   = 1
	DeploymentContractVersion = "filler-structure-window-openrouter-deployment-v1"
	ReasoningDisabled         = "disabled"
	ReasoningProviderRequired = "provider_required"
)

type DeploymentFamily struct {
	AssessorID           string `json:"assessorId"`
	ModelFamily          string `json:"modelFamily"`
	Model                string `json:"model"`
	UpstreamProvider     string `json:"upstreamProvider"`
	UpstreamProviderSlug string `json:"upstreamProviderSlug"`
	ReasoningMode        string `json:"reasoningMode"`
	MaximumInputTokens   int64  `json:"maximumInputTokens"`
	ReservationNanoUSD   int64  `json:"reservationNanoUsd"`
}

// Deployment is an operator-approved execution envelope. Credentials, mutable catalog metadata,
// current prices, and filesystem locations remain generation-scoped runtime inputs.
type Deployment struct {
	SchemaVersion              int                `json:"schemaVersion"`
	ContractVersion            string             `json:"contractVersion"`
	AuthoritySHA256            string             `json:"authoritySha256"`
	Families                   []DeploymentFamily `json:"families"`
	PerSourceBudgetNanoUSD     int64              `json:"perSourceBudgetNanoUsd"`
	PerDayBudgetNanoUSD        int64              `json:"perDayBudgetNanoUsd"`
	AutomaticAssessmentAllowed bool               `json:"automaticAssessmentAllowed"`
	SHA256                     string             `json:"sha256"`
}

func DeploymentSHA256(deployment Deployment) string {
	deployment.SHA256 = ""
	raw, err := json.Marshal(deployment)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func ValidateDeployment(deployment Deployment, authority fillerstructurewindow.MaterializationAuthority) error {
	if err := fillerstructurewindow.ValidateMaterializationAuthority(authority); err != nil {
		return err
	}
	if deployment.SchemaVersion != DeploymentSchemaVersion || deployment.ContractVersion != DeploymentContractVersion ||
		deployment.AuthoritySHA256 != authority.SHA256 || !deployment.AutomaticAssessmentAllowed ||
		deployment.PerSourceBudgetNanoUSD <= 0 || deployment.PerDayBudgetNanoUSD < deployment.PerSourceBudgetNanoUSD ||
		len(deployment.Families) != len(authority.Assessors) || !validDeploymentSHA256(deployment.SHA256) ||
		deployment.SHA256 != DeploymentSHA256(deployment) {
		return errors.New("structure window OpenRouter deployment identity or permission is invalid")
	}
	if !slices.IsSortedFunc(deployment.Families, func(left, right DeploymentFamily) int {
		return strings.Compare(left.AssessorID, right.AssessorID)
	}) {
		return errors.New("structure window OpenRouter deployment families are not canonical")
	}
	var maximumSourceReservation int64
	models := make(map[string]struct{}, len(deployment.Families))
	for index, family := range deployment.Families {
		profile := authority.Assessors[index]
		if family.AssessorID != profile.ID || family.ModelFamily != profile.ModelFamily || family.Model != profile.Model ||
			!validDeploymentIdentity(family.UpstreamProvider) || !validDeploymentIdentity(family.UpstreamProviderSlug) ||
			!validReasoningMode(family.ReasoningMode) || family.MaximumInputTokens <= 0 ||
			family.ReservationNanoUSD <= 0 || family.ReservationNanoUSD > deployment.PerSourceBudgetNanoUSD {
			return errors.New("structure window OpenRouter deployment family is invalid or differs from authority")
		}
		if _, duplicate := models[family.Model]; duplicate {
			return errors.New("structure window OpenRouter deployment repeats a model across independent families")
		}
		models[family.Model] = struct{}{}
		if int64(authority.MaximumWindows) > deployment.PerSourceBudgetNanoUSD/family.ReservationNanoUSD {
			return errors.New("structure window OpenRouter deployment per-source budget cannot reserve one family")
		}
		familyMaximum := int64(authority.MaximumWindows) * family.ReservationNanoUSD
		if familyMaximum > deployment.PerSourceBudgetNanoUSD-maximumSourceReservation {
			return errors.New("structure window OpenRouter deployment per-source budget cannot reserve every family")
		}
		maximumSourceReservation += familyMaximum
	}
	return nil
}

func validReasoningMode(value string) bool {
	return value == ReasoningDisabled || value == ReasoningProviderRequired
}

func validDeploymentIdentity(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 256 && !strings.ContainsAny(value, "\r\n\t")
}

func validDeploymentSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
