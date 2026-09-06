package fillerstructurewindow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	PlanSchemaVersion   = 2
	PlanContractVersion = "filler-structure-window-plan-v2"
)

// Window separates the one source interval whose decisions it owns from the wider media interval
// supplied as context. Both intervals are half-open and source-relative.
type Window struct {
	Ordinal        int   `json:"ordinal"`
	PrimaryStartMS int64 `json:"primaryStartMs"`
	PrimaryEndMS   int64 `json:"primaryEndMs"`
	MediaStartMS   int64 `json:"mediaStartMs"`
	MediaEndMS     int64 `json:"mediaEndMs"`
	LeftContextMS  int64 `json:"leftContextMs"`
	RightContextMS int64 `json:"rightContextMs"`
}

// Plan is the path-free complete-coverage authority shared by all assessor families for one source.
type Plan struct {
	SchemaVersion   int                    `json:"schemaVersion"`
	ContractVersion string                 `json:"contractVersion"`
	Source          fillerstructure.Source `json:"source"`
	Profile         Profile                `json:"profile"`
	Windows         []Window               `json:"windows"`
	SHA256          string                 `json:"sha256"`
}

// NewPlan deterministically partitions the exact source timeline into primary spans and expands
// only the supplied media context. It performs no media I/O and grants no production authority.
func NewPlan(source fillerstructure.Source) (Plan, error) {
	if err := fillerstructure.ValidateSource(source); err != nil {
		return Plan{}, err
	}
	profile := CanonicalProfile()
	if source.DurationMS > profile.MaximumSourceDurationMS {
		return Plan{}, fmt.Errorf("structure window source duration %d exceeds protocol capacity %d", source.DurationMS, profile.MaximumSourceDurationMS)
	}
	plan := Plan{
		SchemaVersion: PlanSchemaVersion, ContractVersion: PlanContractVersion,
		Source: source, Profile: profile, Windows: planWindows(source.DurationMS, profile),
	}
	plan.SHA256 = PlanSHA256(plan)
	return plan, ValidatePlan(plan)
}

// PlanSHA256 returns the plan identity with its self-digest excluded.
func PlanSHA256(plan Plan) string {
	plan.SHA256 = ""
	raw, err := json.Marshal(plan)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// BoundaryOwner returns the unique primary window that owns a non-edge source boundary. A boundary
// exactly on a primary seam belongs to the right-hand window.
func (plan Plan) BoundaryOwner(atMS int64) (Window, error) {
	if err := ValidatePlan(plan); err != nil {
		return Window{}, err
	}
	if atMS <= 0 || atMS >= plan.Source.DurationMS {
		return Window{}, errors.New("structure boundary must be inside the source timeline")
	}
	for _, window := range plan.Windows {
		if atMS >= window.PrimaryStartMS && atMS < window.PrimaryEndMS {
			return window, nil
		}
	}
	return Window{}, errors.New("structure boundary has no primary owner")
}

func planWindows(durationMS int64, profile Profile) []Window {
	windows := make([]Window, 0, (durationMS+profile.PrimarySpanMS-1)/profile.PrimarySpanMS)
	for primaryStart := int64(0); primaryStart < durationMS; primaryStart += profile.PrimarySpanMS {
		primaryEnd := min(primaryStart+profile.PrimarySpanMS, durationMS)
		mediaStart := max(int64(0), primaryStart-profile.ContextOverlapMS)
		mediaEnd := min(durationMS, primaryEnd+profile.ContextOverlapMS)
		windows = append(windows, Window{
			Ordinal: len(windows), PrimaryStartMS: primaryStart, PrimaryEndMS: primaryEnd,
			MediaStartMS: mediaStart, MediaEndMS: mediaEnd,
			LeftContextMS: primaryStart - mediaStart, RightContextMS: mediaEnd - primaryEnd,
		})
	}
	return windows
}
