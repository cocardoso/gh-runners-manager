import type { ReactNode } from "react";
import { InlineCopyText, LayerCard, Link } from "@cloudflare/kumo";
import type { Environment } from "@/api/client";
import { formatAbsolute, formatMB } from "@/lib/format";
import { EnvironmentStateBadge } from "./status-badge";
import { useT } from "@/i18n";

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
  const t = useT();
  return (
    <LayerCard>
      <LayerCard.Secondary>{t("overview.environment.title")}</LayerCard.Secondary>
      <LayerCard.Primary className="p-0">
        <dl>
          <Row label={t("overview.environment.id")}>
            <InlineCopyText variant="mono">{e.id}</InlineCopyText>
          </Row>
          <Row label={t("overview.environment.state")}>
            <EnvironmentStateBadge state={e.state} />
          </Row>
          {e.failure_stage && (
            <Row label={t("overview.environment.failure")}>
              <span className="text-kumo-danger">
                {t("overview.environment.failureAt", { stage: e.failure_stage, reason: e.failure_reason || t("overview.environment.noReason") })}
              </span>
            </Row>
          )}
          <Row label={t("overview.environment.scaleSet")}>{e.scale_set}</Row>
          <Row label={t("overview.environment.runtimeRef")}>{e.runtime_ref || "—"}</Row>
          <Row label={t("overview.environment.ip")}>{e.ip || "—"}</Row>
          <Row label={t("overview.environment.runner")}>{e.runner_name ? `${e.runner_name}${e.runner_id ? ` (#${e.runner_id})` : ""}` : "—"}</Row>
          <Row label={t("overview.environment.memory")}>{formatMB(e.memory_mb)}</Row>
          {e.exit_code !== undefined && e.exit_code !== null && <Row label={t("overview.environment.exitCode")}>{e.exit_code}</Row>}
          {jobLink && <Row label={t("overview.environment.job")}>{e.job_id ? <Link href={`/jobs/${encodeURIComponent(e.job_id)}`}>{e.job_id}</Link> : t("overview.environment.noJob")}</Row>}
          <Row label={t("overview.environment.created")}>{formatAbsolute(e.created_at)}</Row>
          <Row label={t("overview.environment.lastStateChange")}>{formatAbsolute(e.state_changed_at)}</Row>
        </dl>
      </LayerCard.Primary>
    </LayerCard>
  );
}
