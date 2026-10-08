import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const linkNames = (el: HTMLElement) => within(el).getAllByRole("link").map((a) => a.textContent?.trim());

test("the sidebar groups inventory and activity pages", async () => {
  mockApi();
  renderApp("/");
  const nav = await screen.findByRole("complementary", { name: "Main navigation" });
  const inventory = within(nav).getByRole("group", { name: "Inventory" });
  const activity = within(nav).getByRole("group", { name: "Activity" });
  expect(linkNames(inventory)).toEqual(["Repositories", "Scale sets", "Templates"]);
  expect(linkNames(activity)).toEqual(["Jobs", "Environments", "Events"]);
  expect(linkNames(nav)).toEqual(["Overview", "Repositories", "Scale sets", "Templates", "Jobs", "Environments", "Events", "Settings"]);
  expect(within(activity).getByRole("link", { name: "Events" })).toHaveAttribute("href", "/logs");
  expect(within(inventory).getByRole("link", { name: "Repositories" })).toHaveAttribute("href", "/repositories");
  expect(within(nav).queryByRole("link", { name: "Live logs" })).not.toBeInTheDocument();
});

test("the sidebar groups are translated", async () => {
  mockApi();
  renderApp("/", { locale: "pt-BR" });
  const nav = await screen.findByRole("complementary", { name: "Navegação principal" });
  expect(linkNames(within(nav).getByRole("group", { name: "Inventário" }))).toEqual(["Repositórios", "Scale sets", "Templates"]);
  expect(linkNames(within(nav).getByRole("group", { name: "Atividade" }))).toEqual(["Jobs", "Ambientes", "Eventos"]);
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
