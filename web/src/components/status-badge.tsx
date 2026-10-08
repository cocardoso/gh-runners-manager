import { Badge } from "@cloudflare/kumo";
import { stateLabel } from "@/i18n/labels";

export { stateLabel };

export type Tone = "success" | "error" | "warning" | "info" | "neutral";

const tones: Record<string, Record<string, Tone>> = {
  environment: {
    pending: "neutral",
    provisioning: "warning",
    booting: "warning",
    connected: "info",
    idle: "info",
    running: "info",
    completing: "info",
    destroying: "neutral",
    destroyed: "neutral",
    failed: "error",
  },
  job: { assigned: "warning", running: "info", completed: "neutral" },
  result: { succeeded: "success", failed: "error", canceled: "neutral", cancelled: "neutral" },
  template: {
    building: "warning",
    creating: "warning",
    verifying: "warning",
    ready: "info",
    active: "success",
    failed: "error",
    retired: "neutral",
    deleted: "neutral",
  },
  level: { debug: "neutral", info: "info", warn: "warning", warning: "warning", error: "error" },
};

/** The tone for a value; values the UI does not know yet are neutral (newer servers may add some). */
export function toneFor(kind: keyof typeof tones, value: string | undefined): Tone {
  return (value && tones[kind]?.[value]) || "neutral";
}

/** A state, status, result or level in the language in use; values the UI does not know show raw. */

function ToneBadge({ tone, children }: { tone: Tone; children: string }) {
  return (
    // Kumo draws a status dot for success, warning, error and neutral; info is a filled badge.
    <Badge variant={tone} appearance={tone === "info" ? "filled" : "dot"} className="whitespace-nowrap">
      {children}
    </Badge>
  );
}

export function EnvironmentStateBadge({ state }: { state: string }) {
  return <ToneBadge tone={toneFor("environment", state)}>{stateLabel(state || "unknown")}</ToneBadge>;
}

export function JobStatusBadge({ status, result }: { status: string; result?: string }) {
  if (status === "completed" && result) return <ToneBadge tone={toneFor("result", result)}>{stateLabel(result)}</ToneBadge>;
  return <ToneBadge tone={toneFor("job", status)}>{stateLabel(status || "unknown")}</ToneBadge>;
}

export function LevelBadge({ level }: { level: string }) {
  return <ToneBadge tone={toneFor("level", level)}>{stateLabel(level || "info")}</ToneBadge>;
}

export function TemplateStateBadge({ state }: { state: string }) {
  return <ToneBadge tone={toneFor("template", state)}>{stateLabel(state || "unknown")}</ToneBadge>;
}
