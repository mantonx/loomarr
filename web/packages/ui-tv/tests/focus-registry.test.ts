import { describe, expect, it, vi } from "vitest";

import { createTvGuideFocusRegistry, createTvSurfFocusRegistry, TvFocusRegistry } from "../index";

describe("TV focus registry", () => {
  it("focuses a mounted identity immediately", () => {
    const registry = new TvFocusRegistry<{ id: string }>((target) => target.id);
    const focus = vi.fn();
    registry.register({ id: "seven" }, { focus });
    registry.request({ id: "seven" });
    expect(focus).toHaveBeenCalledOnce();
  });

  it("restores a requested identity after its bounded row mounts", () => {
    const registry = new TvFocusRegistry<{ id: string }>((target) => target.id);
    const focus = vi.fn();
    registry.request({ id: "seven" });
    registry.register({ id: "six" }, { focus });
    expect(focus).not.toHaveBeenCalled();
    registry.register({ id: "seven" }, { focus });
    expect(focus).toHaveBeenCalledOnce();
  });

  it("does not retain an unmounted handle", () => {
    const registry = new TvFocusRegistry<{ id: string }>((target) => target.id);
    const focus = vi.fn();
    registry.register({ id: "seven" }, { focus });
    registry.register({ id: "seven" }, null);
    registry.request({ id: "seven" });
    expect(focus).not.toHaveBeenCalled();
  });

  it("keys Guide restoration by stable schedule identity", () => {
    const registry = createTvGuideFocusRegistry();
    const focus = vi.fn();
    registry.request({
      kind: "airing",
      selection: { anchorMs: 1, channelId: "springfield", scheduleBlockId: "bart" },
    });
    registry.register(
      {
        kind: "airing",
        selection: { anchorMs: 2, channelId: "springfield", scheduleBlockId: "bart" },
      },
      { focus },
    );
    expect(focus).toHaveBeenCalledOnce();
  });

  it("keeps identical Surf channel ids in separate groups", () => {
    const registry = createTvSurfFocusRegistry();
    const recentFocus = vi.fn();
    const allFocus = vi.fn();
    registry.register({ channelId: "springfield", group: "recent" }, { focus: recentFocus });
    registry.register({ channelId: "springfield", group: "all" }, { focus: allFocus });
    registry.request({ channelId: "springfield", group: "all" });
    expect(recentFocus).not.toHaveBeenCalled();
    expect(allFocus).toHaveBeenCalledOnce();
  });
});
