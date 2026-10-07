import { expect, pages, test } from "./fixtures";

test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });

for (const [path, title] of pages) {
  test(`${title} has no horizontal page scroll on a phone`, async ({ page, demo }) => {
    await page.goto(demo.url + path);
    await expect(page.getByRole("heading", { level: 1, name: title })).toBeVisible();
    await page.waitForTimeout(500);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(overflow).toBeLessThanOrEqual(1);
  });
}
