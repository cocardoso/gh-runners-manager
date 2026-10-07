import type { LogEntry } from "@/api/client";
import { withDefaults } from "./defaults";

export const LOG_LINE_CAP = 50_000;

/**
 * The lines a log view holds, ordered by byte offset and capped. Appending past the cap
 * drops the oldest lines; prepending earlier lines ("load earlier") drops the newest ones,
 * which a later follow fetches again from the last retained offset.
 */
export class LogBuffer {
  lines: LogEntry[] = [];
  /** Lines dropped from the front since the buffer was created. */
  dropped = 0;
  private readonly cap: number;

  constructor(cap = LOG_LINE_CAP) {
    this.cap = cap;
  }

  get firstOffset(): number {
    return this.lines[0]?.offset ?? -1;
  }

  get lastOffset(): number {
    return this.lines.at(-1)?.offset ?? -1;
  }

  get hasEarlier(): boolean {
    return this.lines.length > 0 && this.firstOffset > 0;
  }

  append(entries: LogEntry[]) {
    const last = this.lastOffset;
    const fresh = entries.filter((e, i) => e.offset > last && (i === 0 || e.offset > entries[i - 1]!.offset));
    if (fresh.length === 0) return;
    let next = this.lines.concat(fresh);
    if (next.length > this.cap) {
      const drop = next.length - this.cap;
      next = next.slice(drop);
      this.dropped += drop;
    }
    this.lines = next;
  }

  prepend(entries: LogEntry[]) {
    const first = this.lines.length ? this.firstOffset : Infinity;
    const earlier = entries.filter((e) => e.offset < first);
    if (earlier.length === 0) return;
    this.lines = earlier.concat(this.lines).slice(0, this.cap);
  }

  clear() {
    this.lines = [];
    this.dropped = 0;
  }
}

export type FollowState = "connecting" | "live" | "reconnecting" | "ended";

export interface LogFollowerOptions {
  url: (offset: number) => string;
  onEntries: (entries: LogEntry[]) => void;
  onState: (s: FollowState) => void;
  createEventSource?: (url: string) => EventSource;
  flushMs?: number;
  initialBackoffMs?: number;
  maxBackoffMs?: number;
}

function isEntry(v: unknown): v is LogEntry {
  return typeof v === "object" && v !== null && typeof (v as LogEntry).offset === "number" && typeof (v as LogEntry).text === "string";
}

/** Follows one log stream over SSE, batching entries and reconnecting from the last offset. */
export class LogFollower {
  private readonly o: Required<LogFollowerOptions>;
  private es: EventSource | null = null;
  private last = -1;
  private pending: LogEntry[] = [];
  private flushTimer: ReturnType<typeof setTimeout> | null = null;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private backoff: number;
  private stopped = true;

  constructor(opts: LogFollowerOptions) {
    this.o = withDefaults<LogFollowerOptions>(
      { createEventSource: (url) => new EventSource(url), flushMs: 100, initialBackoffMs: 1_000, maxBackoffMs: 30_000, url: opts.url, onEntries: opts.onEntries, onState: opts.onState },
      opts,
    );
    this.backoff = this.o.initialBackoffMs;
  }

  /** Starts following at offset; entries at or before an already delivered offset are skipped. */
  start(offset: number, deliveredUpTo = -1) {
    this.stop();
    this.stopped = false;
    this.last = deliveredUpTo;
    this.o.onState("connecting");
    this.connect(offset);
  }

  stop() {
    this.stopped = true;
    if (this.retryTimer) clearTimeout(this.retryTimer);
    this.retryTimer = null;
    this.flush();
    this.es?.close();
    this.es = null;
  }

  private connect(offset: number) {
    const es = this.o.createEventSource(this.o.url(offset));
    this.es = es;
    es.onopen = () => {
      if (es !== this.es) return;
      this.backoff = this.o.initialBackoffMs;
      this.o.onState("live");
    };
    es.onmessage = (m: MessageEvent) => {
      if (es !== this.es) return;
      let v: unknown;
      try {
        v = JSON.parse(String(m.data));
      } catch {
        return;
      }
      if (!isEntry(v) || v.offset <= this.last) return;
      this.last = v.offset;
      this.pending.push(v);
      this.flushTimer ??= setTimeout(() => this.flush(), this.o.flushMs);
    };
    es.addEventListener("end", () => {
      if (es !== this.es) return;
      this.stop();
      this.o.onState("ended");
    });
    es.onerror = () => {
      if (es !== this.es || this.stopped) return;
      es.close();
      this.es = null;
      this.flush();
      this.o.onState("reconnecting");
      const delay = this.backoff;
      this.backoff = Math.min(this.backoff * 2, this.o.maxBackoffMs);
      this.retryTimer = setTimeout(() => {
        this.retryTimer = null;
        if (!this.stopped) this.connect(Math.max(this.last, offset));
      }, delay);
    };
  }

  private flush() {
    if (this.flushTimer) clearTimeout(this.flushTimer);
    this.flushTimer = null;
    if (this.pending.length === 0) return;
    const batch = this.pending;
    this.pending = [];
    this.o.onEntries(batch);
  }
}
