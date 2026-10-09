import type { Environment, Job, Overview, ScaleSet } from "@/api/client";

const T = "2026-10-07T11:50:00Z";
const ZERO = "0001-01-01T00:00:00Z";

export function job(over: Partial<Job> = {}): Job {
  return {
    id: "job-1",
    scale_set: "homelab",
    repository: "octo/app",
    display_name: "build (ubuntu)",
    status: "running",
    queued_at: T,
    started_at: T,
    finished_at: ZERO,
    updated_at: T,
    ...over,
  } as Job;
}

export function env(over: Partial<Environment> = {}): Environment {
  return {
    id: "env-1",
    scale_set: "homelab",
    state: "running",
    memory_mb: 4096,
    created_at: T,
    updated_at: T,
    state_changed_at: T,
    ...over,
  } as Environment;
}

export function scaleSet(over: Partial<ScaleSet> = {}): ScaleSet {
  return { name: "homelab", github_id: 7, desired: 1, live: 1, listening: true, waiting_since: ZERO, ...over } as ScaleSet;
}

export const emptyOverview: Overview = {
  kpis: { running_jobs: 0, waiting_demand: 0, jobs_24h: 0, success_rate_24h: 0, median_queue_seconds_24h: 0, median_duration_seconds_24h: 0, queued_jobs: 0, preparing_runners: 0, ready_runners: 0 },
  capacity: {
    environments_live: 0,
    environments_max: 6,
    memory_committed_mb: 0,
    memory_budget_mb: 24576,
    host_memory_available_mb: 20000,
    host_memory_total_mb: 40000,
    disk_percent: 12,
    disk_max_percent: 85,
  },
  alerts: [],
} as Overview;

export type Routes = Record<string, unknown | ((url: URL) => unknown)>;

export function defaultRoutes(): Routes {
  return {
    "/api/v1/auth/session": { state: "signed_in", username: "admin", csrf: "csrf-1" },
    "/api/v1/overview": emptyOverview,
    "/api/v1/repositories": { repositories: [] },
    "/api/v1/history/settings": { mode: "automatic", days: 30, audit_days: 365 },
    "/api/v1/capacity": { max_environments: 4, memory_budget_mb: 16384, memory_margin_mb: 4096, max_disk_percent: 85, source: "default", host_memory_total_mb: 40960 },
    "/api/v1/cache": { enabled: false, up: false, origins: [], disk_used_bytes: 0, disk_budget_bytes: 0, checked_at: ZERO },
    "/api/v1/stats/jobs": { buckets: [] },
    "/api/v1/jobs": { jobs: [] },
    "/api/v1/environments": { environments: [] },
    "/api/v1/scale-sets": { scale_sets: [] },
    "/api/v1/events": { events: [] },
    "/api/v1/templates": { templates: [], building: false, enabled: false },
    "/api/v1/template-profiles": {
      profiles: [{ name: "default", remove: [], toolcache: { node: ["22", "24"] }, apt: [], used_by: [] }],
      components: [
        { id: "azure-cli", report: ["Azure CLI"] },
        { id: "azure-devops-cli", report: ["Azure CLI (azure-devops)"], needs: "azure-cli" },
        { id: "github-cli", report: ["GitHub CLI"] },
      ],
      toolcache_tools: ["go", "node", "python"],
    },
    "/api/v1/settings": { version: "dev", admin_actions: false, proxmox: {}, ingest: {}, capacity: {}, scale_sets: [] },
  };
}

/** Stubs fetch with JSON answers by pathname; unknown paths answer 404. Returns the request log. */
export function mockApi(overrides: Routes = {}) {
  const routes = { ...defaultRoutes(), ...overrides };
  const calls: { method: string; url: URL; headers: Headers; request: Request }[] = [];
  vi.stubGlobal("fetch", async (req: Request) => {
    const url = new URL(req.url);
    calls.push({ method: req.method, url, headers: req.headers, request: req.clone() });
    const r = routes[`${req.method} ${url.pathname}`] ?? (req.method === "GET" ? routes[url.pathname] : undefined) ?? routes[url.pathname];
    if (r === undefined) return new Response(JSON.stringify({ title: "Not Found", detail: "not found" }), { status: 404, headers: { "Content-Type": "application/json" } });
    const body = typeof r === "function" ? await (r as (u: URL) => unknown)(url) : r;
    if (body instanceof Response) return body;
    return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
  });
  return calls;
}

/** Answers /api/v1/jobs like the server: filtered by ?status= and cut at ?limit=. */
export function jobsByStatus(list: Job[]) {
  return (url: URL) => {
    const status = url.searchParams.get("status");
    const limit = Number(url.searchParams.get("limit") ?? 1000);
    return { jobs: list.filter((j) => !status || j.status === status).slice(0, limit) };
  };
}
