package fillersafetycert

import (
	"context"
	"fmt"
)

func validateAuthorityEvidence(ctx context.Context, inputs loadedAuthorityInputs, config AuthorityBuildConfig) error {
	for index, item := range inputs.draft.Cases {
		if err := ctx.Err(); err != nil {
			return err
		}
		rightsRaw, err := readPrivateEvidence(inputs.sourceRoot, item.RightsPath)
		if err != nil || hashBytes(rightsRaw) != item.RightsSHA256 {
			return fmt.Errorf("authority case %d rights evidence is invalid", index+1)
		}
		provenanceRaw, err := readPrivateEvidence(inputs.sourceRoot, item.TruthProvenancePath)
		if err != nil || hashBytes(provenanceRaw) != item.TruthProvenanceSHA256 {
			return fmt.Errorf("authority case %d truth provenance is invalid", index+1)
		}
		if err := config.ValidateEvidence(rightsRaw, provenanceRaw, item, config.AuthoredAt.UTC()); err != nil {
			return fmt.Errorf("authority case %d rights and provenance do not authorize its source", index+1)
		}
	}
	return nil
}
