package fillerstructure

import (
	"errors"
	"reflect"
)

var ErrAssessmentLedgerConflict = errors.New("filler structure assessment ledger conflicts with existing authority")

func ValidateAssessmentReservation(reservation AssessmentReservation) error {
	if reservation.SchemaVersion != AssessmentReservationSchemaVersion ||
		reservation.ContractVersion != AssessmentReservationContractVersion ||
		!digest(reservation.SHA256) || reservation.SHA256 != AssessmentReservationSHA256(reservation) ||
		!digest(reservation.RequestSHA256) || !validSource(reservation.Source) ||
		!validAssessmentMedia(reservation.Media, reservation.Source) || ValidateAssessorProfile(reservation.Assessor) != nil ||
		!digest(reservation.MetadataSnapshotSHA256) ||
		!digest(reservation.PromptSHA256) || !digest(reservation.SchemaSHA256) ||
		!canonicalIdentity(reservation.ExpectedResolvedModel) || !canonicalIdentity(reservation.UpstreamProvider) ||
		!canonicalIdentity(reservation.UpstreamProviderSlug) || reservation.RequestedNanoUSD <= 0 ||
		reservation.MaximumChargeNanoUSD <= 0 || reservation.MaximumChargeNanoUSD > reservation.RequestedNanoUSD ||
		reservation.RequestedAt.IsZero() || reservation.RequestedAt != reservation.RequestedAt.UTC() {
		return errors.New("filler structure assessment reservation is invalid")
	}
	return nil
}

func ValidateAssessmentLedgerEntry(entry AssessmentLedgerEntry) error {
	if err := ValidateAssessmentReservation(entry.Reservation); err != nil {
		return err
	}
	switch entry.State {
	case AssessmentLedgerOpen, AssessmentLedgerHeldBudget:
		if entry.Record != nil {
			return errors.New("filler structure assessment ledger entry closes an open reservation")
		}
	case AssessmentLedgerSettled:
		if entry.Record == nil || ValidateAssessmentRecord(*entry.Record) != nil ||
			!assessmentRecordMatchesReservation(*entry.Record, entry.Reservation) {
			return errors.New("filler structure assessment ledger settlement is invalid")
		}
	default:
		return errors.New("filler structure assessment ledger state is invalid")
	}
	return nil
}

func assessmentRecordMatchesReservation(record AssessmentRecord, reservation AssessmentReservation) bool {
	if record.RequestSHA256 != reservation.RequestSHA256 || record.Source != reservation.Source ||
		record.Media != reservation.Media || !reflect.DeepEqual(record.Assessor, reservation.Assessor) ||
		record.MetadataSnapshotSHA256 != reservation.MetadataSnapshotSHA256 ||
		record.PromptSHA256 != reservation.PromptSHA256 || record.SchemaSHA256 != reservation.SchemaSHA256 ||
		record.UpstreamProvider != reservation.UpstreamProvider || record.UpstreamProviderSlug != reservation.UpstreamProviderSlug ||
		record.RequestedNanoUSD != reservation.RequestedNanoUSD || record.AssessedAt.Before(reservation.RequestedAt) {
		return false
	}
	if record.State == AssessmentRecordHeldBudget {
		return true
	}
	if (record.ResolvedProvider != "" && record.ResolvedProvider != reservation.Assessor.Provider) ||
		(record.ResolvedModel != "" && record.ResolvedModel != reservation.ExpectedResolvedModel) {
		return false
	}
	return true
}
