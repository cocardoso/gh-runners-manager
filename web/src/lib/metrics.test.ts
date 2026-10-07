import { metricsSeries } from "./metrics";

const line = (t: string, cpu: number, mem: number, offset = 0) => ({ offset, time: t, text: JSON.stringify({ cpu_usec: cpu, mem_bytes: mem }) });

test("turns cumulative CPU time into a usage percentage and memory into MB", () => {
  const s = metricsSeries([
    line("2026-10-07T12:00:00Z", 1_000_000, 512 * 1024 * 1024),
    line("2026-10-07T12:00:05Z", 6_000_000, 1024 * 1024 * 1024),
    { offset: 9, time: "2026-10-07T12:00:06Z", text: "not json" },
    line("2026-10-07T12:00:10Z", 6_000_000, 1024 * 1024 * 1024),
  ]);
  expect(s.cpu).toEqual([
    [Date.parse("2026-10-07T12:00:05Z"), 100],
    [Date.parse("2026-10-07T12:00:10Z"), 0],
  ]);
  expect(s.memoryMB[0]).toEqual([Date.parse("2026-10-07T12:00:00Z"), 512]);
  expect(s.peakMemoryMB).toBe(1024);
});

test("a counter reset does not produce negative usage", () => {
  const s = metricsSeries([line("2026-10-07T12:00:00Z", 9_000_000, 1), line("2026-10-07T12:00:05Z", 1_000, 1)]);
  expect(s.cpu).toEqual([]);
});
