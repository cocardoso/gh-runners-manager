import { Banner, Badge, Empty, LayerCard, Link, Table, cn } from "@cloudflare/kumo";
import { CheckCircleIcon, QuestionIcon, WarningIcon, XCircleIcon } from "@phosphor-icons/react";
import { useParams, useSearch } from "@tanstack/react-router";
import { ApiError } from "@/api/client";
import { useEnvironment, useSettings, useTemplate } from "@/api/queries";
import { useAdminAction } from "@/components/admin-action";
import { DeleteRecord } from "@/components/delete-record";
import { ErrorState, Loading, Page, RelativeTime } from "@/components/common";
import { DefinitionList } from "@/components/definition-list";
import { DetailTabs } from "@/components/detail-tabs";
import { LiveLog } from "@/components/live-log";
import { TemplateStateBadge } from "@/components/status-badge";
import { tr, useT, type Key } from "@/i18n";
import { formatDuration } from "@/lib/format";
import type { DetailSearch } from "@/router";
import { formatBytes, TemplateActions, type TemplateView } from "./templates";

interface Check {
  name: string;
  ok: boolean;
  detail?: string;
  warning?: boolean;
  seconds: number;
}
interface Difference {
  kind: string;
  name: string;
  expected?: string;
  actual?: string;
  explained: boolean;
  reason?: string;
}
interface Fidelity {
  checks?: Check[];
  differences?: Difference[];
  unexpected?: number;
  note?: string;
}

// A template that no longer exists belongs to the build history.
const crumbs = (state?: string) => [
  { label: tr("templates.title"), href: state && ["failed", "deleted", "retired"].includes(state) ? "/templates?tab=history" : "/templates" },
];
const PIPELINE = ["building", "creating", "verifying", "ready", "active"] as const;

function Pipeline({ state }: { state: string }) {
  const t = useT();
  const at = (PIPELINE as readonly string[]).indexOf(state);
  const failed = state === "failed";
  return (
    <ol className="flex flex-wrap items-center gap-2 text-sm" aria-label={t("templates.detail.pipeline.label")}>
      {PIPELINE.map((s, i) => (
        <li key={s} className="flex items-center gap-2">
          <span
            className={cn(
              "rounded-full border px-2.5 py-0.5",
              i < at || (state === "active" && i <= at) ? "border-kumo-line text-kumo-default" : "border-kumo-line text-kumo-inactive",
              i === at && !failed && "border-kumo-brand font-medium text-kumo-default",
            )}
          >
            {t(`templates.detail.pipeline.${s}` satisfies Key)}
          </span>
          {i < PIPELINE.length - 1 && <span className="text-kumo-inactive">→</span>}
        </li>
      ))}
      {failed && <Badge variant="error">{t("templates.detail.pipeline.failed")}</Badge>}
    </ol>
  );
}

function BuildTab({ t }: { t: TemplateView }) {
  const tl = useT();
  const env = useEnvironment(t.build_environment_id || undefined);
  if (!t.build_environment_id) return <p className="text-sm text-kumo-subtle">{tl("templates.detail.noBuildLog")}</p>;
  return (
    <LiveLog
      key={t.build_environment_id}
      envId={t.build_environment_id}
      streams={["build", "agent", "control-plane", "runtime"]}
      defaultStream="build"
      live={!!env.data && env.data.state !== "destroyed"}
    />
  );
}

function VerificationTab({ t, fid }: { t: TemplateView; fid: Fidelity }) {
  const tl = useT();
  const env = useEnvironment(t.verify_environment_id || undefined);
  const checks = fid.checks ?? [];
  return (
    <div className="flex flex-col gap-4">
      <LayerCard>
        <LayerCard.Secondary>{tl("templates.detail.selfTest.title")}</LayerCard.Secondary>
        <LayerCard.Primary className="p-0">
          {checks.length === 0 ? (
            <p className="p-4 text-sm text-kumo-subtle">{tl("templates.detail.selfTest.empty")}</p>
          ) : (
            <Table>
              <Table.Header>
                <Table.Row>
                  <Table.Head>{tl("templates.detail.selfTest.check")}</Table.Head>
                  <Table.Head>{tl("templates.detail.selfTest.result")}</Table.Head>
                  <Table.Head>{tl("templates.detail.selfTest.time")}</Table.Head>
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {checks.map((c) => (
                  <Table.Row key={c.name}>
                    <Table.Cell>{c.name}</Table.Cell>
                    <Table.Cell>
                      {c.ok && c.warning ? (
                        <span className="flex items-center gap-1.5 text-kumo-warning">
                          <WarningIcon weight="fill" />
                          {tl("templates.detail.selfTest.warning", { detail: c.detail ?? "" })}
                        </span>
                      ) : (
                        <span className={cn("flex items-center gap-1.5", c.ok ? "text-kumo-success" : "text-kumo-danger")}>
                          {c.ok ? <CheckCircleIcon weight="fill" /> : <XCircleIcon weight="fill" />}
                          {c.ok ? tl("templates.detail.selfTest.passed") : c.detail || tl("templates.detail.selfTest.failed")}
                        </span>
                      )}
                    </Table.Cell>
                    <Table.Cell className="tabular-nums">{formatDuration(c.seconds * 1000)}</Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table>
          )}
        </LayerCard.Primary>
      </LayerCard>
      {t.verify_environment_id && (
        <LiveLog
          key={t.verify_environment_id}
          envId={t.verify_environment_id}
          streams={["selftest", "agent", "control-plane"]}
          defaultStream="selftest"
          live={!!env.data && env.data.state !== "destroyed"}
        />
      )}
    </div>
  );
}

function FidelityTab({ fid }: { fid: Fidelity }) {
  const t = useT();
  if (!fid.differences && !fid.note)
    return (
      <Empty
        icon={<QuestionIcon size={48} className="text-kumo-inactive" />}
        title={t("templates.detail.fidelity.notVerified")}
        description={t("templates.detail.fidelity.notVerifiedHelp")}
      />
    );
  const diffs = [...(fid.differences ?? [])].sort((a, b) => Number(a.explained) - Number(b.explained) || a.name.localeCompare(b.name));
  const unexpected = fid.unexpected ?? 0;
  return (
    <div className="flex flex-col gap-4">
      {fid.note ? (
        <Banner variant="alert" icon={<WarningIcon weight="fill" />} title={t("templates.detail.fidelity.notCompared")} description={fid.note} />
      ) : unexpected === 0 ? (
        <Banner
          variant="default"
          icon={<CheckCircleIcon weight="fill" />}
          title={t("templates.detail.fidelity.matches")}
          description={t("templates.detail.fidelity.matchesHelp")}
        />
      ) : (
        <Banner
          variant="alert"
          icon={<WarningIcon weight="fill" />}
          title={t("templates.detail.fidelity.unexpected", { count: unexpected })}
          description={t("templates.detail.fidelity.unexpectedHelp")}
        />
      )}
      <LayerCard>
        <LayerCard.Primary className="p-0">
          {diffs.length === 0 ? (
            <p className="p-4 text-sm text-kumo-subtle">{t("templates.detail.fidelity.none")}</p>
          ) : (
            <div className="overflow-x-auto">
              <Table className="min-w-[48rem]">
                <Table.Header>
                  <Table.Row>
                    <Table.Head>{t("templates.detail.fidelity.item")}</Table.Head>
                    <Table.Head>{t("templates.detail.fidelity.difference")}</Table.Head>
                    <Table.Head>{t("templates.detail.fidelity.github")}</Table.Head>
                    <Table.Head>{t("templates.detail.fidelity.thisTemplate")}</Table.Head>
                  </Table.Row>
                </Table.Header>
                <Table.Body>
                  {diffs.map((d) => (
                    <Table.Row key={d.kind + d.name}>
                      <Table.Cell className="max-w-md">
                        <span className="block break-words">{d.name}</span>
                      </Table.Cell>
                      <Table.Cell>
                        <span className="flex flex-wrap gap-1">
                          <Badge variant={d.explained ? "neutral" : "warning"} appearance="dot">
                            {d.kind}
                          </Badge>
                          {d.explained && <Badge variant="outline">{d.reason || t("templates.detail.fidelity.layer")}</Badge>}
                        </span>
                      </Table.Cell>
                      <Table.Cell className="font-mono text-sm">{d.expected || "—"}</Table.Cell>
                      <Table.Cell className="font-mono text-sm">{d.actual || "—"}</Table.Cell>
                    </Table.Row>
                  ))}
                </Table.Body>
              </Table>
            </div>
          )}
        </LayerCard.Primary>
      </LayerCard>
    </div>
  );
}

export function TemplateDetailPage() {
  const tl = useT();
  const { id } = useParams({ strict: false }) as { id: string };
  const search = useSearch({ strict: false }) as DetailSearch;
  const tpl = useTemplate(id);
  const settings = useSettings();
  const admin = useAdminAction();

  // A build in progress or a failed one opens on its log; a version that exists opens on its details.
  const defaultTab = !tpl.data || ["building", "creating", "verifying", "failed"].includes(tpl.data.state) ? "build" : "details";
  const tab = search.tab ?? defaultTab;
  if (tpl.isLoading) return <Page title={tl("templates.detail.title")} crumbs={crumbs()}><Loading /></Page>;
  if (tpl.error instanceof ApiError && tpl.error.status === 404)
    return (
      <Page title={tl("templates.detail.title")} crumbs={crumbs()}>
        <Empty icon={<QuestionIcon size={48} className="text-kumo-inactive" />} title={tl("templates.detail.notFound.title")} description={tl("templates.detail.notFound.description", { id })} contents={<Link href="/templates">{tl("templates.detail.notFound.back")}</Link>} />
      </Page>
    );
  if (tpl.error || !tpl.data) return <Page title={tl("templates.detail.title")} crumbs={crumbs()}><ErrorState error={tpl.error} /></Page>;

  const t = tpl.data as TemplateView;
  const fid = (t.report ?? {}) as Fidelity;
  const tabs = [
    { value: "build", label: tl("templates.detail.tabs.build") },
    { value: "verification", label: tl("templates.detail.tabs.verification") },
    { value: "fidelity", label: tl("templates.detail.tabs.fidelity") },
    { value: "details", label: tl("templates.detail.tabs.details") },
  ];
  const canAct = settings.data?.admin_actions === true && (t.state === "ready" || t.state === "active");
  return (
    <Page
      title={t.id}
      description={
        t.bootstrap
          ? tl("templates.detail.bootstrapDescription")
          : `ubuntu-slim ${t.slim_release} · runner ${t.runner_version} · layer ${t.layer_version}`
      }
      crumbs={crumbs(t.state)}
      actions={
        <>
          <TemplateStateBadge state={t.state} />
          {canAct && <TemplateActions t={t} run={admin.run} />}
          {settings.data?.admin_actions === true && (t.state === "failed" || t.state === "deleted") && <DeleteRecord kind="template" id={t.id} />}
        </>
      }
    >
      {t.state === "failed" && (
        <Banner variant="error" icon={<XCircleIcon weight="fill" />} title={tl("templates.detail.failedAt", { stage: t.failure_stage ?? "" })} description={t.failure_reason} />
      )}
      {!t.bootstrap && <Pipeline state={t.state} />}
      <DetailTabs tabs={tabs} value={tabs.some((x) => x.value === tab) ? tab : defaultTab} defaultValue={defaultTab}>
        {tab === "verification" ? (
          <VerificationTab t={t} fid={fid} />
        ) : tab === "fidelity" ? (
          <FidelityTab fid={fid} />
        ) : tab === "details" ? (
          <LayerCard>
            <LayerCard.Primary className="p-0">
              <DefinitionList
                items={[
                  [tl("templates.detail.fields.state"), <TemplateStateBadge key="s" state={t.state} />],
                  [tl("templates.detail.fields.vmid"), t.vmid ? String(t.vmid) : "—"],
                  [tl("templates.detail.fields.trigger"), t.trigger ?? "—"],
                  [tl("templates.detail.fields.sha"), t.archive_sha256 ? <span className="font-mono text-xs break-all">{t.archive_sha256}</span> : "—"],
                  [tl("templates.detail.fields.size"), formatBytes(t.size_bytes)],
                  [tl("templates.detail.fields.pinned"), t.pinned ? tl("templates.detail.fields.yes") : tl("templates.detail.fields.no")],
                  [tl("templates.detail.fields.inUse"), t.in_use ? tl("templates.detail.fields.yes") : tl("templates.detail.fields.no")],
                  [tl("templates.detail.fields.created"), <RelativeTime key="c" value={t.created_at} />],
                  [tl("templates.detail.fields.activated"), <RelativeTime key="a" value={t.activated_at} />],
                ]}
              />
            </LayerCard.Primary>
          </LayerCard>
        ) : (
          <BuildTab t={t} />
        )}
      </DetailTabs>
    </Page>
  );
}
