import type { LogEntry } from "@/api/client";

export interface MetricsSeries {
  /** CPU usage in percent of one core. */
  cpu: [number, number][];
  memoryMB: [number, number][];
  peakMemoryMB: number;
}

/** Parses the metrics stream (cumulative cgroup CPU time and memory bytes per sample). */
export function metricsSeries(lines: LogEntry[]): MetricsSeries {
  const out: MetricsSeries = { cpu: [], memoryMB: [], peakMemoryMB: 0 };
  let prev: { t: number; cpu: number } | undefined;
  for (const l of lines) {
    let v: { cpu_usec?: unknown; mem_bytes?: unknown };
    try {
      v = JSON.parse(l.text);
    } catch {
      continue;
    }
    const t = Date.parse(l.time);
    if (Number.isNaN(t)) continue;
    if (typeof v.mem_bytes === "number") {
      const mb = Math.round(v.mem_bytes / (1024 * 1024));
      out.memoryMB.push([t, mb]);
      out.peakMemoryMB = Math.max(out.peakMemoryMB, mb);
    }
    if (typeof v.cpu_usec === "number") {
      if (prev && t > prev.t && v.cpu_usec >= prev.cpu) {
        out.cpu.push([t, Math.round(((v.cpu_usec - prev.cpu) / ((t - prev.t) * 1000)) * 1000) / 10]);
      }
      prev = { t, cpu: v.cpu_usec };
    }
  }
  return out;
}
