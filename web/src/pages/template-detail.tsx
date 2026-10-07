import { Banner, Badge, Empty, LayerCard, Link, Table, cn } from "@cloudflare/kumo";
import { CheckCircleIcon, QuestionIcon, WarningIcon, XCircleIcon } from "@phosphor-icons/react";
import { useParams, useSearch } from "@tanstack/react-router";
import { ApiError } from "@/api/client";
import { useEnvironment, useSettings, useTemplate } from "@/api/queries";
import { useAdminAction } from "@/components/admin-action";
import { ErrorState, Loading, Page, RelativeTime } from "@/components/common";
import { DefinitionList } from "@/components/definition-list";
import { DetailTabs } from "@/components/detail-tabs";
import { LiveLog } from "@/components/live-log";
import { TemplateStateBadge } from "@/components/status-badge";
import { formatDuration } from "@/lib/format";
import type { DetailSearch } from "@/router";
import { formatBytes, TemplateActions, type TemplateView } from "./templates";

interface Check {
  name: string;
  ok: boolean;
  detail?: string;
  seconds: number;
}
interface Difference {
  kind: string;
  name: string;
  expected?: string;
  actual?: string;
  explained: boolean;
}
interface Fidelity {
  checks?: Check[];
  differences?: Difference[];
  unexpected?: number;
  note?: string;
}

const crumbs = [{ label: "Templates", href: "/templates" }];
const PIPELINE = ["building", "creating", "verifying", "ready", "active"];

function Pipeline({ state }: { state: string }) {
  const at = PIPELINE.indexOf(state);
  const failed = state === "failed";
  return (
    <ol className="flex flex-wrap items-center gap-2 text-sm" aria-label="Build progress">
      {PIPELINE.map((s, i) => (
        <li key={s} className="flex items-center gap-2">
          <span
            className={cn(
              "rounded-full border px-2.5 py-0.5",
              i < at || (state === "active" && i <= at) ? "border-kumo-line text-kumo-default" : "border-kumo-line text-kumo-inactive",
              i === at && !failed && "border-kumo-brand font-medium text-kumo-default",
            )}
          >
            {s}
          </span>
          {i < PIPELINE.length - 1 && <span className="text-kumo-inactive">→</span>}
        </li>
      ))}
      {failed && <Badge variant="error">failed</Badge>}
    </ol>
  );
}

function BuildTab({ t }: { t: TemplateView }) {
  const env = useEnvironment(t.build_environment_id || undefined);
  if (!t.build_environment_id) return <p className="text-sm text-kumo-subtle">The bootstrap template was not built by ghrm, so it has no build log.</p>;
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
  const env = useEnvironment(t.verify_environment_id || undefined);
  const checks = fid.checks ?? [];
  return (
    <div className="flex flex-col gap-4">
      <LayerCard>
        <LayerCard.Secondary>Self-test</LayerCard.Secondary>
        <LayerCard.Primary className="p-0">
          {checks.length === 0 ? (
            <p className="p-4 text-sm text-kumo-subtle">No self-test results yet.</p>
          ) : (
            <Table>
              <Table.Header>
                <Table.Row>
                  <Table.Head>Check</Table.Head>
                  <Table.Head>Result</Table.Head>
                  <Table.Head>Time</Table.Head>
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {checks.map((c) => (
                  <Table.Row key={c.name}>
                    <Table.Cell>{c.name}</Table.Cell>
                    <Table.Cell>
                      <span className={cn("flex items-center gap-1.5", c.ok ? "text-kumo-success" : "text-kumo-danger")}>
                        {c.ok ? <CheckCircleIcon weight="fill" /> : <XCircleIcon weight="fill" />}
                        {c.ok ? "passed" : c.detail || "failed"}
                      </span>
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
  if (!fid.differences && !fid.note)
    return (
      <Empty
        icon={<QuestionIcon size={48} className="text-kumo-inactive" />}
        title="Not verified yet"
        description="The fidelity report appears once a clone of this template has run the self-test and its software report was compared with GitHub's."
      />
    );
  const diffs = [...(fid.differences ?? [])].sort((a, b) => Number(a.explained) - Number(b.explained) || a.name.localeCompare(b.name));
  const unexpected = fid.unexpected ?? 0;
  return (
    <div className="flex flex-col gap-4">
      {fid.note ? (
        <Banner variant="alert" icon={<WarningIcon weight="fill" />} title="The software report could not be compared" description={fid.note} />
      ) : unexpected === 0 ? (
        <Banner
          variant="default"
          icon={<CheckCircleIcon weight="fill" />}
          title="Matches GitHub's software report"
          description="Every difference is something the ghrm layer adds on purpose."
        />
      ) : (
        <Banner
          variant="alert"
          icon={<WarningIcon weight="fill" />}
          title={`${unexpected} unexpected difference${unexpected === 1 ? "" : "s"}`}
          description="The template differs from the image GitHub runs in ways the ghrm layer does not explain. Review them before activating it."
        />
      )}
      <LayerCard>
        <LayerCard.Primary className="p-0">
          {diffs.length === 0 ? (
            <p className="p-4 text-sm text-kumo-subtle">No differences.</p>
          ) : (
            <div className="overflow-x-auto">
              <Table className="min-w-[48rem]">
                <Table.Header>
                  <Table.Row>
                    <Table.Head>Item</Table.Head>
                    <Table.Head>Difference</Table.Head>
                    <Table.Head>GitHub</Table.Head>
                    <Table.Head>This template</Table.Head>
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
                          {d.explained && <Badge variant="outline">ghrm layer</Badge>}
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
  const { id } = useParams({ strict: false }) as { id: string };
  const { tab = "build" } = useSearch({ strict: false }) as DetailSearch;
  const tpl = useTemplate(id);
  const settings = useSettings();
  const admin = useAdminAction();

  if (tpl.isLoading) return <Page title="Template" crumbs={crumbs}><Loading /></Page>;
  if (tpl.error instanceof ApiError && tpl.error.status === 404)
    return (
      <Page title="Template" crumbs={crumbs}>
        <Empty icon={<QuestionIcon size={48} className="text-kumo-inactive" />} title="Template not found" description={`No template version has the id ${id}.`} contents={<Link href="/templates">Back to templates</Link>} />
      </Page>
    );
  if (tpl.error || !tpl.data) return <Page title="Template" crumbs={crumbs}><ErrorState error={tpl.error} /></Page>;

  const t = tpl.data as TemplateView;
  const fid = (t.report ?? {}) as Fidelity;
  const tabs = [
    { value: "build", label: "Build" },
    { value: "verification", label: "Verification" },
    { value: "fidelity", label: "Fidelity" },
    { value: "details", label: "Details" },
  ];
  const canAct = settings.data?.admin_actions === true && (t.state === "ready" || t.state === "active");
  return (
    <Page
      title={t.id}
      description={t.bootstrap ? "The configured bootstrap template" : `ubuntu-slim ${t.slim_release} · runner ${t.runner_version} · layer ${t.layer_version}`}
      crumbs={crumbs}
      actions={
        <>
          <TemplateStateBadge state={t.state} />
          {canAct && <TemplateActions t={t} run={admin.run} />}
        </>
      }
    >
      {t.state === "failed" && (
        <Banner variant="error" icon={<XCircleIcon weight="fill" />} title={`Failed at ${t.failure_stage}`} description={t.failure_reason} />
      )}
      {!t.bootstrap && <Pipeline state={t.state} />}
      <DetailTabs tabs={tabs} value={tabs.some((x) => x.value === tab) ? tab : "build"} />
      {tab === "verification" ? (
        <VerificationTab t={t} fid={fid} />
      ) : tab === "fidelity" ? (
        <FidelityTab fid={fid} />
      ) : tab === "details" ? (
        <LayerCard>
          <LayerCard.Primary className="p-0">
            <DefinitionList
              items={[
                ["State", <TemplateStateBadge key="s" state={t.state} />],
                ["VMID", t.vmid ? String(t.vmid) : "—"],
                ["Trigger", t.trigger ?? "—"],
                ["Archive SHA-256", t.archive_sha256 ? <span className="font-mono text-xs break-all">{t.archive_sha256}</span> : "—"],
                ["Archive size", formatBytes(t.size_bytes)],
                ["Pinned", t.pinned ? "Yes" : "No"],
                ["In use by environments", t.in_use ? "Yes" : "No"],
                ["Created", <RelativeTime key="c" value={t.created_at} />],
                ["Activated", <RelativeTime key="a" value={t.activated_at} />],
              ]}
            />
          </LayerCard.Primary>
        </LayerCard>
      ) : (
        <BuildTab t={t} />
      )}
      {admin.dialog}
    </Page>
  );
}
