import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Job } from "@/api/client";
import { renderApp } from "@/test/render-app";
import { job, mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const T = "2026-10-07T11:50:00Z";
const ZERO = "0001-01-01T00:00:00Z";

const jobs = [
  job({ id: "j1", display_name: "build", repository: "octo/app", status: "running" }),
  job({ id: "j4", display_name: "lint", repository: "octo/app", status: "assigned", started_at: ZERO }),
  job({ id: "j2", display_name: "deploy", repository: "octo/site", status: "completed", result: "failed", finished_at: "2026-10-07T11:55:00Z" }),
  job({
    id: "j3",
    display_name: "a".repeat(200),
    repository: "octo/app",
    status: "completed",
    result: "succeeded",
    scale_set: "docker",
    finished_at: "2026-10-07T11:58:00Z",
  }),
];

/** Answers /api/v1/jobs like the server: one exact status, one exact scale set. */
function jobsRoute(list: Job[]) {
  return (url: URL) => {
    const status = url.searchParams.get("status");
    const set = url.searchParams.get("scale_set");
    return { jobs: list.filter((j) => (!status || j.status === status) && (!set || j.scale_set === set)) };
  };
}

test("the default tab lists only assigned and running jobs", async () => {
  const calls = mockApi({ "/api/v1/jobs": jobsRoute(jobs) });
  renderApp("/jobs");
  expect(await screen.findByRole("tab", { name: "In progress", selected: true })).toBeInTheDocument();
  const table = await screen.findByRole("table");
  expect(within(table).getByRole("link", { name: "build" })).toHaveAttribute("href", "/jobs/j1");
  expect(within(table).getByRole("link", { name: "lint" })).toBeInTheDocument();
  expect(within(table).queryByText("deploy")).not.toBeInTheDocument();
  expect(within(table).getAllByRole("row")).toHaveLength(3);
  expect(within(table).getByRole("columnheader", { name: "Running for" })).toBeInTheDocument();
  const statuses = calls.filter((c) => c.url.pathname === "/api/v1/jobs").map((c) => c.url.searchParams.get("status"));
  expect(statuses).toEqual(expect.arrayContaining(["assigned", "running"]));
  expect(statuses).not.toContain("completed");
});

test("explains when no job is in progress and links to the history", async () => {
  mockApi({ "/api/v1/jobs": jobsRoute(jobs.filter((j) => j.status === "completed")) });
  renderApp("/jobs");
  expect(await screen.findByText("No job in progress")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "See the job history" })).toHaveAttribute("href", "/jobs?tab=history");
});

test("the history tab lists completed jobs with their result, newest first", async () => {
  const calls = mockApi({ "/api/v1/jobs": jobsRoute(jobs) });
  renderApp("/jobs?tab=history");
  expect(await screen.findByRole("tab", { name: "History", selected: true })).toBeInTheDocument();
  const table = await screen.findByRole("table");
  const rows = within(table).getAllByRole("row");
  expect(rows).toHaveLength(3);
  expect(within(rows[1]!).getByRole("link", { name: "a".repeat(200) })).toBeInTheDocument();
  expect(within(rows[1]!).getByText("succeeded")).toBeInTheDocument();
  expect(within(rows[2]!).getByText("failed")).toBeInTheDocument();
  expect(within(table).queryByText("build")).not.toBeInTheDocument();
  expect(within(table).getByRole("columnheader", { name: "Result" })).toBeInTheDocument();
  expect(within(table).getByRole("columnheader", { name: "Finished" })).toBeInTheDocument();
  expect(calls.some((c) => c.url.pathname === "/api/v1/jobs" && c.url.searchParams.get("status") === "completed")).toBe(true);
});

test("the history tab filters by result from the URL and by search", async () => {
  mockApi({ "/api/v1/jobs": jobsRoute(jobs) });
  const user = userEvent.setup();
  const { history } = renderApp("/jobs?tab=history&status=failed");
  const table = await screen.findByRole("table");
  expect(within(table).getAllByRole("row")).toHaveLength(2);
  expect(within(table).getByText("deploy")).toBeInTheDocument();
  expect(screen.getByRole("combobox", { name: "Result" })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Clear filters" }));
  expect(history.location.search).toBe("?tab=history");
  await user.type(screen.getByRole("searchbox", { name: "Search jobs" }), "octo/site");
  expect(within(await screen.findByRole("table")).getAllByRole("row")).toHaveLength(2);
});

test("switching tabs changes ?tab=", async () => {
  mockApi({ "/api/v1/jobs": jobsRoute(jobs) });
  const user = userEvent.setup();
  const { history } = renderApp("/jobs");
  await user.click(await screen.findByRole("tab", { name: "History" }));
  expect(history.location.search).toBe("?tab=history");
  expect(await screen.findByText("deploy")).toBeInTheDocument();
  await user.click(screen.getByRole("tab", { name: "In progress" }));
  expect(history.location.search).toBe("");
  expect(await screen.findByText("build")).toBeInTheDocument();
});

test("long names truncate", async () => {
  mockApi({ "/api/v1/jobs": jobsRoute(jobs) });
  renderApp("/jobs?tab=history");
  const link = await screen.findByRole("link", { name: "a".repeat(200) });
  expect(link.className).toContain("truncate");
});

test("paginates", async () => {
  mockApi({
    "/api/v1/jobs": jobsRoute(Array.from({ length: 30 }, (_, i) => job({ id: `j${i}`, display_name: `job ${i}`, status: "completed", result: "succeeded", finished_at: T }))),
  });
  renderApp("/jobs?tab=history");
  const table = await screen.findByRole("table");
  expect(within(table).getAllByRole("row")).toHaveLength(26);
});

test("explains an empty history", async () => {
  mockApi();
  renderApp("/jobs?tab=history");
  expect(await screen.findByText("No finished jobs yet")).toBeInTheDocument();
});

test("explains when filters match nothing", async () => {
  mockApi({ "/api/v1/jobs": jobsRoute(jobs) });
  renderApp("/jobs?tab=history&status=canceled");
  expect(await screen.findByText("No job matches these filters")).toBeInTheDocument();
});

test("says when the history is capped", async () => {
  mockApi({ "/api/v1/jobs": jobsRoute(Array.from({ length: 1000 }, (_, i) => job({ id: `j${i}`, status: "completed", result: "succeeded" }))) });
  renderApp("/jobs?tab=history");
  expect(await screen.findByText(/newest 1,000 matching jobs/)).toBeInTheDocument();
});

test("speaks Brazilian Portuguese", async () => {
  mockApi({ "/api/v1/jobs": jobsRoute(jobs) });
  const user = userEvent.setup();
  renderApp("/jobs", { locale: "pt-BR" });
  expect(await screen.findByRole("tab", { name: "Em andamento", selected: true })).toBeInTheDocument();
  expect(within(await screen.findByRole("table")).getByRole("columnheader", { name: "Executando há" })).toBeInTheDocument();
  await user.click(screen.getByRole("tab", { name: "Histórico" }));
  expect(await screen.findByRole("columnheader", { name: "Resultado" })).toBeInTheDocument();
  expect(screen.getByRole("columnheader", { name: "Concluído" })).toBeInTheDocument();
});

test("a job that moves from assigned to running between answers shows once", async () => {
  mockApi({
    "/api/v1/jobs": (url: URL) => ({
      jobs: url.searchParams.get("status") === "assigned" ? [job({ id: "j9", display_name: "test", status: "assigned" })] : [job({ id: "j9", display_name: "test", status: "running" })],
    }),
  });
  renderApp("/jobs");
  const table = await screen.findByRole("table");
  expect(within(table).getAllByRole("row")).toHaveLength(2);
});

test("jobs can be shown as cards, in both tabs", async () => {
  mockApi({ "/api/v1/jobs": jobsRoute(jobs) });
  const user = userEvent.setup();
  renderApp("/jobs");
  await screen.findByRole("link", { name: "build" });
  await user.click(screen.getByRole("button", { name: "Cards" }));
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
  const build = screen.getByRole("article", { name: "build" });
  expect(within(build).getByText("Running for")).toBeInTheDocument();
  expect(within(build).getByText(/octo\/app/)).toBeInTheDocument();
  await user.click(screen.getByRole("tab", { name: "History" }));
  const deploy = await screen.findByRole("article", { name: "deploy" });
  expect(within(deploy).getByText("Duration")).toBeInTheDocument();
});
