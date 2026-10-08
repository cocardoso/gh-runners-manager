import { useMemo, useState } from "react";
import { Badge, Empty, LayerCard, Link, Table } from "@cloudflare/kumo";
import { ClockCounterClockwiseIcon, CubeIcon, FunnelSimpleIcon } from "@phosphor-icons/react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import type { Environment, Job } from "@/api/client";
import { useEnvironments, useJobs, useScaleSets } from "@/api/queries";
import { ErrorState, Loading, Page, RelativeTime, Truncate, useNow } from "@/components/common";
import { DetailTabs } from "@/components/detail-tabs";
import { EnvironmentStateBadge, JobStatusBadge } from "@/components/status-badge";
import { ListToolbar } from "@/components/list-toolbar";
import { AppPagination } from "@/components/app-pagination";
import { currentFormatLocale, useT } from "@/i18n";
import { durationBetween, formatDuration, formatMB, formatNumber } from "@/lib/format";
import { useFrozenOrder } from "@/lib/frozen-order";
import type { ListSearch } from "@/router";

const PER_PAGE = 25;
const LIST_LIMIT = 1000;
/** Every state of an environment that still exists: failed ones are kept for debugging until reaped. */
const LIVE = ["pending", "provisioning", "booting", "connected", "idle", "running", "completing", "destroying", "failed"];

type Tab = "running" | "history";

// The language's own clock (12 or 24 h), with the day when it is not today.
function formatUntil(t: Date, now: number) {
  const today = new Date(now).toDateString() === t.toDateString();
  return new Intl.DateTimeFormat(currentFormatLocale(), today ? { hour: "numeric", minute: "2-digit" } : { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }).format(t);
}

/** A failed environment stays until its scale set's keep_on_failure_minutes have passed
 * since it failed (the reaper's rule); past that, or without its scale set, it is going. */
function KeptBadge({ env, keepMinutes, known }: { env: Environment; keepMinutes: number | undefined; known: boolean }) {
  const t = useT();
  const now = useNow();
  const failedAt = Date.parse(env.state_changed_at);
  const until = keepMinutes !== undefined && Number.isFinite(failedAt) ? failedAt + keepMinutes * 60_000 : undefined;
  const label = !known
    ? t("environments.list.kept")
    : until === undefined || keepMinutes === 0 || until <= now.getTime()
      ? t("environments.list.removing")
      : t("environments.list.keptUntil", { time: formatUntil(new Date(until), now.getTime()) });
  return (
    <Badge variant="warning" appearance="dot" className="whitespace-nowrap">
      {label}
    </Badge>
  );
}

function JobCell({ id, jobs }: { id: string | undefined; jobs: Map<string, Job> }) {
  if (!id) return <span className="text-kumo-subtle">—</span>;
  return (
    <Link href={`/jobs/${encodeURIComponent(id)}`} className="block truncate">
      {jobs.get(id)?.display_name || id}
    </Link>
  );
}

function EnvLink({ env }: { env: Environment }) {
  return (
    <Link href={`/environments/${encodeURIComponent(env.id)}`} className="block truncate font-mono text-sm">
      {env.id}
    </Link>
  );
}

export function EnvironmentsPage() {
  const search = useSearch({ strict: false }) as ListSearch;
  const navigate = useNavigate();
  const t = useT();
  const tab: Tab = search.tab === "history" ? "history" : "running";
  // Tab, state and scale set filter on the server, so they reach past the newest LIST_LIMIT environments.
  const envs = useEnvironments({ limit: LIST_LIMIT, state: tab === "history" ? "destroyed" : LIVE.join(","), scaleSet: search.scale_set });
  const sets = useScaleSets();
  const jobs = useJobs({ limit: 1000 });
  const [hovering, setHovering] = useState(false);
  const set = (patch: Partial<ListSearch>) =>
    void navigate({ to: "/environments", search: (prev: ListSearch) => ({ ...prev, page: undefined, ...patch }), replace: true });

  const all = useMemo(() => envs.data ?? [], [envs.data]);
  const jobsById = useMemo(() => new Map((jobs.data ?? []).map((j) => [j.id, j])), [jobs.data]);
  const keepMinutes = useMemo(() => new Map((sets.data ?? []).map((s) => [s.name, s.settings?.keep_on_failure_minutes ?? 0])), [sets.data]);
  const filtered = useMemo(() => {
    const q = search.q?.toLowerCase();
    const states = new Set(tab === "history" ? ["destroyed"] : LIVE);
    const list = all.filter(
      (e) =>
        states.has(e.state) &&
        (!search.scale_set || e.scale_set === search.scale_set) &&
        (tab !== "history" || !search.status || (search.status === "failed") === !!e.failure_stage) &&
        (!q || `${e.id} ${e.runner_name ?? ""} ${e.ip ?? ""} ${e.runtime_ref ?? ""} ${e.job_id ?? ""} ${e.failure_reason ?? ""}`.toLowerCase().includes(q)),
    );
    // History reads newest ending first.
    return tab === "history" ? list.toSorted((a, b) => Date.parse(b.state_changed_at) - Date.parse(a.state_changed_at)) : list;
  }, [all, search, tab]);
  // Live lists shrink: never past the last page.
  const page = Math.min(search.page ?? 1, Math.max(1, Math.ceil(filtered.length / PER_PAGE)));
  const { rows, pending } = useFrozenOrder(filtered.slice((page - 1) * PER_PAGE, page * PER_PAGE), (e) => e.id, hovering);
  const scaleSets = Object.fromEntries([...new Set([...(sets.data ?? []).map((s) => s.name), ...all.map((e) => e.scale_set)])].sort().map((s) => [s, s]));

  const runningTable = (
    <Table className="min-w-[56rem]">
      <Table.Header>
        <Table.Row>
          <Table.Head>{t("environments.list.col.environment")}</Table.Head>
          <Table.Head>{t("environments.list.col.state")}</Table.Head>
          <Table.Head>{t("environments.list.col.scaleSet")}</Table.Head>
          <Table.Head>{t("environments.list.col.job")}</Table.Head>
          <Table.Head>{t("environments.list.col.ip")}</Table.Head>
          <Table.Head>{t("environments.list.col.memory")}</Table.Head>
          <Table.Head>{t("environments.list.col.created")}</Table.Head>
        </Table.Row>
      </Table.Header>
      <Table.Body>
        {rows.map((e) => (
          <Table.Row key={e.id}>
            <Table.Cell className="max-w-64">
              <EnvLink env={e} />
              {e.failure_stage && <Truncate className="text-xs text-kumo-danger" text={t("environments.list.failedAt", { stage: e.failure_stage, reason: e.failure_reason ?? "" })} />}
            </Table.Cell>
            <Table.Cell>
              <div className="flex flex-wrap items-center gap-1.5">
                <EnvironmentStateBadge state={e.state} />
                {e.state === "failed" && <KeptBadge env={e} keepMinutes={keepMinutes.get(e.scale_set)} known={sets.data !== undefined} />}
              </div>
            </Table.Cell>
            <Table.Cell>{e.scale_set}</Table.Cell>
            <Table.Cell className="max-w-56">
              <JobCell id={e.job_id} jobs={jobsById} />
            </Table.Cell>
            <Table.Cell className="font-mono text-sm">{e.ip || "—"}</Table.Cell>
            <Table.Cell className="tabular-nums">{formatMB(e.memory_mb)}</Table.Cell>
            <Table.Cell>
              <RelativeTime value={e.created_at} />
            </Table.Cell>
          </Table.Row>
        ))}
      </Table.Body>
    </Table>
  );

  const historyTable = (
    <Table className="min-w-[56rem]">
      <Table.Header>
        <Table.Row>
          <Table.Head>{t("environments.list.col.environment")}</Table.Head>
          <Table.Head>{t("environments.list.col.scaleSet")}</Table.Head>
          <Table.Head>{t("environments.list.col.job")}</Table.Head>
          <Table.Head>{t("environments.list.col.result")}</Table.Head>
          <Table.Head>{t("environments.list.col.lived")}</Table.Head>
          <Table.Head>{t("environments.list.col.ended")}</Table.Head>
        </Table.Row>
      </Table.Header>
      <Table.Body>
        {rows.map((e) => {
          const j = e.job_id ? jobsById.get(e.job_id) : undefined;
          return (
            <Table.Row key={e.id}>
              <Table.Cell className="max-w-64">
                <EnvLink env={e} />
              </Table.Cell>
              <Table.Cell>{e.scale_set}</Table.Cell>
              <Table.Cell className="max-w-56">
                <JobCell id={e.job_id} jobs={jobsById} />
              </Table.Cell>
              <Table.Cell className="max-w-72">
                {e.failure_stage ? (
                  <div className="flex min-w-0 flex-col gap-0.5">
                    <EnvironmentStateBadge state="failed" />
                    <Truncate className="text-xs text-kumo-danger" text={t("environments.list.failedAt", { stage: e.failure_stage, reason: e.failure_reason ?? "" })} />
                  </div>
                ) : j?.status === "completed" && j.result ? (
                  <JobStatusBadge status={j.status} result={j.result} />
                ) : (
                  <span className="text-sm text-kumo-subtle">{t("environments.list.history.noFailure")}</span>
                )}
              </Table.Cell>
              <Table.Cell className="tabular-nums">{formatDuration(durationBetween(e.created_at, e.state_changed_at))}</Table.Cell>
              <Table.Cell>
                <RelativeTime value={e.state_changed_at} />
              </Table.Cell>
            </Table.Row>
          );
        })}
      </Table.Body>
    </Table>
  );

  let body;
  // While the other tab's list loads, its placeholder is this tab's data: show loading,
  // not a list (or an empty state) that is not true.
  if (envs.isLoading || envs.isPlaceholderData) body = <Loading />;
  else if (envs.error) body = <ErrorState error={envs.error} />;
  else if (all.filter((e) => (tab === "history" ? e.state === "destroyed" : LIVE.includes(e.state))).length === 0 && !search.scale_set)
    body =
      tab === "history" ? (
        <Empty
          icon={<ClockCounterClockwiseIcon size={48} className="text-kumo-inactive" />}
          title={t("environments.list.history.emptyTitle")}
          description={t("environments.list.history.emptyDescription")}
        />
      ) : (
        <Empty
          icon={<CubeIcon size={48} className="text-kumo-inactive" />}
          title={t("environments.list.running.emptyTitle")}
          description={t("environments.list.running.emptyDescription")}
          contents={<Link href="/environments?tab=history">{t("environments.list.running.historyLink")}</Link>}
        />
      );
  else if (filtered.length === 0)
    body = <Empty icon={<FunnelSimpleIcon size={48} className="text-kumo-inactive" />} title={t("environments.list.noMatchTitle")} description={t("environments.list.noMatchDescription")} />;
  else
    body = (
      <>
        <div className="overflow-x-auto" onPointerEnter={() => setHovering(true)} onPointerLeave={() => setHovering(false)}>
          {tab === "history" ? historyTable : runningTable}
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-kumo-line px-3 py-2">
          <span className="text-sm text-kumo-subtle" aria-live="polite">
            {pending > 0
              ? t("environments.list.pending", { count: pending, n: formatNumber(pending) })
              : `${t("environments.list.count", { count: filtered.length, n: formatNumber(filtered.length) })}${all.length >= LIST_LIMIT ? ` ${t("environments.list.limited", { limit: formatNumber(LIST_LIMIT) })}` : ""}`}
          </span>
          <AppPagination page={page} setPage={(p) => set({ page: p > 1 ? p : undefined })} perPage={PER_PAGE} totalCount={filtered.length} />
        </div>
      </>
    );

  const tabs = [
    { value: "running", label: t("environments.list.tabs.running") },
    { value: "history", label: t("environments.list.tabs.history") },
  ];
  const filters = [{ key: "scale_set", label: t("environments.list.filter.scaleSet"), value: search.scale_set, options: scaleSets }];
  if (tab === "history")
    filters.push({
      key: "status",
      label: t("environments.list.filter.outcome"),
      value: search.status,
      options: { failed: t("environments.list.filter.failed"), clean: t("environments.list.filter.clean") },
    });

  return (
    <Page title={t("environments.list.title")} description={t("environments.list.description")}>
      <DetailTabs push tabs={tabs} value={tab}>
        <ListToolbar
          search={search.q ?? ""}
          onSearch={(q) => set({ q: q || undefined })}
          searchLabel={t("environments.list.search")}
          filters={filters}
          onFilter={(key, value) => set({ [key]: value })}
          onClear={() => void navigate({ to: "/environments", search: tab === "history" ? { tab } : {}, replace: true })}
        />
        <LayerCard>
          <LayerCard.Primary className="p-0">{body}</LayerCard.Primary>
        </LayerCard>
      </DetailTabs>
    </Page>
  );
}
