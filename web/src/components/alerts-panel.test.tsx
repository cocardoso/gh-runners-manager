import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { emptyOverview, mockApi } from "@/test/api-mock";

vi.mock("@/components/jobs-chart", () => ({ JobsChart: () => <div data-testid="jobs-chart" /> }));

afterEach(() => vi.unstubAllGlobals());

const ago = (s: number) => new Date(Date.now() - s * 1000).toISOString();

const failure = (id: string, scaleSet: string, count: number) => ({
  id,
  level: "error",
  kind: "environment_failed",
  message: `environment failed at start: proxmox task UPID:pve:1:vzstart:900: failed: unable to get PID for CT 900 (${id})`,
  scale_set: scaleSet,
  environment_id: `env-${id}`,
  count,
  first_time: ago(400),
  time: ago(180),
  dismissible: true,
});

test("repeated failures are one alert with a count, a few at a time", async () => {
  const user = userEvent.setup();
  mockApi({
    "/api/v1/overview": {
      ...emptyOverview,
      alerts: [failure("a", "farma-bot", 37), failure("b", "lab", 1), failure("c", "lab", 2), failure("d", "zeropaper", 4)],
    },
  });
  renderApp("/");
  expect(await screen.findByText("37 environments failed at start")).toBeInTheDocument();
  expect(screen.getByText("An environment failed at start")).toBeInTheDocument();
  expect(screen.getAllByRole("link", { name: "Open the latest environment" })[0]).toHaveAttribute("href", "/environments/env-a");
  expect(screen.queryByText("4 environments failed at start")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Show 1 more alert" }));
  expect(screen.getByText("4 environments failed at start")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Show fewer alerts" }));
  expect(screen.queryByText("4 environments failed at start")).not.toBeInTheDocument();
});

test("dismissing an alert hides it and tells the server", async () => {
  const user = userEvent.setup();
  let alerts = [failure("a", "farma-bot", 37), failure("b", "lab", 2)];
  const calls = mockApi({
    "/api/v1/overview": () => ({ ...emptyOverview, alerts }),
    "POST /api/v1/alerts/dismiss": async () => {
      alerts = alerts.filter((a) => a.id !== "a");
      return new Response(null, { status: 204 });
    },
  });
  renderApp("/");
  await user.click(await screen.findByRole("button", { name: "Dismiss: 37 environments failed at start" }));
  await waitFor(() => expect(screen.queryByText("37 environments failed at start")).not.toBeInTheDocument());
  const post = calls.find((c) => c.method === "POST" && c.url.pathname === "/api/v1/alerts/dismiss");
  expect(await post?.request.json()).toEqual({ ids: ["a"] });
  expect(screen.getByText("2 environments failed at start")).toBeInTheDocument();
});

test("dismiss all sends every dismissible alert, and live conditions stay", async () => {
  const user = userEvent.setup();
  const calls = mockApi({
    "/api/v1/overview": {
      ...emptyOverview,
      alerts: [
        { id: "listener:lab", level: "error", kind: "listener", message: "listener stopped: 401", scale_set: "lab", time: ago(5), count: 0, first_time: ago(5), dismissible: false },
        failure("a", "farma-bot", 3),
        failure("b", "lab", 2),
      ],
    },
    "POST /api/v1/alerts/dismiss": new Response(null, { status: 204 }),
  });
  renderApp("/");
  await user.click(await screen.findByRole("button", { name: "Dismiss all 2 alerts" }));
  const post = calls.find((c) => c.method === "POST" && c.url.pathname === "/api/v1/alerts/dismiss");
  expect(await post?.request.json()).toEqual({ ids: ["a", "b"] });
  expect(screen.getByText("listener stopped: 401")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /^Dismiss: listener/ })).not.toBeInTheDocument();
});

test("errors come before warnings", async () => {
  mockApi({
    "/api/v1/overview": {
      ...emptyOverview,
      alerts: [
        { id: "waiting:lab:disk", level: "warn", kind: "waiting", message: "jobs are waiting: disk", scale_set: "lab", time: ago(5), count: 0, first_time: ago(5), dismissible: true },
        failure("a", "farma-bot", 3),
      ],
    },
  });
  renderApp("/");
  const failed = await screen.findByText("3 environments failed at start");
  const waiting = screen.getByText("Jobs are waiting: the disk is almost full");
  expect(failed.compareDocumentPosition(waiting) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});
