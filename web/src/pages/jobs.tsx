import { useMemo, useState } from "react";
import { Empty, LayerCard, Link, Pagination, Table } from "@cloudflare/kumo";
import { BriefcaseIcon, FunnelSimpleIcon } from "@phosphor-icons/react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import type { Job } from "@/api/client";
import { useJobs, useScaleSets } from "@/api/queries";
import { ErrorState, Loading, Page, RelativeTime, Truncate, useNow } from "@/components/common";
import { JobStatusBadge } from "@/components/status-badge";
import { ListToolbar } from "@/components/list-toolbar";
import { durationBetween, formatDuration, formatNumber, isSet } from "@/lib/format";
import { useT, type Key } from "@/i18n";
import { useFrozenOrder } from "@/lib/frozen-order";
import type { ListSearch } from "@/router";

const PER_PAGE = 25;
const LIST_LIMIT = 1000;

const statuses = ["running", "assigned", "succeeded", "failed", "canceled"] as const;
const ranges = ["1h", "24h", "7d"] as const;
const rangeMs: Record<string, number> = { "1h": 3_600_000, "24h": 86_400_000, "7d": 604_800_000 };

function matchesStatus(j: Job, status: string) {
  if (status === "running" || status === "assigned") return j.status === status;
  return j.status === "completed" && j.result === status;
}

export function JobDuration({ job, now }: { job: Job; now: Date }) {
  if (!isSet(job.started_at)) return <span className="text-kumo-subtle">—</span>;
  const end = isSet(job.finished_at) ? job.finished_at : now.toISOString();
  return <span className="tabular-nums">{formatDuration(durationBetween(job.started_at, end))}</span>;
}

export function JobsPage() {
  const search = useSearch({ strict: false }) as ListSearch;
  const navigate = useNavigate();
  const now = useNow();
  // Status and scale set filter on the server, so they reach past the newest LIST_LIMIT jobs.
  const serverStatus = search.status === "running" || search.status === "assigned" ? search.status : search.status ? "completed" : undefined;
  const jobs = useJobs({ limit: LIST_LIMIT, status: serverStatus, scaleSet: search.scale_set });
  const sets = useScaleSets();
  const [hovering, setHovering] = useState(false);
  const t = useT();
  const statusOptions = Object.fromEntries(statuses.map((s) => [s, t(`overview.jobs.status.${s}` as Key)]));
  const rangeOptions = Object.fromEntries(ranges.map((r) => [r, t(`overview.jobs.range.${r}` as Key)]));

  const set = (patch: Partial<ListSearch>) =>
    void navigate({ to: "/jobs", search: (prev: ListSearch) => ({ ...prev, page: undefined, ...patch }), replace: true });

  const all = useMemo(() => jobs.data ?? [], [jobs.data]);
  const filtered = useMemo(() => {
    const q = search.q?.toLowerCase();
    const since = search.range ? now.getTime() - (rangeMs[search.range] ?? 0) : 0;
    return all.filter(
      (j) =>
        (!search.status || matchesStatus(j, search.status)) &&
        (!search.scale_set || j.scale_set === search.scale_set) &&
        (!search.repo || j.repository === search.repo) &&
        (!since || Date.parse(j.queued_at) >= since) &&
        (!q || `${j.display_name} ${j.repository} ${j.workflow_ref ?? ""} ${j.id} ${j.run_id ?? ""} ${j.runner_name ?? ""}`.toLowerCase().includes(q)),
    );
  }, [all, search, now]);

  const page = search.page ?? 1;
  const pageRows = filtered.slice((page - 1) * PER_PAGE, page * PER_PAGE);
  const { rows, pending } = useFrozenOrder(pageRows, (j) => j.id, hovering);

  const scaleSets = Object.fromEntries([...new Set([...(sets.data ?? []).map((s) => s.name), ...all.map((j) => j.scale_set)])].sort().map((s) => [s, s]));
  const repos = Object.fromEntries([...new Set(all.map((j) => j.repository))].sort().map((s) => [s, s]));

  let body;
  if (jobs.isLoading) body = <Loading />;
  else if (jobs.error) body = <ErrorState error={jobs.error} />;
  else if (all.length === 0)
    body = (
      <Empty
        icon={<BriefcaseIcon size={48} className="text-kumo-inactive" />}
        title={t("overview.jobs.empty.title")}
        description={t("overview.jobs.empty.description")}
        contents={<Link href="/scale-sets">{t("overview.jobs.empty.link")}</Link>}
      />
    );
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
        <div className="overflow-x-auto" onPointerEnter={() => setHovering(true)} onPointerLeave={() => setHovering(false)}>
          <Table layout="auto" className="min-w-[56rem]">
            <Table.Header>
              <Table.Row>
                <Table.Head>{t("overview.jobs.columns.job")}</Table.Head>
                <Table.Head>{t("overview.jobs.columns.repository")}</Table.Head>
                <Table.Head>{t("overview.jobs.columns.scaleSet")}</Table.Head>
                <Table.Head>{t("overview.jobs.columns.status")}</Table.Head>
                <Table.Head>{t("overview.jobs.columns.queued")}</Table.Head>
                <Table.Head>{t("overview.jobs.columns.duration")}</Table.Head>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {rows.map((j) => (
                <Table.Row key={j.id}>
                  <Table.Cell className="max-w-80">
                    <Link href={`/jobs/${encodeURIComponent(j.id)}`} className="block truncate font-medium" title={j.display_name || j.id}>
                      {j.display_name || j.id}
                    </Link>
                    {j.workflow_ref && <Truncate className="text-xs text-kumo-subtle" text={j.workflow_ref} />}
                  </Table.Cell>
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
                </Table.Row>
              ))}
            </Table.Body>
          </Table>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-kumo-line px-3 py-2">
          <span className="text-sm text-kumo-subtle" aria-live="polite">
            {pending > 0
              ? t("overview.jobs.pending", { count: pending })
              : t(all.length >= LIST_LIMIT ? "overview.jobs.countCapped" : "overview.jobs.count", {
                  count: filtered.length,
                  n: formatNumber(filtered.length),
                  limit: formatNumber(LIST_LIMIT),
                })}
          </span>
          <Pagination page={page} setPage={(p) => set({ page: p > 1 ? p : undefined })} perPage={PER_PAGE} totalCount={filtered.length} />
        </div>
      </>
    );

  return (
    <Page title={t("overview.jobs.title")} description={t("overview.jobs.description")}>
      <ListToolbar
        search={search.q ?? ""}
        onSearch={(q) => set({ q: q || undefined })}
        searchLabel={t("overview.jobs.search")}
        filters={[
          { key: "status", label: t("overview.jobs.columns.status"), value: search.status, options: statusOptions },
          { key: "scale_set", label: t("overview.jobs.columns.scaleSet"), value: search.scale_set, options: scaleSets },
          { key: "repo", label: t("overview.jobs.columns.repository"), value: search.repo, options: repos },
          { key: "range", label: t("overview.jobs.columns.queued"), value: search.range, options: rangeOptions },
        ]}
        onFilter={(key, value) => set({ [key]: value })}
        onClear={() => void navigate({ to: "/jobs", search: {}, replace: true })}
      />
      <LayerCard>
        <LayerCard.Primary className="p-0">{body}</LayerCard.Primary>
      </LayerCard>
    </Page>
  );
}
