import { useMemo, useState } from "react";
import { Empty, LayerCard, Link, Pagination, Table } from "@cloudflare/kumo";
import { CubeIcon, FunnelSimpleIcon } from "@phosphor-icons/react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useEnvironments, useJobs } from "@/api/queries";
import { ErrorState, Loading, Page, RelativeTime, Truncate } from "@/components/common";
import { EnvironmentStateBadge } from "@/components/status-badge";
import { ListToolbar } from "@/components/list-toolbar";
import { formatMB } from "@/lib/format";
import { useFrozenOrder } from "@/lib/frozen-order";
import type { ListSearch } from "@/router";

const PER_PAGE = 25;
const LIVE = new Set(["pending", "provisioning", "booting", "connected", "idle", "running", "completing", "destroying", "failed"]);
const stateOptions = { live: "Live", failed: "Failed", destroyed: "Destroyed" };

export function EnvironmentsPage() {
  const search = useSearch({ strict: false }) as ListSearch;
  const navigate = useNavigate();
  const envs = useEnvironments({ limit: 1000 });
  const jobs = useJobs({ limit: 1000 });
  const [hovering, setHovering] = useState(false);
  const set = (patch: Partial<ListSearch>) =>
    void navigate({ to: "/environments", search: (prev: ListSearch) => ({ ...prev, page: undefined, ...patch }), replace: true });

  const all = useMemo(() => envs.data ?? [], [envs.data]);
  const jobNames = useMemo(() => new Map((jobs.data ?? []).map((j) => [j.id, j.display_name || j.id])), [jobs.data]);
  const filtered = useMemo(() => {
    const q = search.q?.toLowerCase();
    return all.filter(
      (e) =>
        (!search.state || (search.state === "live" ? LIVE.has(e.state) : e.state === search.state)) &&
        (!search.scale_set || e.scale_set === search.scale_set) &&
        (!q || `${e.id} ${e.runner_name ?? ""} ${e.ip ?? ""} ${e.runtime_ref ?? ""} ${e.job_id ?? ""} ${e.failure_reason ?? ""}`.toLowerCase().includes(q)),
    );
  }, [all, search]);
  const page = search.page ?? 1;
  const { rows, pending } = useFrozenOrder(filtered.slice((page - 1) * PER_PAGE, page * PER_PAGE), (e) => e.id, hovering);
  const scaleSets = Object.fromEntries([...new Set(all.map((e) => e.scale_set))].sort().map((s) => [s, s]));

  let body;
  if (envs.isLoading) body = <Loading />;
  else if (envs.error) body = <ErrorState error={envs.error} />;
  else if (all.length === 0)
    body = (
      <Empty
        icon={<CubeIcon size={48} className="text-kumo-inactive" />}
        title="No environments yet"
        description="An environment is created for every job a scale set receives, and destroyed when the job ends."
      />
    );
  else if (filtered.length === 0)
    body = <Empty icon={<FunnelSimpleIcon size={48} className="text-kumo-inactive" />} title="No environment matches these filters" description="Change or clear the filters." />;
  else
    body = (
      <>
        <div className="overflow-x-auto" onPointerEnter={() => setHovering(true)} onPointerLeave={() => setHovering(false)}>
          <Table className="min-w-[56rem]">
            <Table.Header>
              <Table.Row>
                <Table.Head>Environment</Table.Head>
                <Table.Head>State</Table.Head>
                <Table.Head>Scale set</Table.Head>
                <Table.Head>Job</Table.Head>
                <Table.Head>IP</Table.Head>
                <Table.Head>Memory</Table.Head>
                <Table.Head>Created</Table.Head>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {rows.map((e) => (
                <Table.Row key={e.id}>
                  <Table.Cell className="max-w-64">
                    <Link href={`/environments/${encodeURIComponent(e.id)}`} className="block truncate font-mono text-sm">
                      {e.id}
                    </Link>
                    {e.failure_stage && <Truncate className="text-xs text-kumo-danger" text={`at ${e.failure_stage}: ${e.failure_reason ?? ""}`} />}
                  </Table.Cell>
                  <Table.Cell>
                    <EnvironmentStateBadge state={e.state} />
                  </Table.Cell>
                  <Table.Cell>{e.scale_set}</Table.Cell>
                  <Table.Cell className="max-w-56">
                    {e.job_id ? (
                      <Link href={`/jobs/${encodeURIComponent(e.job_id)}`} className="block truncate">
                        {jobNames.get(e.job_id) ?? e.job_id}
                      </Link>
                    ) : (
                      <span className="text-kumo-subtle">—</span>
                    )}
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
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-kumo-line px-3 py-2">
          <span className="text-sm text-kumo-subtle" aria-live="polite">
            {pending > 0 ? `${pending} new — move the pointer away to show them` : `${filtered.length.toLocaleString()} environments`}
          </span>
          <Pagination page={page} setPage={(p) => set({ page: p > 1 ? p : undefined })} perPage={PER_PAGE} totalCount={filtered.length} />
        </div>
      </>
    );

  return (
    <Page title="Environments" description="One short-lived LXC per job, from creation to destruction.">
      <ListToolbar
        search={search.q ?? ""}
        onSearch={(q) => set({ q: q || undefined })}
        searchLabel="Search environments"
        filters={[
          { key: "state", label: "State", value: search.state, options: stateOptions },
          { key: "scale_set", label: "Scale set", value: search.scale_set, options: scaleSets },
        ]}
        onFilter={(key, value) => set({ [key]: value })}
        onClear={() => void navigate({ to: "/environments", search: {}, replace: true })}
      />
      <LayerCard>
        <LayerCard.Primary className="p-0">{body}</LayerCard.Primary>
      </LayerCard>
    </Page>
  );
}
