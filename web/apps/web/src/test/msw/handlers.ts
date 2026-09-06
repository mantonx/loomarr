import {
  getChannelGuideMockHandler,
  getChannelPlayUrlMockHandler,
  getChannelTimelineMockHandler,
  getChannelTracksMockHandler,
  getDeviceListMockHandler,
  getFillerDecisionActivityMockHandler,
  getFillerDecisionDiagnosticsMockHandler,
  getFillerDecisionOverviewMockHandler,
  getFillerDecisionReviewsMockHandler,
  getFillerIncomingMockHandler,
  getFillerPoolMockHandler,
  getFillerReadinessMockHandler,
  getFillerWatchMockHandler,
  getGetChannelMockHandler,
  getGetCurrentHealthMockHandler,
  getJobsListMockHandler,
  getListActivityMockHandler,
  getListChannelsMockHandler,
  getListDocsMockHandler,
  getListFillerMockHandler,
  getListFillerSourcesMockHandler,
  getListInvitationsMockHandler,
  getListProposalJobsMockHandler,
  getListProposalsMockHandler,
  getListStartupReportsMockHandler,
  getListTitlesMockHandler,
  getListUsersMockHandler,
  getNotificationProvidersListMockHandler,
  getNotificationProviderTypesListMockHandler,
  getSettingsListMockHandler,
  getSetupStateMockHandler,
  getSetupStatusMockHandler,
  getSystemEncryptionStatusMockHandler,
  getSystemLlmDiscoverMockHandler,
  getSystemLlmStatusMockHandler,
  getSystemVersionMockHandler,
} from "@loomarr/api/msw";
import type { RequestHandler } from "msw";
import { channel } from "../fixtures/channels";

// appHandlers — the shared baseline for tests that mount the REAL route tree.
//
// ⚠ This exists because route-level tests fetch whatever the landed screen needs, and that is not
// a list anyone can hold in their head. Before V53e each of them answered the whole surface with a
// catch-all (`json({}, 200)` / `json({})`), so every screen rendered against empty objects — and
// the tests passed, because an empty object is a perfectly good answer to a question nobody
// checked. `test/app-router`, `test/reachability`, `test/settings`, `test/wizard-router`,
// `test/filler`, `test/guide-page`, `test/help` and `test/users` all did this.
//
// ⚠ EVERY response here is EMPTY-BUT-VALID, and that is deliberate: these are the "this screen
// renders at all" reads, not the data any assertion depends on. A test that cares about content
// overrides the one endpoint it asserts on and leaves the rest of the baseline alone.
//
// ⚠⚠ SPREAD IT LAST — `server.use(myOverride, ...appHandlers())`, NOT the other way round.
// MSW resolves the FIRST matching handler in its list, and `server.use()` PREPENDS its arguments
// to that list. So "the most recently registered handler wins" is true across separate `use()`
// CALLS and exactly backwards WITHIN one: spread the baseline first and its empty response sits
// in front of the override, which then never fires.
//
// This is not theoretical and it is not loud. `test/help` overrode `/v1/docs` with two pages,
// got `{ docs: [] }` from the baseline instead, and failed as five 5-SECOND TIMEOUTS with no
// unhandled-request error — the guard cannot help here, because the request WAS handled, just by
// the wrong handler. Silent-empty is the precise failure mode this whole layer exists to remove,
// so the ordering is load-bearing. `test/app-router` never hit it only because everything it adds
// (`/v1/auth/me`, login) is absent from the list below.
//
// ⚠ It is a HAND-MAINTAINED LIST, which is the drift class this repo tracks in three other places.
// It cannot be generated, because "which endpoints does the app fetch on route X" is a property of
// the components, not the spec. What keeps it honest is the unhandled-request guard in `./server`:
// add a screen that fetches something new and the test fails BY NAME rather than silently getting
// an empty object. That is the opposite failure mode from the catch-all this replaces — loud and
// specific, instead of silent and plausible.
const appHandlers = (): RequestHandler[] => [
  // ⚠ `/v1/setup/state` is fetched by the ROOT route on every render — the first thing the app
  // asks and the one the old catch-all most reliably answered with `{}`, which reads as
  // "not bootstrapped, no SSO, no dev login" whether or not that was true.
  getSetupStateMockHandler({ bootstrapped: true, devLogin: false, sso: false }),
  // AppShell reads this once for every authenticated route. `dev` is the honest baseline for a
  // test bundle; tests that exercise release/dirty presentation override the generated handler.
  getSystemVersionMockHandler({ version: "dev", ready: true }),
  getGetCurrentHealthMockHandler({
    generationId: "startup-test",
    generation: 1,
    version: "dev",
    processStartedAt: 1,
    generationStartedAt: 1,
    updatedAt: 2,
    state: "healthy",
    checks: [],
  }),
  getListStartupReportsMockHandler({
    current: {
      id: "startup-test",
      generation: 1,
      version: "dev",
      processStartedAt: 1,
      generationStartedAt: 1,
      generationEndedAt: 2,
      durationMillis: 1,
      state: "ready",
      checks: [],
    },
    items: [],
  }),
  getSetupStatusMockHandler({ checks: [] }),
  // Settings → Security lists the operator's paired devices on mount (§11, Shield P1). Empty is
  // the honest default: a test that cares about devices overrides this, and every other test that
  // merely renders the page gets the "no devices yet" state rather than an unhandled request.
  getDeviceListMockHandler({ devices: [] }),
  getSystemEncryptionStatusMockHandler({
    enabled: true,
    installationKeyFingerprint: "sha256:test-installation-key",
    dataKeyCount: 1,
  }),
  getListChannelsMockHandler({ channels: [] }),
  // A single channel read &mdash; the channel-detail routes fetch this by id.
  getGetChannelMockHandler(channel()),
  // ⚠ The WATCH surface's three reads, and they belong in the SHARED set because opening a channel
  // now lands on Watch and tunes in on mount (§9.1 V54). Every route-level test that renders a
  // channel therefore mounts the player, so leaving these per-test would make the shared set
  // incomplete for the app's default landing screen — an unhandled request is a loud MSW error,
  // which is the failure this file's own note prefers over a silent catch-all.
  //
  // Deliberately empty/inert payloads: these exist so the player MOUNTS without an unhandled
  // request, not to make it play. A test that cares about tracks or the timeline stubs its own.
  getChannelTracksMockHandler({ audio: [], subtitles: [] }),
  getChannelTimelineMockHandler({ serverNowMs: Date.UTC(2026, 0, 1), airings: [] }),
  getChannelPlayUrlMockHandler({ url: "", relativeUrl: "", expiresAt: "2026-01-01T00:00:00Z" }),
  getListTitlesMockHandler({ titles: [] }),
  getListProposalJobsMockHandler({ journeys: [] }),
  getListProposalsMockHandler({ proposals: [] }),
  getListUsersMockHandler({ users: [] }),
  getListInvitationsMockHandler({ invitations: [] }),
  getSettingsListMockHandler({ settings: [], features: {} }),
  getNotificationProviderTypesListMockHandler({ providers: [] }),
  getNotificationProvidersListMockHandler({ providers: [] }),
  // ⚠ `total` is REQUIRED since §10 V51d added paging. This line and that field arrived in two
  // different PRs (#214 added the handler, #203 added the field); each was green against the main
  // it branched from, and together they did not typecheck — main went red on the second merge.
  // The generated client is the coupling, and neither diff mentions the other's file.
  getListFillerMockHandler({ clips: [], total: 0 }),
  getListFillerSourcesMockHandler({ sources: [], total: 0 }),
  // ⚠ `clips` is the whole conveyor (§10 V51e) — being-prepared and needs-a-decision in ONE list,
  // where this used to carry `asks` and `pipeline` as separate arrays over overlapping populations.
  getFillerIncomingMockHandler({
    overview: {
      runnable: 0,
      inProgress: 0,
      scheduled: 0,
      needsDecision: 0,
      recoverable: 0,
      admitted: 0,
      rejected: 0,
      dismissed: 0,
    },
    clips: [],
    clipsTotal: 0,
    decisionsTotal: 0,
    reels: [],
    reelsTotal: 0,
    recentlyFiled: [],
    recentlyFiledTotal: 0,
    rejected: [],
    rejectedTotal: 0,
    stageOrder: [],
    total: 0,
  }),
  // The guide grid's window read. `fromMs`/`toMs` are required, so an empty grid still has to
  // carry a coherent window rather than `{}`.
  getChannelGuideMockHandler({ channels: [], fromMs: 0, toMs: 0 }),
  getJobsListMockHandler({ jobs: [] }),
  getListActivityMockHandler({ activity: [] }),
  // The AI connection block reads both of these on mount, and it renders on the wizard's
  // connections step AS WELL AS on Settings → AI — so they are common surface, not one screen's
  // detail. ⚠ `local` and `reachable` are REQUIRED on the status body; a stub answering `{}`
  // (which is what every catch-all did) reads as neither, which is a coherent-looking lie.
  getSystemLlmStatusMockHandler({
    local: true,
    reachable: false,
    provider: "ollama",
    model: "",
    catalog: [],
    hosted: [],
  }),
  getSystemLlmDiscoverMockHandler({ models: [], sourceOk: true }),
  getListDocsMockHandler({ docs: [] }),
  getFillerPoolMockHandler({ channels: [], clips: 0, commercials: 0, eligible: 0, untagged: 0 }),
  getFillerWatchMockHandler({ clips: 0, health: "healthy", held: 0, sourcesOn: 0, sourcesTotal: 0 }),
  getFillerReadinessMockHandler({
    ready: true,
    nextAction: "none",
    repairs: { count: 0 },
    fetch: { enabled: true, catalogClips: 0 },
    pipeline: {
      runnable: 0,
      inProgress: 0,
      scheduled: 0,
      needsDecision: 0,
      recoverable: 0,
      admitted: 0,
      rejected: 0,
      dismissed: 0,
    },
    pool: { channels: [], clips: 0, commercials: 0, eligible: 0, untagged: 0 },
    acquisitions: [],
  }),
  getFillerDecisionOverviewMockHandler({
    healthy: true,
    nextAction: "none",
    counts: { admitted: 0, rejected: 0, reviews: 0, unresolvedReviews: 0, operational: 0, retryable: 0 },
  }),
  getFillerDecisionReviewsMockHandler({ rows: [], total: 0 }),
  getFillerDecisionActivityMockHandler({ rows: [], total: 0 }),
  getFillerDecisionDiagnosticsMockHandler({ rows: [], total: 0 }),
];

export { appHandlers };
