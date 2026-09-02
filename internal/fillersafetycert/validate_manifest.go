package fillersafetycert

import (
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/loomarr/loomarr/internal/fillersafety"
)

func validateManifest(authority Authority, authoritySHA string, manifest ResultManifest, scoredAt time.Time) error {
	if manifest.SchemaVersion != SchemaVersion || manifest.ContractVersion != ContractVersion ||
		manifest.ManifestedAt.IsZero() || manifest.ManifestedAt.After(scoredAt) ||
		manifest.AuthoritySHA256 != authoritySHA || len(manifest.Runs) != len(authority.Cases) {
		return fmt.Errorf("manifest identity, time, authority, or run count is invalid")
	}
	cases := make(map[string]AuthorityCase, len(authority.Cases))
	for _, item := range authority.Cases {
		cases[item.Alias] = item
	}
	seen := map[string]struct{}{}
	previousAlias := ""
	for _, result := range manifest.Runs {
		item, exists := cases[result.Alias]
		if !exists || result.Alias <= previousAlias {
			return fmt.Errorf("manifest contains an extra, duplicate, or disordered alias")
		}
		previousAlias = result.Alias
		if _, duplicate := seen[result.Alias]; duplicate {
			return fmt.Errorf("manifest repeats a case alias")
		}
		seen[result.Alias] = struct{}{}
		if err := validateResultRun(authority, authoritySHA, manifest.ManifestedAt, item, result); err != nil {
			return fmt.Errorf("run %s: %w", result.Alias, err)
		}
	}
	return nil
}

func validateResultRun(authority Authority, authoritySHA string, manifestedAt time.Time, item AuthorityCase, result ResultRun) error {
	run := result.Run
	if err := fillersafety.ValidateLedgerRun(run); err != nil || !run.CreatedAt.After(authority.AuthoredAt) ||
		run.CreatedAt.After(manifestedAt) || run.ClipHash != item.SourceSHA256 || run.SourceSHA256 != item.SourceSHA256 ||
		run.AuthoritySHA256 != item.SourceAuthoritySHA256 || run.CertificationSHA256 != authoritySHA ||
		run.PolicySHA256 != authority.PolicySHA256 || run.ProposerSHA256 != authority.ProposerSHA256 ||
		run.Implementation != authority.Implementation || run.SourceBytes != item.SourceBytes || run.DurationMS != item.DurationMS {
		return fmt.Errorf("run does not bind the pre-authored source and evaluator identities")
	}
	if len(result.Events) < 3 {
		return fmt.Errorf("run ledger is incomplete")
	}
	for index, event := range result.Events {
		if event.RunID != run.ID || event.Ordinal != index || event.CreatedAt.Before(run.CreatedAt) || event.CreatedAt.After(manifestedAt) {
			return fmt.Errorf("event identity, ordinal, or time is invalid")
		}
		if err := fillersafety.ValidateLedgerAppend(result.Events[:index], event); err != nil {
			return fmt.Errorf("event sequence is invalid")
		}
	}
	terminalEvent := result.Events[len(result.Events)-1]
	if terminalEvent.Terminal == nil || terminalEvent.ID != result.TerminalEventID || !validSHA256(result.TerminalSHA256) {
		return fmt.Errorf("terminal identity is incomplete")
	}
	digest, err := fillersafety.LedgerEventSHA256(terminalEvent)
	if err != nil || digest != result.TerminalSHA256 {
		return fmt.Errorf("terminal digest does not reproduce")
	}
	if err := validateRunSemantics(authority, item, result.Events); err != nil {
		return err
	}
	return nil
}

func validateRunSemantics(authority Authority, item AuthorityCase, events []fillersafety.LedgerEvent) error {
	source, proposal, terminal := events[0].Source, events[1].Proposal, events[len(events)-1].Terminal
	if source == nil || source.Audio.StartMS != 0 || source.Audio.EndMS != item.DurationMS || source.Video != source.Audio ||
		proposal == nil || proposal.ProposerSHA256 != authority.ProposerSHA256 || terminal == nil ||
		proposal.State != terminal.Evidence.ProposalState || !reflect.DeepEqual(proposal.Candidates, terminal.Evidence.Candidates) {
		return fmt.Errorf("source plan, proposal, and terminal evidence do not agree")
	}
	if proposal.State == fillersafety.ProposalFailed {
		for _, event := range events[2 : len(events)-1] {
			if event.Reserve != nil || event.Settle != nil {
				return fmt.Errorf("failed proposal contains hosted inference")
			}
		}
		return nil
	}
	for _, assessment := range terminal.Evidence.Audio {
		if assessment.MatchedRuleIDs == nil || assessment.State == fillersafety.AudioDetected && len(assessment.MatchedRuleIDs) == 0 {
			return fmt.Errorf("current audio evidence lacks closed rule attribution")
		}
	}
	return validateHostedEvidence(authority, proposal.Candidates, terminal.Evidence, events[2:len(events)-1])
}

func validateHostedEvidence(authority Authority, candidates []fillersafety.Candidate, evidence fillersafety.Evidence, events []fillersafety.LedgerEvent) error {
	settlements := map[string]fillersafety.InferenceSettled{}
	for _, event := range events {
		if event.Settle != nil {
			settlements[event.Settle.ReservationEventID] = *event.Settle
		}
	}
	audioCounts := make(map[string]int, len(candidates))
	videoCount := 0
	assessments := make(map[string]fillersafety.AudioAssessment, len(evidence.Audio))
	for _, assessment := range evidence.Audio {
		assessments[assessment.CandidateID] = assessment
	}
	for _, event := range events {
		if event.Reserve == nil {
			continue
		}
		reservation := *event.Reserve
		settlement, settled := settlements[event.ID]
		if reservation.State == fillersafety.ReservationAccepted && !settled ||
			reservation.State == fillersafety.ReservationHeldBudget && settled {
			return fmt.Errorf("reservation settlement coverage is invalid")
		}
		if slices.Equal(reservation.Modalities, authority.AudioRoute.Modalities) {
			assessment, exists := assessments[reservation.CandidateID]
			if !exists || !routeReservationMatches(authority.AudioRoute, reservation) {
				return fmt.Errorf("audio reservation is not bound to a candidate and route")
			}
			audioCounts[reservation.CandidateID]++
			if !assessmentMatchesSettlement(assessment.State, reservation.State, settlement, settled) ||
				settled && !routeSettlementMatches(authority.AudioRoute, settlement) {
				return fmt.Errorf("audio settlement and terminal evidence do not agree")
			}
		} else if slices.Equal(reservation.Modalities, authority.VideoRoute.Modalities) {
			if reservation.CandidateID != "" || !routeReservationMatches(authority.VideoRoute, reservation) {
				return fmt.Errorf("video reservation is not bound to its route")
			}
			videoCount++
			if !videoMatchesSettlement(evidence.Video, reservation.State, settlement, settled) ||
				settled && !routeSettlementMatches(authority.VideoRoute, settlement) {
				return fmt.Errorf("video settlement and terminal evidence do not agree")
			}
		} else {
			return fmt.Errorf("reservation uses an undeclared modality route")
		}
	}
	for _, candidate := range candidates {
		if audioCounts[candidate.ID] != 1 {
			return fmt.Errorf("every proposed candidate must have exactly one audio reservation")
		}
	}
	allAbsent := true
	for _, assessment := range evidence.Audio {
		allAbsent = allAbsent && assessment.State == fillersafety.AudioAbsent
	}
	wantVideo := 0
	if allAbsent {
		wantVideo = 1
	}
	if videoCount != wantVideo {
		return fmt.Errorf("complete-video corroboration coverage is invalid")
	}
	return nil
}

func routeReservationMatches(route RouteAuthority, reservation fillersafety.InferenceReserved) bool {
	return reservation.RequestedProvider == route.RequestedProvider && reservation.RequestedModel == route.RequestedModel &&
		reservation.UpstreamProvider == route.UpstreamProvider && reservation.CapabilitySHA256 == route.CapabilitySHA256 &&
		reservation.PromptSHA256 == route.PromptSHA256 && reservation.SchemaSHA256 == route.SchemaSHA256 &&
		slices.Equal(reservation.Modalities, route.Modalities)
}

func routeSettlementMatches(route RouteAuthority, settlement fillersafety.InferenceSettled) bool {
	if settlement.State != fillersafety.SettlementCompleted {
		return (settlement.ResolvedProvider == "" || settlement.ResolvedProvider == route.ResolvedProvider) &&
			(settlement.ResolvedModel == "" || settlement.ResolvedModel == route.ResolvedModel) &&
			(settlement.UpstreamProvider == "" || settlement.UpstreamProvider == route.UpstreamProvider)
	}
	return settlement.ResolvedProvider == route.ResolvedProvider && settlement.ResolvedModel == route.ResolvedModel &&
		settlement.UpstreamProvider == route.UpstreamProvider
}

func assessmentMatchesSettlement(state fillersafety.AudioState, reservation fillersafety.ReservationState, settlement fillersafety.InferenceSettled, settled bool) bool {
	if reservation == fillersafety.ReservationHeldBudget {
		return state == fillersafety.AudioFailed
	}
	if !settled {
		return false
	}
	if settlement.State == fillersafety.SettlementCompleted {
		return string(state) == settlement.Outcome
	}
	if settlement.Failure == fillersafety.FailureInvalidResponse {
		return state == fillersafety.AudioInvalidResponse || state == fillersafety.AudioDetectedUnprojectable
	}
	return state == fillersafety.AudioFailed
}

func videoMatchesSettlement(state fillersafety.VideoState, reservation fillersafety.ReservationState, settlement fillersafety.InferenceSettled, settled bool) bool {
	if reservation == fillersafety.ReservationHeldBudget {
		return state == fillersafety.VideoFailed
	}
	if !settled {
		return false
	}
	if settlement.State == fillersafety.SettlementCompleted {
		return string(state) == settlement.Outcome
	}
	if settlement.Failure == fillersafety.FailureInvalidResponse {
		return state == fillersafety.VideoInvalidResponse || state == fillersafety.VideoProhibitedUnprojectable
	}
	return state == fillersafety.VideoFailed
}
