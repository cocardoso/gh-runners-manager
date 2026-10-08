import { useState, type FormEvent } from "react";
import { Banner, Button, Input, LayerCard } from "@cloudflare/kumo";
import { SignOutIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { setCsrfToken } from "@/api/auth-state";
import { useSession } from "@/api/queries";
import { Page } from "@/components/common";
import { useT } from "@/i18n";
import { MIN_PASSWORD } from "./sign-in";

const message = (e: unknown) => (e instanceof Error ? e.message.replace(/^auth: /, "") : String(e));

/** Signs out of this browser. */
export function useSignOut() {
  const qc = useQueryClient();
  return async () => {
    try {
      await api.POST("/api/v1/auth/logout");
    } finally {
      setCsrfToken(undefined);
      qc.setQueryData(["session"], { state: "signed_out" });
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "session" });
    }
  };
}

export function AccountPage() {
  const t = useT();
  const session = useSession();
  const signOut = useSignOut();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; text: string }>();
  const valid = current !== "" && next.length >= MIN_PASSWORD && confirm === next;
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setResult(undefined);
    try {
      unwrap(await api.POST("/api/v1/auth/password", { body: { current, next } }));
      setResult({ ok: true, text: t("settings.account.changed") });
      setCurrent("");
      setNext("");
      setConfirm("");
    } catch (err) {
      setResult({ ok: false, text: message(err) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Page
      title={t("settings.account.title")}
      description={t("settings.account.signedInAs", { username: session.data?.username ?? "…" })}
      actions={
        <Button variant="secondary" icon={SignOutIcon} onClick={() => void signOut()}>
          {t("settings.account.signOut")}
        </Button>
      }
    >
      <LayerCard className="max-w-lg">
        <LayerCard.Secondary>{t("settings.account.changePassword")}</LayerCard.Secondary>
        <LayerCard.Primary className="flex flex-col gap-4 p-4">
          {result && <Banner variant={result.ok ? "default" : "error"} title={result.text} />}
          <p className="text-sm text-kumo-subtle">{t("settings.account.othersSignedOut")}</p>
          <form className="flex flex-col gap-4" onSubmit={submit}>
            <Input label={t("settings.account.current")} type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
            <Input
              label={t("settings.account.next")}
              type="password"
              autoComplete="new-password"
              value={next}
              onChange={(e) => setNext(e.target.value)}
              description={t("settings.password.atLeast", { min: MIN_PASSWORD })}
            />
            <Input
              label={t("settings.account.confirm")}
              type="password"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              error={confirm.length > 0 && confirm !== next ? t("settings.password.differ") : undefined}
            />
            <div>
              <Button variant="primary" type="submit" loading={busy} disabled={!valid}>
                {t("settings.account.changePassword")}
              </Button>
            </div>
          </form>
        </LayerCard.Primary>
      </LayerCard>
    </Page>
  );
}
