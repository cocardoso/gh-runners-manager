import { Badge, LayerCard } from "@cloudflare/kumo";
import { useSettings } from "@/api/queries";
import { ErrorState, Loading, Page } from "@/components/common";
import { DefinitionList } from "@/components/definition-list";
import { CredentialsEditor } from "@/components/credentials-editor";
import { CacheCard } from "@/components/cache-card";
import { formatMB } from "@/lib/format";

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
  const settings = useSettings();
  if (settings.isLoading) return <Page title="Settings"><Loading /></Page>;
  if (settings.error || !settings.data) return <Page title="Settings"><ErrorState error={settings.error} /></Page>;
  const s = settings.data;
  const p = (s.proxmox ?? {}) as Map;
  const c = (s.capacity ?? {}) as Map;
  const i = (s.ingest ?? {}) as Map;
  const range = Array.isArray(p.vmid_range) ? `${p.vmid_range[0]}–${p.vmid_range[1]}` : "—";
  return (
    <Page title="Settings" description="The running configuration, without secrets. Credentials and scale sets can be added here; the rest comes from ghrm.yaml.">
      <Section title="GitHub credentials">
        <CredentialsEditor />
      </Section>
      <CacheCard />
      <Section title="Control plane">
        <DefinitionList
          items={[
            ["Version", s.version],
            ["Admin actions", s.admin_actions ? <Badge variant="success" appearance="dot">Enabled</Badge> : <Badge variant="neutral" appearance="dot">Disabled</Badge>],
          ]}
        />
      </Section>
      <Section title="Proxmox">
        <DefinitionList
          items={[
            ["API URL", text(p.url)],
            ["Node", text(p.node)],
            ["API token", text(p.token_id)],
            ["TLS certificate", p.tls_pinned ? "Pinned by fingerprint" : "System trust store"],
            ["Template", text(p.template_vmid)],
            ["Pool", text(p.pool)],
            ["VMID range", range],
            ["Storage", text(p.storage)],
            ["Thin pool", text(p.thin_pool)],
            ["Firewall settle time", text(p.firewall_settle)],
          ]}
        />
      </Section>
      <Section title="Capacity">
        <DefinitionList
          items={[
            ["Max environments", text(c.max_environments)],
            ["Memory budget", typeof c.memory_budget_mb === "number" ? formatMB(c.memory_budget_mb) : "—"],
            ["Host memory margin", typeof c.memory_margin_mb === "number" ? formatMB(c.memory_margin_mb) : "—"],
            ["Max disk use", typeof c.max_disk_percent === "number" ? `${c.max_disk_percent}%` : "—"],
          ]}
        />
      </Section>
      <Section title="Agent ingest">
        <DefinitionList
          items={[
            ["Listen address", text(i.listen)],
            ["Address given to environments", text(i.advertise_url)],
          ]}
        />
      </Section>
    </Page>
  );
}
