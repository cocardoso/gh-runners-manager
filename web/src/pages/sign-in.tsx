import { useState, type FormEvent, type ReactNode } from "react";
import { Banner, Button, Input, LayerCard, SensitiveInput } from "@cloudflare/kumo";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { setCsrfToken } from "@/api/auth-state";
import { LanguageMenu } from "@/components/language-menu";
import { useT } from "@/i18n";

export const MIN_PASSWORD = 12;

const message = (e: unknown) => (e instanceof Error ? e.message.replace(/^auth: /, "") : String(e));

/** Puts node where placeholder is in a translated text. */
function withNode(text: string, placeholder: string, node: ReactNode): ReactNode {
  const [before, after = ""] = text.split(placeholder);
  return (
    <>
      {before}
      {node}
      {after}
    </>
  );
}

function Centered({ title, description, children }: { title: string; description: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-h-dvh items-center justify-center bg-kumo-canvas p-4">
      <LayerCard className="w-full max-w-sm">
        <LayerCard.Primary className="flex flex-col gap-4 p-6">
          <div className="flex items-center gap-2">
            <img src="/favicon.svg" alt="" className="size-7" />
            <h1 className="flex-1 text-lg font-semibold text-kumo-default">{title}</h1>
            <LanguageMenu />
          </div>
          <p className="text-sm text-kumo-subtle">{description}</p>
          {children}
        </LayerCard.Primary>
      </LayerCard>
    </div>
  );
}

/** The sign-in form; the page that was asked for shows once signed in. */
export function LoginPage() {
  const t = useT();
  const qc = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const s = unwrap(await api.POST("/api/v1/auth/login", { body: { username, password } }));
      setCsrfToken(s.csrf);
      qc.setQueryData(["session"], s);
      void qc.invalidateQueries({ predicate: (q) => q.queryKey[0] !== "session" });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Centered title={t("settings.signIn.title")} description="gh-runners-manager">
      {error && <Banner variant="error" title={error} />}
      <form className="flex flex-col gap-4" onSubmit={submit}>
        <Input label={t("settings.signIn.username")} autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus />
        <Input label={t("settings.signIn.password")} type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        <Button variant="primary" type="submit" loading={busy} disabled={!username || !password}>
          {t("settings.signIn.submit")}
        </Button>
      </form>
    </Centered>
  );
}

/** First run: create the admin account with the setup token the installer printed. */
export function SetupPage() {
  const t = useT();
  const qc = useQueryClient();
  const [token, setToken] = useState("");
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const tooShort = password.length > 0 && password.length < MIN_PASSWORD;
  const mismatch = confirm.length > 0 && confirm !== password;
  const valid = token.trim() !== "" && username.trim() !== "" && password.length >= MIN_PASSWORD && confirm === password;
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      unwrap(await api.POST("/api/v1/auth/setup", { body: { setup_token: token.trim(), username: username.trim(), password } }));
      await qc.invalidateQueries({ queryKey: ["session"] });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Centered
      title={t("settings.setup.title")}
      description={withNode(t("settings.setup.description"), "{file}", <code>setup-token</code>)}
    >
      {error && <Banner variant="error" title={error} />}
      <form className="flex flex-col gap-4" onSubmit={submit}>
        <SensitiveInput label={t("settings.setup.token")} value={token} onValueChange={setToken} autoFocus />
        <Input label={t("settings.setup.username")} autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
        <Input
          label={t("settings.setup.password")}
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          description={t("settings.password.atLeast", { min: MIN_PASSWORD })}
          error={tooShort ? t("settings.password.useAtLeast", { min: MIN_PASSWORD }) : undefined}
        />
        <Input
          label={t("settings.setup.confirm")}
          type="password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          error={mismatch ? t("settings.password.differ") : undefined}
        />
        <Button variant="primary" type="submit" loading={busy} disabled={!valid}>
          {t("settings.setup.submit")}
        </Button>
      </form>
    </Centered>
  );
}
