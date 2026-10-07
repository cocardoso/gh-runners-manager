import type { ReactNode } from "react";
import { InlineCopyText, LayerCard, Link } from "@cloudflare/kumo";
import type { Environment } from "@/api/client";
import { formatAbsolute, formatMB } from "@/lib/format";
import { EnvironmentStateBadge } from "./status-badge";

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-1 border-b border-kumo-line px-4 py-2.5 last:border-b-0 sm:grid-cols-[12rem_1fr] sm:gap-4">
      <dt className="text-sm text-kumo-subtle">{label}</dt>
      <dd className="min-w-0 break-words text-sm text-kumo-default">{children}</dd>
    </div>
  );
}

/** Runtime details of an environment. */
export function EnvironmentPanel({ environment: e, jobLink = true }: { environment: Environment; jobLink?: boolean }) {
  return (
    <LayerCard>
      <LayerCard.Secondary>Environment</LayerCard.Secondary>
      <LayerCard.Primary className="p-0">
        <dl>
          <Row label="ID">
            <InlineCopyText variant="mono">{e.id}</InlineCopyText>
          </Row>
          <Row label="State">
            <EnvironmentStateBadge state={e.state} />
          </Row>
          {e.failure_stage && (
            <Row label="Failure">
              <span className="text-kumo-danger">
                at {e.failure_stage}: {e.failure_reason || "no reason recorded"}
              </span>
            </Row>
          )}
          <Row label="Scale set">{e.scale_set}</Row>
          <Row label="Runtime reference">{e.runtime_ref || "—"}</Row>
          <Row label="IP address">{e.ip || "—"}</Row>
          <Row label="Runner">{e.runner_name ? `${e.runner_name}${e.runner_id ? ` (#${e.runner_id})` : ""}` : "—"}</Row>
          <Row label="Memory">{formatMB(e.memory_mb)}</Row>
          {e.exit_code !== undefined && e.exit_code !== null && <Row label="Runner exit code">{e.exit_code}</Row>}
          {jobLink && <Row label="Job">{e.job_id ? <Link href={`/jobs/${encodeURIComponent(e.job_id)}`}>{e.job_id}</Link> : "No job was assigned"}</Row>}
          <Row label="Created">{formatAbsolute(e.created_at)}</Row>
          <Row label="Last state change">{formatAbsolute(e.state_changed_at)}</Row>
        </dl>
      </LayerCard.Primary>
    </LayerCard>
  );
}
