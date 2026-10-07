import { Badge } from "@cloudflare/kumo";

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
  level: { debug: "neutral", info: "info", warn: "warning", warning: "warning", error: "error" },
};

/** The tone for a value; values the UI does not know yet are neutral (newer servers may add some). */
export function toneFor(kind: keyof typeof tones, value: string | undefined): Tone {
  return (value && tones[kind]?.[value]) || "neutral";
}

function ToneBadge({ tone, children }: { tone: Tone; children: string }) {
  return (
    <Badge variant={tone === "neutral" ? "neutral" : tone} appearance="dot" className="whitespace-nowrap">
      {children}
    </Badge>
  );
}

export function EnvironmentStateBadge({ state }: { state: string }) {
  return <ToneBadge tone={toneFor("environment", state)}>{state || "unknown"}</ToneBadge>;
}

export function JobStatusBadge({ status, result }: { status: string; result?: string }) {
  if (status === "completed" && result) return <ToneBadge tone={toneFor("result", result)}>{result}</ToneBadge>;
  return <ToneBadge tone={toneFor("job", status)}>{status || "unknown"}</ToneBadge>;
}

export function LevelBadge({ level }: { level: string }) {
  return <ToneBadge tone={toneFor("level", level)}>{level || "info"}</ToneBadge>;
}
