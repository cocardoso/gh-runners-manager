import { formatAbsolute, formatDuration, formatMB, formatPercent, formatRelative, isSet, durationBetween } from "./format";

const now = new Date("2026-10-07T12:00:00Z");
const plain = (s: string) => s.replace(/\s/g, " "); // Intl uses narrow no-break spaces

test("formatRelative", () => {
  expect(formatRelative("2026-10-07T11:59:55Z", "en", now)).toBe("now");
  expect(formatRelative("2026-10-07T11:59:00Z", "en", now)).toBe("1 min. ago");
  expect(formatRelative("2026-10-07T09:00:00Z", "en", now)).toBe("3 hr. ago");
  expect(formatRelative("2026-10-05T12:00:00Z", "en", now)).toBe("2 days ago");
  expect(formatRelative("0001-01-01T00:00:00Z", "en", now)).toBe("—");
  expect(formatRelative("garbage", "en", now)).toBe("—");
});

test("formatRelative follows the language", () => {
  const fiveMin = "2026-10-07T11:55:00Z";
  expect(formatRelative(fiveMin, "pt-BR", now)).toBe("há 5 min.");
  expect(plain(formatRelative(fiveMin, "fr", now))).toBe("il y a 5 min");
  expect(plain(formatRelative(fiveMin, "es", now))).toBe("hace 5 min");
  expect(plain(formatRelative(fiveMin, "it", now))).toBe("5 min fa");
});

test("formatDuration", () => {
  expect(formatDuration(0, "en")).toBe("0s");
  expect(formatDuration(4_200, "en")).toBe("4s");
  expect(formatDuration(65_000, "en")).toBe("1m 5s");
  expect(formatDuration(3_725_000, "en")).toBe("1h 2m");
  expect(plain(formatDuration(65_000, "pt-BR"))).toBe("1 min 5 s");
  expect(formatDuration(-5, "en")).toBe("—");
  expect(formatDuration(Number.NaN, "en")).toBe("—");
});

test("durationBetween ignores unset times", () => {
  expect(durationBetween("2026-10-07T11:00:00Z", "2026-10-07T11:01:30Z")).toBe(90_000);
  expect(durationBetween("2026-10-07T11:00:00Z", "0001-01-01T00:00:00Z")).toBeUndefined();
});

test("isSet", () => {
  expect(isSet("0001-01-01T00:00:00Z")).toBe(false);
  expect(isSet(undefined)).toBe(false);
  expect(isSet("2026-10-07T11:00:00Z")).toBe(true);
});

test("formatMB", () => {
  expect(formatMB(512, "en")).toBe("512 MB");
  expect(formatMB(8192, "en")).toBe("8 GB");
  expect(formatMB(24_576 + 512, "en")).toBe("24.5 GB");
  expect(formatMB(24_576 + 512, "pt-BR")).toBe("24,5 GB");
});

test("formatPercent and formatAbsolute follow the language", () => {
  expect(formatPercent(0.5, "en")).toBe("50%");
  expect(plain(formatPercent(0.5, "fr"))).toBe("50 %");
  expect(formatAbsolute("2026-10-07T12:00:00Z", "pt-BR")).toMatch(/07\/10\/2026|7 de out\. de 2026/);
  expect(formatAbsolute("0001-01-01T00:00:00Z", "en")).toBe("not yet");
});
