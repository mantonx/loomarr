package fillersafetycorpus

import (
	"fmt"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func (v *CertificationEvidenceValidator) validateVCTKCertificationEvidence(
	rightsRaw, provenanceRaw []byte,
	item fillersafetycert.AuthorityDraftCase,
	at time.Time,
) error {
	authority, err := v.vctkAuthority(rightsRaw, at)
	if err != nil {
		return err
	}
	provenance, err := decodeCanonicalCertificationJSON[vctkProvenance](provenanceRaw)
	if err != nil {
		return fmt.Errorf("spoken certification VCTK provenance is malformed or noncanonical")
	}
	source := item.SourceAuthority
	if provenance.SchemaVersion != PreparedCohortSchemaVersion || provenance.ContractVersion != PreparedCohortContractVersion ||
		provenance.PreparedAt.IsZero() || !provenance.PreparedAt.Equal(source.MeasuredAt) || provenance.PreparedAt.After(at) ||
		provenance.ReleaseAuthoritySHA256 != hashBytes(rightsRaw) || !validSHA256(provenance.RecipeSHA256) ||
		!vctkSpeakerID.MatchString(provenance.SpeakerID) || !vctkUtteranceID.MatchString(provenance.UtteranceID) ||
		(provenance.Microphone != "mic1" && provenance.Microphone != "mic2") ||
		!validFileAuthority(provenance.Audio) || !validFileAuthority(provenance.Transcript) ||
		!validFileAuthority(provenance.ScreeningEvidence) || provenance.OutputSHA256 != source.SourceSHA256 ||
		provenance.OutputBytes != source.SourceBytes || provenance.DurationMS != source.DurationMS ||
		item.Label != fillersafetycert.LabelClean || len(item.PositiveIntervals) != 0 {
		return fmt.Errorf("spoken certification VCTK provenance does not bind the case source")
	}
	for _, member := range authority.Members {
		if member.SpeakerID == provenance.SpeakerID && member.UtteranceID == provenance.UtteranceID &&
			member.Microphone == provenance.Microphone {
			if member.Locale != item.Locale || member.Audio != provenance.Audio || member.Transcript != provenance.Transcript ||
				member.ScreeningEvidence != provenance.ScreeningEvidence {
				return fmt.Errorf("spoken certification VCTK provenance does not match its release member")
			}
			return nil
		}
	}
	return fmt.Errorf("spoken certification VCTK provenance names no release member")
}

func (v *CertificationEvidenceValidator) vctkAuthority(raw []byte, at time.Time) (VCTKReleaseAuthority, error) {
	digest := hashBytes(raw)
	v.mu.Lock()
	authority, found := v.vctk[digest]
	v.mu.Unlock()
	if !found {
		decoded, err := decodeCanonicalCertificationJSON[VCTKReleaseAuthority](raw)
		if err != nil {
			return VCTKReleaseAuthority{}, fmt.Errorf("spoken certification VCTK rights envelope is malformed or noncanonical")
		}
		v.mu.Lock()
		if existing, ok := v.vctk[digest]; ok {
			authority = existing
		} else {
			v.vctk[digest] = decoded
			authority = decoded
		}
		v.mu.Unlock()
	}
	currentKey := digest + "\x00" + at.UTC().Format(time.RFC3339Nano)
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.vctkCurrent[currentKey]; ok {
		return authority, nil
	}
	if err := validateRelease(authority, at); err != nil {
		return VCTKReleaseAuthority{}, fmt.Errorf("spoken certification VCTK rights are not current")
	}
	v.vctkCurrent[currentKey] = struct{}{}
	return authority, nil
}
