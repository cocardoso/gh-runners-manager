import { useState } from "react";
import { Banner, Button, Link } from "@cloudflare/kumo";
import { InfoIcon, WarningCircleIcon, WarningIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type Alert } from "@/api/client";
import { RelativeTime } from "@/components/common";
import { useT } from "@/i18n";
import { withNode } from "@/i18n/nodes";
import { formatNumber } from "@/lib/format";
import { knownReason, waitingTitle } from "@/lib/waiting";

// Alerts shown before "show more": enough to see what is wrong, not a wall of red.
const SHOWN_ALERTS = 3;

const failedPrefix = /^environment failed at [^:]+: /;

const severity = (a: Alert) => (a.level === "error" ? 0 : a.level === "warn" || a.level === "warning" ? 1 : 2);

// An alert is dismissed as it was seen: what happens after it stays visible.
async function dismiss(alerts: Alert[]) {
  unwrap(await api.POST("/api/v1/alerts/dismiss", { body: { alerts: alerts.map((a) => ({ id: a.id, time: a.time })) } }));
}

function AlertBanner({ alert, onDismiss, busy }: { alert: Alert; onDismiss: () => void; busy: boolean }) {
  const t = useT();
  const variant = alert.level === "error" ? "error" : alert.level === "warn" || alert.level === "warning" ? "alert" : "secondary";
  const Icon = variant === "error" ? WarningCircleIcon : variant === "alert" ? WarningIcon : InfoIcon;
  const failed = alert.kind === "environment_failed";
  const count = Math.max(alert.count ?? 1, 1);
  // The server words its alerts in English; a waiting reason and a failure's stage have a translation.
  const reason = alert.kind === "waiting" ? alert.message.split(": ").pop() : undefined;
  const stage = failed ? /^environment failed at ([^:]+):/.exec(alert.message)?.[1] : undefined;
  const title = failed
    ? t("overview.alerts.failed", { count, n: formatNumber(count), stage: stage ?? "?" })
    : knownReason(reason)
      ? waitingTitle(t, reason)
      : alert.message;
  const detail = failed ? alert.message.replace(failedPrefix, "") : undefined;
  return (
    <Banner
      variant={variant}
      icon={<Icon weight="fill" />}
      title={title}
      description={
        <span className="flex min-w-0 flex-col gap-1">
          {detail && (
            <span className="line-clamp-2 break-words font-mono text-xs [overflow-wrap:anywhere]" title={detail}>
              {detail}
            </span>
          )}
          <span className="flex flex-wrap items-center gap-x-2">
            {alert.scale_set && <span>{alert.scale_set}</span>}
            {count > 1 ? (
              <>
                <span>{withNode(t("overview.alerts.first"), "{time}", <RelativeTime value={alert.first_time} />)}</span>
                <span>{withNode(t("overview.alerts.latest"), "{time}", <RelativeTime value={alert.time} />)}</span>
              </>
            ) : (
              <RelativeTime value={alert.time} />
            )}
            {alert.environment_id && (
              <Link href={`/environments/${encodeURIComponent(alert.environment_id)}`}>
                {count > 1 ? t("overview.alerts.openLatest") : t("overview.openEnvironment")}
              </Link>
            )}
          </span>
        </span>
      }
      action={
        alert.dismissible ? (
          <Banner.Action variant="ghost" onClick={onDismiss} disabled={busy} aria-label={t("overview.alerts.dismissLabel", { title })}>
            {t("overview.alerts.dismiss")}
          </Banner.Action>
        ) : undefined
      }
    />
  );
}

/** The overview's alerts: one per cause, the first few shown, each dismissible until it happens again. */
export function AlertsPanel({ alerts }: { alerts: Alert[] }) {
  const t = useT();
  const qc = useQueryClient();
  const [expanded, setExpanded] = useState(false);
  // Hidden at once on dismiss, until the overview is fetched again: from then on the
  // server decides (an alert that happens again comes back with the same ID).
  const [gone, setGone] = useState<Set<string>>(() => new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  // Errors first; the server's order (most recent first) within a level.
  const visible = alerts.filter((a) => !gone.has(a.id)).sort((a, b) => severity(a) - severity(b));
  if (visible.length === 0 && !error) return null;
  const shown = expanded ? visible : visible.slice(0, SHOWN_ALERTS);
  const hidden = visible.length - shown.length;
  const dismissible = visible.filter((a) => a.dismissible);

  const run = async (list: Alert[]) => {
    const ids = list.map((a) => a.id);
    setBusy(true);
    setError(undefined);
    setGone((g) => new Set([...g, ...ids]));
    try {
      await dismiss(list);
      await qc.invalidateQueries({ queryKey: ["overview"] });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setGone((g) => new Set([...g].filter((id) => !ids.includes(id))));
      setBusy(false);
    }
  };

  return (
    <section aria-label={t("overview.alerts.title")} className="flex flex-col gap-2">
      {error && <Banner variant="error" title={t("overview.alerts.dismissFailed")} description={error} />}
      {shown.map((a) => (
        <AlertBanner key={a.id || `${a.kind}-${a.environment_id ?? a.scale_set ?? ""}`} alert={a} busy={busy} onDismiss={() => void run([a])} />
      ))}
      {(hidden > 0 || expanded || dismissible.length > 1) && (
        <div className="flex flex-wrap items-center justify-between gap-2">
          {hidden > 0 || expanded ? (
            <Button variant="ghost" size="sm" onClick={() => setExpanded((v) => !v)}>
              {expanded ? t("overview.alerts.showLess") : t("overview.alerts.showMore", { count: hidden, n: formatNumber(hidden) })}
            </Button>
          ) : (
            <span />
          )}
          {dismissible.length > 1 && (
            <Button variant="ghost" size="sm" disabled={busy} onClick={() => void run(dismissible)}>
              {t("overview.alerts.dismissAll", { count: dismissible.length, n: formatNumber(dismissible.length) })}
            </Button>
          )}
        </div>
      )}
    </section>
  );
}
