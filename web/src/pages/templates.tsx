import { Badge, Banner, Button, DropdownMenu, Empty, LayerCard, Link, Table, Tooltip } from "@cloudflare/kumo";
import { DotsThreeIcon, HammerIcon, InfoIcon, PackageIcon, PushPinIcon, PushPinSlashIcon, ArrowCounterClockwiseIcon, CheckCircleIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type TemplateVersion } from "@/api/client";
import { useSettings, useTemplates } from "@/api/queries";
import { useAdminAction } from "@/components/admin-action";
import { ErrorState, Loading, Page, RelativeTime } from "@/components/common";
import { TemplateStateBadge } from "@/components/status-badge";

export type TemplateView = TemplateVersion;

export function formatBytes(n: number): string {
  if (!n) return "—";
  if (n < 1024 ** 2) return `${Math.max(1, Math.round(n / 1024))} KB`;
  const gb = n / 1024 ** 3;
  return gb >= 1 ? `${gb.toFixed(2)} GB` : `${Math.round(n / 1024 ** 2)} MB`;
}

const triggerLabel: Record<string, string> = {
  manual: "Manual",
  "slim-release": "New ubuntu-slim release",
  "runner-release": "New runner release",
  layer: "New ghrm layer",
  "bootstrap-replacement": "Replaces the bootstrap template",
  bootstrap: "Bootstrap",
};

function Flags({ t }: { t: TemplateView }) {
  return (
    <span className="flex flex-wrap gap-1">
      {t.pinned && <Badge variant="outline">Pinned</Badge>}
      {t.in_use && <Badge variant="outline">In use</Badge>}
    </span>
  );
}

export function TemplateActions({ t, run }: { t: TemplateView; run: ReturnType<typeof useAdminAction>["run"] }) {
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: ["templates"] });
  const activate = () =>
    run(t.state === "ready" && t.activated_at && !t.activated_at.startsWith("0001") ? `Rolled back to ${t.id}` : `${t.id} is now active`, async (auth) => {
      unwrap(await api.POST("/api/v1/templates/{id}/activate", { params: { path: { id: t.id }, header: { Authorization: auth } } }));
      await refresh();
    });
  const pin = (pinned: boolean) =>
    run(pinned ? `${t.id} pinned` : `${t.id} unpinned`, async (auth) => {
      const path = pinned ? "/api/v1/templates/{id}/pin" : "/api/v1/templates/{id}/unpin";
      unwrap(await api.POST(path, { params: { path: { id: t.id }, header: { Authorization: auth } } }));
      await refresh();
    });
  const wasActive = !!t.activated_at && !t.activated_at.startsWith("0001");
  return (
    <DropdownMenu>
      <DropdownMenu.Trigger render={<Button variant="ghost" size="sm" shape="square" icon={DotsThreeIcon} aria-label={`Actions for ${t.id}`} />} />
      <DropdownMenu.Content>
        <DropdownMenu.Item icon={wasActive ? ArrowCounterClockwiseIcon : CheckCircleIcon} disabled={t.active} onClick={activate}>
          {wasActive ? "Roll back to this version" : "Activate"}
        </DropdownMenu.Item>
        {t.pinned ? (
          <DropdownMenu.Item icon={PushPinSlashIcon} onClick={() => pin(false)}>
            Unpin
          </DropdownMenu.Item>
        ) : (
          <DropdownMenu.Item icon={PushPinIcon} onClick={() => pin(true)}>
            Pin (stop automatic activation)
          </DropdownMenu.Item>
        )}
      </DropdownMenu.Content>
    </DropdownMenu>
  );
}

export function TemplatesPage() {
  const list = useTemplates();
  const settings = useSettings();
  const qc = useQueryClient();
  const admin = useAdminAction();
  const enabled = list.data?.enabled ?? false;
  const building = list.data?.building ?? false;
  const canAct = settings.data?.admin_actions === true;
  const templates: TemplateView[] = list.data?.templates ?? [];

  const buildNow = () =>
    admin.run("Build started", async (auth) => {
      unwrap(await api.POST("/api/v1/templates/build", { params: { header: { Authorization: auth } } }));
      await qc.invalidateQueries({ queryKey: ["templates"] });
    });
  const reason = !enabled ? "Template builds are not configured" : building ? "A build is already running" : !canAct ? "No admin token is configured" : "";
  const button = (
    <Button variant="primary" icon={HammerIcon} disabled={!!reason || admin.busy} loading={admin.busy} onClick={buildNow}>
      Build now
    </Button>
  );

  let body;
  if (list.isLoading) body = <Loading />;
  else if (list.error) body = <ErrorState error={list.error} />;
  else if (templates.length === 0)
    body = <Empty icon={<PackageIcon size={48} className="text-kumo-inactive" />} title="No templates" description="The configured template is registered on the first start." />;
  else
    body = (
      <div className="overflow-x-auto">
        <Table className="min-w-[56rem]">
          <Table.Header>
            <Table.Row>
              <Table.Head>Version</Table.Head>
              <Table.Head>ubuntu-slim</Table.Head>
              <Table.Head>Runner</Table.Head>
              <Table.Head>Layer</Table.Head>
              <Table.Head>State</Table.Head>
              <Table.Head>Size</Table.Head>
              <Table.Head>Created</Table.Head>
              <Table.Head>
                <span className="sr-only">Actions</span>
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
                    {t.bootstrap ? `Bootstrap: the configured template (VMID ${t.vmid})` : `${triggerLabel[t.trigger ?? ""] ?? t.trigger ?? ""}${t.vmid ? ` · VMID ${t.vmid}` : ""}`}
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
                  {t.failure_reason && <span className="block max-w-64 truncate text-xs text-kumo-danger" title={t.failure_reason}>{`at ${t.failure_stage}: ${t.failure_reason}`}</span>}
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
      title="Templates"
      description="Every environment is cloned from the active template, built from GitHub's ubuntu-slim recipe plus the ghrm layer."
      actions={reason ? <Tooltip content={reason} render={<span>{button}</span>} /> : button}
    >
      {list.data && !enabled && (
        <Banner
          variant="secondary"
          icon={<InfoIcon weight="fill" />}
          title="Template builds are not configured"
          description="Set templates.vmid_range in ghrm.yaml (and give the API token Datastore.AllocateTemplate on the template storage). Until then every environment clones the configured template."
        />
      )}
      {building && (
        <Banner variant="default" icon={<HammerIcon weight="fill" />} title="A template build is running" description="It is listed below; open it to follow the build log." />
      )}
      <LayerCard>
        <LayerCard.Primary className="p-0">{body}</LayerCard.Primary>
      </LayerCard>
      {admin.dialog}
    </Page>
  );
}
