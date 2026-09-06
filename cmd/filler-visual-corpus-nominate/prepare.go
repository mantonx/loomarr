package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/loomarr/loomarr/internal/fillervisualsafety"
)

func runPrepare(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("filler-visual-corpus-nominate prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "absolute path to frozen inventory JSON")
	materializationPath := flags.String("materialization-ledger", "", "absolute path to materialization ledger JSON")
	mediaRoot := flags.String("media-root", "", "absolute path to private materialized media")
	preparedAtText := flags.String("prepared-at", "", "worksheet preparation time in RFC3339 format")
	outputDir := flags.String("output", "", "new private worksheet directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	preparedAt, err := time.Parse(time.RFC3339, *preparedAtText)
	if err != nil {
		return errors.New("invalid --prepared-at")
	}
	inventoryJSON, err := readPrivateInput(*inventoryPath)
	if err != nil {
		return fmt.Errorf("read inventory: %w", err)
	}
	materializationJSON, err := readPrivateInput(*materializationPath)
	if err != nil {
		return fmt.Errorf("read materialization ledger: %w", err)
	}
	worksheet, err := fillervisualsafety.PrepareVisualCorpusNominationWorksheet(ctx, fillervisualsafety.VisualCorpusNominationPrepareConfig{
		InventoryJSON: inventoryJSON, MaterializationJSON: materializationJSON, MediaRoot: *mediaRoot, PreparedAt: preparedAt,
	})
	if err != nil {
		return err
	}
	worksheetRaw, err := json.MarshalIndent(worksheet, "", "  ")
	if err != nil {
		return err
	}
	worksheetRaw = append(worksheetRaw, '\n')
	var csvBuffer bytes.Buffer
	writer := csv.NewWriter(&csvBuffer)
	if err := writer.Write(fillervisualsafety.VisualCorpusNominationCSVHeader()); err != nil {
		return err
	}
	for _, row := range worksheet.Cases {
		record := fillervisualsafety.ImmutableVisualCorpusNominationCSVRecord(worksheet, row)
		record = append(record, "", "", "", "")
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	boardRaw, err := fillervisualsafety.RenderVisualCorpusNominationReviewBoard(worksheet, *mediaRoot)
	if err != nil {
		return err
	}
	if err := publishWorksheetDirectory(*outputDir, worksheetRaw, csvBuffer.Bytes(), boardRaw); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "filler-visual-corpus-nominate: prepared %d inert rows; worksheet %s\n", len(worksheet.Cases), worksheet.SHA256)
	return nil
}
