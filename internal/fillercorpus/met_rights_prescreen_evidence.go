package fillercorpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"time"
)

const MetOpenAccessPolicyEvidenceSchemaVersion = 1

type MetOpenAccessPolicyEvidence struct {
	SchemaVersion int                         `json:"schemaVersion"`
	EvidenceID    string                      `json:"evidenceId"`
	CapturedAt    time.Time                   `json:"capturedAt"`
	Sources       []MetOpenAccessPolicySource `json:"sources"`
	Limitations   []string                    `json:"limitations"`
}

type MetOpenAccessPolicySource struct {
	Kind   string `json:"kind"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Commit string `json:"commit,omitempty"`
}

var requiredMetPolicySources = []MetOpenAccessPolicySource{
	{Kind: "api_documentation", URL: "https://metmuseum.github.io/", SHA256: "037f875cd22180ecb31a67cb38707ce2ea88eb7087c2f81edd27a0a1aa56dd6a"},
	{Kind: "openaccess_license", URL: "https://raw.githubusercontent.com/metmuseum/openaccess/6fa206f0df6cf349d4fe558028d4c08e95f44eb6/LICENSE", SHA256: "36ffd9dc085d529a7e60e1276d73ae5a030b020313e6c5408593a6ae2af39673", Commit: "6fa206f0df6cf349d4fe558028d4c08e95f44eb6"},
	{Kind: "openaccess_readme", URL: "https://raw.githubusercontent.com/metmuseum/openaccess/6fa206f0df6cf349d4fe558028d4c08e95f44eb6/README.md", SHA256: "26f24c669b3eb888a02498113dc94feb2674ee9d007a1d470c13be36413a29c2", Commit: "6fa206f0df6cf349d4fe558028d4c08e95f44eb6"},
}

var requiredMetPolicyLimitations = []string{
	"cc0_does_not_resolve_non_copyright_rights",
	"dataset_cc0_does_not_license_images",
	"metadata_prescreen_is_not_rights_approval",
	"source_policy_pages_require_independent_review",
}

func decodeMetOpenAccessPolicyEvidence(raw []byte) (MetOpenAccessPolicyEvidence, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var evidence MetOpenAccessPolicyEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return MetOpenAccessPolicyEvidence{}, fmt.Errorf("decode Met Open Access policy evidence: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return MetOpenAccessPolicyEvidence{}, fmt.Errorf("decode Met Open Access policy evidence: trailing JSON value")
	}
	if evidence.SchemaVersion != MetOpenAccessPolicyEvidenceSchemaVersion || evidence.EvidenceID != "met-open-access-metadata-prescreen-v1" ||
		evidence.CapturedAt.IsZero() || evidence.CapturedAt.Location() != time.UTC || !reflect.DeepEqual(evidence.Limitations, requiredMetPolicyLimitations) ||
		len(evidence.Sources) != len(requiredMetPolicySources) || !slices.IsSortedFunc(evidence.Sources, func(left, right MetOpenAccessPolicySource) int { return compareStrings(left.Kind, right.Kind) }) {
		return MetOpenAccessPolicyEvidence{}, fmt.Errorf("met Open Access policy evidence identity, time, sources, or limitations are invalid")
	}
	for index, source := range evidence.Sources {
		required := requiredMetPolicySources[index]
		if source != required || !IsSHA256(source.SHA256) {
			return MetOpenAccessPolicyEvidence{}, fmt.Errorf("met Open Access policy source %q is invalid", source.Kind)
		}
	}
	return evidence, nil
}

func compareStrings(left, right string) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
