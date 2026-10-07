import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { env, job, mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

vi.mock("@/components/resources-chart", () => ({ ResourcesChart: () => <div /> }));

beforeEach(() => {
  FakeEventSource.reset();
  sessionStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

const envs = [
  env({ id: "env-run", state: "running", job_id: "j1" }),
  env({ id: "env-bad", state: "failed", failure_stage: "booting", failure_reason: "no hello" }),
  env({ id: "env-old", state: "destroyed" }),
];

test("lists environments including ones that failed before a job", async () => {
  mockApi({ "/api/v1/environments": { environments: envs }, "/api/v1/jobs": { jobs: [job({ id: "j1", display_name: "build" })] } });
  renderApp("/environments");
  const table = await screen.findByRole("table");
  expect(within(table).getByRole("link", { name: "env-bad" })).toBeInTheDocument();
  expect(within(table).getByText(/no hello/)).toBeInTheDocument();
  expect(await within(table).findByRole("link", { name: "build" })).toHaveAttribute("href", "/jobs/j1");
});

test("filters by state", async () => {
  mockApi({ "/api/v1/environments": { environments: envs } });
  renderApp("/environments?state=live");
  const table = await screen.findByRole("table");
  expect(within(table).getAllByRole("row")).toHaveLength(3);
  expect(within(table).queryByText("env-old")).not.toBeInTheDocument();
});

async function clickDestroy(user: ReturnType<typeof userEvent.setup>) {
  await waitFor(() => expect(screen.getByRole("button", { name: "Destroy" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "Destroy" }));
}

function detailRoutes(destroy: (u: URL) => unknown) {
  return {
    "/api/v1/environments/env-run": env({ id: "env-run", state: "running" }),
    "/api/v1/settings": { version: "dev", admin_actions: true, proxmox: {}, ingest: {}, capacity: {}, scale_sets: [] },
    "POST /api/v1/environments/env-run/destroy": destroy,
    "/api/v1/environments/env-run/logs/control-plane": { entries: [], next: 0 },
  };
}

test("destroying asks for the name and sends the session's CSRF token", async () => {
  const calls = mockApi(detailRoutes(() => new Response(null, { status: 202 })));
  const user = userEvent.setup();
  renderApp("/environments/env-run");
  await clickDestroy(user);
  await user.type(await screen.findByRole("textbox", { name: "Type env-run to confirm deletion" }), "env-run");
  await user.click(screen.getByRole("button", { name: "Destroy environment" }));
  await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
  const post = calls.find((c) => c.method === "POST")!;
  expect(post.headers.get("X-CSRF-Token")).toBe("csrf-1");
  expect(await screen.findByText("Destroy requested")).toBeInTheDocument();
});

test("a refused destroy shows the server's reason", async () => {
  mockApi(detailRoutes(() => new Response(JSON.stringify({ detail: "environment env-run is destroyed" }), { status: 409, headers: { "Content-Type": "application/json" } })));
  const user = userEvent.setup();
  renderApp("/environments/env-run");
  await clickDestroy(user);
  await user.type(await screen.findByRole("textbox", { name: "Type env-run to confirm deletion" }), "env-run");
  await user.click(screen.getByRole("button", { name: "Destroy environment" }));
  expect(await screen.findByText(/environment env-run is destroyed/)).toBeInTheDocument();
});
