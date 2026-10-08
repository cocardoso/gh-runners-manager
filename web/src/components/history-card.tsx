import { useState } from "react";
import { Banner, Button, Dialog, DialogRoot, DialogTitle, Input, LayerCard, Radio, useKumoToastManager } from "@cloudflare/kumo";
import { BroomIcon } from "@phosphor-icons/react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import type { components } from "@/api/schema";
import { useHistorySettings } from "@/api/queries";
import { useAdminAction } from "@/components/admin-action";
import { ErrorState, Loading } from "@/components/common";

type Settings = components["schemas"]["HistorySettings"];
type Counts = components["schemas"]["HistoryCounts"];

const DAY = 24 * 60 * 60 * 1000;

/** "3 environments, 4 jobs, …", or "Nothing to delete". */
export function describeCounts(c: Counts) {
  const parts = (
    [
      [c.environments, "environment", "environments"],
      [c.jobs, "job", "jobs"],
      [c.events, "event", "events"],
      [c.audit_events, "audit event", "audit events"],
      [c.templates, "template record", "template records"],
    ] as const
  ).map(([n, one, many]) => `${n} ${n === 1 ? one : many}`);
  const total = c.environments + c.jobs + c.events + c.audit_events + c.templates;
  return total === 0 ? "Nothing to delete" : parts.join(", ");
}

function dateInput(t: Date) {
  return t.toISOString().slice(0, 10);
}

function CleanupDialog({ days, onClose }: { days: number; onClose: () => void }) {
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
  const empty = !preview || describeCounts(preview) === "Nothing to delete";
  const clean = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const done = unwrap(await api.POST("/api/v1/history/cleanup", { body: { before } }));
      toast.add({ title: "History deleted", description: describeCounts(done), variant: "success" });
      for (const key of ["templates", "template", "environments", "environment", "jobs", "events", "overview"]) void qc.invalidateQueries({ queryKey: [key] });
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
        <DialogTitle className="text-lg font-semibold">Clean up history</DialogTitle>
        <Input
          label="Delete history before"
          type="date"
          value={date}
          max={dateInput(new Date())}
          onChange={(e) => setDate(e.target.value)}
          description="Finished environments with their jobs and logs, events, and failed or deleted template records. Running environments and the templates in use are kept."
        />
        <p className="text-sm">{preview ? describeCounts(preview) : "Counting…"}</p>
        {(error ?? previewQuery.error) && <Banner variant="error" title={error ?? String(previewQuery.error)} />}
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="destructive" loading={busy} disabled={empty} onClick={() => void clean()}>
            Delete
          </Button>
        </div>
      </Dialog>
    </DialogRoot>
  );
}

function SettingsForm({ initial }: { initial: Settings }) {
  const qc = useQueryClient();
  const admin = useAdminAction();
  const [mode, setMode] = useState<Settings["mode"]>(initial.mode);
  const [days, setDays] = useState(String(initial.days));
  const [auditDays, setAuditDays] = useState(String(initial.audit_days));
  const [cleaning, setCleaning] = useState(false);
  const save = () =>
    admin.run("History settings saved", async () => {
      unwrap(await api.PUT("/api/v1/history/settings", { body: { mode, days: Number(days), audit_days: Number(auditDays) } }));
      await qc.invalidateQueries({ queryKey: ["history-settings"] });
    });
  return (
    <div className="flex flex-col gap-4">
      <Radio.Group legend="Cleanup" value={mode} onValueChange={(v) => setMode(v as Settings["mode"])}>
        <Radio.Item label="Automatic" value="automatic" />
        <Radio.Item label="Manual" value="manual" />
      </Radio.Group>
      <p className="text-sm text-kumo-subtle">
        {mode === "automatic"
          ? "Every day, history older than the period below is deleted."
          : "Nothing is deleted until you clean up."}
      </p>
      <div className="grid gap-4 sm:grid-cols-2">
        <Input label="Keep history (days)" type="number" min={1} max={365} value={days} onChange={(e) => setDays(e.target.value)} />
        <Input
          label="Keep audit events (days)"
          type="number"
          min={1}
          max={3650}
          value={auditDays}
          onChange={(e) => setAuditDays(e.target.value)}
          description="Sign-ins and changes made by administrators."
        />
      </div>
      <div className="flex flex-wrap gap-2">
        <Button variant="primary" loading={admin.busy} onClick={save}>
          Save
        </Button>
        <Button variant="secondary" icon={BroomIcon} onClick={() => setCleaning(true)}>
          Clean up now…
        </Button>
      </div>
      {cleaning && <CleanupDialog days={Number(days) || initial.days} onClose={() => setCleaning(false)} />}
    </div>
  );
}

/** How long history is kept, and a way to delete it now. */
export function HistoryCard() {
  const history = useHistorySettings();
  return (
    <section aria-labelledby="history-title">
      <LayerCard>
        <LayerCard.Secondary>
          <h2 id="history-title" className="text-base font-medium">
            History
          </h2>
        </LayerCard.Secondary>
        <LayerCard.Primary className="flex flex-col gap-4 p-4">
          {history.isPending ? <Loading /> : history.error || !history.data ? <ErrorState error={history.error} /> : <SettingsForm initial={history.data} />}
        </LayerCard.Primary>
      </LayerCard>
    </section>
  );
}
