import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => {
  FakeEventSource.reset();
  sessionStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

const T = "2026-10-07T12:00:00Z";
const ZERO = "0001-01-01T00:00:00Z";
const report = {
  checks: [
    { name: "docker hello-world", ok: true, seconds: 1.2 },
    { name: "blocked 192.168.1.10:8006", ok: true, seconds: 3 },
    { name: "registry cache 10.50.0.3:5000", ok: true, warning: true, detail: "connection refused; jobs will pull from the registries directly", seconds: 0.1 },
  ],
  differences: [
    { kind: "version", name: "Installed Software / Language and Runtime / Node.js", expected: "24.13.0", actual: "24.14.0", explained: false },
    { kind: "extra", name: "Installed Software / Tools / Docker Server", actual: "28.4.0", explained: true, reason: "installed by the ghrm layer" },
    { kind: "version", name: "Installed Software / Tools / jq", expected: "1.7.1", actual: "1.7.2", explained: true, reason: "newer release: built after GitHub's image, the recipe installed the latest" },
  ],
  unexpected: 1,
};
const templates = [
  { id: "tplnew", state: "ready", slim_release: "20261012.3", runner_version: "2.339.0", layer_version: "1", vmid: 951, size_bytes: 1_900_000_000,
    pinned: false, active: false, bootstrap: false, in_use: false, trigger: "slim-release", build_environment_id: "bld1", verify_environment_id: "vfy1",
    report, created_at: T, updated_at: T, activated_at: ZERO },
  { id: "tplold", state: "active", slim_release: "20261005.17", runner_version: "2.338.0", layer_version: "1", vmid: 950, size_bytes: 1_800_000_000,
    pinned: true, active: true, bootstrap: false, in_use: true, trigger: "manual", report: { checks: [], differences: [], unexpected: 0 },
    created_at: T, updated_at: T, activated_at: T },
  { id: "boot", state: "ready", vmid: 949, size_bytes: 0, pinned: false, active: false, bootstrap: true, in_use: false, trigger: "bootstrap",
    created_at: T, updated_at: T, activated_at: ZERO },
];
const settings = { version: "dev", admin_actions: true, proxmox: { template_vmid: 949 }, ingest: {}, capacity: {}, scale_sets: [] };

test("lists versions with their state, badges and inputs", async () => {
  mockApi({ "/api/v1/templates": { templates, building: false, enabled: true }, "/api/v1/settings": settings });
  renderApp("/templates");
  const table = await screen.findByRole("table");
  const rows = within(table).getAllByRole("row");
  expect(rows).toHaveLength(4);
  expect(within(rows[2]!).getByText("active")).toBeInTheDocument();
  expect(within(rows[2]!).getByText("Pinned")).toBeInTheDocument();
  expect(within(rows[2]!).getByText("In use")).toBeInTheDocument();
  expect(within(rows[1]!).getByText("20261012.3")).toBeInTheDocument();
  expect(within(rows[3]!).getByText(/Bootstrap/)).toBeInTheDocument();
  expect(within(table).getByRole("link", { name: /tplnew/ })).toHaveAttribute("href", "/templates/tplnew");
});

test("build now starts a build with the session's CSRF token", async () => {
  const calls = mockApi({
    "/api/v1/templates": { templates, building: false, enabled: true },
    "/api/v1/settings": settings,
    "POST /api/v1/templates/build": () => new Response(JSON.stringify({ ...templates[0], id: "tplx", state: "building" }), { status: 202, headers: { "Content-Type": "application/json" } }),
  });
  const user = userEvent.setup();
  renderApp("/templates");
  await waitFor(() => expect(screen.getByRole("button", { name: "Build now" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "Build now" }));
  await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.url.pathname === "/api/v1/templates/build")).toBe(true));
  const post = calls.find((c) => c.method === "POST")!;
  expect(post.headers.get("X-CSRF-Token")).toBe("csrf-1");
  expect(post.headers.get("Authorization")).toBeNull();
  expect(await screen.findByText("Build started")).toBeInTheDocument();
});

test("a running build disables build now; a conflict is explained", async () => {
  sessionStorage.setItem("ghrm.adminToken", "tok");
  mockApi({
    "/api/v1/templates": { templates, building: true, enabled: true },
    "/api/v1/settings": settings,
    "POST /api/v1/templates/tplnew/activate": () =>
      new Response(JSON.stringify({ detail: "template: only a ready version can be activated" }), { status: 409, headers: { "Content-Type": "application/json" } }),
  });
  const user = userEvent.setup();
  renderApp("/templates");
  expect(await screen.findByRole("button", { name: "Build now" })).toBeDisabled();
  await user.click(await screen.findByRole("button", { name: "Actions for tplnew" }));
  await user.click(await screen.findByRole("menuitem", { name: /Activate/ }));
  expect(await screen.findByText(/only a ready version can be activated/)).toBeInTheDocument();
});

test("explains when builds are not configured", async () => {
  mockApi({ "/api/v1/templates": { templates: [templates[2]], building: false, enabled: false }, "/api/v1/settings": settings });
  renderApp("/templates");
  expect(await screen.findByText(/templates.vmid_range/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Build now" })).toBeDisabled();
});

test("the detail page shows checks and fidelity differences", async () => {
  mockApi({
    "/api/v1/templates/tplnew": templates[0],
    "/api/v1/settings": settings,
    "/api/v1/environments/bld1/logs/build": { entries: [{ offset: 0, time: T, text: "build finished" }], next: 10 },
    "/api/v1/environments/bld1": { id: "bld1", scale_set: "", state: "destroyed", memory_mb: 8192, created_at: T, updated_at: T, state_changed_at: T },
  });
  const user = userEvent.setup();
  renderApp("/templates/tplnew?tab=fidelity");
  expect(await screen.findByText("Installed Software / Language and Runtime / Node.js")).toBeInTheDocument();
  expect(screen.getByText("24.14.0")).toBeInTheDocument();
  expect(screen.getByText(/1 unexpected difference/)).toBeInTheDocument();
  expect(screen.getByText("installed by the ghrm layer")).toBeInTheDocument();
  expect(screen.getByText(/newer release: built after GitHub's image/)).toBeInTheDocument();
  await user.click(screen.getByRole("tab", { name: "Verification" }));
  expect(await screen.findByText("docker hello-world")).toBeInTheDocument();
  const cacheRow = screen.getByText("registry cache 10.50.0.3:5000").closest("tr")!;
  expect(within(cacheRow).getByText(/passed with a warning: connection refused/)).toBeInTheDocument();
  await user.click(screen.getByRole("tab", { name: "Build" }));
  expect(await screen.findByText("build finished")).toBeInTheDocument();
});

test("formatBytes", async () => {
  const { formatBytes } = await import("./templates");
  expect(formatBytes(0)).toBe("—");
  expect(formatBytes(65_536)).toBe("64 KB");
  expect(formatBytes(5 * 1024 ** 2)).toBe("5 MB");
  expect(formatBytes(1_900_000_000)).toBe("1.77 GB");
});

test("an unverified version does not claim to match", async () => {
  mockApi({ "/api/v1/templates/tplb": { ...templates[0], id: "tplb", state: "building", report: undefined }, "/api/v1/settings": settings });
  renderApp("/templates/tplb?tab=fidelity");
  expect(await screen.findByText("Not verified yet")).toBeInTheDocument();
  expect(screen.queryByText("Matches GitHub's software report")).not.toBeInTheDocument();
});

test("a failed template record can be deleted after confirming", async () => {
  const failed = { ...templates[0], id: "tplfail", state: "failed" };
  const calls = mockApi({ "/api/v1/templates/tplfail": failed, "/api/v1/settings": settings, "DELETE /api/v1/templates/tplfail": () => new Response(null, { status: 204 }) });
  const user = userEvent.setup();
  renderApp("/templates/tplfail");
  await user.click(await screen.findByRole("button", { name: "Delete" }));
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByText(/nothing changes on Proxmox/)).toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Delete" }));
  await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url.pathname === "/api/v1/templates/tplfail")).toBe(true));
});

test("a ready template has no Delete action", async () => {
  mockApi({ "/api/v1/templates/tplnew": templates[0], "/api/v1/settings": settings });
  renderApp("/templates/tplnew");
  expect(await screen.findByRole("button", { name: "Actions for tplnew" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
});
