package fillerstructurewindow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const (
	CallPublicationSchemaVersion   = 1
	CallPublicationContractVersion = "filler-structure-window-call-publication-v1"
)

// CallPublication is the immutable completed-operation index. Its path is the operation digest;
// its body binds that exact operation to one fully persisted and settled call record.
type CallPublication struct {
	SchemaVersion   int                             `json:"schemaVersion"`
	ContractVersion string                          `json:"contractVersion"`
	MediaSetSHA256  string                          `json:"mediaSetSha256"`
	WindowOrdinal   int                             `json:"windowOrdinal"`
	Assessor        fillerstructure.AssessorProfile `json:"assessor"`
	OperationSHA256 string                          `json:"operationSha256"`
	RecordSHA256    string                          `json:"recordSha256"`
	SHA256          string                          `json:"sha256"`
}

func NewCallPublication(record CallRecord) (CallPublication, error) {
	publication := CallPublication{
		SchemaVersion: CallPublicationSchemaVersion, ContractVersion: CallPublicationContractVersion,
		MediaSetSHA256: record.MediaSet.SHA256, WindowOrdinal: record.WindowOrdinal, Assessor: record.Assessor,
		OperationSHA256: CallOperationSHA256(record.MediaSet, record.WindowOrdinal, record.Assessor),
		RecordSHA256:    record.SHA256,
	}
	publication.SHA256 = CallPublicationSHA256(publication)
	return publication, ValidateCallPublication(publication, record)
}

func CallOperationSHA256(set MediaSet, ordinal int, assessor fillerstructure.AssessorProfile) string {
	raw, err := json.Marshal(struct {
		ContractVersion string                          `json:"contractVersion"`
		MediaSetSHA256  string                          `json:"mediaSetSha256"`
		WindowOrdinal   int                             `json:"windowOrdinal"`
		Assessor        fillerstructure.AssessorProfile `json:"assessor"`
	}{CallPublicationContractVersion, set.SHA256, ordinal, assessor})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func CallPublicationSHA256(publication CallPublication) string {
	publication.SHA256 = ""
	raw, err := json.Marshal(publication)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
