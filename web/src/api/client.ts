import createClient from "openapi-fetch";
import type { components, paths } from "./schema";

type Schemas = components["schemas"];

export type Alert = Schemas["Alert"];
export type Environment = Schemas["Environment"];
export type Job = Schemas["Job"];
export type JobBucket = Schemas["JobBucket"];
export type JobDetails = Schemas["JobDetails"];
export type LogEntry = Schemas["Entry"];
export type LogStreamInfo = Schemas["LogStream"];
export type Overview = Schemas["Overview"];
export type ScaleSet = Schemas["ScaleSet"];
export type Settings = Schemas["Settings"];
export type ApiEvent = Omit<Schemas["Event"], "$schema" | "data"> & { data?: Record<string, unknown>; [extra: string]: unknown };

// fetch is looked up per call so tests can stub it.
export const api = createClient<paths>({
  baseUrl: typeof window === "undefined" ? "" : window.location.origin,
  fetch: (req) => globalThis.fetch(req),
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
