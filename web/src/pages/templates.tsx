import { Badge, Banner, Button, DropdownMenu, Empty, LayerCard, Link, Table, Tooltip } from "@cloudflare/kumo";
import { DotsThreeIcon, HammerIcon, InfoIcon, PackageIcon, PushPinIcon, PushPinSlashIcon, ArrowCounterClockwiseIcon, CheckCircleIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type TemplateVersion } from "@/api/client";
import { useSettings, useTemplates } from "@/api/queries";
import { useAdminAction } from "@/components/admin-action";
import { ErrorState, Loading, Page, RelativeTime } from "@/components/common";
import { TemplateStateBadge } from "@/components/status-badge";
import { currentFormatLocale, tr, useT, type Key } from "@/i18n";

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

export function TemplatesPage() {
  const tl = useT();
  const list = useTemplates();
  const settings = useSettings();
  const qc = useQueryClient();
  const admin = useAdminAction();
  const enabled = list.data?.enabled ?? false;
  const building = list.data?.building ?? false;
  const canAct = settings.data?.admin_actions === true;
  const templates: TemplateView[] = list.data?.templates ?? [];

  const buildNow = () =>
    admin.run(tl("templates.build.started"), async () => {
      unwrap(await api.POST("/api/v1/templates/build"));
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
      {tl("templates.build.now")}
    </Button>
  );

  let body;
  if (list.isLoading) body = <Loading />;
  else if (list.error) body = <ErrorState error={list.error} />;
  else if (templates.length === 0)
    body = <Empty icon={<PackageIcon size={48} className="text-kumo-inactive" />} title={tl("templates.empty.title")} description={tl("templates.empty.description")} />;
  else
    body = (
      <div className="overflow-x-auto">
        <Table className="min-w-[56rem]">
          <Table.Header>
            <Table.Row>
              <Table.Head>{tl("templates.columns.version")}</Table.Head>
              <Table.Head>ubuntu-slim</Table.Head>
              <Table.Head>{tl("templates.columns.runner")}</Table.Head>
              <Table.Head>{tl("templates.columns.layer")}</Table.Head>
              <Table.Head>{tl("templates.columns.state")}</Table.Head>
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
                  <Link href={`/templates/${encodeURIComponent(t.id)}`} className="block truncate font-mono text-sm">
                    {t.id}
                  </Link>
                  <span className="block truncate text-xs text-kumo-subtle">
                    {t.bootstrap
                      ? tl("templates.bootstrapLine", { vmid: t.vmid })
                      : `${triggerLabel(t.trigger)}${t.vmid ? ` · ${tl("templates.vmid", { vmid: t.vmid })}` : ""}`}
                  </span>
                </Table.Cell>
                <Table.Cell>{t.slim_release || "—"}</Table.Cell>
                <Table.Cell>{t.runner_version || "—"}</Table.Cell>
                <Table.Cell>{t.layer_version || "—"}</Table.Cell>
                <Table.Cell>
                  <span className="flex flex-wrap items-center gap-1">
                    <TemplateStateBadge state={t.state} />
                    <Flags t={t} />
                  </span>
                  {t.failure_reason && <span className="block max-w-64 truncate text-xs text-kumo-danger" title={t.failure_reason}>{tl("templates.failedAt", { stage: t.failure_stage ?? "", reason: t.failure_reason })}</span>}
                </Table.Cell>
                <Table.Cell className="tabular-nums">{formatBytes(t.size_bytes)}</Table.Cell>
                <Table.Cell>
                  <RelativeTime value={t.created_at} />
                </Table.Cell>
                <Table.Cell>{canAct && (t.state === "ready" || t.state === "active") && <TemplateActions t={t} run={admin.run} />}</Table.Cell>
              </Table.Row>
            ))}
          </Table.Body>
        </Table>
      </div>
    );

  return (
    <Page
      title={tl("templates.title")}
      description={tl("templates.description")}
      actions={reason ? <Tooltip content={reason} render={<span>{button}</span>} /> : button}
    >
      {list.data && !enabled && (
        <Banner
          variant="secondary"
          icon={<InfoIcon weight="fill" />}
          title={tl("templates.build.notConfigured")}
          description={tl("templates.build.notConfiguredHelp")}
        />
      )}
      {building && (
        <Banner variant="default" icon={<HammerIcon weight="fill" />} title={tl("templates.build.runningTitle")} description={tl("templates.build.runningHelp")} />
      )}
      <LayerCard>
        <LayerCard.Primary className="p-0">{body}</LayerCard.Primary>
      </LayerCard>
    </Page>
  );
}
