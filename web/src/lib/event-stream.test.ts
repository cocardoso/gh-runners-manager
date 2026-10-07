import { FakeEventSource } from "@/test/fake-event-source";
import { EventStream, type ConnectionState, invalidationKeys } from "./event-stream";
import type { ApiEvent } from "@/api/client";

function ev(seq: number, kind: string, extra: Partial<ApiEvent> = {}): ApiEvent {
  return { seq, kind, level: "info", message: kind, time: "2026-10-07T12:00:00Z", ...extra };
}

describe("EventStream", () => {
  let states: ConnectionState[];
  let received: ApiEvent[];
  let stream: EventStream;

  beforeEach(() => {
    vi.useFakeTimers();
    FakeEventSource.reset();
    states = [];
    received = [];
    stream = new EventStream({
      url: "/api/v1/events/stream",
      createEventSource: (url) => new FakeEventSource(url) as unknown as EventSource,
      onEvent: (e) => received.push(e),
      onState: (s) => states.push(s),
      staleAfterMs: 40_000,
    });
  });

  afterEach(() => {
    stream.stop();
    vi.useRealTimers();
  });

  test("starts at the latest event and goes live on open", () => {
    stream.start();
    expect(FakeEventSource.last().url).toBe("/api/v1/events/stream?after=latest");
    expect(states).toEqual(["connecting"]);
    FakeEventSource.last().open();
    expect(states.at(-1)).toBe("live");
  });

  test("reconnects with backoff and resumes after the last sequence", () => {
    stream.start();
    const first = FakeEventSource.last();
    first.open();
    first.emit(ev(7, "job.started"), 7);
    first.fail();
    expect(first.closed).toBe(true);
    expect(states.at(-1)).toBe("reconnecting");
    vi.advanceTimersByTime(999);
    expect(FakeEventSource.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.last().url).toBe("/api/v1/events/stream?after=7");
    FakeEventSource.last().fail();
    vi.advanceTimersByTime(1999);
    expect(FakeEventSource.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances).toHaveLength(3);
    FakeEventSource.last().open();
    expect(states.at(-1)).toBe("live");
    // A successful open resets the backoff.
    FakeEventSource.last().fail();
    vi.advanceTimersByTime(1000);
    expect(FakeEventSource.instances).toHaveLength(4);
  });

  test("backoff is capped", () => {
    stream.start();
    for (let i = 0; i < 10; i++) {
      FakeEventSource.last().fail();
      vi.advanceTimersByTime(30_000);
    }
    const before = FakeEventSource.instances.length;
    FakeEventSource.last().fail();
    vi.advanceTimersByTime(30_000);
    expect(FakeEventSource.instances.length).toBe(before + 1);
  });

  test("drops duplicate and out-of-order events after a resume", () => {
    stream.start();
    const es = FakeEventSource.last();
    es.open();
    es.emit(ev(1, "job.started"), 1);
    es.emit(ev(2, "job.completed"), 2);
    es.emit(ev(2, "job.completed"), 2);
    es.emit(ev(1, "job.started"), 1);
    expect(received.map((e) => e.seq)).toEqual([1, 2]);
  });

  test("a silent connection is treated as stale and replaced", () => {
    stream.start();
    const es = FakeEventSource.last();
    es.open();
    vi.advanceTimersByTime(30_000);
    es.emit({}, undefined, "ping");
    vi.advanceTimersByTime(30_000);
    expect(es.closed).toBe(false);
    vi.advanceTimersByTime(10_001);
    expect(es.closed).toBe(true);
    expect(states.at(-1)).toBe("reconnecting");
  });

  test("passes unknown kinds through untouched", () => {
    stream.start();
    const es = FakeEventSource.last();
    es.open();
    es.emit({ ...ev(3, "brand.new_kind"), level: "mystery", extra: 1 }, 3);
    expect(received[0]).toMatchObject({ kind: "brand.new_kind", level: "mystery", extra: 1 });
  });

  test("ignores malformed payloads", () => {
    stream.start();
    const es = FakeEventSource.last();
    es.open();
    es.onmessage?.(new MessageEvent("message", { data: "not json" }));
    es.emit({ nope: true });
    expect(received).toEqual([]);
  });

  test("stop closes the connection and cancels reconnects", () => {
    stream.start();
    FakeEventSource.last().fail();
    stream.stop();
    vi.advanceTimersByTime(60_000);
    expect(FakeEventSource.instances).toHaveLength(1);
  });
});

describe("invalidationKeys", () => {
  test("maps kinds to the queries they affect", () => {
    expect(invalidationKeys(ev(1, "job.completed", { job_id: "j1" }))).toEqual(
      expect.arrayContaining([["jobs"], ["job", "j1"], ["overview"], ["stats"]]),
    );
    expect(invalidationKeys(ev(1, "environment.state", { environment_id: "e1" }))).toEqual(
      expect.arrayContaining([["environments"], ["environment", "e1"], ["overview"], ["scale-sets"]]),
    );
    expect(invalidationKeys(ev(1, "scaleset.demand"))).toEqual(expect.arrayContaining([["scale-sets"], ["overview"]]));
  });

  test("unknown kinds refresh only the overview", () => {
    expect(invalidationKeys(ev(1, "something.else"))).toEqual([["overview"]]);
  });
});

test("an undefined factory falls back to the global EventSource", () => {
  FakeEventSource.reset();
  vi.stubGlobal("EventSource", FakeEventSource);
  const s = new EventStream({ url: "/x", onEvent: () => {}, onState: () => {}, createEventSource: undefined });
  s.start();
  expect(FakeEventSource.instances).toHaveLength(1);
  s.stop();
  vi.unstubAllGlobals();
});
