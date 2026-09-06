package fillersafetycorpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

// CertificationEvidenceValidator caches shared decoded authorities while still
// validating the provenance and source binding of every certification case.
type CertificationEvidenceValidator struct {
	mu          sync.Mutex
	vctk        map[string]VCTKReleaseAuthority
	vctkCurrent map[string]struct{}
}

func NewCertificationEvidenceValidator() *CertificationEvidenceValidator {
	return &CertificationEvidenceValidator{
		vctk: make(map[string]VCTKReleaseAuthority), vctkCurrent: make(map[string]struct{}),
	}
}

// Validate recognizes every rights/provenance pair supported by the spoken
// certification corpus. Unknown formats and evidence that is current but does
// not govern the exact case source fail closed.
func (v *CertificationEvidenceValidator) Validate(
	rightsRaw, provenanceRaw []byte,
	item fillersafetycert.AuthorityDraftCase,
	at time.Time,
) error {
	if v == nil || len(rightsRaw) == 0 || int64(len(rightsRaw)) > maximumReleaseAuthorityBytes ||
		len(provenanceRaw) == 0 || int64(len(provenanceRaw)) > maximumReleaseAuthorityBytes || at.IsZero() {
		return fmt.Errorf("spoken certification evidence input is invalid")
	}
	if _, err := fillersafety.SourceAuthoritySHA256(item.SourceAuthority); err != nil {
		return fmt.Errorf("spoken certification source authority is invalid")
	}
	var header struct {
		ContractVersion string `json:"contractVersion"`
	}
	if err := json.Unmarshal(rightsRaw, &header); err != nil {
		return fmt.Errorf("spoken certification rights envelope is malformed")
	}
	switch header.ContractVersion {
	case KnownScriptRightsContractVersion:
		return validateKnownScriptCertificationEvidence(rightsRaw, provenanceRaw, item, at)
	case VCTKReleaseContractVersion:
		return v.validateVCTKCertificationEvidence(rightsRaw, provenanceRaw, item, at)
	default:
		return fmt.Errorf("spoken certification rights contract is unsupported")
	}
}

func decodeCanonicalCertificationJSON[T any](raw []byte) (T, error) {
	var zero T
	value, err := decodeKnownScriptJSON[T](raw)
	if err != nil {
		return zero, err
	}
	canonical, err := marshalPrivateJSON(value)
	if err != nil || !bytes.Equal(raw, canonical) {
		return zero, fmt.Errorf("private certification document is not canonical")
	}
	return value, nil
}
