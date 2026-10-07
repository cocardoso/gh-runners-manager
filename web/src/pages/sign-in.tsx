import { useState, type FormEvent, type ReactNode } from "react";
import { Banner, Button, Input, LayerCard, SensitiveInput } from "@cloudflare/kumo";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { setCsrfToken } from "@/api/auth-state";

export const MIN_PASSWORD = 12;

const message = (e: unknown) => (e instanceof Error ? e.message.replace(/^auth: /, "") : String(e));

function Centered({ title, description, children }: { title: string; description: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-h-dvh items-center justify-center bg-kumo-canvas p-4">
      <LayerCard className="w-full max-w-sm">
        <LayerCard.Primary className="flex flex-col gap-4 p-6">
          <div className="flex items-center gap-2">
            <img src="/favicon.svg" alt="" className="size-7" />
            <h1 className="text-lg font-semibold text-kumo-default">{title}</h1>
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
    <Centered title="Sign in" description="gh-runners-manager">
      {error && <Banner variant="error" title={error} />}
      <form className="flex flex-col gap-4" onSubmit={submit}>
        <Input label="Username" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus />
        <Input label="Password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        <Button variant="primary" type="submit" loading={busy} disabled={!username || !password}>
          Sign in
        </Button>
      </form>
    </Centered>
  );
}

/** First run: create the admin account with the setup token the installer printed. */
export function SetupPage() {
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
      title="Create the admin account"
      description={
        <>
          The setup token is in <code>setup-token</code> in the control plane's data directory; the installer and the service log print it.
        </>
      }
    >
      {error && <Banner variant="error" title={error} />}
      <form className="flex flex-col gap-4" onSubmit={submit}>
        <SensitiveInput label="Setup token" value={token} onValueChange={setToken} autoFocus />
        <Input label="Username" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
        <Input
          label="Password"
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          description={`At least ${MIN_PASSWORD} characters.`}
          error={tooShort ? `Use at least ${MIN_PASSWORD} characters.` : undefined}
        />
        <Input
          label="Confirm password"
          type="password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          error={mismatch ? "The passwords differ." : undefined}
        />
        <Button variant="primary" type="submit" loading={busy} disabled={!valid}>
          Create account
        </Button>
      </form>
    </Centered>
  );
}
