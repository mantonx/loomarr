import * as fillerApi from "@loomarr/api/endpoints/filler";
import { toProblem } from "@loomarr/api/mutator";
import { unwrap } from "@loomarr/api/unwrap";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/loomarr/feedback/error-state";
import { IncomingPanel } from "@/components/loomarr/filler/incoming-panel";
import { TunePanel } from "../tune-panel";
import { useFillerInvalidate } from "../use-filler-invalidate";
import type { IncomingTabProps } from "./incoming-tab.type";
import { useClipPipeline } from "./use-clip-pipeline";

// IncomingTab — the review queue (§10 V38): what has been downloaded but is not yet filed.
//
// ⚠ It owns its own query and its own mutations, and that is the point of it existing. All of
// this lived in `filler-page` beside the Catalog's and Sources' state, so a member sitting on
// the Catalog tab still mounted the Incoming queue, its four filing mutations and their busy
// state — paying for a surface they cannot even reach (the queue is admin-only server-side).
// Routing renders one tab at a time; the hooks now follow the routing.
//
// ⚠ ADMIN-ONLY, enforced server-side. The shell does not render this tab for a member, and
// nothing here re-checks that: a second, weaker copy of an authorization rule is worse than one
// rule in one place. The query gate below is about a member's console staying free of 403s, not
// about access.
const IncomingTab = ({
  onEditTags,
  excludedHashes = new Set(),
  semanticReviewCount = 0,
}: IncomingTabProps) => {
  const { invalidateLifecycle } = useFillerInvalidate();

  // ⚠ Mounted BEFORE the query is read, and it must stay a plain subscription rather than
  // something conditional on the data: the SSE frames are what keep the pipeline rows moving
  // between refetches, and a listener attached only once rows exist would miss the transition
  // that produced the first one.
  useClipPipeline();

  const incomingQuery = fillerApi.useFillerIncoming();
  const allClips = unwrap(incomingQuery.data, (b) => b.clips) ?? [];
  // A semantic exception and its intake row are two views of the same exact clip. The richer
  // decision card owns it while the exception is open; leaving the intake row below would ask
  // about the same object twice with two different sets of controls.
  const excludedClips = allClips.filter((clip) => excludedHashes.has(clip.hash));
  const clips = allClips.filter((clip) => !excludedHashes.has(clip.hash));
  const reels = unwrap(incomingQuery.data, (b) => b.reels) ?? [];
  const recentlyFiled = unwrap(incomingQuery.data, (b) => b.recentlyFiled) ?? [];
  const rejected = unwrap(incomingQuery.data, (b) => b.rejected) ?? [];
  const stageOrder = unwrap(incomingQuery.data, (b) => b.stageOrder) ?? [];
  const overview = unwrap(incomingQuery.data, (b) => b.overview);
  const clipsTotal = Math.max(
    clips.length,
    (unwrap(incomingQuery.data, (b) => b.clipsTotal) ?? allClips.length) - excludedClips.length,
  );
  const decisionsTotal = Math.max(
    0,
    (unwrap(incomingQuery.data, (b) => b.decisionsTotal) ?? 0) -
      excludedClips.filter((clip) => clip.needsDecision).length,
  );
  const visibleOverview = overview
    ? {
        ...overview,
        needsDecision:
          Math.max(0, overview.needsDecision - excludedClips.filter((clip) => clip.needsDecision).length) +
          semanticReviewCount,
      }
    : undefined;
  const reelsTotal = unwrap(incomingQuery.data, (b) => b.reelsTotal) ?? reels.length;
  const rejectedTotal = unwrap(incomingQuery.data, (b) => b.rejectedTotal) ?? rejected.length;
  const recentlyFiledTotal = unwrap(incomingQuery.data, (b) => b.recentlyFiledTotal) ?? recentlyFiled.length;

  // Which clip a write is in flight for, so ONE row disables rather than the whole list. The
  // mutation's own isPending is global to the hook — using it alone greys out every button on
  // the page while a single confirm lands, which reads as the page having frozen.
  const [busyClip, setBusyClip] = useState<string>();
  const settle = () => setBusyClip(undefined);

  // Filing — plain, and as-suggested. ⚠ It carries the error toast that used to sit on the
  // era-confirm mutation: "Looks right" now files through this hook (§10 V54), and without one a
  // failed decision would be silent, leaving the operator to conclude the row simply did not move.
  const fileClips = fillerApi.useFileFillerClips({
    mutation: {
      onSettled: settle,
      onSuccess: invalidateLifecycle,
      onError: (e) => toast.error(toProblem(e).title ?? "Couldn't file those clips"),
    },
  });
  const holdClips = fillerApi.useHoldFillerClips({
    mutation: {
      onSettled: settle,
      onSuccess: () => {
        toast.success("Sent back", { description: "It's out of rotation and back in the queue." });
        invalidateLifecycle();
      },
    },
  });
  const removeClips = fillerApi.useBulkRemoveFiller({
    mutation: {
      onSettled: settle,
      onSuccess: invalidateLifecycle,
      onError: (e) => toast.error(toProblem(e).title ?? "Couldn't remove those clips"),
    },
  });
  const rewind = fillerApi.useRewindFillerClip({
    mutation: {
      onSettled: settle,
      onSuccess: () => {
        toast.success("Clip queued again", { description: "Completed upstream work was preserved." });
        invalidateLifecycle();
      },
      onError: (error) => toast.error(toProblem(error).title ?? "Couldn't retry that clip"),
    },
  });
  const retryFailures = fillerApi.useRetryFillerFailures({
    mutation: {
      onSettled: settle,
      onSuccess: () => {
        toast.success("Clip queued again", { description: "Completed upstream work was preserved." });
        invalidateLifecycle();
      },
      onError: (error) => toast.error(toProblem(error).title ?? "Couldn't retry that clip"),
    },
  });
  // ⚠ The era-confirm PATCH mutation that used to live here is GONE, not merely unused (§10 V54).
  // "Looks right" files through `fileClips` with `asSuggested`, so the single-clip tag route has no
  // caller on this tab — and a mutation left wired to nothing is how a later reader concludes there
  // are two ways to confirm an era and picks the one that no longer files. The route still exists
  // and is still the Catalog's tag dialog's writer; what is deleted is this tab's second path to it.
  const busy =
    removeClips.isPending ||
    fileClips.isPending ||
    holdClips.isPending ||
    rewind.isPending ||
    retryFailures.isPending;

  return (
    <div className="flex flex-col gap-4">
      {incomingQuery.error != null && (
        <ErrorState error={incomingQuery.error} onRetry={() => incomingQuery.refetch()} />
      )}
      {visibleOverview && (
        <section aria-label="Filler pipeline status" className="grid gap-2 sm:grid-cols-3">
          <div className="rounded-lg border border-border p-3">
            <p className="font-medium text-sm">Loomarr is working</p>
            <p className="text-muted-foreground text-xs">
              {visibleOverview.runnable + visibleOverview.inProgress} ready or active ·{" "}
              {visibleOverview.scheduled} waiting for retry
            </p>
          </div>
          <div className="rounded-lg border border-border p-3">
            <p className="font-medium text-sm">Needs you</p>
            <p className="text-muted-foreground text-xs">
              {visibleOverview.needsDecision} clip decisions · {reelsTotal} compilations
            </p>
          </div>
          <div className="rounded-lg border border-border p-3">
            <p className="font-medium text-sm">Stopped or settled</p>
            <p className="text-muted-foreground text-xs">
              {visibleOverview.rejected} rejected · {visibleOverview.admitted} admitted ·{" "}
              {visibleOverview.dismissed} dismissed
            </p>
          </div>
        </section>
      )}
      {/* ⚠ ABOVE the queue, matching the mock: the policy is the context the rows below are
          read in. Its counts come from this tab's query rather than a second one — the panel
          reports on the queue it sits over. */}
      <TunePanel filed={recentlyFiledTotal} needsYou={decisionsTotal} />
      <IncomingPanel
        clips={clips}
        clipsTotal={clipsTotal}
        decisionsTotal={decisionsTotal}
        reels={reels}
        reelsTotal={reelsTotal}
        suppressEmptyState={semanticReviewCount > 0}
        recentlyFiled={recentlyFiled}
        recentlyFiledTotal={recentlyFiledTotal}
        rejected={rejected}
        rejectedTotal={rejectedTotal}
        stageOrder={stageOrder}
        // ⚠ Restore rides the EXISTING bulk route with `restore: true`, not a second endpoint.
        // The V51 plan sketched `POST /v1/filler/clips/{hash}/restore`; V51b deliberately did not
        // build it, because two ways to un-refuse a clip are two places for the rule about which
        // refusals may be overturned to disagree. `restorable` on the row and `Soft()` on the
        // server are already one source of truth — a second route would make three.
        onRestore={(clip) => {
          setBusyClip(clip.hash);
          removeClips.mutate({ data: { hashes: [clip.hash], restore: true } });
        }}
        onRetryFailure={(clip) => {
          setBusyClip(clip.hash);
          retryFailures.mutate({ data: { hashes: [clip.hash] } });
        }}
        // "Looks right" CONFIRMS the guess and FILES, in one request (§10 V54).
        //
        // ⚠ It used to PATCH the era and stop there, which meant it did not file — and for a
        // clip with a guessed era it is the only affirmative control on the row, because the
        // panel offers "Use it" only when there is no guess. So the one button that was
        // supposed to clear a guessed clip out of the queue left it exactly where it was.
        //
        // ⚠ Routed through the EXISTING `asSuggested` flag rather than chaining a PATCH and a
        // file from here. The server confirms each clip's own `suggestedEra` — the store clears
        // `suggested_era` in the same statement, so the question cannot outlive its answer — and
        // then files, in one round trip. Two client-side mutations would put the halves in
        // different requests, where a failure between them leaves a clip filed with an
        // unconfirmed guess. It is also exactly what "File all as suggested" below sends, for a
        // selection of one, so the single and bulk paths cannot drift.
        onConfirmEra={(ask) => {
          setBusyClip(ask.path);
          fileClips.mutate({ data: { paths: [ask.path], asSuggested: true } });
        }}
        // ⚠ The clip's IDENTITY, not its path. The shell's dialog resolves a clip by hash — it
        // is shared with the Catalog tab, whose rows are keyed that way — and handing it a path
        // meant the lookup matched nothing and no dialog ever opened.
        onEditTags={(ask) => onEditTags(ask.hash)}
        onReclassify={(ask) => {
          setBusyClip(ask.path);
          rewind.mutate({ data: { hash: ask.hash, from: "tag" as never } });
        }}
        onRetryStage={(clip) => {
          setBusyClip(clip.path);
          retryFailures.mutate({ data: { hashes: [clip.hash] } });
        }}
        // "Don't use it" removes the clip from the CATALOG. The file stays where the operator
        // put it — the server's action is a tombstone, never a delete.
        onDismiss={(ask) => {
          setBusyClip(ask.path);
          // ⚠ bulk-remove is HASH-keyed (§10 V45a) — unlike file/hold below, which stay `paths` (the
          // V38 SetClipsHeld/File store methods are path-keyed by design). IncomingClipDTO carries both.
          removeClips.mutate({ data: { hashes: [ask.hash] } });
        }}
        // "Use it" files a clip as it stands — its tags are right enough. No era to confirm, so
        // this is the plain file, not the confirm-then-file above.
        onFile={(ask) => {
          setBusyClip(ask.path);
          fileClips.mutate({ data: { paths: [ask.path] } });
        }}
        // ⚠ `asSuggested` is what makes this per-CLIP: the server confirms each clip's own
        // proposed era. Sending one era for the whole selection is what the bulk tag bar does,
        // and it is the wrong answer for a queue of different guesses.
        // ⚠ This depends on the server never marking a COMPILATION as needing a decision (§10 V54).
        // It used to, and this button would then have tried to file the reels themselves — a
        // 20-minute recording filed as a commercial. Do not "helpfully" widen the filter here; the
        // invariant belongs server-side, where `IncomingClipDTO` carries no composite marker for a
        // client to re-derive it from.
        onFileAllAsSuggested={() =>
          fileClips.mutate({
            data: { paths: clips.filter((c) => c.needsDecision).map((a) => a.path), asSuggested: true },
          })
        }
        // The undo for auto-filing. ⚠ NOT a removal: the clip and its file both stay, it simply
        // stops being matched into pods until someone decides.
        onSendBack={(clip) => {
          setBusyClip(clip.path);
          holdClips.mutate({ data: { paths: [clip.path] } });
        }}
        {...(busy ? { busyPath: busyClip } : {})}
      />
      {/* ⚠ `IngestPanel` — the paste-a-URL box — is retired-ok here (V38b), not merely moved.
          Clips arrive because you added a SOURCE: Sources registers one, its per-row search
          queues a result, an approved pull fetches in bulk, and auto-fetch polls on a
          schedule. Every one of those paths is better than re-supplying a URL by hand, and
          a box that made you find the URL yourself was the odd one out.

          The ingest SSE frame it consumed is deliberately NOT deleted — auto-fetch and
          queued downloads still emit progress; nothing renders it yet. `events-provider`
          records why that fan-out is guarded structurally. */}
    </div>
  );
};

export { IncomingTab };
