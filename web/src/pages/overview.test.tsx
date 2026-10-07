import { screen, within } from "@testing-library/react";
import { renderApp } from "@/test/render-app";
import { emptyOverview, env, job, mockApi, scaleSet } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

vi.mock("@/components/jobs-chart", () => ({ JobsChart: () => <div data-testid="jobs-chart" /> }));

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const busy = {
  ...emptyOverview,
  kpis: { running_jobs: 3, waiting_demand: 2, jobs_24h: 41, success_rate_24h: 0.9512, median_queue_seconds_24h: 12.4 },
  capacity: { ...emptyOverview.capacity, environments_live: 4, memory_committed_mb: 16384 },
  alerts: [
    { level: "error", kind: "listener", message: "listener stopped: 401 Bad credentials", scale_set: "homelab", time: "2026-10-07T11:00:00Z" },
    { level: "brand-new-level", kind: "future", message: "something new", time: "2026-10-07T11:00:00Z" },
  ],
};

test("shows KPIs, capacity, alerts and the live list", async () => {
  mockApi({
    "/api/v1/overview": busy,
    "/api/v1/stats/jobs": { buckets: [{ start: "2026-10-07T11:00:00Z", succeeded: 3, failed: 1, canceled: 0, other: 0 }] },
    "/api/v1/jobs": { jobs: [job()] },
    "/api/v1/environments": { environments: [env({ id: "env-live", state: "running", job_id: "job-1" }), env({ id: "env-gone", state: "destroyed" })] },
  });
  renderApp("/");
  expect(await screen.findByText("95%")).toBeInTheDocument();
  expect(screen.getByText("41")).toBeInTheDocument();
  expect(screen.getByText("12s")).toBeInTheDocument();
  expect(screen.getByText(/401 Bad credentials/)).toBeInTheDocument();
  expect(screen.getByText("something new")).toBeInTheDocument();
  expect(screen.getByText("4 / 6")).toBeInTheDocument();
  const now = screen.getByRole("region", { name: "Now" });
  expect(await within(now).findByRole("link", { name: "env-live" })).toHaveAttribute("href", "/environments/env-live");
  expect(within(now).queryByText("env-gone")).not.toBeInTheDocument();
  expect(screen.getByTestId("jobs-chart")).toBeInTheDocument();
});

test("a fresh install explains how to send the first job", async () => {
  mockApi({ "/api/v1/scale-sets": { scale_sets: [scaleSet({ name: "homelab" })] } });
  renderApp("/");
  expect(await screen.findByText("No jobs yet")).toBeInTheDocument();
  expect(await screen.findByText(/runs-on: homelab/)).toBeInTheDocument();
});
