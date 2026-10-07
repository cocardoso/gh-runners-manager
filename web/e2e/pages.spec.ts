import { expect, pages, test, trackErrors } from "./fixtures";

for (const [path, title] of pages) {
  test(`${title} renders without console errors`, async ({ page, demo }) => {
    const errors = trackErrors(page);
    await page.goto(demo.url + path);
    await expect(page.getByRole("heading", { level: 1, name: title })).toBeVisible();
    await expect(page.getByRole("status").filter({ hasText: "Live" }).first()).toBeVisible();
    await page.waitForTimeout(1000);
    expect(errors).toEqual([]);
  });
}

test("job and environment detail pages render without console errors", async ({ page, demo, request }) => {
  const errors = trackErrors(page);
  await expect.poll(async () => ((await (await request.get(`${demo.url}/api/v1/jobs?status=running`)).json()).jobs ?? []).length, { timeout: 30_000 }).toBeGreaterThan(0);
  const { jobs } = await (await request.get(`${demo.url}/api/v1/jobs?status=running`)).json();
  const job = jobs[0];
  for (const tab of ["", "?tab=steps", "?tab=logs", "?tab=resources", "?tab=environment"]) {
    await page.goto(`${demo.url}/jobs/${job.id}${tab}`);
    await expect(page.getByRole("heading", { level: 1, name: job.display_name })).toBeVisible();
  }
  await page.goto(`${demo.url}/environments/${job.environment_id}`);
  await expect(page.getByRole("heading", { level: 1, name: job.environment_id })).toBeVisible();
  await page.waitForTimeout(500);
  expect(errors).toEqual([]);
});
