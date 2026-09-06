package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/loomarr/loomarr/internal/filler"
	"github.com/loomarr/loomarr/internal/store"
)

// Filing decisions — the Incoming tab's verbs (§10 V38).
//
// A held clip is recorded but not in the playable catalog. These two routes are the human half of
// the lifecycle: file (admit it) and hold (send it back). The machine half is the tag job's
// auto-file, which uses the same store writer.
//
// ⚠ **`file-as-suggested` is a THIRD verb, not a flag on the first**, because it answers a
// different question. Filing takes a clip as it stands; filing-as-suggested first CONFIRMS the
// tagger's ungrounded era guess — committing each clip's OWN `suggestedEra` rather than one value
// the operator picked. `bulk/tag` cannot express that: it applies a single era to the whole
// selection, which is the opposite of "each of these, as proposed".

type fileFillerInput struct {
	Body struct {
		Paths []string `json:"paths" minItems:"1" doc:"Clip identities (paths relative to FILLER_DIR)"`
		// AsSuggested confirms each clip's OWN ungrounded era guess as it files (§10 V34/V38).
		//
		// ⚠ Per clip, never one value across the selection. The mock's "File all as suggested" is
		// exactly this: N clips, N different proposed eras, one action.
		AsSuggested bool `json:"asSuggested,omitempty" doc:"Confirm each clip's own suggested era as it files"`
	}
}

type holdFillerInput struct {
	Body struct {
		Paths []string `json:"paths" minItems:"1" doc:"Clip identities (paths relative to FILLER_DIR)"`
	}
}

func (s *Server) registerFillerFile(api huma.API) {
	huma.Register(api, withRole(huma.Operation{
		OperationID: "file-filler-clips", Method: http.MethodPost, Path: "/v1/filler/file",
		Summary: "File held clips into the catalog",
		Description: "Admin only (§10 V38). Admits held clips to the playable catalog — they become matchable " +
			"into pods. With `asSuggested`, each clip's OWN ungrounded era guess is confirmed as it files " +
			"(the mock's \"File all as suggested\"), which `bulk/tag` cannot express because it applies one " +
			"era to the whole selection. Filing by hand CLEARS the auto-filed marker: a human looked. A " +
			"materialized compilation child cannot be filed until its rendered-child screening rung completed.",
		Tags: []string{"filler"},
	}, RoleAdmin), s.fileFillerClips)

	huma.Register(api, withRole(huma.Operation{
		OperationID: "hold-filler-clips", Method: http.MethodPost, Path: "/v1/filler/hold",
		Summary: "Send clips back to the review queue",
		Description: "Admin only (§10 V38). The undo for auto-filing: a clip Loomarr filed without asking goes " +
			"back to Incoming and stops being matched into pods. ⚠ It does NOT remove the clip or touch the " +
			"file — that is \"Remove from catalog\", a different action with a different promise.",
		Tags: []string{"filler"},
	}, RoleAdmin), s.holdFillerClips)
}

func (s *Server) fileFillerClips(ctx context.Context, in *fileFillerInput) (*bulkResultOutput, error) {
	if s.store == nil {
		return nil, huma.Error501NotImplemented("no store configured")
	}
	now := time.Now().UTC()

	// ⚠ **This route is PATH-keyed and the two stores below are not**, so identity is resolved once
	// here rather than per call site. `GetClipByPath` is the lookup — `GetClip` matches on the HASH
	// (V38c split identity from location), and calling it with a path returns an ordinary
	// not-found. That is precisely what this loop used to do: the miss hit the `continue`, so
	// `asSuggested` filed the clips and confirmed NOTHING. Every test in the package was blind to
	// it because `putClip` defaults `Hash = Path`.
	//
	// A stale selection races a re-scan; skipping one row beats failing the batch, the same call
	// bulk/tag makes.
	hashes := make([]string, 0, len(in.Body.Paths))
	clips := make([]store.Clip, 0, len(in.Body.Paths))
	for _, path := range in.Body.Paths {
		c, err := s.store.GetClipByPath(ctx, path)
		if err != nil {
			continue
		}
		hashes = append(hashes, c.Hash)
		clips = append(clips, c)
	}
	// Human curation may settle later language, taxonomy, or confidence questions, but it cannot
	// substitute for the rendered-child broadcast-safety boundary. Check the entire resolved batch
	// before confirming suggestions or unholding anything, so one unresolved child cannot create a
	// partially filed request.
	for _, clip := range clips {
		if clip.ParentHash == "" {
			continue
		}
		row, found, err := s.store.GetClipPipeline(ctx, clip.Hash)
		if err != nil {
			return nil, huma.Error500InternalServerError("verify rendered-child screening", err)
		}
		if !found || !filler.SegmentScreeningCompleted(row) {
			return nil, errUnprocessable("Broadcast-safety screening incomplete",
				"This compilation child must complete its rendered-child screening before it can be filed.")
		}
	}

	// Confirming the suggestions happens BEFORE filing, so a failure leaves the clips held with
	// their guesses intact rather than filed with unconfirmed tags. The safe half-state is "still
	// waiting", never "in the catalog claiming something nobody agreed to".
	if in.Body.AsSuggested {
		for _, c := range clips {
			if c.SuggestedEra == 0 {
				continue
			}
			// ⚠ Writing `era` is what CONFIRMS: the store clears `suggested_era` in the same
			// statement (see UpdateClipClassification), so the question cannot outlive its answer.
			// Audience is passed through unchanged; taxonomy tags and their category shadow are owned
			// by the separate taxonomy transaction.
			//
			// ⚠ Keyed by HASH: UpdateClipClassification is `WHERE hash = ?`, unlike SetClipsHeld below.
			if err := s.store.UpdateClipClassification(ctx, c.Hash, c.SuggestedEra, string(c.Audience), 0, c.AITagged, now); err != nil {
				return nil, huma.Error500InternalServerError("confirm suggested era", err)
			}
		}
	}

	// ⚠ `autoFiled: false`. A human filed these, so the marker that says "nobody looked" must be
	// cleared — it is the only thing that can answer "which of these did I never see?", and
	// leaving it set would make a reviewed clip indistinguishable from an unreviewed one.
	updated, err := s.store.SetClipsHeld(ctx, in.Body.Paths, false, false, now)
	if err != nil {
		return nil, huma.Error500InternalServerError("file clips", err)
	}
	// ⚠ **The pipeline row has to move too, or the decision does not stick** (§10 V54). Clearing
	// `held` admits the clip to the catalog; it does not tell the pipeline a human decided. The row
	// stays `review`, `ListClipPipelines(ConveyorOnly)` keeps returning it and `needsDecision` stays
	// true — so the clip the operator just filed reappears on the next refetch and the tab's count
	// never reaches zero.
	s.settlePipeline(ctx, hashes, filler.DispositionFiled, filler.DispositionReview)
	if updated > 0 {
		s.reconcileChannelsForFillerChange(ctx, storeClipsToFiller(clips)...)
	}
	out := &bulkResultOutput{}
	out.Body.Updated = updated
	out.Body.Missing = len(in.Body.Paths) - updated
	return out, nil
}

func (s *Server) holdFillerClips(ctx context.Context, in *holdFillerInput) (*bulkResultOutput, error) {
	if s.store == nil {
		return nil, huma.Error501NotImplemented("no store configured")
	}
	// ⚠ `autoFiled: false` here too, and for a subtler reason than above: a clip sent back is no
	// longer filed at all, so a marker saying "filed without a human" would describe a state it
	// is not in. Re-filing it later re-decides that flag from scratch.
	clips := make([]store.Clip, 0, len(in.Body.Paths))
	for _, path := range in.Body.Paths {
		if c, err := s.store.GetClipByPath(ctx, path); err == nil {
			clips = append(clips, c)
		}
	}
	updated, err := s.store.SetClipsHeld(ctx, in.Body.Paths, true, false, time.Now().UTC())
	if err != nil {
		return nil, huma.Error500InternalServerError("hold clips", err)
	}
	// The mirror of filing: a clip sent back is waiting on a person again, which is what `review`
	// means (§10 V54). Guarded on `filed`, so sending back a clip the pipeline is still working
	// leaves it running rather than declaring it decided.
	hashes := make([]string, 0, len(in.Body.Paths))
	for _, path := range in.Body.Paths {
		c, err := s.store.GetClipByPath(ctx, path)
		if err != nil {
			continue
		}
		hashes = append(hashes, c.Hash)
	}
	s.settlePipeline(ctx, hashes, filler.DispositionReview, filler.DispositionFiled)
	if updated > 0 {
		s.reconcileChannelsForFillerChange(ctx, storeClipsToFiller(clips)...)
	}
	out := &bulkResultOutput{}
	out.Body.Updated = updated
	out.Body.Missing = len(in.Body.Paths) - updated
	return out, nil
}

// reconcileChannelsForFillerChange is the latency path for §10 V56. The clip state is already
// durable when this runs, so a reconcile failure must not turn a successful filing decision into
// an HTTP failure. The ordinary channel sweep remains the crash-safe retry.
func (s *Server) reconcileChannelsForFillerChange(ctx context.Context, snapshots ...filler.Clip) {
	if s.channels == nil || s.store == nil {
		return
	}
	if targeted, ok := s.channels.(interface {
		ReconcileFillerChange(context.Context, []filler.Clip) error
	}); ok {
		if err := targeted.ReconcileFillerChange(ctx, snapshots); err != nil && s.log != nil {
			s.log.Warn("filler catalog changed but affected-channel reconcile failed; sweep will retry", "err", err)
		}
		return
	}
	all, err := s.store.ListChannels(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Warn("filler catalog changed but active channels could not be listed; sweep will retry", "err", err)
		}
		return
	}
	for _, ch := range all {
		if !ch.Status.Reconcilable() {
			continue
		}
		if err := s.channels.Reconcile(ctx, ch.ID); err != nil && s.log != nil {
			s.log.Warn("filler catalog changed but channel reconcile failed; sweep will retry",
				"channel", ch.ID, "err", err)
		}
	}
}

func storeClipsToFiller(clips []store.Clip) []filler.Clip {
	out := make([]filler.Clip, len(clips))
	for i := range clips {
		out[i] = clips[i].Clip
	}
	return out
}
