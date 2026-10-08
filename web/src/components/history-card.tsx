import { useId, useState } from "react";
import { Banner, Button, Dialog, DialogRoot, DialogTitle, Input, LayerCard, Radio, useKumoToastManager } from "@cloudflare/kumo";
import { BroomIcon } from "@phosphor-icons/react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import type { components } from "@/api/schema";
import { useHistorySettings } from "@/api/queries";
import { formatNumber } from "@/lib/format";
import { useAdminAction } from "@/components/admin-action";
import { HelpLabel, helpField } from "@/components/help-tip";
import { ErrorState, Loading } from "@/components/common";
import { tr, useT } from "@/i18n";

type Settings = components["schemas"]["HistorySettings"];
type Counts = components["schemas"]["CleanupResult"];

const DAY = 24 * 60 * 60 * 1000;

/** "3 environments, 4 jobs, …", or "Nothing to delete". */
export function describeCounts(c: Counts) {
  if (countTotal(c) === 0) return tr("settings.history.nothing");
  return [
    tr("settings.history.environments", { count: c.environments, n: formatNumber(c.environments) }),
    tr("settings.history.jobs", { count: c.jobs, n: formatNumber(c.jobs) }),
    tr("settings.history.events", { count: c.events, n: formatNumber(c.events) }),
    tr("settings.history.auditEvents", { count: c.audit_events, n: formatNumber(c.audit_events) }),
    tr("settings.history.templateRecords", { count: c.templates, n: formatNumber(c.templates) }),
  ].join(", ");
}

function countTotal(c: Counts) {
  return c.environments + c.jobs + c.events + c.audit_events + c.templates;
}

/** The browser's calendar day of t, as an input[type=date] value. */
export function dateInput(t: Date) {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${t.getFullYear()}-${pad(t.getMonth() + 1)}-${pad(t.getDate())}`;
}

function CleanupDialog({ days, onClose }: { days: number; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const toast = useKumoToastManager();
  const [date, setDate] = useState(() => dateInput(new Date(Date.now() - days * DAY)));
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const before = date ? new Date(`${date}T00:00:00`).toISOString() : "";
  const previewQuery = useQuery({
    queryKey: ["history-preview", before],
    enabled: !!before,
    queryFn: async () => unwrap(await api.POST("/api/v1/history/cleanup", { body: { before, dry_run: true } })),
  });
  const preview = previewQuery.data;
  const empty = !preview || countTotal(preview) === 0;
  const clean = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const done = unwrap(await api.POST("/api/v1/history/cleanup", { body: { before } }));
      toast.add({
        title: t("settings.history.deleted"),
        description: done.warning ? `${describeCounts(done)}. ${done.warning}` : describeCounts(done),
        variant: done.warning ? "warning" : "success",
      });
      for (const key of ["templates", "template", "environments", "environment", "jobs", "job", "stats", "overview", "repositories"]) void qc.invalidateQueries({ queryKey: [key] });
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <DialogRoot open onOpenChange={(o) => !o && onClose()}>
      <Dialog size="sm" className="flex flex-col gap-4 p-6">
        <DialogTitle className="text-lg font-semibold">{t("settings.history.dialogTitle")}</DialogTitle>
        <Input
          label={t("settings.history.before")}
          type="date"
          value={date}
          max={dateInput(new Date())}
          onChange={(e) => setDate(e.target.value)}
          description={t("settings.history.beforeHint")}
        />
        <p role="status" className="text-sm">
          {preview ? describeCounts(preview) : t("settings.history.counting")}
        </p>
        {(error ?? previewQuery.error) && <Banner variant="error" title={error ?? String(previewQuery.error)} />}
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            {t("settings.history.cancel")}
          </Button>
          <Button variant="destructive" loading={busy} disabled={empty} onClick={() => void clean()}>
            {t("settings.history.delete")}
          </Button>
        </div>
      </Dialog>
    </DialogRoot>
  );
}

function SettingsForm({ initial }: { initial: Settings }) {
  const t = useT();
  const id = useId();
  const qc = useQueryClient();
  const admin = useAdminAction();
  const [mode, setMode] = useState<Settings["mode"]>(initial.mode);
  const [days, setDays] = useState(String(initial.days));
  const [auditDays, setAuditDays] = useState(String(initial.audit_days));
  const [cleaning, setCleaning] = useState(false);
  const save = () =>
    admin.run(t("settings.history.saved"), async () => {
      unwrap(await api.PUT("/api/v1/history/settings", { body: { mode, days: Number(days), audit_days: Number(auditDays) } }));
      await qc.invalidateQueries({ queryKey: ["history-settings"] });
    });
  return (
    <div className="flex flex-col gap-4">
      {/* The help sits outside the legend, so the group is named by its title alone. */}
      <div className="-mb-2 text-base font-medium text-kumo-default">
        <HelpLabel label={<span aria-hidden>{t("settings.history.cleanup")}</span>} help={t("settings.historyHelp.modeTip")} />
      </div>
      <Radio.Group value={mode} onValueChange={(v) => setMode(v as Settings["mode"])}>
        <Radio.Legend className="sr-only">{t("settings.history.cleanup")}</Radio.Legend>
        <Radio.Item label={t("settings.history.automatic")} value="automatic" />
        <Radio.Item label={t("settings.history.manual")} value="manual" />
      </Radio.Group>
      <p className="text-sm text-kumo-subtle">
        {mode === "automatic" ? t("settings.history.automaticHint") : t("settings.history.manualHint")}
      </p>
      {/* items-start: a field without a hint keeps its own height, so both inputs line up. */}
      <div className="grid items-start gap-4 sm:grid-cols-2">
        <Input
          {...helpField(`${id}-days`, t("settings.history.keepDays"), t("settings.historyHelp.daysTip"))}
          type="number"
          min={1}
          max={365}
          value={days}
          onChange={(e) => setDays(e.target.value)}
        />
        <Input
          {...helpField(`${id}-audit`, t("settings.history.keepAuditDays"), t("settings.historyHelp.auditTip"))}
          type="number"
          min={1}
          max={3650}
          value={auditDays}
          onChange={(e) => setAuditDays(e.target.value)}
          description={t("settings.history.auditHint")}
        />
      </div>
      <div className="flex flex-wrap gap-2">
        <Button variant="primary" loading={admin.busy} onClick={save}>
          {t("settings.history.save")}
        </Button>
        <Button variant="secondary" icon={BroomIcon} onClick={() => setCleaning(true)}>
          {t("settings.history.cleanUpNow")}
        </Button>
      </div>
      {cleaning && <CleanupDialog days={Number(days) || initial.days} onClose={() => setCleaning(false)} />}
    </div>
  );
}

/** How long history is kept, and a way to delete it now. */
export function HistoryCard() {
  const t = useT();
  const history = useHistorySettings();
  return (
    <section aria-labelledby="history-title">
      <LayerCard>
        <LayerCard.Secondary>
          <h2 id="history-title" className="text-base font-medium">
            {t("settings.history.title")}
          </h2>
        </LayerCard.Secondary>
        <LayerCard.Primary className="flex flex-col gap-4 p-4">
          {history.isPending ? <Loading /> : history.error || !history.data ? <ErrorState error={history.error} /> : <SettingsForm initial={history.data} />}
        </LayerCard.Primary>
      </LayerCard>
    </section>
  );
}
