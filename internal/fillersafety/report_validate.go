package fillersafety

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
)

// ValidateEvaluationReport authenticates the complete path-free spoken-safety
// result without requiring access to the execution repository. The terminal
// identity binds the exact reduced evidence and the ordered ledger event set.
func ValidateEvaluationReport(report EvaluationReport) error {
	if report.SchemaVersion != EvaluationReportSchemaVersion || report.ContractVersion != EvaluationReportContractVersion ||
		ValidateLedgerRun(report.Run) != nil || !boundedLedgerID(report.TerminalEventID) ||
		report.TerminalCreatedAt.IsZero() || report.TerminalCreatedAt.Before(report.Run.CreatedAt) ||
		!validSHA256(report.TerminalSHA256) || len(report.TerminalEventIDs) == 0 ||
		!validSHA256(report.SHA256) || report.SHA256 != EvaluationReportSHA256(report) {
		return ErrEvaluationInvalid
	}
	if _, valid := validateEvidence(report.Evidence); !valid ||
		!reflect.DeepEqual(report.Result, Reduce(report.Evidence)) {
		return ErrEvaluationInvalid
	}
	seen := make(map[string]struct{}, len(report.TerminalEventIDs))
	for _, id := range report.TerminalEventIDs {
		if !boundedLedgerID(id) || id == report.TerminalEventID {
			return ErrEvaluationInvalid
		}
		if _, duplicate := seen[id]; duplicate {
			return ErrEvaluationInvalid
		}
		seen[id] = struct{}{}
	}
	event := LedgerEvent{
		ID: report.TerminalEventID, RunID: report.Run.ID, Ordinal: len(report.TerminalEventIDs),
		Kind: LedgerTerminal, CreatedAt: report.TerminalCreatedAt,
		Terminal: &TerminalResult{
			Evidence: report.Evidence, Result: report.Result,
			EventIDs: slices.Clone(report.TerminalEventIDs),
		},
	}
	digest, err := LedgerEventSHA256(event)
	if err != nil || digest != report.TerminalSHA256 {
		return ErrEvaluationInvalid
	}
	return nil
}

// EvaluationReportSHA256 addresses the complete public report, including its
// run policy/certification identity and authenticated terminal event.
func EvaluationReportSHA256(report EvaluationReport) string {
	report.SHA256 = ""
	raw, err := json.Marshal(report)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
