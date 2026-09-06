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
});
