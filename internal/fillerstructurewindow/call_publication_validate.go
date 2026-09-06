package fillerstructurewindow

import (
	"errors"
	"reflect"
)

func ValidateCallPublication(publication CallPublication, record CallRecord) error {
	if err := ValidateCallRecord(record); err != nil {
		return err
	}
	if publication.SchemaVersion != CallPublicationSchemaVersion ||
		publication.ContractVersion != CallPublicationContractVersion ||
		publication.MediaSetSHA256 != record.MediaSet.SHA256 || publication.WindowOrdinal != record.WindowOrdinal ||
		!reflect.DeepEqual(publication.Assessor, record.Assessor) || publication.RecordSHA256 != record.SHA256 ||
		!contentHash(publication.OperationSHA256) ||
		publication.OperationSHA256 != CallOperationSHA256(record.MediaSet, record.WindowOrdinal, record.Assessor) ||
		!contentHash(publication.SHA256) || publication.SHA256 != CallPublicationSHA256(publication) {
		return errors.New("structure window call publication is invalid")
	}
	return nil
}
