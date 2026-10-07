import { buildTimeline } from "./timeline";
import { env, job } from "@/test/api-mock";
import type { ApiEvent } from "@/api/client";

const st = (seq: number, at: string, from: string, to: string): ApiEvent => ({
  seq, kind: "environment.state", level: "info", message: `${from} → ${to}`, time: at, environment_id: "env-1", data: { from, to },
});

test("builds stages with durations from job times and state events", () => {
  const stages = buildTimeline({
    job: job({ queued_at: "2026-10-07T12:00:00Z", started_at: "2026-10-07T12:00:20Z", finished_at: "2026-10-07T12:05:20Z", status: "completed", result: "succeeded" }),
    environment: env({ created_at: "2026-10-07T12:00:02Z", state: "destroyed" }),
    events: [
      st(2, "2026-10-07T12:00:03Z", "pending", "provisioning"),
      st(3, "2026-10-07T12:00:08Z", "provisioning", "booting"),
      st(4, "2026-10-07T12:00:15Z", "booting", "connected"),
      st(5, "2026-10-07T12:00:20Z", "connected", "running"),
      st(6, "2026-10-07T12:05:21Z", "running", "completing"),
      st(7, "2026-10-07T12:05:22Z", "completing", "destroying"),
      st(8, "2026-10-07T12:05:30Z", "destroying", "destroyed"),
    ],
    now: new Date("2026-10-07T12:10:00Z"),
  });
  expect(stages.map((s) => s.key)).toEqual(["queued", "created", "provisioning", "booting", "connected", "running", "completing", "destroying", "destroyed"]);
  expect(stages[0]).toMatchObject({ durationMs: 2000, status: "done" });
  expect(stages.find((s) => s.key === "provisioning")).toMatchObject({ durationMs: 5000 });
  expect(stages.find((s) => s.key === "running")).toMatchObject({ durationMs: 301_000 });
  expect(stages.at(-1)).toMatchObject({ key: "destroyed", status: "done" });
  expect(stages.at(-1)?.durationMs).toBeUndefined();
});

test("the last stage of a live environment is current and runs until now", () => {
  const stages = buildTimeline({
    job: job({ queued_at: "2026-10-07T12:00:00Z" }),
    environment: env({ created_at: "2026-10-07T12:00:01Z", state: "booting" }),
    events: [st(1, "2026-10-07T12:00:05Z", "provisioning", "booting")],
    now: new Date("2026-10-07T12:00:35Z"),
  });
  expect(stages.at(-1)).toMatchObject({ key: "booting", status: "current", durationMs: 30_000 });
});

test("a failed environment highlights the failed stage with its reason", () => {
  const stages = buildTimeline({
    environment: env({ created_at: "2026-10-07T12:00:00Z", state: "failed", failure_stage: "booting", failure_reason: "agent never said hello" }),
    events: [st(1, "2026-10-07T12:00:05Z", "provisioning", "booting"), st(2, "2026-10-07T12:03:05Z", "booting", "failed")],
    now: new Date("2026-10-07T12:10:00Z"),
  });
  expect(stages.find((s) => s.key === "booting")).toMatchObject({ status: "failed" });
  expect(stages.find((s) => s.key === "failed")).toMatchObject({ status: "failed", detail: "agent never said hello" });
});

test("unknown states are kept with their raw name", () => {
  const stages = buildTimeline({
    environment: env({ created_at: "2026-10-07T12:00:00Z", state: "hibernating" }),
    events: [st(1, "2026-10-07T12:00:05Z", "idle", "hibernating")],
    now: new Date("2026-10-07T12:00:10Z"),
  });
  expect(stages.at(-1)).toMatchObject({ key: "hibernating", label: "hibernating" });
});

test("a completed job without an environment ends instead of running forever", () => {
  const stages = buildTimeline({
    job: job({ queued_at: "2026-10-07T12:00:00Z", started_at: "2026-10-07T12:00:10Z", finished_at: "2026-10-07T12:01:00Z", status: "completed", result: "succeeded" }),
    events: [],
    now: new Date("2026-10-08T12:00:00Z"),
  });
  expect(stages.some((s) => s.status === "current")).toBe(false);
  expect(stages.at(-1)).toMatchObject({ key: "finished", detail: "Job succeeded" });
});
