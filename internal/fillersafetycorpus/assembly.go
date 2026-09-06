package fillersafetycorpus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
	"github.com/loomarr/loomarr/internal/fillersafetycert"
)

// AssembleReviewDraft verifies complete prepared cohorts and publishes the one
// self-contained draft and worklist that independent reviewers share. It runs
// no reviewer, model, certification, or ingestion operation.
func AssembleReviewDraft(ctx context.Context, config ReviewDraftConfig) (ReviewDraftResult, error) {
	if ctx == nil || ctx.Err() != nil {
		return ReviewDraftResult{}, fmt.Errorf("spoken corpus assembly requires an active context")
	}
	if err := validateReviewDraftConfig(config); err != nil {
		return ReviewDraftResult{}, err
	}
	plan, planRaw, inputRoot, err := loadAssemblyPlan(config)
	if err != nil {
		return ReviewDraftResult{}, err
	}
	if err := validateAssemblyPlan(plan); err != nil {
		return ReviewDraftResult{}, err
	}
	boundedContext, cancel := context.WithTimeout(ctx, time.Duration(plan.MaximumWallTimeMS)*time.Millisecond)
	defer cancel()
	ctx = boundedContext
	tracker := &assemblyByteTracker{
		maximumInput: plan.MaximumInputBytes, maximumOutput: plan.MaximumOutputBytes,
		inputs: make(map[string]FileAuthority), input: int64(len(planRaw)),
	}
	if tracker.input > tracker.maximumInput {
		return ReviewDraftResult{}, fmt.Errorf("spoken corpus assembly exceeds its input byte ceiling")
	}
	policyRaw, err := readPrivateAssemblyFile(inputRoot, plan.Policy, maximumAssemblyPolicyBytes)
	if err != nil {
		return ReviewDraftResult{}, fmt.Errorf("verify spoken-safety policy: %w", err)
	}
	if err := tracker.addUniqueInput(inputRoot, plan.Policy); err != nil {
		return ReviewDraftResult{}, err
	}
	stage, err := beginPrivateStage(config.OutputDirectory)
	if err != nil {
		return ReviewDraftResult{}, err
	}
	defer stage.cleanup()
	if err := tracker.addOutput(int64(len(policyRaw))); err != nil {
		return ReviewDraftResult{}, err
	}
	if err := writePrivate(filepath.Join(stage.path, "policy.json"), policyRaw); err != nil {
		return ReviewDraftResult{}, err
	}
	draft := fillersafetycert.AuthorityDraft{
		SchemaVersion: fillersafetycert.AuthorityDraftSchemaVersion, ContractVersion: fillersafetycert.AuthorityDraftContractVersion,
		ChallengeKind: plan.ChallengeKind, PolicySHA256: plan.Policy.SHA256, ProposerSHA256: plan.ProposerSHA256,
		ProposerFamily: plan.ProposerFamily, Implementation: plan.Implementation,
		AudioRoute: plan.AudioRoute, VideoRoute: plan.VideoRoute,
	}
	reviewCases := make(map[string]ReviewWorklistCase, plan.ExpectedCases)
	caseIDs, families, sources := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	positiveFamilies, cleanFamilies := 0, 0
	for cohortIndex, authority := range plan.Cohorts {
		if err := ctx.Err(); err != nil {
			return ReviewDraftResult{}, fmt.Errorf("spoken corpus assembly exceeded its wall-time ceiling")
		}
		cohortRoot, err := resolvePrivateAssemblyDirectory(inputRoot, authority.SourceRoot)
		if err != nil {
			return ReviewDraftResult{}, fmt.Errorf("resolve spoken corpus cohort %d: %w", cohortIndex+1, err)
		}
		cohort, cohortRaw, err := readPrivateAssemblyJSON[PreparedCohort](inputRoot, authority.CohortPath, authority.SHA256, maximumPreparedCohortDocumentBytes)
		if err != nil {
			return ReviewDraftResult{}, fmt.Errorf("read spoken corpus cohort %d: %w", cohortIndex+1, err)
		}
		cohortTracker := &cohortByteTracker{maximum: authority.MaximumBytes, bytes: int64(len(cohortRaw)), inputs: make(map[string]FileAuthority)}
		if cohortTracker.bytes > cohortTracker.maximum {
			return ReviewDraftResult{}, fmt.Errorf("spoken corpus cohort %d exceeds its byte ceiling", cohortIndex+1)
		}
		if err := tracker.addDocument(inputRoot, authority.CohortPath, authority.SHA256, int64(len(cohortRaw))); err != nil {
			return ReviewDraftResult{}, err
		}
		if err := validatePreparedCandidateCohort(cohort, authority.ExpectedCases, authority.Kind, authority.Dataset, plan.AssembledAt); err != nil {
			return ReviewDraftResult{}, fmt.Errorf("validate spoken corpus cohort %d: %w", cohortIndex+1, err)
		}
		for caseIndex, item := range cohort.Cases {
			if err := ctx.Err(); err != nil {
				return ReviewDraftResult{}, fmt.Errorf("spoken corpus assembly exceeded its wall-time ceiling")
			}
			if item.SourceAuthority.PolicySHA256 != plan.Policy.SHA256 || item.SourceAuthority.Implementation != plan.Implementation {
				return ReviewDraftResult{}, fmt.Errorf("spoken corpus cohort %d mixes policy or implementation identity", cohortIndex+1)
			}
			if _, duplicate := caseIDs[item.CaseID]; duplicate {
				return ReviewDraftResult{}, fmt.Errorf("spoken corpus assembly repeats a case identity")
			}
			if _, duplicate := families[item.SourceFamily]; duplicate {
				return ReviewDraftResult{}, fmt.Errorf("spoken corpus assembly repeats a source family")
			}
			if _, duplicate := sources[item.SourceAuthority.SourceSHA256]; duplicate {
				return ReviewDraftResult{}, fmt.Errorf("spoken corpus assembly repeats source content")
			}
			caseIDs[item.CaseID], families[item.SourceFamily], sources[item.SourceAuthority.SourceSHA256] = struct{}{}, struct{}{}, struct{}{}
			label, ok := preparedLabel(item.Claim)
			if !ok {
				return ReviewDraftResult{}, fmt.Errorf("spoken corpus cohort %d case %d has an invalid claim", cohortIndex+1, caseIndex+1)
			}
			caseRootRelative := "cases/" + item.CaseID
			caseRoot := filepath.Join(stage.path, filepath.FromSlash(caseRootRelative))
			if err := os.MkdirAll(caseRoot, 0o700); err != nil {
				return ReviewDraftResult{}, err
			}
			sourceRelative := caseRootRelative + "/source.mp4"
			if err := tracker.snapshot(cohortRoot, FileAuthority{Path: item.SourcePath, SHA256: item.SourceAuthority.SourceSHA256, Bytes: item.SourceAuthority.SourceBytes},
				filepath.Join(stage.path, filepath.FromSlash(sourceRelative)), cohortTracker); err != nil {
				return ReviewDraftResult{}, fmt.Errorf("snapshot spoken corpus cohort %d case %d source: %w", cohortIndex+1, caseIndex+1, err)
			}
			transcriptRelative := ""
			if item.TranscriptPath != "" {
				transcriptRelative = caseRootRelative + "/transcript.txt"
				if err := tracker.snapshot(cohortRoot, FileAuthority{Path: item.TranscriptPath, SHA256: item.TranscriptSHA256, Bytes: item.TranscriptBytes},
					filepath.Join(stage.path, filepath.FromSlash(transcriptRelative)), cohortTracker); err != nil {
					return ReviewDraftResult{}, fmt.Errorf("snapshot spoken corpus cohort %d case %d transcript: %w", cohortIndex+1, caseIndex+1, err)
				}
			}
			provenanceRelative := caseRootRelative + "/provenance.json"
			if err := tracker.snapshot(cohortRoot, FileAuthority{Path: item.TruthProvenancePath, SHA256: item.TruthProvenanceSHA256, Bytes: item.TruthProvenanceBytes},
				filepath.Join(stage.path, filepath.FromSlash(provenanceRelative)), cohortTracker); err != nil {
				return ReviewDraftResult{}, fmt.Errorf("snapshot spoken corpus cohort %d case %d provenance: %w", cohortIndex+1, caseIndex+1, err)
			}
			rightsRelative := caseRootRelative + "/rights.json"
			if err := tracker.snapshot(cohortRoot, FileAuthority{Path: item.RightsPath, SHA256: item.RightsSHA256, Bytes: item.RightsBytes},
				filepath.Join(stage.path, filepath.FromSlash(rightsRelative)), cohortTracker); err != nil {
				return ReviewDraftResult{}, fmt.Errorf("snapshot spoken corpus cohort %d case %d rights: %w", cohortIndex+1, caseIndex+1, err)
			}
			positiveIntervals := make([]fillersafetycert.PositiveInterval, len(item.PositiveIntervals))
			for index, interval := range item.PositiveIntervals {
				positiveIntervals[index] = fillersafetycert.PositiveInterval{RuleID: interval.RuleID, StartMS: interval.StartMS, EndMS: interval.EndMS}
			}
			draft.Cases = append(draft.Cases, fillersafetycert.AuthorityDraftCase{
				CaseID: item.CaseID, SourcePath: sourceRelative, SourceAuthority: item.SourceAuthority,
				SourceFamily: item.SourceFamily, TruthProvenancePath: provenanceRelative,
				TruthProvenanceSHA256: item.TruthProvenanceSHA256, RightsPath: rightsRelative,
				RightsSHA256: item.RightsSHA256, Label: label, Locale: item.Locale,
				Slices: slices.Clone(item.Slices), PositiveIntervals: positiveIntervals,
			})
			sourceAuthoritySHA, err := fillersafety.SourceAuthoritySHA256(item.SourceAuthority)
			if err != nil {
				return ReviewDraftResult{}, fmt.Errorf("hash spoken corpus cohort %d case %d authority: %w", cohortIndex+1, caseIndex+1, err)
			}
			reviewCases[item.CaseID] = ReviewWorklistCase{
				CaseID: item.CaseID, SourcePath: sourceRelative, SourceSHA256: item.SourceAuthority.SourceSHA256,
				SourceAuthoritySHA256: sourceAuthoritySHA, SourceBytes: item.SourceAuthority.SourceBytes,
				DurationMS: item.SourceAuthority.DurationMS, TranscriptPath: transcriptRelative,
				TranscriptSHA256: item.TranscriptSHA256, TranscriptBytes: item.TranscriptBytes,
				TruthProvenancePath: provenanceRelative, TruthProvenanceSHA256: item.TruthProvenanceSHA256,
				TruthProvenanceBytes: item.TruthProvenanceBytes, RightsPath: rightsRelative,
				RightsSHA256: item.RightsSHA256, RightsBytes: item.RightsBytes,
				Claim: label, Locale: item.Locale, Slices: slices.Clone(item.Slices),
				PositiveIntervals: slices.Clone(item.PositiveIntervals),
			}
			if label == fillersafetycert.LabelPositive {
				positiveFamilies++
			} else {
				cleanFamilies++
			}
		}
	}
	if len(draft.Cases) != plan.ExpectedCases {
		return ReviewDraftResult{}, fmt.Errorf("spoken corpus assembly did not materialize the exact case count")
	}
	slices.SortFunc(draft.Cases, func(a, b fillersafetycert.AuthorityDraftCase) int { return strings.Compare(a.CaseID, b.CaseID) })
	draftRaw, draftSHA, err := fillersafetycert.MarshalCertificationDraft(draft)
	if err != nil {
		return ReviewDraftResult{}, fmt.Errorf("validate reviewable spoken corpus draft: %w", err)
	}
	if err := tracker.addOutput(int64(len(draftRaw))); err != nil {
		return ReviewDraftResult{}, err
	}
	if err := writePrivate(filepath.Join(stage.path, "draft.json"), draftRaw); err != nil {
		return ReviewDraftResult{}, err
	}
	worklist := ReviewWorklist{
		SchemaVersion: ReviewWorklistSchemaVersion, ContractVersion: ReviewWorklistContractVersion,
		AssembledAt: plan.AssembledAt.UTC(), DraftSHA256: draftSHA, PolicyPath: "policy.json", PolicySHA256: plan.Policy.SHA256,
	}
	for _, item := range draft.Cases {
		worklist.Cases = append(worklist.Cases, reviewCases[item.CaseID])
	}
	if err := validateReviewWorklist(worklist, draft, draftSHA); err != nil {
		return ReviewDraftResult{}, err
	}
	worklistRaw, err := marshalPrivateJSON(worklist)
	if err != nil {
		return ReviewDraftResult{}, err
	}
	if int64(len(worklistRaw)) > (tracker.maximumOutput-tracker.output)/2 {
		return ReviewDraftResult{}, fmt.Errorf("spoken corpus assembly exceeds its output byte ceiling")
	}
	for _, name := range []string{"primary-review-one.json", "primary-review-two.json"} {
		if err := writePrivate(filepath.Join(stage.path, name), worklistRaw); err != nil {
			return ReviewDraftResult{}, err
		}
		tracker.output += int64(len(worklistRaw))
	}
	if err := ctx.Err(); err != nil {
		return ReviewDraftResult{}, fmt.Errorf("spoken corpus assembly exceeded its wall-time ceiling")
	}
	if err := stage.publish(); err != nil {
		return ReviewDraftResult{}, err
	}
	return ReviewDraftResult{
		Cases: len(draft.Cases), PositiveFamilies: positiveFamilies, CleanFamilies: cleanFamilies,
		DraftSHA256: draftSHA, WorklistSHA256: hashBytes(worklistRaw), InputBytes: tracker.input, OutputBytes: tracker.output,
	}, nil
}

func marshalPrivateJSON(value any) ([]byte, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
