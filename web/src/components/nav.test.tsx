import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { emptyOverview, job, mockApi, scaleSet } from "@/test/api-mock";
import { hashId, subActive } from "./nav";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

/** The top-level entries of a sidebar group: links, and the buttons that open a submenu. */
const entries = (el: HTMLElement) =>
  within(el)
    .getAllByRole("listitem")
    .filter((li) => li.parentElement?.closest("li") === null)
    .map((li) => (within(li).queryAllByRole("link")[0] ?? within(li).getAllByRole("button")[0])!.textContent?.trim());

test("the sidebar groups inventory, activity and system pages", async () => {
  mockApi();
  renderApp("/");
  const nav = await screen.findByRole("complementary", { name: "Main navigation" });
  expect(entries(within(nav).getByRole("group", { name: "Inventory" }))).toEqual(["Repositories", "Scale sets", "Templates"]);
  expect(entries(within(nav).getByRole("group", { name: "Activity" }))).toEqual(["Jobs", "Environments", "Events"]);
  expect(entries(within(nav).getByRole("group", { name: "System" }))).toEqual(["Settings"]);
  expect(within(nav).getByRole("link", { name: "Events" })).toHaveAttribute("href", "/logs");
  expect(within(nav).getByRole("link", { name: "Repositories" })).toHaveAttribute("href", "/repositories");
});

test("an item opens to its page's tabs and sections", async () => {
  mockApi();
  const user = userEvent.setup();
  renderApp("/");
  const nav = await screen.findByRole("complementary", { name: "Main navigation" });
  const jobs = within(nav).getByRole("button", { name: /^Jobs/ });
  expect(jobs).toHaveAttribute("aria-expanded", "false");
  await user.click(jobs);
  expect(within(nav).getByRole("link", { name: "In progress" })).toHaveAttribute("href", "/jobs");
  expect(within(nav).getByRole("link", { name: "History" })).toHaveAttribute("href", "/jobs?tab=history");
  await user.click(within(nav).getByRole("button", { name: "Settings" }));
  expect(within(nav).getByRole("link", { name: "Capacity" })).toHaveAttribute("href", "/settings#capacity");
  await user.click(within(nav).getByRole("link", { name: "Capacity" }));
  expect(await screen.findByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
});

test("the scale sets item lists each scale set, open on its page", async () => {
  mockApi({ "/api/v1/scale-sets": { scale_sets: [scaleSet({ name: "farma-bot" }), scaleSet({ name: "zeropaper" })] } });
  renderApp("/scale-sets");
  const nav = await screen.findByRole("complementary", { name: "Main navigation" });
  expect(within(nav).getByRole("button", { name: "Scale sets" })).toHaveAttribute("aria-expanded", "true");
  expect(await within(nav).findByRole("link", { name: "zeropaper" })).toHaveAttribute("href", "/scale-sets#zeropaper");
  expect(within(nav).getByRole("link", { name: "All scale sets" })).toHaveAttribute("href", "/scale-sets");
});

test("the jobs item counts running jobs and flags waiting ones", async () => {
  mockApi({ "/api/v1/overview": { ...emptyOverview, kpis: { ...emptyOverview.kpis, running_jobs: 2, queued_jobs: 1 } } });
  renderApp("/");
  const nav = await screen.findByRole("complementary", { name: "Main navigation" });
  // Named in words: the count and the dot are hidden from screen readers.
  expect(await within(nav).findByRole("button", { name: "Jobs, 2 running, waiting for a runner" })).toBeInTheDocument();
});

test("the item of a page is marked when none of its links is the place shown", async () => {
  mockApi({ "/api/v1/jobs/j1": { job: job({ id: "j1" }) } });
  renderApp("/jobs/j1");
  const nav = await screen.findByRole("complementary", { name: "Main navigation" });
  const jobs = within(nav).getByRole("button", { name: /^Jobs/ });
  expect(jobs).toHaveAttribute("aria-expanded", "true");
  expect(jobs).toHaveAttribute("data-active");
  expect(within(nav).getByRole("link", { name: "In progress" })).not.toHaveAttribute("data-active");
});

test("on its own tab, the link is marked instead of the item", async () => {
  mockApi();
  renderApp("/jobs?tab=history");
  const nav = await screen.findByRole("complementary", { name: "Main navigation" });
  expect(within(nav).getByRole("link", { name: "History" })).toHaveAttribute("data-active");
  expect(within(nav).getByRole("button", { name: /^Jobs/ })).not.toHaveAttribute("data-active");
});

test("an item closed by hand stays closed on its page, and opens again when the page is left and shown again", async () => {
  mockApi();
  const user = userEvent.setup();
  renderApp("/settings");
  const nav = await screen.findByRole("complementary", { name: "Main navigation" });
  const settingsItem = within(nav).getByRole("button", { name: "Settings" });
  expect(settingsItem).toHaveAttribute("data-active");
  await user.click(settingsItem);
  expect(settingsItem).toHaveAttribute("aria-expanded", "false");
  await user.click(within(nav).getByRole("link", { name: "Repositories" }));
  expect(await screen.findByRole("heading", { level: 1, name: "Repositories" })).toBeInTheDocument();
  expect(within(nav).getByRole("button", { name: "Settings" })).toHaveAttribute("aria-expanded", "false");
  await user.click(within(nav).getByRole("button", { name: "Settings" }));
  await user.click(within(nav).getByRole("link", { name: "Capacity" }));
  expect(await screen.findByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
  expect(within(nav).getByRole("button", { name: "Settings" })).toHaveAttribute("aria-expanded", "true");
});

test("the sidebar groups are translated", async () => {
  mockApi();
  renderApp("/", { locale: "pt-BR" });
  const nav = await screen.findByRole("complementary", { name: "Navegação principal" });
  expect(entries(within(nav).getByRole("group", { name: "Inventário" }))).toEqual(["Repositórios", "Scale sets", "Templates"]);
  expect(entries(within(nav).getByRole("group", { name: "Atividade" }))).toEqual(["Jobs", "Ambientes", "Eventos"]);
  expect(entries(within(nav).getByRole("group", { name: "Sistema" }))).toEqual(["Configurações"]);
});

test("the command palette lists the pages with their new names", async () => {
  mockApi();
  const user = userEvent.setup();
  renderApp("/");
  await screen.findByRole("complementary", { name: "Main navigation" });
  await user.keyboard("{Control>}k{/Control}");
  const input = await screen.findByRole("combobox", { name: "Search" });
  await user.type(input, "events");
  expect(await screen.findByRole("option", { name: /Events/ })).toBeInTheDocument();
  await user.clear(input);
  await user.type(input, "repositories");
  expect(await screen.findByRole("option", { name: /Repositories/ })).toBeInTheDocument();
});

test("a link is the place shown by page and tab, or by section", () => {
  const at = (pathname: string, tab?: string, hash = "") => ({ pathname, tab, hash });
  expect(subActive("/jobs", at("/jobs"))).toBe(true);
  expect(subActive("/jobs", at("/jobs", "history"))).toBe(false);
  expect(subActive("/jobs?tab=history", at("/jobs", "history"))).toBe(true);
  expect(subActive("/jobs", at("/jobs/j1"))).toBe(false);
  expect(subActive("/settings#capacity", at("/settings", undefined, "capacity"))).toBe(true);
  expect(subActive("/settings#capacity", at("/settings"))).toBe(false);
  expect(subActive("/scale-sets", at("/scale-sets", undefined, "farma-bot"))).toBe(false);
  expect(hashId("/scale-sets#a%20b")).toBe("a b");
  expect(hashId("/x#100%")).toBe("100%");
  expect(hashId("/x")).toBe("");
});
