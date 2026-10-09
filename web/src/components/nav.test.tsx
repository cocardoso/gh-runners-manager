import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { emptyOverview, mockApi, scaleSet } from "@/test/api-mock";
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
  expect(await within(nav).findByLabelText("2 running")).toHaveTextContent("2");
  expect(within(nav).getByRole("img", { name: "Jobs waiting" })).toBeInTheDocument();
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
