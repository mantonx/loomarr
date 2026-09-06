import * as fillerApi from "@loomarr/api/endpoints/filler";
import type { ClipDTO } from "@loomarr/api/models/clipDTO";
import { toProblem } from "@loomarr/api/mutator";
import { isOk, unwrap } from "@loomarr/api/unwrap";
import { formatClipDuration, pluralize } from "@loomarr/core/format";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { LayoutGrid, List } from "lucide-react";
import { type KeyboardEvent, type ReactNode, useRef, useState } from "react";
import { toast } from "sonner";
import { EmptyState } from "@/components/loomarr/feedback/empty-state";
import { ErrorState } from "@/components/loomarr/feedback/error-state";
import { ClipCard } from "@/components/loomarr/filler/clip-card";
import { ClipPlayer } from "@/components/loomarr/filler/clip-player";
import { ClipRow } from "@/components/loomarr/filler/clip-row";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Disclosure } from "@/components/ui/disclosure";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLoomarrEventListener } from "@/events/events-provider";
import { ConfirmSplitDialog } from "../confirm-split-dialog";
import type { FillerSearch } from "../filler-search";
import { PinClipDialog } from "../pin-clip-dialog";
import { useFillerInvalidate } from "../use-filler-invalidate";
import type { FillerCatalogProps } from "./filler-catalog.type";

// One cycled tag. Deliberately the same shape ClipCard's onCycle emits, so the card and the
// page cannot drift on what a retag carries. ⚠ `category` is GONE (§10 V45a): it is a DERIVED
// shadow of the taxonomy tags, not a directly-cycled field — the card cycles only era/audience now,
// and tags are edited in the dialog (which serves the real vocabulary).
type TagChange = Partial<Pick<ClipDTO, "era" | "audience">>;

// The bulk bar's three dropdowns (V35). ⚠ Each is INDEPENDENT — picking one sends only that
// field, and the server leaves the other two alone. A single "apply" that posted all three
// would blank whatever the operator had not touched, which is the failure the BE's per-field
// optionality exists to prevent.
//
// The vocabularies mirror the clip card's chips. ⚠ They deliberately omit the card's trailing
// "" / 0 entries: those exist there so CYCLING can pass through "unset", which is a different
// affordance from a menu — an "unset" item in a bulk menu is a one-click way to blank a
// hundred clips' tags, and nothing in this bar should be that easy.
// ⚠ Labelled "Set …", not "Era"/"Audience"/"Category". The catalog's FILTER bar already owns
// those names on this page, and two controls sharing an accessible name is a real ambiguity for
// anyone driving by keyboard or screen reader — a test caught it as "found multiple elements",
// which is the same collision seen from the outside. The verb also says what each one does:
// the filter narrows what you see, this changes what the clips ARE.
// ⚠ Bulk "Set category" was REMOVED (§10 V45a). Category is a DERIVED shadow of the taxonomy tags —
// not a directly-settable field — and a single-value bulk menu cannot express a tag SET without either
// wiping a clip's other tags or inventing a questionable "add one tag to N clips" affordance. Tag
// editing is per-clip in the dialog, which serves the real vocabulary. Bulk era/audience stay: those
// ARE single closed-enum values a menu fits. (The old category options were also a rule violation —
// hardcoded, and a DIFFERENT 6-value set than every other place used; see the no-hardcode rule.)

// How many clips one page of the catalog holds (§10 V51d).
//
// ⚠ **ONE size for both the grid and the list**, deliberately. A per-view size would renumber the
// pages under the operator the moment they switched rendering — page 3 of the grid and page 3 of
// the list would be different clips — and `pageSize` is kept out of the URL for the same reason.
// 60 divides evenly by the grid's 2/3/4 columns, so the last row is never a ragged single card.
const CATALOG_PAGE_SIZE = 60;

const BULK_TAG_FIELDS = [
  { key: "era", label: "Set era", options: ["1950", "1960", "1970", "1980", "1990", "2000", "2010", "2020"] },
  { key: "audience", label: "Set audience", options: ["kids", "family", "general", "late_night"] },
] as const;

// The catalog's two renderings (V35b, the mock's `catViews`). Grid is the default because
// filler is picked by LOOKING at it — a wall of frames is the point; the list is for scanning
// a large catalog and bulk-selecting, where a thumbnail per row is noise.
//
// ⚠ Only the GRID acts on one clip (tag/pin/split). That is the mock's split and it is
// deliberate: see the note in `clip-row.tsx` on why per-row actions are not added back.
const VIEWS = [
  { id: "grid", label: "Grid", icon: LayoutGrid, title: "Cards with thumbnails and per-clip actions" },
  { id: "list", label: "List", icon: List, title: "A dense row per clip, for scanning and selecting" },
] as const;

interface CompositeCatalogGroupProps {
  clip: ClipDTO;
  onManage: () => void;
  renderParent: (clip: ClipDTO) => ReactNode;
  renderChild: (clip: ClipDTO) => ReactNode;
}

const CompositeCatalogGroup = ({ clip, onManage, renderParent, renderChild }: CompositeCatalogGroupProps) => {
  const [open, setOpen] = useState(false);
  const children = fillerApi.useListFiller(
    { parentHash: clip.hash, limit: 500 },
    { query: { enabled: open } },
  );
  const rows = unwrap(children.data, (body) => body.clips) ?? [];
  const total = unwrap(children.data, (body) => body.total) ?? 0;

  return (
    <div className="flex flex-col gap-2">
      {/* Keep the ordinary clip surface as the parent. A compilation still needs the same
          preview, tag, era and split controls as every other catalog item; replacing it with a
          bespoke group heading made those established actions disappear. The disclosure below
          adds hierarchy without creating a second, weaker representation of the parent. */}
      {renderParent(clip)}
      <Disclosure open={open} onOpenChange={setOpen}>
        <Card className="overflow-hidden">
          <div className="flex flex-wrap items-center gap-3 p-3">
            <div className="min-w-0 flex-1">
              <p className="font-medium text-sm">Compilation segments</p>
              <p className="font-mono text-muted-foreground text-xs">
                {formatClipDuration(clip.durationMs)} source reel
              </p>
            </div>
            {open && children.isFetching ? (
              <span className="text-muted-foreground text-xs">Loading segments…</span>
            ) : null}
            {open && !children.isFetching ? (
              <Button variant="outline" size="sm" onClick={onManage}>
                Manage {pluralize(total, "segment")}
              </Button>
            ) : null}
            <Disclosure.Trigger label={`${open ? "Hide" : "Show"} segments from ${clip.name}`} />
          </div>
          <Disclosure.Panel className="border-border border-t p-3">
            {children.error ? (
              <p className="text-onair-300 text-sm">Segments could not be loaded.</p>
            ) : rows.length > 0 ? (
              <div className="overflow-hidden rounded-lg border border-border">{rows.map(renderChild)}</div>
            ) : children.isFetching ? null : (
              <p className="text-muted-foreground text-sm">
                No filed segments remain under this compilation.
              </p>
            )}
          </Disclosure.Panel>
        </Card>
      </Disclosure>
    </div>
  );
};

// FillerPage — the §10 clip catalog: browse, search, tag and (on the filler image) download.
// ⚠ No longer "sync": V38c removed the whole-catalog Sync and AI-tag buttons, because both run
// on their own schedule and a button for work that already happens invites the reading that it
// does not happen unless pressed. Filtering is client-driven but server-executed: the store indexes
// these columns, so a query per filter change is cheaper and always correct, versus
// holding thousands of clips in memory to filter locally (§7.2 — no client index).
// The catalog is mounted only for `/filler/library`. Its filters remain URL-driven, deep-linkable,
// and scoped to the validated filler route; the page shell merely preserves that opaque search
// state when it renders the Library navigation link.
const FillerCatalog = ({ isAdmin, onEditTags, onProposePull }: FillerCatalogProps) => {
  const navigate = useNavigate();
  // Filters live in the URL (deep-linkable, shareable, back-button aware) — the route's
  // validateSearch narrows them. setFilters merges a partial change and writes with
  // `replace: true` so typing in the search box doesn't stack a history entry per keystroke.
  // ⚠ `strict: false` is retained at this composition boundary because the route id is generated
  // and the shell preserves the validated search object opaquely. The cast below narrows only to
  // the route's exported search contract; Incoming and Sources never mount this module.
  const {
    q = "",
    kind = "",
    audience = "",
    taxon = "",
    unclassified = false,
    withoutAxis,
    untagged = false,
    view = "grid",
    page = 1,
    parent: parentHash,
  } = useSearch({ strict: false }) as Partial<FillerSearch>;
  const filtered = Boolean(q || kind || audience || taxon || unclassified || withoutAxis || untagged);
  // ⚠ **Every filter change RESETS the page, and this is the single highest-risk line on the
  // page** (§10 V51d). `setFilters` merges blindly; without the reset, typing in the search box
  // while on page 7 lands on an empty page 7 of a two-page result and renders "No clips match"
  // over a catalog that matches plenty. The rule lives HERE, in the one function every filter
  // control calls, rather than at each control — six call sites is six chances to forget it.
  //
  // ⚠ Paging itself is the exception, so it passes `page` explicitly and keeps it.
  const setFilters = (next: Partial<FillerSearch>) =>
    navigate({
      to: "/filler/library",
      search: (prev) => ({ ...prev, page: undefined, ...next }),
      replace: true,
    });
  const viewRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const chooseView = (next: (typeof VIEWS)[number]["id"]) =>
    setFilters({ view: next === "grid" ? undefined : next });
  const onViewKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    let next = index;
    if (event.key === "ArrowRight" || event.key === "ArrowDown") next = (index + 1) % VIEWS.length;
    else if (event.key === "ArrowLeft" || event.key === "ArrowUp")
      next = (index - 1 + VIEWS.length) % VIEWS.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = VIEWS.length - 1;
    else return;

    event.preventDefault();
    viewRefs.current[next]?.focus();
    const nextView = VIEWS[next];
    if (nextView) chooseView(nextView.id);
  };

  const [pinning, setPinning] = useState<string>();
  // The clip whose split is awaiting confirmation, by hash (§10 V54 A8). Nothing fires until the
  // operator confirms — see the dialog for why the old first-click behaviour was a hazard.
  const [splitting, setSplitting] = useState<string>();
  // The clip open in the player (V39), by path. A path rather than the DTO so the dialog always
  // renders the CURRENT row — retagging a clip while it plays would otherwise leave the player
  // showing a stale copy.
  const [playing, setPlaying] = useState<string>();

  // One page of the catalog (§10 V51d). ⚠ This was an unbounded read of every clip in the
  // install; the endpoint now caps at 500 and defaults to 100, so an un-paged catalog would
  // silently show the first hundred clips and call it the catalog.
  const clips = fillerApi.useListFiller(
    {
      ...(q ? { q } : {}),
      ...(kind ? { kind: kind as never } : {}),
      ...(audience ? { audience: audience as never } : {}),
      ...(taxon ? { taxon } : {}),
      ...(unclassified ? { unclassified: true } : {}),
      ...(withoutAxis ? { withoutAxis } : {}),
      ...(untagged ? { untagged: true } : {}),
      ...(parentHash ? { parentHash } : !filtered ? { includeComposites: true, topLevel: true } : {}),
      limit: CATALOG_PAGE_SIZE,
      ...(page > 1 ? { offset: (page - 1) * CATALOG_PAGE_SIZE } : {}),
    },
    { query: { enabled: true } },
  );
  const parent = fillerApi.useListFiller(
    { hashes: parentHash ? [parentHash] : [], includeComposites: true, limit: 1 },
    { query: { enabled: Boolean(parentHash) } },
  );

  // ⚠ `invalidateLifecycle` invalidates the clip list AND the two queue views (Incoming, pool).
  // The trio was written out by hand at four call sites here, which is three chances to forget
  // the third key and leave an operator looking at a queue that has already moved on.
  // ⚠ `invalidateCatalog` no longer has a caller here (§10 V54): the tag dialog's save was its
  // last one, and that moved to `invalidateLifecycle` because the Incoming rows render the very
  // badges the dialog edits. Destructuring it "just in case" is how a page keeps a second, weaker
  // invalidation vocabulary alive for the next writer to reach for by accident.
  const { invalidateLifecycle } = useFillerInvalidate();

  // ⚠ The Discover query state that used to live here is GONE with its tab (V35). Searching a
  // source is moving onto the Sources tab, where it belongs — a search is something you do to a
  // source, not a destination of its own — and `GET /v1/filler/discover` is still the route
  // behind it. Deleting the state rather than leaving it wired to nothing: an unused query that
  // still fires is how a page keeps paying for a feature nobody can reach.

  // ⚠ The filing mutations (file / hold / confirm-era) and their busy-row state moved into
  // `IncomingTab` with the queue they serve. They are not shared: nothing outside Incoming
  // files or holds a clip, so mounting them here made the Catalog tab pay for a surface it
  // never touches.

  // Bulk selection (V35). ⚠ Deliberately NOT in the URL, unlike the filters: a selection is a
  // transient intent about the rows in front of you, and a shared link that carried it would
  // hand someone else a pre-armed destructive action over clips they never chose.
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const toggleSelected = (hash: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (!next.delete(hash)) next.add(hash);
      return next;
    });
  const clearSelection = () => setSelected(new Set());
  // Paging (§10 V51d).
  //
  // ⚠ `replace: false`, unlike `setFilters`: paging IS navigation, so the back button should walk
  // back through the pages. Typing in a search box should not stack one history entry per
  // keystroke, which is why the two differ.
  //
  // ⚠ It clears the selection. "Select this page" means the rows in front of you, and the next
  // control on the bulk bar is Remove-from-catalog — a selection that survived paging would let
  // one click remove clips from a page nobody is looking at.
  const goToPage = (next: number) => {
    clearSelection();
    navigate({
      to: "/filler/library",
      search: (prev) => ({ ...prev, page: next > 1 ? next : undefined }),
    });
  };
  // "Select all" over the rows currently RENDERED (V35b). ⚠ Scoped to the filtered set on
  // purpose: `rows` is what the operator can see, and the bulk bar's next control is
  // "Remove from catalog". Selecting clips hidden behind a filter would arm a destructive
  // action over rows nobody looked at.
  //
  // It doubles as the un-select once everything shown is picked, so the control is not a
  // one-way door that needs a second button to undo.
  const allSelected = (rows: readonly ClipDTO[]) =>
    rows.length > 0 && rows.every((clip) => selected.has(clip.hash));
  const selectAll = (rows: readonly ClipDTO[]) =>
    setSelected(allSelected(rows) ? new Set() : new Set(rows.map((clip) => clip.hash)));

  // Bulk retag. Each field is independent on the server, so sending only what the operator
  // picked leaves the other two alone rather than blanking them.
  const bulkTag = fillerApi.useBulkTagFiller({
    mutation: {
      onSuccess: (res) => {
        clearSelection();
        invalidateLifecycle();
        const updated = isOk(res) ? res.data.updated : 0;
        toast.success(`Retagged ${pluralize(updated, "clip")}`);
      },
      onError: (e) => toast.error(toProblem(e).title ?? "Couldn't retag those clips"),
    },
  });

  // Removing a clip from the catalog (V35). ⚠ A TOMBSTONE on the server: the clip leaves the
  // catalog and stops being used in breaks, and the file is untouched. The copy here says
  // "catalog" for that reason and must keep saying it.
  const removeClips = fillerApi.useBulkRemoveFiller({
    mutation: {
      onSuccess: invalidateLifecycle,
      onError: (e) => toast.error(toProblem(e).title ?? "Couldn't remove those clips"),
    },
  });

  // Era-suggestion confirm (§10 V34). ⚠ The PATCH body must carry the clip's CURRENT
  // audience: UpdateClipClassification writes both scalar fields unconditionally, so a
  // bare `{era}` would wipe it. Setting era confirms and clears the
  // suggestion in the same write (the BE's rule).
  // ⚠ `invalidateLifecycle`, not just the clip list. A retag can move a clip between the queue
  // and the catalog and changes what the pool can cover, so all three views are now stale —
  // without the other two keys the row sits in Incoming until a reload.
  const confirmEra = fillerApi.useTagFillerClip({
    mutation: {
      onSuccess: invalidateLifecycle,
      onError: (e) => toast.error(toProblem(e).title ?? "Couldn't confirm the era"),
    },
  });

  // Curried so the JSX spread stays a plain value — a typed inline arrow inside
  // `{...(cond ? {...} : {})}` trips the TSX parser on the generic-looking annotation.
  const cycleFor = (clip: ClipDTO) => (change: TagChange) => retag(clip, change);

  // ⚠ THE ONLY WAY THIS PAGE WRITES ONE SCALAR CLASSIFICATION. `UpdateClipClassification`
  // overwrites era and audience on every call, so a PATCH carrying just the changed field silently
  // wipes the other. The taxonomy and its category shadow are a separate transaction.
  const retag = (clip: ClipDTO, change: TagChange) =>
    confirmEra.mutate({
      data: {
        // ⚠ The clip is identified by `hash` IN THE BODY (§10 V45a) — no {id} URL segment (the
        // path has slashes a route can't match / a proxy decodes).
        hash: clip.hash,
        // Kind is deliberately absent: the BE writes it separately (a shared code path with
        // the AI tagger), and sending it here would be a second opinion on a field this
        // interaction never edits. ⚠ `category`/`tags` are absent too (§10 V45a): a cycle only
        // ever changes era or audience, and omitting tags leaves the clip's taxonomy tags alone —
        // the derived category shadow rides along unchanged. Tag edits go through the dialog.
        era: change.era ?? clip.era,
        audience: (change.audience ?? clip.audience) as never,
      },
    });

  // Compilation splitting (§10 V34): POST starts a detection JOB, the terminal
  // `filler_split` SSE frame hands over the proposal id, and we navigate to the review
  // gate. Same shape as the ingest job below — request returns immediately, progress
  // arrives on the bus.
  const [splitJob, setSplitJob] = useState<{
    clipHash: string;
    // The status line reads better with a name than a content hash; carried alongside the
    // identity because the DTO's wire identity (hash) is meaningless to read in a sentence.
    clipName: string;
    jobId: string;
    status: string;
    error?: string;
  }>();
  const split = fillerApi.useSplitFiller({
    mutation: {
      onSuccess: (res, vars) => {
        if (isOk(res)) {
          setSplitJob({
            clipHash: vars.data.hash,
            clipName: pendingSplitName.current ?? vars.data.hash,
            jobId: res.data.jobId,
            status: "running",
          });
        }
      },
    },
  });
  // The clip name for whatever split is currently in flight (§10 V45a) — a ref, not state,
  // because it is write-then-read-once inside the mutation callback above and never rendered
  // itself; only the resulting `splitJob.clipName` is.
  const pendingSplitName = useRef<string | undefined>(undefined);

  useLoomarrEventListener({
    onFillerSplit: (e) => {
      // Frames for OTHER split jobs (another tab, another admin) are not ours to act on.
      if (!e.jobId || e.jobId !== splitJob?.jobId) return;
      if (e.status === "success" && e.proposalId) {
        setSplitJob(undefined);
        void navigate({ to: "/filler/splits/$proposalId", params: { proposalId: e.proposalId } });
      } else if (e.status === "error") {
        setSplitJob((prev) => (prev ? { ...prev, status: "error", error: e.error } : prev));
      }
    },
  });

  const rows = clips.data?.status === 200 ? (clips.data.data.clips ?? []) : undefined;
  const clipList = rows ?? [];
  // ⚠ `total` is how many clips MATCH THE FILTER, counted in SQL through the same predicate as
  // the rows (§10 V51d) — not `rows.length`, which is one page. The two are the same number only
  // on a single-page result, which is exactly why deriving one from the other looked fine for as
  // long as the listing was unbounded.
  const total = clips.data?.status === 200 ? (clips.data.data.total ?? 0) : 0;
  const pageCount = Math.max(1, Math.ceil(total / CATALOG_PAGE_SIZE));
  const firstOnPage = (page - 1) * CATALOG_PAGE_SIZE + 1;
  const lastOnPage = (page - 1) * CATALOG_PAGE_SIZE + clipList.length;

  const parentName = unwrap(parent.data, (body) => body.clips[0]?.name);
  const selectableRows = clipList.filter((clip) => !clip.isComposite);

  const renderCatalogRow = (clip: ClipDTO) => (
    <ClipRow
      key={clip.hash}
      clip={clip}
      {...(isAdmin ? { onToggleSelect: () => toggleSelected(clip.hash) } : {})}
      selected={selected.has(clip.hash)}
    />
  );
  const renderCatalogCard = (clip: ClipDTO) => (
    <ClipCard
      key={clip.hash}
      clip={clip}
      {...(isAdmin ? { onTag: () => onEditTags(clip.hash) } : {})}
      {...(isAdmin && clip.aiTagged ? { onConfirmTags: () => onEditTags(clip.hash) } : {})}
      {...(isAdmin && !clip.isComposite ? { onPin: () => setPinning(clip.hash) } : {})}
      {...(isAdmin && clip.suggestedEra
        ? { onConfirmEra: () => retag(clip, { era: clip.suggestedEra ?? 0 }) }
        : {})}
      {...(isAdmin ? { onCycle: cycleFor(clip) } : {})}
      {...(isAdmin && clip.isComposite ? { onSplit: () => setSplitting(clip.hash) } : {})}
      splitPending={Boolean(splitJob) && splitJob?.clipHash === clip.hash && splitJob.status === "running"}
      {...(isAdmin ? { onToggleSelect: () => toggleSelected(clip.hash) } : {})}
      selected={selected.has(clip.hash)}
      onPlay={() => setPlaying(clip.hash)}
    />
  );
  const renderComposite = (clip: ClipDTO) => (
    <CompositeCatalogGroup
      key={clip.hash}
      clip={clip}
      onManage={() => setFilters({ parent: clip.hash })}
      renderParent={view === "list" ? renderCatalogRow : renderCatalogCard}
      renderChild={(child) => <ClipRow key={child.hash} clip={child} />}
    />
  );

  return (
    <div className="flex flex-col gap-6">
      {split.error != null && <ErrorState error={split.error} />}
      {clips.error != null && <ErrorState error={clips.error} onRetry={() => clips.refetch()} />}

      {parentHash ? (
        <div className="flex flex-wrap items-center gap-3 rounded-lg border border-border bg-card p-3">
          <div className="min-w-0 flex-1">
            <p className="font-medium text-sm">{parentName || "Compilation segments"}</p>
            <p className="text-muted-foreground text-xs">Airable clips filed from this compilation.</p>
          </div>
          <Button variant="outline" size="sm" onClick={() => setFilters({ parent: undefined })}>
            Back to top-level catalog
          </Button>
        </div>
      ) : null}

      {taxon || unclassified || withoutAxis ? (
        <div className="flex flex-wrap items-center gap-3 rounded-lg border border-signal/30 bg-signal/5 p-3">
          <p className="min-w-0 flex-1 text-sm">
            {unclassified
              ? "Showing clips with no directly assigned taxonomy tags"
              : withoutAxis
                ? `Showing clips without a directly assigned ${withoutAxis} tag`
                : `Showing clips matching “${taxon}”, including descendants`}
          </p>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setFilters({ taxon: undefined, unclassified: undefined, withoutAxis: undefined })}
          >
            Clear taxonomy filter
          </Button>
        </div>
      ) : null}

      {/* The bulk bar, shown only when something is selected. It appears ABOVE the grid
              rather than floating over it: a bar that covers the cards hides the very thing the
              operator is deciding about. */}
      {selected.size > 0 && (
        <div className="flex flex-wrap items-center gap-3 rounded-lg border border-signal/40 bg-signal/5 p-3">
          <span className="font-mono text-signal text-xs">{pluralize(selected.size, "clip")} selected</span>

          {BULK_TAG_FIELDS.map((field) => (
            <Select
              key={field.key}
              value=""
              onValueChange={(value) =>
                bulkTag.mutate({
                  data: {
                    // The selection is HASHES (§10 V45a) — the bulk endpoint now keys on hashes,
                    // matching the single-clip PATCH.
                    hashes: [...selected],
                    [field.key]: field.key === "era" ? Number(value) : value,
                  },
                })
              }
            >
              <SelectTrigger className="w-auto min-w-32" aria-label={field.label}>
                <SelectValue placeholder={field.label} />
              </SelectTrigger>
              <SelectContent>
                {field.options.map((option) => (
                  <SelectItem key={option} value={option}>
                    {option}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ))}

          {/* ⚠ "Remove from catalog", not "Delete". The server's action is a TOMBSTONE: the
                  clip stops appearing here and stops being used in breaks, and the file stays
                  exactly where the operator put it. The label has to keep saying catalog. */}
          <Button
            variant="outline"
            size="sm"
            disabled={removeClips.isPending}
            // ⚠ Same KNOWN GAP as the bulk-tag selects above: `paths` is genuinely path-keyed
            // server-side and `selected` can only carry `clip.hash` now that `ClipDTO` has no
            // path. See the comment on the bulk-tag Select's onValueChange.
            onClick={() => removeClips.mutate({ data: { hashes: [...selected] } })}
            title="Stop using these clips. The files stay in your folder."
          >
            Remove from catalog
          </Button>

          <Button variant="ghost" size="sm" className="ml-auto" onClick={clearSelection}>
            Clear
          </Button>
        </div>
      )}

      {/* Split detection progress (§10 V34) — a job, not a request, so it gets a live
              status line the way ingest does. Success NAVIGATES to the review route; only
              running/error render here. */}
      {splitJob && (
        <p
          role="status"
          className={splitJob.status === "error" ? "text-onair-300 text-sm" : "text-muted-foreground text-sm"}
        >
          {splitJob.status === "error"
            ? (splitJob.error ?? "Split detection failed.")
            : `Detecting cuts in ${splitJob.clipName}… this can take a few minutes for a long compilation.`}
        </p>
      )}

      {/* ⚠ The "Synced: N added…" and "Tagged N of M…" result banners were here and went with
              their buttons (V38c). Reporting the outcome of work nothing on this page can start
              is a result with no cause — an operator reading it has no way to know what produced
              it or how to produce it again. Per-source outcomes belong on the Sources row that
              did the work. */}

      <Card className="flex flex-wrap items-end gap-3 p-4">
        {/* ⚠ Capped. `flex-1` alone stretched a clip-name box to ~900px on a 1440
                viewport — a text field far wider than anything typed into it, which reads
                as a layout bug rather than a generous input. It still grows on narrow
                screens (min-w-48) and stops being silly on wide ones. */}
        <div className="min-w-48 max-w-md flex-1">
          <Label htmlFor="clip-search">Search</Label>
          <Input
            id="clip-search"
            value={q}
            placeholder="Clip name"
            onChange={(e) => setFilters({ q: e.target.value || undefined })}
          />
        </div>
        <div>
          <Label htmlFor="clip-kind">Kind</Label>
          {/* "any" sentinel ↔ "" (Radix forbids an empty value) — the no-filter default. */}
          <Select
            value={kind || "any"}
            onValueChange={(v) => setFilters({ kind: v === "any" ? undefined : v })}
          >
            <SelectTrigger id="clip-kind">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="any">Any</SelectItem>
              <SelectItem value="commercial">Commercial</SelectItem>
              <SelectItem value="bumper">Bumper</SelectItem>
              <SelectItem value="station_id">Station ID</SelectItem>
              <SelectItem value="psa">PSA</SelectItem>
              <SelectItem value="trailer">Trailer</SelectItem>
              <SelectItem value="interstitial">Interstitial</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div>
          <Label htmlFor="clip-audience">Audience</Label>
          <Select
            value={audience || "any"}
            onValueChange={(v) => setFilters({ audience: v === "any" ? undefined : v })}
          >
            <SelectTrigger id="clip-audience">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="any">Any</SelectItem>
              <SelectItem value="kids">Kids</SelectItem>
              <SelectItem value="family">Family</SelectItem>
              <SelectItem value="general">General</SelectItem>
              <SelectItem value="late_night">Late night</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <Button
          variant={untagged ? "default" : "outline"}
          size="sm"
          onClick={() => setFilters({ untagged: untagged ? undefined : true })}
          // "Untagged" means a COMMERCIAL missing a match tag — bumpers do their job
          // without era/audience, so they are never counted as needing work (§10).
          title="Commercials missing era, audience, or category"
        >
          Untagged only
        </Button>

        {/* Grid ⇄ list (V35b, the mock's `catViews`). ⚠ A radiogroup, not two toggle
                buttons: these are two states of ONE setting, and a pair of independent
                buttons announces neither which is active nor that they are alternatives.
                `ml-auto` puts it at the far end of the toolbar, as the mock draws it. */}
        <div className="ml-auto flex gap-1" role="radiogroup" aria-label="Clip view">
          {VIEWS.map((v, index) => (
            <Button
              key={v.id}
              ref={(node) => {
                viewRefs.current[index] = node;
              }}
              variant={view === v.id ? "default" : "outline"}
              size="sm"
              role="radio"
              aria-checked={view === v.id}
              tabIndex={view === v.id ? 0 : -1}
              title={v.title}
              onClick={() => chooseView(v.id)}
              onKeyDown={(event) => onViewKeyDown(event, index)}
            >
              <v.icon className="size-4" aria-hidden />
              {v.label}
            </Button>
          ))}
        </div>
      </Card>

      {rows === undefined ? (
        <p className="text-muted-foreground text-sm">Loading clips…</p>
      ) : rows.length === 0 ? (
        <EmptyState
          title={filtered ? "No clips match" : "No clips yet"}
          description={
            filtered
              ? "Try a wider filter, or clear the search."
              : "Anything that lands in the filler folder shows up here on its own. Drop files in, or ask Loomarr to pull some."
          }
          {...(filtered
            ? {
                action: {
                  label: "Clear filters",
                  onClick: () =>
                    setFilters({
                      q: undefined,
                      kind: undefined,
                      audience: undefined,
                      taxon: undefined,
                      unclassified: undefined,
                      withoutAxis: undefined,
                      untagged: undefined,
                    }),
                },
              }
            : // An empty catalog is exactly when an operator needs the way OUT of it, so the
              // empty state carries the same action the health strip does.
              //
              // ⚠ It used to read "Find clips" and navigate to `tab: "discover"` — a tab this
              // phase RETIRED. `validateSearch` drops the unknown value, so the button landed
              // back on the empty catalog it was offered from: a control that looked like the
              // way out and did nothing. Two independent reviewers found it, which is the
              // useful lesson — deleting a destination is not done until every route TO it is
              // gone, and a nav target is not type-checked.
              isAdmin
              ? {
                  action: {
                    label: "Propose a pull",
                    onClick: onProposePull,
                  },
                }
              : {})}
        />
      ) : (
        <>
          {/* The count line sits ABOVE the grid ("did my filter work?"), the pager BELOW
                  ("what's next?") — never both in one place. */}
          <div className="flex items-center gap-3">
            <p className="text-muted-foreground text-sm">
              {pageCount > 1
                ? `Showing ${firstOnPage.toLocaleString()}–${lastOnPage.toLocaleString()} of ${total.toLocaleString()}`
                : pluralize(total, "clip")}
            </p>
            {/* Select all (V35b, the mock's `catSelAll`). ⚠ It selects the FILTERED rows —
                    what is on screen — not the whole catalog. Selecting rows the operator
                    cannot see, then offering "Remove from catalog", is how a bulk action
                    surprises someone. The label says so when a filter is active.
                    ⚠ Paging sharpened that: "on screen" is now one PAGE, so the label says
                    "Select this page" whenever there is more than one — and `goToPage` clears
                    the selection, or a Remove would reach rows from a page nobody is looking at. */}
            {isAdmin && (
              <Button variant="ghost" size="sm" onClick={() => selectAll(selectableRows)}>
                {allSelected(selectableRows)
                  ? "Clear selection"
                  : pageCount > 1
                    ? "Select this page"
                    : filtered
                      ? "Select these"
                      : "Select all"}
              </Button>
            )}
          </div>
          {view === "list" ? (
            <div className="overflow-hidden rounded-lg border border-border">
              {rows.map((clip) => (clip.isComposite ? renderComposite(clip) : renderCatalogRow(clip)))}
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {rows.map((clip) =>
                clip.isComposite ? (
                  <div key={clip.hash} className="col-span-full">
                    {renderComposite(clip)}
                  </div>
                ) : (
                  renderCatalogCard(clip)
                ),
              )}
            </div>
          )}

          {/* The pager sits BELOW the grid ("what's next?"); the count line above it answers
                  "did my filter work?". Rendered only when there is more than one page, so the
                  common household catalog never grows a control it does not need.

                  ⚠ Prev/Next plus a position, not a numbered page strip. A strip is the V51e
                  catalog redesign's call to make; what this must not do is leave the clips past
                  row 60 unreachable, which is what a page size with no pager would be. */}
          {pageCount > 1 && (
            <nav aria-label="Catalog pages" className="flex items-center justify-between gap-3 pt-1">
              <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => goToPage(page - 1)}>
                Previous
              </Button>
              {/* ⚠ `aria-live="polite"`: paging replaces the grid in place, and without an
                      announcement a screen-reader user gets a silently different list. */}
              <p aria-live="polite" className="text-muted-foreground text-sm">
                Page {page.toLocaleString()} of {pageCount.toLocaleString()}
              </p>
              <Button
                variant="outline"
                size="sm"
                disabled={page >= pageCount}
                onClick={() => goToPage(page + 1)}
              >
                Next
              </Button>
            </nav>
          )}
        </>
      )}

      {pinning && rows && (
        <PinClipDialog clip={rows.find((c) => c.hash === pinning)} onClose={() => setPinning(undefined)} />
      )}

      {/* The split confirmation (§10 V54 A8). Resolved from the CURRENT page like its
              siblings, so a clip that vanishes under a filter closes the dialog rather than
              confirming a hash that is no longer on screen. */}
      {splitting && rows && (
        <ConfirmSplitDialog
          clip={rows.find((c) => c.hash === splitting)}
          onConfirm={() => {
            const clip = rows.find((c) => c.hash === splitting);
            setSplitting(undefined);
            if (!clip) return;
            pendingSplitName.current = clip.name;
            split.mutate({ data: { hash: clip.hash } });
          }}
          onClose={() => setSplitting(undefined)}
        />
      )}

      {/* The player (V39). ⚠ `?? null` rather than a `&&` guard like its siblings above: this
              dialog takes a NULLABLE clip and derives `open` from it, so handing it `undefined`
              would be a type error and handing it nothing at all would leave it permanently
              closed. A row that has vanished under a filter closes the player, which is the
              honest outcome — the clip it was showing is no longer in the list. */}
      <ClipPlayer
        clip={rows?.find((c) => c.hash === playing) ?? null}
        onClose={() => setPlaying(undefined)}
      />
    </div>
  );
};

export { FillerCatalog };
