import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { mockApi } from "@/test/api-mock";
import { dateInput, describeCounts } from "./history-card";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

async function historyCard() {
  return (await screen.findByRole("heading", { name: "History" })).closest("section")!;
}

test("the history card saves the mode and the days", async () => {
  const calls = mockApi({ "/api/v1/history/settings": { mode: "automatic", days: 30, audit_days: 365 } });
  const user = userEvent.setup();
  renderApp("/settings");
  const card = await historyCard();
  await user.click(await within(card).findByRole("radio", { name: "Manual" }));
  const days = within(card).getByLabelText("Keep history (days)");
  await user.clear(days);
  await user.type(days, "7");
  await user.click(within(card).getByRole("button", { name: "Save" }));
  await waitFor(() => expect(calls.some((c) => c.method === "PUT" && c.url.pathname === "/api/v1/history/settings")).toBe(true));
  const put = calls.find((c) => c.method === "PUT")!;
  expect(await put.request.json()).toEqual({ mode: "manual", days: 7, audit_days: 365 });
});

test("clean up now previews the counts before deleting", async () => {
  const calls = mockApi({
    "/api/v1/history/settings": { mode: "manual", days: 30, audit_days: 365 },
    "POST /api/v1/history/cleanup": { environments: 3, jobs: 4, events: 50, audit_events: 0, templates: 1 },
  });
  const user = userEvent.setup();
  renderApp("/settings");
  const card = await historyCard();
  await user.click(await within(card).findByRole("button", { name: /Clean up now/ }));
  const dialog = await screen.findByRole("dialog");
  expect(await within(dialog).findByText(/3 environments/)).toBeInTheDocument();
  const cleanups = () => calls.filter((c) => c.url.pathname === "/api/v1/history/cleanup");
  expect(cleanups()).toHaveLength(1);
  expect((await cleanups()[0]!.request.json()).dry_run).toBe(true);
  await user.click(within(dialog).getByRole("button", { name: "Delete" }));
  await waitFor(() => expect(cleanups()).toHaveLength(2));
  expect((await cleanups()[1]!.request.json()).dry_run).toBeFalsy();
});

test("nothing to delete disables the delete button", async () => {
  mockApi({
    "/api/v1/history/settings": { mode: "automatic", days: 30, audit_days: 365 },
    "POST /api/v1/history/cleanup": { environments: 0, jobs: 0, events: 0, audit_events: 0, templates: 0 },
  });
  const user = userEvent.setup();
  renderApp("/settings");
  const card = await historyCard();
  await user.click(await within(card).findByRole("button", { name: /Clean up now/ }));
  const dialog = await screen.findByRole("dialog");
  expect(await within(dialog).findByText(/Nothing to delete/)).toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: "Delete" })).toBeDisabled();
});

test("dates come from the browser's day, not UTC's", () => {
  const tz = process.env.TZ;
  process.env.TZ = "America/Sao_Paulo";
  try {
    // 22:30 on the 8th in São Paulo is already the 9th in UTC.
    expect(dateInput(new Date("2026-10-09T01:30:00Z"))).toBe("2026-10-08");
  } finally {
    process.env.TZ = tz;
  }
});

test("the cleanup dialog warns that failed builds may run again", async () => {
  mockApi({
    "/api/v1/history/settings": { mode: "automatic", days: 30, audit_days: 365 },
    "POST /api/v1/history/cleanup": { environments: 0, jobs: 0, events: 0, audit_events: 0, templates: 1 },
  });
  const user = userEvent.setup();
  renderApp("/settings");
  const card = await historyCard();
  await user.click(await within(card).findByRole("button", { name: /Clean up now/ }));
  const dialog = await screen.findByRole("dialog");
  expect(await within(dialog).findByText(/may build the same version again/)).toBeInTheDocument();
});

test("a partial cleanup shows its warning", async () => {
  mockApi({
    "/api/v1/history/settings": { mode: "automatic", days: 30, audit_days: 365 },
    "POST /api/v1/history/cleanup": { environments: 1, jobs: 0, events: 0, audit_events: 0, templates: 0, warning: "logs: could not remove env1" },
  });
  const user = userEvent.setup();
  renderApp("/settings");
  const card = await historyCard();
  await user.click(await within(card).findByRole("button", { name: /Clean up now/ }));
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByText(/1 environment/);
  await user.click(within(dialog).getByRole("button", { name: "Delete" }));
  expect(await screen.findByText(/could not remove env1/)).toBeInTheDocument();
});

test("cleanup counts are written in the language's number format", () => {
  expect(describeCounts({ environments: 1, jobs: 0, events: 12345, audit_events: 0, templates: 0 })).toContain("12,345 events");
});

test("the history settings explain themselves", async () => {
  mockApi({ "/api/v1/history/settings": { mode: "automatic", days: 30, audit_days: 365 } });
  renderApp("/settings");
  const card = await historyCard();
  expect(await within(card).findAllByRole("button", { name: "More information" })).toHaveLength(3);
  expect(within(card).getByRole("spinbutton", { name: "Keep history (days)" })).toBeInTheDocument();
  expect(within(card).getByRole("group", { name: "Cleanup" })).toBeInTheDocument();
});
