package fillersafetycorpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

const knownScriptRightsContractPrefix = "filler-spoken-known-script-rights-"

// AuthorizeKnownScriptProcessor recognizes this module's participant-rights
// envelope and authorizes one exact hosted processor at review time. Unrelated
// rights contracts return applies=false and retain their own policy owner.
func AuthorizeKnownScriptProcessor(
	raw []byte,
	at time.Time,
	processor KnownScriptHostedProcessor,
) (applies bool, err error) {
	if len(raw) == 0 || int64(len(raw)) > maximumReleaseAuthorityBytes {
		if bytes.Contains(raw, []byte(knownScriptRightsContractPrefix)) {
			return true, fmt.Errorf("known-script rights envelope size is invalid")
		}
		return false, nil
	}
	var header struct {
		ContractVersion string `json:"contractVersion"`
	}
	if decodeErr := json.Unmarshal(raw, &header); decodeErr != nil {
		if bytes.Contains(raw, []byte(knownScriptRightsContractPrefix)) {
			return true, fmt.Errorf("known-script rights envelope is malformed")
		}
		return false, nil
	}
	if header.ContractVersion == "" || !strings.HasPrefix(header.ContractVersion, knownScriptRightsContractPrefix) {
		return false, nil
	}
	if header.ContractVersion != KnownScriptRightsContractVersion {
		return true, fmt.Errorf("known-script rights envelope version is unsupported")
	}
	rights, validationErr := validateKnownScriptRights(raw, at)
	if validationErr != nil {
		return true, validationErr
	}
	if !validKnownScriptProcessor(processor) || !slices.Contains(rights.ProcessorSchedule.Processors, processor) {
		return true, fmt.Errorf("known-script participant did not authorize the exact hosted processor")
	}
	return true, nil
}

func validateKnownScriptRights(raw []byte, at time.Time) (knownScriptRights, error) {
	var zero knownScriptRights
	rights, decodeErr := decodeKnownScriptJSON[knownScriptRights](raw)
	if decodeErr != nil {
		return zero, fmt.Errorf("known-script rights envelope is malformed")
	}
	canonical, marshalErr := marshalPrivateJSON(rights)
	if marshalErr != nil || !bytes.Equal(raw, canonical) {
		return zero, fmt.Errorf("known-script rights envelope is not canonical")
	}
	if at.IsZero() || rights.SchemaVersion != KnownScriptRightsSchemaVersion ||
		rights.ContractVersion != KnownScriptRightsContractVersion || rights.PreparedAt.IsZero() ||
		rights.PreparedAt.After(at) || !validSHA256(rights.AuthoritySHA256) ||
		!boundedID(rights.ParticipantID) {
		return zero, fmt.Errorf("known-script rights identity or review time is invalid")
	}
	if consentErr := validateKnownScriptConsent(
		rights.Consent, rights.ParticipantID, rights.PreparedAt, at,
	); consentErr != nil {
		return zero, fmt.Errorf("known-script participant consent is not current")
	}
	if scheduleErr := validateKnownScriptProcessorSchedule(rights.ProcessorSchedule); scheduleErr != nil {
		return zero, fmt.Errorf("known-script processor schedule is invalid")
	}
	if _, assetsErr := validateKnownScriptAssets(rights.Assets, at); assetsErr != nil {
		return zero, fmt.Errorf("known-script asset rights are not current")
	}
	return rights, nil
}
