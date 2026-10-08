import { screen, within } from "@testing-library/react";
import { renderApp } from "@/test/render-app";
import { emptyOverview, env, job, mockApi, scaleSet } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";
import { parseWorkflowRef } from "./overview";

vi.mock("@/components/jobs-chart", () => ({ JobsChart: () => <div data-testid="jobs-chart" /> }));

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const ago = (s: number) => new Date(Date.now() - s * 1000).toISOString();

const busy = {
  ...emptyOverview,
  kpis: {
    ...emptyOverview.kpis,
    running_jobs: 3,
    jobs_24h: 41,
    success_rate_24h: 0.9512,
    median_duration_seconds_24h: 95,
    queued_jobs: 2,
    oldest_queued_at: ago(90),
    preparing_runners: 1,
    ready_runners: 1,
  },
  capacity: { ...emptyOverview.capacity, environments_live: 4, memory_committed_mb: 16384 },
  alerts: [
    { level: "error", kind: "listener", message: "listener stopped: 401 Bad credentials", scale_set: "homelab", time: "2026-10-07T11:00:00Z" },
    { level: "brand-new-level", kind: "future", message: "something new", time: "2026-10-07T11:00:00Z" },
    { level: "warn", kind: "waiting", message: "jobs are waiting: host_memory", scale_set: "busy", time: "2026-10-07T11:00:00Z" },
  ],
};

const ref = "octo/app/.github/workflows/stg.yml@refs/heads/main";

function busyApi() {
  mockApi({
    "/api/v1/overview": busy,
    "/api/v1/stats/jobs": { buckets: [{ start: "2026-10-07T11:00:00Z", succeeded: 3, failed: 1, canceled: 0, other: 0 }] },
    "/api/v1/jobs": {
      jobs: [
        job({ id: "run-1", display_name: "package", workflow_ref: ref, run_id: 77, runner_name: "ghrm-abc", started_at: ago(41) }),
        job({ id: "wait-1", display_name: "deploy", workflow_ref: ref, status: "assigned", queued_at: ago(18), started_at: "0001-01-01T00:00:00Z" }),
        job({ id: "wait-2", scale_set: "busy", display_name: "lint", status: "assigned", queued_at: ago(5), started_at: "0001-01-01T00:00:00Z" }),
        job({ id: "done-old", display_name: "tests", status: "completed", result: "succeeded", queued_at: ago(600), started_at: ago(577), finished_at: ago(545) }),
        job({ id: "done-new", display_name: "cleanup", status: "completed", result: "failed", queued_at: ago(300), started_at: ago(278), finished_at: ago(264) }),
      ],
    },
    "/api/v1/environments": {
      environments: [
        env({ id: "e-run", state: "running", job_id: "run-1" }),
        env({ id: "e-boot", state: "booting" }),
        env({ id: "e-ready", state: "idle" }),
        env({ id: "e-build", scale_set: "", state: "connected", kind: "build" }),
        env({ id: "e-gone", state: "destroyed" }),
      ],
    },
    "/api/v1/scale-sets": {
      scale_sets: [
        scaleSet({ name: "homelab", settings: { url: "https://github.com/octo/app", credential: "c", max_concurrent: 3 } }),
        scaleSet({ name: "busy", listening: false, waiting: "memory_budget" }),
      ],
    },
    "/api/v1/templates": { templates: [{ id: "tpl-1", state: "active", slim_release: "20261005.17", activated_at: ago(3600), created_at: ago(7200), updated_at: ago(3600), vmid: 951, pinned: false, active: true }], building: false, enabled: true },
    "/api/v1/cache": { enabled: true, up: true, origins: [], disk_used_bytes: 0, disk_budget_bytes: 0, checked_at: ago(10) },
    "/api/v1/settings": { version: "v0.6.0", admin_actions: false, proxmox: {}, ingest: {}, capacity: {}, scale_sets: [] },
  });
}

test("the headline numbers say what runs, what waits and how the day went", async () => {
  busyApi();
  renderApp("/");
  expect(await screen.findByText("Running now")).toBeInTheDocument();
  expect(screen.getByText("1 runner preparing")).toBeInTheDocument();
  expect(screen.getByText(/Waiting for 1m 3\ds/)).toBeInTheDocument();
  expect(screen.getByText("95% succeeded")).toBeInTheDocument();
  expect(screen.getByText("41")).toBeInTheDocument();
  expect(screen.getByText("1m 35s")).toBeInTheDocument();
  expect(screen.getByText("Median job time (24 h)")).toBeInTheDocument();
  expect(screen.queryByText(/Median queue time/)).not.toBeInTheDocument();
  expect(screen.getAllByText("4 / 6").length).toBeGreaterThan(0);
  expect(screen.getByText(/401 Bad credentials/)).toBeInTheDocument();
  expect(screen.getByText("something new")).toBeInTheDocument();
  expect(screen.getByText("Jobs are waiting: the host is low on memory")).toBeInTheDocument();
  expect(screen.getByTestId("jobs-chart")).toBeInTheDocument();
});

test("now lists running jobs first, with a timer and the run, then queued jobs with why they wait", async () => {
  busyApi();
  renderApp("/");
  const now = await screen.findByRole("region", { name: "Now" });
  const rows = await within(now).findAllByRole("listitem");
  expect(rows.map((r) => r.textContent)).toEqual([
    expect.stringContaining("app · stg · package"),
    expect.stringContaining("app · stg · deploy"),
    expect.stringContaining("lint"),
  ]);
  expect(within(rows[0]!).getByText("Running")).toBeInTheDocument();
  expect(within(rows[0]!).getByText(/^4\ds$/)).toBeInTheDocument();
  expect(within(rows[0]!).getByRole("link", { name: "Open the run on GitHub" })).toHaveAttribute("href", "https://github.com/octo/app/actions/runs/77");
  expect(within(rows[0]!).getByText(/homelab · main · ghrm-abc/)).toBeInTheDocument();
  expect(within(rows[1]!).getByText("Queued")).toBeInTheDocument();
  expect(within(rows[1]!).getByText(/a runner is being prepared/)).toBeInTheDocument();
  expect(within(rows[2]!).getByText(/the memory budget is full/)).toBeInTheDocument();
  // A template build is no runner.
  expect(within(now).getByText("Runners preparing: 1 · ready: 1")).toBeInTheDocument();
});

test("recently finished jobs show how long they took and waited, newest first", async () => {
  busyApi();
  renderApp("/");
  const recent = await screen.findByRole("region", { name: "Recently finished" });
  const rows = await within(recent).findAllByRole("listitem");
  expect(rows[0]).toHaveTextContent("cleanup");
  expect(rows[0]).toHaveTextContent("took 14s · waited 22s");
  expect(rows[1]).toHaveTextContent("tests");
});

test("scale sets and the platform show their health", async () => {
  busyApi();
  renderApp("/");
  const sets = await screen.findByRole("region", { name: "Scale sets" });
  const home = (await within(sets).findByText("homelab")).closest("li")!;
  expect(home).toHaveTextContent("Listening");
  expect(home).toHaveTextContent("running 1 · queued 1 · ready 1 · max 3");
  expect(within(sets).getByText("busy").closest("li")).toHaveTextContent("Not listening");
  const platform = screen.getByRole("region", { name: "Platform" });
  expect(await within(platform).findByRole("link", { name: "20261005.17" })).toHaveAttribute("href", "/templates/tpl-1");
  expect(await within(platform).findByText("Answering")).toBeInTheDocument();
  expect(within(platform).getByText("1 of 2 listening")).toBeInTheDocument();
  expect(await within(platform).findByText("v0.6.0")).toBeInTheDocument();
});

test("a long queue shows the first ten and links to the rest", async () => {
  const many = Array.from({ length: 13 }, (_, i) => job({ id: `q${i}`, display_name: `job ${i}`, status: "assigned", queued_at: ago(60 - i), started_at: "0001-01-01T00:00:00Z" }));
  mockApi({ "/api/v1/jobs": { jobs: many } });
  renderApp("/");
  const now = await screen.findByRole("region", { name: "Now" });
  expect(await within(now).findAllByRole("listitem")).toHaveLength(10);
  expect(within(now).getByRole("link", { name: "3 more jobs" })).toHaveAttribute("href", "/jobs");
});

test("a quiet fleet says nothing runs or waits", async () => {
  mockApi({});
  renderApp("/");
  expect(await screen.findByText("No job is running or waiting.")).toBeInTheDocument();
  expect(screen.getByText("No job waiting")).toBeInTheDocument();
  expect(screen.getByText("No runner preparing")).toBeInTheDocument();
});

test("a fresh install explains how to send the first job", async () => {
  mockApi({ "/api/v1/scale-sets": { scale_sets: [scaleSet({ name: "homelab" })] } });
  renderApp("/");
  expect(await screen.findByText("No jobs yet")).toBeInTheDocument();
  expect(await screen.findByText(/runs-on: homelab/)).toBeInTheDocument();
});

test("the workflow and branch come from the workflow ref", () => {
  expect(parseWorkflowRef("o/r/.github/workflows/ci.yaml@refs/tags/v1.2")).toEqual({ workflow: "ci", branch: "v1.2" });
  expect(parseWorkflowRef(undefined)).toEqual({});
});
