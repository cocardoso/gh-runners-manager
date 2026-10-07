import { useState, type FormEvent } from "react";
import { Badge, Banner, Button, Dialog, DialogRoot, DialogTitle, Input, SensitiveInput, Table, Tooltip } from "@cloudflare/kumo";
import { PlusIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type CredentialView } from "@/api/client";
import { useCredentials } from "@/api/queries";
import { ErrorState, Loading } from "./common";

const message = (e: unknown) => (e instanceof Error ? e.message.replace(/^settings: /, "") : String(e));

function CredentialDialog({ open, name: fixedName, onClose }: { open: boolean; name?: string; onClose: () => void }) {
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
        <DialogTitle className="text-lg font-semibold">{fixedName ? `Replace the token of ${fixedName}` : "Add a GitHub credential"}</DialogTitle>
        <p className="text-sm text-kumo-subtle">
          A fine-grained personal access token with Administration: read and write on the repositories (Actions: read shows job steps). It is stored
          encrypted and never shown again.
        </p>
        {error && <Banner variant="error" title={error} />}
        <form className="flex flex-col gap-4" onSubmit={submit}>
          <Input label="Name" value={name} onChange={(e) => setName(e.target.value)} disabled={!!fixedName} description="Lower-case letters, digits and dashes." />
          <SensitiveInput label="Token" value={token} onValueChange={setToken} />
          <div className="flex justify-end gap-2">
            <Button variant="secondary" type="button" onClick={onClose}>
              Cancel
            </Button>
            <Button variant="primary" type="submit" loading={busy} disabled={!name.trim() || !token.trim()}>
              Save credential
            </Button>
          </div>
        </form>
      </Dialog>
    </DialogRoot>
  );
}

function DeleteDialog({ name, onClose }: { name?: string; onClose: () => void }) {
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
        <DialogTitle className="text-lg font-semibold">Delete {name}?</DialogTitle>
        <p className="text-sm text-kumo-subtle">The token is removed from the control plane. Revoke it on GitHub too if nothing else uses it.</p>
        {error && <Banner variant="error" title={error} />}
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="destructive" loading={busy} onClick={() => void remove()}>
            Delete credential
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
  const creds = useCredentials();
  const [editing, setEditing] = useState<{ name?: string } | null>(null);
  const [deleting, setDeleting] = useState<string>();
  const [tests, setTests] = useState<Record<string, { ok: boolean; text: string }>>({});
  const test = async (c: CredentialView) => {
    try {
      const r = unwrap(await api.POST("/api/v1/credentials/{name}/test", { params: { path: { name: c.name } } }));
      setTests((t) => ({ ...t, [c.name]: r.ok ? { ok: true, text: `Works: signed in as ${r.login}` } : { ok: false, text: `Failed: ${r.error}` } }));
    } catch (err) {
      setTests((t) => ({ ...t, [c.name]: { ok: false, text: `Failed: ${message(err)}` } }));
    }
  };
  if (creds.isLoading) return <Loading />;
  if (creds.error) return <ErrorState error={creds.error} />;
  return (
    <div className="flex flex-col gap-3 p-4">
      <div>
        <Button variant="secondary" icon={PlusIcon} onClick={() => setEditing({})}>
          Add credential
        </Button>
      </div>
      <Table aria-label="GitHub credentials">
        <Table.Header>
          <Table.Row>
            <Table.Head>Name</Table.Head>
            <Table.Head>Source</Table.Head>
            <Table.Head>Used by</Table.Head>
            <Table.Head>Token</Table.Head>
            <Table.Head>Actions</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {(creds.data ?? []).map((c) => {
            const file = c.source === "file";
            const usedBy = c.used_by ?? [];
            const inUse = usedBy.length > 0;
            const del = (
              <Button variant="ghost" size="sm" aria-label={`Delete ${c.name}`} disabled={file || inUse} onClick={() => setDeleting(c.name)}>
                Delete
              </Button>
            );
            return (
              <Table.Row key={c.name}>
                <Table.Cell className="font-medium">{c.name}</Table.Cell>
                <Table.Cell>
                  <Badge variant="outline">{file ? "ghrm.yaml" : "UI"}</Badge>
                </Table.Cell>
                <Table.Cell>{usedBy.length ? usedBy.join(", ") : "—"}</Table.Cell>
                <Table.Cell className="font-mono text-sm">{c.token_hint ? `…${c.token_hint}` : "—"}</Table.Cell>
                <Table.Cell>
                  <span className="flex flex-wrap items-center gap-1">
                    <Button variant="ghost" size="sm" aria-label={`Test ${c.name}`} onClick={() => void test(c)}>
                      Test
                    </Button>
                    {!file && (
                      <Button variant="ghost" size="sm" aria-label={`Replace ${c.name}`} onClick={() => setEditing({ name: c.name })}>
                        Replace
                      </Button>
                    )}
                    {file || inUse ? (
                      <Tooltip content={file ? "Defined in ghrm.yaml" : `Used by ${usedBy.join(", ")}`} render={<span>{del}</span>} />
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
