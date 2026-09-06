package fillercorpus

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	MetRightsPrescreenSchemaVersion = 1

	metRightsPrescreenPass = "met_metadata_prescreen_pass"
	metRightsPrescreenHold = "hold"
)

var requiredMetRightsPrescreenInstructions = []string{
	"This is a mechanical metadata pre-screen, not a rights approval or legal conclusion.",
	"Inspect every held row; passing rows still require the existing independent item-level rights decision.",
	"No result grants download, truth, training, production, scheduling, or broadcast authority.",
}

// MetRightsPrescreenOptions supplies the local evidence root and explicit
// coverage/time bounds for one complete, non-authorizing pre-screen.
type MetRightsPrescreenOptions struct {
	MetadataRoot string
	PreparedAt   time.Time
	MinItems     int
	MaxItems     int
}

// MetRightsPrescreen is a path-free account of mechanical consistency. Its
// authority flags are always false; the existing item-level review remains the
// only route to a downloader-compatible rights decision.
type MetRightsPrescreen struct {
	SchemaVersion        int                         `json:"schemaVersion"`
	InventorySHA256      string                      `json:"inventorySha256"`
	PolicyEvidenceID     string                      `json:"policyEvidenceId"`
	PolicyEvidenceSHA256 string                      `json:"policyEvidenceSha256"`
	PolicySources        []MetOpenAccessPolicySource `json:"policySources"`
	Limitations          []string                    `json:"limitations"`
	PreparedAt           time.Time                   `json:"preparedAt"`
	MinItems             int                         `json:"minItems"`
	MaxItems             int                         `json:"maxItems"`
	TotalCases           int                         `json:"totalCases"`
	PassedCases          int                         `json:"passedCases"`
	HeldCases            int                         `json:"heldCases"`
	CompleteCoverage     bool                        `json:"completeCoverage"`
	RightsApproval       bool                        `json:"rightsApproval"`
	DownloadAuthority    bool                        `json:"downloadAuthority"`
	TruthAuthority       bool                        `json:"truthAuthority"`
	TrainingAuthority    bool                        `json:"trainingAuthority"`
	ProductionAuthority  bool                        `json:"productionAuthority"`
	IngestionAuthority   bool                        `json:"ingestionAuthority"`
	SchedulingAuthority  bool                        `json:"schedulingAuthority"`
	BroadcastAuthority   bool                        `json:"broadcastAuthority"`
	Instructions         []string                    `json:"instructions"`
	Cases                []MetRightsPrescreenCase    `json:"cases"`
}

type MetRightsPrescreenCase struct {
	CaseID         string   `json:"caseId"`
	MetadataSHA256 string   `json:"metadataSha256"`
	Status         string   `json:"status"`
	ReasonCodes    []string `json:"reasonCodes"`
}

// PrepareMetRightsPrescreen reopens every inventory-bound Met response and
// reduces repetitive checks to a complete anomaly report. It performs no
// network request and grants no rights or downstream-use authority.
func PrepareMetRightsPrescreen(inventoryRaw, policyEvidenceRaw []byte, opts MetRightsPrescreenOptions) (MetRightsPrescreen, error) {
	if opts.PreparedAt.IsZero() || opts.PreparedAt.Location() != time.UTC || opts.MinItems <= 0 || opts.MaxItems < opts.MinItems || opts.MaxItems > 500 {
		return MetRightsPrescreen{}, fmt.Errorf("met rights pre-screen requires a UTC preparation time and positive bounded item coverage")
	}
	inventory, err := DecodeInventoryBytes(inventoryRaw)
	if err != nil {
		return MetRightsPrescreen{}, fmt.Errorf("met rights pre-screen inventory: %w", err)
	}
	if len(inventory.Cases) < opts.MinItems || len(inventory.Cases) > opts.MaxItems || opts.PreparedAt.Before(inventory.SnapshotAt) {
		return MetRightsPrescreen{}, fmt.Errorf("met rights pre-screen inventory count or time is outside its declared bounds")
	}
	policy, err := decodeMetOpenAccessPolicyEvidence(policyEvidenceRaw)
	if err != nil {
		return MetRightsPrescreen{}, err
	}
	if opts.PreparedAt.Before(policy.CapturedAt) {
		return MetRightsPrescreen{}, fmt.Errorf("met rights pre-screen predates its policy evidence")
	}
	root, err := openPrivateMetMetadataRoot(opts.MetadataRoot)
	if err != nil {
		return MetRightsPrescreen{}, err
	}
	report := MetRightsPrescreen{
		SchemaVersion: MetRightsPrescreenSchemaVersion, InventorySHA256: InventorySHA256(inventoryRaw),
		PolicyEvidenceID: policy.EvidenceID, PolicyEvidenceSHA256: InventorySHA256(policyEvidenceRaw),
		PolicySources: slices.Clone(policy.Sources), Limitations: slices.Clone(policy.Limitations),
		PreparedAt: opts.PreparedAt, MinItems: opts.MinItems, MaxItems: opts.MaxItems,
		Instructions: slices.Clone(requiredMetRightsPrescreenInstructions),
	}
	for _, item := range inventory.Cases {
		if item.Authority != MetAuthority {
			return MetRightsPrescreen{}, fmt.Errorf("met rights pre-screen cannot mix authority %q", item.Authority)
		}
		caseResult := prescreenMetRightsCase(root, item)
		report.Cases = append(report.Cases, caseResult)
		if caseResult.Status == metRightsPrescreenPass {
			report.PassedCases++
		} else {
			report.HeldCases++
		}
	}
	slices.SortFunc(report.Cases, func(left, right MetRightsPrescreenCase) int {
		return strings.Compare(left.CaseID, right.CaseID)
	})
	report.TotalCases = len(report.Cases)
	report.CompleteCoverage = report.TotalCases == len(inventory.Cases) && report.TotalCases == report.PassedCases+report.HeldCases
	if !report.CompleteCoverage {
		return MetRightsPrescreen{}, fmt.Errorf("met rights pre-screen did not cover its complete inventory")
	}
	return report, nil
}
