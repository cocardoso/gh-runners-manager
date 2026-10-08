import { useId, type ReactNode } from "react";
import { Badge, Banner, Button, DropdownMenu, Empty, LayerCard, Link, Select, Table, Tooltip } from "@cloudflare/kumo";
import {
  ArrowCounterClockwiseIcon,
  CheckCircleIcon,
  ClockCounterClockwiseIcon,
  DotsThreeIcon,
  HammerIcon,
  InfoIcon,
  PackageIcon,
  PushPinIcon,
  PushPinSlashIcon,
  QuestionIcon,
  WarningIcon,
} from "@phosphor-icons/react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type TemplateVersion } from "@/api/client";
import { useSettings, useTemplateProfiles, useTemplates } from "@/api/queries";
import { useAdminAction } from "@/components/admin-action";
import { ErrorState, Loading, Page, RelativeTime, useNow } from "@/components/common";
import { DeleteRecord } from "@/components/delete-record";
import { DetailTabs } from "@/components/detail-tabs";
import { TemplateStateBadge } from "@/components/status-badge";
import { ProfilesTab } from "@/components/template-profiles";
import { currentFormatLocale, tr, useT, type Key } from "@/i18n";
import { formatRelative } from "@/lib/format";
import type { ListSearch } from "@/router";

export type TemplateView = TemplateVersion;

export function formatBytes(n: number): string {
  if (!n) return "—";
  const num = (v: number, digits = 0) => new Intl.NumberFormat(currentFormatLocale(), { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(v);
  if (n < 1024 ** 2) return tr("templates.units.kb", { n: num(Math.max(1, Math.round(n / 1024))) });
  const gb = n / 1024 ** 3;
  return gb >= 1 ? tr("templates.units.gb", { n: num(gb, 2) }) : tr("templates.units.mb", { n: num(Math.round(n / 1024 ** 2)) });
}

const triggerKey: Record<string, Key> = {
  manual: "templates.trigger.manual",
  "slim-release": "templates.trigger.slimRelease",
  "runner-release": "templates.trigger.runnerRelease",
  layer: "templates.trigger.layer",
  "bootstrap-replacement": "templates.trigger.bootstrapReplacement",
  "new-profile": "templates.trigger.newProfile",
  bootstrap: "templates.trigger.bootstrap",
};

function triggerLabel(trigger?: string): string {
  const key = triggerKey[trigger ?? ""];
  return key ? tr(key) : (trigger ?? "");
}

function Flags({ t }: { t: TemplateView }) {
  const tl = useT();
  return (
    <span className="flex flex-wrap gap-1">
      {t.pinned && <Badge variant="outline">{tl("templates.flags.pinned")}</Badge>}
      {t.in_use && <Badge variant="outline">{tl("templates.flags.inUse")}</Badge>}
    </span>
  );
}

export function TemplateActions({ t, run }: { t: TemplateView; run: ReturnType<typeof useAdminAction>["run"] }) {
  const tl = useT();
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: ["templates"] });
  const activate = () =>
    run(t.state === "ready" && t.activated_at && !t.activated_at.startsWith("0001") ? tl("templates.actions.rolledBack", { id: t.id }) : tl("templates.actions.activated", { id: t.id }), async () => {
      unwrap(await api.POST("/api/v1/templates/{id}/activate", { params: { path: { id: t.id } } }));
      await refresh();
    });
  const pin = (pinned: boolean) =>
    run(pinned ? tl("templates.actions.pinned", { id: t.id }) : tl("templates.actions.unpinned", { id: t.id }), async () => {
      const path = pinned ? "/api/v1/templates/{id}/pin" : "/api/v1/templates/{id}/unpin";
      unwrap(await api.POST(path, { params: { path: { id: t.id } } }));
      await refresh();
    });
  const wasActive = !!t.activated_at && !t.activated_at.startsWith("0001");
  return (
    <DropdownMenu>
      <DropdownMenu.Trigger render={<Button variant="ghost" size="sm" shape="square" icon={DotsThreeIcon} aria-label={tl("templates.actions.menu", { id: t.id })} />} />
      <DropdownMenu.Content>
        <DropdownMenu.Item icon={wasActive ? ArrowCounterClockwiseIcon : CheckCircleIcon} disabled={t.active} onClick={activate}>
          {wasActive ? tl("templates.actions.rollBack") : tl("templates.actions.activate")}
        </DropdownMenu.Item>
        {t.pinned ? (
          <DropdownMenu.Item icon={PushPinSlashIcon} onClick={() => pin(false)}>
            {tl("templates.actions.unpin")}
          </DropdownMenu.Item>
        ) : (
          <DropdownMenu.Item icon={PushPinIcon} onClick={() => pin(true)}>
            {tl("templates.actions.pin")}
          </DropdownMenu.Item>
        )}
      </DropdownMenu.Content>
    </DropdownMenu>
  );
}

const SLIM = "ubuntu-slim";
const IN_PROGRESS = ["building", "creating", "verifying"];
const HISTORY = ["failed", "deleted", "retired"];

const detailHref = (id: string, tab?: string) => `/templates/${encodeURIComponent(id)}${tab ? `?tab=${tab}` : ""}`;

/** The template's ubuntu-slim, runner and layer versions on one line. */
function versionsLine(t: TemplateView): string {
  if (t.bootstrap) return tr("templates.bootstrapLine", { vmid: t.vmid });
  return [t.slim_release && `${SLIM} ${t.slim_release}`, t.runner_version && `runner ${t.runner_version}`, t.layer_version && `layer ${t.layer_version}`]
    .filter(Boolean)
    .join(" · ");
}

/** A titled card, exposed as a named region. */
function Section({ title, icon, children, flush }: { title: string; icon?: ReactNode; children: ReactNode; flush?: boolean }) {
  const id = useId();
  return (
    <section aria-labelledby={id}>
      <LayerCard>
        <LayerCard.Secondary>
          <h2 id={id} className="flex items-center gap-2 text-sm font-medium">
            {icon}
            {title}
          </h2>
        </LayerCard.Secondary>
        <LayerCard.Primary className={flush ? "p-0" : undefined}>{children}</LayerCard.Primary>
      </LayerCard>
    </section>
  );
}

interface Fidelity {
  differences?: unknown[];
  unexpected?: number;
  note?: string;
}

/** Whether the template matches GitHub's software report, from its fidelity report. */
function FidelitySummary({ t }: { t: TemplateView }) {
  const tl = useT();
  const fid = (t.report ?? {}) as Fidelity;
  if (fid.note)
    return (
      <span className="flex items-center gap-1.5 text-kumo-warning">
        <WarningIcon weight="fill" />
        {tl("templates.detail.fidelity.notCompared")}
      </span>
    );
  if (!fid.differences)
    return (
      <span className="flex items-center gap-1.5 text-kumo-subtle">
        <QuestionIcon />
        {tl("templates.detail.fidelity.notVerified")}
      </span>
    );
  const unexpected = fid.unexpected ?? 0;
  return unexpected === 0 ? (
    <span className="flex items-center gap-1.5 text-kumo-success">
      <CheckCircleIcon weight="fill" />
      {tl("templates.detail.fidelity.matches")}
    </span>
  ) : (
    <span className="flex items-center gap-1.5 text-kumo-warning">
      <WarningIcon weight="fill" />
      {tl("templates.detail.fidelity.unexpected", { count: unexpected })}
    </span>
  );
}

function BuildCard({ t }: { t: TemplateView }) {
  const tl = useT();
  const now = useNow();
  return (
    <Section title={tl("templates.building.title")} icon={<HammerIcon weight="fill" className="text-kumo-warning" />}>
      <div className="flex flex-wrap items-center justify-between gap-3 text-sm">
        <div className="flex min-w-0 flex-col gap-1">
          <span className="font-mono">{t.id}</span>
          <span className="text-kumo-subtle">
            {tl("templates.building.stage", { stage: tl(`templates.detail.pipeline.${t.state as "building" | "creating" | "verifying"}` satisfies Key) })}
            {" · "}
            {tl("templates.building.started", { time: formatRelative(t.created_at, undefined, now) })}
          </span>
        </div>
        <Link href={detailHref(t.id)}>{tl("templates.building.open")}</Link>
      </div>
    </Section>
  );
}

/** A build the control plane started but the list does not show yet. */
function StartingCard() {
  const tl = useT();
  return (
    <Section title={tl("templates.building.title")} icon={<HammerIcon weight="fill" className="text-kumo-warning" />}>
      <span className="text-sm text-kumo-subtle">{tl("templates.building.starting")}</span>
    </Section>
  );
}

function InUseCard({ t, actions }: { t: TemplateView | undefined; actions?: ReactNode }) {
  const tl = useT();
  if (!t)
    return (
      <Section title={tl("templates.inUse.title")}>
        <Empty icon={<PackageIcon size={48} className="text-kumo-inactive" />} title={tl("templates.inUse.none.title")} description={tl("templates.inUse.none.description")} />
      </Section>
    );
  const fields: [string, ReactNode][] = t.bootstrap
    ? [[tl("templates.detail.fields.vmid"), String(t.vmid)]]
    : [
        [SLIM, t.slim_release || "—"],
        [tl("templates.columns.runner"), t.runner_version || "—"],
        [tl("templates.columns.layer"), t.layer_version || "—"],
        [tl("templates.detail.fields.vmid"), t.vmid ? String(t.vmid) : "—"],
      ];
  fields.push(
    [tl("templates.columns.size"), formatBytes(t.size_bytes)],
    [tl("templates.detail.fields.activated"), <RelativeTime key="a" value={t.activated_at} />],
  );
  return (
    <Section title={tl("templates.inUse.title")} icon={<CheckCircleIcon weight="fill" className="text-kumo-success" />}>
      <div className="flex flex-col gap-4">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="flex min-w-0 flex-col gap-1">
            <span className="flex flex-wrap items-center gap-2">
              <span className="truncate font-mono text-sm">{t.id}</span>
              <Flags t={t} />
            </span>
            <span className="text-xs text-kumo-subtle">{t.bootstrap ? tl("templates.bootstrapLine", { vmid: t.vmid }) : triggerLabel(t.trigger)}</span>
          </div>
          {actions}
        </div>
        <dl className="grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-3 lg:grid-cols-6">
          {fields.map(([label, value]) => (
            <div key={label} className="flex min-w-0 flex-col gap-0.5">
              <dt className="text-xs text-kumo-subtle">{label}</dt>
              <dd className="truncate text-sm tabular-nums">{value}</dd>
            </div>
          ))}
        </dl>
        <div className="flex flex-wrap items-center justify-between gap-3 text-sm">
          <FidelitySummary t={t} />
          <span className="flex flex-wrap gap-4">
            {!t.bootstrap && <Link href={detailHref(t.id, "fidelity")}>{tl("templates.inUse.fidelity")}</Link>}
            <Link href={detailHref(t.id)}>{tl("templates.inUse.details")}</Link>
          </span>
        </div>
      </div>
    </Section>
  );
}

function AvailableCard({ templates, actions }: { templates: TemplateView[]; actions: (t: TemplateView) => ReactNode }) {
  const tl = useT();
  return (
    <Section title={tl("templates.available.title")} icon={<ArrowCounterClockwiseIcon className="text-kumo-subtle" />} flush>
      {templates.length === 0 ? (
        <Empty icon={<PackageIcon size={48} className="text-kumo-inactive" />} title={tl("templates.available.empty.title")} description={tl("templates.available.empty.description")} />
      ) : (
        <div className="overflow-x-auto">
          <Table className="min-w-[52rem]">
            <Table.Header>
              <Table.Row>
                <Table.Head>{tl("templates.columns.version")}</Table.Head>
                <Table.Head>{SLIM}</Table.Head>
                <Table.Head>{tl("templates.columns.runner")}</Table.Head>
                <Table.Head>{tl("templates.columns.layer")}</Table.Head>
                <Table.Head>{tl("templates.columns.size")}</Table.Head>
                <Table.Head>{tl("templates.columns.created")}</Table.Head>
                <Table.Head>
                  <span className="sr-only">{tl("templates.columns.actions")}</span>
                </Table.Head>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {templates.map((t) => (
                <Table.Row key={t.id}>
                  <Table.Cell className="max-w-72">
                    <span className="flex flex-wrap items-center gap-1.5">
                      <Link href={detailHref(t.id)} className="truncate font-mono text-sm">
                        {t.id}
                      </Link>
                      {t.state !== "ready" && <TemplateStateBadge state={t.state} />}
                      <Flags t={t} />
                    </span>
                    <span className="block truncate text-xs text-kumo-subtle">
                      {t.bootstrap
                        ? tl("templates.bootstrapLine", { vmid: t.vmid })
                        : `${triggerLabel(t.trigger)}${t.vmid ? ` · ${tl("templates.vmid", { vmid: t.vmid })}` : ""}`}
                    </span>
                  </Table.Cell>
                  <Table.Cell>{t.slim_release || "—"}</Table.Cell>
                  <Table.Cell>{t.runner_version || "—"}</Table.Cell>
                  <Table.Cell>{t.layer_version || "—"}</Table.Cell>
                  <Table.Cell className="tabular-nums">{formatBytes(t.size_bytes)}</Table.Cell>
                  <Table.Cell>
                    <RelativeTime value={t.created_at} />
                  </Table.Cell>
                  <Table.Cell>{actions(t)}</Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table>
        </div>
      )}
    </Section>
  );
}

function historyReason(t: TemplateView): string {
  if (t.state === "failed") return t.failure_reason ? tr("templates.failedAt", { stage: t.failure_stage ?? "", reason: t.failure_reason }) : "";
  if (t.state === "deleted") return tr("templates.history.replaced");
  return tr("templates.history.retired");
}

function BuildHistory({ templates, canDelete }: { templates: TemplateView[]; canDelete: boolean }) {
  const tl = useT();
  if (templates.length === 0)
    return (
      <LayerCard>
        <LayerCard.Primary>
          <Empty icon={<ClockCounterClockwiseIcon size={48} className="text-kumo-inactive" />} title={tl("templates.history.empty.title")} description={tl("templates.history.empty.description")} />
        </LayerCard.Primary>
      </LayerCard>
    );
  return (
    <LayerCard>
      <LayerCard.Primary className="p-0">
        <div className="overflow-x-auto">
          <Table className="min-w-[52rem]">
            <Table.Header>
              <Table.Row>
                <Table.Head>{tl("templates.columns.version")}</Table.Head>
                <Table.Head>{tl("templates.history.columns.versions")}</Table.Head>
                <Table.Head>{tl("templates.history.columns.outcome")}</Table.Head>
                <Table.Head>{tl("templates.history.columns.reason")}</Table.Head>
                <Table.Head>{tl("templates.history.columns.when")}</Table.Head>
                <Table.Head>
                  <span className="sr-only">{tl("templates.columns.actions")}</span>
                </Table.Head>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {templates.map((t) => {
                const reason = historyReason(t);
                return (
                  <Table.Row key={t.id}>
                    <Table.Cell className="max-w-64">
                      <Link href={detailHref(t.id)} className="block truncate font-mono text-sm">
                        {t.id}
                      </Link>
                    </Table.Cell>
                    <Table.Cell className="max-w-72">
                      <span className="block truncate text-sm">{versionsLine(t) || "—"}</span>
                    </Table.Cell>
                    <Table.Cell>
                      <TemplateStateBadge state={t.state} />
                    </Table.Cell>
                    <Table.Cell className="max-w-80">
                      <span className={`block truncate text-sm ${t.state === "failed" ? "text-kumo-danger" : "text-kumo-subtle"}`} title={reason}>
                        {reason || "—"}
                      </span>
                    </Table.Cell>
                    <Table.Cell>
                      <RelativeTime value={t.updated_at} />
                    </Table.Cell>
                    <Table.Cell>{canDelete && (t.state === "failed" || t.state === "deleted") && <DeleteRecord kind="template" id={t.id} />}</Table.Cell>
                  </Table.Row>
                );
              })}
            </Table.Body>
          </Table>
        </div>
      </LayerCard.Primary>
    </LayerCard>
  );
}

const newestFirst = (a: TemplateView, b: TemplateView) => (b.updated_at ?? "").localeCompare(a.updated_at ?? "");

export function TemplatesPage() {
  const tl = useT();
  const { tab, profile: chosen } = useSearch({ strict: false }) as ListSearch;
  const navigate = useNavigate();
  const list = useTemplates();
  const settings = useSettings();
  const qc = useQueryClient();
  const admin = useAdminAction();
  const enabled = list.data?.enabled ?? false;
  const building = list.data?.building ?? false;
  const canAct = settings.data?.admin_actions === true;
  const profilesQuery = useTemplateProfiles();
  const profileNames = (profilesQuery.data?.profiles ?? []).map((p) => p.name);
  // The profile shown stays in the URL; a profile that is gone falls back to the default one.
  const profile = chosen && (profilesQuery.isLoading || profileNames.includes(chosen)) ? chosen : "default";
  const setProfile = (p: string) =>
    void navigate({ to: ".", search: (prev: ListSearch) => ({ ...prev, profile: p === "default" ? undefined : p }), replace: true });
  const all: TemplateView[] = list.data?.templates ?? [];
  // Versions of the chosen profile (records from before profiles belong to the default one).
  const templates = all.filter((t) => (t.profile || "default") === profile);
  const history = tab === "history";
  const profilesTab = tab === "profiles";

  const buildNow = () =>
    admin.run(tl("templates.build.started"), async () => {
      unwrap(await api.POST("/api/v1/templates/build", { body: { profile } }));
      await qc.invalidateQueries({ queryKey: ["templates"] });
    });
  const reason = !enabled
    ? tl("templates.build.notConfigured")
    : building
      ? tl("templates.build.alreadyRunning")
      : !canAct
        ? tl("templates.build.noToken")
        : "";
  const button = (
    <Button variant="primary" icon={HammerIcon} disabled={!!reason || admin.busy} loading={admin.busy} onClick={buildNow}>
      {profileNames.length > 1 ? tl("templates.profiles.build", { name: profile }) : tl("templates.build.now")}
    </Button>
  );

  const inProgress = templates.filter((t) => IN_PROGRESS.includes(t.state));
  const active = templates.find((t) => t.active || t.state === "active");
  const available = templates.filter((t) => t !== active && !IN_PROGRESS.includes(t.state) && !HISTORY.includes(t.state));
  const past = templates.filter((t) => HISTORY.includes(t.state)).sort(newestFirst);
  const actionsFor = (t: TemplateView) => (canAct && (t.state === "ready" || t.state === "active") ? <TemplateActions t={t} run={admin.run} /> : null);

  const tabs = [
    { value: "available", label: tl("templates.tabs.available") },
    { value: "history", label: tl("templates.tabs.history") },
    { value: "profiles", label: tl("templates.profiles.tab") },
  ];
  const picker =
    profileNames.length > 1 && !profilesTab ? (
      <Select
        aria-label={tl("templates.profiles.filter")}
        label={tl("templates.profiles.filter")}
        value={profile}
        onValueChange={(v) => setProfile(String(v ?? "default"))}
        items={Object.fromEntries(profileNames.map((n) => [n, n]))}
        className="w-full sm:w-64"
      />
    ) : null;

  let body;
  if (list.isLoading) body = <Loading />;
  else if (list.error) body = <ErrorState error={list.error} />;
  else if (profilesTab) body = <ProfilesTab canAct={canAct} />;
  else if (history) body = <BuildHistory templates={past} canDelete={canAct} />;
  else
    body = (
      <>
        {inProgress.map((t) => (
          <BuildCard key={t.id} t={t} />
        ))}
        {building && all.filter((t) => IN_PROGRESS.includes(t.state)).length === 0 && <StartingCard />}
        <InUseCard t={active} actions={active && actionsFor(active)} />
        <AvailableCard templates={available} actions={actionsFor} />
      </>
    );

  return (
    <Page
      title={tl("templates.title")}
      description={tl("templates.description")}
      actions={profilesTab ? undefined : reason ? <Tooltip content={reason} render={<span>{button}</span>} /> : button}
    >
      {list.data && !enabled && (
        <Banner
          variant="secondary"
          icon={<InfoIcon weight="fill" />}
          title={tl("templates.build.notConfigured")}
          description={tl("templates.build.notConfiguredHelp")}
        />
      )}
      <DetailTabs push tabs={tabs} value={profilesTab ? "profiles" : history ? "history" : "available"}>
        {picker}
        {body}
      </DetailTabs>
    </Page>
  );
}
