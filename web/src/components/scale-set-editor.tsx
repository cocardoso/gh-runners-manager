import { useState, type FormEvent, type ReactNode } from "react";
import { Banner, Button, ClipboardText, Dialog, DialogRoot, DialogTitle, Input, Select } from "@cloudflare/kumo";
import { PlusIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type ScaleSetSettings } from "@/api/client";
import { useCredentials } from "@/api/queries";
import { CredentialDialog } from "@/components/credentials-editor";
import { currentFormatLocale, useT } from "@/i18n";
import { formatMB } from "@/lib/format";

const message = (e: unknown) => (e instanceof Error ? e.message.replace(/^settings: /, "") : String(e));

const numberOr = (v: string, fallback: number) => (v.trim() === "" || Number.isNaN(Number(v)) ? fallback : Math.floor(Number(v)));

// The same rules as the control plane (internal/config).
const NAME = /^[a-z0-9][a-z0-9-]{0,62}$/;
const URL_RE = /^https:\/\/github\.com\/([A-Za-z0-9][A-Za-z0-9-]*)(?:\/([A-Za-z0-9._-]+))?\/?$/;

const MEMORY_MB = [1024, 2048, 4096, 8192, 16384, 32768, 65536];
const KEEP_MINUTES = [0, 15, 60, 240, 1440] as const;

/** A titled group of fields. */
function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <fieldset className="flex flex-col gap-4">
      <legend className="mb-3 text-sm font-semibold text-kumo-default">{title}</legend>
      {children}
    </fieldset>
  );
}

/** Creates a scale set, or edits one created here (name fixed). */
export function ScaleSetDialog({ name: fixedName, initial, onClose }: { name?: string; initial?: ScaleSetSettings | null; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const creds = useCredentials();
  const credNames = (creds.data ?? []).map((c) => c.name);
  const [name, setName] = useState(fixedName ?? "");
  const [url, setUrl] = useState(initial?.url ?? "");
  const [credential, setCredential] = useState(initial?.credential ?? "");
  const [labels, setLabels] = useState((initial?.labels ?? []).join(", "));
  const [group, setGroup] = useState(initial?.runner_group ?? "default");
  const [maxConcurrent, setMaxConcurrent] = useState(String(initial?.max_concurrent ?? 2));
  const [cores, setCores] = useState(String(initial?.cores ?? 2));
  const [memory, setMemory] = useState(String(initial?.memory_mb ?? 4096));
  const [keep, setKeep] = useState(String(initial?.keep_on_failure_minutes ?? 0));
  const [addingCredential, setAddingCredential] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const chosen = credential || credNames[0] || "";

  const nameBad = name.trim() !== "" && !NAME.test(name.trim());
  const urlBad = url.trim() !== "" && !URL_RE.test(url.trim());
  const missing = [!name.trim() && "name", !url.trim() && "url", !chosen && "credential"].filter(Boolean) as ("name" | "url" | "credential")[];
  const wrong = [nameBad && "name", urlBad && "url"].filter(Boolean) as ("name" | "url")[];
  const list = (fields: ("name" | "url" | "credential")[]) =>
    new Intl.ListFormat(currentFormatLocale(), { type: "conjunction" }).format(fields.map((f) => t(`templates.scaleSetForm.field.${f}`)));
  const blocked = wrong.length > 0 ? t("templates.scaleSetForm.fix", { fields: list(wrong) }) : missing.length > 0 ? t("templates.scaleSetForm.fill", { fields: list(missing) }) : "";

  // Values outside the lists (set in an earlier version, or by hand) stay selectable.
  const memoryItems = Object.fromEntries([...new Set([...MEMORY_MB, Number(memory)])].sort((a, b) => a - b).map((mb) => [String(mb), formatMB(mb)]));
  const keepLabel = (m: number) =>
    m === 0 ? t("templates.scaleSetForm.keepOff") : m === 15 ? t("templates.scaleSetForm.m15") : m === 60 ? t("templates.scaleSetForm.h1") : m === 240 ? t("templates.scaleSetForm.h4") : m === 1440 ? t("templates.scaleSetForm.h24") : `${m} min`;
  const keepItems = Object.fromEntries([...new Set<number>([...KEEP_MINUTES, Number(keep)])].sort((a, b) => a - b).map((m) => [String(m), keepLabel(m)]));

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (blocked) return;
    setBusy(true);
    setError(undefined);
    try {
      const body: ScaleSetSettings = {
        url: url.trim(),
        credential: chosen,
        runner_group: group.trim() || "default",
        labels: labels.split(",").map((l) => l.trim()).filter(Boolean),
        max_concurrent: numberOr(maxConcurrent, 2),
        cores: numberOr(cores, 2),
        memory_mb: numberOr(memory, 4096),
        keep_on_failure_minutes: numberOr(keep, 0),
      };
      unwrap(await api.PUT("/api/v1/scale-sets/{name}", { params: { path: { name: name.trim() } }, body }));
      void qc.invalidateQueries({ queryKey: ["scale-sets"] });
      void qc.invalidateQueries({ queryKey: ["credentials"] });
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <DialogRoot open onOpenChange={(o) => !o && !addingCredential && onClose()}>
        {/* Title and buttons stay in view; only the fields scroll. */}
        <Dialog size="xl" className="flex max-h-[90dvh] flex-col overflow-hidden p-0">
          <div className="border-b border-kumo-line px-6 py-4">
            <DialogTitle className="text-lg font-semibold">{fixedName ? t("templates.scaleSets.editor.edit", { name: fixedName }) : t("templates.scaleSets.new")}</DialogTitle>
            <p className="mt-1 text-sm text-kumo-subtle">{t("templates.scaleSets.editor.changes")}</p>
          </div>
          <form className="flex min-h-0 flex-1 flex-col" onSubmit={submit}>
            <div className="flex min-h-0 flex-1 flex-col gap-8 overflow-y-auto px-6 py-5">
              {error && <Banner variant="error" title={error} />}
              <Section title={t("templates.scaleSetForm.essentials")}>
                <div className="grid gap-4 sm:grid-cols-2">
                  <div className="min-w-0">
                    <Input
                      label={t("templates.scaleSets.editor.name")}
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      disabled={!!fixedName}
                      error={nameBad ? t("templates.scaleSetForm.nameInvalid") : undefined}
                      description={nameBad ? undefined : t("templates.scaleSets.editor.nameHelp")}
                    />
                  </div>
                  {/* Kumo's label style and spacing, so it lines up with the name field. */}
                  <div className="flex min-w-0 flex-col gap-1.5">
                    <span className="m-0 text-base font-medium text-kumo-default">{t("templates.scaleSetForm.preview")}</span>
                    <ClipboardText
                      text={`runs-on: ${name.trim() || "<name>"}`}
                      tooltip={{ text: t("shell.copy.short"), copiedText: t("shell.copy.copied") }}
                      labels={{ copyAction: t("shell.copy.action") }}
                    />
                  </div>
                </div>
                <Input
                  label={t("templates.scaleSets.editor.url")}
                  placeholder="https://github.com/owner/repo"
                  value={url}
                  onChange={(e) => setUrl(e.target.value)}
                  error={urlBad ? t("templates.scaleSetForm.urlInvalid") : undefined}
                />
                {credNames.length > 0 ? (
                  <Select
                    label={t("templates.scaleSets.editor.credential")}
                    value={chosen}
                    onValueChange={(v) => setCredential(String(v ?? ""))}
                    items={Object.fromEntries(credNames.map((n) => [n, n]))}
                  />
                ) : (
                  <div className="flex flex-col gap-1.5">
                    <span className="text-sm font-medium text-kumo-default">{t("templates.scaleSets.editor.credential")}</span>
                    <div className="flex flex-wrap items-center gap-3 rounded-lg border border-dashed border-kumo-line px-3 py-2">
                      <span className="text-sm text-kumo-subtle">{t("templates.scaleSetForm.noCredential")}</span>
                      <Button type="button" size="sm" variant="secondary" icon={PlusIcon} onClick={() => setAddingCredential(true)}>
                        {t("templates.scaleSetForm.addCredential")}
                      </Button>
                    </div>
                  </div>
                )}
              </Section>
              <Section title={t("templates.scaleSetForm.resources")}>
                <div className="grid gap-4 sm:grid-cols-3">
                  <div className="min-w-0">
                    <Input
                      label={t("templates.scaleSetForm.maxConcurrent")}
                      type="number"
                      min={1}
                      value={maxConcurrent}
                      onChange={(e) => setMaxConcurrent(e.target.value)}
                      description={t("templates.scaleSetForm.maxConcurrentHelp")}
                    />
                  </div>
                  <div className="min-w-0">
                    <Input
                      label={t("templates.scaleSetForm.cores")}
                      type="number"
                      min={1}
                      value={cores}
                      onChange={(e) => setCores(e.target.value)}
                      description={t("templates.scaleSetForm.coresHelp")}
                    />
                  </div>
                  <div className="min-w-0">
                    <Select
                      label={t("templates.scaleSetForm.memory")}
                      value={memory}
                      onValueChange={(v) => setMemory(String(v ?? memory))}
                      items={memoryItems}
                      className="w-full"
                      description={t("templates.scaleSetForm.memoryHelp")}
                    />
                  </div>
                </div>
                <div className="grid gap-4 sm:grid-cols-3">
                  <div className="min-w-0">
                    <Select
                      label={t("templates.scaleSetForm.keep")}
                      value={keep}
                      onValueChange={(v) => setKeep(String(v ?? keep))}
                      items={keepItems}
                      className="w-full"
                      description={t("templates.scaleSetForm.keepHelp")}
                    />
                  </div>
                  <div className="min-w-0">
                    <Input
                      label={t("templates.scaleSets.editor.labels")}
                      placeholder="linux, big"
                      value={labels}
                      onChange={(e) => setLabels(e.target.value)}
                      description={t("templates.scaleSetForm.labelsHelp")}
                    />
                  </div>
                  <div className="min-w-0">
                    <Input
                      label={t("templates.scaleSets.editor.runnerGroup")}
                      value={group}
                      onChange={(e) => setGroup(e.target.value)}
                      description={t("templates.scaleSetForm.runnerGroupHelp")}
                    />
                  </div>
                </div>
              </Section>
            </div>
            <div className="flex flex-wrap items-center justify-end gap-3 border-t border-kumo-line px-6 py-4">
              {blocked && <span className="mr-auto text-sm text-kumo-subtle">{blocked}</span>}
              <Button variant="secondary" type="button" onClick={onClose}>
                {t("templates.scaleSets.editor.cancel")}
              </Button>
              <Button variant="primary" type="submit" loading={busy} disabled={!!blocked}>
                {t("templates.scaleSets.editor.save")}
              </Button>
            </div>
          </form>
        </Dialog>
      </DialogRoot>
      {addingCredential && <CredentialDialog open onClose={() => setAddingCredential(false)} />}
    </>
  );
}
