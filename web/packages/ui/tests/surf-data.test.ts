import { layoutGuide } from "@loomarr/core/guide";
import { describe, expect, it } from "vitest";

import {
  restoreSurfSelection,
  surfGroupsFromGuide,
  surfPreviousChannel,
  watchingScheduleFromGuide,
} from "../index";

const layout = layoutGuide(
  {
    channels: [
      {
        airings: [
          {
            episode: 2,
            kind: "program",
            scheduleBlockId: "now",
            season: 7,
            startMs: 1_000,
            stopMs: 3_000,
            thumbUrl: "/v1/images/now.jpg",
            title: "Now",
          },
          { kind: "program", scheduleBlockId: "next", startMs: 3_000, stopMs: 5_000, title: "Next" },
        ],
        channelId: "seven",
        logo: "/v1/images/seven-logo.png",
        name: "Science Fiction",
        number: 7,
        pendingCount: 0,
        status: "live",
      },
      {
        airings: [],
        channelId: "blocked",
        name: "Not playable",
        number: 8,
        pendingCount: 0,
        status: "live",
      },
      {
        airings: [],
        channelId: "nine",
        name: "Drama",
        number: 9,
        pendingCount: 0,
        status: "live",
      },
    ],
    fromMs: 1_000,
    timezone: "America/New_York",
    toMs: 5_000,
  },
  2_000,
);

const groups = (favoriteChannelIds?: readonly string[]) =>
  surfGroupsFromGuide({
    currentChannelId: "seven",
    favoriteChannelIds,
    layout,
    nowMs: 2_000,
    playableChannelIds: ["seven", "nine"],
    recentChannelIds: ["blocked", "nine", "nine", "seven"],
  });

describe("Surf data", () => {
  it("uses authoritative now/next identity and only playable Channels", () => {
    const result = groups();

    expect(result[0]).toEqual({ channels: [], kind: "favourites", label: "Favourites" });
    expect(result[1]?.channels.map(({ id }) => id)).toEqual(["nine"]);
    expect(result[2]?.channels.map(({ id }) => id)).toEqual(["seven", "nine"]);
    expect(result[2]?.channels[0]).toMatchObject({
      channelLogoUri: "/v1/images/seven-logo.png",
      channelName: "Science Fiction",
      next: { title: "Next" },
      now: {
        artworkUri: "/v1/images/now.jpg",
        episodeLabel: "S07E02",
        progressPercent: 50,
        remainingLabel: "1m left",
        title: "Now",
      },
    });
  });

  it("maps the tuned Channel's authoritative Guide identity into Watching", () => {
    expect(watchingScheduleFromGuide(layout, "seven", 2_000)).toMatchObject({
      next: { title: "Next" },
      now: { episodeLabel: "S07E02", progressPercent: 50, title: "Now" },
    });
    expect(watchingScheduleFromGuide(layout, "missing", 2_000)).toBeUndefined();
  });

  it("populates Favourites only from authoritative membership and preserves Channel order", () => {
    expect(groups(["blocked", "nine", "seven"])[0]?.channels.map(({ id }) => id)).toEqual(["seven", "nine"]);
  });

  it("restores by group, then Channel identity, then the first available row", () => {
    const result = groups();
    expect(restoreSurfSelection(result, { channelId: "nine", group: "recent" })).toEqual({
      channelId: "nine",
      group: "recent",
    });
    expect(restoreSurfSelection(result.slice(2), { channelId: "nine", group: "recent" })).toEqual({
      channelId: "nine",
      group: "all",
    });
    expect(restoreSurfSelection(result, { channelId: "removed", group: "all" })).toEqual({
      channelId: "nine",
      group: "recent",
    });
  });

  it("returns the first distinct playable Recent Channel as the previous target", () => {
    expect(surfPreviousChannel("seven", ["seven", "removed", "nine"], ["seven", "nine"])).toBe("nine");
    expect(surfPreviousChannel("seven", ["seven", "removed"], ["seven", "nine"])).toBeUndefined();
  });
});
