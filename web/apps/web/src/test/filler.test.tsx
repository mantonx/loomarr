import type {
  ClipDTO,
  FillerIncomingOutputBody,
  FillerWatchOutputBody,
  ListTaxonomyOutputBody,
  MeBody,
} from "@loomarr/api";
import {
  getBulkRemoveFillerMockHandler,
  getBulkTagFillerMockHandler,
  getCreateTaxonMockHandler,
  getFillerIncomingMockHandler,
  getFillerPoolMockHandler,
  getFillerWatchMockHandler,
  getGetFillerSplitMockHandler,
  getIngestFillerMockHandler,
  getListFillerMockHandler,
  getListTaxonomyMockHandler,
  getMeMockHandler,
  getPreviewTaxonomyEditMockHandler,
  getRewindFillerClipMockHandler,
  getSettingsListMockHandler,
  getSplitFillerMockHandler,
  getSyncFillerMockHandler,
  getTagFillerClipMockHandler,
  getTagFillerMockHandler,
} from "@loomarr/api/msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Toaster } from "sonner";
import { afterEach, describe, expect, it, vi } from "vitest";
import { routeTree } from "@/routeTree.gen";
import { me } from "@/test/fixtures/users";
import { appHandlers } from "@/test/msw/handlers";
import { server } from "@/test/msw/server";

const ADMIN = me();
const MEMBER: MeBody = me({ role: "member" });

// ⚠ `playCount` and `playsCounted` are REQUIRED on ClipDTO and this fixture omitted both. It was
// typed `Record<string, unknown>`, so nothing objected — and `playsCounted` is precisely the field
// whose absence is indistinguishable from `false`, which the DTO's own comment says must render as
// "not counted" rather than "0".
const clip = (over: Partial<ClipDTO> = {}): ClipDTO => ({
  hash: "c1-hash",
  tunarrProgramId: "c1",
  name: "Frosted Flakes",
  kind: "commercial",
  durationMs: 30000,
  era: 1992,
  audience: "kids",
  category: "cereal",
  tags: ["cereal", "food"],
  assertedTags: ["cereal"],
  tagged: true,
  aiTagged: false,
  playCount: 0,
  playsCounted: true,
  ...over,
});

type Opts = {
  features?: Record<string, boolean>;
  clips?: ClipDTO[];
  me?: MeBody;
  // Overrides the header pill's payload. Needed because `clips` here seeds the CATALOG, and the
  // states worth testing in the header are the ones where the catalog and the review queue
  // disagree — a fresh auto-fetch has zero of one and a dozen of the other.
  watch?: FillerWatchOutputBody;
  // Clips that exist but are HELD, so `GET /v1/filler` returns them only when the caller asks for
  // them (§10 V38). Kept separate from `clips` rather than flagged inside it, because the
  // distinction the stub has to honour is which LIST a clip appears in, not a field on the row.
  held?: ClipDTO[];
  // The review queue's payload. Defaults to empty — most tests here are about the catalog.
  incoming?: Partial<FillerIncomingOutputBody>;
  taxonomy?: ListTaxonomyOutputBody;
};

// The SSE stream, captured so a test can fire frames at the app: the split job's terminal
// `filler_split` frame is what hands the review route its proposal id (V34), and a no-op
// EventSource would leave that handoff untestable.
class CaptureEventSource {
  static listeners = new Map<string, Array<(ev: MessageEvent) => void>>();
  addEventListener(type: string, cb: (ev: MessageEvent) => void) {
    const list = CaptureEventSource.listeners.get(type) ?? [];
    list.push(cb);
    CaptureEventSource.listeners.set(type, list);
  }
  close() {}
}

const fireFrame = (type: string, payload: unknown) => {
  const data = JSON.stringify(payload);
  for (const cb of CaptureEventSource.listeners.get(type) ?? []) {
    cb({ data } as MessageEvent);
  }
};

// ⚠ The stub this replaces was a ten-branch `includes` ladder whose CORRECTNESS DEPENDED ON ITS
// ORDER, and it said so out loud: "Stubbed BEFORE the catch-all `/v1/filler` branch below, which
// would otherwise answer these with a clip list". That comment is the bug report. Three of the
// branches were wrong in ways nothing could see:
//
//   • `u.includes("/v1/filler/") && method === "PATCH"` matched BOTH `PATCH /v1/filler/tags`
//     (tag one clip) and `PATCH /v1/filler/sources/:id` (enable/disable a source). Three separate
//     assertions in this file then searched for "a PATCH, to anything" and read its body.
//   • `u.endsWith("/split")` and `u.includes("/v1/filler/splits/")` are two different endpoints
//     distinguished only by a trailing slash and a plural.
//   • `u.includes("/v1/filler/tag")` also matches `/v1/filler/tags`, so the whole-catalog AI
//     tagger and the single-clip tag write shared one branch.
//
// Every one is route-bound now, and the recorded values below come from the resolver that owns
// the route rather than from a scan of the test's own call log.
const stubFiller = ({
  features = { filler: true, suggestions: true },
  clips = [clip()],
  me: who = ADMIN,
  watch,
  held = [],
  incoming,
  taxonomy,
}: Opts = {}) => {
  CaptureEventSource.listeners = new Map();
  const tagPatches: unknown[] = [];
  const bulkRemoves: unknown[] = [];
  const bulkTags: unknown[] = [];
  const listQueries: string[] = [];
  const rewinds: unknown[] = [];
  const taxonCreates: unknown[] = [];
  let splits = 0;

  server.use(
    getMeMockHandler(who),
    getSyncFillerMockHandler({ total: 3, added: 2, updated: 1, pruned: 0 }),
    getTagFillerMockHandler({ considered: 2, tagged: 2, partial: 0, skipped: 0 }),
    getIngestFillerMockHandler({ jobId: "job-1" }),
    // Split (V34): detection starts as a job; the review route reads the proposal back.
    getSplitFillerMockHandler(() => {
      splits += 1;
      return { jobId: "job-split-1" };
    }),
    getGetFillerSplitMockHandler({
      id: "sp-1",
      clipHash: "c1",
      createdAt: "2026-07-25T20:00:00Z",
      segments: [{ index: 0, startMs: 0, endMs: 30000, name: "First ad" }],
    }),
    getTagFillerClipMockHandler(async ({ request }) => {
      tagPatches.push(await request.json());
      return clips[0] ?? clip();
    }),
    getRewindFillerClipMockHandler(async ({ request }) => {
      rewinds.push(await request.json());
    }),
    getCreateTaxonMockHandler(async ({ request }) => {
      const body = (await request.json()) as {
        slug: string;
        label: string;
        axis: "product" | "format" | "seasonal" | "audience-cue";
      };
      taxonCreates.push(body);
      return { ...body, assertedClips: 0, matchedClips: 0, storedClips: 0 };
    }),
    getPreviewTaxonomyEditMockHandler(async ({ request }) => {
      const body = (await request.json()) as { operation: "create" | "update" | "delete"; slug: string };
      const deleting = body.operation === "delete";
      return {
        directStoredClips: deleting ? 2 : 0,
        descendantStoredClips: 0,
        affectedStoredClips: deleting ? 2 : 0,
        affectedPlayableClips: deleting ? 1 : 0,
        descendants: [],
        savedChannelSelections: [],
        resolverTermsAdded: body.operation === "create" ? [body.slug] : [],
        resolverTermsRemoved: deleting ? [body.slug] : [],
        deleteBlocked: deleting,
      };
    }),
    getFillerPoolMockHandler({
      clips: clips.length,
      commercials: clips.length,
      eligible: clips.length,
      untagged: 0,
      channels: [],
    }),
    getFillerIncomingMockHandler({
      clips: [],
      reels: [],
      recentlyFiled: [],
      rejected: [],
      stageOrder: [],
      total: 0,
      ...incoming,
      overview: incoming?.overview ?? {
        runnable: 0,
        inProgress: 0,
        scheduled: 0,
        needsDecision: 0,
        recoverable: 0,
        admitted: 0,
        rejected: 0,
        dismissed: 0,
      },
      clipsTotal: incoming?.clipsTotal ?? incoming?.clips?.length ?? 0,
      decisionsTotal:
        incoming?.decisionsTotal ?? incoming?.clips?.filter((clip) => clip.needsDecision).length ?? 0,
      reelsTotal: incoming?.reelsTotal ?? incoming?.reels?.length ?? 0,
      recentlyFiledTotal: incoming?.recentlyFiledTotal ?? incoming?.recentlyFiled?.length ?? 0,
      rejectedTotal: incoming?.rejectedTotal ?? incoming?.rejected?.length ?? 0,
    }),
    // The header pill's live status (§10 V38c). ⚠ Served here because the header reads it from
    // the SERVER — counts and health verdict both — rather than deriving them from the sources
    // list, which is admin-only and would leave a member's pill permanently grey.
    getFillerWatchMockHandler(
      watch ?? { health: "healthy", sourcesOn: 1, sourcesTotal: 2, clips: clips.length, held: 0 },
    ),
    getBulkRemoveFillerMockHandler(async ({ request }) => {
      bulkRemoves.push(await request.json());
      return { updated: 1, missing: 0 };
    }),
    getBulkTagFillerMockHandler(async ({ request }) => {
      bulkTags.push(await request.json());
      return { updated: 1, missing: 0 };
    }),
    getListFillerMockHandler(({ request }) => {
      // Honor the query string so a filter test proves the SERVER did the filtering.
      const params = new URL(request.url).searchParams;
      listQueries.push(params.toString());
      // ⚠ **The held predicate is enforced here, and the `hashes` filter ANDs with it** — exactly
      // as the store does (`clipWhere`), where held clips are excluded at one chokepoint and every
      // other filter narrows what survives it. A stub that handed back a held clip for a bare
      // `hashes` query could not fail when a caller forgot `includeHeld`, which is precisely the
      // defect this models: the shared tag dialog resolves a clip by identity, and on the Incoming
      // tab every clip it can be asked about is held.
      let out = params.get("includeHeld") === "true" ? [...clips, ...held] : clips;
      const hashes = params.getAll("hashes");
      if (hashes.length > 0) out = out.filter((c) => hashes.includes(c.hash));
      const q = params.get("q");
      if (q) out = out.filter((c) => c.name.toLowerCase().includes(q.toLowerCase()));
      const kind = params.get("kind");
      if (kind) out = out.filter((c) => c.kind === kind);
      // ⚠ `total` is the count IGNORING limit/offset (§10 V51d's pager). It is required, and no
      // hand-rolled stub in this suite ever sent it.
      return { clips: out, total: out.length };
    }),
    // ⚠ FOUND BY THE GUARD. The tag editor reads the taxonomy to build its tag picker, and the
    // old catch-all answered it with `{}` — so `taxa` was undefined every time the editor opened
    // and the picker rendered from nothing.
    getListTaxonomyMockHandler(
      taxonomy ?? {
        taxa: [],
        totalClips: clips.length,
        taggedClips: 0,
        unclassifiedClips: clips.length,
        axisCoverage: [],
      },
    ),
    getSettingsListMockHandler({ features, settings: [] }),
    ...appHandlers(),
  );

  vi.stubGlobal("EventSource", CaptureEventSource);
  return { tagPatches, bulkRemoves, bulkTags, listQueries, rewinds, taxonCreates, splitCount: () => splits };
};

const renderAt = (path: string) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <Toaster />
    </QueryClientProvider>,
  );
  return router;
};

// ⚠ `unstubAllGlobals`, not `restoreAllMocks` — CaptureEventSource is installed with
// `vi.stubGlobal`, and restoreAllMocks does not undo a stubbed global. Leaving it in place leaks
// the capture map into whatever runs next.
afterEach(() => vi.unstubAllGlobals());

describe("Filler page", () => {
  it("lists the catalog with each clip's match tags", async () => {
    stubFiller();
    renderAt("/filler/library");
    expect(await screen.findByText("Frosted Flakes")).toBeInTheDocument();
    // §10 V45a era label: a SPECIFIC year renders plain (the tagger grounds a literal year, §8), only
    // a decade boundary gets the "s". 1992 → "1992", not the old nonsense "1992s".
    expect(screen.getByText("1992")).toBeInTheDocument();
  });

  // Search is executed SERVER-side (§7.2 name LIKE) rather than filtering in memory —
  // the store already indexes these columns and the catalog can run to thousands.
  it("sends the search term to the server", async () => {
    const { listQueries } = stubFiller({
      clips: [
        clip(),
        clip({ hash: "c2-hash", tunarrProgramId: "c2", name: "TMNT figures", category: "toys" }),
      ],
    });
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");

    await userEvent.type(screen.getByLabelText("Search"), "tmnt");

    await screen.findByText("TMNT figures");
    expect(screen.queryByText("Frosted Flakes")).not.toBeInTheDocument();
    // The query params are read off the REQUEST inside the handler bound to `GET /v1/filler`, so
    // this cannot be satisfied by a `q=tmnt` appearing on some other endpoint's URL.
    expect(
      listQueries.some((s) => new URLSearchParams(s).get("q") === "tmnt"),
      "the filter must reach the API, not filter a cached list",
    ).toBe(true);
  });

  // ⚠ The whole-catalog Sync and AI-tag buttons are GONE (V38c, maintainer). Two tests here
  // pinned them — "runs a catalog sync and reports what changed" and "disables AI tagging when
  // no LLM is configured" — and both were removed rather than repaired, because they asserted
  // affordances that no longer exist rather than behaviour that broke.
  //
  // Neither was work an operator should have to remember to start: the sync runs on its own
  // schedule and drains the watch folder every pass, and tagging follows it. A button for
  // something that already happens invites the reading that it does NOT happen unless pressed.
  // This test is what stops them being re-added by reflex.
  it("offers no whole-catalog sync or tag button — that work runs on its own", async () => {
    stubFiller();
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");

    expect(screen.queryByRole("button", { name: /^sync$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /ai tag/i })).not.toBeInTheDocument();
  });

  // The mock's `watchLine`: what is on, what is held, when anything last arrived. ⚠ "N of M
  // sources on" rather than a bare count — an operator who switched a source off wants to see
  // that, and "3 sources" on an install where one is dark is a reassuring lie.
  it("heads the page with how many sources are on", async () => {
    stubFiller();
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");

    expect(await screen.findByText(/\d+ of \d+ sources on/i)).toBeInTheDocument();
  });

  // ⚠ **A held clip must not read as a missing clip.** Auto-fetch holds everything it downloads
  // until someone reviews it, so the FIRST successful fetch leaves the catalog at zero and the
  // Incoming queue full. The header said "5 of 5 sources on · 0 clips" on an install that had just
  // pulled twelve — a working fetcher rendered as a broken one, which is how the maintainer came
  // to ask why the Catalog was empty.
  //
  // The two counts stay SEPARATE clauses. Summing them would be the opposite lie: it would claim a
  // channel can play clips nobody has approved yet.
  it("says how many clips are waiting rather than reporting an empty catalog", async () => {
    stubFiller({
      clips: [],
      watch: { health: "healthy", sourcesOn: 5, sourcesTotal: 5, clips: 0, held: 12 },
    });
    renderAt("/filler/library");

    expect(await screen.findByText(/0 clips · 12 waiting/i)).toBeInTheDocument();
  });

  // The mirror: with nothing held the clause is absent entirely, not "0 waiting". A permanent
  // zero is noise on the many installs that never hold anything, and noise in a status line is
  // how an operator learns to stop reading it.
  it("omits the waiting clause when nothing is held", async () => {
    stubFiller({
      watch: { health: "healthy", sourcesOn: 5, sourcesTotal: 5, clips: 9, held: 0 },
    });
    renderAt("/filler/library");

    await screen.findByText(/9 clips/i);
    expect(screen.queryByText(/waiting/i)).not.toBeInTheDocument();
  });

  it("explains rather than listing when no filler folder is configured", async () => {
    stubFiller({ features: { filler: false } });
    renderAt("/filler/library");
    expect(await screen.findByText(/no filler folder configured/i)).toBeInTheDocument();
  });

  // ⚠ TWO ingest tests were here and are DELETED with the panel they drove (V38b, retired-ok):
  // one asserted the degraded-install copy, the other typed a URL and clicked Download.
  //
  // They are not replaced, because the behaviour is not relocated — it is gone. Clips arrive
  // because you added a SOURCE: registration, per-row search, an approved pull, or the auto-fetch
  // job, each of which has its own coverage. A test kept alive against a deleted surface is worse
  // than no test: it passes by rendering something nobody can reach.
  //
  // ⚠ The `loomarr:filler` (retired-ok) absence assertion went with them. That check now lives
  // where it belongs — `scripts/check-retired.sh` greps the whole tree for the dead image name,
  // which is stronger than one component test asserting one string is missing from one panel.

  it("opens the tag editor and saves corrected structural and grounded facts", async () => {
    const { tagPatches } = stubFiller({
      clips: [clip({ kind: "commercial", name: "Some Trailer", brand: "Wrong brand" })],
    });
    renderAt("/filler/library");
    await screen.findByText("Some Trailer");

    await userEvent.click(screen.getByRole("button", { name: /edit tags/i }));

    // Scoped to the editor's region: the page's own Kind/Audience FILTERS share those
    // visible names, which is why the editor is a labelled region in the first place.
    // Open the editor's Kind select, then pick "trailer" — the listbox portals to the
    // body (outside the region), so its option is found at the screen level.
    const editor = await screen.findByRole("region", { name: /edit tags: some trailer/i });
    await userEvent.click(within(editor).getByLabelText("Kind"));
    await userEvent.click(await screen.findByRole("option", { name: "Trailer" }));
    await userEvent.clear(within(editor).getByLabelText("Brand"));
    await userEvent.type(within(editor).getByLabelText("Brand"), "Warner Bros.");
    await userEvent.click(within(editor).getByRole("button", { name: /save tags/i }));

    // ⚠ Was `find(([, i]) => i?.method === "PATCH")` — "a PATCH, to anything". `tagPatches` is fed
    // only by the resolver bound to `PATCH /v1/filler/tags`.
    await expect.poll(() => tagPatches).toHaveLength(1);
    expect((tagPatches[0] as { kind: string }).kind).toBe("trailer");
    expect((tagPatches[0] as { brand: string }).brand).toBe("Warner Bros.");
  });

  it("round-trips only directly assigned tags, never inherited rollups", async () => {
    const { tagPatches } = stubFiller({
      clips: [clip({ tags: ["cereal", "food"], assertedTags: ["cereal"] })],
      taxonomy: {
        totalClips: 1,
        taggedClips: 1,
        unclassifiedClips: 0,
        axisCoverage: [],
        taxa: [
          { slug: "food", label: "Food", axis: "product", assertedClips: 0, matchedClips: 1, storedClips: 0 },
          {
            slug: "cereal",
            label: "Cereal",
            axis: "product",
            parent: "food",
            assertedClips: 1,
            matchedClips: 1,
            storedClips: 1,
          },
        ],
      },
    });
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");

    await userEvent.click(screen.getByRole("button", { name: /edit tags/i }));
    const editor = await screen.findByRole("region", { name: /edit tags: frosted flakes/i });
    expect(within(editor).getByRole("button", { name: /cereal$/i })).toHaveAttribute("aria-pressed", "true");
    expect(within(editor).getByRole("button", { name: "Food" })).toHaveAttribute("aria-pressed", "false");
    const derived = within(editor).getByRole("region", { name: "Derived matches" });
    expect(within(derived).getByText("Food")).toBeInTheDocument();
    expect(within(derived).getByText(/read only/i)).toBeInTheDocument();
    await userEvent.click(within(editor).getByRole("button", { name: /save tags/i }));

    await expect.poll(() => tagPatches).toHaveLength(1);
    expect((tagPatches[0] as { tags: string[] }).tags).toEqual(["cereal"]);
  });

  it("makes taxonomy hierarchy and coverage understandable, then links into the matching catalog", async () => {
    const { listQueries } = stubFiller({
      taxonomy: {
        totalClips: 12,
        taggedClips: 10,
        unclassifiedClips: 2,
        axisCoverage: [
          { axis: "product", taggedClips: 7, untaggedClips: 5 },
          { axis: "format", taggedClips: 3, untaggedClips: 9 },
          { axis: "seasonal", taggedClips: 1, untaggedClips: 11 },
          { axis: "audience-cue", taggedClips: 2, untaggedClips: 10 },
        ],
        taxa: [
          { slug: "food", label: "Food", axis: "product", assertedClips: 1, matchedClips: 7, storedClips: 2 },
          {
            slug: "cereal",
            label: "Cereal",
            axis: "product",
            parent: "food",
            assertedClips: 4,
            matchedClips: 4,
            storedClips: 4,
          },
        ],
      },
    });
    const router = renderAt("/filler/taxonomy");

    expect(await screen.findByText("10 / 12")).toBeInTheDocument();
    expect(screen.getAllByText("Products & topics")).toHaveLength(2);
    await userEvent.click(screen.getByText("Manage vocabulary"));
    expect(screen.getByText("1 direct")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Food" }));
    await userEvent.click(screen.getByRole("button", { name: "Review removal" }));
    expect(
      await screen.findByText(/retag 2 directly assigned stored clips before removing/i),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Confirm removal" })).toBeDisabled();
    await userEvent.click(screen.getByRole("link", { name: "7 clips" }));

    await expect.poll(() => router.state.location.pathname).toBe("/filler/library");
    expect(router.state.location.search).toMatchObject({ taxon: "food" });
    await expect
      .poll(() => listQueries.some((query) => new URLSearchParams(query).get("taxon") === "food"))
      .toBe(true);

    await router.navigate({ to: "/filler/taxonomy" });
    await userEvent.click(await screen.findByRole("link", { name: /browse 5 without/i }));
    await expect.poll(() => router.state.location.search).toMatchObject({ withoutAxis: "product" });
    await expect
      .poll(() => listQueries.some((query) => new URLSearchParams(query).get("withoutAxis") === "product"))
      .toBe(true);
  });

  it("lets an admin add a grounded classifier term from the taxonomy surface", async () => {
    const { taxonCreates } = stubFiller({
      taxonomy: { totalClips: 0, taggedClips: 0, unclassifiedClips: 0, axisCoverage: [], taxa: [] },
    });
    renderAt("/filler/taxonomy");
    await screen.findByText("Manage vocabulary");
    await userEvent.click(screen.getByText("Manage vocabulary"));
    const addProduct = screen.getAllByRole("button", { name: "Add" })[0];
    if (!addProduct) throw new Error("product vocabulary Add button missing");
    await userEvent.click(addProduct);

    await userEvent.type(screen.getByLabelText("Label"), "Breakfast cereal");
    await userEvent.type(screen.getByLabelText("Slug"), "breakfast-cereal");
    await userEvent.click(screen.getByRole("button", { name: "Review changes" }));
    expect(await screen.findByText("No stored clip classifications will change.")).toBeInTheDocument();
    expect(screen.getByText(/starts resolving: breakfast-cereal/i)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Confirm add" }));

    await expect
      .poll(() => taxonCreates)
      .toEqual([
        {
          slug: "breakfast-cereal",
          label: "Breakfast cereal",
          axis: "product",
          synonyms: [],
          retiredAliases: [],
        },
      ]);
  });

  it("names the auto-fetch ceiling that paused unattended acquisition", async () => {
    stubFiller({
      watch: {
        health: "healthy",
        sourcesOn: 2,
        sourcesTotal: 2,
        clips: 2000,
        held: 0,
        autoFetch: {
          enabled: true,
          stoppedBy: "catalog",
          catalogClips: 2000,
          maxCatalog: 2000,
        },
      },
    });
    renderAt("/filler/library");

    expect(await screen.findByText("Automatic fetching is paused")).toBeInTheDocument();
    expect(screen.getByText(/2,000 of 2,000 catalog clips/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Review limits" })).toHaveAttribute("href", "/filler/settings");
  });

  // §10 era grounding (V34): the ungrounded AI year is a QUESTION on the card, and the
  // admin's one-click confirm PATCHes it — carrying the clip's existing audience, because the
  // BE's UpdateClipClassification writes era and audience unconditionally and a bare {era} would wipe
  // audience. `category` is NOT sent (§10 V45a): it's a derived shadow of the taxonomy tags,
  // and this confirm never touches tags.
  it("confirms an era suggestion, keeping the clip's other tags", async () => {
    const { tagPatches } = stubFiller({
      clips: [clip({ era: 0, suggestedEra: 1985, audience: "kids", category: "cereal", tagged: false })],
    });
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");
    expect(screen.getByText("1985s?")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /confirm 1985/i }));

    // §10 V45a: the clip is identified by `hash` in the body (no {id} URL segment).
    await expect
      .poll(() => tagPatches, { message: "the confirm should PATCH the clip" })
      .toEqual([{ hash: "c1-hash", era: 1985, audience: "kids" }]);
  });

  // ⚠ THE FOOTGUN, pinned at the page level. `UpdateClipClassification` overwrites era and audience on
  // every call, so a cycle that PATCHed only the clicked field would silently wipe the other —
  // once per click, not once per dialog. This asserts the whole tag row travels with a single
  // cycled chip. `category` is NOT part of the body (§10 V45a): it's a derived shadow, and a
  // cycle never touches the clip's taxonomy tags — omitting `tags` leaves them, and therefore
  // the shadow, unchanged server-side.
  it("sends the clip's other tags when one is cycled, so none are wiped", async () => {
    const { tagPatches } = stubFiller({
      clips: [clip({ era: 1990, audience: "kids", category: "cereal", tagged: true })],
    });
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");

    await userEvent.click(screen.getByRole("button", { name: /change the audience/i }));

    // era rides along UNCHANGED; only audience advances; category/tags are absent entirely.
    // §10 V45a: the clip is identified by `hash` in the body.
    await expect
      .poll(() => tagPatches, { message: "cycling a chip should PATCH the clip" })
      .toEqual([{ hash: "c1-hash", era: 1990, audience: "family" }]);
  });

  // A member sees the suggestion but NOT its answer — the PATCH is admin-only server-side
  // (§19), and the UI gate is the courtesy that keeps the console clean.
  it("shows a member the era question without the confirm action", async () => {
    // ⚠ `isComposite` is deliberate. Since V54 A8 the split action renders only on a compilation,
    // so a member test using the DEFAULT 30s commercial would assert the absence of a button that
    // is absent for everyone — passing whether or not the admin gate exists. The clip must be one
    // an ADMIN would see the button on, or this line tests nothing.
    stubFiller({
      me: MEMBER,
      clips: [clip({ era: 0, suggestedEra: 1985, tagged: false, isComposite: true, durationMs: 900_000 })],
    });
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");
    expect(screen.getByText("1985s?")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /confirm 1985/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /split into clips/i })).not.toBeInTheDocument();
  });

  // ⚠ Splitting a 30-second commercial is a full-decode search for adverts inside one advert:
  // minutes of GPU, contending with playout, to find nothing. The action was offered on every
  // card in the catalog (§10 V54 A8).
  it("does not offer splitting on a clip that is not a compilation", async () => {
    stubFiller({ clips: [clip()] }); // the default: a 30s commercial
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");
    expect(screen.queryByRole("button", { name: /split into clips/i })).not.toBeInTheDocument();
  });

  // The confirmation itself: the first click must ask, not act.
  it("asks before starting a split, and starts nothing if the operator backs out", async () => {
    const { splitCount } = stubFiller({ clips: [clip({ isComposite: true, durationMs: 900_000 })] });
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");

    await userEvent.click(screen.getByRole("button", { name: /split into clips/i }));

    // The dialog is up and it names the cost — an operator deciding needs to know it is minutes
    // of decoding, and that nothing is destroyed either way.
    expect(await screen.findByRole("heading", { name: /split .*into clips\?/i })).toBeInTheDocument();
    expect(screen.getByText(/several minutes/i)).toBeInTheDocument();
    expect(screen.getByText(/nothing enters the catalog yet/i)).toBeInTheDocument();

    // ⚠ THE assertion. Opening the dialog must not have fired the job — the whole defect was that
    // the first click already had.
    expect(splitCount()).toBe(0);

    await userEvent.click(screen.getByRole("button", { name: /cancel/i }));
    expect(splitCount()).toBe(0);
  });

  // The V34 handoff: POST returns a job id, the terminal filler_split frame carries the
  // proposal id, and the app navigates to the review gate, which reads the proposal back.
  it("starts split detection and navigates to the review on the success frame", async () => {
    // ⚠ A COMPILATION now, because the action is offered only on one (§10 V54 A8) — and the split
    // is confirmed rather than fired on the first click.
    const { splitCount } = stubFiller({ clips: [clip({ isComposite: true, durationMs: 900_000 })] });
    const router = renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");

    await userEvent.click(screen.getByRole("button", { name: /split into clips/i }));
    await userEvent.click(await screen.findByRole("button", { name: /find the clips/i }));
    // ⚠ `u.endsWith("/split")` was one trailing character away from `/v1/filler/splits/:id`, the
    // read that follows it. Two endpoints, one predicate.
    await expect.poll(splitCount, { message: "the action should POST the split job" }).toBe(1);
    // The status line now reads the clip's NAME (§10 V45a): `ClipDTO` carries no path any more,
    // and a content hash is meaningless to read in a sentence.
    expect(await screen.findByText(/detecting cuts in frosted flakes/i)).toBeInTheDocument();

    act(() => {
      fireFrame("filler_split", { jobId: "job-split-1", clipHash: "c1", status: "running" });
    });
    act(() => {
      fireFrame("filler_split", {
        jobId: "job-split-1",
        clipHash: "c1",
        status: "success",
        proposalId: "sp-1",
        segments: 1,
      });
    });

    expect(await screen.findByRole("heading", { name: /review split/i })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/filler/splits/sp-1");
  });

  it("surfaces the split job's terminal error instead of navigating", async () => {
    stubFiller({ clips: [clip({ isComposite: true, durationMs: 900_000 })] });
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");
    await userEvent.click(screen.getByRole("button", { name: /split into clips/i }));
    await userEvent.click(await screen.findByRole("button", { name: /find the clips/i }));

    act(() => {
      fireFrame("filler_split", {
        jobId: "job-split-1",
        clipHash: "c1",
        status: "error",
        error: "ffprobe found no streams",
      });
    });

    expect(await screen.findByText("ffprobe found no streams")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /review split/i })).not.toBeInTheDocument();
  });

  // --- bulk selection (V35) ---

  it("shows the bulk bar only once something is selected", async () => {
    stubFiller();
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");

    expect(screen.queryByText(/clip selected/i)).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("checkbox", { name: /select frosted flakes/i }));

    expect(await screen.findByText("1 clip selected")).toBeInTheDocument();
  });

  // ⚠ The copy is a promise. The server's action is a TOMBSTONE — the clip leaves the catalog
  // and stops being used in breaks, and the file stays where the operator put it. A label
  // saying "Delete" would describe something Loomarr deliberately does not do.
  it("offers removal in terms of the catalog, never the files", async () => {
    stubFiller();
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");
    await userEvent.click(screen.getByRole("checkbox", { name: /select frosted flakes/i }));

    const remove = await screen.findByRole("button", { name: /remove from catalog/i });
    expect(remove).toHaveAttribute("title", expect.stringMatching(/files stay/i));
    expect(screen.queryByRole("button", { name: /^delete/i })).not.toBeInTheDocument();
  });

  it("bulk-removes the selection through the bulk route", async () => {
    const { bulkRemoves } = stubFiller();
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");
    await userEvent.click(screen.getByRole("checkbox", { name: /select frosted flakes/i }));
    await userEvent.click(await screen.findByRole("button", { name: /remove from catalog/i }));

    // §10 V45a: the bulk endpoint takes HASHES (the wire identity `selected` is built from), matching
    // the single-clip PATCH. The backend resolves hash → path internally for the path-keyed tombstone.
    await expect.poll(() => bulkRemoves).toEqual([{ hashes: ["c1-hash"] }]);
  });

  // ⚠ Each dropdown sends ONLY its own field. A bar that posted all three would blank whatever
  // the operator had not touched — the failure the server's per-field optionality prevents, and
  // which only holds if the client sends a partial body.
  it("sends only the tag field the operator picked", async () => {
    const { bulkTags } = stubFiller();
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");
    await userEvent.click(screen.getByRole("checkbox", { name: /select frosted flakes/i }));

    // ⚠ "Set audience", not "Audience": the catalog's filter bar already owns that name, and
    // the first draft of this test found two comboboxes. The distinct label is the fix, and
    // this assertion is what keeps them distinct.
    await userEvent.click(await screen.findByRole("combobox", { name: "Set audience" }));
    await userEvent.click(await screen.findByRole("option", { name: "family" }));

    // §10 V45a: bulk edit is hash-keyed (see the bulk-remove test above).
    await expect.poll(() => bulkTags).toEqual([{ hashes: ["c1-hash"], audience: "family" }]);
  });

  // ⚠ **The processing queue's "Add tags" took a click and did nothing** (§10 V54). `ClipTagDialog`
  // was mounted only inside the catalog-tab branch, and the identifier handed up was the clip's
  // PATH where the shell resolves by hash — two independent reasons for the same silence, either
  // of which alone would have been enough.
  //
  // This drives the app the way an operator does — Incoming, click, look — rather than asserting
  // a callback fired. The tab's own test already asserts the callback, and it was green the whole
  // time the button did nothing; only rendering the whole page can tell the difference.
  it("opens the tag editor from Incoming, on the clip's real record", async () => {
    stubFiller({
      incoming: {
        clips: [
          {
            hash: "held-hash",
            path: "a3/f9/held.mp4",
            name: "Held promo",
            kind: "commercial",
            durationMs: 30_000,
            reason: "untagged",
            needsDecision: true,
          },
        ],
        total: 1,
      },
      // ⚠ Held, so it is NOT in the catalog list — which is the whole reason the shell needs a
      // second read with `includeHeld` to resolve it, and the reason `tags` must come from the
      // server rather than from the Incoming row (that DTO carries no tag array at all, so a
      // synthesised clip would offer to save an empty tag set over a tagged clip).
      held: [clip({ hash: "held-hash", name: "Held promo", category: "cereal", era: 1985 })],
    });
    renderAt("/filler/incoming");

    await userEvent.click(await screen.findByRole("button", { name: /add tags/i }));

    // The dialog labels its region with the clip's name, so finding it by name proves BOTH that
    // it opened and that it opened on the right record.
    expect(await screen.findByRole("region", { name: "Edit tags: Held promo" })).toBeInTheDocument();
  });

  it("re-runs classification without deleting the clip or upstream pipeline work", async () => {
    const { rewinds } = stubFiller({
      incoming: {
        clips: [
          {
            hash: "held-hash",
            path: "a3/f9/held.mp4",
            name: "Held promo",
            kind: "commercial",
            durationMs: 30_000,
            reason: "classification needs a decision",
            needsDecision: true,
            pipeline: {
              stage: "tag",
              status: "done",
              lifecycle: "needs_decision",
              attempts: 0,
              progress: 100,
              stages: [],
              updatedAt: "2026-08-15T12:00:00Z",
            },
          },
        ],
        total: 1,
      },
    });
    renderAt("/filler/incoming");

    await userEvent.click(await screen.findByText("More"));
    await userEvent.click(await screen.findByRole("button", { name: "Re-run AI" }));

    await expect.poll(() => rewinds).toEqual([{ hash: "held-hash", from: "tag" }]);
    expect(await screen.findByText("Clip queued again")).toBeInTheDocument();
  });

  // A member cannot bulk-edit, so the control that would 403 is simply absent rather than
  // present-and-failing.
  it("gives a member no way to select clips", async () => {
    stubFiller({ me: MEMBER });
    renderAt("/filler/library");
    await screen.findByText("Frosted Flakes");

    expect(screen.queryByRole("checkbox", { name: /select frosted flakes/i })).not.toBeInTheDocument();
  });
});
