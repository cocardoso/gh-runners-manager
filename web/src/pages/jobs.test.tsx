import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { job, mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const jobs = [
  job({ id: "j1", display_name: "build", repository: "octo/app", status: "running" }),
  job({ id: "j2", display_name: "deploy", repository: "octo/site", status: "completed", result: "failed" }),
  job({ id: "j3", display_name: "a".repeat(200), repository: "octo/app", status: "completed", result: "succeeded", scale_set: "docker" }),
];

test("lists jobs with links and statuses", async () => {
  mockApi({ "/api/v1/jobs": { jobs } });
  renderApp("/jobs");
  const table = await screen.findByRole("table");
  expect(within(table).getByRole("link", { name: "build" })).toHaveAttribute("href", "/jobs/j1");
  expect(within(table).getByText("failed")).toBeInTheDocument();
  expect(within(table).getAllByRole("row")).toHaveLength(4);
});

test("filters from the URL and by search", async () => {
  mockApi({ "/api/v1/jobs": { jobs } });
  const user = userEvent.setup();
  renderApp("/jobs?status=failed");
  const table = await screen.findByRole("table");
  expect(within(table).getAllByRole("row")).toHaveLength(2);
  expect(within(table).getByText("deploy")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Clear filters" }));
  await user.type(screen.getByRole("searchbox", { name: "Search jobs" }), "octo/site");
  expect(within(await screen.findByRole("table")).getAllByRole("row")).toHaveLength(2);
});

test("long names truncate", async () => {
  mockApi({ "/api/v1/jobs": { jobs } });
  renderApp("/jobs");
  const link = await screen.findByRole("link", { name: "a".repeat(200) });
  expect(link.className).toContain("truncate");
});

test("paginates", async () => {
  mockApi({ "/api/v1/jobs": { jobs: Array.from({ length: 30 }, (_, i) => job({ id: `j${i}`, display_name: `job ${i}` })) } });
  renderApp("/jobs");
  const table = await screen.findByRole("table");
  expect(within(table).getAllByRole("row")).toHaveLength(26);
});

test("explains an empty list", async () => {
  mockApi();
  renderApp("/jobs");
  expect(await screen.findByText("No jobs yet")).toBeInTheDocument();
});

test("explains when filters match nothing", async () => {
  mockApi({ "/api/v1/jobs": { jobs } });
  renderApp("/jobs?status=canceled");
  expect(await screen.findByText("No job matches these filters")).toBeInTheDocument();
});

test("says when the list is capped", async () => {
  mockApi({ "/api/v1/jobs": { jobs: Array.from({ length: 1000 }, (_, i) => job({ id: `j${i}` })) } });
  renderApp("/jobs");
  expect(await screen.findByText(/newest 1,000 matching jobs/)).toBeInTheDocument();
});
