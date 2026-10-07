import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { ApiEvent } from "@/api/client";
import { EventStream, invalidationKeys, type ConnectionState } from "./event-stream";

type Listener = (e: ApiEvent) => void;

interface LiveContextValue {
  state: ConnectionState;
  subscribe: (fn: Listener) => () => void;
}

const LiveContext = createContext<LiveContextValue>({ state: "connecting", subscribe: () => () => {} });

/** Owns the single shared event stream: connection state, listeners and query invalidation. */
export function LiveProvider({
  children,
  createEventSource,
  invalidateEveryMs = 250,
}: {
  children: ReactNode;
  createEventSource?: (url: string) => EventSource;
  invalidateEveryMs?: number;
}) {
  const qc = useQueryClient();
  const [state, setState] = useState<ConnectionState>("connecting");
  const listeners = useRef(new Set<Listener>());
  const [value] = useState<Omit<LiveContextValue, "state">>(() => ({
    subscribe: (fn: Listener) => {
      listeners.current.add(fn);
      return () => listeners.current.delete(fn);
    },
  }));

  useEffect(() => {
    const pending = new Map<string, unknown[]>();
    let timer: ReturnType<typeof setTimeout> | null = null;
    const flush = () => {
      timer = null;
      for (const key of pending.values()) void qc.invalidateQueries({ queryKey: key });
      pending.clear();
    };
    let wasLive = false;
    const stream = new EventStream({
      url: "/api/v1/events/stream",
      createEventSource,
      onState: (s) => {
        setState(s);
        // After a gap the backlog replays, but refresh everything in case it was trimmed.
        if (s === "live" && wasLive) void qc.invalidateQueries();
        if (s === "live") wasLive = true;
      },
      onEvent: (e) => {
        listeners.current.forEach((fn) => fn(e));
        for (const key of invalidationKeys(e)) pending.set(JSON.stringify(key), key);
        timer ??= setTimeout(flush, invalidateEveryMs);
      },
    });
    stream.start();
    return () => {
      stream.stop();
      if (timer) clearTimeout(timer);
    };
  }, [qc, createEventSource, invalidateEveryMs]);

  return <LiveContext.Provider value={{ state, subscribe: value.subscribe }}>{children}</LiveContext.Provider>;
}

export function useConnectionState(): ConnectionState {
  return useContext(LiveContext).state;
}

/** Calls fn for every event on the shared stream. */
export function useLiveEvents(fn: Listener) {
  const { subscribe } = useContext(LiveContext);
  const ref = useRef(fn);
  useEffect(() => {
    ref.current = fn;
  });
  useEffect(() => subscribe((e) => ref.current(e)), [subscribe]);
}
