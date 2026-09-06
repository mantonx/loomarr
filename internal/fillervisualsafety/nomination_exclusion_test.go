package fillervisualsafety

import (
	"context"
	"testing"
)

func TestVisualCorpusNominationWorkflowPublishesOnlyExplicitCandidates(t *testing.T) {
	t.Parallel()
	fixture := newVisualNominationFixture(t)
	addDuplicateFamilyNominationCase(t, &fixture)
	worksheet, err := PrepareVisualCorpusNominationWorksheet(context.Background(), fixture.prepare)
	if err != nil {
		t.Fatal(err)
	}
	records := completedNominationRecords(worksheet, VisualCorpusNominationPositive, VisualCorpusSubjectHistoricalAdult)
	setExcludedNomination(records[1])
	result, err := LockVisualCorpusNominations(context.Background(), VisualCorpusNominationLockConfig{
		Prepare: fixture.prepare, Worksheet: worksheet, CompletedCSV: records,
		ReviewedBy: "visual-reviewer", ReviewedAt: fixture.reviewedAt, OutputDir: fixture.output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReviewedCount != 2 || result.CandidateCount != 1 || result.ExcludedCount != 1 {
		t.Fatalf("result = %+v", result)
	}
	set, err := OpenVisualCorpusNominationSet(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	if set.ReviewedCaseCount != 2 || set.ExcludedCaseCount != 1 || len(set.Candidates) != 1 ||
		set.Candidates[0].CandidateID != worksheet.Cases[1].CaseID || set.ReviewDecisionsSHA256 != digestJSON(records) {
		t.Fatalf("set = %+v", set)
	}
}

func TestVisualCorpusNominationWorkflowRejectsIncompleteExclusion(t *testing.T) {
	t.Parallel()
	fixture := newVisualNominationFixture(t)
	worksheet, err := PrepareVisualCorpusNominationWorksheet(context.Background(), fixture.prepare)
	if err != nil {
		t.Fatal(err)
	}
	records := completedNominationRecords(worksheet, VisualCorpusNominationPositive, VisualCorpusSubjectHistoricalAdult)
	records[1][len(VisualCorpusNominationCSVHeader())-4] = VisualCorpusNominationExclude
	_, err = LockVisualCorpusNominations(context.Background(), VisualCorpusNominationLockConfig{
		Prepare: fixture.prepare, Worksheet: worksheet, CompletedCSV: records,
		ReviewedBy: "visual-reviewer", ReviewedAt: fixture.reviewedAt, OutputDir: fixture.output,
	})
	if err == nil {
		t.Fatal("LockVisualCorpusNominations accepted an exclusion with subject assertions")
	}
}

func setExcludedNomination(record []string) {
	firstJudgment := len(VisualCorpusNominationCSVHeader()) - 4
	record[firstJudgment] = VisualCorpusNominationExclude
	record[firstJudgment+1] = ""
	record[firstJudgment+2] = ""
	record[firstJudgment+3] = "[]"
}
