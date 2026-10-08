import { Badge, Empty, LayerCard, Link, Table } from "@cloudflare/kumo";
import { GitBranchIcon } from "@phosphor-icons/react";
import type { Repository } from "@/api/client";
import { useRepositories } from "@/api/queries";
import { ErrorState, Loading, Page, RelativeTime, Truncate } from "@/components/common";
import { JobStatusBadge } from "@/components/status-badge";
import { useT } from "@/i18n";
import { formatNumber, formatPercent, isSet } from "@/lib/format";

/** How many seen repositories an organization lists before "+N more". */
const SEEN_SHOWN = 5;

function Name({ r }: { r: Repository }) {
  const t = useT();
  const org = r.kind === "organization";
  const seen = r.repositories_seen ?? [];
  const hidden = seen.length - SEEN_SHOWN;
  const list = seen.slice(0, SEEN_SHOWN).join(", ") + (hidden > 0 ? ` ${t("repositories.more", { count: hidden, n: formatNumber(hidden) })}` : "");
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="flex flex-wrap items-center gap-2">
        <Link href={r.url} target="_blank" rel="noreferrer" className="font-medium">
          {org ? r.owner : `${r.owner}/${r.repo}`}
        </Link>
        {org && <Badge variant="neutral">{t("repositories.organization")}</Badge>}
      </span>
      {org && seen.length > 0 && <Truncate className="max-w-96 text-xs text-kumo-subtle" text={t("repositories.seen", { list })} />}
    </div>
  );
}

function Jobs24h({ r }: { r: Repository }) {
  const t = useT();
  if (r.jobs_24h === 0) return <span className="text-kumo-subtle">{t("repositories.noJobs")}</span>;
  return (
    <div className="flex flex-col">
      <span className="tabular-nums">{t("repositories.jobs", { count: r.jobs_24h, n: formatNumber(r.jobs_24h) })}</span>
      <span className="text-xs text-kumo-subtle">{t("repositories.successRate", { percent: formatPercent(r.succeeded_24h / r.jobs_24h) })}</span>
    </div>
  );
}

function LastJob({ r }: { r: Repository }) {
  const t = useT();
  const j = r.last_job;
  if (!j) return <span className="text-kumo-subtle">{t("repositories.never")}</span>;
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="flex min-w-0 items-center gap-2">
        <Link href={`/jobs/${encodeURIComponent(j.id)}`} className="max-w-56 truncate" title={j.display_name || j.id}>
          {j.display_name || j.id}
        </Link>
        <JobStatusBadge status={j.status} result={j.result} />
      </span>
      <span className="flex min-w-0 items-center gap-1 text-xs text-kumo-subtle">
        <RelativeTime value={isSet(j.finished_at) ? j.finished_at : j.updated_at} />
        {r.kind === "organization" && j.repository && <Truncate className="max-w-40" text={`· ${j.repository}`} />}
      </span>
    </div>
  );
}

/** The GitHub repositories and organizations the scale sets serve. */
export function RepositoriesPage() {
  const t = useT();
  const repos = useRepositories();
  const rows = repos.data ?? [];
  let body;
  if (repos.isLoading) body = <Loading />;
  else if (repos.error) body = <ErrorState error={repos.error} />;
  else if (rows.length === 0)
    body = (
      <Empty
        icon={<GitBranchIcon size={48} className="text-kumo-inactive" />}
        title={t("repositories.empty.title")}
        description={t("repositories.empty.description")}
        contents={<Link href="/scale-sets">{t("repositories.empty.link")}</Link>}
      />
    );
  else
    body = (
      <div className="overflow-x-auto">
        <Table layout="auto" className="min-w-[56rem]">
          <Table.Header>
            <Table.Row>
              <Table.Head>{t("repositories.columns.repository")}</Table.Head>
              <Table.Head>{t("repositories.columns.scaleSets")}</Table.Head>
              <Table.Head>{t("repositories.columns.credential")}</Table.Head>
              <Table.Head>{t("repositories.columns.running")}</Table.Head>
              <Table.Head>{t("repositories.columns.jobs24h")}</Table.Head>
              <Table.Head>{t("repositories.columns.lastJob")}</Table.Head>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {rows.map((r) => (
              <Table.Row key={r.url}>
                <Table.Cell className="max-w-96 align-top">
                  <Name r={r} />
                </Table.Cell>
                <Table.Cell className="align-top">
                  <span className="flex flex-col gap-0.5">
                    {(r.scale_sets ?? []).map((name) => (
                      <Link key={name} href="/scale-sets">
                        {name}
                      </Link>
                    ))}
                  </span>
                </Table.Cell>
                <Table.Cell className="align-top">{(r.credentials ?? []).join(", ") || "—"}</Table.Cell>
                <Table.Cell className={r.jobs_running > 0 ? "align-top tabular-nums" : "align-top tabular-nums text-kumo-subtle"}>
                  {formatNumber(r.jobs_running)}
                </Table.Cell>
                <Table.Cell className="align-top">
                  <Jobs24h r={r} />
                </Table.Cell>
                <Table.Cell className="align-top">
                  <LastJob r={r} />
                </Table.Cell>
              </Table.Row>
            ))}
          </Table.Body>
        </Table>
      </div>
    );
  return (
    <Page title={t("repositories.title")} description={t("repositories.description")}>
      <LayerCard>
        <LayerCard.Primary className="p-0">{body}</LayerCard.Primary>
      </LayerCard>
    </Page>
  );
}
