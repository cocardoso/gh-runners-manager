import { useCallback, useEffect, useRef, useState } from "react";
import { api, unwrap, type LogEntry } from "@/api/client";
import { LogBuffer, LogFollower, type FollowState } from "./log-buffer";

export type LogStreamName = "control-plane" | "runtime" | "agent" | "runner" | "job" | "metrics";
export type LogStreamState = "loading" | "paused" | "error" | FollowState;

export interface UseLogStreamOptions {
  envId: string;
  stream: LogStreamName;
  /** Follow new lines live. When false the stream is closed and the view stays still. */
  follow: boolean;
  pageSize?: number;
  cap?: number;
  createEventSource?: (url: string) => EventSource;
  flushMs?: number;
}

export interface LogStreamView {
  lines: LogEntry[];
  state: LogStreamState;
  error?: string;
  hasEarlier: boolean;
  /** Lines dropped from the front to respect the memory cap. */
  dropped: number;
  loadingEarlier: boolean;
  loadEarlier: () => Promise<void>;
}

async function readPage(envId: string, stream: LogStreamName, before: number, limit: number) {
  return unwrap(
    await api.GET("/api/v1/environments/{id}/logs/{stream}", {
      params: { path: { id: envId, stream }, query: { tail: true, before, limit } },
    }),
  );
}

/** A log stream: the last page first, then live lines over SSE, capped in memory. */
export function useLogStream({ envId, stream, follow, pageSize = 5000, cap, createEventSource, flushMs }: UseLogStreamOptions): LogStreamView {
  const key = `${envId}/${stream}/${pageSize}/${cap ?? ""}`;
  const buffer = useRef<LogBuffer | null>(null);
  const end = useRef(-1);
  const [current, setCurrent] = useState(key);
  const [snap, setSnap] = useState<{ lines: LogEntry[]; dropped: number }>({ lines: [], dropped: 0 });
  const [loaded, setLoaded] = useState(false);
  const [followState, setFollowState] = useState<FollowState>("connecting");
  const [error, setError] = useState<string>();
  const [loadingEarlier, setLoadingEarlier] = useState(false);

  // A different stream starts from scratch (state reset during render, not in an effect).
  if (current !== key) {
    setCurrent(key);
    setSnap({ lines: [], dropped: 0 });
    setLoaded(false);
    setFollowState("connecting");
    setError(undefined);
  }

  useEffect(() => {
    let cancelled = false;
    const b = new LogBuffer(cap);
    buffer.current = b;
    end.current = -1;
    readPage(envId, stream, -1, pageSize)
      .then((page) => {
        if (cancelled) return;
        b.append(page.entries ?? []);
        end.current = page.next;
        setSnap({ lines: b.lines, dropped: b.dropped });
        setLoaded(true);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [envId, stream, pageSize, cap]);

  useEffect(() => {
    const b = buffer.current;
    if (!loaded || !follow || !b) return;
    const f = new LogFollower({
      url: (offset) => `/api/v1/environments/${encodeURIComponent(envId)}/logs/${stream}?follow=true&offset=${offset}`,
      createEventSource,
      flushMs,
      onEntries: (entries) => {
        b.append(entries);
        setSnap({ lines: b.lines, dropped: b.dropped });
      },
      onState: setFollowState,
    });
    const last = b.lastOffset;
    f.start(last >= 0 ? last : Math.max(end.current, 0), last);
    return () => f.stop();
  }, [loaded, follow, envId, stream, createEventSource, flushMs]);

  const loadEarlier = useCallback(async () => {
    const b = buffer.current;
    if (!b || b.firstOffset <= 0) return;
    setLoadingEarlier(true);
    try {
      const page = await readPage(envId, stream, b.firstOffset, pageSize);
      b.prepend(page.entries ?? []);
      setSnap({ lines: b.lines, dropped: b.dropped });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoadingEarlier(false);
    }
  }, [envId, stream, pageSize]);

  let state: LogStreamState;
  if (!loaded) state = error ? "error" : "loading";
  else if (followState === "ended") state = "ended";
  else state = follow ? followState : "paused";
  const lines = snap.lines;
  const hasEarlier = lines.length > 0 && lines[0]!.offset > 0;
  return { lines, state, error, hasEarlier, dropped: snap.dropped, loadingEarlier, loadEarlier };
}
