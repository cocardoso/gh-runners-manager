import { formatDuration, formatMB, formatRelative, isSet, durationBetween } from "./format";

const now = new Date("2026-10-07T12:00:00Z");

test("formatRelative", () => {
  expect(formatRelative("2026-10-07T11:59:55Z", now)).toBe("just now");
  expect(formatRelative("2026-10-07T11:59:00Z", now)).toBe("1 min ago");
  expect(formatRelative("2026-10-07T09:00:00Z", now)).toBe("3 h ago");
  expect(formatRelative("2026-10-05T12:00:00Z", now)).toBe("2 d ago");
  expect(formatRelative("0001-01-01T00:00:00Z", now)).toBe("—");
  expect(formatRelative("garbage", now)).toBe("—");
});

test("formatDuration", () => {
  expect(formatDuration(0)).toBe("0s");
  expect(formatDuration(4_200)).toBe("4s");
  expect(formatDuration(65_000)).toBe("1m 5s");
  expect(formatDuration(3_725_000)).toBe("1h 2m");
  expect(formatDuration(-5)).toBe("—");
  expect(formatDuration(Number.NaN)).toBe("—");
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
  expect(formatMB(512)).toBe("512 MB");
  expect(formatMB(8192)).toBe("8 GB");
  expect(formatMB(24_576 + 512)).toBe("24.5 GB");
});
