import { createGuideController } from "@loomarr/core/guide";
import { LoomarrProvider } from "@loomarr/design-system";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { SurfJourney } from "../index";

const sourceGuide = {
  channels: [
    {
      airings: [
        {
          kind: "program" as const,
          scheduleBlockId: "now-seven",
          startMs: 1_000,
          stopMs: 3_000,
          title: "Radioactive Man",
        },
        {
          kind: "program" as const,
          scheduleBlockId: "next-seven",
          startMs: 3_000,
          stopMs: 5_000,
          title: "Fallout Boy",
        },
      ],
      channelId: "seven",
      name: "Science Fiction",
      number: 7,
      pendingCount: 0,
      status: "live" as const,
    },
    {
      airings: [],
      channelId: "nine",
      name: "Drama",
      number: 9,
      pendingCount: 0,
      status: "live" as const,
    },
  ],
  fromMs: 1_000,
  timezone: "UTC",
  toMs: 5_000,
};

const renderJourney = (
  controller: ReturnType<typeof createGuideController>,
  playableChannelIds: readonly string[] = ["seven", "nine"],
) =>
  renderToStaticMarkup(
    <LoomarrProvider>
      <SurfJourney
        clientVersion="0.2.0"
        controller={controller}
        currentChannelId="seven"
        favoriteChannelIds={["seven"]}
        now={() => 2_000}
        onDisconnect={vi.fn()}
        onTune={vi.fn()}
        playableChannelIds={playableChannelIds}
        recentChannelIds={["nine"]}
        serverName="http://loomarr.test:8080"
        serverVersion="0.2.1"
      />
    </LoomarrProvider>,
  );

describe("SurfJourney", () => {
  it("composes authoritative groups and focused now/next identity", async () => {
    const controller = createGuideController({
      now: () => 2_000,
      source: { load: vi.fn().mockResolvedValue(sourceGuide) },
    });
    await controller.refresh("nine");

    const output = renderJourney(controller);

    expect(output).toContain("FAVOURITES");
    expect(output).toContain("RECENT");
    expect(output).toContain("ALL CHANNELS");
    expect(output).toContain("Radioactive Man");
    expect(output).toContain("Next 12:00 AM · Fallout Boy");
    expect(output).toContain("Loomarr TV 0.2.0 · Server 0.2.1");
  });

  it("maps loading, no-playable-Channel, and error states", async () => {
    const loading = createGuideController({ source: { load: vi.fn() } });
    expect(renderJourney(loading)).toContain("Loading channels");

    const noPlayable = createGuideController({
      now: () => 2_000,
      source: { load: vi.fn().mockResolvedValue(sourceGuide) },
    });
    await noPlayable.refresh();
    expect(renderJourney(noPlayable, [])).toContain("No channels on air");
    expect(renderJourney(noPlayable, [])).toContain("Disconnect device");

    const failed = createGuideController({
      source: { load: vi.fn().mockRejectedValue(new Error("offline")) },
    });
    await failed.refresh();
    expect(renderJourney(failed)).toContain("Surf unavailable");
    expect(renderJourney(failed)).toContain("Try again");
    expect(renderJourney(failed)).toContain("Disconnect device");
  });
});
