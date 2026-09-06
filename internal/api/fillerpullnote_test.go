package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/loomarr/loomarr/internal/filler"
)

func TestApproveFillerPull_NoteIsAnAnnotationAndTargetsStayExact(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)
	ff.Candidates = []filler.AcquisitionCandidate{{
		Identity: filler.RemoteIdentity{Provider: "archive", SourceID: "classic", RemoteID: "reel-1"},
		URL:      "https://archive.org/details/reel-1", Title: "Classic reel",
	}}
	created := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls", `{}`, adminToken))
	approved := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls/"+created.ID+"/approve",
		`{"note":"reviewed by programming"}`, adminToken))

	if approved.Note != "reviewed by programming" {
		t.Fatalf("note = %q, want annotation preserved", approved.Note)
	}
	if len(ff.pullTargets) != 1 || ff.pullTargets[0].RemoteID != "reel-1" || ff.pullTargets[0].URL != "https://archive.org/details/reel-1" {
		t.Fatalf("ingest targets = %+v, want unchanged exact candidate", ff.pullTargets)
	}
}

func TestApproveFillerPull_NoteIsAnAnnotationForLegacySourceLevelPlan(t *testing.T) {
	srv, st, ff := newFillerServer(t)
	seedSource(t, st, "classic", "https://archive.org/details/classic", true)
	pull := filler.Pull{
		ID: "legacy-pull", Status: filler.PullPending, Plan: []filler.PullPlanRow{{
			SourceID: "classic", Name: "Classic collection",
		}},
	}
	if err := st.UpsertPull(context.Background(), pull); err != nil {
		t.Fatal(err)
	}
	approved := decodePull(t, sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/pulls/legacy-pull/approve",
		`{"note":"legacy review annotation"}`, adminToken))

	if approved.Note != "legacy review annotation" {
		t.Fatalf("note = %q, want annotation preserved", approved.Note)
	}
	if len(ff.pullTargets) != 1 || ff.pullTargets[0].URL != "https://archive.org/details/classic" {
		t.Fatalf("legacy ingest targets = %+v, want registered source URL", ff.pullTargets)
	}
}
