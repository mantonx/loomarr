package fillerstructurewindow

import (
	"errors"
	"reflect"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

func ValidateCallReservation(reservation CallReservation) error {
	if reservation.SchemaVersion != CallReservationSchemaVersion || reservation.ContractVersion != CallReservationContractVersion ||
		!contentHash(reservation.SHA256) || reservation.SHA256 != CallReservationSHA256(reservation) ||
		!contentHash(reservation.RequestSHA256) || ValidateMediaSet(reservation.MediaSet) != nil ||
		reservation.WindowOrdinal < 0 || reservation.WindowOrdinal >= len(reservation.MediaSet.Windows) ||
		fillerstructure.ValidateAssessorProfile(reservation.Assessor) != nil || !contentHash(reservation.MetadataSnapshotSHA256) ||
		!contentHash(reservation.PromptSHA256) ||
		!contentHash(reservation.SchemaSHA256) || !callIdentity(reservation.ExpectedResolvedModel) ||
		!callIdentity(reservation.UpstreamProvider) || !callIdentity(reservation.UpstreamProviderSlug) ||
		reservation.RequestedNanoUSD <= 0 || reservation.MaximumChargeNanoUSD <= 0 ||
		reservation.MaximumChargeNanoUSD > reservation.RequestedNanoUSD || reservation.RequestedAt.IsZero() ||
		reservation.RequestedAt != reservation.RequestedAt.UTC() {
		return errors.New("structure window call reservation is invalid")
	}
	return nil
}

func ValidateCallLedgerEntry(entry CallLedgerEntry) error {
	if err := ValidateCallReservation(entry.Reservation); err != nil {
		return err
	}
	switch entry.State {
	case CallLedgerOpen, CallLedgerHeldBudget:
		if entry.Record != nil {
			return errors.New("structure window call ledger entry closes an open reservation")
		}
	case CallLedgerSettled:
		if entry.Record == nil || ValidateCallRecord(*entry.Record) != nil ||
			!callRecordMatchesReservation(*entry.Record, entry.Reservation) {
			return errors.New("structure window call ledger settlement is invalid")
		}
	default:
		return errors.New("structure window call ledger state is invalid")
	}
	return nil
}

func callRecordMatchesReservation(record CallRecord, reservation CallReservation) bool {
	if record.RequestSHA256 != reservation.RequestSHA256 || !reflect.DeepEqual(record.MediaSet, reservation.MediaSet) ||
		record.WindowOrdinal != reservation.WindowOrdinal || !reflect.DeepEqual(record.Assessor, reservation.Assessor) ||
		record.MetadataSnapshotSHA256 != reservation.MetadataSnapshotSHA256 ||
		record.PromptSHA256 != reservation.PromptSHA256 || record.SchemaSHA256 != reservation.SchemaSHA256 ||
		record.UpstreamProvider != reservation.UpstreamProvider || record.UpstreamProviderSlug != reservation.UpstreamProviderSlug ||
		record.RequestedNanoUSD != reservation.RequestedNanoUSD || record.AssessedAt.Before(reservation.RequestedAt) {
		return false
	}
	if record.State == fillerstructure.AssessmentRecordHeldBudget {
		return true
	}
	if record.ResolvedProvider != "" && record.ResolvedProvider != reservation.Assessor.Provider ||
		record.ResolvedModel != "" && record.ResolvedModel != reservation.ExpectedResolvedModel {
		return false
	}
	return true
}
