import type { ClipDTO } from "@loomarr/api";
import { getListFillerMockHandler } from "@loomarr/api/msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { server } from "@/test/msw/server";
import { RouterHarness } from "@/test/story-utils";
import { FillerCatalog } from "./filler-catalog";

const clip: ClipDTO = {
  hash: "catalog-hash",
  name: "Local soda commercial",
  kind: "commercial",
  era: 1990,
  audience: "general",
  category: "drinks",
  durationMs: 30_000,
  source: "folder",
  playCount: 0,
  playsCounted: true,
  aiTagged: false,
  tagged: true,
  suggestedEra: 0,
};

const Wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider
    client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}
  >
    {children}
  </QueryClientProvider>
);

describe("FillerCatalog", () => {
  it("owns catalog rendering while delegating the shared exact-clip editor", async () => {
    server.use(getListFillerMockHandler({ clips: [clip], total: 1 }));
    const onEditTags = vi.fn();

    render(
      <RouterHarness
        initialPath="/filler/library"
        content={<FillerCatalog isAdmin onEditTags={onEditTags} onProposePull={vi.fn()} />}
      />,
      { wrapper: Wrapper },
    );

    expect(await screen.findByText("Local soda commercial")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Edit tags" }));
    expect(onEditTags).toHaveBeenCalledWith("catalog-hash");
  });

  it("clears a selected clip when a search removes it from the rendered result set", async () => {
    const otherClip: ClipDTO = { ...clip, hash: "other-catalog-hash", name: "Weather report" };
    server.use(
      getListFillerMockHandler(({ request }) => {
        const q = new URL(request.url).searchParams.get("q");
        return q === "weather" ? { clips: [otherClip], total: 1 } : { clips: [clip], total: 1 };
      }),
    );

    const user = userEvent.setup();
    render(
      <RouterHarness
        initialPath="/filler/library"
        content={<FillerCatalog isAdmin onEditTags={vi.fn()} onProposePull={vi.fn()} />}
      />,
      { wrapper: Wrapper },
    );

    await screen.findByText("Local soda commercial");
    await user.click(screen.getByRole("checkbox", { name: "Select Local soda commercial" }));
    expect(await screen.findByText("1 clip selected")).toBeInTheDocument();

    await user.type(screen.getByLabelText("Search"), "weather");
    expect(await screen.findByText("Weather report")).toBeInTheDocument();
    expect(screen.queryByText("Local soda commercial")).not.toBeInTheDocument();

    // Selection is transient intent about the rows in front of the operator. Keeping this bar
    // armed would let its hash-keyed bulk actions target a row hidden by the new server result.
    expect(screen.queryByText("1 clip selected")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove from catalog" })).not.toBeInTheDocument();
  });

  it("keeps a selected clip when switching the view on the same result page", async () => {
    server.use(getListFillerMockHandler({ clips: [clip], total: 1 }));

    const user = userEvent.setup();
    render(
      <RouterHarness
        initialPath="/filler/library"
        content={<FillerCatalog isAdmin onEditTags={vi.fn()} onProposePull={vi.fn()} />}
      />,
      { wrapper: Wrapper },
    );

    await screen.findByText("Local soda commercial");
    await user.click(screen.getByRole("checkbox", { name: "Select Local soda commercial" }));
    await user.click(screen.getByRole("radio", { name: "List" }));

    expect(await screen.findByText("1 clip selected")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove from catalog" })).toBeInTheDocument();
  });
});
