import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { env, job, mockApi, scaleSet } from "@/test/api-mock";
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

const LIVE_STATES = ["pending", "provisioning", "booting", "connected", "idle", "running", "completing", "destroying", "failed"];

/** Answers like the server: only the environments in the asked states. */
function byState(list: ReturnType<typeof env>[]) {
  return (u: URL) => {
    const states = u.searchParams.get("state")?.split(",");
    return { environments: states ? list.filter((e) => states.includes(e.state)) : list };
  };
}

test("the Running tab lists every environment that exists now, asking the server only for those states", async () => {
  const calls = mockApi({ "/api/v1/environments": byState(envs), "/api/v1/jobs": { jobs: [job({ id: "j1", display_name: "build" })] } });
  renderApp("/environments");
  const table = await screen.findByRole("table");
  expect(screen.getByRole("tab", { name: "Running", selected: true })).toBeInTheDocument();
  expect(within(table).getByRole("link", { name: "env-run" })).toBeInTheDocument();
  expect(within(table).getByRole("link", { name: "env-bad" })).toBeInTheDocument();
  expect(within(table).getByText(/no hello/)).toBeInTheDocument();
  expect(within(table).queryByText("env-old")).not.toBeInTheDocument();
  expect(await within(table).findByRole("link", { name: "build" })).toHaveAttribute("href", "/jobs/j1");
  const asked = calls.filter((c) => c.url.pathname === "/api/v1/environments").map((c) => c.url.searchParams.get("state")?.split(",").sort());
  expect(asked).toContainEqual([...LIVE_STATES].sort());
});

function keptFixture(minutesAgo: number, keepMinutes: number | null) {
  const failedAt = new Date(Date.now() - minutesAgo * 60_000);
  mockApi({
    "/api/v1/environments": byState([env({ id: "env-bad", state: "failed", scale_set: "homelab", state_changed_at: failedAt.toISOString(), failure_stage: "booting" })]),
    "/api/v1/scale-sets": {
      scale_sets: keepMinutes === null ? [] : [scaleSet({ name: "homelab", settings: { url: "https://github.com/octo", credential: "c", keep_on_failure_minutes: keepMinutes } })],
    },
  });
  return new Date(failedAt.getTime() + (keepMinutes ?? 0) * 60_000);
}

test("a failed environment kept for debugging says until when, in the language's clock", async () => {
  const until = keptFixture(10, 30);
  renderApp("/environments");
  const table = await screen.findByRole("table");
  const time = new Intl.DateTimeFormat("en", { hour: "numeric", minute: "2-digit" }).format(until);
  expect(await within(table).findByText(`kept for debugging until ${time}`)).toBeInTheDocument();
});

test("a keep time on another day says which day", async () => {
  const until = keptFixture(10, 26 * 60);
  renderApp("/environments");
  const table = await screen.findByRole("table");
  const when = new Intl.DateTimeFormat("en", { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }).format(until);
  expect(await within(table).findByText(`kept for debugging until ${when}`)).toBeInTheDocument();
});

test("an environment past its keep time, or whose scale set is gone, is being removed", async () => {
  keptFixture(60, 30);
  const first = renderApp("/environments");
  expect(await within(await screen.findByRole("table")).findByText("being removed")).toBeInTheDocument();
  first.unmount();
  vi.unstubAllGlobals();
  keptFixture(1, null);
  renderApp("/environments");
  expect(await within(await screen.findByRole("table")).findByText("being removed")).toBeInTheDocument();
});

test("with nothing running the Running tab says so and points to the history", async () => {
  mockApi({ "/api/v1/environments": byState([env({ id: "env-old", state: "destroyed" })]) });
  renderApp("/environments");
  expect(await screen.findByText("No environment is running")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "See the history" })).toHaveAttribute("href", "/environments?tab=history");
});

test("the History tab lists destroyed environments, newest first, with how long they lived", async () => {
  const calls = mockApi({
    "/api/v1/environments": byState([
      env({ id: "env-run", state: "running" }),
      env({ id: "env-a", state: "destroyed", created_at: "2026-10-08T10:00:00Z", state_changed_at: "2026-10-08T10:05:30Z" }),
      env({ id: "env-b", state: "destroyed", created_at: "2026-10-08T11:00:00Z", state_changed_at: "2026-10-08T11:01:00Z", failure_stage: "booting", failure_reason: "no hello" }),
    ]),
  });
  renderApp("/environments?tab=history");
  const table = await screen.findByRole("table");
  expect(screen.getByRole("tab", { name: "History", selected: true })).toBeInTheDocument();
  const rows = within(table).getAllByRole("row").slice(1);
  expect(rows).toHaveLength(2);
  expect(within(rows[0]!).getByRole("link", { name: "env-b" })).toBeInTheDocument();
  expect(within(rows[0]!).getByText(/no hello/)).toBeInTheDocument();
  expect(within(rows[1]!).getByText("5m 30s")).toBeInTheDocument();
  expect(within(table).queryByText("env-run")).not.toBeInTheDocument();
  expect(calls.some((c) => c.url.pathname === "/api/v1/environments" && c.url.searchParams.get("state") === "destroyed")).toBe(true);
});

test("the History tab filters by outcome", async () => {
  mockApi({
    "/api/v1/environments": byState([
      env({ id: "env-a", state: "destroyed" }),
      env({ id: "env-b", state: "destroyed", failure_stage: "booting", failure_reason: "no hello" }),
    ]),
  });
  renderApp("/environments?tab=history&status=failed");
  const table = await screen.findByRole("table");
  expect(within(table).getAllByRole("row")).toHaveLength(2);
  expect(within(table).getByRole("link", { name: "env-b" })).toBeInTheDocument();
});

test("switching tabs puts the tab in the URL", async () => {
  mockApi({ "/api/v1/environments": byState(envs) });
  const user = userEvent.setup();
  const { history } = renderApp("/environments");
  await screen.findByRole("table");
  await user.click(screen.getByRole("tab", { name: "History" }));
  await waitFor(() => expect(history.location.search).toContain("tab=history"));
  expect(await screen.findByRole("link", { name: "env-old" })).toBeInTheDocument();
  await user.click(screen.getByRole("tab", { name: "Running" }));
  await waitFor(() => expect(history.location.search).not.toContain("tab="));
});

test("in Portuguese the tabs and the badge are translated", async () => {
  mockApi({ "/api/v1/environments": byState(envs), "/api/v1/scale-sets": { scale_sets: [] } });
  renderApp("/environments", { locale: "pt-BR" });
  const table = await screen.findByRole("table");
  expect(screen.getByRole("tab", { name: "Em execução", selected: true })).toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "Histórico" })).toBeInTheDocument();
  expect(within(table).getByText("sendo removido")).toBeInTheDocument();
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

test("a destroyed environment can be deleted from history; a live one cannot", async () => {
  const calls = mockApi({
    "/api/v1/environments/env-old": env({ id: "env-old", state: "destroyed" }),
    "/api/v1/environments/env-run": env({ id: "env-run", state: "running" }),
    "/api/v1/settings": { version: "dev", admin_actions: true, proxmox: {}, ingest: {}, capacity: {}, scale_sets: [] },
    "/api/v1/environments/env-old/logs/control-plane": { entries: [], next: 0 },
    "/api/v1/environments/env-run/logs/control-plane": { entries: [], next: 0 },
    "DELETE /api/v1/environments/env-old": () => new Response(null, { status: 204 }),
  });
  const user = userEvent.setup();
  renderApp("/environments/env-old");
  await user.click(await screen.findByRole("button", { name: "Delete from history" }));
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByText(/jobs, events and logs/)).toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Delete" }));
  await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url.pathname === "/api/v1/environments/env-old")).toBe(true));
});

test("a running environment offers no Delete from history", async () => {
  mockApi({
    "/api/v1/environments/env-run": env({ id: "env-run", state: "running" }),
    "/api/v1/settings": { version: "dev", admin_actions: true, proxmox: {}, ingest: {}, capacity: {}, scale_sets: [] },
    "/api/v1/environments/env-run/logs/control-plane": { entries: [], next: 0 },
  });
  renderApp("/environments/env-run");
  expect(await screen.findByRole("button", { name: "Destroy" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Delete from history" })).not.toBeInTheDocument();
});

test("without admin actions a destroyed environment offers no Delete from history", async () => {
  mockApi({
    "/api/v1/environments/env-old": env({ id: "env-old", state: "destroyed" }),
    "/api/v1/settings": { version: "dev", admin_actions: false, proxmox: {}, ingest: {}, capacity: {}, scale_sets: [] },
    "/api/v1/environments/env-old/logs/control-plane": { entries: [], next: 0 },
  });
  renderApp("/environments/env-old");
  await screen.findByRole("heading", { name: "env-old" });
  expect(screen.queryByRole("button", { name: "Delete from history" })).not.toBeInTheDocument();
});

test("after deleting a destroyed environment the history tab is shown, where it was", async () => {
  mockApi({
    "/api/v1/environments/env-old": env({ id: "env-old", state: "destroyed" }),
    "/api/v1/settings": { version: "dev", admin_actions: true, proxmox: {}, ingest: {}, capacity: {}, scale_sets: [] },
    "/api/v1/environments/env-old/logs/control-plane": { entries: [], next: 0 },
    "DELETE /api/v1/environments/env-old": () => new Response(null, { status: 204 }),
  });
  const user = userEvent.setup();
  const { history } = renderApp("/environments/env-old");
  await user.click(await screen.findByRole("button", { name: "Delete from history" }));
  await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Delete" }));
  await waitFor(() => expect(history.location.pathname).toBe("/environments"));
  expect(history.location.search).toContain("tab=history");
});

test("switching tabs starts the other list on its first page", async () => {
  const destroyed = Array.from({ length: 60 }, (_, i) => env({ id: `env-d${i}`, state: "destroyed" }));
  mockApi({ "/api/v1/environments": byState([env({ id: "env-run", state: "running" }), ...destroyed]) });
  const user = userEvent.setup();
  const { history } = renderApp("/environments?tab=history&page=3");
  await screen.findByText("env-d50");
  await user.click(screen.getByRole("tab", { name: "Running" }));
  expect(await screen.findByText("env-run")).toBeInTheDocument();
  expect(history.location.search).not.toContain("page=");
});

test("while the other tab loads it shows loading, never a false empty list", async () => {
  let release: (v: unknown) => void = () => {};
  const late = new Promise((r) => (release = r));
  mockApi({
    "/api/v1/environments": (u: URL) =>
      u.searchParams.get("state") === "destroyed" ? late : { environments: [env({ id: "env-run", state: "running" })] },
  });
  const user = userEvent.setup();
  renderApp("/environments");
  await screen.findByText("env-run");
  await user.click(screen.getByRole("tab", { name: "History" }));
  expect(screen.queryByText(/No environment has ended yet/)).not.toBeInTheDocument();
  expect(screen.queryByText("env-run")).not.toBeInTheDocument();
  release({ environments: [env({ id: "env-old", state: "destroyed" })] });
  expect(await screen.findByText("env-old")).toBeInTheDocument();
});
