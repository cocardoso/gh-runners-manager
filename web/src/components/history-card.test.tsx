import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { mockApi } from "@/test/api-mock";
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
