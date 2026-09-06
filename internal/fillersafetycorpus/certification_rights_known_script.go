package fillersafetycorpus

import (
	"fmt"
	"reflect"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

func validateKnownScriptCertificationEvidence(
	rightsRaw, provenanceRaw []byte,
	item fillersafetycert.AuthorityDraftCase,
	at time.Time,
) error {
	rights, err := validateKnownScriptRights(rightsRaw, at)
	if err != nil {
		return fmt.Errorf("spoken certification participant rights are not current")
	}
	provenance, err := decodeCanonicalCertificationJSON[knownScriptProvenance](provenanceRaw)
	if err != nil {
		return fmt.Errorf("spoken certification participant provenance is malformed or noncanonical")
	}
	source := item.SourceAuthority
	transformation := provenance.Transformation
	if provenance.SchemaVersion != KnownScriptAuthoritySchemaVersion ||
		provenance.ContractVersion != KnownScriptAuthorityContractVersion || provenance.PreparedAt.IsZero() ||
		!provenance.PreparedAt.Equal(rights.PreparedAt) || !provenance.PreparedAt.Equal(source.MeasuredAt) ||
		provenance.PreparedAt.After(at) || provenance.AuthoritySHA256 != rights.AuthoritySHA256 ||
		provenance.ParticipantID != rights.ParticipantID || !boundedID(provenance.SessionID) ||
		!boundedID(provenance.TakeID) || !boundedID(provenance.ScriptID) || !validFileAuthority(provenance.Script) ||
		!validFileAuthority(provenance.PolicyMapping) || !validFileAuthority(provenance.MasterAudio) ||
		!validFileAuthority(provenance.SelectedAudio) || !boundedID(transformation.RecipeID) ||
		!validSHA256(transformation.RecipeSHA256) || transformation.RenderedAt.IsZero() ||
		transformation.RenderedAt.Before(rights.Consent.SignedAt) || transformation.RenderedAt.After(provenance.PreparedAt) ||
		!validTool(transformation.Tool) || transformation.MasterSHA256 != provenance.MasterAudio.SHA256 ||
		transformation.OutputSHA256 != provenance.SelectedAudio.SHA256 ||
		!reflect.DeepEqual(transformation.Assets, rights.Assets) || provenance.OutputSHA256 != source.SourceSHA256 ||
		provenance.OutputBytes != source.SourceBytes || provenance.DurationMS != source.DurationMS ||
		item.Label != fillersafetycert.LabelPositive || !equalPositiveIntervals(provenance.PositiveIntervals, item.PositiveIntervals) {
		return fmt.Errorf("spoken certification participant provenance does not bind the case source")
	}
	if _, err := validateKnownScriptAssets(transformation.Assets, at); err != nil {
		return fmt.Errorf("spoken certification participant transformation rights are not current")
	}
	return nil
}

func equalPositiveIntervals(actual []PreparedPositiveInterval, expected []fillersafetycert.PositiveInterval) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index].RuleID != expected[index].RuleID || actual[index].StartMS != expected[index].StartMS ||
			actual[index].EndMS != expected[index].EndMS {
			return false
		}
	}
	return true
}
