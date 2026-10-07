import { fireEvent, render, screen } from "@testing-library/react";
import { LogView } from "./log-view";
import type { LogEntry } from "@/api/client";

const lines = (n: number, text = (i: number) => `line ${i}`): LogEntry[] =>
  Array.from({ length: n }, (_, i) => ({ offset: i * 10, time: "2026-10-07T12:00:00.123Z", text: text(i) }));

const base = {
  follow: false,
  onFollowChange: () => {},
  wrap: false,
  showTimestamps: false,
  showLineNumbers: true,
  initialRect: { width: 800, height: 400 },
};

test("renders 50k lines quickly by only mounting the visible rows", () => {
  const start = performance.now();
  render(<LogView {...base} lines={lines(50_000)} />);
  expect(performance.now() - start).toBeLessThan(2000);
  expect(screen.getAllByRole("row").length).toBeLessThan(200);
  expect(screen.getByText("line 0")).toBeInTheDocument();
});

test("colours ANSI output", () => {
  render(<LogView {...base} lines={lines(1, () => "\u001b[31mfailed\u001b[0m ok")} />);
  expect(screen.getByText("failed")).toHaveClass("ansi-fg-red");
});

test("highlights search matches", () => {
  render(<LogView {...base} lines={lines(3, (i) => (i === 1 ? "an Error here" : "fine"))} search="error" />);
  expect(screen.getByText("Error").tagName).toBe("MARK");
});

test("scrolling up while following pauses, reaching the bottom resumes", () => {
  const onFollowChange = vi.fn();
  render(<LogView {...base} follow lines={lines(100)} onFollowChange={onFollowChange} />);
  const scroller = screen.getByRole("log");
  Object.defineProperty(scroller, "scrollHeight", { configurable: true, value: 2000 });
  Object.defineProperty(scroller, "clientHeight", { configurable: true, value: 400 });
  scroller.scrollTop = 500;
  fireEvent.scroll(scroller);
  expect(onFollowChange).toHaveBeenLastCalledWith(false);
});

test("a paused view offers to jump to the latest line", () => {
  const onFollowChange = vi.fn();
  render(<LogView {...base} lines={lines(10)} onFollowChange={onFollowChange} canFollow />);
  fireEvent.click(screen.getByRole("button", { name: /jump to latest/i }));
  expect(onFollowChange).toHaveBeenCalledWith(true);
});

test("offers to load earlier lines", () => {
  const onLoadEarlier = vi.fn();
  render(<LogView {...base} lines={lines(10)} hasEarlier onLoadEarlier={onLoadEarlier} />);
  fireEvent.click(screen.getByRole("button", { name: /load earlier/i }));
  expect(onLoadEarlier).toHaveBeenCalled();
});

test("styles GitHub Actions workflow commands", () => {
  render(<LogView {...base} lines={lines(2, (i) => (i ? "##[error]Process completed with exit code 1." : "##[group]Run npm test"))} />);
  expect(screen.getByText("Process completed with exit code 1.")).toHaveClass("text-kumo-danger");
  expect(screen.getByText("Run npm test")).toHaveClass("font-semibold");
});
