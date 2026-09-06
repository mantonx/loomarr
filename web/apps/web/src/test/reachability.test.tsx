import {
  getChannelFillerCoverageMockHandler,
  getChannelTracksMockHandler,
  getChannelUpcomingMockHandler,
  getDiscoverFillerMockHandler,
  getFillerDecisionActivityMockHandler,
  getFillerDecisionDiagnosticsMockHandler,
  getFillerDecisionReviewsMockHandler,
  getFillerIncomingMockHandler,
  getGetChannelMockHandler,
  getGetFillerSplitMockHandler,
  getGetPlayoutStatusMockHandler,
  getGetProgrammingVocabularyMockHandler,
  getImportCandidatesMockHandler,
  getListChannelsMockHandler,
  getListDiagnosticEventsMockHandler,
  getListDiscoveryFeedbackMockHandler,
  getListDocsMockHandler,
  getListFillerMockHandler,
  getListFillerPullsMockHandler,
  getListFillerSourcesMockHandler,
  getListLibraryCollectionsMockHandler,
  getListTaxonomyMockHandler,
  getListUserSessionsMockHandler,
  getListUsersMockHandler,
  getMeMockHandler,
  getPreviewChannelCycleMockHandler,
  getPreviewChannelPodsMockHandler,
  getPreviewDraftChannelPodsMockHandler,
  getSettingsListMockHandler,
  getSystemBackupsListMockHandler,
  getSystemDatabaseStatusMockHandler,
  getSystemRestartCostMockHandler,
  getSystemServicesMockHandler,
  getSystemVersionMockHandler,
} from "@loomarr/api/msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { routeTree } from "@/routeTree.gen";
import { channel } from "@/test/fixtures/channels";
import { setting } from "@/test/fixtures/settings";
import { me, user } from "@/test/fixtures/users";
import { appHandlers } from "@/test/msw/handlers";
import { server } from "@/test/msw/server";

// REACHABILITY — the gate this phase earned.
//
// Seven times in 13.4 something was built, unit-tested, and unreachable: two settings
// panels never mounted; a formatter never called, so "·til 8:00 PM" was dead UI on every
// channel card; a 323-line settings form rendered by nothing; a clip's tag action gated
// so the one clip that needed correcting couldn't be; a search scope that always returned
// empty; a Search button wired to a discarded setState. Every component test passed in
// every case, because a component test cannot see whether anything mounts it.
//
// So this asserts REACHABILITY rather than behavior: every route in the generated tree
// renders real content, and every feature-gated panel appears when its flag is on. The
// route list is derived from the router itself — a hand-maintained list is the same
// mistake one level up (see structure.test.ts, which learned this the hard way).

// `local: true` mirrors the meBody field (§11 credential path) — the Account screen
// offers a password form only for a Loomarr-stored credential, so a fixture without it
// would silently exercise the media-server branch instead. It is `me()`'s default.
const ADMIN = me();

// Every feature on, so gated panels are expected to appear rather than explain themselves.
const FEATURES = {
  filler: true,
  suggestions: true,
  acquisition: true,
  user_sync: true,
  ingest: true,
};

// At least one entry for every curated block — a block with no matching field renders no form.
// ⚠ Each of these was ten hand-written lines repeating the same eight
// required SettingEntry fields; `setting()` owns them now, so a new required field on the wire
// breaks one fixture instead of seven copies.
const SETTINGS = [
  setting({ key: "library.url", group: "connections.media_server", kind: "url", value: "http://emby:8096" }),
  setting({ key: "filler.dir", group: "filler", kind: "string", value: "/filler" }),
  setting({ key: "filler.pod_max", group: "filler", kind: "int", value: "4" }),
  setting({ key: "filler.breaks_per_hour", group: "filler", kind: "int", value: "4" }),
  setting({ key: "job.workers", group: "advanced", kind: "int", value: "2" }),
  setting({ key: "sched.window_hours", group: "channels", kind: "duration", value: "24h" }),
  setting({ key: "session.ttl", group: "users_security", kind: "duration", value: "720h" }),
  setting({ key: "llm.url", group: "ai", kind: "url", value: "http://ollama:11434" }),
];

// ⚠ THE STUB THIS REPLACES DOCUMENTED ITS OWN FAILURES, twice, in comments that both begin
// "Before the /v1/filler catalog match": the sources tab and the incoming queue each fell
// through to the CLIPS payload and rendered a wrong-shaped page that still passed. Its own
// words — "a stub that answers the wrong shape is indistinguishable from a working page until
// something depends on the shape" — are the argument for this whole migration, and the ordering
// discipline they demanded is gone now that MSW matches on the route rather than a substring.
//
// ⚠ It also carried `/v1/proposals` TWICE, the second branch unreachable. Nothing noticed,
// because nothing could.
const stubReachable = () => {
  server.use(
    getMeMockHandler(ADMIN),
    getSettingsListMockHandler({ features: FEATURES, settings: SETTINGS }),
    getSystemVersionMockHandler({ version: "dev", ready: true }),
    getListDiagnosticEventsMockHandler({ items: [], dropped: 0 }),
    getListDiscoveryFeedbackMockHandler([]),
    getImportCandidatesMockHandler({ candidates: [] }),
    getListUserSessionsMockHandler({ sessions: [] }),
    getListUsersMockHandler({ users: [user()] }),
    getListDocsMockHandler({ docs: [{ slug: "troubleshooting", title: "Troubleshooting" }] }),
    getGetFillerSplitMockHandler({
      id: "sp-1",
      clipHash: "comp-hash",
      createdAt: "2026-07-25T20:00:00Z",
      segments: [{ index: 0, startMs: 0, endMs: 30000, name: "First ad" }],
    }),
    getDiscoverFillerMockHandler({
      items: [],
      total: 0,
      licenceNote: "Licence information isn't available.",
    }),
    getListFillerSourcesMockHandler({
      // ⚠ V37: a flat list. `remote` was the CONTAINER row and is retired — a registered
      // archive collection is a peer carrying `searchable`, which is what the search
      // expander now keys on (the page no longer tests the kind itself).
      sources: [
        {
          id: "archive:classic_tv_commercials",
          enabled: true,
          autoAdmit: true,
          admissionControllable: true,
          switchable: true,
          removable: true,
          kind: "archive",
          target: "Classic TV Commercials",
          detail: "an archive.org collection — searchable here",
          count: 0,
          configured: true,
          fetchable: true,
          searchable: true,
        },
      ],
      total: 1,
    }),
    getFillerIncomingMockHandler({
      overview: {
        runnable: 0,
        inProgress: 0,
        scheduled: 0,
        needsDecision: 1,
        recoverable: 0,
        admitted: 1,
        rejected: 0,
        dismissed: 0,
      },
      // ⚠ `hash` is REQUIRED on an incoming clip — it is the content identity (§10, the
      // filler-path-identity rule), and every row action keys on it. Both of these fixtures
      // omitted it, so the queue rendered rows whose identity was undefined.
      //
      // ⚠ These were `asks` until §10 V51e made Incoming ONE conveyor: `asks` and `pipeline` were
      // separate arrays over overlapping populations (84 of 85 clips appeared in both on a fresh
      // scan), and they collapsed into `clips`, where `needsDecision` says which end a clip is at.
      // The old field name is now a BANNED identifier — see `scripts/check-retired.sh`.
      clips: [
        {
          hash: "held-hash",
          path: "held.mp4",
          name: "Unidentified toy spot",
          durationMs: 30000,
          kind: "commercial",
          reason: "Loomarr couldn't work out what this is, so it will only match broadly.",
          confidence: 45,
          // ⚠ `needsDecision: true` is the FAITHFUL translation of the old `asks` array, not a
          // detail to leave off. On one belt the flag is the only thing saying which end a clip is
          // at, and the panel counts exactly this (`clips.filter(c => c.needsDecision)`) — a clip
          // without it lands in the queue as already-handled, which is the opposite of an ask.
          needsDecision: true,
        },
      ],
      clipsTotal: 1,
      decisionsTotal: 1,
      reels: [],
      reelsTotal: 0,
      // V38's audit half — what was filed with nobody looking.
      recentlyFiled: [
        {
          hash: "auto-hash",
          path: "auto.mp4",
          name: "Hot Wheels spot",
          durationMs: 30000,
          kind: "commercial",
          reason: "Loomarr was confident enough about these tags to file it without asking.",
          confidence: 88,
          autoFiled: true,
        },
      ],
      recentlyFiledTotal: 1,
      rejected: [],
      rejectedTotal: 0,
      stageOrder: [],
      total: 1,
    }),
    getFillerDecisionReviewsMockHandler({
      rows: [
        {
          id: "decision-review",
          clipHash: "held-hash",
          applicationMode: "shadow",
          createdAt: "2026-08-25T12:00:00Z",
          question: "Is this a toy commercial?",
          reasonCodes: ["unidentified"],
          evidenceRefs: ["frame-1"],
          conflicts: [],
        },
      ],
      total: 1,
    }),
    getFillerDecisionActivityMockHandler({
      rows: [
        {
          id: "automatic-admit-event",
          decisionId: "automatic-admit-decision",
          clipHash: "auto-hash",
          kind: "automatic_admit",
          applicationMode: "shadow",
          createdAt: "2026-08-25T12:00:00Z",
        },
      ],
      total: 1,
    }),
    getFillerDecisionDiagnosticsMockHandler({ rows: [], total: 0 }),
    // Both catalog shapes, not an empty catalog: Split belongs to the composite source reel,
    // while pinning belongs to an airable clip. A composite is deliberately excluded from pods,
    // so using one row to assert both doors would require the UI to offer an action that cannot
    // have an effect.
    getListFillerMockHandler({
      total: 2,
      clips: [
        {
          hash: "hash-comp",
          name: "80s compilation",
          kind: "commercial",
          durationMs: 900000,
          // ⚠ The fixture was named "80s compilation" and ran 15 minutes but never set this, so
          // the DATA did not claim to be what the name said. Since §10 V54 A8 the split action
          // renders only on a compilation, and without the flag this row asserts the entry point
          // mounts on a clip it is deliberately not offered for.
          isComposite: true,
          tagged: false,
          aiTagged: false,
          playCount: 0,
          playsCounted: true,
        },
        {
          hash: "hash-ad",
          name: "80s cereal advert",
          kind: "commercial",
          durationMs: 30000,
          isComposite: false,
          tagged: true,
          aiTagged: false,
          playCount: 0,
          playsCounted: true,
        },
      ],
    }),
    // ⚠ `GET /v1/channels/:id/pods` — orval names it `getPreviewChannelPods…` after its operation
    // id, which is NOT the URL. The old stub matched `u.includes("/pods")`, which is also true of
    // `/pods/preview`, the POST one route over.
    getPreviewChannelPodsMockHandler({ entries: [], totalMs: 0, matchLevel: "exact" }),
    getGetChannelMockHandler(
      channel({ id: "ch-1", name: "Cartoons", number: 42, programCount: 3, pendingCount: 1, slotCount: 4 }),
    ),
    getListChannelsMockHandler({
      channels: [
        channel({ id: "ch-1", name: "Cartoons", number: 42, programCount: 3, pendingCount: 1, slotCount: 4 }),
      ],
    }),
    // ⚠ THE THIRTEEN THE CATCH-ALL WAS ANSWERING WITH `{}`. This suite mounts EVERY route in the
    // generated tree, so it touches more of the API than any other file — and its whole purpose is
    // to prove a screen renders REAL CONTENT. Each of these was reaching `json({})`, which means
    // thirteen screens were being asserted "reachable" against an empty object: `channels` for the
    // playout status page, `rows` for services, `tables` for the database page, `backups` for
    // backup, `what/when/how` for the programming vocabulary. Every one of those is a required
    // array the page maps over.
    //
    // They stay HERE rather than in `appHandlers()`: the baseline is the common surface, and these
    // are per-screen reads that only a suite mounting all routes at once needs.
    getPreviewChannelCycleMockHandler({
      at: "2026-07-25T20:00:00Z",
      windowMs: 3_600_000,
      slots: [],
      activeRule: { id: "r-1", label: "Default", matched: true, priority: 0 },
      excluded: { overCeiling: 0, unrated: 0, items: [] },
      trace: {
        version: 1,
        ordering: "sequential",
        seed: "0",
        windowMs: 3_600_000,
        windowIndex: 0,
        relaxations: [],
        factTotal: 0,
        recordedTotal: 0,
        truncated: false,
        facts: [],
      },
    }),
    getChannelFillerCoverageMockHandler({
      level: "exact",
      total: 4,
      rungs: [{ level: "exact", clips: 4 }],
      // The per-setting breakdown (V51f). Nothing at zero: this suite is about REACHING screens,
      // not diagnosing a catalog, so the meter should render its ordinary healthy shape.
      criteria: [
        { criterion: "era", clips: 4 },
        { criterion: "audience", clips: 4 },
        { criterion: "category", clips: 4 },
        { criterion: "kind", clips: 4 },
        { criterion: "duration", clips: 4 },
        { criterion: "quality", clips: 4 },
      ],
    }),
    getChannelUpcomingMockHandler({ upcoming: [] }),
    getListFillerPullsMockHandler({ pulls: [], total: 0 }),
    getListLibraryCollectionsMockHandler({ collections: [] }),
    getGetPlayoutStatusMockHandler({
      running: false,
      channels: [],
      gpu: { contended: false },
      prepared: {
        available: false,
        running: false,
        channels: 0,
        readyChannels: 0,
        scheduledBindings: 0,
        readyBindings: 0,
        missingBindings: 0,
        queuedPublications: 0,
        remainingBytes: 0,
        budgetBytes: 0,
        protectedBytes: 0,
      },
    }),
    getGetProgrammingVocabularyMockHandler({ what: [], when: [], how: [] }),
    getSystemBackupsListMockHandler({
      backups: [],
      dir: "/backups",
      retain: 7,
      schedule: "0 30 3 * * *",
      supported: true,
    }),
    getSystemDatabaseStatusMockHandler({
      backend: "sqlite",
      canMigrate: false,
      parity: "n/a",
      phase: "idle",
      tables: [],
    }),
    getSystemRestartCostMockHandler({
      available: true,
      restartRequired: false,
      streamingChannels: 0,
    }),
    getSystemServicesMockHandler({
      loomarr: { name: "Loomarr", ok: true },
      rows: [],
    }),
    getListTaxonomyMockHandler({
      taxa: [],
      totalClips: 0,
      taggedClips: 0,
      unclassifiedClips: 0,
      axisCoverage: [],
    }),
    getPreviewDraftChannelPodsMockHandler({
      entries: [],
      totalMs: 0,
      matchLevel: "exact",
      // The draft preview answers with its own coverage since V51f — same selection, one response.
      coverage: { level: "exact", total: 0, rungs: [], criteria: [] },
    }),
    // The watch route's audio/subtitle picker (V46) — channel-level tracks, media-derived.
    getChannelTracksMockHandler({ audio: [], subtitles: [] }),
    ...appHandlers(),
  );
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
    </QueryClientProvider>,
  );
  // Returned so a test can assert WHERE the router settled — a redirect (the bare channel route
  // lands on Watch, §9.1 V54) is otherwise invisible to a content-only assertion. Additive: every
  // existing caller ignores it.
  return router;
};

// ⚠ `href`, never pathname + search. `location.search` is a PARSED OBJECT here, so concatenating
// it throws "Cannot convert object to primitive value" — a failure that reads like an app bug and
// was once blamed on one for five tests running (see app-router.test.tsx).
const at = (router: ReturnType<typeof renderAt>) => router.state.location.href;

// Turn a generated route id into a navigable path: strip pathless layout segments
// (`_authed`), drop the trailing index marker, and fill params with a value the stub
// serves. Deriving these from the router means a NEW route is covered the day it lands.
const pathOf = (id: string): string => {
  const path = id
    .split("/")
    .filter((seg) => seg !== "" && !seg.startsWith("_"))
    .map((seg) => (seg.startsWith("$") ? "ch-1" : seg))
    .join("/");
  return `/${path}`;
};

const routeIds = (): string[] => {
  const queryClient = new QueryClient();
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return Object.keys(router.routesById).filter(
    (id) =>
      id !== "__root__" &&
      // Layout routes render only an <Outlet>; their children are covered individually.
      id !== "/_authed" &&
      id !== "/_authed/settings" &&
      // The catch-all is SUPPOSED to be a placeholder — it is the 404 page.
      id !== "/_authed/$" &&
      // Login and the wizard redirect an authenticated admin away; they have their own
      // suites (auth + wizard) that drive them unauthenticated.
      id !== "/login" &&
      id !== "/wizard",
  );
};

describe("every route is reachable", () => {
  it.each(routeIds().map((id) => [id, pathOf(id)] as const))("%s renders real content", async (_id, path) => {
    stubReachable();
    renderAt(path);

    // A heading proves the screen composed, not just that the shell painted around an
    // empty pane. The shell's own nav has no headings, so this cannot pass vacuously.
    await waitFor(
      () => {
        const headings = screen.getAllByRole("heading");
        expect(headings.length).toBeGreaterThan(0);
      },
      { timeout: 3000 },
    );

    // The catch-all's copy appearing anywhere else means the path did not match the
    // route we think it did.
    expect(screen.queryByText("Off the air")).not.toBeInTheDocument();
  });
});

describe("feature-gated panels mount when their flag is on", () => {
  // Each entry names a panel that EXISTS as a component but has, at least once in this
  // phase, not been rendered by the page that owns it.
  it.each([
    ["/settings/ai", /probing your llm host|model|provider/i, "the §8.1 model picker"],
    ["/settings/security", /api token|playback token/i, "the generated-secrets panel"],
    // ⚠ The SSO block AND the note stating what SSO does not do (§11, V8). The note is the
    // part most likely to be lost in a tidy-up — it looks like prose rather than a control —
    // and losing it leaves §11's unusual model (most apps DO auto-create) reading as an
    // oversight.
    ["/settings/security", /does not create an account here/i, "the SSO scope note"],
    ["/people", /import from emby\/jellyfin/i, "the §11 import dialog trigger"],
    // ⚠ The ingest panel moved to INCOMING (V35). Discover was retired as a tab — finding
    // clips is now something you do to a source — and the download tooling went with the tab
    // that is about how clips ARRIVE. Keeping this pointed at a real URL is what stops a tab
    // nobody can navigate to from passing as "reachable".
    //
    // ⚠ The ingest panel's assertion was here and is DELETED with it (V38b, retired-ok). This
    // suite guards that a built thing is REACHABLE; a row for a deleted surface asserts the
    // opposite of what it looks like, and would have to be satisfied by re-adding the panel.
    //
    // What replaced it is the Sources row below — "add a source" is now the door clips come
    // through, and it is asserted there.
    // V35: the queue of clips waiting on a human decision. Same reason as the ingest panel —
    // this suite exists because eight things were built, unit-tested and imported by nothing.
    ["/filler/incoming", /is this a toy commercial/i, "the semantic decision queue"],
    // V63's durable audit half. A shadow admission decision is normal work, so it belongs in Manage
    // activity rather than in the exception queue. Keeping this route-level assertion prevents
    // unattended decisions from becoming invisible when the old Incoming surface is retired.
    ["/filler/manage", /would admit \(shadow\)/i, "the shadow decision activity"],
    // ⚠ And the tab itself must be reachable FROM the catalog, or the assertions above only
    // prove a deep link works. This is the V1/V17a/V23 failure in tab form.
    ["/filler", /^incoming/i, "the incoming workbench's own entry point"],
    // V35: catalog health is a strip above the tabs rather than a tab of its own, so it has
    // no nav entry to assert — it must simply BE on the page, on every tab.
    ["/filler/library", /fits a break/i, "the pool-health strip"],
    // V34: the split review route exists, but if no card offers the entry point the
    // operator can never reach it. The action lives on each clip card (admin).
    ["/filler/library", /split into clips/i, "the compilation-split entry point"],
    // V35 item 1.7: the per-channel override picker. Its entry point is on each clip card
    // (admin) — the picker itself is behind a click, so this asserts the DOOR, which is the
    // half that has gone missing eight times before.
    ["/filler/library", /use in a channel/i, "the channel-override entry point"],
    // V35: per-source search, on the Sources tab. ⚠ `GET /v1/filler/discover` was API-ONLY for
    // a whole phase — the route shipped, `DiscoverPanel` was deleted rather than left orphaned,
    // and nothing called it. This is the assertion that stops it going back to that state.
    //
    // ⚠ V35b moved the search INSIDE the archive source row, behind a "Search it" toggle, and
    // this assertion correctly went red when the old "Find clips" heading disappeared. It now
    // names the new DOOR. It deliberately does not reach past the toggle: what this suite
    // guards is that an entry point exists, and asserting on something behind the click would
    // pass just as happily with no way in — the exact state it was written to catch. The
    // panel's own contents are covered by the expander test below.
    ["/filler/sources", /search it/i, "the per-source search's entry point"],
    // V37: adding a source. ⚠ `POST /v1/filler/sources` shipped in V35 and NOTHING called it for
    // two phases — the route existed, the store wrote, and there was no way to reach it from the
    // app. That is the same API-only state this suite caught for `discover`, and the reason
    // YouTube could not be registered was partly that no UI ever tried.
    ["/filler/sources", /add a source/i, "the add-a-source form"],
  ])("%s mounts %s", async (path, pattern) => {
    stubReachable();
    renderAt(path);
    // findAllBy, not findBy: a panel legitimately renders its name more than once (a
    // heading plus a row label). Presence is the assertion here, not uniqueness.
    const found = await screen.findAllByText(pattern, undefined, { timeout: 3000 });
    expect(found.length).toBeGreaterThan(0);
  });

  // V35b: the per-source search moved INSIDE the archive row, behind a "Search it" toggle. The
  // row above proves the door exists; this proves it OPENS onto the real panel.
  //
  // ⚠ The assertion is the search's downloads-nothing promise, not merely the input. That line
  // is a behaviour claim — it is why an operator dares to browse a collection at all — and it
  // previously sat in a card that was always mounted. Behind a click it needs a click to guard,
  // or the promise could silently stop rendering with every test still green.
  it("/filler/sources opens the archive row's search onto its downloads-nothing promise", async () => {
    stubReachable();
    renderAt("/filler/sources");
    // ⚠ By the SOURCE's name, not the button's visible "Search it" (§10 V54 B6). The accessible
    // name now carries the target, because the provider roll-up puts several collections on
    // screen at once and five buttons all reading "Search it" are indistinguishable to anyone
    // not looking at the row they sit in. The row-level assertion above still matches the visible
    // text, which is deliberately unchanged.
    await userEvent.click(await screen.findByRole("button", { name: /^search Classic TV Commercials$/i }));
    const found = await screen.findAllByText(/nothing downloads until you queue it/i, undefined, {
      timeout: 3000,
    });
    expect(found.length).toBeGreaterThan(0);
  });

  // The §12 pod preview lives in the channel-detail "Filler" tab (admin-only) — the detail page
  // is now a tabbed layout, one section shown at a time. This guards the tab is WIRED +
  // reachable: the Filler tab is present, and selecting it reveals the live draft-sandbox break.
  it("/channels/ch-1 reaches the §12 filler section (its tab) with the break preview", async () => {
    stubReachable();
    // ⚠ Rendered AT the section's own URL rather than clicking through from `/channels/ch-1`.
    // The section bar moved onto `NavTabs` (V-nav-paths), so each section is a real route — and
    // a deep link is the stronger reachability claim: it proves the URL an operator can bookmark
    // or be sent actually mounts the panel, not merely that a click within one page swaps it.
    // The tab's own presence is asserted separately below.
    renderAt("/channels/ch-1/filler");
    const found = await screen.findAllByText(/preview break/i, undefined, { timeout: 3000 });
    expect(found.length).toBeGreaterThan(0);
  });

  // ⚠ And the section must be reachable FROM the channel page, or the deep-link test above only
  // proves a URL works for someone who already knows it. This is the V1/V17a/V23 failure in tab
  // form — a panel that exists at an address nobody can navigate to.
  it("/channels/ch-1 offers the Filler section as a link", async () => {
    stubReachable();
    renderAt("/channels/ch-1");
    // ⚠ Scoped to the section bar by NAME, because the app SIDEBAR also has a "Filler" link
    // (to `/filler`, the catalog) and it wins a bare `findByRole("link", { name: "Filler" })`.
    // Two different destinations sharing one accessible name is fine for a sighted user — the
    // bars are far apart — but a test has to say which one it means.
    const sectionBar = await screen.findByRole("navigation", { name: /channel sections/i });
    const tab = within(sectionBar).getByRole("link", { name: "Filler" });
    // A real destination, not a button that swaps state: middle-clickable, copyable, bookmarkable.
    expect(tab).toHaveAttribute("href", expect.stringContaining("/channels/ch-1/filler"));
  });

  // V29b's meter, in the same tab. Guarded here rather than trusted because this suite exists
  // for exactly this shape of miss: the component has stories, six unit tests and a Go test
  // proving it agrees with pod assembly, and every one of those passes whether or not anything
  // renders it. Only a route test answers "can an operator see it".
  it("/channels/ch-1 reaches the filler coverage meter", async () => {
    stubReachable();
    renderAt("/channels/ch-1/filler");
    expect(
      await screen.findByText(/saved channel coverage/i, undefined, { timeout: 3000 }),
    ).toBeInTheDocument();
    // And the meter itself rendered, not just its heading.
    expect(await screen.findByText("Exact match")).toBeInTheDocument();
  });

  // The eighth instance of this file's founding bug: ChannelIconField shipped complete —
  // stories, five visual baselines, an admin gate — and was imported by nothing, so the
  // channel icon was unreachable in the app. Its component tests all passed, which is
  // exactly the blind spot this suite exists for.
  it("/channels/ch-1 reaches the channel icon field on the info panel", async () => {
    stubReachable();
    // ⚠ The INFO path explicitly. The bare `/channels/ch-1` used to land here; since §9.1 V54 it
    // redirects to Watch, so pointing this at the bare route would silently stop testing the icon
    // field — it would assert against a player instead, which is how a reachability test quietly
    // stops covering the thing it is named for. The bare route's own landing is asserted below.
    renderAt("/channels/ch-1/info");
    expect(await screen.findByText("Channel icon")).toBeInTheDocument();
  });

  // ⚠ Opening a channel lands on WATCH (§9.1 V54) — the redirect the bare route performs. Asserted
  // because the default landing section is a routing DECISION: without this, flipping it back (or
  // to any other section) breaks nothing, and the guide's channel rows all go through this hop.
  it("/channels/ch-1 lands on Watch", async () => {
    stubReachable();
    const router = renderAt("/channels/ch-1");
    await screen.findByRole("heading", { name: /Watch/i });
    await waitFor(() => expect(at(router)).toContain("/channels/ch-1/watch"));
  });

  // The ninth instance, and the one this suite failed to prevent: V7 shipped
  // POST /v1/auth/password with 19 tests and no screen, so a user still could not
  // change their password by clicking anything. A route test is the gate — the
  // endpoint being correct was never the question.
  it("/account reaches the change-password form for a local user", async () => {
    stubReachable();
    renderAt("/account");
    expect(await screen.findByText("Your account")).toBeInTheDocument();
    expect(await screen.findByLabelText("Current password")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /change password/i })).toBeInTheDocument();
  });
});
