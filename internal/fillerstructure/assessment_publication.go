package fillerstructure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

const (
	AssessmentPublicationSchemaVersion   = 1
	AssessmentPublicationContractVersion = "filler-structure-assessment-publication-v1"
)

// AssessmentPublication is the immutable completed-operation pointer for one complete-video
// assessment. It becomes visible only after all evidence named by the record is durable.
type AssessmentPublication struct {
	SchemaVersion   int             `json:"schemaVersion"`
	ContractVersion string          `json:"contractVersion"`
	OperationSHA256 string          `json:"operationSha256"`
	RecordSHA256    string          `json:"recordSha256"`
	Source          Source          `json:"source"`
	Media           AssessmentMedia `json:"media"`
	Assessor        AssessorProfile `json:"assessor"`
	PromptSHA256    string          `json:"promptSha256"`
	SchemaSHA256    string          `json:"schemaSha256"`
	SHA256          string          `json:"sha256"`
}

func NewAssessmentPublication(record AssessmentRecord) (AssessmentPublication, error) {
	if err := ValidateAssessmentRecord(record); err != nil {
		return AssessmentPublication{}, err
	}
	publication := AssessmentPublication{
		SchemaVersion: AssessmentPublicationSchemaVersion, ContractVersion: AssessmentPublicationContractVersion,
		OperationSHA256: AssessmentOperationSHA256(record.Source, record.Media, record.Assessor),
		RecordSHA256:    record.SHA256, Source: record.Source, Media: record.Media, Assessor: record.Assessor,
		PromptSHA256: record.PromptSHA256, SchemaSHA256: record.SchemaSHA256,
	}
	publication.SHA256 = AssessmentPublicationSHA256(publication)
	return publication, ValidateAssessmentPublication(publication, record)
}

func ValidateAssessmentPublication(publication AssessmentPublication, record AssessmentRecord) error {
	if publication.SchemaVersion != AssessmentPublicationSchemaVersion ||
		publication.ContractVersion != AssessmentPublicationContractVersion ||
		!digest(publication.OperationSHA256) || !digest(publication.RecordSHA256) ||
		!digest(publication.PromptSHA256) || !digest(publication.SchemaSHA256) ||
		!digest(publication.SHA256) || publication.SHA256 != AssessmentPublicationSHA256(publication) ||
		ValidateAssessmentRecord(record) != nil || publication.RecordSHA256 != record.SHA256 ||
		publication.Source != record.Source || publication.Media != record.Media || publication.Assessor != record.Assessor ||
		publication.PromptSHA256 != record.PromptSHA256 || publication.SchemaSHA256 != record.SchemaSHA256 ||
		publication.PromptSHA256 != DirectVideoPromptSHA256(record.Source.DurationMS) ||
		publication.SchemaSHA256 != DirectVideoSchemaSHA256(record.Source.DurationMS) ||
		publication.OperationSHA256 != AssessmentOperationSHA256(record.Source, record.Media, record.Assessor) {
		return errors.New("filler structure assessment publication is invalid")
	}
	return nil
}

func AssessmentOperationSHA256(source Source, media AssessmentMedia, assessor AssessorProfile) string {
	operation := struct {
		ContractVersion string          `json:"contractVersion"`
		Source          Source          `json:"source"`
		Media           AssessmentMedia `json:"media"`
		Assessor        AssessorProfile `json:"assessor"`
		PromptSHA256    string          `json:"promptSha256"`
		SchemaSHA256    string          `json:"schemaSha256"`
	}{
		ContractVersion: AssessmentPublicationContractVersion, Source: source, Media: media, Assessor: assessor,
		PromptSHA256: DirectVideoPromptSHA256(source.DurationMS), SchemaSHA256: DirectVideoSchemaSHA256(source.DurationMS),
	}
	raw, err := json.Marshal(operation)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func AssessmentPublicationSHA256(publication AssessmentPublication) string {
	publication.SHA256 = ""
	raw, err := json.Marshal(publication)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
