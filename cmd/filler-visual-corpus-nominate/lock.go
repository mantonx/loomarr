package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func runLock(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("filler-visual-corpus-nominate lock", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "absolute path to frozen inventory JSON")
	materializationPath := flags.String("materialization-ledger", "", "absolute path to materialization ledger JSON")
	mediaRoot := flags.String("media-root", "", "absolute path to private materialized media")
	worksheetPath := flags.String("worksheet", "", "absolute path to inert worksheet JSON")
	completedCSVPath := flags.String("completed-csv", "", "absolute path to completed worksheet CSV")
	reviewedBy := flags.String("reviewed-by", "", "visual reviewer identity")
	reviewedAtText := flags.String("reviewed-at", "", "visual review time in RFC3339 format")
	outputDir := flags.String("output", "", "new private nomination-set source root")
	if err := flags.Parse(args); err != nil {
		return err
	}
	reviewedAt, err := time.Parse(time.RFC3339, *reviewedAtText)
	if err != nil {
		return errors.New("invalid --reviewed-at")
	}
	inventoryJSON, err := readPrivateInput(*inventoryPath)
	if err != nil {
		return fmt.Errorf("read inventory: %w", err)
	}
	materializationJSON, err := readPrivateInput(*materializationPath)
	if err != nil {
		return fmt.Errorf("read materialization ledger: %w", err)
	}
	worksheetRaw, err := readPrivateInput(*worksheetPath)
	if err != nil {
		return fmt.Errorf("read worksheet: %w", err)
	}
	var worksheet fillervisualsafety.VisualCorpusNominationWorksheet
	if err := decodeStrictJSON(worksheetRaw, &worksheet); err != nil {
		return errors.New("worksheet is malformed")
	}
	completedRaw, err := readPrivateInput(*completedCSVPath)
	if err != nil {
		return fmt.Errorf("read completed CSV: %w", err)
	}
	reader := csv.NewReader(bytes.NewReader(completedRaw))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return errors.New("completed CSV is malformed")
	}
	result, err := fillervisualsafety.LockVisualCorpusNominations(ctx, fillervisualsafety.VisualCorpusNominationLockConfig{
		Prepare: fillervisualsafety.VisualCorpusNominationPrepareConfig{
			InventoryJSON: inventoryJSON, MaterializationJSON: materializationJSON,
			MediaRoot: *mediaRoot, PreparedAt: worksheet.PreparedAt,
		},
		Worksheet: worksheet, CompletedCSV: records, ReviewedBy: *reviewedBy, ReviewedAt: reviewedAt, OutputDir: *outputDir,
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "filler-visual-corpus-nominate: reviewed %d rows; locked %d nominations; excluded %d; set %s\n",
		result.ReviewedCount, result.CandidateCount, result.ExcludedCount, result.SetSHA256)
	return nil
}
