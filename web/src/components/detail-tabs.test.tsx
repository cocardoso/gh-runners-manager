import { screen } from "@testing-library/react";
import { renderApp } from "@/test/render-app";
import { env, mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

test.each([
  ["/jobs", "In progress"],
  ["/jobs?tab=history", "History"],
  ["/environments", "Running"],
  ["/templates?tab=history", "Build history"],
  ["/environments/env-1", "Timeline"],
])("%s puts its content in a tab panel named by the selected tab", async (path, label) => {
  mockApi({ "/api/v1/environments/env-1": env(), "/api/v1/environments/env-1/logs/control-plane": { entries: [], next: 0 } });
  renderApp(path);
  expect(await screen.findByRole("tabpanel", { name: label })).toBeInTheDocument();
});
