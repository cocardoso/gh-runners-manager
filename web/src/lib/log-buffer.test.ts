import { LogBuffer, LogFollower } from "./log-buffer";
import { FakeEventSource } from "@/test/fake-event-source";
import type { LogEntry } from "@/api/client";

const entry = (offset: number, text = `line ${offset}`): LogEntry => ({ offset, text, time: "2026-10-07T12:00:00Z" });
const range = (from: number, to: number) => Array.from({ length: to - from }, (_, i) => entry(from + i));

describe("LogBuffer", () => {
  test("appends in order and drops duplicates by offset", () => {
    const b = new LogBuffer(10);
    b.append([entry(0), entry(1)]);
    b.append([entry(1), entry(2)]);
    expect(b.lines.map((l) => l.offset)).toEqual([0, 1, 2]);
    expect(b.lastOffset).toBe(2);
  });

  test("caps memory by dropping the oldest lines", () => {
    const b = new LogBuffer(50_000);
    b.append(range(0, 60_000));
    expect(b.lines).toHaveLength(50_000);
    expect(b.lines[0]!.offset).toBe(10_000);
    expect(b.hasEarlier).toBe(true);
    expect(b.dropped).toBe(10_000);
  });

  test("prepending earlier lines trims the newest ones to stay under the cap", () => {
    const b = new LogBuffer(5);
    b.append(range(10, 15));
    b.prepend(range(7, 10));
    expect(b.lines.map((l) => l.offset)).toEqual([7, 8, 9, 10, 11]);
    expect(b.lastOffset).toBe(11);
    b.prepend([entry(11)]);
    expect(b.lines[0]!.offset).toBe(7);
  });

  test("a fresh buffer starting at offset zero has nothing earlier", () => {
    const b = new LogBuffer(5);
    b.append(range(0, 3));
    expect(b.hasEarlier).toBe(false);
    expect(b.firstOffset).toBe(0);
  });
});

describe("LogFollower", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeEventSource.reset();
  });
  afterEach(() => vi.useRealTimers());

  function follower() {
    const got: LogEntry[][] = [];
    const states: string[] = [];
    const f = new LogFollower({
      url: (offset) => `/logs?follow=true&offset=${offset}`,
      createEventSource: (url) => new FakeEventSource(url) as unknown as EventSource,
      onEntries: (e) => got.push(e),
      onState: (s) => states.push(s),
      flushMs: 50,
    });
    return { f, got, states };
  }

  test("batches entries and reports live", () => {
    const { f, got, states } = follower();
    f.start(120);
    expect(FakeEventSource.last().url).toBe("/logs?follow=true&offset=120");
    FakeEventSource.last().open();
    FakeEventSource.last().emit(entry(120), 120);
    FakeEventSource.last().emit(entry(130), 130);
    expect(got).toHaveLength(0);
    vi.advanceTimersByTime(50);
    expect(got).toEqual([[entry(120), entry(130)]]);
    expect(states).toContain("live");
  });

  test("resumes from the last delivered offset after an error", () => {
    const { f, got } = follower();
    f.start(0);
    FakeEventSource.last().open();
    FakeEventSource.last().emit(entry(0), 0);
    FakeEventSource.last().emit(entry(10), 10);
    FakeEventSource.last().fail();
    vi.advanceTimersByTime(1000);
    expect(FakeEventSource.last().url).toBe("/logs?follow=true&offset=10");
    FakeEventSource.last().emit(entry(10), 10);
    FakeEventSource.last().emit(entry(20), 20);
    vi.advanceTimersByTime(50);
    expect(got.flat().map((e) => e.offset)).toEqual([0, 10, 20]);
  });

  test("ends when the server says the environment is gone", () => {
    const { f, got, states } = follower();
    f.start(0);
    const es = FakeEventSource.last();
    es.open();
    es.emit(entry(0), 0);
    es.emit({}, undefined, "end");
    expect(es.closed).toBe(true);
    expect(got.flat()).toHaveLength(1);
    expect(states.at(-1)).toBe("ended");
    vi.advanceTimersByTime(60_000);
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  test("stop closes without reconnecting", () => {
    const { f } = follower();
    f.start(0);
    f.stop();
    FakeEventSource.last().fail();
    vi.advanceTimersByTime(60_000);
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.last().closed).toBe(true);
  });
});

test("LogFollower: an undefined factory falls back to the global EventSource", () => {
  FakeEventSource.reset();
  vi.stubGlobal("EventSource", FakeEventSource);
  const f = new LogFollower({ url: () => "/x", onEntries: () => {}, onState: () => {}, createEventSource: undefined, flushMs: undefined });
  f.start(0);
  expect(FakeEventSource.instances).toHaveLength(1);
  f.stop();
  vi.unstubAllGlobals();
});

describe("LogFollower stale detection", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeEventSource.reset();
  });
  afterEach(() => vi.useRealTimers());

  test("a silent follow is replaced; pings keep it alive", () => {
    const states: string[] = [];
    const f = new LogFollower({ url: (o) => `/l?offset=${o}`, createEventSource: (u) => new FakeEventSource(u) as unknown as EventSource, onEntries: () => {}, onState: (s) => states.push(s), staleAfterMs: 40_000 });
    f.start(0);
    const es = FakeEventSource.last();
    es.open();
    vi.advanceTimersByTime(30_000);
    es.emit({}, undefined, "ping");
    vi.advanceTimersByTime(30_000);
    expect(es.closed).toBe(false);
    vi.advanceTimersByTime(10_001);
    expect(es.closed).toBe(true);
    expect(states.at(-1)).toBe("reconnecting");
    f.stop();
  });
});

test("LogBuffer keeps line numbers stable across trimming and prepending", () => {
  const b = new LogBuffer(3);
  b.append(range(10, 13));
  b.firstLine = 100;
  b.append([entry(13)]);
  expect(b.firstLine).toBe(101);
  b.prepend([entry(9), entry(10)]);
  expect(b.firstLine).toBe(99);
  expect(b.lines.map((l) => l.offset)).toEqual([9, 10, 11]);
});
