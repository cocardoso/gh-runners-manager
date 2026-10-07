import { cn } from "@cloudflare/kumo";
import { CheckCircleIcon, CircleNotchIcon, XCircleIcon } from "@phosphor-icons/react";
import type { Stage } from "@/lib/timeline";
import { formatAbsolute, formatDuration } from "@/lib/format";
import { RelativeTime } from "./common";

const icon = {
  done: <CheckCircleIcon size={20} weight="fill" className="text-kumo-success" />,
  current: <CircleNotchIcon size={20} className="animate-spin text-kumo-info motion-reduce:animate-none" />,
  failed: <XCircleIcon size={20} weight="fill" className="text-kumo-danger" />,
};

/** The lifecycle stages of a job or environment, with timestamps and durations. */
export function Timeline({ stages }: { stages: Stage[] }) {
  if (stages.length === 0) return <p className="text-sm text-kumo-subtle">No lifecycle events recorded yet.</p>;
  const longest = Math.max(...stages.map((s) => s.durationMs ?? 0), 1);
  return (
    <ol className="flex flex-col" aria-label="Lifecycle">
      {stages.map((s, i) => (
        <li key={`${s.key}-${s.at}`} className="relative flex gap-3 pb-4 last:pb-0">
          {i < stages.length - 1 && <span aria-hidden className="absolute top-6 bottom-0 left-[9px] w-px bg-kumo-line" />}
          <span className="relative z-[1] shrink-0 bg-kumo-base">{icon[s.status]}</span>
          <div className={cn("flex min-w-0 flex-1 flex-col gap-1 rounded-md px-2 py-0.5", s.status === "failed" && "bg-kumo-danger/10")}>
            <div className="flex flex-wrap items-baseline gap-x-3">
              <span className={cn("font-medium", s.status === "failed" ? "text-kumo-danger" : "text-kumo-default")}>{s.label}</span>
              <RelativeTime className="text-sm text-kumo-subtle" value={s.at} />
              <span className="text-xs text-kumo-inactive">{formatAbsolute(s.at)}</span>
            </div>
            {s.detail && <span className={cn("text-sm", s.status === "failed" ? "text-kumo-danger" : "text-kumo-subtle")}>{s.detail}</span>}
            {s.durationMs !== undefined && (
              <div className="flex items-center gap-2">
                <span className="h-1.5 rounded-full bg-kumo-brand/60" style={{ width: `${Math.max(2, (s.durationMs / longest) * 100)}%`, maxWidth: "60%" }} />
                <span className="text-sm tabular-nums text-kumo-subtle">
                  {formatDuration(s.durationMs)}
                  {s.status === "current" ? " so far" : ""}
                </span>
              </div>
            )}
          </div>
        </li>
      ))}
    </ol>
  );
}
