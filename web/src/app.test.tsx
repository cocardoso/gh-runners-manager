import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { job, mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";
import { LiveIndicatorView } from "@/components/live-indicator";
import { render } from "@testing-library/react";
import { TooltipProvider } from "@cloudflare/kumo";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const pages: [string, string][] = [
  ["/", "Overview"],
  ["/jobs", "Jobs"],
  ["/environments", "Environments"],
  ["/scale-sets", "Scale sets"],
  ["/templates", "Templates"],
  ["/logs", "Live logs"],
  ["/settings", "Settings"],
];

test.each(pages)("navigating to %s renders the %s page", async (path, title) => {
  mockApi();
  renderApp(path);
  expect(await screen.findByRole("heading", { level: 1, name: title })).toBeInTheDocument();
});

test("the sidebar navigates between pages", async () => {
  mockApi();
  const user = userEvent.setup();
  renderApp("/");
  await screen.findByRole("heading", { level: 1, name: "Overview" });
  const nav = screen.getByRole("complementary", { name: "Main navigation" });
  await user.click(within(nav).getByRole("link", { name: /Jobs/ }));
  expect(await screen.findByRole("heading", { level: 1, name: "Jobs" })).toBeInTheDocument();
});

test("unknown paths show a not-found page", async () => {
  mockApi();
  renderApp("/nope");
  expect(await screen.findByRole("heading", { level: 1, name: "Not found" })).toBeInTheDocument();
});

test("the command palette finds a job and opens it", async () => {
  mockApi({ "/api/v1/jobs": { jobs: [job({ id: "job-42", display_name: "deploy production", run_id: 9911 })] } });
  const user = userEvent.setup();
  renderApp("/");
  await screen.findByRole("heading", { level: 1, name: "Overview" });
  await user.keyboard("{Meta>}k{/Meta}");
  const input = await screen.findByRole("combobox", { name: "Search" });
  await user.type(input, "9911");
  const item = await screen.findByText("deploy production");
  await user.click(item);
  await waitFor(() => expect(screen.queryByRole("combobox", { name: "Search" })).not.toBeInTheDocument());
  expect(await screen.findByRole("heading", { level: 1 })).toBeInTheDocument();
});

test("the live indicator reflects the shared stream", async () => {
  mockApi();
  renderApp("/");
  expect(await screen.findByRole("status", { name: "" })).toHaveTextContent("Connecting");
  act(() => FakeEventSource.last().open());
  expect(screen.getByText("Live")).toBeInTheDocument();
  act(() => FakeEventSource.last().fail());
  expect(screen.getByText("Reconnecting")).toBeInTheDocument();
});

test.each([
  ["connecting", "Connecting"],
  ["live", "Live"],
  ["reconnecting", "Reconnecting"],
] as const)("LiveIndicatorView %s", (state, label) => {
  render(
    <TooltipProvider>
      <LiveIndicatorView state={state} />
    </TooltipProvider>,
  );
  expect(screen.getByText(label)).toBeInTheDocument();
});
