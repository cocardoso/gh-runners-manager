import { Empty, LayerCard, Link } from "@cloudflare/kumo";
import { PackageIcon } from "@phosphor-icons/react";
import { useSettings } from "@/api/queries";
import { Page } from "@/components/common";

export function TemplatesPage() {
  const settings = useSettings();
  const vmid = (settings.data?.proxmox as Record<string, unknown> | undefined)?.template_vmid;
  return (
    <Page title="Templates" description="The images every environment is cloned from.">
      <LayerCard>
        <LayerCard.Primary>
          <Empty
            icon={<PackageIcon size={48} className="text-kumo-inactive" />}
            title="Template builds are coming next"
            description={`Environments are cloned from the template${vmid ? ` with VMID ${vmid}` : ""} configured in ghrm.yaml. Building ubuntu-slim templates, with versions, a fidelity report, pinning and rollback, arrives in the next milestone.`}
            contents={<Link href="/settings">See the current configuration</Link>}
          />
        </LayerCard.Primary>
      </LayerCard>
    </Page>
  );
}
