import { useMemo, useState, type ReactNode } from "react";
import { Empty, LayerCard, Link, Table } from "@cloudflare/kumo";
import { ViewToggle } from "@/components/view-toggle";
import { useViewMode, type ViewMode } from "@/lib/view-mode";
import { BriefcaseIcon, FunnelSimpleIcon, HourglassIcon } from "@phosphor-icons/react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import type { Job } from "@/api/client";
import { useJobs, useScaleSets } from "@/api/queries";
import { ErrorState, Loading, Page, RelativeTime, Truncate, useNow } from "@/components/common";
import { JobStatusBadge } from "@/components/status-badge";
import { ListToolbar, type FilterDef } from "@/components/list-toolbar";
import { DetailTabs } from "@/components/detail-tabs";
import { durationBetween, formatDuration, formatNumber, isSet } from "@/lib/format";
import { AppPagination } from "@/components/app-pagination";
import { useT, type Key } from "@/i18n";
import { useFrozenOrder } from "@/lib/frozen-order";
import type { ListSearch } from "@/router";

const PER_PAGE = 25;
const LIST_LIMIT = 1000;

const results = ["succeeded", "failed", "canceled"] as const;
const ranges = ["1h", "24h", "7d"] as const;
const rangeMs: Record<string, number> = { "1h": 3_600_000, "24h": 86_400_000, "7d": 604_800_000 };

export function JobDuration({ job, now }: { job: Job; now: Date }) {
  if (!isSet(job.started_at)) return <span className="text-kumo-subtle">—</span>;
  const end = isSet(job.finished_at) ? job.finished_at : now.toISOString();
  return <span className="tabular-nums">{formatDuration(durationBetween(job.started_at, end))}</span>;
}

/** When a completed job finished; older rows without a finish time fall back to their last update. */
function finishedAt(j: Job) {
  return isSet(j.finished_at) ? j.finished_at : j.updated_at;
}

function matchesSearch(j: Job, q: string | undefined) {
  return !q || `${j.display_name} ${j.repository} ${j.workflow_ref ?? ""} ${j.id} ${j.run_id ?? ""} ${j.runner_name ?? ""}`.toLowerCase().includes(q);
}

function useListNav() {
  const navigate = useNavigate();
  return (patch: Partial<ListSearch>) =>
    void navigate({ to: "/jobs", search: (prev: ListSearch) => ({ ...prev, page: undefined, ...patch }), replace: true });
}

export function JobsPage() {
  const search = useSearch({ strict: false }) as ListSearch;
  const t = useT();
  const history = search.tab === "history";
  const [view, setView] = useViewMode("jobs", "list");
  const tabs = [
    { value: "now", label: t("overview.jobs.tabs.inProgress") },
    { value: "history", label: t("overview.jobs.tabs.history") },
  ];
  return (
    <Page title={t("overview.jobs.title")} description={t("overview.jobs.description")} actions={<ViewToggle value={view} onChange={setView} />}>
      <DetailTabs push tabs={tabs} value={history ? "history" : "now"}>
        {history ? <JobHistory search={search} view={view} /> : <JobsInProgress search={search} view={view} />}
      </DetailTabs>
    </Page>
  );
}

function JobColumn({ j }: { j: Job }) {
  return (
    <Table.Cell className="max-w-80">
      <Link href={`/jobs/${encodeURIComponent(j.id)}`} className="block truncate font-medium" title={j.display_name || j.id}>
        {j.display_name || j.id}
      </Link>
      {j.workflow_ref && <Truncate className="text-xs text-kumo-subtle" text={j.workflow_ref} />}
    </Table.Cell>
  );
}

/** Jobs assigned to a scale set or running now. The API takes one status, so both are asked for. */
function JobsInProgress({ search, view }: { search: ListSearch; view: ViewMode }) {
  const t = useT();
  const now = useNow();
  const set = useListNav();
  const assigned = useJobs({ limit: LIST_LIMIT, status: "assigned", scaleSet: search.scale_set });
  const running = useJobs({ limit: LIST_LIMIT, status: "running", scaleSet: search.scale_set });
  const sets = useScaleSets();

  // A job can move from assigned to running between the two answers: keep one row per job, its running one.
  const all = useMemo(() => {
    const byId = new Map<string, Job>();
    for (const j of [...(assigned.data ?? []), ...(running.data ?? [])]) if (j.status === "assigned" || j.status === "running") byId.set(j.id, j);
    return [...byId.values()].sort((a, b) => Date.parse(b.queued_at) - Date.parse(a.queued_at));
  }, [running.data, assigned.data]);
  const filtered = useMemo(() => {
    const q = search.q?.toLowerCase();
    return all.filter((j) => (!search.scale_set || j.scale_set === search.scale_set) && (!search.repo || j.repository === search.repo) && matchesSearch(j, q));
  }, [all, search]);

  return (
    <JobList
      search={search}
      view={view}
      set={set}
      all={all}
      filtered={filtered}
      facts={(j) => [
        [t("overview.jobs.columns.queued"), <RelativeTime key="q" value={j.queued_at} />],
        [t("overview.jobs.columns.runningFor"), <JobDuration key="d" job={j} now={now} />],
      ]}
      capped={(assigned.data?.length ?? 0) >= LIST_LIMIT || (running.data?.length ?? 0) >= LIST_LIMIT}
      isLoading={assigned.isLoading || running.isLoading}
      error={assigned.error ?? running.error}
      filters={[scaleSetFilter(t, search, sets.data?.map((s) => s.name), all), repoFilter(t, search, all)]}
      empty={
        <Empty
          icon={<HourglassIcon size={48} className="text-kumo-inactive" />}
          title={t("overview.jobs.inProgressEmpty.title")}
          description={t("overview.jobs.inProgressEmpty.description")}
          contents={<Link href="/jobs?tab=history">{t("overview.jobs.inProgressEmpty.link")}</Link>}
        />
      }
      head={
        <>
          <Table.Head>{t("overview.jobs.columns.job")}</Table.Head>
          <Table.Head>{t("overview.jobs.columns.repository")}</Table.Head>
          <Table.Head>{t("overview.jobs.columns.scaleSet")}</Table.Head>
          <Table.Head>{t("overview.jobs.columns.status")}</Table.Head>
          <Table.Head>{t("overview.jobs.columns.queued")}</Table.Head>
          <Table.Head>{t("overview.jobs.columns.runningFor")}</Table.Head>
        </>
      }
      row={(j) => (
        <>
          <JobColumn j={j} />
          <Table.Cell className="max-w-56">
            <Truncate text={j.repository} />
          </Table.Cell>
          <Table.Cell>{j.scale_set}</Table.Cell>
          <Table.Cell>
            <JobStatusBadge status={j.status} result={j.result} />
          </Table.Cell>
          <Table.Cell>
            <RelativeTime value={j.queued_at} />
          </Table.Cell>
          <Table.Cell>
            <JobDuration job={j} now={now} />
          </Table.Cell>
        </>
      )}
    />
  );
}

/** Completed jobs, newest first, filtered by result, scale set, repository and period. */
function JobHistory({ search, view }: { search: ListSearch; view: ViewMode }) {
  const t = useT();
  const now = useNow();
  const set = useListNav();
  const jobs = useJobs({ limit: LIST_LIMIT, status: "completed", scaleSet: search.scale_set });
  const sets = useScaleSets();
  const result = results.includes(search.status as (typeof results)[number]) ? search.status : undefined;

  const all = useMemo(() => (jobs.data ?? []).filter((j) => j.status === "completed").sort((a, b) => Date.parse(finishedAt(b)) - Date.parse(finishedAt(a))), [jobs.data]);
  const filtered = useMemo(() => {
    const q = search.q?.toLowerCase();
    const since = search.range ? now.getTime() - (rangeMs[search.range] ?? 0) : 0;
    return all.filter(
      (j) =>
        (!result || j.result === result) &&
        (!search.scale_set || j.scale_set === search.scale_set) &&
        (!search.repo || j.repository === search.repo) &&
        (!since || Date.parse(finishedAt(j)) >= since) &&
        matchesSearch(j, q),
    );
  }, [all, search, result, now]);

  return (
    <JobList
      search={search}
      view={view}
      set={set}
      all={all}
      filtered={filtered}
      facts={(j) => [
        [t("overview.jobs.columns.duration"), <JobDuration key="d" job={j} now={now} />],
        [t("overview.jobs.columns.finished"), <RelativeTime key="f" value={finishedAt(j)} />],
      ]}
      capped={all.length >= LIST_LIMIT}
      isLoading={jobs.isLoading}
      error={jobs.error}
      filters={[
        {
          key: "status",
          label: t("overview.jobs.columns.result"),
          value: result,
          options: Object.fromEntries(results.map((r) => [r, t(`overview.jobs.result.${r}` as Key)])),
        },
        scaleSetFilter(t, search, sets.data?.map((s) => s.name), all),
        repoFilter(t, search, all),
        {
          key: "range",
          label: t("overview.jobs.columns.finished"),
          value: search.range,
          options: Object.fromEntries(ranges.map((r) => [r, t(`overview.jobs.range.${r}` as Key)])),
        },
      ]}
      empty={
        <Empty
          icon={<BriefcaseIcon size={48} className="text-kumo-inactive" />}
          title={t("overview.jobs.historyEmpty.title")}
          description={t("overview.jobs.historyEmpty.description")}
          contents={<Link href="/scale-sets">{t("overview.jobs.historyEmpty.link")}</Link>}
        />
      }
      head={
        <>
          <Table.Head>{t("overview.jobs.columns.job")}</Table.Head>
          <Table.Head>{t("overview.jobs.columns.repository")}</Table.Head>
          <Table.Head>{t("overview.jobs.columns.scaleSet")}</Table.Head>
          <Table.Head>{t("overview.jobs.columns.result")}</Table.Head>
          <Table.Head>{t("overview.jobs.columns.duration")}</Table.Head>
          <Table.Head>{t("overview.jobs.columns.finished")}</Table.Head>
        </>
      }
      row={(j) => (
        <>
          <JobColumn j={j} />
          <Table.Cell className="max-w-56">
            <Truncate text={j.repository} />
          </Table.Cell>
          <Table.Cell>{j.scale_set}</Table.Cell>
          <Table.Cell>
            <JobStatusBadge status={j.status} result={j.result} />
          </Table.Cell>
          <Table.Cell>
            <JobDuration job={j} now={now} />
          </Table.Cell>
          <Table.Cell>
            <RelativeTime value={finishedAt(j)} />
          </Table.Cell>
        </>
      )}
    />
  );
}

type T = ReturnType<typeof useT>;

function scaleSetFilter(t: T, search: ListSearch, configured: string[] | undefined, all: Job[]): FilterDef {
  const names = [...new Set([...(configured ?? []), ...all.map((j) => j.scale_set)])].sort();
  return { key: "scale_set", label: t("overview.jobs.columns.scaleSet"), value: search.scale_set, options: Object.fromEntries(names.map((s) => [s, s])) };
}

function repoFilter(t: T, search: ListSearch, all: Job[]): FilterDef {
  const repos = [...new Set(all.map((j) => j.repository))].sort();
  return { key: "repo", label: t("overview.jobs.columns.repository"), value: search.repo, options: Object.fromEntries(repos.map((s) => [s, s])) };
}

/** A job as a card: name, status, where it runs and the tab's times. */
function JobCard({ j, facts }: { j: Job; facts: [string, ReactNode][] }) {
  return (
    <article aria-label={j.display_name || j.id} className="flex min-w-0 flex-col gap-2 rounded-lg border border-kumo-line bg-kumo-base p-3">
      <div className="flex min-w-0 items-start justify-between gap-2">
        <div className="min-w-0">
          <Link href={`/jobs/${encodeURIComponent(j.id)}`} className="block truncate font-medium" title={j.display_name || j.id}>
            {j.display_name || j.id}
          </Link>
          {j.workflow_ref && <Truncate className="text-xs text-kumo-subtle" text={j.workflow_ref} />}
        </div>
        <JobStatusBadge status={j.status} result={j.result} />
      </div>
      <Truncate className="text-sm text-kumo-subtle" text={`${j.repository} · ${j.scale_set}`} />
      <dl className="grid grid-cols-2 gap-2 text-sm">
        {facts.map(([label, value]) => (
          <div key={label} className="min-w-0">
            <dt className="text-xs text-kumo-subtle">{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </article>
  );
}

function JobList({
  search,
  view,
  set,
  all,
  filtered,
  facts,
  capped,
  isLoading,
  error,
  filters,
  empty,
  head,
  row,
}: {
  search: ListSearch;
  view: ViewMode;
  set: (patch: Partial<ListSearch>) => void;
  all: Job[];
  filtered: Job[];
  /** What a card shows below the job: the tab's times. */
  facts: (j: Job) => [string, ReactNode][];
  capped: boolean;
  isLoading: boolean;
  error: unknown;
  filters: FilterDef[];
  empty: ReactNode;
  head: ReactNode;
  row: (j: Job) => ReactNode;
}) {
  const t = useT();
  const navigate = useNavigate();
  const [hovering, setHovering] = useState(false);
  // The tab switch keeps the other tab's page, so clamp to the pages this tab has.
  const pages = Math.max(1, Math.ceil(filtered.length / PER_PAGE));
  const page = Math.min(search.page ?? 1, pages);
  const pageRows = filtered.slice((page - 1) * PER_PAGE, page * PER_PAGE);
  const { rows, pending } = useFrozenOrder(pageRows, (j) => j.id, hovering);

  let body;
  if (isLoading) body = <Loading />;
  else if (error) body = <ErrorState error={error} />;
  else if (all.length === 0) body = empty;
  else if (filtered.length === 0)
    body = (
      <Empty
        icon={<FunnelSimpleIcon size={48} className="text-kumo-inactive" />}
        title={t("overview.jobs.noMatch.title")}
        description={t("overview.jobs.noMatch.description")}
      />
    );
  else
    body = (
      <>
        {view === "cards" ? (
          <div className="grid gap-3 p-3 sm:grid-cols-2 xl:grid-cols-3" onPointerEnter={() => setHovering(true)} onPointerLeave={() => setHovering(false)}>
            {rows.map((j) => (
              <JobCard key={j.id} j={j} facts={facts(j)} />
            ))}
          </div>
        ) : (
          <div className="overflow-x-auto" onPointerEnter={() => setHovering(true)} onPointerLeave={() => setHovering(false)}>
            <Table layout="auto" className="min-w-[56rem]">
              <Table.Header>
                <Table.Row>{head}</Table.Row>
              </Table.Header>
              <Table.Body>
                {rows.map((j) => (
                  <Table.Row key={j.id}>{row(j)}</Table.Row>
                ))}
              </Table.Body>
            </Table>
          </div>
        )}
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-kumo-line px-3 py-2">
          <span className="text-sm text-kumo-subtle" aria-live="polite">
            {pending > 0
              ? t("overview.jobs.pending", { count: pending, n: formatNumber(pending) })
              : t(capped ? "overview.jobs.countCapped" : "overview.jobs.count", {
                  count: filtered.length,
                  n: formatNumber(filtered.length),
                  limit: formatNumber(LIST_LIMIT),
                })}
          </span>
          <AppPagination page={page} setPage={(p) => set({ page: p > 1 ? p : undefined })} perPage={PER_PAGE} totalCount={filtered.length} />
        </div>
      </>
    );

  return (
    <>
      <ListToolbar
        search={search.q ?? ""}
        onSearch={(q) => set({ q: q || undefined })}
        searchLabel={t("overview.jobs.search")}
        filters={filters}
        onFilter={(key, value) => set({ [key]: value })}
        onClear={() => void navigate({ to: "/jobs", search: { tab: search.tab }, replace: true })}
      />
      <LayerCard>
        <LayerCard.Primary className="p-0">{body}</LayerCard.Primary>
      </LayerCard>
    </>
  );
}
