import type { MeBody, TitleDTO } from "@loomarr/api";
import {
  getChannelGuideMockHandler,
  getDeleteChannelMockHandler,
  getEnqueueTitleMockHandler,
  getListChannelsMockHandler,
  getListTitlesMockHandler,
  getMeMockHandler,
  getSettingsListMockHandler,
} from "@loomarr/api/msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { routeTree } from "@/routeTree.gen";
import { channel } from "@/test/fixtures/channels";
import { setting } from "@/test/fixtures/settings";
import { me } from "@/test/fixtures/users";
import { appHandlers } from "@/test/msw/handlers";
import { server } from "@/test/msw/server";

// The Guide — the channels surface — through the REAL route tree.
//
// Was `channels-board.test.tsx` against `/channels`. That route folded into `/guide` (§12):
// one surface answers "what do I have" and "what is on", and it owns ORIGINATION. The
// assertions that survived the move are the ones about behavior rather than layout — the
// per-row actions menu, the inline "Add a channel" door, and the absence of any manual
// rebuild control (§9 self-maintaining). The card-list-shaped ones did not survive, because
// the cards did not.
const MEMBER: MeBody = me({ id: "u2", name: "Kid", role: "member", autoApprove: false });

// ⚠ Both of these were written inline as ten-field objects and were missing `lineup`, which
// ChannelDTO requires. `channel()` carries the full required set.
const CHANNELS = [
  channel({
    id: "ch-live",
    name: "Saturday Cartoons",
    number: 42,
    programCount: 10,
    breakCount: 4,
    slotCount: 14,
  }),
  channel({
    id: "ch-part",
    name: "Late Night",
    number: 43,
    programCount: 3,
    pendingCount: 7,
    slotCount: 10,
  }),
];

const TITLES: TitleDTO[] = [
  { key: "movie:tmdb:1", mediaType: "movie", state: "available", name: "Landed" },
  { key: "movie:tmdb:2", mediaType: "movie", state: "downloading", name: "Coming" },
  { key: "movie:tmdb:3", mediaType: "movie", state: "unavailable", name: "Gave Up", tmdbId: 3 },
];

// GET /v1/guide's shape, read off the generated GuideChannelTimeline/GuideAiring DTOs
// rather than remembered — the same rule the fixtures carry, and the one that caught two
// wrong proposal shapes in 13.4e.
const NOW = 1_700_000_000_000;
const GUIDE = {
  fromMs: NOW,
  toMs: NOW + 4 * 3_600_000,
  channels: [
    {
      channelId: "ch-live",
      name: "Saturday Cartoons",
      number: 42,
      status: "live" as const,
      pendingCount: 0,
      airings: [
        {
          kind: "program" as const,
          scheduleBlockId: "block_matrix",
          title: "The Matrix",
          startMs: NOW,
          stopMs: NOW + 7_200_000,
          runtimeMs: 7_200_000,
        },
      ],
    },
  ],
};

// ⚠ GET /v1/titles is a single-state FILTER and it 400s without a `state` param — mirroring
// `internal/api/titles.go`, so a caller that forgets the param fails HERE exactly as it would in
// production. Answering every titles request with the full set is what let the Board ship a
// param-less call that 400s live while the suite stayed green.
//
// This is the one case in the file that cannot be generated: the spec declares errors via
// `default:` (RFC 7807) with no explicit 400, so orval has no status to emit a handler for. The
// shape used is MSW's PASS-THROUGH — a resolver that returns nothing declines the request and the
// next matching handler takes it — so the 400 is a hand-written GUARD in front of the generated
// success path, rather than a hand-written replacement for it.
const titlesRequireState = () =>
  http.get("*/v1/titles", ({ request }) => {
    if (new URL(request.url).searchParams.get("state")) return undefined;
    return HttpResponse.json(
      { title: "Bad Request", detail: "state query param is required" },
      { status: 400 },
    );
  });

// `empty` serves a guide with NO channels, which is the fresh-install state the "Dead air"
// empty state and the hidden header door both hang off.
const stubGuide = (who: MeBody = me(), opts: { empty?: boolean } = {}) => {
  const enqueued: unknown[] = [];

  server.use(
    getMeMockHandler(who),
    getChannelGuideMockHandler(opts.empty ? { ...GUIDE, channels: [] } : GUIDE),
    getListChannelsMockHandler({ channels: opts.empty ? [] : CHANNELS }),
    titlesRequireState(),
    getListTitlesMockHandler(({ request }) => {
      const state = new URL(request.url).searchParams.get("state");
      return { titles: TITLES.filter((t) => t.state === state) };
    }),
    getEnqueueTitleMockHandler(async ({ request }) => {
      enqueued.push(await request.json());
      return { key: "movie:tmdb:3", mediaType: "movie", state: "wanted" };
    }),
    // tunarr.url set → the list's Rebuild button is enabled (it's gated on Tunarr being
    // connected, so a rebuild can't 501). ⚠ The old entry supplied four of SettingEntry's eight
    // required fields; `setting()` carries the rest.
    getSettingsListMockHandler({ features: {}, settings: [setting({ key: "tunarr.url" })] }),
    // Spread LAST — see handlers.ts: MSW takes the FIRST match and `use()` prepends.
    ...appHandlers(),
  );

  return { enqueued };
};

const renderAt = (path: string) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  // Returns the render RESULT so a test can scope its queries to its own tree. `screen`
  // searches the shared document.body, and a query that reaches a neighbouring test's markup
  // clicks a detached button, which silently does nothing.
  return render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
};

describe("Guide", () => {
  it("is headed 'Channels' and shows what's on, from the guide endpoint", async () => {
    stubGuide();
    renderAt("/guide");
    // The heading is "Channels", not "Guide": one surface, and the mock names it for the
    // objects it lists rather than the view it uses (§12).
    expect(await screen.findByRole("heading", { name: "Channels", level: 1 })).toBeInTheDocument();
    expect(await screen.findByText("Saturday Cartoons")).toBeInTheDocument();
    expect(await screen.findByText(/The Matrix/)).toBeInTheDocument();
  });

  it("keeps everyday time controls visible and precise view controls behind one disclosure", async () => {
    const user = userEvent.setup();
    stubGuide();
    const view = renderAt("/guide");

    // Wait for a guide row so the loading state cannot be mistaken for a closed disclosure.
    expect(await view.findByRole("button", { name: /actions for saturday cartoons/i })).toBeInTheDocument();
    expect(view.getByRole("button", { name: "NOW" })).toBeInTheDocument();
    expect(view.getByRole("button", { name: "Show 4 hours" })).toHaveAttribute("aria-pressed", "true");

    // Planning controls do not compete with the everyday toolbar until asked for.
    expect(view.queryByLabelText("Start hour")).not.toBeInTheDocument();
    expect(view.queryByRole("button", { name: "Zoom in" })).not.toBeInTheDocument();

    const viewTrigger = view.getByRole("button", { name: "View options" });
    expect(viewTrigger).toHaveAttribute("aria-expanded", "false");
    await user.click(viewTrigger);

    expect(view.getByLabelText("Start hour")).toBeInTheDocument();
    await user.click(view.getByRole("button", { name: "Zoom in" }));
    expect(viewTrigger).toHaveAccessibleName("View options, custom");

    // Closing the row keeps its non-default state legible on the trigger.
    await user.click(viewTrigger);
    expect(view.queryByLabelText("Start hour")).not.toBeInTheDocument();
    expect(view.getByRole("button", { name: "View options, custom" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
  });

  it("keeps the floating airing detail from blocking nearby guide rows", async () => {
    const user = userEvent.setup();
    stubGuide();
    renderAt("/guide");

    await user.hover(await screen.findByRole("button", { name: /The Matrix/ }));
    expect(await screen.findByTestId("guide-detail-card")).toBeInTheDocument();
    expect(screen.getByTestId("guide-detail-positioner")).toHaveClass("pointer-events-none");
  });

  it("has no manual rebuild/refresh — edits are seamless (§9) — and each row opens its channel", async () => {
    stubGuide();
    renderAt("/guide");

    // The row is present (its ⋮ actions menu is the stable, explicitly-labelled handle on
    // it — the channel button's accessible name is assembled from sibling spans and is a
    // rendering detail). Awaited because rows depend on the guide query resolving.
    expect(await screen.findByRole("button", { name: /actions for saturday cartoons/i })).toBeInTheDocument();

    // No manual "Rebuild"/"Refresh" buttons — a background reconcile + a `channel` SSE
    // frame keep the surface current on their own.
    expect(screen.queryByRole("button", { name: /rebuild/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /refresh/i })).not.toBeInTheDocument();
  });

  it("removes a deleted channel from the current Guide without a page refresh", async () => {
    const user = userEvent.setup();
    let deleted = false;
    stubGuide();
    server.use(
      getChannelGuideMockHandler(() => (deleted ? { ...GUIDE, channels: [] } : GUIDE)),
      getDeleteChannelMockHandler(() => {
        deleted = true;
        return undefined as never;
      }),
    );
    renderAt("/guide");

    await user.click(await screen.findByRole("button", { name: /actions for saturday cartoons/i }));
    await user.click(await screen.findByRole("menuitem", { name: /delete/i }));
    await user.click(screen.getByRole("button", { name: "Delete" }));

    expect(await screen.findByText("Dead air")).toBeInTheDocument();
    expect(screen.queryByText("Saturday Cartoons")).not.toBeInTheDocument();
  });

  it("owns origination: 'Add a channel' opens the describe panel in place", async () => {
    const user = userEvent.setup();
    stubGuide();
    renderAt("/guide");

    // Each row carries a ⋮ actions menu (pause/resume + delete) so removing a channel doesn't
    // require opening it — awaited because the rows depend on the guide query resolving.
    expect(await screen.findByRole("button", { name: /actions for saturday cartoons/i })).toBeInTheDocument();

    // THE origination door (§12), and the reason `/channels` could fold away: describing a
    // channel happens here, inline, rather than on a separate page.
    const add = screen.getByRole("button", { name: /add a channel/i });
    await user.click(add);
    expect(await screen.findByLabelText("Channel intent")).toBeInTheDocument();
  });

  // The fresh-install state. ⚠ Before this, the header's "Add a channel" and the empty
  // state's "Describe your first channel" were BOTH on screen, both calling the same
  // handler, opening the same panel — which then titled itself "Add a channel". Three
  // labels, one action, two of them visible at once.
  it("shows only ONE origination door on an empty guide, and names it the same as the header", async () => {
    stubGuide(me(), { empty: true });
    renderAt("/guide");

    expect(await screen.findByText("Dead air")).toBeInTheDocument();
    // Exactly one control offers the action, and it uses the header's own wording.
    const doors = screen.getAllByRole("button", { name: /add a channel/i });
    expect(doors).toHaveLength(1);
    // The old second label is gone entirely.
    expect(screen.queryByRole("button", { name: /describe your first channel/i })).not.toBeInTheDocument();
  });

  // ⚠ The dead end this avoids: the header button becomes "Close" once the panel is open,
  // and an empty guide is exactly when someone is most likely to have opened it. Hiding it
  // unconditionally would leave the panel with no way out.
  it("brings the header door back as Close once the panel is open on an empty guide", async () => {
    const user = userEvent.setup();
    stubGuide(me(), { empty: true });
    const view = renderAt("/guide");

    // Wait for the EMPTY STATE, not just the button. "Dead air" only renders once the guide
    // query has answered; before that `channels` is [] for a different reason (still loading)
    // and the header door is still on screen. Clicking that one opens the panel in a tree the
    // very next render replaces, so the Close never appears.
    await view.findByText("Dead air");
    await user.click(view.getByRole("button", { name: /add a channel/i }));

    // Scoped to THIS render rather than `screen`: document.body is shared across tests in
    // the file, and a query that reaches a neighbour's markup clicks a detached button, which
    // silently does nothing. The point of the assertion is only that the EXIT exists; the
    // panel's own contents are covered by the origination test above.
    expect(await view.findByRole("button", { name: /close/i })).toBeInTheDocument();
  });

  it("keeps the header door on a populated guide, where no empty state offers it", async () => {
    stubGuide();
    renderAt("/guide");

    expect(await screen.findByRole("button", { name: /actions for saturday cartoons/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /add a channel/i })).toBeInTheDocument();
    expect(screen.queryByText("Dead air")).not.toBeInTheDocument();
  });

  it("names the origination door for a member — they request rather than add", async () => {
    stubGuide(MEMBER);
    renderAt("/guide");
    // A member has no other way to ask for a channel now that /suggest is gone, so the
    // affordance must be present for them too — worded for what they are actually doing.
    expect(await screen.findByRole("button", { name: /request a channel/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /add a channel/i })).not.toBeInTheDocument();
  });

  it("opens the describe panel when the wizard hands off ?intent=", async () => {
    stubGuide();
    renderAt("/guide?intent=saturday-morning%20cartoons");
    // §13's blank-page killer: the handoff must land on a FILLED form, not a bare grid with
    // the operator wondering where their template went.
    const intent = await screen.findByLabelText("Channel intent");
    expect(intent).toHaveValue("saturday-morning cartoons");
  });
});

describe("Board", () => {
  it("leads with the journey, not a table of states", async () => {
    stubGuide();
    renderAt("/queue");
    // "1 of 3 have landed" — the member framing (§13).
    expect(await screen.findByText(/1 of 3 titles have landed/i)).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /on the way/i })).toBeInTheDocument();
  });

  it("offers a retry only for a title that gave up", async () => {
    const { enqueued } = stubGuide();
    renderAt("/queue");

    const retries = await screen.findAllByRole("button", { name: /try again/i });
    expect(retries).toHaveLength(1); // only the unavailable one
    await userEvent.click(retries[0] as HTMLElement);

    // Re-enqueued by identity, not by key — that is what the enqueue contract takes.
    // ⚠ The old assertion scanned `fetchMock.mock.calls` for a POST whose url contained
    // "/v1/titles"; landing in the handler bound to `POST /v1/titles` proves the route itself.
    await expect.poll(() => enqueued).toEqual([expect.objectContaining({ mediaType: "movie", tmdbId: 3 })]);
  });
});
