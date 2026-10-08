import { useMemo, useState } from "react";
import { Banner, Empty, LayerCard, Link, LinkButton, Table } from "@cloudflare/kumo";
import { ArrowSquareOutIcon, LockKeyIcon, QuestionIcon } from "@phosphor-icons/react";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import type { ApiEvent, Job, JobDetails } from "@/api/client";
import { ApiError } from "@/api/client";
import { useEnvironment, useJob, useJobEvents, useJobGitHub } from "@/api/queries";
import { ErrorState, Loading, Page, RelativeTime, Truncate, useNow } from "@/components/common";
import { DetailTabs } from "@/components/detail-tabs";
import { EnvironmentPanel } from "@/components/environment-panel";
import { LiveLog } from "@/components/live-log";
import { ResourcesPanel } from "@/components/resources-panel";
import { JobStatusBadge, toneFor } from "@/components/status-badge";
import { Timeline } from "@/components/timeline";
import { buildTimeline } from "@/lib/timeline";
import { durationBetween, formatDuration } from "@/lib/format";
import { useT } from "@/i18n";
import type { LogStreamName } from "@/lib/use-log-stream";
import type { DetailSearch } from "@/router";
import { JobDuration } from "./jobs";

const STREAMS: LogStreamName[] = ["job", "runner", "agent", "control-plane", "runtime"];

function sentence(s: string) {
  const text = s.charAt(0).toUpperCase() + s.slice(1);
  return /[.!?]$/.test(text) ? text : `${text}.`;
}

function Steps({ details, loading, error, onOpen }: { details?: JobDetails; loading: boolean; error: unknown; onOpen: (name: string) => void }) {
  const t = useT();
  if (loading) return <Loading />;
  if (error) return <ErrorState error={error} />;
  if (!details?.available)
    return (
      <Banner
        variant="secondary"
        icon={<LockKeyIcon weight="fill" />}
        title={t("overview.job.steps.unavailable")}
        description={sentence(details?.reason || t("overview.job.steps.notReturned"))}
      />
    );
  const steps = details.steps ?? [];
  return (
    <LayerCard>
      <LayerCard.Secondary className="flex items-center justify-between">
        <span>{t("overview.job.steps.title")}</span>
        {details.url && (
          <LinkButton href={details.url} external variant="ghost" size="sm" icon={ArrowSquareOutIcon}>
            {t("overview.job.steps.viewOnGitHub")}
          </LinkButton>
        )}
      </LayerCard.Secondary>
      <LayerCard.Primary className="p-0">
        {steps.length === 0 ? (
          <p className="p-4 text-sm text-kumo-subtle">{t("overview.job.steps.none")}</p>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <Table.Header>
                <Table.Row>
                  <Table.Head>#</Table.Head>
                  <Table.Head>{t("overview.job.steps.step")}</Table.Head>
                  <Table.Head>{t("overview.job.steps.result")}</Table.Head>
                  <Table.Head>{t("overview.job.steps.duration")}</Table.Head>
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {steps.map((s) => {
                  const result = s.conclusion || s.status;
                  const tone = toneFor("result", s.conclusion === "success" ? "succeeded" : s.conclusion === "failure" ? "failed" : s.conclusion);
                  return (
                    <Table.Row key={s.number}>
                      <Table.Cell className="tabular-nums text-kumo-subtle">{s.number}</Table.Cell>
                      <Table.Cell>
                        <button type="button" className="text-left text-kumo-link hover:underline" onClick={() => onOpen(s.name)}>
                          {s.name}
                        </button>
                      </Table.Cell>
                      <Table.Cell className={tone === "error" ? "text-kumo-danger" : tone === "success" ? "text-kumo-success" : "text-kumo-subtle"}>{result}</Table.Cell>
                      <Table.Cell className="tabular-nums">{formatDuration(durationBetween(s.started_at, s.completed_at))}</Table.Cell>
                    </Table.Row>
                  );
                })}
              </Table.Body>
            </Table>
          </div>
        )}
      </LayerCard.Primary>
    </LayerCard>
  );
}

function Summary({ job, now }: { job: Job; now: Date }) {
  const t = useT();
  const items: [string, React.ReactNode][] = [
    [t("overview.jobs.columns.status"), <JobStatusBadge key="s" status={job.status} result={job.result} />],
    [t("overview.jobs.columns.repository"), job.repository],
    [t("overview.jobs.columns.scaleSet"), job.scale_set],
    [t("overview.jobs.columns.queued"), <RelativeTime key="q" value={job.queued_at} />],
    [t("overview.jobs.columns.duration"), <JobDuration key="d" job={job} now={now} />],
  ];
  return (
    <dl className="flex flex-wrap gap-x-8 gap-y-2">
      {items.map(([k, v]) => (
        <div key={k} className="flex min-w-0 flex-col">
          <dt className="text-xs text-kumo-subtle">{k}</dt>
          <dd className="min-w-0 max-w-[70vw] text-sm">{typeof v === "string" ? <Truncate text={v} /> : v}</dd>
        </div>
      ))}
    </dl>
  );
}

function mergeEvents(a: ApiEvent[] = [], b: ApiEvent[] = []) {
  const seen = new Map<number, ApiEvent>();
  for (const e of [...a, ...b]) seen.set(e.seq, e);
  return [...seen.values()];
}

export function JobDetailPage() {
  const { id } = useParams({ strict: false }) as { id: string };
  const { tab = "timeline" } = useSearch({ strict: false }) as DetailSearch;
  const navigate = useNavigate();
  const t = useT();
  const now = useNow();
  const job = useJob(id);
  const envId = job.data?.environment_id || undefined;
  const environment = useEnvironment(envId);
  const jobEvents = useJobEvents(id, undefined);
  const envEvents = useJobEvents(undefined, envId);
  const github = useJobGitHub(id, tab === "steps");
  const [logSearch, setLogSearch] = useState("");

  const stages = useMemo(
    () => buildTimeline({ job: job.data, environment: environment.data, events: mergeEvents(jobEvents.data, envEvents.data), now }),
    [job.data, environment.data, jobEvents.data, envEvents.data, now],
  );

  const crumbs = [{ label: t("overview.jobs.title"), href: "/jobs" }];
  if (job.isLoading) return <Page title={t("overview.job.title")} crumbs={crumbs}><Loading /></Page>;
  if (job.error instanceof ApiError && job.error.status === 404)
    return (
      <Page title={t("overview.job.title")} crumbs={crumbs}>
        <Empty
          icon={<QuestionIcon size={48} className="text-kumo-inactive" />}
          title={t("overview.job.notFound.title")}
          description={t("overview.job.notFound.description", { id })}
          contents={<Link href="/jobs">{t("overview.job.notFound.back")}</Link>}
        />
      </Page>
    );
  if (job.error || !job.data) return <Page title={t("overview.job.title")} crumbs={crumbs}><ErrorState error={job.error} /></Page>;

  const j = job.data;
  const env = environment.data;
  const live = !!env && env.state !== "destroyed";
  const tabs = [
    { value: "timeline", label: t("overview.job.tabs.timeline") },
    { value: "steps", label: t("overview.job.tabs.steps") },
    { value: "logs", label: t("overview.job.tabs.logs") },
    { value: "resources", label: t("overview.job.tabs.resources") },
    { value: "environment", label: t("overview.job.tabs.environment") },
  ];

  return (
    <Page title={j.display_name || j.id} description={j.workflow_ref} crumbs={crumbs}>
      <Summary job={j} now={now} />
      <DetailTabs tabs={tabs} value={tabs.some((x) => x.value === tab) ? tab : "timeline"} />
      {tab === "steps" ? (
        <Steps
          details={github.data}
          loading={github.isLoading}
          error={github.error}
          onOpen={(name) => {
            setLogSearch(name);
            void navigate({ to: ".", search: { tab: "logs" }, replace: true });
          }}
        />
      ) : tab === "logs" ? (
        envId ? (
          <LiveLog key={logSearch} envId={envId} streams={STREAMS} defaultStream="job" live={live} initialSearch={logSearch} />
        ) : (
          <p className="text-sm text-kumo-subtle">{t("overview.job.noLogs")}</p>
        )
      ) : tab === "resources" ? (
        envId ? <ResourcesPanel envId={envId} live={live} memoryLimitMB={env?.memory_mb} /> : <p className="text-sm text-kumo-subtle">{t("overview.job.noEnvironment")}</p>
      ) : tab === "environment" ? (
        env ? <EnvironmentPanel environment={env} jobLink={false} /> : <p className="text-sm text-kumo-subtle">{t("overview.job.noEnvironmentAssigned")}</p>
      ) : (
        <LayerCard>
          <LayerCard.Secondary>{t("overview.job.lifecycle")}</LayerCard.Secondary>
          <LayerCard.Primary>
            <Timeline stages={stages} />
          </LayerCard.Primary>
        </LayerCard>
      )}
    </Page>
  );
}
