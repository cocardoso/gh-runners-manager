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
import type { LogStreamName } from "@/lib/use-log-stream";
import type { DetailSearch } from "@/router";
import { JobDuration } from "./jobs";

const STREAMS: LogStreamName[] = ["job", "runner", "agent", "control-plane", "runtime"];

function sentence(s: string) {
  const t = s.charAt(0).toUpperCase() + s.slice(1);
  return /[.!?]$/.test(t) ? t : `${t}.`;
}

function Steps({ details, loading, error, onOpen }: { details?: JobDetails; loading: boolean; error: unknown; onOpen: (name: string) => void }) {
  if (loading) return <Loading />;
  if (error) return <ErrorState error={error} />;
  if (!details?.available)
    return (
      <Banner
        variant="secondary"
        icon={<LockKeyIcon weight="fill" />}
        title="Steps are not available"
        description={sentence(details?.reason || "GitHub did not return this job")}
      />
    );
  const steps = details.steps ?? [];
  return (
    <LayerCard>
      <LayerCard.Secondary className="flex items-center justify-between">
        <span>Steps</span>
        {details.url && (
          <LinkButton href={details.url} external variant="ghost" size="sm" icon={ArrowSquareOutIcon}>
            View on GitHub
          </LinkButton>
        )}
      </LayerCard.Secondary>
      <LayerCard.Primary className="p-0">
        {steps.length === 0 ? (
          <p className="p-4 text-sm text-kumo-subtle">GitHub has not reported any step yet.</p>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <Table.Header>
                <Table.Row>
                  <Table.Head>#</Table.Head>
                  <Table.Head>Step</Table.Head>
                  <Table.Head>Result</Table.Head>
                  <Table.Head>Duration</Table.Head>
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
  const items: [string, React.ReactNode][] = [
    ["Status", <JobStatusBadge key="s" status={job.status} result={job.result} />],
    ["Repository", job.repository],
    ["Scale set", job.scale_set],
    ["Queued", <RelativeTime key="q" value={job.queued_at} />],
    ["Duration", <JobDuration key="d" job={job} now={now} />],
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

  if (job.isLoading) return <Page title="Job" crumbs={[{ label: "Jobs", href: "/jobs" }]}><Loading /></Page>;
  if (job.error instanceof ApiError && job.error.status === 404)
    return (
      <Page title="Job" crumbs={[{ label: "Jobs", href: "/jobs" }]}>
        <Empty icon={<QuestionIcon size={48} className="text-kumo-inactive" />} title="Job not found" description={`No job has the id ${id}.`} contents={<Link href="/jobs">Back to jobs</Link>} />
      </Page>
    );
  if (job.error || !job.data) return <Page title="Job" crumbs={[{ label: "Jobs", href: "/jobs" }]}><ErrorState error={job.error} /></Page>;

  const j = job.data;
  const env = environment.data;
  const live = !!env && env.state !== "destroyed";
  const tabs = [
    { value: "timeline", label: "Timeline" },
    { value: "steps", label: "Steps" },
    { value: "logs", label: "Logs" },
    { value: "resources", label: "Resources" },
    { value: "environment", label: "Environment" },
  ];

  return (
    <Page title={j.display_name || j.id} description={j.workflow_ref} crumbs={[{ label: "Jobs", href: "/jobs" }]}>
      <Summary job={j} now={now} />
      <DetailTabs tabs={tabs} value={tabs.some((t) => t.value === tab) ? tab : "timeline"} />
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
          <p className="text-sm text-kumo-subtle">This job has no environment yet, so there are no logs.</p>
        )
      ) : tab === "resources" ? (
        envId ? <ResourcesPanel envId={envId} live={live} memoryLimitMB={env?.memory_mb} /> : <p className="text-sm text-kumo-subtle">No environment yet.</p>
      ) : tab === "environment" ? (
        env ? <EnvironmentPanel environment={env} jobLink={false} /> : <p className="text-sm text-kumo-subtle">No environment was assigned to this job yet.</p>
      ) : (
        <LayerCard>
          <LayerCard.Secondary>Lifecycle</LayerCard.Secondary>
          <LayerCard.Primary>
            <Timeline stages={stages} />
          </LayerCard.Primary>
        </LayerCard>
      )}
    </Page>
  );
}
