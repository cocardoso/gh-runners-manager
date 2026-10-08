import { useMemo } from "react";
import { Empty, LayerCard, Link } from "@cloudflare/kumo";
import { QuestionIcon } from "@phosphor-icons/react";
import { useParams, useSearch } from "@tanstack/react-router";
import { ApiError } from "@/api/client";
import { useEnvironment, useJob, useJobEvents, useSettings } from "@/api/queries";
import { useT } from "@/i18n";
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

export function EnvironmentDetailPage() {
  const t = useT();
  const { id } = useParams({ strict: false }) as { id: string };
  const { tab = "timeline" } = useSearch({ strict: false }) as DetailSearch;
  const now = useNow();
  const environment = useEnvironment(id);
  // A destroyed environment belongs to the history.
  const crumbs = [{ label: t("environments.list.title"), href: environment.data?.state === "destroyed" ? "/environments?tab=history" : "/environments" }];
  const settings = useSettings();
  const jobId = environment.data?.job_id || undefined;
  const job = useJob(jobId ?? "");
  const events = useJobEvents(undefined, id);
  const stages = useMemo(
    () => buildTimeline({ job: jobId ? job.data : undefined, environment: environment.data, events: events.data ?? [], now }),
    [jobId, job.data, environment.data, events.data, now],
  );

  if (environment.isLoading) return <Page title={t("environments.detail.title")} crumbs={crumbs}><Loading /></Page>;
  if (environment.error instanceof ApiError && environment.error.status === 404)
    return (
      <Page title={t("environments.detail.title")} crumbs={crumbs}>
        <Empty icon={<QuestionIcon size={48} className="text-kumo-inactive" />} title={t("environments.detail.notFoundTitle")} description={t("environments.detail.notFoundDescription", { id })} contents={<Link href="/environments">{t("environments.detail.back")}</Link>} />
      </Page>
    );
  if (environment.error || !environment.data) return <Page title={t("environments.detail.title")} crumbs={crumbs}><ErrorState error={environment.error} /></Page>;

  const e = environment.data;
  const live = e.state !== "destroyed";
  const streams = STREAMS.filter((s) => s !== "job" || e.job_id);
  const tabs = [
    { value: "timeline", label: t("environments.detail.tabs.timeline") },
    { value: "logs", label: t("environments.detail.tabs.logs") },
    { value: "resources", label: t("environments.detail.tabs.resources") },
    { value: "environment", label: t("environments.detail.tabs.details") },
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
            settings.data?.admin_actions === true && <DeleteRecord kind="environment" id={e.id} />
          ) : (
            <DestroyEnvironment id={e.id} disabled={e.state === "destroying"} />
          )}
        </>
      }
    >
      <DetailTabs tabs={tabs} value={tabs.some((x) => x.value === tab) ? tab : "timeline"}>
        {tab === "logs" ? (
          <LiveLog envId={e.id} streams={streams} defaultStream={e.job_id ? "job" : "control-plane"} live={live} />
        ) : tab === "resources" ? (
          <ResourcesPanel envId={e.id} live={live} memoryLimitMB={e.memory_mb} />
        ) : tab === "environment" ? (
          <EnvironmentPanel environment={e} />
        ) : (
          <LayerCard>
            <LayerCard.Secondary>{t("environments.detail.lifecycle")}</LayerCard.Secondary>
            <LayerCard.Primary>
              <Timeline stages={stages} />
            </LayerCard.Primary>
          </LayerCard>
        )}
      </DetailTabs>
    </Page>
  );
}
