import { lazy, Suspense } from "react";
import { SkeletonLine } from "@cloudflare/kumo";

const Impl = lazy(() => import("./resources-chart-impl"));

export interface ResourcesChartProps {
  cpu: [number, number][];
  memoryMB: [number, number][];
  memoryLimitMB?: number;
}

export function ResourcesChart(props: ResourcesChartProps) {
  return (
    <Suspense fallback={<SkeletonLine className="w-full" blockHeight="200px" />}>
      <Impl {...props} />
    </Suspense>
  );
}
