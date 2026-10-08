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

const T2 = "2026-10-06T12:00:00Z";
const T3 = "2026-10-05T12:00:00Z";
const past = [
  { id: "tplfail", state: "failed", slim_release: "20261012.3", runner_version: "2.339.0", layer_version: "1", vmid: 952, size_bytes: 0,
    pinned: false, active: false, bootstrap: false, in_use: false, trigger: "slim-release", failure_stage: "verify", failure_reason: "docker hello-world failed",
    created_at: T3, updated_at: T3, activated_at: ZERO },
  { id: "tplgone", state: "deleted", slim_release: "20260928.1", runner_version: "2.337.0", layer_version: "1", vmid: 948, size_bytes: 0,
    pinned: false, active: false, bootstrap: false, in_use: false, trigger: "manual", created_at: T2, updated_at: T, activated_at: T2 },
  { id: "bootold", state: "retired", vmid: 900, size_bytes: 0, pinned: false, active: false, bootstrap: true, in_use: false, trigger: "bootstrap",
    created_at: T3, updated_at: T2, activated_at: T3 },
];
const all = [...templates, ...past];

test("the default tab shows the active template in use and the versions available for rollback", async () => {
  mockApi({ "/api/v1/templates": { templates: all, building: false, enabled: true }, "/api/v1/settings": settings });
  renderApp("/templates");
  const inUse = await screen.findByRole("region", { name: "In use" });
  expect(within(inUse).getByText("tplold")).toBeInTheDocument();
  expect(within(inUse).getByText("20261005.17")).toBeInTheDocument();
  expect(within(inUse).getByText("2.338.0")).toBeInTheDocument();
  expect(within(inUse).getByText("950")).toBeInTheDocument();
  expect(within(inUse).getByText(/Matches GitHub's software report/)).toBeInTheDocument();
  expect(within(inUse).getByRole("link", { name: /details/i })).toHaveAttribute("href", "/templates/tplold");

  const available = screen.getByRole("region", { name: "Available for rollback" });
  const table = within(available).getByRole("table");
  const rows = within(table).getAllByRole("row");
  expect(rows).toHaveLength(3);
  expect(within(rows[1]!).getByRole("link", { name: /tplnew/ })).toHaveAttribute("href", "/templates/tplnew");
  expect(within(rows[1]!).getByText("20261012.3")).toBeInTheDocument();
  expect(within(rows[2]!).getByText(/Bootstrap/)).toBeInTheDocument();
  expect(within(available).getByRole("button", { name: "Actions for tplnew" })).toBeInTheDocument();

  for (const id of ["tplfail", "tplgone", "bootold"]) expect(screen.queryByText(id)).not.toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "In use and available" })).toHaveAttribute("aria-selected", "true");
});

test("without an active template the in-use card explains it", async () => {
  mockApi({ "/api/v1/templates": { templates: [templates[0], past[0]], building: false, enabled: true }, "/api/v1/settings": settings });
  renderApp("/templates");
  const inUse = await screen.findByRole("region", { name: "In use" });
  expect(within(inUse).getByText("No active template")).toBeInTheDocument();
});

test("the build history tab lists failed, deleted and retired versions with their reason", async () => {
  mockApi({ "/api/v1/templates": { templates: all, building: false, enabled: true }, "/api/v1/settings": settings });
  renderApp("/templates?tab=history");
  const table = await screen.findByRole("table");
  const rows = within(table).getAllByRole("row").slice(1);
  expect(rows.map((r) => within(r).getAllByRole("link")[0]!.textContent)).toEqual(["tplgone", "bootold", "tplfail"]);
  expect(within(rows[2]!).getByText("failed")).toBeInTheDocument();
  expect(within(rows[2]!).getByText(/verify: docker hello-world failed/)).toBeInTheDocument();
  expect(within(rows[0]!).getByText(/Replaced/)).toBeInTheDocument();
  expect(within(rows[1]!).getByText(/Retired/)).toBeInTheDocument();
  expect(within(rows[2]!).getByRole("button", { name: "Delete" })).toBeInTheDocument();
  expect(within(rows[0]!).getByRole("button", { name: "Delete" })).toBeInTheDocument();
  expect(within(rows[1]!).queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  for (const id of ["tplnew", "tplold"]) expect(screen.queryByText(id)).not.toBeInTheDocument();
});

test("a failed record is deleted from the history tab", async () => {
  const calls = mockApi({
    "/api/v1/templates": { templates: all, building: false, enabled: true },
    "/api/v1/settings": settings,
    "DELETE /api/v1/templates/tplfail": () => new Response(null, { status: 204 }),
  });
  const user = userEvent.setup();
  const { history } = renderApp("/templates?tab=history");
  const entries = history.length;
  const row = (await screen.findByRole("link", { name: "tplfail" })).closest("tr")!;
  await user.click(within(row).getByRole("button", { name: "Delete" }));
  await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Delete" }));
  await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url.pathname === "/api/v1/templates/tplfail")).toBe(true));
  // Already on the history: Back must not step through the same page again.
  expect(await screen.findByText("Record deleted")).toBeInTheDocument();
  expect(history.length).toBe(entries);
  expect(history.location.search).toContain("tab=history");
});

test("the history tab has an empty state", async () => {
  mockApi({ "/api/v1/templates": { templates, building: false, enabled: true }, "/api/v1/settings": settings });
  renderApp("/templates?tab=history");
  expect(await screen.findByText("No build history")).toBeInTheDocument();
});

test("switching tabs changes ?tab=", async () => {
  mockApi({ "/api/v1/templates": { templates: all, building: false, enabled: true }, "/api/v1/settings": settings });
  const user = userEvent.setup();
  const { history } = renderApp("/templates");
  await user.click(await screen.findByRole("tab", { name: "Build history" }));
  await waitFor(() => expect(history.location.search).toContain("tab=history"));
  expect(await screen.findByRole("link", { name: "tplfail" })).toBeInTheDocument();
  await user.click(screen.getByRole("tab", { name: "In use and available" }));
  await waitFor(() => expect(history.location.search).not.toContain("tab="));
  expect(await screen.findByRole("region", { name: "In use" })).toBeInTheDocument();
});

test("a build in progress has its own card with a link to its log", async () => {
  const running = { ...templates[0], id: "tplbuild", state: "creating", report: undefined, created_at: T };
  mockApi({ "/api/v1/templates": { templates: [running, ...all], building: true, enabled: true }, "/api/v1/settings": settings });
  renderApp("/templates");
  const card = await screen.findByRole("region", { name: "Build in progress" });
  expect(within(card).getByText(/creating/)).toBeInTheDocument();
  expect(within(card).getByRole("link", { name: /build log/i })).toHaveAttribute("href", "/templates/tplbuild");
  expect(within(screen.getByRole("region", { name: "Available for rollback" })).queryByText("tplbuild")).not.toBeInTheDocument();
});

test("the tabs and cards speak Brazilian Portuguese", async () => {
  mockApi({ "/api/v1/templates": { templates: all, building: false, enabled: true }, "/api/v1/settings": settings });
  const user = userEvent.setup();
  renderApp("/templates", { locale: "pt-BR" });
  expect(await screen.findByRole("region", { name: "Em uso" })).toBeInTheDocument();
  expect(screen.getByRole("region", { name: "Disponíveis para rollback" })).toBeInTheDocument();
  await user.click(screen.getByRole("tab", { name: "Histórico de builds" }));
  expect(await screen.findByText(/Substituído/)).toBeInTheDocument();
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

test("a build that the list does not show yet still has its card", async () => {
  mockApi({ "/api/v1/templates": { templates: templates.filter((t) => t.state !== "building"), building: true, enabled: true }, "/api/v1/settings": settings });
  renderApp("/templates");
  const card = await screen.findByRole("region", { name: "Build in progress" });
  expect(within(card).getByText("Starting…")).toBeInTheDocument();
});

test("a template in use opens on its details; a failed build opens on its build log", async () => {
  const active = { ...templates[0], id: "tplact", state: "active", active: true };
  const failed = { ...templates[0], id: "tplbad", state: "failed", failure_stage: "build", failure_reason: "boom" };
  mockApi({ "/api/v1/templates/tplact": active, "/api/v1/templates/tplbad": failed, "/api/v1/settings": settings,
    "/api/v1/environments/bld1/logs/build": { entries: [], next: 0 },
    "/api/v1/environments/bld1": { id: "bld1", scale_set: "", state: "destroyed", memory_mb: 8192, created_at: T, updated_at: T, state_changed_at: T } });
  const first = renderApp("/templates/tplact");
  expect(await screen.findByRole("tab", { name: "Details", selected: true })).toBeInTheDocument();
  first.unmount();
  renderApp("/templates/tplbad");
  expect(await screen.findByRole("tab", { name: "Build", selected: true })).toBeInTheDocument();
});

const noContent = () => new Response(null, { status: 204 });

test("profiles say what each template preinstalls, leaves out and who uses it", async () => {
  mockApi({
    "/api/v1/settings": settings,
    "/api/v1/template-profiles": {
      profiles: [
        { name: "default", remove: [], toolcache: { node: ["22", "24"] }, apt: [], used_by: ["farma-bot"], active_template_id: "tpl1" },
        { name: "lean", remove: ["azure-cli"], toolcache: {}, apt: ["zip"], script: "echo hi", used_by: [] },
      ],
      components: [{ id: "azure-cli", report: ["Azure CLI"] }],
      toolcache_tools: ["go", "node", "python"],
    },
  });
  renderApp("/templates?tab=profiles");
  const def = await screen.findByRole("region", { name: "default" });
  expect(within(def).getByText("farma-bot")).toBeInTheDocument();
  expect(within(def).getByText("Node.js 22, 24")).toBeInTheDocument();
  expect(within(def).getByRole("link", { name: "tpl1" })).toHaveAttribute("href", "/templates/tpl1");
  expect(within(def).getByRole("button", { name: "Delete default" })).toBeDisabled();
  const lean = screen.getByRole("region", { name: "lean" });
  expect(within(lean).getByText("Azure CLI")).toBeInTheDocument();
  expect(within(lean).getByText("zip")).toBeInTheDocument();
  expect(within(lean).getByText(/Not built yet/)).toBeInTheDocument();
  expect(within(lean).getByRole("button", { name: "Delete lean" })).toBeEnabled();
});

test("a new profile is saved with what it leaves out and preinstalls", async () => {
  const calls = mockApi({ "/api/v1/settings": settings, "PUT /api/v1/template-profiles/lean": noContent });
  const user = userEvent.setup();
  renderApp("/templates?tab=profiles");
  await user.click(await screen.findByRole("button", { name: "New profile" }));
  const dialog = await screen.findByRole("dialog");
  await user.type(within(dialog).getByLabelText("Name"), "lean");
  // Leaving out the Azure CLI leaves out the Azure DevOps CLI, which needs it.
  await user.click(within(dialog).getByRole("checkbox", { name: "Azure CLI" }));
  expect(within(dialog).getByRole("checkbox", { name: "Azure CLI (azure-devops)" })).toBeChecked();
  await user.type(within(dialog).getByLabelText("Python"), "3.12");
  await user.type(within(dialog).getByLabelText("Extra Ubuntu packages"), "zip, libpq-dev");
  await user.click(within(dialog).getByRole("button", { name: "Save profile" }));
  await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
  expect(await calls.find((c) => c.method === "PUT")!.request.json()).toEqual({
    remove: ["azure-cli", "azure-devops-cli"],
    toolcache: { go: [], node: [], python: ["3.12"] },
    apt: ["zip", "libpq-dev"],
    script: "",
  });
});

test("versions are shown and built per profile", async () => {
  const calls = mockApi({
    "/api/v1/settings": settings,
    "/api/v1/templates": { templates: [{ ...templates[0], id: "lean1", profile: "lean" }, { ...templates[1], profile: "default" }], building: false, enabled: true },
    "/api/v1/template-profiles": {
      profiles: [
        { name: "default", remove: [], toolcache: {}, apt: [], used_by: [] },
        { name: "lean", remove: [], toolcache: {}, apt: [], used_by: [] },
      ],
      components: [],
      toolcache_tools: [],
    },
    "POST /api/v1/templates/build": { ...templates[0], id: "new", state: "building", profile: "lean" },
  });
  const user = userEvent.setup();
  renderApp("/templates");
  await user.click(await screen.findByRole("combobox", { name: "Profile" }));
  await user.click(await screen.findByRole("option", { name: "lean" }));
  await user.click(screen.getByRole("button", { name: "Build lean" }));
  await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
  expect(await calls.find((c) => c.method === "POST")!.request.json()).toEqual({ profile: "lean" });
});

test("the profile shown stays in the URL, and a new profile never replaces one", async () => {
  const calls = mockApi({
    "/api/v1/settings": settings,
    "/api/v1/templates": { templates: [{ ...templates[0], id: "lean1", profile: "lean" }], building: false, enabled: true },
    "/api/v1/template-profiles": {
      profiles: [
        { name: "default", remove: [], toolcache: {}, apt: [], used_by: [] },
        { name: "lean", remove: [], toolcache: {}, apt: [], used_by: [] },
      ],
      components: [],
      toolcache_tools: [],
    },
    "PUT /api/v1/template-profiles/fresh": noContent,
  });
  const user = userEvent.setup();
  renderApp("/templates?profile=lean");
  expect(await screen.findByRole("button", { name: "Build lean" })).toBeInTheDocument();
  await user.click(screen.getByRole("tab", { name: "Profiles" }));
  await user.click(await screen.findByRole("button", { name: "New profile" }));
  const dialog = await screen.findByRole("dialog");
  await user.type(within(dialog).getByLabelText("Name"), "fresh");
  await user.click(within(dialog).getByRole("button", { name: "Save profile" }));
  await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
  expect(calls.find((c) => c.method === "PUT")!.headers.get("If-None-Match")).toBe("*");
});
