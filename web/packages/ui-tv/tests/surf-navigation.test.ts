import { surfGroups } from "@loomarr/fixtures";
import { describe, expect, it } from "vitest";

import { activateTvSurfSelection, moveTvSurfSelection, restoreTvSurfSelection } from "../index";

describe("TV Surf navigation", () => {
  it("traverses populated groups in order without landing on an empty group", () => {
    const recent = { channelId: "ch-springfield", group: "recent" } as const;
    const firstAll = moveTvSurfSelection(surfGroups, recent, "down");
    expect(firstAll.selection).toEqual({ channelId: "ch-springfield", group: "all" });
    const trek = moveTvSurfSelection(surfGroups, firstAll.selection, "down");
    expect(trek.selection).toEqual({ channelId: "ch-scifi", group: "all" });
    expect(activateTvSurfSelection(trek.selection)).toEqual({ channelId: "ch-scifi", kind: "tune" });
  });

  it("clamps traversal at the rail boundaries", () => {
    const first = { channelId: "ch-springfield", group: "recent" } as const;
    expect(moveTvSurfSelection(surfGroups, first, "up")).toEqual({
      boundary: "up",
      selection: first,
    });
  });

  it("restores the same channel in another group before falling back to the first occurrence", () => {
    const allOnly = surfGroups.filter((group) => group.kind === "all");
    expect(restoreTvSurfSelection(allOnly, { channelId: "ch-springfield", group: "recent" })).toEqual({
      channelId: "ch-springfield",
      group: "all",
    });
    expect(restoreTvSurfSelection(surfGroups, { channelId: "removed", group: "all" })).toEqual({
      channelId: "ch-springfield",
      group: "recent",
    });
  });
});
