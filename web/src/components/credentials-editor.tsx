import { useState, type FormEvent } from "react";
import { Badge, Banner, Button, Dialog, DialogRoot, DialogTitle, Input, SensitiveInput, Table, Tooltip } from "@cloudflare/kumo";
import { PlusIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type CredentialView } from "@/api/client";
import { useCredentials } from "@/api/queries";
import { useT } from "@/i18n";
import { ErrorState, Loading } from "./common";

const message = (e: unknown) => (e instanceof Error ? e.message.replace(/^settings: /, "") : String(e));

export function CredentialDialog({ open, name: fixedName, onClose }: { open: boolean; name?: string; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [name, setName] = useState(fixedName ?? "");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      unwrap(await api.PUT("/api/v1/credentials/{name}", { params: { path: { name: name.trim() } }, body: { token: token.trim() } }));
      void qc.invalidateQueries({ queryKey: ["credentials"] });
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <DialogRoot open={open} onOpenChange={(o) => !o && onClose()}>
      <Dialog size="sm" className="flex flex-col gap-4 p-6">
        <DialogTitle className="text-lg font-semibold">{fixedName ? t("settings.credentials.replaceTitle", { name: fixedName }) : t("settings.credentials.addTitle")}</DialogTitle>
        <p className="text-sm text-kumo-subtle">{t("settings.credentials.help")}</p>
        {error && <Banner variant="error" title={error} />}
        <form className="flex flex-col gap-4" onSubmit={submit}>
          <Input
            label={t("settings.credentials.name")}
            value={name}
            onChange={(e) => setName(e.target.value)}
            disabled={!!fixedName}
            description={t("settings.credentials.nameHint")}
          />
          <SensitiveInput label={t("settings.credentials.token")} value={token} onValueChange={setToken} />
          <div className="flex justify-end gap-2">
            <Button variant="secondary" type="button" onClick={onClose}>
              {t("settings.credentials.cancel")}
            </Button>
            <Button variant="primary" type="submit" loading={busy} disabled={!name.trim() || !token.trim()}>
              {t("settings.credentials.save")}
            </Button>
          </div>
        </form>
      </Dialog>
    </DialogRoot>
  );
}

function DeleteDialog({ name, onClose }: { name?: string; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const remove = async () => {
    if (!name) return;
    setBusy(true);
    setError(undefined);
    try {
      unwrap(await api.DELETE("/api/v1/credentials/{name}", { params: { path: { name } } }));
      void qc.invalidateQueries({ queryKey: ["credentials"] });
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <DialogRoot open={!!name} onOpenChange={(o) => !o && onClose()}>
      <Dialog size="sm" className="flex flex-col gap-4 p-6">
        <DialogTitle className="text-lg font-semibold">{t("settings.credentials.deleteTitle", { name: name ?? "" })}</DialogTitle>
        <p className="text-sm text-kumo-subtle">{t("settings.credentials.deleteBody")}</p>
        {error && <Banner variant="error" title={error} />}
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            {t("settings.credentials.cancel")}
          </Button>
          <Button variant="destructive" loading={busy} onClick={() => void remove()}>
            {t("settings.credentials.deleteConfirm")}
          </Button>
        </div>
      </Dialog>
    </DialogRoot>
  );
}

function TestResult({ result }: { result?: { ok: boolean; text: string } }) {
  if (!result) return null;
  return (
    <Badge variant={result.ok ? "success" : "error"} appearance="dot">
      {result.text}
    </Badge>
  );
}

/** Lists the GitHub credentials; the ones created here can be replaced, tested and deleted. */
export function CredentialsEditor() {
  const t = useT();
  const creds = useCredentials();
  const [editing, setEditing] = useState<{ name?: string } | null>(null);
  const [deleting, setDeleting] = useState<string>();
  const [tests, setTests] = useState<Record<string, { ok: boolean; text: string }>>({});
  const test = async (c: CredentialView) => {
    try {
      const r = unwrap(await api.POST("/api/v1/credentials/{name}/test", { params: { path: { name: c.name } } }));
      const result = r.ok ? { ok: true, text: t("settings.credentials.works", { login: r.login ?? "" }) } : { ok: false, text: t("settings.credentials.failed", { error: r.error ?? "" }) };
      setTests((all) => ({ ...all, [c.name]: result }));
    } catch (err) {
      setTests((all) => ({ ...all, [c.name]: { ok: false, text: t("settings.credentials.failed", { error: message(err) }) } }));
    }
  };
  if (creds.isLoading) return <Loading />;
  if (creds.error) return <ErrorState error={creds.error} />;
  return (
    <div className="flex flex-col gap-3 p-4">
      <div>
        <Button variant="secondary" icon={PlusIcon} onClick={() => setEditing({})}>
          {t("settings.credentials.add")}
        </Button>
      </div>
      <Table aria-label={t("settings.sections.credentials")}>
        <Table.Header>
          <Table.Row>
            <Table.Head>{t("settings.credentials.name")}</Table.Head>
            <Table.Head>{t("settings.credentials.source")}</Table.Head>
            <Table.Head>{t("settings.credentials.usedBy")}</Table.Head>
            <Table.Head>{t("settings.credentials.token")}</Table.Head>
            <Table.Head>{t("settings.credentials.actions")}</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {(creds.data ?? []).map((c) => {
            const file = c.source === "file";
            const usedBy = c.used_by ?? [];
            const inUse = usedBy.length > 0;
            const del = (
              <Button variant="ghost" size="sm" aria-label={t("settings.credentials.deleteName", { name: c.name })} disabled={file || inUse} onClick={() => setDeleting(c.name)}>
                {t("settings.credentials.delete")}
              </Button>
            );
            return (
              <Table.Row key={c.name}>
                <Table.Cell className="font-medium">{c.name}</Table.Cell>
                <Table.Cell>
                  <Badge variant="outline">{file ? "ghrm.yaml" : t("settings.credentials.sourceUi")}</Badge>
                </Table.Cell>
                <Table.Cell>{usedBy.length ? usedBy.join(", ") : "—"}</Table.Cell>
                <Table.Cell className="font-mono text-sm">{c.token_hint ? `…${c.token_hint}` : "—"}</Table.Cell>
                <Table.Cell>
                  <span className="flex flex-wrap items-center gap-1">
                    <Button variant="ghost" size="sm" aria-label={t("settings.credentials.testName", { name: c.name })} onClick={() => void test(c)}>
                      {t("settings.credentials.test")}
                    </Button>
                    {!file && (
                      <Button variant="ghost" size="sm" aria-label={t("settings.credentials.replaceName", { name: c.name })} onClick={() => setEditing({ name: c.name })}>
                        {t("settings.credentials.replace")}
                      </Button>
                    )}
                    {file || inUse ? (
                      <Tooltip content={file ? t("settings.credentials.definedInFile") : t("settings.credentials.usedByList", { names: usedBy.join(", ") })} render={<span>{del}</span>} />
                    ) : (
                      del
                    )}
                    <TestResult result={tests[c.name]} />
                  </span>
                </Table.Cell>
              </Table.Row>
            );
          })}
        </Table.Body>
      </Table>
      {editing && <CredentialDialog open name={editing.name} onClose={() => setEditing(null)} />}
      <DeleteDialog name={deleting} onClose={() => setDeleting(undefined)} />
    </div>
  );
}
