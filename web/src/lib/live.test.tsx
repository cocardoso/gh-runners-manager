import { act, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { FakeEventSource } from "@/test/fake-event-source";
import { LiveProvider, useConnectionState, useLiveEvents } from "./live";
import type { ApiEvent } from "@/api/client";

function State() {
  return <span data-testid="state">{useConnectionState()}</span>;
}

function Collector({ out }: { out: ApiEvent[] }) {
  useLiveEvents((e) => out.push(e));
  return null;
}

describe("LiveProvider", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeEventSource.reset();
  });
  afterEach(() => vi.useRealTimers());

  test("exposes the connection state, fans out events and batches invalidations", () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    const out: ApiEvent[] = [];
    render(
      <QueryClientProvider client={qc}>
        <LiveProvider createEventSource={(u) => new FakeEventSource(u) as unknown as EventSource}>
          <State />
          <Collector out={out} />
        </LiveProvider>
      </QueryClientProvider>,
    );
    expect(screen.getByTestId("state")).toHaveTextContent("connecting");
    act(() => FakeEventSource.last().open());
    expect(screen.getByTestId("state")).toHaveTextContent("live");
    act(() => {
      for (let i = 1; i <= 20; i++) FakeEventSource.last().emit({ seq: i, kind: "job.started", level: "info", message: "", time: "", job_id: "j1" }, i);
    });
    expect(out).toHaveLength(20);
    expect(spy).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(300));
    const keys = spy.mock.calls.map((c) => JSON.stringify(c[0]?.queryKey));
    expect(keys.filter((k) => k === '["jobs"]')).toHaveLength(1);
    expect(keys).toContain('["job","j1"]');
    act(() => FakeEventSource.last().fail());
    expect(screen.getByTestId("state")).toHaveTextContent("reconnecting");
  });
});
