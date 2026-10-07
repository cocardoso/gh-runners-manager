import * as echarts from "echarts";
import { ChartPalette, TimeseriesChart } from "@cloudflare/kumo";
import type { JobBucket } from "@/api/client";
import { useIsDark } from "@/lib/theme";

export default function JobsChartImpl({ buckets, height }: { buckets: JobBucket[]; height: number }) {
  const dark = useIsDark();
  const at = (b: JobBucket) => Date.parse(b.start);
  const series = [
    { name: "Succeeded", color: ChartPalette.semantic("Success", dark), data: buckets.map((b) => [at(b), b.succeeded] as [number, number]) },
    { name: "Failed", color: ChartPalette.semantic("Attention", dark), data: buckets.map((b) => [at(b), b.failed] as [number, number]) },
    { name: "Canceled", color: ChartPalette.semantic("Warning", dark), data: buckets.map((b) => [at(b), b.canceled] as [number, number]) },
    { name: "Other", color: ChartPalette.semantic("Neutral", dark), data: buckets.map((b) => [at(b), b.other] as [number, number]) },
  ];
  return (
    <TimeseriesChart
      echarts={echarts}
      isDarkMode={dark}
      type="bar"
      data={series}
      height={height}
      yAxisMinInterval={1}
      xAxisTickFormat={(v) => new Date(v).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}
      ariaDescription="Jobs finished per hour over the last 24 hours, by result."
    />
  );
}
