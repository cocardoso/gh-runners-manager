import { useEffect, useState, type ReactNode } from "react";
import { Badge, Banner, Empty, Grid, LayerCard, Link, Meter, Text, CodeBlock, cn } from "@cloudflare/kumo";
import { ArrowSquareOutIcon, InfoIcon, RocketLaunchIcon, WarningCircleIcon, WarningIcon } from "@phosphor-icons/react";
import type { Alert, Environment, Job, Overview, ScaleSet } from "@/api/client";
import { useCache, useEnvironments, useJobStats, useJobs, useOverview, useScaleSets, useSettings, useTemplates } from "@/api/queries";
import { ErrorState, Loading, Page, RelativeTime, Truncate } from "@/components/common";
import { JobStatusBadge } from "@/components/status-badge";
import { JobsChart } from "@/components/jobs-chart";
import { durationBetween, formatDuration, formatMB, formatPercent, formatNumber, isSet } from "@/lib/format";
import { useT, type Key } from "@/i18n";

// Job environments on their way to a runner, and idle runners waiting for a job.
const PREPARING = new Set(["pending", "provisioning", "booting", "connected"]);
const isJobEnvironment = (e: Environment) => !e.kind || e.kind === "job";
// The longest the live list gets; the rest is one click away.
const NOW_ROWS = 10;
const REASONS = new Set(["scale_set_limit", "global_limit", "memory_budget", "host_memory", "disk"]);

/** The current time, every second: for timers of what is running now. */
function useSecond(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  return now;
}

function Elapsed({ since }: { since: string | undefined }) {
  const now = useSecond();
  if (!isSet(since)) return null;
  return <span className="tabular-nums">{formatDuration(Math.max(0, now - Date.parse(since)))}</span>;
}

/** "owner/repo/.github/workflows/stg.yml@refs/heads/main" → { workflow: "stg", branch: "main" }. */
export function parseWorkflowRef(ref: string | undefined): { workflow?: string; branch?: string } {
  if (!ref) return {};
  const [path = "", at] = ref.split("@");
  const file = path.split("/").pop() ?? "";
  return { workflow: file.replace(/\.ya?ml$/, "") || undefined, branch: at?.replace(/^refs\/(heads|tags)\//, "") || undefined };
}

function jobTitle(j: Job) {
  const repo = j.repository?.split("/").pop();
  const { workflow } = parseWorkflowRef(j.workflow_ref);
  return [repo, workflow, j.display_name || j.id].filter(Boolean).join(" · ");
}

function runURL(j: Job) {
  return j.repository && j.run_id ? `https://github.com/${j.repository}/actions/runs/${j.run_id}` : undefined;
}

function Kpi({ label, value, hint, className }: { label: string; value: string; hint?: ReactNode; className?: string }) {
  return (
    <LayerCard className={cn("h-full", className)}>
      <LayerCard.Secondary>{label}</LayerCard.Secondary>
      <LayerCard.Primary className="flex flex-1 flex-col gap-1">
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
  // The server words its alerts in English; a waiting reason has a translation.
  const reason = alert.kind === "waiting" ? alert.message.split(": ").pop() : undefined;
  const title = reason && REASONS.has(reason) ? t("templates.scaleSets.waiting", { reason: t(`overview.now.reasons.${reason}` as Key) }) : alert.message;
  return (
    <Banner
      variant={variant}
      icon={<Icon weight="fill" />}
      title={title}
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

function Kpis({ ov }: { ov: Overview }) {
  const t = useT();
  const k = ov.kpis;
  const c = ov.capacity;
  const runnersHint =
    k.preparing_runners > 0
      ? t("overview.kpis.preparing", { count: k.preparing_runners, n: formatNumber(k.preparing_runners) })
      : k.ready_runners > 0
        ? t("overview.kpis.ready", { count: k.ready_runners, n: formatNumber(k.ready_runners) })
        : t("overview.kpis.idle");
  return (
    // Two per row on a phone, so the headline stays one screen tall.
    <div className="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-3 xl:grid-cols-5">
      <Kpi label={t("overview.kpis.running")} value={formatNumber(k.running_jobs)} hint={runnersHint} />
      <Kpi
        label={t("overview.kpis.queued")}
        value={formatNumber(k.queued_jobs)}
        hint={k.queued_jobs > 0 && isSet(k.oldest_queued_at) ? <OldestWait since={k.oldest_queued_at} /> : t("overview.kpis.nothingQueued")}
      />
      <Kpi
        label={t("overview.kpis.jobs24h")}
        value={formatNumber(k.jobs_24h)}
        hint={k.jobs_24h > 0 ? t("overview.kpis.succeeded", { percent: formatPercent(k.success_rate_24h) }) : t("overview.kpis.noJobs")}
      />
      <Kpi
        label={t("overview.kpis.medianDuration")}
        value={k.jobs_24h > 0 ? formatDuration(k.median_duration_seconds_24h * 1000) : "—"}
        hint={t("overview.kpis.medianDurationHint")}
      />
      <Kpi
        className="col-span-2 lg:col-span-1"
        label={t("overview.kpis.capacity")}
        value={`${formatNumber(c.environments_live)} / ${formatNumber(c.environments_max)}`}
        hint={t("overview.kpis.capacityHint", { memory: formatMB(c.memory_committed_mb), budget: formatMB(c.memory_budget_mb) })}
      />
    </div>
  );
}

function OldestWait({ since }: { since: string }) {
  const t = useT();
  const now = useSecond();
  return <>{t("overview.kpis.oldestWait", { duration: formatDuration(Math.max(0, now - Date.parse(since))) })}</>;
}

function NowCard({ jobs, sets, environments }: { jobs: Job[]; sets: ScaleSet[]; environments: Environment[] }) {
  const t = useT();
  const active = jobs
    .filter((j) => j.status === "running" || j.status === "assigned")
    // Running first, then the longest waiting.
    .sort((a, b) => (a.status === b.status ? Date.parse(a.queued_at) - Date.parse(b.queued_at) : a.status === "running" ? -1 : 1));
  const runners = environments.filter(isJobEnvironment);
  const preparing = runners.filter((e) => PREPARING.has(e.state));
  const ready = runners.filter((e) => e.state === "idle");
  const waiting = new Map(sets.map((s) => [s.name, s.waiting]));
  const queuedHint = (j: Job) => {
    const reason = waiting.get(j.scale_set);
    if (reason) return REASONS.has(reason) ? t(`overview.now.reasons.${reason}` as Key) : reason;
    return preparing.some((e) => e.scale_set === j.scale_set) ? t("overview.now.preparingRunner") : t("overview.now.waitingRunner");
  };
  return (
    <section aria-label={t("overview.now.title")}>
      <LayerCard>
        <LayerCard.Secondary className="flex items-center justify-between">
          <span>{t("overview.now.title")}</span>
          <Link href="/jobs">{t("overview.now.allJobs")}</Link>
        </LayerCard.Secondary>
        <LayerCard.Primary className="p-0">
          {active.length === 0 ? (
            <p className="p-4 text-sm text-kumo-subtle">{t("overview.now.empty")}</p>
          ) : (
            <ul className="divide-y divide-kumo-line">
              {active.slice(0, NOW_ROWS).map((j) => {
                const running = j.status === "running";
                const url = runURL(j);
                const { branch } = parseWorkflowRef(j.workflow_ref);
                return (
                  <li key={j.id} className="flex min-w-0 items-center gap-3 px-4 py-2.5">
                    <span
                      aria-hidden
                      className={cn("size-2.5 shrink-0 rounded-full", running ? "bg-kumo-success" : "border-2 border-kumo-warning bg-transparent")}
                    />
                    <div className="flex min-w-0 flex-1 flex-col">
                      <Link href={`/jobs/${encodeURIComponent(j.id)}`} className="min-w-0">
                        <Truncate className="font-medium" text={jobTitle(j)} />
                      </Link>
                      <Truncate
                        className="text-sm text-kumo-subtle"
                        text={[j.scale_set, branch, running ? j.runner_name : queuedHint(j)].filter(Boolean).join(" · ")}
                      />
                    </div>
                    <div className="flex shrink-0 items-center gap-3 text-sm">
                      <span className={running ? "text-kumo-success" : "text-kumo-warning"}>{running ? t("overview.now.running") : t("overview.now.queued")}</span>
                      <Elapsed since={running ? j.started_at : j.queued_at} />
                      {url && (
                        <Link href={url} target="_blank" rel="noreferrer" aria-label={t("overview.now.openRun")} className="inline-flex">
                          <ArrowSquareOutIcon size={16} />
                        </Link>
                      )}
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
          {active.length > NOW_ROWS && (
            <p className="border-t border-kumo-line px-4 py-2 text-sm">
              <Link href="/jobs">{t("overview.now.more", { count: active.length - NOW_ROWS, n: formatNumber(active.length - NOW_ROWS) })}</Link>
            </p>
          )}
          <p className="border-t border-kumo-line px-4 py-2 text-sm text-kumo-subtle">
            {t("overview.now.runners", { preparing: formatNumber(preparing.length), ready: formatNumber(ready.length) })}
          </p>
        </LayerCard.Primary>
      </LayerCard>
    </section>
  );
}

function RecentCard({ jobs }: { jobs: Job[] }) {
  const t = useT();
  const done = jobs
    .filter((j) => j.status === "completed" && isSet(j.finished_at))
    .sort((a, b) => Date.parse(b.finished_at) - Date.parse(a.finished_at))
    .slice(0, 8);
  return (
    <section aria-label={t("overview.recent.title")}>
      <LayerCard>
        <LayerCard.Secondary>{t("overview.recent.title")}</LayerCard.Secondary>
        <LayerCard.Primary className="p-0">
          {done.length === 0 ? (
            <p className="p-4 text-sm text-kumo-subtle">{t("overview.recent.empty")}</p>
          ) : (
            <ul className="divide-y divide-kumo-line">
              {done.map((j) => {
                const took = durationBetween(j.started_at, j.finished_at);
                const waited = durationBetween(j.queued_at, j.started_at);
                return (
                  <li key={j.id} className="flex min-w-0 items-center gap-3 px-4 py-2.5">
                    <JobStatusBadge status={j.status} result={j.result} />
                    <div className="flex min-w-0 flex-1 flex-col">
                      <Link href={`/jobs/${encodeURIComponent(j.id)}`} className="min-w-0">
                        <Truncate className="font-medium" text={jobTitle(j)} />
                      </Link>
                      <Truncate
                        className="text-sm text-kumo-subtle"
                        text={[
                          took !== undefined && took >= 0 ? t("overview.recent.took", { duration: formatDuration(took) }) : undefined,
                          waited !== undefined && waited >= 0 ? t("overview.recent.waited", { duration: formatDuration(waited) }) : undefined,
                        ]
                          .filter(Boolean)
                          .join(" · ")}
                      />
                    </div>
                    <RelativeTime className="shrink-0 text-sm text-kumo-subtle" value={j.finished_at} />
                  </li>
                );
              })}
            </ul>
          )}
        </LayerCard.Primary>
      </LayerCard>
    </section>
  );
}

function ScaleSetsCard({ sets, jobs, environments }: { sets: ScaleSet[]; jobs: Job[]; environments: Environment[] }) {
  const t = useT();
  const count = (pred: (x: { scale_set: string }) => boolean, list: { scale_set: string }[]) => list.filter(pred).length;
  return (
    <section aria-label={t("overview.scaleSets.title")}>
      <LayerCard>
        <LayerCard.Secondary>
          <Link href="/scale-sets">{t("overview.scaleSets.title")}</Link>
        </LayerCard.Secondary>
        <LayerCard.Primary className="p-0">
          <ul className="divide-y divide-kumo-line">
            {sets.map((s) => {
              const mine = (x: { scale_set: string }) => x.scale_set === s.name;
              const running = count((x) => mine(x) && (x as Job).status === "running", jobs);
              const queued = count((x) => mine(x) && (x as Job).status === "assigned", jobs);
              const ready = count((x) => mine(x) && isJobEnvironment(x as Environment) && (x as Environment).state === "idle", environments);
              return (
                <li key={s.name} className="flex min-w-0 flex-col gap-1 px-4 py-2.5">
                  <div className="flex min-w-0 items-center justify-between gap-2">
                    <span className="truncate font-medium">{s.name}</span>
                    <Badge variant={s.listening ? "success" : "error"} appearance="dot">
                      {s.listening ? t("overview.scaleSets.listening") : t("overview.scaleSets.notListening")}
                    </Badge>
                  </div>
                  <span className="text-sm text-kumo-subtle">
                    {t("overview.scaleSets.summary", {
                      running: formatNumber(running),
                      queued: formatNumber(queued),
                      ready: formatNumber(ready),
                      max: s.settings?.max_concurrent ? formatNumber(s.settings.max_concurrent) : "—",
                    })}
                  </span>
                </li>
              );
            })}
          </ul>
        </LayerCard.Primary>
      </LayerCard>
    </section>
  );
}

function PlatformCard({ sets }: { sets: ScaleSet[] }) {
  const t = useT();
  const templates = useTemplates();
  const cache = useCache();
  const settings = useSettings();
  const active = templates.data?.templates?.find((v) => v.state === "active");
  const listening = sets.filter((s) => s.listening).length;
  const rows: [string, ReactNode][] = [
    [
      t("overview.platform.template"),
      active?.slim_release ? (
        <span className="flex flex-wrap items-center gap-x-2">
          <Link href={`/templates/${encodeURIComponent(active.id)}`}>{active.slim_release}</Link>
          <RelativeTime className="text-kumo-subtle" value={active.activated_at} />
        </span>
      ) : (
        t("overview.platform.bootstrap")
      ),
    ],
    [
      t("overview.platform.cache"),
      !cache.data?.enabled ? (
        t("overview.platform.cacheOff")
      ) : (
        <Badge variant={cache.data.up ? "success" : "error"} appearance="dot">
          {cache.data.up ? t("overview.platform.cacheUp") : t("overview.platform.cacheDown")}
        </Badge>
      ),
    ],
    [t("overview.platform.listeners"), t("overview.platform.listenersValue", { n: formatNumber(listening), total: formatNumber(sets.length) })],
    [t("overview.platform.version"), settings.data?.version ?? "—"],
  ];
  return (
    <section aria-label={t("overview.platform.title")}>
      <LayerCard>
        <LayerCard.Secondary>{t("overview.platform.title")}</LayerCard.Secondary>
        <LayerCard.Primary className="p-0">
          <dl className="divide-y divide-kumo-line">
            {rows.map(([label, value]) => (
              <div key={label} className="flex min-w-0 items-center justify-between gap-3 px-4 py-2.5 text-sm">
                <dt className="text-kumo-subtle">{label}</dt>
                <dd className="min-w-0 truncate text-right">{value}</dd>
              </div>
            ))}
          </dl>
        </LayerCard.Primary>
      </LayerCard>
    </section>
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
      <Kpis ov={ov} />
      <Grid variant="2-1" gap="base">
        <div className="flex min-w-0 flex-col gap-4">
          <NowCard jobs={jobs.data ?? []} sets={sets.data ?? []} environments={envs.data ?? []} />
          <RecentCard jobs={jobs.data ?? []} />
        </div>
        <div className="flex min-w-0 flex-col gap-4">
          {(sets.data?.length ?? 0) > 0 && <ScaleSetsCard sets={sets.data ?? []} jobs={jobs.data ?? []} environments={envs.data ?? []} />}
          <PlatformCard sets={sets.data ?? []} />
        </div>
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
    </Page>
  );
}
