import createClient from "openapi-fetch";
import type { components, paths } from "./schema";
import { csrfToken, notifyUnauthorized } from "./auth-state";

type Schemas = components["schemas"];

export type Alert = Schemas["Alert"];
export type Environment = Schemas["Environment"];
export type Job = Schemas["Job"];
export type Target = Schemas["Target"];
export type JobBucket = Schemas["JobBucket"];
export type JobDetails = Schemas["JobDetails"];
export type LogEntry = Schemas["Entry"];
export type LogStreamInfo = Schemas["LogStream"];
export type Overview = Schemas["Overview"];
export type Repository = Schemas["Repository"];
export type ScaleSet = Schemas["ScaleSet"];
export type SessionState = Schemas["SessionState"];
export type CredentialView = Schemas["CredentialView"];
export type ScaleSetSettings = Schemas["ScaleSetSettings"];
export type CacheStatus = Schemas["Status"];
export type Settings = Schemas["Settings"];
export type TemplateVersion = Schemas["Template"];
export type ApiEvent = Omit<Schemas["Event"], "$schema" | "data"> & { data?: Record<string, unknown>; [extra: string]: unknown };

// fetch is looked up per call so tests can stub it.
export const api = createClient<paths>({
  baseUrl: typeof window === "undefined" ? "" : window.location.origin,
  fetch: (req) => globalThis.fetch(req),
});

// Writes carry the session's CSRF token; a 401 outside the sign-in calls means the
// session ended, so the app shows the sign-in page again.
api.use({
  onRequest({ request }) {
    if (request.method !== "GET" && request.method !== "HEAD" && csrfToken()) request.headers.set("X-CSRF-Token", csrfToken());
    return request;
  },
  onResponse({ request, response }) {
    if (response.status === 401 && !new URL(request.url).pathname.startsWith("/api/v1/auth/")) notifyUnauthorized();
    return response;
  },
});

/** Thrown for non-2xx answers, with the server's detail when there is one. */
export class ApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

/** Unwraps an openapi-fetch result, throwing ApiError on failure. */
export function unwrap<T>(res: { data?: T; error?: unknown; response: Response }): T {
  if (res.response.ok) return res.data as T; // 202/204 answers have no body
  const err = res.error as { detail?: string; title?: string } | undefined;
  throw new ApiError(res.response.status, err?.detail ?? err?.title ?? res.response.statusText ?? "request failed");
}
