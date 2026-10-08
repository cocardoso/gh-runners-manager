import { currentLocale, tr } from "@/i18n";

/** Go's zero time and missing values count as unset. */
export function isSet(t: string | undefined | null): t is string {
  if (!t || t.startsWith("0001-01-01")) return false;
  return !Number.isNaN(Date.parse(t));
}

export function formatRelative(t: string | undefined, locale: string = currentLocale(), now: Date = new Date()): string {
  if (!isSet(t)) return "—";
  const s = Math.round((now.getTime() - Date.parse(t)) / 1000);
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto", style: "short" });
  if (s < 30) return rtf.format(0, "second");
  if (s < 3600) return rtf.format(-Math.max(1, Math.floor(s / 60)), "minute");
  if (s < 86_400) return rtf.format(-Math.floor(s / 3600), "hour");
  return rtf.format(-Math.floor(s / 86_400), "day");
}

export function formatAbsolute(t: string | undefined, locale: string = currentLocale(), unset = tr("common.notYet")): string {
  if (!isSet(t)) return unset;
  return new Date(t).toLocaleString(locale, { dateStyle: "medium", timeStyle: "medium" });
}

const unit = (locale: string, u: "hour" | "minute" | "second", n: number) =>
  new Intl.NumberFormat(locale, { style: "unit", unit: u, unitDisplay: "narrow" }).format(n);

export function formatDuration(ms: number | undefined, locale: string = currentLocale()): string {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return "—";
  const s = Math.round(ms / 1000);
  if (s < 60) return unit(locale, "second", s);
  const m = Math.floor(s / 60);
  if (m < 60) return `${unit(locale, "minute", m)} ${unit(locale, "second", s % 60)}`;
  return `${unit(locale, "hour", Math.floor(m / 60))} ${unit(locale, "minute", m % 60)}`;
}

export function durationBetween(from: string | undefined, to: string | undefined): number | undefined {
  if (!isSet(from) || !isSet(to)) return undefined;
  return Date.parse(to) - Date.parse(from);
}

export function formatMB(mb: number, locale: string = currentLocale()): string {
  if (mb < 1024) return `${new Intl.NumberFormat(locale).format(mb)} MB`;
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(mb / 1024)} GB`;
}

export function formatPercent(ratio: number, locale: string = currentLocale()): string {
  return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 0 }).format(ratio);
}

export function formatNumber(n: number, locale: string = currentLocale()): string {
  return new Intl.NumberFormat(locale).format(n);
}

export function shortId(id: string): string {
  return id.length > 12 ? id.slice(0, 12) : id;
}
