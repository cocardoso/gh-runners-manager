import { useMemo } from "react";
import { LayerCard } from "@cloudflare/kumo";
import { metricsSeries } from "@/lib/metrics";
import { useLogStream } from "@/lib/use-log-stream";
import { formatMB } from "@/lib/format";
import { ResourcesChart } from "./resources-chart";

/** CPU and memory over time, from the environment's metrics stream. */
export function ResourcesPanel({ envId, live, memoryLimitMB }: { envId: string; live: boolean; memoryLimitMB?: number }) {
  const log = useLogStream({ envId, stream: "metrics", follow: live, pageSize: 10000 });
  const series = useMemo(() => metricsSeries(log.lines), [log.lines]);
  return (
    <LayerCard>
      <LayerCard.Secondary className="flex flex-wrap items-center justify-between gap-2">
        <span>Resources</span>
        {series.peakMemoryMB > 0 && <span className="text-sm text-kumo-subtle">Peak memory {formatMB(series.peakMemoryMB)}</span>}
      </LayerCard.Secondary>
      <LayerCard.Primary>
        {series.memoryMB.length === 0 ? (
          <p className="text-sm text-kumo-subtle">{log.state === "loading" ? "Loading…" : "No metrics were reported for this environment."}</p>
        ) : (
          <ResourcesChart cpu={series.cpu} memoryMB={series.memoryMB} memoryLimitMB={memoryLimitMB} />
        )}
      </LayerCard.Primary>
    </LayerCard>
  );
}
