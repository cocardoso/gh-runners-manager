import { Banner, Empty, Grid, LayerCard, Link, Meter, Text, CodeBlock } from "@cloudflare/kumo";
import { InfoIcon, RocketLaunchIcon, WarningCircleIcon, WarningIcon } from "@phosphor-icons/react";
import type { Alert, Environment, Overview } from "@/api/client";
import { useEnvironments, useJobStats, useJobs, useOverview, useScaleSets } from "@/api/queries";
import { ErrorState, Loading, Page, RelativeTime, Truncate } from "@/components/common";
import { EnvironmentStateBadge } from "@/components/status-badge";
import { JobsChart } from "@/components/jobs-chart";
import { formatDuration, formatMB, formatPercent, formatNumber } from "@/lib/format";
import { useT } from "@/i18n";

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
  const t = useT();
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
          {alert.environment_id && <Link href={`/environments/${encodeURIComponent(alert.environment_id)}`}>{t("overview.openEnvironment")}</Link>}
        </span>
      }
    />
  );
}

function Capacity({ c }: { c: Overview["capacity"] }) {
  const t = useT();
  const hostUsed = Math.max(0, c.host_memory_total_mb - c.host_memory_available_mb);
  return (
    <LayerCard>
      <LayerCard.Secondary>{t("overview.capacity.title")}</LayerCard.Secondary>
      <LayerCard.Primary className="flex flex-col gap-4">
        <Meter
          label={t("overview.capacity.environments")}
          value={c.environments_live}
          max={Math.max(c.environments_max, 1)}
          customValue={`${formatNumber(c.environments_live)} / ${formatNumber(c.environments_max)}`}
        />
        <Meter
          label={t("overview.capacity.memoryCommitted")}
          value={c.memory_committed_mb}
          max={Math.max(c.memory_budget_mb, 1)}
          customValue={`${formatMB(c.memory_committed_mb)} / ${formatMB(c.memory_budget_mb)}`}
        />
        {c.host_memory_total_mb > 0 && (
          <Meter
            label={t("overview.capacity.hostMemory")}
            value={hostUsed}
            max={c.host_memory_total_mb}
            customValue={`${formatMB(hostUsed)} / ${formatMB(c.host_memory_total_mb)}`}
          />
        )}
        <Meter
          label={t("overview.capacity.disk")}
          value={c.disk_percent}
          max={100}
          customValue={t("overview.capacity.diskValue", { percent: formatPercent(c.disk_percent / 100), limit: formatPercent(c.disk_max_percent / 100) })}
        />
      </LayerCard.Primary>
    </LayerCard>
  );
}

function NowList({ environments, jobNames }: { environments: Environment[]; jobNames: Map<string, string> }) {
  const t = useT();
  const active = environments.filter((e) => ACTIVE.has(e.state));
  return (
    <section aria-label={t("overview.now.title")}>
      <LayerCard>
        <LayerCard.Secondary className="flex items-center justify-between">
          <span>{t("overview.now.title")}</span>
          <Link href="/environments">{t("overview.now.allEnvironments")}</Link>
        </LayerCard.Secondary>
        <LayerCard.Primary className="p-0">
          {active.length === 0 ? (
            <p className="p-4 text-sm text-kumo-subtle">{t("overview.now.empty")}</p>
          ) : (
            <ul className="divide-y divide-kumo-line">
              {active.map((e) => (
                <li key={e.id} className="flex min-w-0 items-center gap-3 px-4 py-2.5">
                  <EnvironmentStateBadge state={e.state} />
                  <div className="flex min-w-0 flex-1 flex-col">
                    <Link href={`/environments/${encodeURIComponent(e.id)}`} className="truncate font-mono text-sm">
                      {e.id}
                    </Link>
                    <Truncate className="text-sm text-kumo-subtle" text={[e.scale_set, e.job_id ? jobNames.get(e.job_id) ?? e.job_id : t("overview.now.waitingForJob")].join(" · ")} />
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
  const t = useT();
  return (
    <LayerCard>
      <LayerCard.Primary>
        <Empty
          icon={<RocketLaunchIcon size={48} className="text-kumo-inactive" />}
          title={t("overview.fresh.title")}
          description={scaleSet ? t("overview.fresh.listening") : t("overview.fresh.configure")}
          contents={scaleSet ? <CodeBlock code={`jobs:\n  build:\n    runs-on: ${scaleSet}`} /> : undefined}
        />
      </LayerCard.Primary>
    </LayerCard>
  );
}

export function OverviewPage() {
  const t = useT();
  const overview = useOverview();
  const stats = useJobStats(24);
  const envs = useEnvironments({ limit: 200 });
  const jobs = useJobs({ limit: 200 });
  const sets = useScaleSets();

  if (overview.isLoading) return <Page title={t("overview.title")}><Loading /></Page>;
  if (overview.error || !overview.data) return <Page title={t("overview.title")}><ErrorState error={overview.error} /></Page>;

  const ov = overview.data;
  const alerts = ov.alerts ?? [];
  const fresh = jobs.isSuccess && (jobs.data?.length ?? 0) === 0 && envs.isSuccess && (envs.data?.length ?? 0) === 0;
  const jobNames = new Map((jobs.data ?? []).map((j) => [j.id, j.display_name || j.id]));
  const k = ov.kpis;

  return (
    <Page title={t("overview.title")} description={t("overview.description")}>
      {alerts.length > 0 && (
        <div className="flex flex-col gap-2">
          {alerts.map((a, i) => (
            <AlertBanner key={`${a.kind}-${a.environment_id ?? a.scale_set ?? ""}-${i}`} alert={a} />
          ))}
        </div>
      )}
      {fresh && <FreshInstall scaleSet={sets.data?.[0]?.name} />}
      <Grid variant="1-2-4up" gap="base">
        <Kpi
          label={t("overview.kpis.runningJobs")}
          value={formatNumber(k.running_jobs)}
          hint={k.waiting_demand > 0 ? t("overview.kpis.waitingDemand", { count: k.waiting_demand }) : t("overview.kpis.noDemand")}
        />
        <Kpi label={t("overview.kpis.jobs24h")} value={formatNumber(k.jobs_24h)} />
        <Kpi label={t("overview.kpis.successRate24h")} value={k.jobs_24h > 0 ? formatPercent(k.success_rate_24h) : "—"} />
        <Kpi
          label={t("overview.kpis.medianQueue24h")}
          value={k.jobs_24h > 0 ? formatDuration(k.median_queue_seconds_24h * 1000) : "—"}
          hint={t("overview.kpis.medianQueueHint")}
        />
      </Grid>
      <Grid variant="2-1" gap="base">
        <LayerCard>
          <LayerCard.Secondary>{t("overview.chart.title")}</LayerCard.Secondary>
          <LayerCard.Primary>
            {stats.data && stats.data.some((b) => b.succeeded + b.failed + b.canceled + b.other > 0) ? (
              <JobsChart buckets={stats.data} />
            ) : (
              <Text variant="secondary">{t("overview.chart.empty")}</Text>
            )}
          </LayerCard.Primary>
        </LayerCard>
        <Capacity c={ov.capacity} />
      </Grid>
      <NowList environments={envs.data ?? []} jobNames={jobNames} />
    </Page>
  );
}
