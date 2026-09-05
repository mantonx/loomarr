import { createGuideController } from "@loomarr/core/guide";
import { LoomarrProvider } from "@loomarr/design-system";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { GuideJourney } from "../index";

const sourceGuide = {
  channels: [
    {
      airings: [
        {
          kind: "program" as const,
          scheduleBlockId: "bart",
          startMs: 0,
          stopMs: 1_800_000,
          title: "Bart the Mother",
        },
      ],
      channelId: "springfield",
      name: "Springfield Classics",
      number: 1,
      pendingCount: 0,
      status: "live" as const,
    },
  ],
  fromMs: 0,
  timezone: "UTC",
  toMs: 3_600_000,
};

const markup = (controller: ReturnType<typeof createGuideController>) =>
  renderToStaticMarkup(
    <LoomarrProvider>
      <GuideJourney controller={controller} onTune={vi.fn()} preferredChannelId="springfield" />
    </LoomarrProvider>,
  );

describe("GuideJourney", () => {
  it("composes a ready controller snapshot into the shared Guide presentation", async () => {
    const controller = createGuideController({
      now: () => 900_000,
      source: { load: vi.fn().mockResolvedValue(sourceGuide) },
    });
    await controller.refresh("springfield");

    const output = markup(controller);

    expect(output).toContain("Springfield Classics");
    expect(output).toContain("Bart the Mother");
    expect(output).toContain('accessibilityLabel="Programme guide"');
  });

  it("lets a platform adapter bound the rendered Channel rows", async () => {
    const controller = createGuideController({
      now: () => 900_000,
      source: { load: vi.fn().mockResolvedValue(sourceGuide) },
    });
    await controller.refresh("springfield");

    const output = renderToStaticMarkup(
      <LoomarrProvider>
        <GuideJourney
          channelWindow={() => ({ end: 0, positionLabel: "1 of 1", start: 0 })}
          controller={controller}
          onTune={vi.fn()}
        />
      </LoomarrProvider>,
    );

    expect(output).not.toContain("Springfield Classics, The Simpsons");
    expect(output).toContain("1 channels · 1 of 1");
  });

  it("maps loading, empty, and error controller states without platform input mechanics", async () => {
    const loading = createGuideController({ source: { load: vi.fn() } });
    expect(markup(loading)).toContain("Loading channels");

    const empty = createGuideController({
      source: { load: vi.fn().mockResolvedValue({ ...sourceGuide, channels: [] }) },
    });
    await empty.refresh();
    expect(markup(empty)).toContain("No channels on air");

    const failed = createGuideController({
      source: { load: vi.fn().mockRejectedValue(new Error("offline")) },
    });
    await failed.refresh();
    expect(markup(failed)).toContain("Guide unavailable");
    expect(markup(failed)).toContain("Try again");
  });
});
