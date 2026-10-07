import { Tooltip, cn } from "@cloudflare/kumo";
import { useConnectionState } from "@/lib/live";
import type { ConnectionState } from "@/lib/event-stream";

const copy: Record<ConnectionState, { label: string; hint: string; dot: string }> = {
  connecting: { label: "Connecting", hint: "Opening the live event stream…", dot: "bg-kumo-warning" },
  live: { label: "Live", hint: "Updates arrive as they happen.", dot: "bg-kumo-success" },
  reconnecting: { label: "Reconnecting", hint: "The live stream dropped; data on screen may be stale until it reconnects.", dot: "bg-kumo-danger" },
};

export function LiveIndicatorView({ state }: { state: ConnectionState }) {
  const c = copy[state];
  return (
    <Tooltip
      content={c.hint}
      render={
        <span role="status" aria-live="polite" className="inline-flex items-center gap-2 rounded-full border border-kumo-line px-2.5 py-1 text-sm text-kumo-default">
          <span className="relative flex size-2">
            {state === "live" && <span className={cn("absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:hidden", c.dot)} />}
            <span className={cn("relative inline-flex size-2 rounded-full", c.dot)} />
          </span>
          {c.label}
        </span>
      }
    />
  );
}

export function LiveIndicator() {
  return <LiveIndicatorView state={useConnectionState()} />;
}
