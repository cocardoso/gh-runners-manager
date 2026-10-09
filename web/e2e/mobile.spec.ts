import { expect, pages, test } from "./fixtures";

test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });

for (const [path, title] of pages) {
  test(`${title} (${path}) has no horizontal page scroll on a phone`, async ({ page, demo }) => {
    await page.goto(demo.url + path);
    await expect(page.getByRole("heading", { level: 1, name: title })).toBeVisible();
    await page.waitForTimeout(500);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(overflow).toBeLessThanOrEqual(1);
    // A phone zooms out to fit wide content, which hides an overflow from the check above:
    // the page must keep the device width, start at its left edge, and fit its top bar.
    const layout = await page.evaluate(() => {
      const header = document.querySelector("header")!;
      return { width: window.innerWidth, mainLeft: document.querySelector("main")!.getBoundingClientRect().left, headerFits: header.scrollWidth <= header.clientWidth };
    });
    expect(layout).toEqual({ width: 390, mainLeft: 0, headerFits: true });
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

test("the credential dialog fits a phone, and a tap opens a field's help", async ({ page, demo }) => {
  await page.goto(`${demo.url}/settings`);
  await page.getByRole("button", { name: "Add credential" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Save credential" })).toBeInViewport();
  expect(await dialog.evaluate((d) => d.scrollWidth <= d.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await dialog.getByRole("button", { name: "More information" }).first().tap();
  await expect(page.getByText("A name for this token in ghrm; scale sets refer to it.")).toBeVisible();
});

test("on a phone, a link in a submenu closes the menu and shows its section", async ({ page, demo }) => {
  await page.goto(`${demo.url}/`);
  await page.getByRole("button", { name: "Open navigation" }).tap();
  // On a phone the sidebar is a drawer, announced as navigation.
  const nav = page.getByRole("navigation", { name: "Main navigation" });
  await nav.getByRole("button", { name: "Settings" }).tap();
  await nav.getByRole("link", { name: "Capacity" }).tap();
  await expect(page).toHaveURL(/\/settings#capacity$/);
  await expect(nav.getByRole("link", { name: "Capacity" })).toBeHidden();
  await expect(page.getByRole("heading", { name: "Capacity" })).toBeInViewport();
});
