package filler_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/loomarr/loomarr/internal/filler"
)

func TestConfirmBindsExactStructureDecisionToPublishedChildren(t *testing.T) {
	store := newSplitMemStore()
	oldHash := seedCompilation(store, "comps/decided.mp4", 60_000)
	drop := t.TempDir()
	full := filepath.Join(drop, "comps", "decided.mp4")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("complete compilation"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := bindCompilationIdentity(t, store, oldHash, full)
	stageParentForSplitReview(store, hash)
	splitter := newSplitter(store, &fakeTools{chapters: []filler.Chapter{
		{StartMs: 0, EndMs: 30_000}, {StartMs: 30_000, EndMs: 60_000},
	}}, nil, drop)
	proposal, err := splitter.Propose(t.Context(), hash)
	if err != nil {
		t.Fatal(err)
	}
	artifact := structureDecisionArtifact(t, proposal.Source, 30_000, false)
	assessment, err := filler.ProjectConfirmedStructureDecision(proposal.Source, proposal.Structure, artifact)
	if err != nil {
		t.Fatal(err)
	}
	proposal.Structure, proposal.StructureDecision = &assessment, &artifact
	if err := store.UpdateSplitProposal(t.Context(), *proposal); err != nil {
		t.Fatal(err)
	}
	spawned, err := splitter.Confirm(t.Context(), proposal.ID, proposal.Segments)
	if err != nil {
		t.Fatal(err)
	}
	if len(spawned) != 2 {
		t.Fatalf("spawned=%v", spawned)
	}
	wantKinds := []filler.Kind{filler.Commercial, filler.Interstitial}
	wantRoles := []filler.StructureSegmentRole{filler.SegmentRoleCommercial, filler.SegmentRolePromo}
	for index, childHash := range spawned {
		child, found, err := store.GetClip(t.Context(), childHash)
		if err != nil || !found {
			t.Fatalf("child %s found=%v error=%v", childHash, found, err)
		}
		if child.Kind != wantKinds[index] {
			t.Fatalf("child %s kind=%q want=%q", childHash, child.Kind, wantKinds[index])
		}
		tags, ok := filler.ReadSidecarTags(filepath.Join(drop, filepath.FromSlash(child.Path)))
		lineage := tags.ConditioningLineage
		if !ok || lineage == nil || lineage.StructureDecisionSHA256 != artifact.SHA256 ||
			lineage.StructureRole != wantRoles[index] || tags.Kind != string(wantKinds[index]) {
			t.Fatalf("child %s lineage=%+v ok=%v", childHash, tags.ConditioningLineage, ok)
		}
	}
}
