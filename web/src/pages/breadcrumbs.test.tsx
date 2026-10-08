import { screen, waitFor, within } from "@testing-library/react";
import { renderApp } from "@/test/render-app";
import { env, job, mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const T = "2026-10-07T12:00:00Z";
const settings = { version: "dev", admin_actions: true, proxmox: {}, ingest: {}, capacity: {}, scale_sets: [] };

test.each([
  ["a finished job", "/jobs/j1", "Jobs", "/jobs?tab=history"],
  ["a running job", "/jobs/j2", "Jobs", "/jobs"],
  ["a destroyed environment", "/environments/e1", "Environments", "/environments?tab=history"],
  ["a running environment", "/environments/e2", "Environments", "/environments"],
  ["a failed template", "/templates/t1", "Templates", "/templates?tab=history"],
  ["a ready template", "/templates/t2", "Templates", "/templates"],
])("the breadcrumb of %s leads back to the list it belongs to", async (_, path, label, href) => {
  const tpl = (id: string, state: string) => ({ id, state, slim_release: "1", runner_version: "2", layer_version: "5", vmid: 951, size_bytes: 1, pinned: false, active: false, bootstrap: false, in_use: false, trigger: "manual", created_at: T, updated_at: T, activated_at: "0001-01-01T00:00:00Z" });
  mockApi({
    "/api/v1/settings": settings,
    "/api/v1/jobs/j1": job({ id: "j1", status: "completed", result: "succeeded", environment_id: "" }),
    "/api/v1/jobs/j2": job({ id: "j2", status: "running", environment_id: "" }),
    "/api/v1/environments/e1": env({ id: "e1", state: "destroyed" }),
    "/api/v1/environments/e2": env({ id: "e2", state: "running" }),
    "/api/v1/environments/e1/logs/control-plane": { entries: [], next: 0 },
    "/api/v1/environments/e2/logs/control-plane": { entries: [], next: 0 },
    "/api/v1/templates/t1": tpl("t1", "failed"),
    "/api/v1/templates/t2": tpl("t2", "ready"),
  });
  renderApp(path);
  const crumbs = await screen.findByRole("navigation", { name: "breadcrumb" });
  // The header renders the trail once per layout (wide and narrow): every copy must agree.
  await waitFor(() => expect(within(crumbs).getAllByRole("link", { name: label }).map((l) => l.getAttribute("href"))).toEqual(expect.arrayContaining([href])));
  expect(new Set(within(crumbs).getAllByRole("link", { name: label }).map((l) => l.getAttribute("href")))).toEqual(new Set([href]));
});
