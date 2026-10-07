import path from "node:path";
import { expect, test } from "./fixtures";

// Refreshes the README screenshots: `pnpm screenshots` (needs a built UI).
test.skip(!process.env.SCREENSHOTS, "set SCREENSHOTS=1 to refresh docs/images");
test.use({ viewport: { width: 1440, height: 900 } });

const shots: [string, string][] = [
  ["overview", "/"],
  ["jobs", "/jobs"],
  ["environments", "/environments"],
  ["scale-sets", "/scale-sets"],
  ["live-logs", "/logs"],
  ["templates", "/templates"],
  ["settings", "/settings"],
];

test("README screenshots", async ({ page, demo, request, browser }) => {
  test.setTimeout(180_000);
  // Let the simulated fleet run long enough to have history.
  await expect.poll(async () => ((await (await request.get(`${demo.url}/api/v1/jobs?status=completed`)).json()).jobs ?? []).length, { timeout: 90_000 }).toBeGreaterThan(12);
  const { jobs } = await (await request.get(`${demo.url}/api/v1/jobs?status=running`)).json();
  if (jobs?.[0]) shots.push(["job-logs", `/jobs/${jobs[0].id}?tab=logs`], ["job-timeline", `/jobs/${jobs[0].id}`]);
  // A simulated template build, so the Templates page has a verified version.
  await request.post(`${demo.url}/api/v1/templates/build`, { headers: { Authorization: "Bearer demo" } });
  await expect.poll(async () => (await (await request.get(`${demo.url}/api/v1/templates`)).json()).templates?.[0]?.state, { timeout: 60_000 }).toBe("active");
  const { templates } = await (await request.get(`${demo.url}/api/v1/templates`)).json();
  const verified = (templates ?? []).find((t: { report?: { checks?: unknown[] } }) => (t.report?.checks ?? []).length > 0);
  if (verified) shots.push(["template-fidelity", `/templates/${verified.id}?tab=fidelity`]);
  // A credential and a scale set created in the UI, next to the ones from ghrm.yaml.
  const admin = { headers: { Authorization: "Bearer demo" } };
  await request.put(`${demo.url}/api/v1/credentials/work`, { ...admin, data: { token: "github_pat_example_1a2b" } });
  await request.put(`${demo.url}/api/v1/scale-sets/big-builds`, {
    ...admin,
    data: { url: "https://github.com/octo-org", credential: "work", labels: ["large"], max_concurrent: 1, cores: 4, memory_mb: 8192 },
  });
  const out = path.resolve(import.meta.dirname, "../../docs/images");
  // The sign-in page, seen by a browser without a session.
  for (const mode of ["light", "dark"]) {
    const anon = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    const p = await anon.newPage();
    await p.addInitScript((m) => localStorage.setItem("ghrm.theme", m), mode);
    await p.goto(demo.url);
    await expect(p.getByRole("button", { name: "Sign in" })).toBeVisible();
    await p.screenshot({ path: path.join(out, `sign-in-${mode}.png`) });
    await anon.close();
  }
  for (const mode of ["light", "dark"]) {
    await page.addInitScript((m) => localStorage.setItem("ghrm.theme", m), mode);
    for (const [name, url] of shots) {
      await page.goto(demo.url + url);
      await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
      await page.waitForTimeout(1500);
      await page.screenshot({ path: path.join(out, `${name}-${mode}.png`) });
    }
  }
});
