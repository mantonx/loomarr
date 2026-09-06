package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filler"
)

// The operator's decision has to reach the PIPELINE ROW, not only `clips` (§10 V54).
//
// ⚠ These assert through `GET /v1/filler/incoming` rather than by reading the row back, and that
// is deliberate. The defect was never "a column holds the wrong string" — it was "the clip I just
// decided on is still sitting in my queue", and the queue is the only thing that can say so. A
// test that read the disposition directly would have gone green on a fix that left the belt's
// fallback loop re-resolving the clip anyway.

// seedForDecision puts a held clip with a pipeline row waiting on a person.
func seedForDecision(t *testing.T, st interface {
	UpsertClipPipeline(context.Context, filler.ClipPipeline) error
}, put func(), hash string, d filler.Disposition) {
	t.Helper()
	put()
	if err := st.UpsertClipPipeline(context.Background(), filler.ClipPipeline{
		ClipHash: hash, Stage: filler.StageScore, Status: filler.StatusDone,
		Disposition: d, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

// ⚠ **The largest defect V54 found.** `POST /v1/filler/file` called `SetClipsHeld(held=false)` and
// nothing else, so the row still read `review`, `ListClipPipelines(ConveyorOnly)` still returned it
// and `conveyorDTO` still marked `needsDecision`. A filed clip came back on the next refetch and
// the tab's `total` never decremented.
func TestFileFillerClips_TakesTheClipOffTheQueueForGood(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	const hash, path = "hash-filed", "1985/filed.mp4"
	seedForDecision(t, st, func() {
		putClip(t, st, filler.Clip{
			Hash: hash, Path: path, Name: "Filed", Kind: filler.Commercial,
			DurationMs: 30_000, Era: 1985, Audience: filler.Kids, Category: "toys", Held: true,
		})
	}, hash, filler.DispositionReview)

	if _, body := getIncoming(t, srv.URL+"/v1/filler/incoming", adminToken); body.Total != 1 {
		t.Fatalf("total = %d before filing, want 1 — the fixture is not actually waiting on anyone", body.Total)
	}

	if res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/file",
		`{"paths":["`+path+`"]}`, adminToken); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}

	_, body := getIncoming(t, srv.URL+"/v1/filler/incoming", adminToken)
	for _, c := range body.Clips {
		if c.Hash == hash && c.NeedsDecision {
			t.Error("a filed clip still says it needs a decision — it returns to the queue on refetch")
		}
	}
	if body.Total != 0 {
		t.Errorf("total = %d after filing the only ask, want 0 — the count never reaches zero", body.Total)
	}
}

func TestFileFillerClips_DoesNotBypassRenderedChildScreening(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	const hash, path = "screening-child", "children/screening-child.mp4"
	putClip(t, st, filler.Clip{
		Hash: hash, Path: path, Name: "Compilation child", Kind: filler.Commercial,
		DurationMs: 30_000, Held: true, ParentHash: "compilation-parent",
	})
	if err := st.UpsertClipPipeline(context.Background(), filler.ClipPipeline{
		ClipHash: hash, Stage: filler.StageScreen, Status: filler.StatusDone,
		Disposition: filler.DispositionReview,
		Stages:      []filler.StageRecord{{Stage: filler.StageScreen, Status: filler.StatusDone}},
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/file",
		`{"paths":["`+path+`"]}`, adminToken); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for unresolved child screening", res.StatusCode)
	}
	clip, err := st.GetClip(context.Background(), hash)
	if err != nil || !clip.Held {
		t.Fatalf("screening review became airable: clip=%+v err=%v", clip, err)
	}
}

func TestFileFillerClips_AllowsHumanDecisionAfterChildScreeningCompleted(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	const hash, path = "screened-child", "children/screened-child.mp4"
	putClip(t, st, filler.Clip{
		Hash: hash, Path: path, Name: "Screened compilation child", Kind: filler.Commercial,
		DurationMs: 30_000, Held: true, ParentHash: "compilation-parent",
	})
	if err := st.UpsertClipPipeline(context.Background(), filler.ClipPipeline{
		ClipHash: hash, Stage: filler.StageLanguage, Status: filler.StatusDone,
		Disposition: filler.DispositionReview,
		Stages:      []filler.StageRecord{{Stage: filler.StageScreen, Status: filler.StatusDone}},
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/file",
		`{"paths":["`+path+`"]}`, adminToken); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want later human review to remain fileable", res.StatusCode)
	}
	clip, err := st.GetClip(context.Background(), hash)
	if err != nil || clip.Held {
		t.Fatalf("completed screening did not permit later human decision: clip=%+v err=%v", clip, err)
	}
}

// A clip the machine is still working on is NOT settled by an operator verb: it finishes its
// ladder and settles itself. Filing early must not abandon the transcribe and tag rungs.
func TestFileFillerClips_LeavesARunningRowToFinishItsLadder(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	const hash, path = "hash-running", "1985/running.mp4"
	seedForDecision(t, st, func() {
		putClip(t, st, filler.Clip{
			Hash: hash, Path: path, Name: "Running", Kind: filler.Commercial,
			DurationMs: 30_000, Held: true,
		})
	}, hash, filler.DispositionRunning)

	if res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/file",
		`{"paths":["`+path+`"]}`, adminToken); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}

	row, found, err := st.GetClipPipeline(context.Background(), hash)
	if err != nil || !found {
		t.Fatalf("pipeline row: found=%v err=%v", found, err)
	}
	if row.Disposition != filler.DispositionRunning {
		t.Errorf("disposition = %q, want running — an operator verb settled a clip mid-ladder", row.Disposition)
	}
}

// "Don't use it" wrote `removed_at` and nothing else. `GetClip` carries no `removed_at` predicate,
// so the belt's fallback loop re-resolved the clip and put it straight back on the queue.
func TestBulkRemoveFiller_DismissalTakesTheClipOffTheBelt(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	const hash = "hash-dismissed"
	seedForDecision(t, st, func() {
		putClip(t, st, filler.Clip{
			Hash: hash, Path: "1985/dismissed.mp4", Name: "Dismissed", Kind: filler.Commercial,
			DurationMs: 30_000, Held: true,
		})
	}, hash, filler.DispositionReview)

	if res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/bulk/remove",
		`{"hashes":["`+hash+`"]}`, adminToken); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}

	_, body := getIncoming(t, srv.URL+"/v1/filler/incoming", adminToken)
	for _, c := range body.Clips {
		if c.Hash == hash {
			t.Error("a dismissed clip is still on the conveyor — the fallback loop re-resolved it")
		}
	}
	// ⚠ And NOT on the refusals list either: that is the audit of what Loomarr decided WITHOUT the
	// operator, and a dismissal is what the operator decided themselves.
	for _, r := range body.Rejected {
		if r.Hash == hash {
			t.Error("an operator dismissal appeared under 'Loomarr didn't use these' — that list is the machine's")
		}
	}

	row, found, err := st.GetClipPipeline(context.Background(), hash)
	if err != nil || !found {
		t.Fatalf("pipeline row: found=%v err=%v", found, err)
	}
	if row.Disposition != filler.DispositionDismissed {
		t.Errorf("disposition = %q, want dismissed", row.Disposition)
	}
}

// Restore is ONE endpoint and has to undo BOTH halves — the tombstone and the refusal — for a
// dismissal exactly as it already did for a machine rejection.
func TestBulkRemoveFiller_RestoreUndoesADismissal(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	const hash = "hash-restored"
	seedForDecision(t, st, func() {
		putClip(t, st, filler.Clip{
			Hash: hash, Path: "1985/restored.mp4", Name: "Restored", Kind: filler.Commercial,
			DurationMs: 30_000, Held: true,
		})
	}, hash, filler.DispositionDismissed)

	if res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/bulk/remove",
		`{"hashes":["`+hash+`"],"restore":true}`, adminToken); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}

	row, found, err := st.GetClipPipeline(context.Background(), hash)
	if err != nil || !found {
		t.Fatalf("pipeline row: found=%v err=%v", found, err)
	}
	if row.Disposition != filler.DispositionReview {
		t.Errorf("disposition = %q, want review — a restored clip is waiting on a person again", row.Disposition)
	}
}

// The mirror of filing: sending a filed clip back means it is waiting on a person again.
func TestHoldFillerClips_SendsAFiledRowBackToReview(t *testing.T) {
	srv, st, _ := newFillerServer(t)
	const hash, path = "hash-sentback", "1985/sentback.mp4"
	seedForDecision(t, st, func() {
		putClip(t, st, filler.Clip{
			Hash: hash, Path: path, Name: "Sent back", Kind: filler.Commercial,
			DurationMs: 30_000, Era: 1990, Audience: filler.Kids, Category: "toys", AutoFiled: true,
		})
	}, hash, filler.DispositionFiled)

	if res := sourceReq(t, http.MethodPost, srv.URL+"/v1/filler/hold",
		`{"paths":["`+path+`"]}`, adminToken); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}

	row, found, err := st.GetClipPipeline(context.Background(), hash)
	if err != nil || !found {
		t.Fatalf("pipeline row: found=%v err=%v", found, err)
	}
	if row.Disposition != filler.DispositionReview {
		t.Errorf("disposition = %q, want review", row.Disposition)
	}
	_, body := getIncoming(t, srv.URL+"/v1/filler/incoming", adminToken)
	if body.Total != 1 {
		t.Errorf("total = %d, want 1 — a clip sent back is work the operator owes", body.Total)
	}
}
