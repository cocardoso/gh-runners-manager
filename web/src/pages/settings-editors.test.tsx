import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { mockApi, scaleSet } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const noContent = () => new Response(null, { status: 204 });
const credentials = {
  credentials: [
    { name: "personal", source: "file", used_by: ["homelab"], token_hint: "abcd" },
    { name: "home", source: "ui", used_by: [], token_hint: "good" },
  ],
};

test("credentials can be added, tested and deleted; file ones are read-only", async () => {
  const calls = mockApi({
    "/api/v1/credentials": credentials,
    "PUT /api/v1/credentials/work": noContent,
    "POST /api/v1/credentials/home/test": { ok: true, login: "octocat" },
    "DELETE /api/v1/credentials/home": noContent,
  });
  const user = userEvent.setup();
  renderApp("/settings");
  const table = await screen.findByRole("table", { name: "GitHub credentials" });
  const fileRow = within(table).getByText("personal").closest("tr")!;
  expect(within(fileRow).getByText("ghrm.yaml")).toBeInTheDocument();
  expect(within(fileRow).getByRole("button", { name: "Delete personal" })).toBeDisabled();
  const uiRow = within(table).getByText("home").closest("tr")!;
  expect(within(uiRow).getByText("…good")).toBeInTheDocument();

  await user.click(within(uiRow).getByRole("button", { name: "Test home" }));
  expect(await screen.findByText(/Works: signed in as octocat/)).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "Add credential" }));
  await user.type(await screen.findByLabelText("Name"), "work");
  await user.type(screen.getByLabelText("Token"), "github_pat_new");
  await user.click(screen.getByRole("button", { name: "Save credential" }));
  await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
  const put = calls.find((c) => c.method === "PUT")!;
  expect(put.url.pathname).toBe("/api/v1/credentials/work");
  expect(await put.request.json()).toEqual({ token: "github_pat_new" });
  expect(put.headers.get("X-CSRF-Token")).toBe("csrf-1");

  await user.click(within(uiRow).getByRole("button", { name: "Delete home" }));
  await user.click(await screen.findByRole("button", { name: "Delete credential" }));
  await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url.pathname === "/api/v1/credentials/home")).toBe(true));
});

test("scale sets can be created, edited and removed; file ones are read-only", async () => {
  const fileSet = scaleSet({ name: "homelab", source: "file", settings: { url: "https://github.com/octo/app", credential: "personal", memory_mb: 4096, cores: 2, max_concurrent: 2 } });
  const uiSet = scaleSet({ name: "big", source: "ui", settings: { url: "https://github.com/octo", credential: "home", labels: ["gpu"], memory_mb: 8192, cores: 4, max_concurrent: 1 } });
  const calls = mockApi({
    "/api/v1/scale-sets": { scale_sets: [fileSet, uiSet] },
    "/api/v1/credentials": credentials,
    "PUT /api/v1/scale-sets/new-set": noContent,
    "PUT /api/v1/scale-sets/big": noContent,
    "DELETE /api/v1/scale-sets/big": noContent,
  });
  const user = userEvent.setup();
  renderApp("/scale-sets");
  const fileCard = (await screen.findByRole("heading", { name: "homelab" })).closest("section")!;
  expect(within(fileCard).getByText(/Defined in ghrm.yaml/)).toBeInTheDocument();
  expect(within(fileCard).queryByRole("button", { name: /Edit/ })).toBeNull();

  await user.click(screen.getByRole("button", { name: "New scale set" }));
  await user.type(await screen.findByLabelText("Name"), "new-set");
  await user.type(screen.getByLabelText("Repository or organization URL"), "https://github.com/octo/other");
  await user.type(screen.getByLabelText("Extra labels"), "linux, big");
  await user.click(screen.getByRole("button", { name: "Save scale set" }));
  await waitFor(() => expect(calls.some((c) => c.method === "PUT" && c.url.pathname === "/api/v1/scale-sets/new-set")).toBe(true));
  const created = await calls.find((c) => c.url.pathname === "/api/v1/scale-sets/new-set")!.request.json();
  expect(created).toMatchObject({ url: "https://github.com/octo/other", credential: "personal", labels: ["linux", "big"], max_concurrent: 2, cores: 2, memory_mb: 4096 });

  const uiCard = screen.getByRole("heading", { name: "big" }).closest("section")!;
  await user.click(within(uiCard).getByRole("button", { name: "Edit big" }));
  const memory = await screen.findByRole("combobox", { name: /^Memory/ });
  expect(memory).toHaveTextContent("8 GB");
  await user.click(memory);
  await user.click(await screen.findByRole("option", { name: "16 GB" }));
  await user.click(screen.getByRole("button", { name: "Save scale set" }));
  await waitFor(() => expect(calls.some((c) => c.method === "PUT" && c.url.pathname === "/api/v1/scale-sets/big")).toBe(true));
  expect(await calls.find((c) => c.url.pathname === "/api/v1/scale-sets/big")!.request.json()).toMatchObject({ memory_mb: 16384, credential: "home", labels: ["gpu"] });

  await user.click(within(uiCard).getByRole("button", { name: "Remove big" }));
  await user.type(await screen.findByRole("textbox", { name: "Type big to confirm deletion" }), "big");
  await user.click(screen.getByRole("button", { name: "Remove scale set" }));
  await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
});

test("a token is checked in the dialog before it is saved, and GitHub's page to create one is linked", async () => {
  const calls = mockApi({
    "/api/v1/credentials": { credentials: [] },
    "POST /api/v1/credentials/check": { ok: true, login: "octocat", repositories: 3, organizations: 1 },
  });
  const user = userEvent.setup();
  renderApp("/settings");
  await user.click(await screen.findByRole("button", { name: "Add credential" }));
  const dialog = await screen.findByRole("dialog");
  const create = within(dialog).getByRole("link", { name: /Create a token on GitHub/ });
  expect(create.getAttribute("href")).toBe("https://github.com/settings/personal-access-tokens/new");
  expect(create.getAttribute("target")).toBe("_blank");
  await user.type(within(dialog).getByLabelText("Token"), "github_pat_x");
  await user.click(within(dialog).getByRole("button", { name: "Test token" }));
  expect(await within(dialog).findByText(/octocat/)).toBeInTheDocument();
  expect(within(dialog).getByText(/3 repositories, 1 organization/)).toBeInTheDocument();
  expect(await calls.find((c) => c.url.pathname === "/api/v1/credentials/check")!.request.json()).toEqual({ token: "github_pat_x" });
});

test("a token that works but cannot list its repositories says so, not a plain success", async () => {
  mockApi({
    "/api/v1/credentials": { credentials: [] },
    "POST /api/v1/credentials/check": { ok: true, login: "octocat", repositories: 0, organizations: 0, truncated: false, error: "Resource not accessible by personal access token" },
  });
  const user = userEvent.setup();
  renderApp("/settings");
  await user.click(await screen.findByRole("button", { name: "Add credential" }));
  const dialog = await screen.findByRole("dialog");
  await user.type(within(dialog).getByLabelText("Token"), "github_pat_x");
  await user.click(within(dialog).getByRole("button", { name: "Test token" }));
  const status = await within(dialog).findByRole("status");
  expect(status).toHaveTextContent(/octocat/);
  expect(status).toHaveTextContent(/could not list its repositories: Resource not accessible/);
});

test("a token check that answers after the token changed is dropped", async () => {
  let answer: (r: Response) => void = () => {};
  mockApi({
    "/api/v1/credentials": { credentials: [] },
    "POST /api/v1/credentials/check": () => new Promise<Response>((r) => (answer = r)),
  });
  const user = userEvent.setup();
  renderApp("/settings");
  await user.click(await screen.findByRole("button", { name: "Add credential" }));
  const dialog = await screen.findByRole("dialog");
  await user.type(within(dialog).getByLabelText("Token"), "github_pat_old");
  await user.click(within(dialog).getByRole("button", { name: "Test token" }));
  await user.type(within(dialog).getByLabelText("Token"), "x");
  answer(new Response(JSON.stringify({ ok: true, login: "octocat", repositories: 1, organizations: 0, truncated: false }), { headers: { "Content-Type": "application/json" } }));
  await new Promise((r) => setTimeout(r, 50));
  expect(within(dialog).queryByText(/octocat/)).not.toBeInTheDocument();
});

test("the credential dialog's fields are named by their labels alone", async () => {
  mockApi({ "/api/v1/credentials": { credentials: [] } });
  const user = userEvent.setup();
  renderApp("/settings");
  await user.click(await screen.findByRole("button", { name: "Add credential" }));
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByRole("textbox", { name: "Name" })).toBeInTheDocument();
});
