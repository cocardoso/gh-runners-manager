/** Go's zero time and missing values count as unset. */
export function isSet(t: string | undefined | null): t is string {
  if (!t || t.startsWith("0001-01-01")) return false;
  return !Number.isNaN(Date.parse(t));
}

export function formatRelative(t: string | undefined, now: Date = new Date()): string {
  if (!isSet(t)) return "—";
  const s = Math.round((now.getTime() - Date.parse(t)) / 1000);
  if (s < 0) return "in the future";
  if (s < 30) return "just now";
  if (s < 3600) return `${Math.max(1, Math.floor(s / 60))} min ago`;
  if (s < 86_400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86_400)} d ago`;
}

export function formatAbsolute(t: string | undefined): string {
  if (!isSet(t)) return "not yet";
  return new Date(t).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "medium" });
}

export function formatDuration(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return "—";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

export function durationBetween(from: string | undefined, to: string | undefined): number | undefined {
  if (!isSet(from) || !isSet(to)) return undefined;
  return Date.parse(to) - Date.parse(from);
}

export function formatMB(mb: number): string {
  if (mb < 1024) return `${mb} MB`;
  const gb = mb / 1024;
  return `${Number.isInteger(gb) ? gb : gb.toFixed(1)} GB`;
}

export function formatPercent(ratio: number): string {
  return `${Math.round(ratio * 100)}%`;
}

export function shortId(id: string): string {
  return id.length > 12 ? id.slice(0, 12) : id;
}
