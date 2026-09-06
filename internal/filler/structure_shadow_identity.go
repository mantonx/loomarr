package filler

import (
	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

func structureMaterializationAuthorityIdentity(materialization *StructureMaterializationPolicy, artifact *fillerstructure.Artifact) string {
	if materialization == nil || artifact == nil {
		return ""
	}
	switch artifact.Decision.Input.Kind {
	case fillerstructure.AssessmentInputCompleteVideo:
		if materialization.Authority != nil && fillerstructure.ValidateAuthority(*materialization.Authority) == nil {
			return materialization.Authority.SHA256
		}
	case fillerstructure.AssessmentInputWindowMediaSet:
		if materialization.WindowAuthority != nil && fillerstructurewindow.ValidateMaterializationAuthority(*materialization.WindowAuthority) == nil {
			return materialization.WindowAuthority.SHA256
		}
	}
	return ""
}
