import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, unwrap } from "./client";

// Query keys match invalidationKeys in lib/event-stream.ts.

export function useOverview() {
  return useQuery({ queryKey: ["overview"], queryFn: async () => unwrap(await api.GET("/api/v1/overview")) });
}

export function useJobStats(hours = 24) {
  return useQuery({
    queryKey: ["stats", hours],
    queryFn: async () => unwrap(await api.GET("/api/v1/stats/jobs", { params: { query: { hours } } })).buckets ?? [],
  });
}

export function useJobs(filter: { status?: string; scaleSet?: string; limit?: number } = {}) {
  return useQuery({
    queryKey: ["jobs", filter],
    queryFn: async () =>
      unwrap(await api.GET("/api/v1/jobs", { params: { query: { status: filter.status, scale_set: filter.scaleSet, limit: filter.limit ?? 1000 } } })).jobs ??
      [],
    placeholderData: keepPreviousData,
  });
}

export function useJob(id: string) {
  return useQuery({ queryKey: ["job", id], queryFn: async () => unwrap(await api.GET("/api/v1/jobs/{id}", { params: { path: { id } } })), enabled: id !== "" });
}

export function useJobGitHub(id: string, enabled = true) {
  return useQuery({
    queryKey: ["job", id, "github"],
    queryFn: async () => unwrap(await api.GET("/api/v1/jobs/{id}/github", { params: { path: { id } } })),
    enabled,
    staleTime: 15_000,
  });
}

export function useEnvironments(filter: { state?: string; scaleSet?: string; limit?: number } = {}) {
  return useQuery({
    queryKey: ["environments", filter],
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/environments", { params: { query: { state: filter.state, scale_set: filter.scaleSet, limit: filter.limit ?? 1000 } } }),
      ).environments ?? [],
    placeholderData: keepPreviousData,
  });
}

export function useEnvironment(id: string | undefined) {
  return useQuery({
    queryKey: ["environment", id],
    queryFn: async () => unwrap(await api.GET("/api/v1/environments/{id}", { params: { path: { id: id! } } })),
    enabled: !!id,
  });
}

export function useScaleSets() {
  return useQuery({ queryKey: ["scale-sets"], queryFn: async () => unwrap(await api.GET("/api/v1/scale-sets")).scale_sets ?? [] });
}

export function useSettings() {
  return useQuery({ queryKey: ["settings"], queryFn: async () => unwrap(await api.GET("/api/v1/settings")), staleTime: 60_000 });
}

export function useJobEvents(jobId: string | undefined, environmentId: string | undefined) {
  return useQuery({
    queryKey: jobId ? ["job", jobId, "events"] : ["environment", environmentId, "events"],
    queryFn: async () =>
      unwrap(await api.GET("/api/v1/events", { params: { query: { job: jobId, environment: jobId ? undefined : environmentId, limit: 5000 } } }))
        .events ?? [],
    enabled: !!(jobId || environmentId),
  });
}
