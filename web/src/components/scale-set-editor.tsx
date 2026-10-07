import { useState, type FormEvent } from "react";
import { Banner, Button, Dialog, DialogRoot, DialogTitle, Input, Select } from "@cloudflare/kumo";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type ScaleSetSettings } from "@/api/client";
import { useCredentials } from "@/api/queries";

const message = (e: unknown) => (e instanceof Error ? e.message.replace(/^settings: /, "") : String(e));

const numberOr = (v: string, fallback: number) => (v.trim() === "" || Number.isNaN(Number(v)) ? fallback : Math.floor(Number(v)));

/** Creates a scale set, or edits one created here (name fixed). */
export function ScaleSetDialog({ name: fixedName, initial, onClose }: { name?: string; initial?: ScaleSetSettings | null; onClose: () => void }) {
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
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const chosen = credential || credNames[0] || "";
  const submit = async (e: FormEvent) => {
    e.preventDefault();
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
    <DialogRoot open onOpenChange={(o) => !o && onClose()}>
      <Dialog className="flex max-h-[90dvh] flex-col gap-4 overflow-y-auto p-6">
        <DialogTitle className="text-lg font-semibold">{fixedName ? `Edit ${fixedName}` : "New scale set"}</DialogTitle>
        <p className="text-sm text-kumo-subtle">
          Jobs use it with <code>runs-on: &lt;name&gt;</code>. Changes apply to new environments; running ones keep their settings.
        </p>
        {error && <Banner variant="error" title={error} />}
        <form className="flex flex-col gap-4" onSubmit={submit}>
          <Input label="Name" value={name} onChange={(e) => setName(e.target.value)} disabled={!!fixedName} description="Lower-case letters, digits and dashes." />
          <Input
            label="Repository or organization URL"
            placeholder="https://github.com/owner/repo"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
          />
          <Select
            label="Credential"
            value={chosen}
            onValueChange={(v) => setCredential(String(v ?? ""))}
            items={Object.fromEntries(credNames.map((n) => [n, n]))}
            placeholder="Add a credential in Settings first"
          />
          <Input label="Extra labels" placeholder="linux, big" value={labels} onChange={(e) => setLabels(e.target.value)} description="Comma-separated; the name is always a label." />
          <Input label="Runner group" value={group} onChange={(e) => setGroup(e.target.value)} />
          <div className="grid grid-cols-2 gap-4">
            <Input label="Max concurrent" type="number" min={1} value={maxConcurrent} onChange={(e) => setMaxConcurrent(e.target.value)} />
            <Input label="Cores" type="number" min={1} value={cores} onChange={(e) => setCores(e.target.value)} />
            <Input label="Memory (MB)" type="number" min={256} step={256} value={memory} onChange={(e) => setMemory(e.target.value)} />
            <Input
              label="Keep failed environments (minutes)"
              type="number"
              min={0}
              value={keep}
              onChange={(e) => setKeep(e.target.value)}
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="secondary" type="button" onClick={onClose}>
              Cancel
            </Button>
            <Button variant="primary" type="submit" loading={busy} disabled={!name.trim() || !url.trim() || !chosen}>
              Save scale set
            </Button>
          </div>
        </form>
      </Dialog>
    </DialogRoot>
  );
}
