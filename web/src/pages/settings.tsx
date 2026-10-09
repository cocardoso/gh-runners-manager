import { Badge, LayerCard } from "@cloudflare/kumo";
import { useSettings } from "@/api/queries";
import { ErrorState, Loading, Page } from "@/components/common";
import { DefinitionList } from "@/components/definition-list";
import { CredentialsEditor } from "@/components/credentials-editor";
import { CacheCard } from "@/components/cache-card";
import { CapacityCard } from "@/components/capacity-card";
import { HistoryCard } from "@/components/history-card";
import { useT } from "@/i18n";

type Map = Record<string, unknown>;
const text = (v: unknown) => (v === undefined || v === null || v === "" ? "—" : String(v));

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <LayerCard>
      <LayerCard.Secondary>{title}</LayerCard.Secondary>
      <LayerCard.Primary className="p-0">{children}</LayerCard.Primary>
    </LayerCard>
  );
}

export function SettingsPage() {
  const t = useT();
  const settings = useSettings();
  if (settings.isLoading) return <Page title={t("settings.title")}><Loading /></Page>;
  if (settings.error || !settings.data) return <Page title={t("settings.title")}><ErrorState error={settings.error} /></Page>;
  const s = settings.data;
  const p = (s.proxmox ?? {}) as Map;
  const i = (s.ingest ?? {}) as Map;
  const range = Array.isArray(p.vmid_range) ? `${p.vmid_range[0]}–${p.vmid_range[1]}` : "—";
  return (
    <Page title={t("settings.title")} description={t("settings.description")}>
      <Section title={t("settings.sections.credentials")}>
        <CredentialsEditor />
      </Section>
      <CapacityCard />
      <CacheCard />
      <HistoryCard />
      <Section title={t("settings.sections.controlPlane")}>
        <DefinitionList
          items={[
            [t("settings.fields.version"), s.version],
            [
              t("settings.fields.adminActions"),
              s.admin_actions ? (
                <Badge variant="success" appearance="dot">{t("settings.fields.enabled")}</Badge>
              ) : (
                <Badge variant="neutral" appearance="dot">{t("settings.fields.disabled")}</Badge>
              ),
            ],
          ]}
        />
      </Section>
      <Section title="Proxmox">
        <DefinitionList
          items={[
            [t("settings.fields.apiUrl"), text(p.url)],
            [t("settings.fields.node"), text(p.node)],
            [t("settings.fields.apiToken"), text(p.token_id)],
            [t("settings.fields.tlsCertificate"), p.tls_pinned ? t("settings.fields.tlsPinned") : t("settings.fields.tlsSystem")],
            [t("settings.fields.template"), text(p.template_vmid)],
            [t("settings.fields.pool"), text(p.pool)],
            [t("settings.fields.vmidRange"), range],
            [t("settings.fields.storage"), text(p.storage)],
            [t("settings.fields.thinPool"), text(p.thin_pool)],
            [t("settings.fields.firewallSettle"), text(p.firewall_settle)],
          ]}
        />
      </Section>
      <Section title={t("settings.sections.ingest")}>
        <DefinitionList
          items={[
            [t("settings.fields.listen"), text(i.listen)],
            [t("settings.fields.advertise"), text(i.advertise_url)],
          ]}
        />
      </Section>
    </Page>
  );
}
