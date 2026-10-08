import { useId, useRef, useState, type FormEvent } from "react";
import { Badge, Banner, Button, Dialog, DialogRoot, DialogTitle, Input, Link, SensitiveInput, Table, Tooltip } from "@cloudflare/kumo";
import { PlusIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type CredentialView } from "@/api/client";
import { useCredentials } from "@/api/queries";
import { helpField } from "@/components/help-tip";
import { formatNumber } from "@/lib/format";
import { useT } from "@/i18n";
import { ErrorState, Loading } from "./common";

const toneClass = { success: "text-kumo-success", warning: "text-kumo-warning", danger: "text-kumo-danger" } as const;

const message = (e: unknown) => (e instanceof Error ? e.message.replace(/^settings: /, "") : String(e));

/** Adds a credential, or replaces its token: checked against GitHub before it is saved,
 * next to how to create one. */
export function CredentialDialog({ open, name: fixedName, onClose }: { open: boolean; name?: string; onClose: () => void }) {
  const t = useT();
  const id = useId();
  const qc = useQueryClient();
  const [name, setName] = useState(fixedName ?? "");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [check, setCheck] = useState<{ tone: "success" | "warning" | "danger"; text: string }>();
  const [checking, setChecking] = useState(false);
  // The token being checked: an answer for a token changed meanwhile is dropped.
  const checked = useRef("");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      unwrap(await api.PUT("/api/v1/credentials/{name}", { params: { path: { name: name.trim() } }, body: { token: token.trim() } }));
      void qc.invalidateQueries({ queryKey: ["credentials"] });
      void qc.invalidateQueries({ queryKey: ["credential-targets"] });
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };
  const test = async () => {
    const tok = token.trim();
    checked.current = tok;
    setChecking(true);
    setCheck(undefined);
    try {
      const r = unwrap(await api.POST("/api/v1/credentials/check", { body: { token: tok } }));
      if (checked.current !== tok) return;
      const login = r.login ?? "";
      if (!r.ok) setCheck({ tone: "danger", text: r.error ?? "" });
      else if (r.error) setCheck({ tone: "warning", text: t("settings.credentialGuide.listFailed", { login, error: r.error }) });
      else if (r.repositories === 0 && r.organizations === 0) setCheck({ tone: "warning", text: t("settings.credentialGuide.noRepos", { login }) });
      else {
        const works = t("settings.credentialGuide.works", {
          login,
          repos: t("settings.credentialGuide.repos", { count: r.repositories, n: formatNumber(r.repositories) }),
          orgs: t("settings.credentialGuide.orgs", { count: r.organizations, n: formatNumber(r.organizations) }),
        });
        setCheck({ tone: "success", text: r.truncated ? `${works} ${t("settings.credentialGuide.more", { n: formatNumber(1000) })}` : works });
      }
    } catch (err) {
      if (checked.current === tok) setCheck({ tone: "danger", text: message(err) });
    } finally {
      if (checked.current === tok) setChecking(false);
    }
  };
  const guide = (
    <aside className="flex flex-col gap-3 rounded-lg bg-kumo-tint p-4 text-sm">
      <h3 className="font-semibold text-kumo-default">{t("settings.credentialGuide.title")}</h3>
      <ol className="flex list-decimal flex-col gap-2 pl-5 text-kumo-subtle">
        <li>
          {t("settings.credentialGuide.step1")}{" "}
          <Link href="https://github.com/settings/personal-access-tokens/new" target="_blank" rel="noreferrer">
            {t("settings.credentialGuide.create")}
          </Link>
        </li>
        <li>{t("settings.credentialGuide.step2")}</li>
        <li>
          {t("settings.credentialGuide.step3")}
          <ul className="mt-1 flex list-disc flex-col gap-1 pl-4">
            <li>{t("settings.credentialGuide.admin")}</li>
            <li>{t("settings.credentialGuide.actions")}</li>
            <li>{t("settings.credentialGuide.orgRunners")}</li>
          </ul>
        </li>
        <li>{t("settings.credentialGuide.step4")}</li>
      </ol>
    </aside>
  );
  return (
    <DialogRoot open={open} onOpenChange={(o) => !o && onClose()}>
      <Dialog size="xl" className="flex max-h-[90dvh] flex-col overflow-hidden p-0">
        <div className="border-b border-kumo-line px-6 py-4">
          <DialogTitle className="text-lg font-semibold">{fixedName ? t("settings.credentials.replaceTitle", { name: fixedName }) : t("settings.credentials.addTitle")}</DialogTitle>
          <p className="mt-1 text-sm text-kumo-subtle">{t("settings.credentials.help")}</p>
        </div>
        <form className="flex min-h-0 flex-1 flex-col" onSubmit={submit}>
          <div className="grid min-h-0 flex-1 gap-6 overflow-y-auto px-6 py-5 md:grid-cols-2">
            <div className="flex min-w-0 flex-col gap-4">
              {error && <Banner variant="error" title={error} />}
              <Input
                {...helpField(`${id}-name`, t("settings.credentials.name"), t("settings.credentialGuide.nameTip"))}
                value={name}
                onChange={(e) => setName(e.target.value)}
                disabled={!!fixedName}
                description={t("settings.credentials.nameHint")}
              />
              <SensitiveInput
                {...helpField(`${id}-token`, t("settings.credentials.token"), t("settings.credentialGuide.tokenTip"))}
                value={token}
                onValueChange={(v) => {
                  setToken(v);
                  setCheck(undefined);
                  checked.current = "";
                  setChecking(false);
                }}
              />
              <div className="flex flex-wrap items-center gap-3">
                <Button type="button" variant="secondary" loading={checking} disabled={!token.trim()} onClick={() => void test()}>
                  {t("settings.credentialGuide.test")}
                </Button>
                <span role="status" className={check ? `text-sm ${toneClass[check.tone]}` : "sr-only"}>
                  {check?.text}
                </span>
              </div>
            </div>
            {guide}
          </div>
          <div className="flex justify-end gap-2 border-t border-kumo-line px-6 py-4">
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
