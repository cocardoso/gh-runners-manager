import * as echarts from "echarts";
import { ChartPalette, TimeseriesChart } from "@cloudflare/kumo";
import { useIsDark } from "@/lib/theme";
import { currentFormatLocale, useT } from "@/i18n";
import type { ResourcesChartProps } from "./resources-chart";

const time = (v: number) => new Date(v).toLocaleTimeString(currentFormatLocale(), { hour: "2-digit", minute: "2-digit", second: "2-digit" });

export default function ResourcesChartImpl({ cpu, memoryMB, memoryLimitMB }: ResourcesChartProps) {
  const dark = useIsDark();
  const t = useT();
  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <section aria-label="CPU">
        <h3 className="mb-2 text-sm font-medium text-kumo-subtle">{t("overview.resources.cpuTitle")}</h3>
        <TimeseriesChart
          echarts={echarts}
          isDarkMode={dark}
          height={220}
          gradient
          data={[{ name: "CPU", color: ChartPalette.categorical(0, dark), data: cpu }]}
          xAxisTickFormat={time}
          yAxisTickFormat={(v) => `${v}%`}
          tooltipValueFormat={(v) => `${v}%`}
          ariaDescription={t("overview.resources.cpuDescription")}
        />
      </section>
      <section aria-label={t("overview.resources.memory")}>
        <h3 className="mb-2 text-sm font-medium text-kumo-subtle">{t("overview.resources.memoryTitle")}</h3>
        <TimeseriesChart
          echarts={echarts}
          isDarkMode={dark}
          height={220}
          gradient
          data={[{ name: t("overview.resources.memory"), color: ChartPalette.categorical(1, dark), data: memoryMB }]}
          thresholds={memoryLimitMB ? [{ value: memoryLimitMB, label: t("overview.resources.limit"), color: ChartPalette.semantic("Attention", dark) }] : undefined}
          xAxisTickFormat={time}
          tooltipValueFormat={(v) => `${v} MB`}
          ariaDescription={t("overview.resources.memoryDescription")}
        />
      </section>
    </div>
  );
}
