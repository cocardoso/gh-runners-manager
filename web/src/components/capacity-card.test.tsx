import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const capacity = { max_environments: 4, memory_budget_mb: 16384, memory_margin_mb: 4096, max_disk_percent: 85, source: "default", host_memory_total_mb: 40960 };

async function card() {
  return within((await screen.findByRole("heading", { name: "Capacity" })).closest("section")!);
}

test("the capacity limits are edited in GiB and saved in MB", async () => {
  const calls = mockApi({ "/api/v1/capacity": capacity, "PUT /api/v1/capacity": () => new Response(null, { status: 204 }) });
  const user = userEvent.setup();
  renderApp("/settings");
  const c = await card();
  const envs = await c.findByRole("spinbutton", { name: "Environments at once" });
  const budget = c.getByRole("spinbutton", { name: "Memory budget (GiB)" });
  expect(envs).toHaveValue(4);
  expect(budget).toHaveValue(16);
  expect(c.getByText("The host has 40 GB.")).toBeInTheDocument();
  await user.clear(envs);
  await user.type(envs, "6");
  await user.clear(budget);
  await user.type(budget, "48");
  // A budget above the host's memory is allowed, and explained.
  expect(c.getByText(/Above the host's memory \(40 GB\)/)).toBeInTheDocument();
  const margin = c.getByRole("spinbutton", { name: "Host memory margin (GiB)" });
  await user.clear(margin);
  await user.type(margin, "2");
  await user.click(c.getByRole("button", { name: "Save capacity" }));
  await waitFor(() => expect(calls.some((x) => x.method === "PUT")).toBe(true));
  expect(await calls.find((x) => x.method === "PUT")!.request.json()).toEqual({ max_environments: 6, memory_budget_mb: 49152, memory_margin_mb: 2048, max_disk_percent: 85 });
});

test("invalid limits are explained and not saved", async () => {
  mockApi({ "/api/v1/capacity": capacity });
  const user = userEvent.setup();
  renderApp("/settings");
  const c = await card();
  const envs = await c.findByRole("spinbutton", { name: "Environments at once" });
  await user.clear(envs);
  await user.type(envs, "0");
  expect(c.getByText("Between 1 and 100.")).toBeInTheDocument();
  expect(c.getByRole("button", { name: "Save capacity" })).toBeDisabled();
});

test("limits set in ghrm.yaml are shown, not edited", async () => {
  mockApi({ "/api/v1/capacity": { ...capacity, source: "file" } });
  renderApp("/settings");
  const c = await card();
  expect(await c.findByText(/Set in the capacity section of ghrm.yaml/)).toBeInTheDocument();
  expect(c.getByText("16 GB")).toBeInTheDocument();
  expect(c.queryByRole("button", { name: "Save capacity" })).not.toBeInTheDocument();
});
