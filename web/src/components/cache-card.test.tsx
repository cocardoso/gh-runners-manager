import { screen, within } from "@testing-library/react";
import { renderApp } from "@/test/render-app";
import { mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const T = "2026-10-07T12:00:00Z";

test("the cache card shows each origin, the hit ratio and the disk", async () => {
  mockApi({
    "/api/v1/cache": {
      enabled: true,
      address: "10.50.0.3",
      up: false,
      disk_used_bytes: 25 * 2 ** 30,
      disk_budget_bytes: 100 * 2 ** 30,
      checked_at: T,
      origins: [
        { origin: "docker.io", up: true, blob_hits: 90, blob_misses: 10, served_bytes: 1, pulled_bytes: 1 },
        { origin: "ghcr.io", up: false, error: "connection refused", blob_hits: 0, blob_misses: 0, served_bytes: 0, pulled_bytes: 0 },
      ],
    },
  });
  renderApp("/settings");
  const card = (await screen.findByRole("heading", { name: "Registry cache" })).closest("section")!;
  const docker = (await within(card).findByText("docker.io")).closest("tr")!;
  expect(within(docker).getByText("Answering")).toBeInTheDocument();
  expect(within(docker).getByText("90%")).toBeInTheDocument();
  const ghcr = within(card).getByText("ghcr.io").closest("tr")!;
  expect(within(ghcr).getByText(/connection refused/)).toBeInTheDocument();
  expect(within(card).getByText(/25 GB of 100 GB/)).toBeInTheDocument();
  expect(within(card).getByText("10.50.0.3")).toBeInTheDocument();
});

test("the cache card explains when no cache is configured", async () => {
  mockApi({ "/api/v1/cache": { enabled: false, up: false, origins: [], disk_used_bytes: 0, disk_budget_bytes: 0, checked_at: "0001-01-01T00:00:00Z" } });
  renderApp("/settings");
  const card = (await screen.findByRole("heading", { name: "Registry cache" })).closest("section")!;
  expect(await within(card).findByText(/No registry cache is configured/)).toBeInTheDocument();
});

test("the cache card waits for the first check before judging the cache", async () => {
  mockApi({ "/api/v1/cache": { enabled: true, address: "10.50.0.3", up: false, origins: [], disk_used_bytes: 0, disk_budget_bytes: 0, checked_at: "0001-01-01T00:00:00Z" } });
  renderApp("/settings");
  const card = (await screen.findByRole("heading", { name: "Registry cache" })).closest("section")!;
  expect(await within(card).findByText("Checking…")).toBeInTheDocument();
  expect(within(card).queryByText(/Not answering/)).not.toBeInTheDocument();
  expect(within(card).queryByText(/checked/)).not.toBeInTheDocument();
});

test("the cache card says since when the cache is down", async () => {
  mockApi({
    "/api/v1/cache": {
      enabled: true, address: "10.50.0.3", up: false, origins: [], disk_used_bytes: 0, disk_budget_bytes: 0,
      checked_at: T, down_since: "2026-10-07T11:00:00Z",
    },
  });
  renderApp("/settings");
  const card = (await screen.findByRole("heading", { name: "Registry cache" })).closest("section")!;
  expect(await within(card).findByText(/Not answering/)).toBeInTheDocument();
  expect(within(card).getByText(/down since/)).toBeInTheDocument();
});
