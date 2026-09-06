package fillervisualsafety

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"net/url"
	"path/filepath"
	"slices"
)

type visualCorpusNominationBoardCase struct {
	Rank          int      `json:"rank"`
	CaseID        string   `json:"caseId"`
	Creator       []string `json:"creator"`
	SubjectTerms  []string `json:"subjectTerms"`
	ObjectURL     string   `json:"objectUrl"`
	ContentSHA256 string   `json:"contentSha256"`
	AssetURL      string   `json:"assetUrl"`
	Width         int      `json:"width"`
	Height        int      `json:"height"`
	ImmutableCSV  []string `json:"immutableCsv"`
}

type visualCorpusNominationBoardData struct {
	WorksheetSHA256 string
	WorksheetJSON   template.JS
	HeaderJSON      template.JS
	CasesJSON       template.JS
	CleanReviewJSON template.JS
}

// RenderVisualCorpusNominationReviewBoard builds a private, non-authorizing
// reviewer aid. The lock operation remains the only consumer of its CSV output.
func RenderVisualCorpusNominationReviewBoard(worksheet VisualCorpusNominationWorksheet, mediaRoot string) ([]byte, error) {
	if validateVisualCorpusNominationWorksheet(worksheet) != nil || !cleanAbsoluteReviewPath(mediaRoot) ||
		validatePrivateReviewDirectory(mediaRoot) != nil {
		return nil, errors.New("visual corpus nomination review board input is invalid")
	}
	cases := make([]visualCorpusNominationBoardCase, len(worksheet.Cases))
	for index, row := range worksheet.Cases {
		assetPath := filepath.Join(mediaRoot, filepath.FromSlash(row.LocalFile))
		assetURL := (&url.URL{Scheme: "file", Path: assetPath}).String()
		cases[index] = visualCorpusNominationBoardCase{
			Rank: row.Rank, CaseID: row.CaseID, Creator: row.Creator, SubjectTerms: row.SubjectTerms,
			ObjectURL: row.ObjectURL, ContentSHA256: row.Asset.SHA256, AssetURL: assetURL,
			Width: row.Width, Height: row.Height,
			ImmutableCSV: ImmutableVisualCorpusNominationCSVRecord(worksheet, row),
		}
	}
	caseBytes, err := json.Marshal(cases)
	if err != nil {
		return nil, errors.New("encode visual corpus nomination review cases")
	}
	worksheetBytes, err := json.Marshal(worksheet.SHA256)
	if err != nil {
		return nil, errors.New("encode visual corpus nomination review worksheet")
	}
	headerBytes, err := json.Marshal(VisualCorpusNominationCSVHeader())
	if err != nil {
		return nil, errors.New("encode visual corpus nomination review header")
	}
	cleanReviewBytes, err := json.Marshal(visualCorpusCleanReviewMode(worksheet))
	if err != nil {
		return nil, errors.New("encode visual corpus nomination review mode")
	}
	value := visualCorpusNominationBoardData{
		WorksheetSHA256: worksheet.SHA256,
		WorksheetJSON:   template.JS(worksheetBytes),   //nolint:gosec // encoding/json escapes the validated digest
		HeaderJSON:      template.JS(headerBytes),      //nolint:gosec // fixed Loomarr-owned field names
		CasesJSON:       template.JS(caseBytes),        //nolint:gosec // encoding/json escapes source-authored text
		CleanReviewJSON: template.JS(cleanReviewBytes), //nolint:gosec // encoding/json emits a boolean literal
	}
	var output bytes.Buffer
	if err := visualCorpusNominationBoardTemplate.Execute(&output, value); err != nil {
		return nil, errors.New("render visual corpus nomination review board")
	}
	return output.Bytes(), nil
}

func visualCorpusCleanReviewMode(worksheet VisualCorpusNominationWorksheet) bool {
	for _, row := range worksheet.Cases {
		if !slices.Equal(row.RoleHints, []string{VisualCorpusCleanNominationRoleHint}) {
			return false
		}
	}
	return len(worksheet.Cases) > 0
}
