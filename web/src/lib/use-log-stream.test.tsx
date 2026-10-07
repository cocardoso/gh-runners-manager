import { act, renderHook, waitFor } from "@testing-library/react";
import { FakeEventSource } from "@/test/fake-event-source";
import { useLogStream } from "./use-log-stream";

const entry = (offset: number) => ({ offset, text: `line ${offset}`, time: "2026-10-07T12:00:00Z" });

function mockFetch(pages: Record<string, unknown>) {
  const calls: string[] = [];
  vi.stubGlobal("fetch", async (req: Request) => {
    const url = new URL(req.url);
    calls.push(url.pathname + url.search);
    const key = Object.keys(pages).find((k) => (url.pathname + url.search).includes(k));
    return new Response(JSON.stringify(key ? pages[key] : { entries: [], next: 0 }), { headers: { "Content-Type": "application/json" } });
  });
  return calls;
}

describe("useLogStream", () => {
  beforeEach(() => FakeEventSource.reset());
  afterEach(() => vi.unstubAllGlobals());

  const create = (u: string) => new FakeEventSource(u) as unknown as EventSource;

  test("loads the tail, then follows from its end", async () => {
    const calls = mockFetch({ "tail=true": { entries: [entry(100), entry(110)], next: 120 } });
    const { result } = renderHook(() => useLogStream({ envId: "e1", stream: "job", follow: true, createEventSource: create, flushMs: 0 }));
    await waitFor(() => expect(result.current.lines).toHaveLength(2));
    expect(calls[0]).toContain("/api/v1/environments/e1/logs/job?tail=true");
    expect(result.current.hasEarlier).toBe(true);
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
    expect(FakeEventSource.last().url).toBe("/api/v1/environments/e1/logs/job?follow=true&offset=110");
    act(() => {
      FakeEventSource.last().open();
      FakeEventSource.last().emit(entry(120), 120);
    });
    await waitFor(() => expect(result.current.lines).toHaveLength(3));
    expect(result.current.state).toBe("live");
  });

  test("pausing closes the stream and resuming continues from the last line", async () => {
    mockFetch({ "tail=true": { entries: [entry(0)], next: 10 } });
    const { result, rerender } = renderHook(({ follow }) => useLogStream({ envId: "e1", stream: "job", follow, createEventSource: create, flushMs: 0 }), {
      initialProps: { follow: true },
    });
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
    act(() => FakeEventSource.last().emit(entry(10), 10));
    await waitFor(() => expect(result.current.lines).toHaveLength(2));
    rerender({ follow: false });
    expect(FakeEventSource.last().closed).toBe(true);
    expect(result.current.state).toBe("paused");
    rerender({ follow: true });
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));
    expect(FakeEventSource.last().url).toContain("offset=10");
  });

  test("load earlier prepends the page before the first line", async () => {
    const calls = mockFetch({ "before=100": { entries: [entry(0), entry(50)], next: 100 }, "tail=true": { entries: [entry(100)], next: 110 } });
    const { result } = renderHook(() => useLogStream({ envId: "e1", stream: "job", follow: false, createEventSource: create, flushMs: 0 }));
    await waitFor(() => expect(result.current.lines).toHaveLength(1));
    await act(() => result.current.loadEarlier());
    expect(calls.at(-1)).toContain("before=100");
    expect(result.current.lines.map((l) => l.offset)).toEqual([0, 50, 100]);
    expect(result.current.hasEarlier).toBe(false);
  });
});
