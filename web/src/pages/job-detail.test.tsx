import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { env, job, mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

vi.mock("@/components/resources-chart", () => ({ ResourcesChart: () => <div data-testid="resources-chart" /> }));

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const theJob = job({ id: "j1", display_name: "build (ubuntu)", environment_id: "env-1", run_id: 77, status: "completed", result: "failed", finished_at: "2026-10-07T11:55:00Z" });
const theEnv = env({ id: "env-1", state: "destroyed", job_id: "j1", runtime_ref: "lxc/905", ip: "10.50.0.105", log_streams: [{ environment_id: "env-1", stream: "job", last_seq: 2, bytes: 10, lines: 2, first_at: "", last_at: "" }] as never });

function routes(github: unknown = { available: false, reason: "the token lacks Actions: read", steps: [] }) {
  return {
    "/api/v1/jobs/j1": theJob,
    "/api/v1/environments/env-1": theEnv,
    "/api/v1/jobs/j1/github": github,
    "/api/v1/events": {
      events: [
        { seq: 1, kind: "environment.state", level: "info", message: "", time: "2026-10-07T11:50:05Z", environment_id: "env-1", data: { from: "provisioning", to: "booting" } },
      ],
    },
    "/api/v1/environments/env-1/logs/job": { entries: [{ offset: 0, time: "2026-10-07T11:51:00Z", text: "Run tests" }], next: 10 },
    "/api/v1/environments/env-1/logs/metrics": { entries: [], next: 0 },
  };
}

test("shows the job header and its timeline", async () => {
  mockApi(routes());
  renderApp("/jobs/j1");
  expect(await screen.findByRole("heading", { level: 1, name: "build (ubuntu)" })).toBeInTheDocument();
  expect(await screen.findByText("Booting")).toBeInTheDocument();
  expect(screen.getByText("Queued on GitHub")).toBeInTheDocument();
});

test("the steps tab explains a missing permission", async () => {
  mockApi(routes());
  renderApp("/jobs/j1?tab=steps");
  expect(await screen.findByText(/lacks Actions: read/)).toBeInTheDocument();
});

test("the steps tab lists steps and opens one in the log", async () => {
  mockApi(
    routes({
      available: true,
      url: "https://github.com/octo/app/actions/runs/77/job/5",
      steps: [{ number: 1, name: "Run tests", status: "completed", conclusion: "failure", started_at: "2026-10-07T11:51:00Z", completed_at: "2026-10-07T11:52:00Z" }],
    }),
  );
  const user = userEvent.setup();
  renderApp("/jobs/j1?tab=steps");
  const step = await screen.findByRole("button", { name: /Run tests/ });
  expect(screen.getByRole("link", { name: /View on GitHub/ })).toHaveAttribute("href", "https://github.com/octo/app/actions/runs/77/job/5");
  await user.click(step);
  expect(await screen.findByRole("searchbox", { name: "Search the log" })).toHaveValue("Run tests");
});

test("the logs tab shows the job log", async () => {
  mockApi(routes());
  renderApp("/jobs/j1?tab=logs");
  expect(await screen.findByText("Run tests")).toBeInTheDocument();
});

test("the environment tab shows runtime details", async () => {
  mockApi(routes());
  renderApp("/jobs/j1?tab=environment");
  expect(await screen.findByText("lxc/905")).toBeInTheDocument();
  expect(screen.getByText("10.50.0.105")).toBeInTheDocument();
});

test("an unknown job says so", async () => {
  mockApi();
  renderApp("/jobs/nope");
  expect(await screen.findByText("Job not found")).toBeInTheDocument();
});

test("the permission hint is not repeated when the server already gives it", async () => {
  mockApi(routes({ available: false, reason: "the GitHub credential cannot read workflow runs; grant it the Actions: read permission", steps: [] }));
  renderApp("/jobs/j1?tab=steps");
  const text = (await screen.findByText(/cannot read workflow runs/)).closest("div")!.textContent ?? "";
  expect(text.match(/Actions: read/g)).toHaveLength(1);
});

test("reasons that are not about permissions get no permission hint", async () => {
  mockApi(routes({ available: false, reason: "no workflow run is known for this job", steps: [] }));
  renderApp("/jobs/j1?tab=steps");
  await screen.findByText(/No workflow run is known for this job/);
  expect(screen.queryByText(/Actions: read/)).not.toBeInTheDocument();
});

test("long names wrap instead of overflowing", async () => {
  mockApi({ ...routes(), "/api/v1/jobs/j1": { ...theJob, display_name: "x".repeat(300) } });
  renderApp("/jobs/j1");
  const h1 = await screen.findByRole("heading", { level: 1, name: "x".repeat(300) });
  expect(h1.className).toMatch(/break-words|wrap-anywhere/);
});
