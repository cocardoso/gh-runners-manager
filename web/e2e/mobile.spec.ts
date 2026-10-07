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

test("a job detail page has no horizontal page scroll on a phone", async ({ page, demo, request }) => {
  await expect.poll(async () => ((await (await request.get(`${demo.url}/api/v1/jobs?status=running`)).json()).jobs ?? []).length, { timeout: 30_000 }).toBeGreaterThan(0);
  const { jobs } = await (await request.get(`${demo.url}/api/v1/jobs?status=running`)).json();
  for (const url of [`/jobs/${jobs[0].id}`, `/jobs/${jobs[0].id}?tab=logs`, `/environments/${jobs[0].environment_id}`]) {
    await page.goto(demo.url + url);
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await page.waitForTimeout(500);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth), url).toBeLessThanOrEqual(1);
  }
});
