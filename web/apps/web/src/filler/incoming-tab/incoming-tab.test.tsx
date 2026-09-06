import type { ClipDTO, FillerIncomingOutputBody, IncomingClipDTO } from "@loomarr/api";
import {
  getFileFillerClipsMockHandler,
  getFillerIncomingMockHandler,
  getSettingsListMockHandler,
  getTagFillerClipMockHandler,
} from "@loomarr/api/msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { server } from "@/test/msw/server";
import { IncomingTab } from "./incoming-tab";

// IncomingTab owns the review queue and its four filing mutations. It was extracted from
// `filler-page.tsx`, where all of it mounted on every tab — so a member reading the Catalog
// still paid for a queue that is admin-only server-side.
//
// ⚠ These tests assert the REQUEST BODIES, not just that a click did something. The BE's
// `UpdateClipClassification` writes era and audience unconditionally, so a confirm that sends a bare
// `{era}` silently wipes audience. That is a data-loss bug no rendering assertion can see, and
// this call site reproduced it once already. `category` is NOT part of the body (§10 V45a): it
// is a derived shadow of the taxonomy tags, and this confirm never touches tags at all.

// ⚠ Typed as IncomingClipDTO, which surfaced two MISSING required fields (`kind`, `reason`) the
// untyped stub happily accepted.
//
// ⚠ `needsDecision` is what puts this clip on the OPERATOR's end of the belt (§10 V51e). Without
// it the row renders as still-being-prepared and carries no controls at all — so every assertion
// below about buttons and PATCH bodies would fail for a reason that looks nothing like the cause.
const ASK: IncomingClipDTO = {
  kind: "commercial",
  reason: "era-guess",
  needsDecision: true,
  path: "a3/f9/abc.mp4",
  hash: "hash-abc",
  name: "Toy ad",
  suggestedEra: 1993,
  audience: "kids",
  category: "toys",
  confidence: 80,
  durationMs: 30_000,
};

// The tagged clip the PATCH answers with. Only its shape matters here — the assertions are on
// what was SENT — but it has to be a real ClipDTO, which is nine required fields.
const TAGGED: ClipDTO = {
  hash: "hash-abc",
  name: "Toy ad",
  kind: "commercial",
  durationMs: 30_000,
  era: 1993,
  audience: "kids",
  tagged: true,
  aiTagged: false,
  playCount: 0,
  playsCounted: true,
};

// ⚠ The stub this replaced ended in `return Promise.resolve(jsonResponse(200, {}))` — a catch-all
// answering any other url with an empty object — and its assertions then searched
// `calls.find((c) => c.method === "PATCH")`. Both are the weakness this migration removes: the
// catch-all could not fail, and a method filter would match a PATCH to ANY endpoint. Since the
// whole point of this file is that the PATCH carries the right BODY to the right route, binding
// the handler to `*/v1/filler/tags` is the assertion the old test could not make.
const stubIncoming = (incoming: Partial<FillerIncomingOutputBody> = {}) => {
  const body: FillerIncomingOutputBody = {
    clips: [ASK],
    reels: [],
    recentlyFiled: [],
    // ⚠ `rejected` and `stageOrder` are REQUIRED and the old stub supplied NEITHER — two more
    // fields an untyped catch-all let through.
    rejected: [],
    stageOrder: [],
    total: 1,
    ...incoming,
    clipsTotal: incoming.clipsTotal ?? incoming.clips?.length ?? 1,
    decisionsTotal:
      incoming.decisionsTotal ?? incoming.clips?.filter((clip) => clip.needsDecision).length ?? 1,
    reelsTotal: incoming.reelsTotal ?? incoming.reels?.length ?? 0,
    recentlyFiledTotal: incoming.recentlyFiledTotal ?? incoming.recentlyFiled?.length ?? 0,
    rejectedTotal: incoming.rejectedTotal ?? incoming.rejected?.length ?? 0,
    overview:
      incoming.overview ??
      ({
        runnable: 0,
        inProgress: 0,
        scheduled: 0,
        needsDecision: 1,
        recoverable: 0,
        admitted: 0,
        rejected: 0,
        dismissed: 0,
      } as const),
  };
  const patches: unknown[] = [];
  const files: unknown[] = [];
  server.use(
    getFillerIncomingMockHandler(body),
    // ⚠ The tab also reads /v1/settings, which the OLD catch-all answered with `{}` — so this
    // code path ran against an empty settings payload and nothing said so. The guard named it.
    getSettingsListMockHandler({ settings: [], features: {} }),
    getTagFillerClipMockHandler(async ({ request }) => {
      patches.push(await request.json());
      return TAGGED;
    }),
    // Bound to the FILE route so the confirm test can assert which endpoint took the decision,
    // not merely that a request went somewhere (§10 V54).
    getFileFillerClipsMockHandler(async ({ request }) => {
      files.push(await request.json());
      return { updated: 1, missing: 0 };
    }),
  );
  return { patches, files };
};

const makeWrapper = () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
};

const renderTab = (onEditTags = vi.fn()) => {
  render(<IncomingTab onEditTags={onEditTags} />, { wrapper: makeWrapper() });
  return { onEditTags };
};

describe("IncomingTab", () => {
  it("fetches the incoming queue itself rather than being handed it", async () => {
    stubIncoming();
    renderTab();
    // The whole point of the extraction: the tab is self-sufficient, so the shell no longer
    // unpacks a queue for a tab that may not be showing. Rendering the ask IS the proof the
    // fetch happened — and an unmatched request would now fail the test by name, where the old
    // catch-all would have answered it silently.
    expect(await screen.findByText("Toy ad")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: /filler pipeline status/i })).toHaveTextContent(
      /1 clip decisions/i,
    );
  });

  // ⚠ **"Looks right" has to FILE, and it did not** (§10 V54). It PATCHed the era and stopped, so
  // the clip stayed held and stayed in the queue — and for a clip with a guessed era this is the
  // ONLY affirmative control on the row, because the panel offers "Use it" only when there is no
  // guess. The one button that should have cleared the row left it exactly where it was.
  //
  // It goes through the existing `asSuggested` flag rather than a PATCH chained to a file: the
  // server confirms each clip's own suggestion and files in one request, so there is no window
  // where a clip is filed carrying an unconfirmed guess. Scalar preservation lives on the server:
  // `UpdateClipClassification` carries audience through, while taxonomy and category remain in
  // their separate transaction, as asserted in `fillerfile_test.go`.
  it("confirming a guessed era files the clip as suggested, in one request", async () => {
    const { patches, files } = stubIncoming();
    renderTab();
    await screen.findByText("Toy ad");

    // "Looks right" is the CONFIRM affordance for a guessed era (the panel's own copy).
    await userEvent.click(await screen.findByRole("button", { name: /looks right/i }));

    await waitFor(() => {
      // ⚠ PATHS, not the hash: the file/hold routes stay path-keyed (the V38 store methods are),
      // while the single-clip tag route is hash-keyed. IncomingClipDTO carries both, and this
      // fixture keeps them DIFFERENT strings so a route using the wrong one cannot pass.
      expect(files).toEqual([{ paths: ["a3/f9/abc.mp4"], asSuggested: true }]);
    });
    // And no tag PATCH: two writes would put the confirm and the file in different requests.
    expect(patches).toEqual([]);
  });

  it("hands tag editing up to the shell, which owns the one dialog", async () => {
    stubIncoming();
    const { onEditTags } = renderTab();
    await screen.findByText("Toy ad");

    // For a clip with a GUESSED era the edit affordance reads "Not right" — the panel offers
    // "Add tags" only when there is no guess to reject.
    await userEvent.click(await screen.findByRole("button", { name: /not right/i }));

    // ⚠ The HASH (§10 V54). This asserted `ASK.path` and was green while the button did nothing:
    // the shell resolves the clip by identity, so a path matched no row and no dialog opened. The
    // fixture's path and hash are deliberately different strings — equate them and this test
    // cannot tell the two apart, which is the same trap `putClip` sets on the Go side.
    expect(onEditTags).toHaveBeenCalledWith(ASK.hash);
  });

  it("renders an empty queue without erroring", async () => {
    stubIncoming({ clips: [], total: 0 });
    renderTab();
    await waitFor(() => expect(screen.queryByText("Toy ad")).not.toBeInTheDocument());
  });

  it("does not render a clip twice when the semantic review card owns it", async () => {
    stubIncoming();
    render(
      <IncomingTab onEditTags={vi.fn()} excludedHashes={new Set([ASK.hash])} semanticReviewCount={1} />,
      { wrapper: makeWrapper() },
    );

    expect(await screen.findByLabelText(/filler pipeline status/i)).toHaveTextContent(/1 clip decisions/i);
    expect(screen.queryByText("Toy ad")).not.toBeInTheDocument();
    expect(screen.queryByText("Nothing needs you")).not.toBeInTheDocument();
  });
});
