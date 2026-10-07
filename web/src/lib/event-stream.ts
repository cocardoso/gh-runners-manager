import type { ApiEvent } from "@/api/client";

export type ConnectionState = "connecting" | "live" | "reconnecting";

export interface EventStreamOptions {
  url: string;
  onEvent: (e: ApiEvent) => void;
  onState: (s: ConnectionState) => void;
  createEventSource?: (url: string) => EventSource;
  /** Reconnect when nothing (not even a heartbeat) arrived for this long. */
  staleAfterMs?: number;
  initialBackoffMs?: number;
  maxBackoffMs?: number;
}

function isEvent(v: unknown): v is ApiEvent {
  return typeof v === "object" && v !== null && typeof (v as ApiEvent).seq === "number" && typeof (v as ApiEvent).kind === "string";
}

/**
 * One EventSource on the global event stream. It owns reconnection (exponential backoff,
 * resuming after the last delivered sequence), drops duplicates, and treats a connection
 * that stays silent past the heartbeat window as stale.
 */
export class EventStream {
  private readonly o: Required<EventStreamOptions>;
  private es: EventSource | null = null;
  private lastSeq = 0;
  private backoff: number;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private staleTimer: ReturnType<typeof setTimeout> | null = null;
  private stopped = true;

  constructor(opts: EventStreamOptions) {
    this.o = {
      createEventSource: (url) => new EventSource(url),
      staleAfterMs: 40_000,
      initialBackoffMs: 1_000,
      maxBackoffMs: 30_000,
      ...opts,
    };
    this.backoff = this.o.initialBackoffMs;
  }

  start() {
    if (!this.stopped) return;
    this.stopped = false;
    this.o.onState("connecting");
    this.connect();
  }

  stop() {
    this.stopped = true;
    this.clearTimers();
    this.es?.close();
    this.es = null;
  }

  private connect() {
    const after = this.lastSeq > 0 ? String(this.lastSeq) : "latest";
    const es = this.o.createEventSource(`${this.o.url}?after=${after}`);
    this.es = es;
    es.onopen = () => {
      if (es !== this.es) return;
      this.backoff = this.o.initialBackoffMs;
      this.o.onState("live");
      this.touch();
    };
    es.onmessage = (m: MessageEvent) => {
      if (es !== this.es) return;
      this.touch();
      let v: unknown;
      try {
        v = JSON.parse(String(m.data));
      } catch {
        return;
      }
      if (!isEvent(v) || v.seq <= this.lastSeq) return;
      this.lastSeq = v.seq;
      this.o.onEvent(v);
    };
    es.addEventListener("ping", () => {
      if (es === this.es) this.touch();
    });
    es.onerror = () => {
      if (es === this.es) this.reconnect();
    };
  }

  private touch() {
    if (this.staleTimer) clearTimeout(this.staleTimer);
    this.staleTimer = setTimeout(() => this.reconnect(), this.o.staleAfterMs);
  }

  private reconnect() {
    if (this.stopped) return;
    this.clearTimers();
    this.es?.close();
    this.es = null;
    this.o.onState("reconnecting");
    const delay = this.backoff;
    this.backoff = Math.min(this.backoff * 2, this.o.maxBackoffMs);
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      if (!this.stopped) this.connect();
    }, delay);
  }

  private clearTimers() {
    if (this.retryTimer) clearTimeout(this.retryTimer);
    if (this.staleTimer) clearTimeout(this.staleTimer);
    this.retryTimer = this.staleTimer = null;
  }
}

/** The query keys an event makes stale. Unknown kinds only refresh the overview. */
export function invalidationKeys(e: ApiEvent): unknown[][] {
  const keys: unknown[][] = [["overview"]];
  const family = e.kind.split(".")[0];
  switch (family) {
    case "job":
      keys.push(["jobs"], ["stats"]);
      if (e.job_id) keys.push(["job", e.job_id]);
      if (e.environment_id) keys.push(["environment", e.environment_id]);
      break;
    case "environment":
    case "audit":
    case "reaper":
      keys.push(["environments"], ["scale-sets"]);
      if (e.environment_id) keys.push(["environment", e.environment_id]);
      if (e.job_id) keys.push(["job", e.job_id], ["jobs"]);
      break;
    case "scaleset":
    case "controller":
      keys.push(["scale-sets"]);
      break;
  }
  return keys;
}
