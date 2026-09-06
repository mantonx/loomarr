package fillersafetyreview

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
	"github.com/loomarr/loomarr/internal/fillersafetycorpus"
)

func verifyWorklist(
	ctx context.Context,
	loaded *loadedInputs,
	now time.Time,
	processor fillersafetycorpus.KnownScriptHostedProcessor,
) error {
	worklist := loaded.worklist
	if worklist.SchemaVersion != fillersafetycorpus.ReviewWorklistSchemaVersion ||
		worklist.ContractVersion != fillersafetycorpus.ReviewWorklistContractVersion ||
		worklist.AssembledAt.IsZero() || worklist.AssembledAt.After(now) ||
		worklist.DraftSHA256 != loaded.draftSHA256 || len(worklist.Cases) != loaded.plan.ExpectedCases ||
		len(loaded.draft.Cases) != loaded.plan.ExpectedCases || !validRelative(worklist.PolicyPath) {
		return fmt.Errorf("model review worklist identity or exact case count is invalid")
	}
	seen := map[string]fillersafetycorpus.FileAuthority{}
	authorizedRights := map[fillersafetycorpus.FileAuthority]bool{}
	loaded.knownScriptRights = make(map[string]fillersafetycorpus.FileAuthority)
	for _, authority := range []fillersafetycorpus.FileAuthority{
		loaded.plan.Draft,
		loaded.plan.Worklist,
		loaded.plan.Snapshot,
		{Path: worklist.PolicyPath, SHA256: loaded.policySHA256, Bytes: loaded.policyBytes},
	} {
		if previous, duplicate := seen[authority.Path]; duplicate && previous != authority {
			return fmt.Errorf("model review inputs reuse a path with conflicting authorities")
		}
		seen[authority.Path] = authority
	}
	addEvidence := func(authority fillersafetycorpus.FileAuthority, maximum int64) error {
		if !validRelative(authority.Path) || !validSHA256(authority.SHA256) || authority.Bytes <= 0 || authority.Bytes > maximum {
			return fmt.Errorf("model review evidence authority is invalid")
		}
		if previous, ok := seen[authority.Path]; ok {
			if previous != authority {
				return fmt.Errorf("model review evidence path has conflicting authorities")
			}
			return nil
		}
		if loaded.inputBytes > loaded.plan.MaximumInputBytes-authority.Bytes {
			return fmt.Errorf("model review inputs exceed byte ceiling")
		}
		if err := hashPrivateAuthority(loaded.root, authority, maximum); err != nil {
			return err
		}
		loaded.inputBytes += authority.Bytes
		seen[authority.Path] = authority
		return nil
	}
	for index, item := range worklist.Cases {
		if err := ctx.Err(); err != nil {
			return err
		}
		draftCase := loaded.draft.Cases[index]
		authoritySHA256, err := fillersafety.SourceAuthoritySHA256(draftCase.SourceAuthority)
		if err != nil || item.CaseID != draftCase.CaseID || item.SourcePath != draftCase.SourcePath ||
			item.SourceSHA256 != draftCase.SourceAuthority.SourceSHA256 ||
			item.SourceBytes != draftCase.SourceAuthority.SourceBytes || item.DurationMS != draftCase.SourceAuthority.DurationMS ||
			item.SourceAuthoritySHA256 != authoritySHA256 || item.TruthProvenancePath != draftCase.TruthProvenancePath ||
			item.TruthProvenanceSHA256 != draftCase.TruthProvenanceSHA256 || item.RightsPath != draftCase.RightsPath ||
			item.RightsSHA256 != draftCase.RightsSHA256 || item.Locale != draftCase.Locale ||
			!slices.Equal(item.Slices, draftCase.Slices) || !equalIntervals(item.PositiveIntervals, draftCase.PositiveIntervals) ||
			claimLabel(item.Claim) != draftCase.Label || draftCase.SourceAuthority.MeasuredAt.After(now) ||
			len(item.PositiveIntervals) > 256 {
			return fmt.Errorf("model review worklist case %d does not bind the draft", index+1)
		}
		if err := requirePrivateRegular(loaded.root, item.SourcePath, item.SourceBytes); err != nil {
			return fmt.Errorf("model review case %d source is not private: %w", index+1, err)
		}
		sourcePath, err := resolveRootPath(loaded.root, item.SourcePath)
		if err != nil {
			return fmt.Errorf("model review case %d source path is invalid", index+1)
		}
		mediaPlan, err := fillersafety.PlanCompleteMedia(ctx, fillersafety.SourceRequest{
			Authority: draftCase.SourceAuthority, Path: sourcePath,
		})
		if err != nil {
			return fmt.Errorf("model review case %d source bytes are invalid", index+1)
		}
		if err := mediaPlan.Close(); err != nil {
			return fmt.Errorf("model review case %d source snapshot cleanup failed", index+1)
		}
		sourceAuthority := fillersafetycorpus.FileAuthority{
			Path: item.SourcePath, SHA256: item.SourceSHA256, Bytes: item.SourceBytes,
		}
		if previous, duplicate := seen[item.SourcePath]; duplicate {
			if previous != sourceAuthority {
				return fmt.Errorf("model review case %d source path has conflicting authority", index+1)
			}
		} else {
			if loaded.inputBytes > loaded.plan.MaximumInputBytes-item.SourceBytes {
				return fmt.Errorf("model review inputs exceed byte ceiling")
			}
			loaded.inputBytes += item.SourceBytes
			seen[item.SourcePath] = sourceAuthority
		}
		if item.TranscriptPath == "" {
			if item.TranscriptSHA256 != "" || item.TranscriptBytes != 0 {
				return fmt.Errorf("model review case %d transcript authority is partial", index+1)
			}
		} else if err := addEvidence(fillersafetycorpus.FileAuthority{
			Path: item.TranscriptPath, SHA256: item.TranscriptSHA256, Bytes: item.TranscriptBytes,
		}, 1<<20); err != nil {
			return fmt.Errorf("model review case %d transcript is invalid", index+1)
		}
		if err := addEvidence(fillersafetycorpus.FileAuthority{Path: item.TruthProvenancePath, SHA256: item.TruthProvenanceSHA256, Bytes: item.TruthProvenanceBytes}, maximumDocumentBytes); err != nil {
			return fmt.Errorf("model review case %d truth provenance is invalid", index+1)
		}
		rightsAuthority := fillersafetycorpus.FileAuthority{
			Path: item.RightsPath, SHA256: item.RightsSHA256, Bytes: item.RightsBytes,
		}
		if err := addEvidence(rightsAuthority, maximumDocumentBytes); err != nil {
			return fmt.Errorf("model review case %d rights evidence is invalid", index+1)
		}
		applies, alreadyAuthorized := authorizedRights[rightsAuthority]
		if !alreadyAuthorized {
			var err error
			applies, err = authorizeKnownScriptRights(loaded.root, rightsAuthority, now, processor)
			if err != nil {
				return fmt.Errorf("model review case %d processor authorization failed", index+1)
			}
			authorizedRights[rightsAuthority] = applies
		}
		if applies {
			loaded.knownScriptRights[item.CaseID] = rightsAuthority
		}
	}
	return nil
}

func authorizeKnownScriptRights(
	root string,
	authority fillersafetycorpus.FileAuthority,
	at time.Time,
	processor fillersafetycorpus.KnownScriptHostedProcessor,
) (bool, error) {
	path, err := resolveRootPath(root, authority.Path)
	if err != nil {
		return false, fmt.Errorf("resolve model review rights")
	}
	raw, err := readPrivateFile(path, maximumDocumentBytes)
	if err != nil || int64(len(raw)) != authority.Bytes || hashBytes(raw) != authority.SHA256 {
		return false, fmt.Errorf("model review rights bytes changed during authorization")
	}
	return fillersafetycorpus.AuthorizeKnownScriptProcessor(raw, at, processor)
}

func claimLabel(claim string) string {
	switch claim {
	case fillersafetycorpus.PreparedCohortKindPositiveCandidate:
		return fillersafetycert.LabelPositive
	case fillersafetycorpus.PreparedCohortKindCleanCandidate:
		return fillersafetycert.LabelClean
	default:
		return ""
	}
}

func equalIntervals(prepared []fillersafetycorpus.PreparedPositiveInterval, draft []fillersafetycert.PositiveInterval) bool {
	return slices.EqualFunc(prepared, draft, func(a fillersafetycorpus.PreparedPositiveInterval, b fillersafetycert.PositiveInterval) bool {
		return a.RuleID == b.RuleID && a.StartMS == b.StartMS && a.EndMS == b.EndMS
	})
}
