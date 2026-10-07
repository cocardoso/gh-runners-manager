import * as echarts from "echarts";
import { ChartPalette, TimeseriesChart } from "@cloudflare/kumo";
import { useIsDark } from "@/lib/theme";
import type { ResourcesChartProps } from "./resources-chart";

const time = (v: number) => new Date(v).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" });

export default function ResourcesChartImpl({ cpu, memoryMB, memoryLimitMB }: ResourcesChartProps) {
  const dark = useIsDark();
  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <section aria-label="CPU">
        <h3 className="mb-2 text-sm font-medium text-kumo-subtle">CPU (% of one core)</h3>
        <TimeseriesChart
          echarts={echarts}
          isDarkMode={dark}
          height={220}
          gradient
          data={[{ name: "CPU", color: ChartPalette.categorical(0, dark), data: cpu }]}
          xAxisTickFormat={time}
          yAxisTickFormat={(v) => `${v}%`}
          tooltipValueFormat={(v) => `${v}%`}
          ariaDescription="CPU usage of the environment over time."
        />
      </section>
      <section aria-label="Memory">
        <h3 className="mb-2 text-sm font-medium text-kumo-subtle">Memory (MB)</h3>
        <TimeseriesChart
          echarts={echarts}
          isDarkMode={dark}
          height={220}
          gradient
          data={[{ name: "Memory", color: ChartPalette.categorical(1, dark), data: memoryMB }]}
          thresholds={memoryLimitMB ? [{ value: memoryLimitMB, label: "Limit", color: ChartPalette.semantic("Attention", dark) }] : undefined}
          xAxisTickFormat={time}
          tooltipValueFormat={(v) => `${v} MB`}
          ariaDescription="Memory used by the environment over time."
        />
      </section>
    </div>
  );
}
