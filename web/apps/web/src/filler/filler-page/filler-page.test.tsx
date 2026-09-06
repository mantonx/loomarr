import type { ClipDTO } from "@loomarr/api";
import {
  getFillerDecisionReviewsMockHandler,
  getFillerPoolMockHandler,
  getFillerWatchMockHandler,
  getListFillerMockHandler,
  getListFillerSourcesMockHandler,
  getMeMockHandler,
  getSettingsListMockHandler,
} from "@loomarr/api/msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { me } from "@/test/fixtures/users";
import { server } from "@/test/msw/server";
import { RouterHarness } from "@/test/story-utils";
import { FillerPage } from "./filler-page";

// The SHELL's own contract, as distinct from the tabs it hosts.
//
// ⚠ `src/test/filler.test.tsx` already drives this page hard (18 tests: search, the bulk bar,
// split, tag cycling, member gating) and it stays the suite for the Catalog tab's BEHAVIOUR.
// What it does not pin is what the shell owes every tab regardless of which one is showing —
// the two badges and the pool strip — which is exactly what a tab-by-tab extraction can break
// without moving a single snapshot. That gap is what this file covers; duplicating the header
// assertions here would only make two places to update.
//
// ⚠ The counts in the fixtures deliberately DISAGREE: the clip list has 2 items while the watch
// endpoint reports 200. The stable Library badge must follow that server-owned catalog count,
// not the route-local result page — equal fixtures could not tell those two rules apart, the same
// collapsed-fixture trap that hid the clip-identity bug in the store.

const clip = (hash: string, name: string): ClipDTO => ({
  hash,
  name,
  kind: "commercial",
  era: 1990,
  audience: "kids",
  category: "toys",
  durationMs: 30_000,
  source: "folder",
  playCount: 0,
  playsCounted: true,
  aiTagged: false,
  tagged: true,
  suggestedEra: 0,
});

// ⚠ `catalogQueries` records the URL of every `GET /v1/filler` and ONLY those. The old `calls`
// array recorded EVERY request at any path, and the paging assertions then filtered it with
// `u.includes("offset=60")` — a query param that could have arrived on any endpoint — or with
// `u.includes("/v1/filler?")`, which is a substring of every other filler route's URL too.
//
// ⚠ The `/v1/filler/pool` branch sent `{ total, untagged, channels }`. `FillerPoolOutputBody` has
// no `total` at all, and REQUIRES `clips`, `commercials` and `eligible` — so the pool response was
// simultaneously carrying a field the API never sends and missing three it always does.
//
// ⚠ The `me` fixture omitted `local`, which MeBody requires.
const stubFillerPage = (over: { clips?: ClipDTO[]; incomingTotal?: number; total?: number } = {}) => {
  const catalogQueries: string[] = [];
  const defaultClips = [clip("hash-a1", "One"), clip("hash-a2", "Two")];

  server.use(
    getMeMockHandler(me({ name: "Admin" })),
    getFillerWatchMockHandler({ sourcesOn: 4, sourcesTotal: 5, clips: 200, held: 0, health: "healthy" }),
    // ⚠ `total` is deliberately NOT derived from the arrays here — this file's whole point is that
    // the Incoming badge follows the SERVER's semantic-review count rather than the rendered
    // rows, and equal fixtures could not tell those two rules apart. So the belt stays empty and
    // the count says 3. (§10 V51e renamed `asks`+`pipeline` → `clips`; the badge reads `total`
    // either way, which is why this survived the rename as a pure field swap.)
    getFillerDecisionReviewsMockHandler({ rows: [], total: over.incomingTotal ?? 3 }),
    getFillerPoolMockHandler({ clips: 200, commercials: 200, eligible: 200, untagged: 0, channels: [] }),
    getListFillerSourcesMockHandler({ sources: [], total: 0 }),
    getSettingsListMockHandler({ settings: [], features: { filler: true } }),
    // ⚠ `total` rides every listing response since §10 V51d — it is how many clips MATCH
    // THE FILTER, counted server-side, while `clips` is one page of them. A mock that
    // omitted it would let a component reading the page length pass.
    getListFillerMockHandler(({ request }) => {
      catalogQueries.push(request.url);
      const clips = over.clips ?? defaultClips;
      return { clips, total: over.total ?? clips.length };
    }),
  );

  return catalogQueries;
};

const makeWrapper = () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
};

const renderPage = (tab: "library" | "incoming" | "sources", initialPath = "/filler/library") =>
  render(<RouterHarness content={<FillerPage tab={tab} />} initialPath={initialPath} />, {
    wrapper: makeWrapper(),
  });

describe("FillerPage shell", () => {
  // The Library badge is shell-owned and intentionally stable: it reports the server-owned watch
  // count, while the extracted catalog module owns its filtered result count and paging details.
  it("keeps the Library badge stable from the server-owned catalog count", async () => {
    stubFillerPage();
    renderPage("library");

    const catalogTab = await screen.findByRole("link", { name: /library/i });
    expect(within(catalogTab).getByText("200")).toBeInTheDocument();
  });

  // ⚠ Admin-only, so this must WAIT for `/v1/auth/me`. Asserting on the link alone would pass
  // instantly against a member's (countless) tab and prove nothing.
  it("counts the Incoming badge from the semantic review total", async () => {
    stubFillerPage({ incomingTotal: 7 });
    renderPage("library");

    await waitFor(() => {
      const incomingTab = screen.getByRole("link", { name: /incoming/i });
      expect(within(incomingTab).getByText("7")).toBeInTheDocument();
    });
  });

  it("makes Sources a routine top-level destination", async () => {
    stubFillerPage();
    renderPage("library");

    expect(await screen.findByRole("link", { name: /^sources$/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /^incoming/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /^manage$/i })).toBeInTheDocument();
  });

  it("treats section links as navigation without an orphan tabpanel role", async () => {
    stubFillerPage();
    renderPage("library");

    await screen.findByRole("navigation", { name: "Filler sections" });
    expect(screen.queryByRole("tabpanel")).not.toBeInTheDocument();
  });

  // ⚠ The pool strip is hidden on Sources (the mock's `showPool: fillerTab !== 'sources'`): it
  // answers "can my catalog fill a break", which is context for reading CLIPS. Above the source
  // list it invites the reading that a source is at fault for weak coverage.
  it("shows the pool strip on Catalog", async () => {
    stubFillerPage();
    renderPage("library");

    expect(await screen.findByLabelText("Catalog health")).toBeInTheDocument();
  });

  it("hides the pool strip on Sources", async () => {
    stubFillerPage();
    renderPage("sources");

    // Wait for the tab to be up, so this cannot pass merely by asserting before the render.
    await screen.findByRole("link", { name: /^sources$/i });
    expect(screen.queryByLabelText("Catalog health")).not.toBeInTheDocument();
  });

  it("moves and selects the clip view with radio-group arrow keys", async () => {
    stubFillerPage();
    renderPage("library");

    const grid = await screen.findByRole("radio", { name: "Grid" });
    const list = screen.getByRole("radio", { name: "List" });
    grid.focus();
    await userEvent.keyboard("{ArrowRight}");

    await waitFor(() => expect(list).toHaveAttribute("aria-checked", "true"));
    expect(list).toHaveFocus();
    expect(list).toHaveAttribute("tabindex", "0");
    expect(grid).toHaveAttribute("tabindex", "-1");
  });

  it("renders compilations as expandable containers and opens their airable segments", async () => {
    stubFillerPage();
    const requests: URL[] = [];
    const composite = { ...clip("parent-hash", "Saturday Morning Reel"), isComposite: true };
    const children = [
      { ...clip("child-a", "Cereal ad"), parentHash: composite.hash },
      { ...clip("child-b", "Toy ad"), parentHash: composite.hash },
    ];
    server.use(
      getListFillerMockHandler(({ request }) => {
        const url = new URL(request.url);
        requests.push(url);
        if (url.searchParams.has("parentHash")) return { clips: children, total: children.length };
        if (url.searchParams.has("hashes")) return { clips: [composite], total: 1 };
        return { clips: [composite], total: 1 };
      }),
    );

    renderPage("library");
    await waitFor(() => {
      const top = requests.find(
        (url) => !url.searchParams.has("parentHash") && !url.searchParams.has("hashes"),
      );
      expect(top?.searchParams.get("includeComposites")).toBe("true");
      expect(top?.searchParams.get("topLevel")).toBe("true");
    });

    await userEvent.click(screen.getByRole("button", { name: /show segments from saturday morning reel/i }));
    expect(await screen.findByText("Cereal ad")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Manage 2 segments" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /use in a channel/i })).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Manage 2 segments" }));
    expect(await screen.findByText("Airable clips filed from this compilation.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /back to top-level catalog/i })).toBeInTheDocument();
  });
});

// Route-level integration regressions for the extracted catalog: the module owns paging, while
// these tests prove `/filler/library` still passes URL state through the page composition boundary.
describe("FillerPage catalog paging", () => {
  const page = (n: number) => Array.from({ length: n }, (_, i) => clip(`hash-${i}`, `Clip ${i}`));

  it("asks for the requested page and says where you are", async () => {
    const catalogQueries = stubFillerPage({ clips: page(60), total: 130 });
    renderPage("library", "/filler/library?page=2");

    // ⚠ Asserting on the REQUEST, not just the rendering: an offset the component computed but
    // never sent would still render a plausible "Page 2 of 3" over page one's clips.
    await waitFor(() => {
      expect(
        catalogQueries.some((u) => {
          const q = new URL(u).searchParams;
          return q.get("offset") === "60" && q.get("limit") === "60";
        }),
      ).toBe(true);
    });
    expect(await screen.findByText("Page 2 of 3")).toBeInTheDocument();
    expect(screen.getByText("Showing 61–120 of 130")).toBeInTheDocument();
  });

  // ⚠ **The highest-risk rule on the page.** Without it, typing in the search box on page 7 lands
  // on an empty page 7 of a two-page result and renders "No clips match" over a catalog that
  // matches plenty — a filter that appears to have found nothing.
  it("resets to page one when a filter changes", async () => {
    // A small page with a large total: the pager only needs `total` to believe there are three
    // pages, and 60 rendered cards per keystroke is what made this test time out.
    const catalogQueries = stubFillerPage({ clips: page(3), total: 130 });
    renderPage("library", "/filler/library?page=2");
    // ⚠ Wait for the CONTROLS, not just the request. The first listing fires before
    // `/v1/settings` resolves, so the page is still showing its unconfigured state at that
    // point and the filter bar does not exist yet.
    const search = await screen.findByLabelText("Search");
    await waitFor(() =>
      expect(catalogQueries.some((u) => new URL(u).searchParams.get("offset") === "60")).toBe(true),
    );

    await userEvent.type(search, "ce");

    await waitFor(() => {
      // Params are read off the recorded REQUEST url rather than substring-matched, so
      // `offset=` cannot be missed by a differently-spelled query string.
      const listings = catalogQueries.filter((u) => new URL(u).searchParams.get("q") === "ce");
      expect(listings.length).toBeGreaterThan(0);
      expect(listings.every((u) => !new URL(u).searchParams.has("offset"))).toBe(true);
    });
  });

  // A single page must not grow a control it does not need — and, more importantly, a catalog
  // LARGER than one page must never be silently truncated to it.
  it("shows no pager on a single page", async () => {
    stubFillerPage({ clips: page(2), total: 2 });
    renderPage("library");
    expect(await screen.findByText("2 clips")).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: /catalog pages/i })).not.toBeInTheDocument();
  });
});
