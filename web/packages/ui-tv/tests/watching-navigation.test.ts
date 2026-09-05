import { describe, expect, it } from "vitest";

import {
  initialTvWatchingRemoteState,
  reduceTvWatchingRemote,
  type TvWatchingRemoteState,
  tvNumberEntryPresentation,
  tvWatchingRemoteEventFromNative,
} from "../index";

describe("TV Watching remote navigation", () => {
  it("translates native TV event names at the platform boundary", () => {
    expect(tvWatchingRemoteEventFromNative("7", 100)).toEqual({
      atMs: 100,
      digit: "7",
      key: "digit",
    });
    expect(tvWatchingRemoteEventFromNative("channelUp", 100)).toEqual({
      direction: "up",
      key: "channel",
    });
    expect(tvWatchingRemoteEventFromNative("channelDown", 100)).toEqual({
      direction: "down",
      key: "channel",
    });
    expect(tvWatchingRemoteEventFromNative("select", 100)).toEqual({ key: "select" });
    expect(tvWatchingRemoteEventFromNative("select", 100, 0)).toBeUndefined();
    expect(tvWatchingRemoteEventFromNative("select", 100, 1)).toEqual({ key: "select" });
    expect(tvWatchingRemoteEventFromNative("playPause", 100)).toBeUndefined();
  });

  it("maps the current Shield remote controls and leaves platform-owned keys unhandled", () => {
    expect(reduceTvWatchingRemote(initialTvWatchingRemoteState, { key: "up" })).toMatchObject({
      handled: true,
      intent: { direction: 1, kind: "step" },
    });
    expect(
      reduceTvWatchingRemote(initialTvWatchingRemoteState, { direction: "down", key: "channel" }),
    ).toMatchObject({ handled: true, intent: { direction: -1, kind: "step" } });
    expect(reduceTvWatchingRemote(initialTvWatchingRemoteState, { key: "select" })).toMatchObject({
      handled: true,
      intent: { kind: "open-guide" },
    });
    expect(reduceTvWatchingRemote(initialTvWatchingRemoteState, { key: "menu" })).toMatchObject({
      handled: true,
      intent: { kind: "open-surf" },
    });
    expect(reduceTvWatchingRemote(initialTvWatchingRemoteState, { key: "back" }).handled).toBe(false);
    expect(reduceTvWatchingRemote(initialTvWatchingRemoteState, { key: "right" }).handled).toBe(false);
  });

  it("keeps the last three digits and restarts the bounded timeout", () => {
    let state: TvWatchingRemoteState = initialTvWatchingRemoteState;
    for (const [digit, atMs] of [
      ["1", 0],
      ["2", 100],
      ["3", 200],
      ["4", 300],
    ] as const) {
      state = reduceTvWatchingRemote(state, { atMs, digit, key: "digit" }).state;
    }

    expect(state).toEqual({ numberEntry: { digits: "234", expiresAtMs: 1_500 } });
    expect(reduceTvWatchingRemote(state, { atMs: 1_499, key: "timeout" })).toEqual({
      handled: false,
      state,
    });
    expect(reduceTvWatchingRemote(state, { atMs: 1_500, key: "timeout" })).toEqual({
      handled: true,
      intent: { digits: "234", kind: "tune-number" },
      state: {},
    });
  });

  it("commits digits with Select instead of opening Guide", () => {
    const entered = reduceTvWatchingRemote(initialTvWatchingRemoteState, {
      atMs: 10,
      digit: "7",
      key: "digit",
    }).state;

    expect(reduceTvWatchingRemote(entered, { key: "select" })).toEqual({
      handled: true,
      intent: { digits: "7", kind: "tune-number" },
      state: {},
    });
  });

  it("previews only an exact Channel number and keeps leading-zero tune identity", () => {
    const channels = [
      { name: "Science Fiction", number: 7 },
      { name: "Nature", number: 21 },
    ];
    const partial = reduceTvWatchingRemote(initialTvWatchingRemoteState, {
      atMs: 10,
      digit: "2",
      key: "digit",
    }).state;
    expect(tvNumberEntryPresentation(partial, channels)).toEqual({
      channelName: undefined,
      digits: "2",
      expiresAtMs: 1_210,
    });

    const leadingZero = reduceTvWatchingRemote(partial, { atMs: 20, digit: "1", key: "digit" }).state;
    expect(tvNumberEntryPresentation(leadingZero, channels)?.channelName).toBe("Nature");
    const exact = reduceTvWatchingRemote(initialTvWatchingRemoteState, {
      atMs: 30,
      digit: "0",
      key: "digit",
    }).state;
    const seven = reduceTvWatchingRemote(exact, { atMs: 40, digit: "7", key: "digit" }).state;
    expect(tvNumberEntryPresentation(seven, channels)?.channelName).toBe("Science Fiction");
  });
});
