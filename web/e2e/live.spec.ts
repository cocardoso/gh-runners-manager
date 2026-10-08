import { expect, test } from "./fixtures";

async function runningJob(request: import("@playwright/test").APIRequestContext, url: string) {
  let job: { id: string; display_name: string; environment_id: string } | undefined;
  await expect
    .poll(
      async () => {
        const { jobs } = await (await request.get(`${url}/api/v1/jobs?status=running`)).json();
        // The most recently started job has the most of its run ahead.
        job = (jobs ?? [])
          .filter((j: { environment_id?: string }) => j.environment_id)
          .sort((a: { started_at: string }, b: { started_at: string }) => b.started_at.localeCompare(a.started_at))[0];
        return !!job;
      },
      { timeout: 30_000 },
    )
    .toBe(true);
  return job!;
}

test("a job progresses live without a reload", async ({ page, demo, request }) => {
  const job = await runningJob(request, demo.url);
  await page.goto(`${demo.url}/jobs/${job.id}`);
  const status = page.locator("dl").getByText(/^(running|succeeded|failed|canceled)$/).first();
  await expect(status).toHaveText("running");
  await expect(status).toHaveText(/succeeded|failed|canceled/, { timeout: 30_000 });
  await expect(page.getByText("Destroyed")).toBeVisible({ timeout: 30_000 });
});

test("following a log appends lines", async ({ page, demo, request }) => {
  // Simulated jobs last 8-40 s, so the one picked may finish while we wait: try a newer one.
  let grew = false;
  for (let attempt = 0; attempt < 3 && !grew; attempt++) {
    const job = await runningJob(request, demo.url);
    await page.goto(`${demo.url}/jobs/${job.id}?tab=logs`);
    const counter = page.getByRole("status").filter({ hasText: / lines$/ });
    await expect(counter).toBeVisible();
    const count = async () => Number(((await counter.textContent()) ?? "").match(/([\d,]+) lines/)?.[1]?.replace(/,/g, "") ?? 0);
    const first = await count();
    grew = await expect
      .poll(count, { timeout: 10_000 })
      .toBeGreaterThan(first)
      .then(() => true)
      .catch(() => false);
  }
  expect(grew).toBe(true);
});

test("the UI reconnects and resumes after the control plane restarts", async ({ page, demo }) => {
  await page.goto(`${demo.url}/logs`);
  const live = page.getByRole("status").filter({ hasText: /^(Connecting|Live|Reconnecting)$/ });
  await expect(live).toHaveText("Live");
  await demo.stop();
  await expect(live).toHaveText("Reconnecting");
  await demo.start();
  await expect(live).toHaveText("Live", { timeout: 40_000 });
  // New events keep arriving after the restart, without a reload.
  const counter = page.getByText(/^[\d,]+ events$/);
  const count = async () => Number(((await counter.textContent()) ?? "0").replace(/[^\d]/g, ""));
  const before = await count();
  await expect.poll(count, { timeout: 20_000 }).toBeGreaterThan(before);
});

test("a template build progresses live to active from the Templates page", async ({ page, demo }) => {
  await page.goto(`${demo.url}/templates`);
  const build = page.getByRole("button", { name: "Build now" });
  await expect(build).toBeEnabled();
  await build.click();
  await expect(page.getByText("Build started")).toBeVisible();
  const row = page.getByRole("row").filter({ hasText: "Manual" }).first();
  await expect(row.getByText(/building|creating|verifying|ready|active/)).toBeVisible();
  await expect(row.getByText("active", { exact: true })).toBeVisible({ timeout: 40_000 });
});
