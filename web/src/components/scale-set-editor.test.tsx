import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const noContent = () => new Response(null, { status: 204 });
const personal = [{ name: "personal", source: "ui", users: [], last4: "1a2b" }];

async function openNew(routes: Record<string, unknown> = {}) {
  const calls = mockApi({ "/api/v1/scale-sets": { scale_sets: [] }, "/api/v1/credentials": { credentials: personal }, ...routes });
  const user = userEvent.setup();
  renderApp("/scale-sets");
  await user.click(await screen.findByRole("button", { name: "New scale set" }));
  return { calls, user, dialog: await screen.findByRole("dialog") };
}

test("the runs-on line shows the name being typed", async () => {
  const { user, dialog } = await openNew();
  await user.type(within(dialog).getByLabelText("Name"), "zeropaper");
  expect(within(dialog).getByText("runs-on: zeropaper")).toBeInTheDocument();
});

test("a bad name or URL is explained where it is typed, and saving says what is missing", async () => {
  const { user, dialog } = await openNew();
  await user.type(within(dialog).getByLabelText("Name"), "Zero Paper");
  await user.type(within(dialog).getByLabelText("Repository or organization URL"), "github.com/octo");
  expect(within(dialog).getByText(/Use lower-case letters, digits and dashes/)).toBeInTheDocument();
  expect(within(dialog).getByText(/Use a URL like https:\/\/github.com\/owner\/repo/)).toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: "Save scale set" })).toBeDisabled();
  expect(within(dialog).getByText(/Fix the name and the URL to save/)).toBeInTheDocument();
});

test("without a credential, one can be added without leaving the form", async () => {
  let creds: unknown[] = [];
  const { user, dialog } = await openNew({
    "/api/v1/credentials": () => ({ credentials: creds }),
    "PUT /api/v1/credentials/home": () => {
      creds = [{ name: "home", source: "ui", users: [], last4: "zzzz" }];
      return noContent();
    },
  });
  await user.type(within(dialog).getByLabelText("Name"), "zeropaper");
  await user.click(await within(dialog).findByRole("button", { name: /Add a credential/ }));
  const credDialog = await screen.findByRole("dialog", { name: "Add a GitHub credential" });
  await user.type(within(credDialog).getByLabelText("Name"), "home");
  await user.type(within(credDialog).getByLabelText("Token"), "github_pat_x");
  await user.click(within(credDialog).getByRole("button", { name: "Save credential" }));
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add a GitHub credential" })).not.toBeInTheDocument());
  expect(within(dialog).getByLabelText("Name")).toHaveValue("zeropaper"); // nothing typed was lost
  expect(await within(dialog).findByText("home")).toBeInTheDocument();
});

test("memory is chosen in GB and failed environments are kept for a chosen time", async () => {
  const { calls, user, dialog } = await openNew({ "PUT /api/v1/scale-sets/big": noContent });
  await user.type(within(dialog).getByLabelText("Name"), "big");
  await user.type(within(dialog).getByLabelText("Repository or organization URL"), "https://github.com/octo/app");
  await user.click(within(dialog).getByRole("combobox", { name: "Memory" }));
  await user.click(await screen.findByRole("option", { name: "16 GB" }));
  await user.click(within(dialog).getByRole("combobox", { name: "Keep a failed environment" }));
  await user.click(await screen.findByRole("option", { name: "1 h" }));
  await user.click(within(dialog).getByRole("button", { name: "Save scale set" }));
  await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
  expect(await calls.find((c) => c.method === "PUT")!.request.json()).toMatchObject({ memory_mb: 16384, keep_on_failure_minutes: 60, credential: "personal" });
});

test("warm runners are saved and never exceed the jobs at a time", async () => {
  const { calls, user, dialog } = await openNew({ "PUT /api/v1/scale-sets/fast": noContent });
  await user.type(within(dialog).getByLabelText("Name"), "fast");
  await user.type(within(dialog).getByLabelText("Repository or organization URL"), "https://github.com/octo/app");
  const warm = within(dialog).getByRole("spinbutton", { name: "Warm runners" });
  await user.clear(warm);
  await user.type(warm, "3");
  expect(within(dialog).getByText("At most the jobs at once (2).")).toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: "Save scale set" })).toBeDisabled();
  await user.clear(warm);
  await user.type(warm, "1");
  await user.click(within(dialog).getByRole("button", { name: "Save scale set" }));
  await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
  expect(await calls.find((c) => c.method === "PUT")!.request.json()).toMatchObject({ warm_runners: 1 });
});

test("a memory size outside the list, set earlier, stays selected", async () => {
  mockApi({
    "/api/v1/scale-sets": { scale_sets: [{ name: "odd", github_id: 1, desired: 0, live: 0, listening: true, waiting_since: "0001-01-01T00:00:00Z", source: "ui", settings: { url: "https://github.com/o/r", credential: "personal", memory_mb: 6144, cores: 2, max_concurrent: 1, keep_on_failure_minutes: 30 } }] },
    "/api/v1/credentials": { credentials: personal },
  });
  const user = userEvent.setup();
  renderApp("/scale-sets");
  await user.click(await screen.findByRole("button", { name: "Edit odd" }));
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByRole("combobox", { name: "Memory" })).toHaveTextContent("6 GB");
  expect(within(dialog).getByRole("combobox", { name: "Keep a failed environment" })).toHaveTextContent("30 min");
});

const targets = {
  targets: [
    { kind: "organization", owner: "acme", full_name: "acme", url: "https://github.com/acme", private: false },
    { kind: "repository", owner: "cocardoso", name: "zeropaper", full_name: "cocardoso/zeropaper", url: "https://github.com/cocardoso/zeropaper", private: true },
  ],
};

test("the repository is picked from what the credential reaches, and names the scale set", async () => {
  const { calls, user, dialog } = await openNew({ "/api/v1/credentials/personal/targets": targets, "PUT /api/v1/scale-sets/zeropaper": noContent });
  await user.click(await within(dialog).findByRole("combobox", { name: "Repository or organization" }));
  await user.click(await screen.findByRole("option", { name: /cocardoso\/zeropaper/ }));
  expect(within(dialog).getByLabelText("Name")).toHaveValue("zeropaper");
  expect(within(dialog).getByText("runs-on: zeropaper")).toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Save scale set" }));
  await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
  expect(await calls.find((c) => c.method === "PUT")!.request.json()).toMatchObject({ url: "https://github.com/cocardoso/zeropaper", credential: "personal" });
});

test("a name already typed is kept when the repository is picked", async () => {
  const { user, dialog } = await openNew({ "/api/v1/credentials/personal/targets": targets });
  await user.type(within(dialog).getByLabelText("Name"), "zp");
  await user.click(await within(dialog).findByRole("combobox", { name: "Repository or organization" }));
  await user.click(await screen.findByRole("option", { name: /acme/ }));
  expect(within(dialog).getByLabelText("Name")).toHaveValue("zp");
});

test("when GitHub cannot list the repositories, the URL is typed and the reason shown", async () => {
  const { user, dialog } = await openNew({
    "/api/v1/credentials/personal/targets": () => new Response(JSON.stringify({ title: "Bad Gateway", status: 502, detail: "github: Resource not accessible by personal access token" }), { status: 502, headers: { "Content-Type": "application/json" } }),
  });
  expect(await within(dialog).findByText(/Resource not accessible/)).toBeInTheDocument();
  await user.type(within(dialog).getByLabelText("Repository or organization URL"), "https://github.com/octo/app");
  expect(within(dialog).getByLabelText("Repository or organization URL")).toHaveValue("https://github.com/octo/app");
});

test("sensitive fields explain themselves", async () => {
  const { dialog } = await openNew({ "/api/v1/credentials/personal/targets": targets });
  expect(within(dialog).getByText("Use in your workflow")).toBeInTheDocument();
  // The help buttons next to the labels.
  expect(within(dialog).getAllByRole("button", { name: "More information" }).length).toBeGreaterThanOrEqual(5);
});

const listed = { "/api/v1/credentials/personal/targets": targets };

test("a URL being typed stays an input, even when it passes through a listed one or is cleared", async () => {
  mockApi({
    ...listed,
    "/api/v1/scale-sets": { scale_sets: [{ name: "odd", github_id: 1, desired: 0, live: 0, listening: true, waiting_since: "0001-01-01T00:00:00Z", source: "ui", settings: { url: "https://github.com/o/r", credential: "personal", memory_mb: 4096, cores: 2, max_concurrent: 1, keep_on_failure_minutes: 0 } }] },
    "/api/v1/credentials": { credentials: personal },
  });
  const user = userEvent.setup();
  renderApp("/scale-sets");
  await user.click(await screen.findByRole("button", { name: "Edit odd" }));
  const dialog = await screen.findByRole("dialog");
  // Not in the list: the URL is shown as typed.
  const input = await within(dialog).findByLabelText("Repository or organization URL");
  await user.clear(input);
  expect(within(dialog).getByLabelText("Repository or organization URL")).toHaveFocus();
  await user.type(within(dialog).getByLabelText("Repository or organization URL"), "https://github.com/acme");
  expect(within(dialog).getByLabelText("Repository or organization URL")).toHaveFocus();
  expect(within(dialog).queryByRole("combobox", { name: "Repository or organization" })).not.toBeInTheDocument();
});

test("going back to the list from a typed URL that is not in it shows the list", async () => {
  const { user, dialog } = await openNew(listed);
  await user.click(await within(dialog).findByRole("button", { name: "Type the URL instead" }));
  await user.type(within(dialog).getByLabelText("Repository or organization URL"), "https://github.com/x/y");
  await user.click(within(dialog).getByRole("button", { name: "Pick from the list" }));
  expect(await within(dialog).findByRole("combobox", { name: "Repository or organization" })).toHaveValue("");
});

test("clearing the picked repository empties it", async () => {
  const { user, dialog } = await openNew(listed);
  await user.click(await within(dialog).findByRole("combobox", { name: "Repository or organization" }));
  await user.click(await screen.findByRole("option", { name: /cocardoso\/zeropaper/ }));
  await user.click(within(dialog).getByRole("button", { name: "Clear selection" }));
  await waitFor(() => expect(within(dialog).getByRole("combobox", { name: "Repository or organization" })).toHaveValue(""));
  expect(within(dialog).getByText(/Fill in the URL to save/)).toBeInTheDocument();
});

test("a suggested name never takes an existing scale set's, and a create never replaces one", async () => {
  const { calls, user, dialog } = await openNew({
    ...listed,
    "/api/v1/scale-sets": { scale_sets: [{ name: "zeropaper", github_id: 1, desired: 0, live: 0, listening: true, waiting_since: "0001-01-01T00:00:00Z", source: "ui" }] },
    "PUT /api/v1/scale-sets/zeropaper-2": noContent,
  });
  await user.type(within(dialog).getByLabelText("Name"), "zeropaper");
  expect(within(dialog).getByText(/A scale set with this name already exists/)).toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: "Save scale set" })).toBeDisabled();
  await user.clear(within(dialog).getByLabelText("Name"));
  await user.click(await within(dialog).findByRole("combobox", { name: "Repository or organization" }));
  await user.click(await screen.findByRole("option", { name: /cocardoso\/zeropaper/ }));
  expect(within(dialog).getByLabelText("Name")).toHaveValue("zeropaper-2"); // a cleared name is suggested again
  await user.click(within(dialog).getByRole("button", { name: "Save scale set" }));
  await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
  expect(calls.find((c) => c.method === "PUT")!.headers.get("If-None-Match")).toBe("*");
});

test("while the list loads, the field is there and the URL can be typed at once", async () => {
  const { user, dialog } = await openNew({ "/api/v1/credentials/personal/targets": () => new Promise(() => {}) });
  expect(await within(dialog).findByText(/Loading what the credential can reach/)).toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Type the URL instead" }));
  await user.type(within(dialog).getByLabelText("Repository or organization URL"), "https://github.com/octo/app");
  expect(within(dialog).getByLabelText("Repository or organization URL")).toHaveValue("https://github.com/octo/app");
});

test("the list can be asked again from GitHub, and says when it is cut", async () => {
  const { calls, user, dialog } = await openNew({ "/api/v1/credentials/personal/targets": { ...targets, truncated: true } });
  expect(await within(dialog).findByText(/Only the first 1,000 repositories are listed/)).toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Reload the list" }));
  await waitFor(() => expect(calls.some((c) => c.url.pathname.endsWith("/targets") && c.url.searchParams.get("refresh") === "true")).toBe(true));
});

test("private repositories are marked for screen readers too", async () => {
  const { user, dialog } = await openNew(listed);
  await user.click(await within(dialog).findByRole("combobox", { name: "Repository or organization" }));
  expect(await screen.findByRole("img", { name: "private" })).toBeInTheDocument();
});

test("the credential select is named by its label alone", async () => {
  const { dialog } = await openNew(listed);
  expect(within(dialog).getByRole("combobox", { name: "Credential" })).toBeInTheDocument();
});
