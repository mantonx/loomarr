package fillercorpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
)

func metRightsBatchCSV(worksheet RightsWorksheet, attestation MetRightsBatchAttestation, attestationSHA256 string) ([]byte, error) {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	if err := writer.Write(RightsReviewCSVHeader()); err != nil {
		return nil, err
	}
	basis := fmt.Sprintf("%s; met_batch_attestation_sha256=%s; purpose=%s; chain_of_title_warranty=%s", attestation.Basis, attestationSHA256, attestation.Purpose, attestation.ChainOfTitleWarranty)
	for _, row := range worksheet.Cases {
		record := append(ImmutableRightsReviewRecord(row), attestation.ReviewerID, attestation.ReviewedAt, "approved", basis, "true", "", "[]")
		if err := writer.Write(record); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func sha256HexString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
