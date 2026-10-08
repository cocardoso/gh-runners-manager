import * as echarts from "echarts";
import { ChartPalette, TimeseriesChart } from "@cloudflare/kumo";
import type { JobBucket } from "@/api/client";
import { useIsDark } from "@/lib/theme";
import { currentLocale, useT } from "@/i18n";

export default function JobsChartImpl({ buckets, height }: { buckets: JobBucket[]; height: number }) {
  const dark = useIsDark();
  const t = useT();
  const at = (b: JobBucket) => Date.parse(b.start);
  const series = [
    { name: t("overview.chart.succeeded"), color: ChartPalette.semantic("Success", dark), data: buckets.map((b) => [at(b), b.succeeded] as [number, number]) },
    { name: t("overview.chart.failed"), color: ChartPalette.semantic("Attention", dark), data: buckets.map((b) => [at(b), b.failed] as [number, number]) },
    { name: t("overview.chart.canceled"), color: ChartPalette.semantic("Warning", dark), data: buckets.map((b) => [at(b), b.canceled] as [number, number]) },
    { name: t("overview.chart.other"), color: ChartPalette.semantic("Neutral", dark), data: buckets.map((b) => [at(b), b.other] as [number, number]) },
  ];
  return (
    <TimeseriesChart
      echarts={echarts}
      isDarkMode={dark}
      type="bar"
      data={series}
      height={height}
      yAxisMinInterval={1}
      xAxisTickFormat={(v) => new Date(v).toLocaleTimeString(currentLocale(), { hour: "2-digit", minute: "2-digit" })}
      ariaDescription={t("overview.chart.description")}
    />
  );
}
