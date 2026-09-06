package filler

import "testing"

func TestStructureDecisionLineageNamesOnlyExactKeepIntervals(t *testing.T) {
	proposal := certifiedStructureProposal(t)
	exact := proposal.Segments[0]
	if got := structureDecisionSHA256ForInterval(proposal, exact); got != proposal.StructureDecision.SHA256 {
		t.Fatalf("exact keep lineage = %q, want %q", got, proposal.StructureDecision.SHA256)
	}
	edited := exact
	edited.EndMs--
	if got := structureDecisionSHA256ForInterval(proposal, edited); got != "" {
		t.Fatalf("edited interval claimed decision %q", got)
	}
	proposal.StructureDecision.SHA256 = "drifted"
	if got := structureDecisionSHA256ForInterval(proposal, exact); got != "" {
		t.Fatalf("invalid artifact claimed decision %q", got)
	}
}
