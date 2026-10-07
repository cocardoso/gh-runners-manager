import { lazy, Suspense } from "react";
import { SkeletonLine } from "@cloudflare/kumo";
import type { JobBucket } from "@/api/client";

const Chart = lazy(() => import("./jobs-chart-impl"));

/** Jobs finished per hour. ECharts loads on demand to keep the first page light. */
export function JobsChart({ buckets, height = 240 }: { buckets: JobBucket[]; height?: number }) {
  return (
    <Suspense fallback={<SkeletonLine className="w-full" blockHeight={`${height}px`} />}>
      <Chart buckets={buckets} height={height} />
    </Suspense>
  );
}
