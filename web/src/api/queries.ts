import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, unwrap } from "./client";
import { setCsrfToken } from "./auth-state";

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
    // Not under ["job", id]: job events must not re-query GitHub.
    queryKey: ["job-github", id],
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

/** Job and scale set events refresh the list; the timer also keeps the 24 h window moving. */
export function useRepositories() {
  return useQuery({
    queryKey: ["repositories"],
    queryFn: async () => unwrap(await api.GET("/api/v1/repositories")).repositories ?? [],
    refetchInterval: 30_000,
  });
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

export function useTemplates() {
  return useQuery({ queryKey: ["templates"], queryFn: async () => unwrap(await api.GET("/api/v1/templates")) });
}

export function useTemplate(id: string) {
  return useQuery({ queryKey: ["templates", id], queryFn: async () => unwrap(await api.GET("/api/v1/templates/{id}", { params: { path: { id } } })) });
}

export function useSession() {
  return useQuery({
    queryKey: ["session"],
    queryFn: async () => {
      const s = unwrap(await api.GET("/api/v1/auth/session"));
      setCsrfToken(s.csrf);
      return s;
    },
    staleTime: 60_000,
  });
}

export function useCredentials() {
  return useQuery({ queryKey: ["credentials"], queryFn: async () => unwrap(await api.GET("/api/v1/credentials")).credentials ?? [] });
}

export function useCache() {
  return useQuery({ queryKey: ["cache"], queryFn: async () => unwrap(await api.GET("/api/v1/cache")), refetchInterval: 30_000 });
}

export function useHistorySettings() {
  return useQuery({ queryKey: ["history-settings"], queryFn: async () => unwrap(await api.GET("/api/v1/history/settings")) });
}

/** The repositories and organizations a credential's token can reach (asked from GitHub). */
export function useCredentialTargets(name: string) {
  return useQuery({
    queryKey: ["credential-targets", name],
    enabled: !!name,
    retry: false,
    staleTime: 5 * 60_000,
    queryFn: async () => unwrap(await api.GET("/api/v1/credentials/{name}/targets", { params: { path: { name } } })).targets ?? [],
  });
}
