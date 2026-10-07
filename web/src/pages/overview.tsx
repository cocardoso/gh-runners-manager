import { Banner, Empty, Grid, LayerCard, Link, Meter, Text, CodeBlock } from "@cloudflare/kumo";
import { InfoIcon, RocketLaunchIcon, WarningCircleIcon, WarningIcon } from "@phosphor-icons/react";
import type { Alert, Environment, Overview } from "@/api/client";
import { useEnvironments, useJobStats, useJobs, useOverview, useScaleSets } from "@/api/queries";
import { ErrorState, Loading, Page, RelativeTime, Truncate } from "@/components/common";
import { EnvironmentStateBadge } from "@/components/status-badge";
import { JobsChart } from "@/components/jobs-chart";
import { formatDuration, formatMB, formatPercent } from "@/lib/format";

const ACTIVE = new Set(["pending", "provisioning", "booting", "connected", "idle", "running", "completing", "destroying", "failed"]);

function Kpi({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <LayerCard>
      <LayerCard.Secondary>{label}</LayerCard.Secondary>
      <LayerCard.Primary className="flex flex-col gap-1">
        <span className="font-heading text-3xl font-semibold tabular-nums text-kumo-default">{value}</span>
        {hint && <span className="text-sm text-kumo-subtle">{hint}</span>}
      </LayerCard.Primary>
    </LayerCard>
  );
}

function AlertBanner({ alert }: { alert: Alert }) {
  const variant = alert.level === "error" ? "error" : alert.level === "warn" || alert.level === "warning" ? "alert" : "secondary";
  const Icon = variant === "error" ? WarningCircleIcon : variant === "alert" ? WarningIcon : InfoIcon;
  const where = [alert.scale_set, alert.environment_id].filter(Boolean).join(" · ");
  return (
    <Banner
      variant={variant}
      icon={<Icon weight="fill" />}
      title={alert.message}
      description={
        <span className="flex flex-wrap items-center gap-x-2">
          {where && <span>{where}</span>}
          <RelativeTime value={alert.time} />
          {alert.environment_id && <Link href={`/environments/${encodeURIComponent(alert.environment_id)}`}>Open environment</Link>}
        </span>
      }
    />
  );
}

function Capacity({ c }: { c: Overview["capacity"] }) {
  const hostUsed = Math.max(0, c.host_memory_total_mb - c.host_memory_available_mb);
  return (
    <LayerCard>
      <LayerCard.Secondary>Capacity</LayerCard.Secondary>
      <LayerCard.Primary className="flex flex-col gap-4">
        <Meter label="Environments" value={c.environments_live} max={Math.max(c.environments_max, 1)} customValue={`${c.environments_live} / ${c.environments_max}`} />
        <Meter
          label="Memory committed"
          value={c.memory_committed_mb}
          max={Math.max(c.memory_budget_mb, 1)}
          customValue={`${formatMB(c.memory_committed_mb)} / ${formatMB(c.memory_budget_mb)}`}
        />
        {c.host_memory_total_mb > 0 && (
          <Meter label="Host memory in use" value={hostUsed} max={c.host_memory_total_mb} customValue={`${formatMB(hostUsed)} / ${formatMB(c.host_memory_total_mb)}`} />
        )}
        <Meter label="Disk (thin pool)" value={c.disk_percent} max={100} customValue={`${Math.round(c.disk_percent)}% (limit ${Math.round(c.disk_max_percent)}%)`} />
      </LayerCard.Primary>
    </LayerCard>
  );
}

function NowList({ environments, jobNames }: { environments: Environment[]; jobNames: Map<string, string> }) {
  const active = environments.filter((e) => ACTIVE.has(e.state));
  return (
    <section aria-label="Now">
      <LayerCard>
        <LayerCard.Secondary className="flex items-center justify-between">
          <span>Now</span>
          <Link href="/environments">All environments</Link>
        </LayerCard.Secondary>
        <LayerCard.Primary className="p-0">
          {active.length === 0 ? (
            <p className="p-4 text-sm text-kumo-subtle">No environment is running. New ones appear here as jobs arrive.</p>
          ) : (
            <ul className="divide-y divide-kumo-line">
              {active.map((e) => (
                <li key={e.id} className="flex min-w-0 items-center gap-3 px-4 py-2.5">
                  <EnvironmentStateBadge state={e.state} />
                  <div className="flex min-w-0 flex-1 flex-col">
                    <Link href={`/environments/${encodeURIComponent(e.id)}`} className="truncate font-mono text-sm">
                      {e.id}
                    </Link>
                    <Truncate className="text-sm text-kumo-subtle" text={[e.scale_set, e.job_id ? jobNames.get(e.job_id) ?? e.job_id : "waiting for a job"].join(" · ")} />
                  </div>
                  <RelativeTime className="text-sm text-kumo-subtle" value={e.state_changed_at} />
                </li>
              ))}
            </ul>
          )}
        </LayerCard.Primary>
      </LayerCard>
    </section>
  );
}

function FreshInstall({ scaleSet }: { scaleSet?: string }) {
  return (
    <LayerCard>
      <LayerCard.Primary>
        <Empty
          icon={<RocketLaunchIcon size={48} className="text-kumo-inactive" />}
          title="No jobs yet"
          description={
            scaleSet
              ? "The control plane is listening. Point a workflow job at a scale set and it will run in a fresh environment."
              : "Configure a scale set in ghrm.yaml, restart the control plane, then point a workflow job at it."
          }
          contents={scaleSet ? <CodeBlock code={`jobs:\n  build:\n    runs-on: ${scaleSet}`} /> : undefined}
        />
      </LayerCard.Primary>
    </LayerCard>
  );
}

export function OverviewPage() {
  const overview = useOverview();
  const stats = useJobStats(24);
  const envs = useEnvironments({ limit: 200 });
  const jobs = useJobs({ limit: 200 });
  const sets = useScaleSets();

  if (overview.isLoading) return <Page title="Overview"><Loading /></Page>;
  if (overview.error || !overview.data) return <Page title="Overview"><ErrorState error={overview.error} /></Page>;

  const ov = overview.data;
  const alerts = ov.alerts ?? [];
  const fresh = jobs.isSuccess && (jobs.data?.length ?? 0) === 0 && envs.isSuccess && (envs.data?.length ?? 0) === 0;
  const jobNames = new Map((jobs.data ?? []).map((j) => [j.id, j.display_name || j.id]));
  const k = ov.kpis;

  return (
    <Page title="Overview" description="What the fleet is doing right now.">
      {alerts.length > 0 && (
        <div className="flex flex-col gap-2">
          {alerts.map((a, i) => (
            <AlertBanner key={`${a.kind}-${a.environment_id ?? a.scale_set ?? ""}-${i}`} alert={a} />
          ))}
        </div>
      )}
      {fresh && <FreshInstall scaleSet={sets.data?.[0]?.name} />}
      <Grid variant="1-2-4up" gap="base">
        <Kpi label="Running jobs" value={String(k.running_jobs)} hint={k.waiting_demand > 0 ? `${k.waiting_demand} waiting for capacity` : "No demand waiting"} />
        <Kpi label="Jobs (24 h)" value={String(k.jobs_24h)} />
        <Kpi label="Success rate (24 h)" value={k.jobs_24h > 0 ? formatPercent(k.success_rate_24h) : "—"} />
        <Kpi label="Median queue time (24 h)" value={k.jobs_24h > 0 ? formatDuration(k.median_queue_seconds_24h * 1000) : "—"} hint="From queued to started" />
      </Grid>
      <Grid variant="2-1" gap="base">
        <LayerCard>
          <LayerCard.Secondary>Jobs per hour</LayerCard.Secondary>
          <LayerCard.Primary>
            {stats.data && stats.data.some((b) => b.succeeded + b.failed + b.canceled + b.other > 0) ? (
              <JobsChart buckets={stats.data} />
            ) : (
              <Text variant="secondary">No job finished in the last 24 hours.</Text>
            )}
          </LayerCard.Primary>
        </LayerCard>
        <Capacity c={ov.capacity} />
      </Grid>
      <NowList environments={envs.data ?? []} jobNames={jobNames} />
    </Page>
  );
}
