import { currentFormatLocale, tr } from "@/i18n";

// Intl objects are costly to build and are asked for on every render: keep one per
// locale and options.
const cache = new Map<string, unknown>();
function memo<T>(key: string, make: () => T): T {
  let v = cache.get(key) as T | undefined;
  if (v === undefined) cache.set(key, (v = make()));
  return v;
}
const numberFormat = (locale: string, o: Intl.NumberFormatOptions = {}) => memo(`n|${locale}|${JSON.stringify(o)}`, () => new Intl.NumberFormat(locale, o));
const relativeFormat = (locale: string) => memo(`r|${locale}`, () => new Intl.RelativeTimeFormat(locale, { numeric: "auto", style: "short" }));

/** Go's zero time and missing values count as unset. */
export function isSet(t: string | undefined | null): t is string {
  if (!t || t.startsWith("0001-01-01")) return false;
  return !Number.isNaN(Date.parse(t));
}

export function formatRelative(t: string | undefined, locale: string = currentFormatLocale(), now: Date = new Date()): string {
  if (!isSet(t)) return "—";
  const s = Math.round((now.getTime() - Date.parse(t)) / 1000);
  const rtf = relativeFormat(locale);
  if (s < 30) return tr("common.justNow"); // also a clock running ahead of ours
  if (s < 3600) return rtf.format(-Math.max(1, Math.floor(s / 60)), "minute");
  if (s < 86_400) return rtf.format(-Math.floor(s / 3600), "hour");
  return rtf.format(-Math.floor(s / 86_400), "day");
}

export function formatAbsolute(t: string | undefined, locale: string = currentFormatLocale(), unset = tr("common.notYet")): string {
  if (!isSet(t)) return unset;
  return memo(`d|${locale}`, () => new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "medium" })).format(new Date(t));
}

const unit = (locale: string, u: "hour" | "minute" | "second", n: number) => numberFormat(locale, { style: "unit", unit: u, unitDisplay: "narrow" }).format(n);

export function formatDuration(ms: number | undefined, locale: string = currentFormatLocale()): string {
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

export function formatMB(mb: number, locale: string = currentFormatLocale()): string {
  if (mb < 1024) return numberFormat(locale, { style: "unit", unit: "megabyte" }).format(mb);
  return numberFormat(locale, { style: "unit", unit: "gigabyte", maximumFractionDigits: 1 }).format(mb / 1024);
}

export function formatPercent(ratio: number, locale: string = currentFormatLocale()): string {
  return numberFormat(locale, { style: "percent", maximumFractionDigits: 0 }).format(ratio);
}

/** Gigabytes from bytes, as the disk meters show them. */
export function formatGB(bytes: number, locale: string = currentFormatLocale()): string {
  return numberFormat(locale, { style: "unit", unit: "gigabyte", maximumFractionDigits: bytes >= 10 * 2 ** 30 ? 0 : 1 }).format(bytes / 2 ** 30);
}

export function formatNumber(n: number, locale: string = currentFormatLocale()): string {
  return numberFormat(locale).format(n);
}

export function shortId(id: string): string {
  return id.length > 12 ? id.slice(0, 12) : id;
}
