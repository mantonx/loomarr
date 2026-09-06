package fillerreview

import (
	"fmt"
	"time"

	"github.com/loomarr/loomarr/internal/fillereval"
)

func validateTemporalStructureHoldoutQuality(report TemporalMediaQualityReport, human TemporalHumanAssessmentSet, attestation TemporalHumanReviewAttestation, humanSHA, attestationFileSHA string, evidence TemporalTruthEvidenceManifest, evidenceSHA string, plannedAt time.Time) error {
	if report.HumanAssessmentSetSHA256 != humanSHA || report.HumanAttestationFileSHA256 != attestationFileSHA || report.EvidenceManifestSHA256 != evidenceSHA || report.SelectionSHA256 != evidence.SelectionSHA256 || report.Cases != len(evidence.Cases) || report.MeasuredAt.Before(attestation.LockedAt) || plannedAt.Before(report.MeasuredAt) || !reviewSHA256(report.HumanPackageSHA256) || !reviewSHA256(report.HumanPrivateMapSHA256) {
		return fmt.Errorf("temporal structure holdout media quality authority drift")
	}
	evidenceByAlias := make(map[string]TemporalTruthEvidenceCase, len(evidence.Cases))
	for _, item := range evidence.Cases {
		evidenceByAlias[item.Alias] = item
	}
	humanByAlias := make(map[string]fillereval.UnitKind, len(human.Assessments))
	for _, item := range human.Assessments {
		humanByAlias[item.EvidenceAlias] = item.Unit
	}
	recount := TemporalMediaQualityReport{}
	seen := make(map[string]struct{}, report.Cases)
	for _, item := range report.CaseMeasurements {
		evidenceCase, evidenceExists := evidenceByAlias[item.EvidenceAlias]
		humanUnit, humanExists := humanByAlias[item.EvidenceAlias]
		if !evidenceExists || !humanExists || item.SourceMediaSHA256 != evidenceCase.Video.SHA256 || item.DurationMS != evidenceCase.Video.DurationMS || item.HumanUnit != humanUnit {
			return fmt.Errorf("temporal structure holdout media quality case %q drifts from its evidence or human label", item.EvidenceAlias)
		}
		if _, duplicate := seen[item.EvidenceAlias]; duplicate {
			return fmt.Errorf("temporal structure holdout media quality repeats an alias")
		}
		seen[item.EvidenceAlias] = struct{}{}
		if item.OperationalFailure == "" && item.PolicyVerdict != mediaQualityContinue && item.PolicyVerdict != mediaQualityReview && item.PolicyVerdict != mediaQualityReject {
			return fmt.Errorf("temporal structure holdout media quality case %q has an invalid verdict", item.EvidenceAlias)
		}
		if item.OperationalFailure != "" && item.PolicyVerdict != "" {
			return fmt.Errorf("temporal structure holdout media quality case %q mixes failure and verdict", item.EvidenceAlias)
		}
		accumulateTemporalMediaQuality(&recount, item)
	}
	if len(seen) != report.Cases || recount.HumanUnusableCases != report.HumanUnusableCases || recount.OperationalFailures != report.OperationalFailures || recount.PolicyRejectCases != report.PolicyRejectCases || recount.PolicyReviewCases != report.PolicyReviewCases || recount.PolicyContinueCases != report.PolicyContinueCases || recount.HumanUnusableHeld != report.HumanUnusableHeld || recount.HumanUnusableContinued != report.HumanUnusableContinued || recount.OtherHumanLabelsHeld != report.OtherHumanLabelsHeld || recount.OtherHumanLabelsContinued != report.OtherHumanLabelsContinued {
		return fmt.Errorf("temporal structure holdout media quality summary does not match its cases")
	}
	return nil
}
