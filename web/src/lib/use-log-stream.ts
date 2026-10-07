import { useCallback, useEffect, useRef, useState } from "react";
import { api, unwrap, type LogEntry } from "@/api/client";
import { LogBuffer, LogFollower, type FollowState } from "./log-buffer";

export type LogStreamName = "control-plane" | "runtime" | "agent" | "runner" | "job" | "metrics" | "build" | "selftest";
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
  /** Delay before retrying a failed first load. */
  retryMs?: number;
}

export interface LogStreamView {
  lines: LogEntry[];
  /** Line number of lines[0]; absolute when the server knew it, relative otherwise. */
  firstLine: number;
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
export function useLogStream({ envId, stream, follow, pageSize = 5000, cap, createEventSource, flushMs, retryMs = 3000 }: UseLogStreamOptions): LogStreamView {
  const key = `${envId}/${stream}/${pageSize}/${cap ?? ""}`;
  const buffer = useRef<LogBuffer | null>(null);
  const end = useRef(-1);
  const [current, setCurrent] = useState(key);
  const [snap, setSnap] = useState<{ lines: LogEntry[]; dropped: number; firstLine: number }>({ lines: [], dropped: 0, firstLine: 1 });
  const [attempt, setAttempt] = useState(0);
  const [followEpoch, setFollowEpoch] = useState(0);
  const [loaded, setLoaded] = useState(false);
  const [followState, setFollowState] = useState<FollowState>("connecting");
  const [error, setError] = useState<string>();
  const [loadingEarlier, setLoadingEarlier] = useState(false);

  // A different stream starts from scratch (state reset during render, not in an effect).
  if (current !== key) {
    setCurrent(key);
    setSnap({ lines: [], dropped: 0, firstLine: 1 });
    setLoaded(false);
    setFollowState("connecting");
    setError(undefined);
  }

  useEffect(() => {
    let cancelled = false;
    const b = new LogBuffer(cap);
    buffer.current = b;
    end.current = -1;
    let retry: ReturnType<typeof setTimeout> | undefined;
    readPage(envId, stream, -1, pageSize)
      .then((page) => {
        if (cancelled) return;
        b.append(page.entries ?? []);
        if (page.first_line) b.firstLine = page.first_line;
        end.current = page.next;
        setSnap({ lines: b.lines, dropped: b.dropped, firstLine: b.firstLine });
        setError(undefined);
        setLoaded(true);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : String(e));
        retry = setTimeout(() => setAttempt((n) => n + 1), retryMs);
      });
    return () => {
      cancelled = true;
      if (retry) clearTimeout(retry);
    };
  }, [envId, stream, pageSize, cap, attempt, retryMs]);

  useEffect(() => {
    const b = buffer.current;
    if (!loaded || !follow || !b) return;
    const f = new LogFollower({
      url: (offset) => `/api/v1/environments/${encodeURIComponent(envId)}/logs/${stream}?follow=true&offset=${offset}`,
      createEventSource,
      flushMs,
      onEntries: (entries) => {
        b.append(entries);
        setSnap({ lines: b.lines, dropped: b.dropped, firstLine: b.firstLine });
      },
      onState: setFollowState,
    });
    const last = b.lastOffset;
    f.start(last >= 0 ? last : Math.max(end.current, 0), last);
    return () => f.stop();
  }, [loaded, follow, envId, stream, createEventSource, flushMs, followEpoch]);

  const loadEarlier = useCallback(async () => {
    const b = buffer.current;
    if (!b || b.firstOffset <= 0) return;
    setLoadingEarlier(true);
    try {
      const page = await readPage(envId, stream, b.firstOffset, pageSize);
      if (buffer.current !== b) return; // the stream changed while loading
      const lastBefore = b.lastOffset;
      b.prepend(page.entries ?? []);
      setSnap({ lines: b.lines, dropped: b.dropped, firstLine: b.firstLine });
      // Prepending past the cap dropped the newest lines: follow again from the last kept one.
      if (b.lastOffset < lastBefore) setFollowEpoch((n) => n + 1);
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
  return { lines, firstLine: snap.firstLine, state, error, hasEarlier, dropped: snap.dropped, loadingEarlier, loadEarlier };
}
