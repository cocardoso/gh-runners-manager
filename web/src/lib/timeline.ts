import type { ApiEvent, Environment, Job } from "@/api/client";
import { isSet } from "./format";

export type StageStatus = "done" | "current" | "failed";

export interface Stage {
  key: string;
  label: string;
  at: string;
  durationMs?: number;
  status: StageStatus;
  detail?: string;
}

const labels: Record<string, string> = {
  queued: "Queued on GitHub",
  created: "Environment requested",
  pending: "Pending",
  provisioning: "Provisioning",
  booting: "Booting",
  connected: "Runner connected",
  idle: "Waiting for a job",
  running: "Running the job",
  completing: "Completing",
  destroying: "Destroying",
  destroyed: "Destroyed",
  failed: "Failed",
  started: "Job started",
  finished: "Job finished",
};

const terminal = new Set(["destroyed", "failed", "finished"]);

/** The lifecycle of a job and its environment as ordered stages with durations. */
export function buildTimeline({
  job,
  environment,
  events,
  now = new Date(),
}: {
  job?: Job;
  environment?: Environment;
  events: ApiEvent[];
  now?: Date;
}): Stage[] {
  const stages: Stage[] = [];
  const add = (key: string, at: string | undefined, detail?: string) => {
    if (isSet(at)) stages.push({ key, label: labels[key] ?? key, at, status: "done", detail });
  };
  if (job) add("queued", job.queued_at, job.repository);
  if (environment) add("created", environment.created_at);
  const states = events
    .filter((e) => e.kind === "environment.state" && (!environment || !e.environment_id || e.environment_id === environment.id))
    .sort((a, b) => a.seq - b.seq);
  for (const e of states) {
    const to = typeof e.data?.to === "string" ? e.data.to : undefined;
    if (to) add(to, e.time);
  }
  // Without an environment, the job's own end closes the timeline.
  if (job && !environment && job.status === "completed") {
    add("started", job.started_at);
    add("finished", job.finished_at, job.result ? `Job ${job.result}` : undefined);
  }
  stages.sort((a, b) => Date.parse(a.at) - Date.parse(b.at));

  stages.forEach((s, i) => {
    const next = stages[i + 1];
    if (next) s.durationMs = Date.parse(next.at) - Date.parse(s.at);
    else if (!terminal.has(s.key)) {
      s.durationMs = now.getTime() - Date.parse(s.at);
      s.status = "current";
    }
  });

  if (job?.status === "completed" && job.result) {
    const running = stages.find((s) => s.key === "running");
    if (running) running.detail = `Job ${job.result}`;
  }
  if (environment?.failure_stage) {
    for (const s of stages) {
      if (s.key === environment.failure_stage) s.status = "failed";
      if (s.key === "failed") {
        s.status = "failed";
        s.detail = environment.failure_reason || `failed at ${environment.failure_stage}`;
      }
    }
  }
  return stages;
}
