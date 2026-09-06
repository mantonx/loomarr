import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import AxeBuilder from "@axe-core/playwright";
import { expect, type Page, test } from "@playwright/test";

// Drive every story from the built Storybook index: a pixel snapshot + an axe sweep, at
// each project's viewport (frontend-design §5). Build storybook-static first —
// `make fe-visual` does `build-storybook` → `playwright test`.
const here = dirname(fileURLToPath(import.meta.url));
const indexPath = join(here, "..", "..", "storybook-static", "index.json");

// The slice of Storybook's preview API this file depends on. Typed here rather than pulled
// from a Storybook package: it is one field read through `page.waitForFunction`, and the
// import would have to resolve inside the browser context, not this Node process.
interface StorybookPreviewWindow {
  __STORYBOOK_PREVIEW__?: { storyRenders?: { id: string; phase?: string }[] };
}

interface StoryEntry {
  id: string;
  title: string;
  name: string;
  tags?: string[];
  type: string;
}

const index = JSON.parse(readFileSync(indexPath, "utf8")) as { entries: Record<string, StoryEntry> };
// JS-driven React Native animations cannot be frozen by Playwright's CSS animation switch.
// Their dedicated stories are exercised by motion.spec.ts through frame-to-frame behavior;
// pixel snapshots cover the same markup in its explicit reduced-motion state.
const stories = Object.values(index.entries).filter(
  (entry) => entry.type === "story" && !entry.tags?.includes("motion-only"),
);

// axe-core keeps a module-global "running" flag per frame, and its runPartialRecursive
// walk can re-enter it — so an analyze() occasionally rejects with "Axe is already
// running" even though this test owns its page. It surfaced on roughly one story per run,
// a DIFFERENT story each time, which is how we know it is a race rather than a real
// violation. Playwright's retry masked it as "1 flaky" and the suite stayed green, but a
// gate that cries wolf on every run stops being read.
//
// Retry ONLY on that specific message: a genuine violation still fails on the first pass.
const analyzeWhenFree = async (page: Page) => {
  for (let attempt = 0; ; attempt++) {
    try {
      return await new AxeBuilder({ page }).include("#storybook-root").analyze();
    } catch (err) {
      const busy = err instanceof Error && err.message.includes("Axe is already running");
      if (!busy || attempt >= 3) throw err;
      await page.waitForTimeout(100 * (attempt + 1));
    }
  }
};

for (const story of stories) {
  test(`${story.title} — ${story.name}`, async ({ page }) => {
    await page.goto(`/iframe.html?id=${story.id}&viewMode=story`);
    // Wait for the story to actually render content (not just the root to attach), then
    // for self-hosted Geist to finish loading — otherwise a screenshot can race a
    // fallback-font paint and drift at the strict 0.001 threshold (§5.2).
    await page.locator("#storybook-root > *").first().waitFor({ state: "visible" });

    // ⚠ WAIT FOR THE PLAY FUNCTION, or a story with one is shot MID-INTERACTION.
    //
    // "First child visible" happens on mount, long before an async `play()` has finished
    // typing and clicking, so the screenshot raced it. This is not theoretical: it produced
    // ChannelLineupEditor/AddingATitle at 340px most runs and 416px intermittently, against
    // a 306px baseline, which read like a mystery flake for weeks.
    //
    // `toHaveScreenshot`'s own stability check does NOT save us. It shoots until two
    // consecutive frames match, and `play()` pauses between keystrokes — so two frames
    // taken inside one of those pauses agree, and it settles on an intermediate state.
    //
    // Storybook tracks the phase per render (`playing` → `finished`); a story with no
    // `play()` reaches a terminal phase immediately, so this costs those nothing. Wait for
    // every terminal phase so a broken play function fails with its actual phase rather
    // than an unrelated timeout, then require success before treating pixels as evidence.
    const phase = await page
      .waitForFunction(
        (id) => {
          const renders = (window as unknown as StorybookPreviewWindow).__STORYBOOK_PREVIEW__?.storyRenders;
          const phase = renders?.find((r) => r.id === id)?.phase;
          return phase === "finished" || phase === "errored" || phase === "aborted" ? phase : undefined;
        },
        story.id,
        { timeout: 15_000 },
      )
      .then((handle) => handle.jsonValue());
    expect(phase, `Storybook interaction for ${story.id} must finish successfully`).toBe("finished");

    await page.evaluate(async () => {
      await document.fonts.ready;
    });

    // Kill animations/transitions/caret OUTRIGHT — reduced-motion only fast-forwards
    // (`duration: 0.001ms`), which leaves an infinite spinner frozen at a random frame
    // and flakes the diff. `animation: none` freezes it at its initial state (§5.2).
    await page.addStyleTag({
      content:
        "*, *::before, *::after { animation: none !important; transition: none !important; caret-color: transparent !important; }",
    });

    // Visual regression (§5.1): snapshot the component element itself, not the full
    // centered page — a fullPage shot re-centers with fractional margins run-to-run,
    // shifting text AA and flaking the 0.001 diff. Committed baseline, 0.001 diff ratio.
    // Portal stories render their actual module beside #storybook-root. React Native Web's Modal
    // gives that portal one dialog root spanning the viewport, so target it directly rather than
    // broadening every story back to the fractional full-page screenshots avoided above.
    const portalStory = story.tags?.includes("portal");
    const snapshotTarget = portalStory ? page.getByRole("dialog") : page.locator("#storybook-root");
    await expect(snapshotTarget).toHaveScreenshot(`${story.id}.png`);

    // Accessibility (§5.3): zero serious/critical violations. The region/landmark rule
    // (moderate) is expected on bare component stories, so only serious+ is blocking.
    const results = portalStory
      ? await new AxeBuilder({ page }).include('[role="dialog"]').analyze()
      : await analyzeWhenFree(page);
    const blocking = results.violations.filter(
      (violation) => violation.impact === "serious" || violation.impact === "critical",
    );
    expect(blocking, `axe: ${blocking.map((violation) => violation.id).join(", ")}`).toEqual([]);
  });
}
