import { useMemo } from "react";
import { Empty, LayerCard, Link } from "@cloudflare/kumo";
import { QuestionIcon } from "@phosphor-icons/react";
import { useParams, useSearch } from "@tanstack/react-router";
import { ApiError } from "@/api/client";
import { useEnvironment, useJob, useJobEvents } from "@/api/queries";
import { ErrorState, Loading, Page, useNow } from "@/components/common";
import { DeleteRecord } from "@/components/delete-record";
import { DestroyEnvironment } from "@/components/destroy-environment";
import { DetailTabs } from "@/components/detail-tabs";
import { EnvironmentPanel } from "@/components/environment-panel";
import { LiveLog } from "@/components/live-log";
import { ResourcesPanel } from "@/components/resources-panel";
import { EnvironmentStateBadge } from "@/components/status-badge";
import { Timeline } from "@/components/timeline";
import { buildTimeline } from "@/lib/timeline";
import type { LogStreamName } from "@/lib/use-log-stream";
import type { DetailSearch } from "@/router";

const STREAMS: LogStreamName[] = ["control-plane", "runtime", "agent", "runner", "job"];
const crumbs = [{ label: "Environments", href: "/environments" }];

export function EnvironmentDetailPage() {
  const { id } = useParams({ strict: false }) as { id: string };
  const { tab = "timeline" } = useSearch({ strict: false }) as DetailSearch;
  const now = useNow();
  const environment = useEnvironment(id);
  const jobId = environment.data?.job_id || undefined;
  const job = useJob(jobId ?? "");
  const events = useJobEvents(undefined, id);
  const stages = useMemo(
    () => buildTimeline({ job: jobId ? job.data : undefined, environment: environment.data, events: events.data ?? [], now }),
    [jobId, job.data, environment.data, events.data, now],
  );

  if (environment.isLoading) return <Page title="Environment" crumbs={crumbs}><Loading /></Page>;
  if (environment.error instanceof ApiError && environment.error.status === 404)
    return (
      <Page title="Environment" crumbs={crumbs}>
        <Empty icon={<QuestionIcon size={48} className="text-kumo-inactive" />} title="Environment not found" description={`No environment has the id ${id}.`} contents={<Link href="/environments">Back to environments</Link>} />
      </Page>
    );
  if (environment.error || !environment.data) return <Page title="Environment" crumbs={crumbs}><ErrorState error={environment.error} /></Page>;

  const e = environment.data;
  const live = e.state !== "destroyed";
  const streams = STREAMS.filter((s) => s !== "job" || e.job_id);
  const tabs = [
    { value: "timeline", label: "Timeline" },
    { value: "logs", label: "Logs" },
    { value: "resources", label: "Resources" },
    { value: "environment", label: "Details" },
  ];
  return (
    <Page
      title={e.id}
      description={`${e.scale_set}${jobId && job.data ? ` · ${job.data.display_name}` : ""}`}
      crumbs={crumbs}
      actions={
        <>
          <EnvironmentStateBadge state={e.state} />
          {e.state === "destroyed" ? (
            <DeleteRecord kind="environment" id={e.id} />
          ) : (
            <DestroyEnvironment id={e.id} disabled={e.state === "destroying"} />
          )}
        </>
      }
    >
      <DetailTabs tabs={tabs} value={tabs.some((t) => t.value === tab) ? tab : "timeline"} />
      {tab === "logs" ? (
        <LiveLog envId={e.id} streams={streams} defaultStream={e.job_id ? "job" : "control-plane"} live={live} />
      ) : tab === "resources" ? (
        <ResourcesPanel envId={e.id} live={live} memoryLimitMB={e.memory_mb} />
      ) : tab === "environment" ? (
        <EnvironmentPanel environment={e} />
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
