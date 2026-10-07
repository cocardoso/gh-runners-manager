import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Toasty, TooltipProvider } from "@cloudflare/kumo";
import { LiveLog } from "./live-log";
import { mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

const entry = (offset: number, text: string) => ({ offset, text, time: "2026-10-07T12:00:00Z" });
const create = (u: string) => new FakeEventSource(u) as unknown as EventSource;

function setup(routes = {}) {
  const calls = mockApi({
    "/api/v1/environments/e1/logs/job": (url: URL) =>
      url.searchParams.get("tail") === "true"
        ? { entries: [entry(0, "hello"), entry(6, "\u001b[31mboom\u001b[0m")], next: 12 }
        : { entries: url.searchParams.get("offset") === "0" ? [entry(0, "hello"), entry(6, "boom")] : [], next: 12 },
    "/api/v1/environments/e1/logs/runner": { entries: [entry(0, "runner line")], next: 12 },
    ...routes,
  });
  render(
    <TooltipProvider>
      <Toasty>
        <LiveLog envId="e1" streams={["job", "runner"]} defaultStream="job" createEventSource={create} live />
      </Toasty>
    </TooltipProvider>,
  );
  return calls;
}

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

test("shows the tail and follows it", async () => {
  setup();
  expect(await screen.findByText("hello")).toBeInTheDocument();
  await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));
  act(() => {
    FakeEventSource.last().open();
    FakeEventSource.last().emit(entry(12, "new line"), 12);
  });
  expect(await screen.findByText("new line")).toBeInTheDocument();
});

test("pause stops following", async () => {
  const user = userEvent.setup();
  setup();
  await screen.findByText("hello");
  await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));
  await user.click(screen.getByRole("button", { name: "Pause" }));
  expect(FakeEventSource.last().closed).toBe(true);
  expect(screen.getByRole("button", { name: "Resume" })).toBeInTheDocument();
});

test("search counts matches", async () => {
  const user = userEvent.setup();
  setup();
  await screen.findByText("hello");
  await user.type(screen.getByRole("searchbox", { name: "Search the log" }), "boom");
  expect(await screen.findByText("1 of 1")).toBeInTheDocument();
});

test("download fetches the whole stream", async () => {
  const user = userEvent.setup();
  const calls = setup();
  const created: Blob[] = [];
  URL.createObjectURL = vi.fn((b: Blob) => (created.push(b), "blob:x"));
  URL.revokeObjectURL = vi.fn();
  await screen.findByText("hello");
  await user.click(screen.getByRole("button", { name: "Download" }));
  await waitFor(() => expect(created).toHaveLength(1));
  expect(await created[0]!.text()).toBe("2026-10-07T12:00:00Z\thello\n2026-10-07T12:00:00Z\tboom\n");
  expect(calls.some((c) => c.url.searchParams.get("offset") === "0")).toBe(true);
});

test("switching streams loads the other stream", async () => {
  setup();
  await screen.findByText("hello");
  fireEvent.click(screen.getByRole("tab", { name: "runner" }));
  expect(await screen.findByText("runner line")).toBeInTheDocument();
});

test("a focused search pauses the stream, and jump to latest clears the focus and follows again", async () => {
  const user = userEvent.setup();
  setup();
  await screen.findByText("hello");
  await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));
  await user.type(screen.getByRole("searchbox", { name: "Search the log" }), "boom");
  await screen.findByText("1 of 1");
  expect(FakeEventSource.last().closed).toBe(true);
  await user.click(screen.getByRole("button", { name: /jump to latest/i }));
  await waitFor(() => expect(FakeEventSource.last().closed).toBe(false));
  expect(screen.queryByRole("button", { name: /jump to latest/i })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Pause" })).toBeInTheDocument();
});

test("the download URL is revoked only after the click", async () => {
  const user = userEvent.setup();
  setup();
  let revokedAtClick: boolean | undefined;
  const revoke = vi.fn();
  URL.createObjectURL = vi.fn(() => "blob:x");
  URL.revokeObjectURL = revoke;
  // A revoke in the same task as the click can cancel the download in some browsers.
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {
    queueMicrotask(() => (revokedAtClick = revoke.mock.calls.length > 0));
  });
  await screen.findByText("hello");
  await user.click(screen.getByRole("button", { name: "Download" }));
  await waitFor(() => expect(revoke).toHaveBeenCalledWith("blob:x"));
  expect(revokedAtClick).toBe(false);
  click.mockRestore();
});
