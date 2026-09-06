import { expect, test } from "@playwright/test";

test("the large filler review queue stays bounded and focused at mobile width", async ({
  page,
}, testInfo) => {
  test.skip(testInfo.project.name !== "mobile", "mobile review-queue layout contract");

  await page.goto("/iframe.html?id=filler-decisionreviewqueue--large-queue&viewMode=story");
  await page.locator("#storybook-root > *").first().waitFor({ state: "visible" });
  const focusedCard = page.locator('[aria-labelledby^="review-decision-"]');
  await expect(focusedCard).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Review queue" })).toContainText("Station promo 2");

  const metrics = await page.evaluate(() => {
    const root = document.querySelector<HTMLElement>("#storybook-root");
    const strip = document.querySelector<HTMLElement>(
      'nav[aria-labelledby="review-queue-navigation-heading"] ol',
    );
    const card = document.querySelector<HTMLElement>('[aria-labelledby^="review-decision-"]');
    const cardBounds = card?.getBoundingClientRect();
    return {
      viewportWidth: window.innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      rootWidth: root?.scrollWidth ?? 0,
      stripClientWidth: strip?.clientWidth ?? 0,
      stripScrollWidth: strip?.scrollWidth ?? 0,
      cardLeft: cardBounds?.left ?? -1,
      cardRight: cardBounds?.right ?? Number.POSITIVE_INFINITY,
    };
  });

  expect(metrics.documentWidth).toBeLessThanOrEqual(metrics.viewportWidth);
  expect(metrics.rootWidth).toBeLessThanOrEqual(metrics.viewportWidth);
  expect(metrics.stripScrollWidth).toBeGreaterThan(metrics.stripClientWidth);
  expect(metrics.cardLeft).toBeGreaterThanOrEqual(0);
  expect(metrics.cardRight).toBeLessThanOrEqual(metrics.viewportWidth);
});
