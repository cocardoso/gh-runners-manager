import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { mockApi, scaleSet } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const settings = {
  version: "v0.3.0",
  admin_actions: true,
  proxmox: { url: "https://pve.example:8006", node: "pve", token_id: "ghrm@pve!ghrm", template_vmid: 950, vmid_range: [900, 948], tls_pinned: true },
  ingest: { listen: ":8081", advertise_url: "http://10.50.0.2:8081" },
  capacity: { max_environments: 6, memory_budget_mb: 24576, memory_margin_mb: 2048, max_disk_percent: 85 },
  scale_sets: [{ name: "homelab", url: "https://github.com/octo/app", credential: "pat", labels: ["homelab", "linux"], max_concurrent: 4, cores: 4, memory_mb: 8192, keep_on_failure_minutes: 0 }],
};

test("scale sets show listener status, counts and a runs-on snippet", async () => {
  mockApi({
    "/api/v1/scale-sets": { scale_sets: [scaleSet({ name: "homelab", desired: 3, live: 1, waiting: "memory_budget" }), scaleSet({ name: "broken", listening: false, listen_error: "401 Bad credentials" })] },
    "/api/v1/settings": settings,
  });
  renderApp("/scale-sets");
  const card = (await screen.findByRole("heading", { name: "homelab" })).closest("section")!;
  expect(within(card).getByText("Listening")).toBeInTheDocument();
  expect(within(card).getByText(/memory_budget/)).toBeInTheDocument();
  expect(within(card).getByText("runs-on: homelab")).toBeInTheDocument();
  expect(within(card).getByText("homelab, linux")).toBeInTheDocument();
  const broken = screen.getByRole("heading", { name: "broken" }).closest("section")!;
  expect(within(broken).getByText(/401 Bad credentials/)).toBeInTheDocument();
});

test("settings show the configuration without secrets", async () => {
  mockApi({ "/api/v1/settings": settings });
  renderApp("/settings");
  expect(await screen.findByText("https://pve.example:8006")).toBeInTheDocument();
  expect(screen.getByText("900–948")).toBeInTheDocument();
  expect(screen.getByText("v0.3.0")).toBeInTheDocument();
});

test("templates explain what is coming", async () => {
  mockApi({ "/api/v1/settings": settings });
  renderApp("/templates");
  expect(await screen.findByText(/950/)).toBeInTheDocument();
});

test("live logs show the recent events and stream new ones, pausable", async () => {
  mockApi({
    "/api/v1/events": { events: [{ seq: 5, kind: "job.started", level: "info", message: "job started: build", time: "2026-10-07T12:00:00Z", job_id: "j1" }] },
  });
  const user = userEvent.setup();
  renderApp("/logs");
  expect(await screen.findByText("job started: build")).toBeInTheDocument();
  act(() => {
    FakeEventSource.last().open();
    FakeEventSource.last().emit({ seq: 6, kind: "environment.failed", level: "error", message: "boom", time: "2026-10-07T12:00:01Z", environment_id: "e1" }, 6);
  });
  expect(await screen.findByText("boom")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Pause" }));
  act(() => FakeEventSource.last().emit({ seq: 7, kind: "brand.new", level: "weird", message: "later", time: "2026-10-07T12:00:02Z" }, 7));
  expect(screen.queryByText("later")).not.toBeInTheDocument();
  expect(screen.getByText(/1 new/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Resume" }));
  expect(await screen.findByText("later")).toBeInTheDocument();
});

test("live logs filter by level", async () => {
  mockApi({
    "/api/v1/events": {
      events: [
        { seq: 1, kind: "job.started", level: "info", message: "fine", time: "2026-10-07T12:00:00Z" },
        { seq: 2, kind: "environment.failed", level: "error", message: "bad", time: "2026-10-07T12:00:00Z" },
      ],
    },
  });
  renderApp("/logs?level=error");
  expect(await screen.findByText("bad")).toBeInTheDocument();
  expect(screen.queryByText("fine")).not.toBeInTheDocument();
});

test("live logs list the newest event first", async () => {
  mockApi({
    "/api/v1/events": {
      events: [
        { seq: 1, kind: "job.started", level: "info", message: "older", time: "2026-10-07T12:00:00Z" },
        { seq: 2, kind: "job.started", level: "info", message: "newer", time: "2026-10-07T12:00:01Z" },
      ],
    },
  });
  renderApp("/logs");
  await screen.findByText("newer");
  const rows = screen.getAllByRole("row").map((r) => r.textContent);
  expect(rows[0]).toContain("newer");
  expect(rows[1]).toContain("older");
});
