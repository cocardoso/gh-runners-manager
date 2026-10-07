# M5 — Sign-in, Secrets, Editable Settings, Installer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make ghrm safe and convenient to run for real: a signed-in web UI (first-run admin account), secrets encrypted at rest, GitHub credentials and scale sets editable from the UI without a restart, Prometheus metrics, daily backups, a Docker image, and an idempotent `install.sh` for the Proxmox host.

**Architecture:** A new `internal/secrets` package seals values with AES-256-GCM under a key kept in a separate `0600` file; `internal/auth` owns the admin account (argon2id), sessions (random IDs stored hashed) and CSRF tokens. The API gains one authentication middleware in front of every `/api/v1` route: a session cookie (plus a CSRF header on state-changing requests) or the existing admin bearer token for scripts. A `settings.Registry` merges the config file's scale sets and credentials (read-only) with the ones created in the UI (stored in SQLite, secrets sealed), and notifies the controller, the GitHub client and a listener supervisor when they change. `deploy/proxmox/install.sh` replaces the hand-made development setup.

**Tech Stack:** Go 1.27, huma v2, SQLite (modernc) + goose, `golang.org/x/crypto/argon2`, `github.com/prometheus/client_golang`, React + Kumo + TanStack Router/Query, vitest, Playwright, bash (installer) with a fake `pvesh`/`pveum`/`pct` test harness.

**Spec:** `docs/superpowers/specs/2026-10-07-gh-runners-manager-design.md` (§10.4 GitHub credentials, §10.5 agent channel, §10.6 web UI access, §12 deployment and operations).

## Global Constraints

- Everything in the repository is in English: code, comments, docs, commit messages. Commits carry no AI attribution.
- The repository is public: no real homelab addresses, repository names or secrets in docs or tests; use example values (`10.0.0.0/24`, `example/repo`).
- Passwords are hashed with argon2id; sessions use HttpOnly cookies, `SameSite=Strict`, and `Secure` whenever the request arrived over HTTPS (directly or via `X-Forwarded-Proto: https`); state-changing requests are CSRF-protected (spec §10.6).
- Secrets are encrypted at rest with a key stored in a separate `0600` file outside the database (spec §10.4).
- Every administrative action is recorded as an `audit.*` event with the actor (spec §10.6).
- Agent TLS pinning (spec §10.5) already exists since M2 (`ingest.LoadOrCreateCert`, `agent.NewClient`); M5 only documents it and keeps its tests green.
- The admin bearer token (`admin_token_file`) stays as the API credential for scripts; it is never required by the UI.
- `docs/architecture.md` diagrams are updated in the same change set as the architecture they describe (component table, request flow, auth flow).
- Database changes are new goose migrations (`internal/store/migrations/0003_*.sql`); existing migrations are never edited (0002 shipped in M4).
- Times are stored as Unix milliseconds via `ms()`/`fromMs()`; store errors are `store.ErrNotFound` / `store.ErrConflict`.

## Review Focus

1. **A second browser racing the first-run setup on the LAN.** Expected: setup needs the one-time setup token that the installer prints (and the log shows); without it, or after an account exists, setup answers 403/409 and nothing changes. → Task 3 test `TestSetupNeedsTheSetupTokenAndRunsOnce`.
2. **The secret key file is lost or replaced while sealed secrets exist.** Expected: the control plane refuses to start with a clear error naming the key file, instead of silently treating credentials as missing. → Task 1 test `TestOpenWithAWrongKeyFailsLoudly`.
3. **Editing a scale set while jobs run on it.** Expected: running environments finish undisturbed; the listener restarts with the new settings; a removed scale set stops getting new environments but its live ones drain. → Task 6 test `TestRemovedScaleSetDrains`.
4. **Plain-HTTP LAN access (no TLS in front).** Expected: sign-in still works over `http://` (cookie without `Secure`), and gets `Secure` behind an HTTPS proxy. → Task 4 test `TestSessionCookieSecureOnlyOverHTTPS`.
5. **Running `install.sh` twice, or on a host where parts already exist (an M2-style hand-made setup).** Expected: the second run changes nothing and reports each step as present; existing users/roles/pools/zones are reused, never recreated or destroyed. → Task 11 test `install_is_idempotent`.

---

## File structure

| Path | Responsibility |
|---|---|
| `internal/secrets/secrets.go` | Key file creation/loading, `Box.Seal/Open`, key check value |
| `internal/store/migrations/0003_auth_settings.sql` | `secrets`, `users`, `sessions`, `credentials`, `scale_set_configs`, `meta` tables |
| `internal/store/secrets.go`, `auth.go`, `settings.go` | Store methods for the new tables |
| `internal/auth/` | argon2id hashing, account, sessions, CSRF, login throttling, setup token |
| `internal/api/auth.go` | Auth endpoints and the middleware |
| `internal/api/settings_edit.go` | Credential and scale set endpoints |
| `internal/settings/registry.go` | Effective credentials and scale sets (file + UI), change notifications |
| `internal/controller/scalesets.go` | `UpdateScaleSets` (add, change, drain) |
| `cmd/ghrm/supervisor.go` | Starts/stops/restarts scale set listeners on registry changes |
| `cmd/ghrm/secret.go` | `ghrm secret set|delete|list` |
| `internal/metrics/metrics.go` | Prometheus collectors read from the store and controller |
| `internal/backup/backup.go` | Daily `VACUUM INTO`, retention |
| `web/src/pages/login.tsx`, `setup.tsx`, `account.tsx` | Sign-in, first-run setup, change password |
| `web/src/lib/session.ts` | Session query, CSRF header injection, 401 redirect |
| `web/src/components/credentials-editor.tsx`, `scale-set-editor.tsx` | Settings editors |
| `Dockerfile`, `deploy/docker/compose.yaml` | Container image and compose file |
| `deploy/proxmox/install.sh`, `deploy/proxmox/test/` | Installer and its fake-CLI tests |
| `.github/workflows/release.yml` | Release binaries on tags (the installer downloads them) |

---

### Task 1: Secrets box and the secrets table

**Files:**
- Create: `internal/secrets/secrets.go`, `internal/secrets/secrets_test.go`
- Create: `internal/store/migrations/0003_auth_settings.sql` (all M5 tables at once)
- Create: `internal/store/secrets.go`, test in `internal/store/store_test.go`
- Create: `cmd/ghrm/secret.go`; modify `cmd/ghrm/main.go` (subcommand)

**Interfaces:**
- Produces: `secrets.LoadOrCreateKey(path string) ([]byte, error)`; `secrets.New(key []byte) (*Box, error)`; `(*Box).Seal(name string, plain []byte) ([]byte, error)`; `(*Box).Open(name string, sealed []byte) ([]byte, error)`; `(*Box).Check() []byte` (sealed known value, stored once in `meta` as `key_check`).
- Produces: `(*Store).PutSecret(ctx, name string, sealed []byte) error`, `GetSecret(ctx, name) ([]byte, error)` (ErrNotFound), `DeleteSecret(ctx, name) error`, `ListSecretNames(ctx) ([]string, error)`, `GetMeta(ctx, key) (string, error)`, `PutMeta(ctx, key, value string) error`.
- Produces: `secrets.Vault{Box, Store}` with `Get(ctx, name) (string, bool, error)`, `Set(ctx, name, value string) error`, `Delete(ctx, name) error`, and `secrets.OpenVault(ctx, store, keyPath) (*Vault, error)` which verifies or records the key check value.

Migration `0003_auth_settings.sql`:

```sql
-- +goose Up
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE secrets (name TEXT PRIMARY KEY, sealed BLOB NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE users (
  id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL, password_changed_at INTEGER NOT NULL);
CREATE TABLE sessions (
  id_hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf TEXT NOT NULL, created_at INTEGER NOT NULL, last_seen_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '', remote_addr TEXT NOT NULL DEFAULT '');
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE credentials (name TEXT PRIMARY KEY, kind TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE scale_set_configs (name TEXT PRIMARY KEY, spec TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
-- +goose Down
DROP TABLE scale_set_configs; DROP TABLE credentials; DROP TABLE sessions; DROP TABLE users; DROP TABLE secrets; DROP TABLE meta;
```

- [ ] **Step 1: Failing tests** — `internal/secrets/secrets_test.go`:

```go
func TestSealOpenRoundTripBindsTheName(t *testing.T) {
	key, err := secrets.LoadOrCreateKey(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil { t.Fatal(err) }
	b, _ := secrets.New(key)
	sealed, err := b.Seal("github/home", []byte("ghp_x"))
	if err != nil || bytes.Contains(sealed, []byte("ghp_x")) { t.Fatalf("sealed %q, %v", sealed, err) }
	if got, err := b.Open("github/home", sealed); err != nil || string(got) != "ghp_x" { t.Fatalf("open = %q, %v", got, err) }
	if _, err := b.Open("github/other", sealed); err == nil { t.Fatal("a value sealed for one name must not open under another") }
}

func TestKeyFileIsCreatedOnceWith0600(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret.key")
	k1, _ := secrets.LoadOrCreateKey(p)
	k2, _ := secrets.LoadOrCreateKey(p)
	st, _ := os.Stat(p)
	if !bytes.Equal(k1, k2) || len(k1) != 32 || st.Mode().Perm() != 0o600 { t.Fatalf("key %d bytes, same %v, mode %v", len(k1), bytes.Equal(k1, k2), st.Mode()) }
}

func TestKeyFileWithLoosePermissionsIsRefused(t *testing.T) { /* chmod 0644 → LoadOrCreateKey error mentions "0600" */ }

func TestOpenWithAWrongKeyFailsLoudly(t *testing.T) {
	ctx := context.Background()
	db := storetest.Open(t) // existing helper or store.Open(tempfile)
	dir := t.TempDir()
	v, err := secrets.OpenVault(ctx, db, filepath.Join(dir, "a.key"))
	if err != nil { t.Fatal(err) }
	_ = v.Set(ctx, "github/home", "ghp_x")
	_, err = secrets.OpenVault(ctx, db, filepath.Join(dir, "b.key")) // a fresh, different key
	if err == nil || !strings.Contains(err.Error(), "b.key") { t.Fatalf("err = %v, want a refusal naming the key file", err) }
}
```

Also `internal/store/store_test.go`: `TestSecretsAndMeta` (put/get/list/delete, ErrNotFound).

- [ ] **Step 2: Run** `go test ./internal/secrets/ ./internal/store/` — FAIL (package missing).
- [ ] **Step 3: Implement.** AES-256-GCM with a random 12-byte nonce prefixed to the ciphertext, the secret name as additional data. `LoadOrCreateKey`: if missing, write 32 random bytes with `os.OpenFile(p, O_CREATE|O_EXCL|O_WRONLY, 0o600)`; if present, refuse when `perm&0o077 != 0`, and when the length is not 32. `OpenVault`: `meta.key_check` missing → store `base64(Seal("ghrm/key-check", "ok"))`; present → it must open, else `fmt.Errorf("secrets: %s does not match the key that sealed this database's secrets", keyPath)`.
- [ ] **Step 4:** `ghrm secret set <name>` (value from stdin, trailing newline trimmed), `ghrm secret delete <name>`, `ghrm secret list` (names only), all with `--config`; they open the store and vault from the config's `data_dir` and `secret_key_file` (default `<data_dir>/secret.key`). Test in `cmd/ghrm/secret_test.go` (set via stdin then list shows the name, value never printed).
- [ ] **Step 5:** `go test -race ./...` PASS; commit `feat(secrets): encrypted secret store with a separate key file`.

### Task 2: Configuration resolves secrets from the vault

**Files:** Modify `internal/config/config.go` (add `secret_key_file`; credentials and the Proxmox secret may come from the vault), `cmd/ghrm/serve.go`; tests in `internal/config/config_test.go`.

**Interfaces:**
- Consumes: `secrets.Vault.Get`.
- Produces: `(*Config).ResolveVaultSecrets(ctx, get func(ctx, name string) (string, bool, error)) error` — fills `Proxmox.TokenSecret` from `proxmox/token-secret` and each `Credential.Token` from `github/<name>` when neither the env var nor the file supplied them. Precedence: env var > file > vault.
- `Load` no longer fails when `token_secret_file` is empty; `ValidateServe` (called after `ResolveVaultSecrets`) reports a missing Proxmox secret as `proxmox token secret: set proxmox.token_secret_file, GHRM_PROXMOX_TOKEN_SECRET or "ghrm secret set proxmox/token-secret"`.
- `SecretKeyFile` default `<data_dir>/secret.key`.

- [ ] Failing tests: `TestVaultSuppliesMissingSecrets`, `TestEnvAndFileWinOverTheVault`, `TestMissingProxmoxSecretNamesAllSources`.
- [ ] Implement; serve opens the store, `OpenVault`, `ResolveVaultSecrets`, then `ValidateServe`.
- [ ] Run suite; commit `feat(config): read the Proxmox and GitHub secrets from the vault`.

### Task 3: Account, sessions and CSRF (`internal/auth`)

**Files:** Create `internal/auth/{password.go,auth.go,throttle.go}` + tests; `internal/store/auth.go` + tests.

**Interfaces:**
- Produces: `auth.HashPassword(pw string) (string, error)` (argon2id, `m=64MiB,t=3,p=2`, 16-byte salt, PHC string `$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>`); `auth.VerifyPassword(encoded, pw string) (bool, error)`.
- Produces: `auth.Service{Store, Now, SessionTTL (default 7 days, sliding), SetupToken string}` with
  - `NeedsSetup(ctx) (bool, error)`
  - `Setup(ctx, setupToken, username, password string) (User, error)` — `ErrSetupDone` once a user exists, `ErrBadSetupToken` on mismatch (constant-time), `ErrWeakPassword` under 12 characters.
  - `Login(ctx, username, password, remoteAddr, userAgent string) (Session, string /*cookie value*/, error)` — `ErrBadCredentials` for unknown user or wrong password (same timing: verify against a dummy hash), `ErrThrottled` after 5 failures per remote address within 15 minutes (doubling lockout, in memory).
  - `Authenticate(ctx, cookieValue string) (Session, User, error)` — looks up `sha256(cookie)`, refreshes `last_seen_at`/`expires_at` at most once a minute, `ErrNoSession` if missing/expired.
  - `Logout(ctx, cookieValue string) error`; `ChangePassword(ctx, userID, current, next string) error` (invalidates the user's other sessions).
  - `Session{ID string (hash), UserID, CSRF string, ExpiresAt time.Time}`; `User{ID, Username}`.
- Produces: `auth.LoadOrCreateSetupToken(dataDir string) (string, error)` — `<data_dir>/setup-token` (0600), removed by `Setup` on success.
- Store: `CreateUser`, `GetUserByName`, `GetUser`, `CountUsers`, `UpdatePassword`, `CreateSession`, `GetSession`, `TouchSession`, `DeleteSession`, `DeleteUserSessions(ctx, userID, except string)`, `DeleteExpiredSessions(ctx, now)`.

- [ ] Failing tests (`internal/auth/auth_test.go`): `TestPasswordHashIsArgon2idAndVerifies`, `TestSetupNeedsTheSetupTokenAndRunsOnce`, `TestLoginCreatesASessionAndLogoutEndsIt`, `TestUnknownUserAndWrongPasswordLookAlike`, `TestLoginThrottlesRepeatedFailures`, `TestSessionsExpireAndSlide`, `TestChangePasswordEndsOtherSessions`.
- [ ] Implement; add `golang.org/x/crypto` to go.mod.
- [ ] Commit `feat(auth): admin account, sessions and CSRF tokens`.

### Task 4: API authentication, audit and first-run endpoints

**Files:** Create `internal/api/auth.go`, `internal/api/auth_test.go`; modify `internal/api/api.go` (Deps gains `Auth *auth.Service`; middleware), `internal/api/templates.go` and `api.go` (drop per-operation `Authorization` inputs, use the middleware's actor), `cmd/ghrm/serve.go`, `cmd/ghrm/demo.go`.

**Interfaces:**
- Endpoints (huma, tag `auth`): `GET /api/v1/auth/session` → `{state: "setup"|"signed_out"|"signed_in", username?, csrf?}` (always public); `POST /api/v1/auth/setup {setup_token, username, password}`; `POST /api/v1/auth/login {username, password}`; `POST /api/v1/auth/logout`; `POST /api/v1/auth/password {current, next}`.
- Middleware `requireAuth(d Deps, next http.Handler) http.Handler` wraps the mux. Public: `/healthz`, `/readyz`, `/metrics`, `/api/openapi*`, `/api/docs`, `GET /api/v1/auth/session`, `POST /api/v1/auth/{setup,login}`, and everything outside `/api/` (the SPA). Every other `/api/` request needs either `Authorization: Bearer <admin token>` (actor `token`) or a valid `ghrm_session` cookie (actor = username); with a cookie, non-GET/HEAD requests also need `X-CSRF-Token` equal to the session's CSRF value (403 otherwise). The actor travels in the request context: `api.Actor(ctx) string`.
- Cookie: `ghrm_session`, `Path=/`, `HttpOnly`, `SameSite=Strict`, `Secure` iff `r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"`, `Max-Age` = TTL.
- Audit: helper `audit(ctx, d, kind, msg string, data map[string]any)` → `d.Recorder.Warn/Info(ctx, "audit."+kind, msg, refs, data+{"actor": Actor(ctx)})`. Kinds: `setup`, `login`, `login_failed`, `logout`, `password`, `destroy`, `template_build`, `template_activate`, `template_pin`, `template_unpin`, `credential_put`, `credential_delete`, `scale_set_put`, `scale_set_delete`.
- Demo mode creates user `admin` with password `demo-password` at startup (logged) so e2e can sign in; the admin token stays `demo`.

- [ ] Failing tests: `TestAPIRequiresSignIn` (GET `/api/v1/environments` → 401 without cookie/token; 200 with each), `TestCSRFRequiredForCookieWrites` (POST destroy with cookie but no header → 403; with header → 200/404), `TestBearerTokenNeedsNoCSRF`, `TestSessionCookieSecureOnlyOverHTTPS`, `TestSetupLoginLogoutFlow`, `TestAdminActionsAreAuditedWithTheActor` (destroy and template build record `audit.*` with `actor`), `TestSSERequiresSignIn`.
- [ ] Implement; update existing API tests that pass `Authorization` (they keep working through the bearer path).
- [ ] `make web-api` to regenerate `web/openapi.json` and `schema.d.ts`.
- [ ] Commit `feat(api): sign-in, CSRF and audited admin actions`.

### Task 5: Settings registry and editable GitHub credentials

**Files:** Create `internal/settings/registry.go` + test; `internal/store/settings.go` + test; `internal/api/settings_edit.go` + test; modify `internal/github/github.go`, `jobs.go` (read through the registry; cache clients keyed by scale set name + URL + token digest), `internal/api/overview.go` (settings show sources).

**Interfaces:**
- Produces: `settings.Registry` with
  - `New(cfg *config.Config, store *store.Store, vault *secrets.Vault) (*Registry, error)` (loads UI rows at start)
  - `Credentials() []settings.Credential` (`Name, Source "file"|"ui", Token string (never serialized)`), `Credential(name) (settings.Credential, bool)`
  - `PutCredential(ctx, name, token string) error` (rejects names used by the file: `ErrReadOnly`), `DeleteCredential(ctx, name) error` (`ErrInUse` when a scale set references it)
  - `ScaleSets() []settings.ScaleSet` (`config.ScaleSet` + `Source`), `ScaleSet(name) (settings.ScaleSet, bool)`, `PutScaleSet(ctx, config.ScaleSet) error` (validated with the same rules as `ValidateServe`), `DeleteScaleSet(ctx, name) error`
  - `Subscribe() (<-chan struct{}, func())` — a coalescing notification after every change.
- `github.New(reg CredentialSource, logger)` where `type CredentialSource interface { ScaleSet(name string) (settings.ScaleSet, bool); Credential(name string) (settings.Credential, bool) }`.
- API: `GET /api/v1/credentials` (name, source, scale sets using it, `token_hint` = last 4 chars), `PUT /api/v1/credentials/{name} {token}`, `DELETE /api/v1/credentials/{name}`, `POST /api/v1/credentials/{name}/test` → `{ok, login?, error?}` (calls `GET https://api.github.com/user` with the token through `github.REST`).

- [ ] Failing tests: `TestRegistryMergesFileAndUI`, `TestFileEntriesAreReadOnly`, `TestCredentialInUseCannotBeDeleted`, `TestCredentialTokenIsSealedAtRest` (raw `secrets` row does not contain the token), `TestGitHubClientPicksUpANewToken`, API `TestCredentialEndpointsNeverReturnTheToken`.
- [ ] Implement; commit `feat(settings): editable GitHub credentials, sealed at rest`.

### Task 6: Editable scale sets without a restart

**Files:** Create `internal/controller/scalesets.go` + test; `cmd/ghrm/supervisor.go` + test; modify `internal/controller/controller.go`, `provision.go` (read scale sets from controller state, not `d.Config.ScaleSets`), `cmd/ghrm/serve.go`; API in `internal/api/settings_edit.go`.

**Interfaces:**
- Produces: `(*Controller).UpdateScaleSets(list []config.ScaleSet)` — adds new states, replaces changed `cfg`, marks removed ones `removed` (no new provisioning; `ScaleSets()` keeps listing them while they have live environments, flagged `Removed: true`).
- Produces: `supervisor{start func(ctx, config.ScaleSet)}` with `Reconcile(ctx, list []config.ScaleSet)` — starts a `listenLoop` per new scale set, cancels and restarts changed ones (deep-equal of `config.ScaleSet`), cancels removed ones; `Wait()`.
- API: `GET /api/v1/scale-sets` gains `source`; `PUT /api/v1/scale-sets/{name}` (body = editable fields of `config.ScaleSet`), `DELETE /api/v1/scale-sets/{name}`. Removing a UI scale set also deletes the GitHub runner scale set best effort (`scaleset.Client.DeleteRunnerScaleSet`) once its environments are gone; the result is recorded as an event.

- [ ] Failing tests: `TestAddedScaleSetGetsEnvironments`, `TestChangedScaleSetUsesTheNewSizeForNewEnvironments`, `TestRemovedScaleSetDrains`, supervisor `TestSupervisorStartsRestartsAndStopsListeners` (fake start func records ctx lifetimes), API `TestScaleSetPutValidates`.
- [ ] Implement; serve wires `reg.Subscribe()` → `ctl.UpdateScaleSets(reg.Configs())` + `sup.Reconcile`.
- [ ] Commit `feat(settings): create, edit and remove scale sets from the UI`.

### Task 7: Web sign-in, first-run setup and account

**Files:** Create `web/src/lib/session.ts`, `web/src/pages/{login,setup,account}.tsx` + tests; modify `web/src/api/client.ts` (inject `X-CSRF-Token` on non-GET; on 401 invalidate the session query), `web/src/router.tsx` (guard: `setup` → `/setup`, `signed_out` → `/login?next=`), shell (user menu: Account, Sign out), `components/admin-action.tsx` and `destroy-environment.tsx` (remove the token dialog; actions run with the session), `lib/admin-token.ts` (delete), `lib/log-buffer.ts` (EventSource sends cookies by default on same origin — nothing to add; test that a 401 stream shows "signed out").
- Tests (vitest): login form submits and redirects to `next`; wrong password shows the error; setup requires token, matching passwords and ≥ 12 characters; writes carry the CSRF header; a 401 sends the user to `/login`.
- e2e: `web/e2e/fixtures.ts` signs in through the API (`request.post("/api/v1/auth/login")`, storing cookies in the context); new `web/e2e/auth.spec.ts` (sign in through the form, sign out, protected page redirects).
- [ ] Commit `feat(web): sign-in, first-run setup and account page`.

### Task 8: Web settings editors

**Files:** Create `web/src/components/credentials-editor.tsx`, `scale-set-editor.tsx` + tests; modify `pages/settings.tsx`, `pages/scale-sets.tsx`.
- Credentials: table (name, source badge, used by, token hint), "Add credential" dialog (name + `SensitiveInput`), Replace, Test (shows the GitHub login or the error), Delete (disabled with a tooltip when in use or from the file).
- Scale sets: "New scale set" and Edit dialog (name, repository or organization URL, credential select, labels, runner group, max concurrent, cores, memory MB, keep on failure minutes), Remove with type-the-name confirmation; file-defined rows are read-only with a "defined in ghrm.yaml" hint.
- Demo mode: the registry works with a demo vault (key in the temp data dir), so the editors are exercised in e2e (`web/e2e/settings.spec.ts`).
- [ ] Commit `feat(web): edit credentials and scale sets`.

### Task 9: Metrics and daily backup

**Files:** Create `internal/metrics/metrics.go` + test, `internal/backup/backup.go` + test; modify `internal/api/api.go` (`/metrics`), `cmd/ghrm/serve.go`, config (`backup: {dir, keep, hour}` defaults `<data_dir>/backups`, 7, 3).
- Metrics (client_golang, own registry): `ghrm_environments{state,scale_set}` gauge, `ghrm_scale_set_desired{scale_set}` gauge, `ghrm_scale_set_listening{scale_set}` gauge, `ghrm_jobs_total{scale_set,result}` counter (from job completion), `ghrm_environment_failures_total{stage}` counter, `ghrm_stage_duration_seconds{stage}` histogram (observed on state transitions), `ghrm_template_builds_total{result}` counter, `ghrm_build_info{version}`.
- Backup: `backup.Run(ctx, db, dir, keep, hour, now)` — once a day at `hour` local, `VACUUM INTO '<dir>/ghrm-YYYYMMDD-HHMM.db'` via a new `(*Store).BackupTo(ctx, path)`, then delete the oldest beyond `keep`; records `backup.done`/`backup.failed` events.
- [ ] Tests: `TestMetricsExposeEnvironmentsByState`, `TestStageDurationsAreObserved`, `TestBackupWritesAUsableCopyAndKeepsN`.
- [ ] Commit `feat(ops): Prometheus metrics and daily backups`.

### Task 10: Docker image and Compose

**Files:** Create `Dockerfile` (stage 1 `node:22` builds `web/dist` with pnpm; stage 2 `golang:1.27` builds static `ghrm` with ldflags; stage 3 `gcr.io/distroless/static-debian12:nonroot`, `ENTRYPOINT ["/ghrm","serve","--config","/etc/ghrm/ghrm.yaml"]`, `EXPOSE 8080 8443`, volume `/var/lib/ghrm`), `.dockerignore`, `deploy/docker/compose.yaml` (ports, config bind mount read-only, named data volume, restart unless-stopped), `deploy/docker/README` section in `docs/development.md`.
- CI: new job `docker` runs `docker build .` and `docker compose -f deploy/docker/compose.yaml config`.
- [ ] Verify locally: `docker build -t ghrm:dev . && docker run --rm ghrm:dev version`.
- [ ] Commit `feat(deploy): container image and compose file`.

### Task 11: Idempotent Proxmox installer and release binaries

**Files:** Create `deploy/proxmox/install.sh`, `deploy/proxmox/test/run.sh`, `deploy/proxmox/test/fakebin/{pvesh,pveum,pct,pvesm,pveam,pveversion,curl,sha256sum}` (record calls to `$FAKE_LOG`, keep state under `$FAKE_STATE`), `.github/workflows/release.yml`; modify `.github/workflows/ci.yml` (shellcheck + installer tests), `deploy/proxmox/dev-template.sh` (sourced by the installer for the bootstrap template, or merged into it).
- Steps (each prints `✓ exists` or `+ created`): check Proxmox VE ≥ 9.1 and root; pool `ghrm`; role `GhrmRuntime` (privileges list from `docs/development.md`) and `GhrmTemplates`; user `ghrm@pve` and token `ghrm` (secret captured once and handed to the control plane via `ghrm secret set proxmox/token-secret` inside the LXC — never written to the host disk); ACLs on `/pool/ghrm`, `/sdn/zones/<zone>`, storages; template storage `ghrm-tpl` (dir); SDN zone/VNet/subnet with DHCP and SNAT, then `pvesh set /cluster/sdn`; security group `ghrm-job` with the rules of spec §10.1 including the ingest exception before the LAN drops; control-plane LXC (Debian 13, 1 vCPU, 2 GiB, 8 GB root + data mount, `net0` LAN DHCP or `--lan-ip`, `net1` job network fixed IP outside the DHCP range); inside it: download the release `ghrm` + `ghrm-agent` (checksums verified), write `/etc/ghrm/ghrm.yaml` from flags, install `ghrm.service`, start it; bootstrap template; print the URL and the setup token (`cat /var/lib/ghrm/setup-token`).
- Flags: `--vmid`, `--lan-bridge`, `--lan-ip`, `--job-subnet` (default `10.50.0.0/24`), `--zone`, `--vnet`, `--storage`, `--template-vmid`, `--version` (default latest release), `--binary <path>` (local build instead of a release, for development), `--dry-run`.
- Tests (`deploy/proxmox/test/run.sh`, plain bash): `install_creates_everything` (fresh fake state → all `+ created`, expected `pveum`/`pvesh`/`pct` calls), `install_is_idempotent` (second run → only `✓ exists`, no create/destroy calls), `install_reuses_existing_setup` (pre-seeded pool/user/zone), `install_refuses_old_pve` (pveversion 8.x → exit 1 with message), `dry_run_changes_nothing`.
- Release workflow: on tag `v*`, build `linux-amd64` `ghrm` and `ghrm-agent` (web embedded) and upload them with `SHA256SUMS` to the GitHub release.
- [ ] Commit `feat(deploy): idempotent Proxmox installer and release binaries`.

### Task 12: Documentation, diagrams and dev deployment

**Files:** `docs/architecture.md` (component table: auth/secrets/installer Implemented (M5); new "Sign-in and API access" sequence diagram; settings registry in the component diagram; deployment diagram with installer steps), `docs/development.md` (vault, `ghrm secret`, sign-in, metrics, backups, Docker, installer; remove the hand-made setup or mark it as the manual alternative), `README.md` (quick start with `install.sh`), `deploy/examples/ghrm.example.yaml` (new keys), screenshots (`pnpm screenshots`, including login and settings editors).
- Dev deployment: deploy to the development control plane; move its PAT and Proxmox secret into the vault with `ghrm secret set`; create the admin account with the setup token; verify sign-in, CSRF, editing a credential, adding and removing a scale set, `/metrics`, a backup file; run the installer with `--dry-run` against the real host and confirm every step reports `✓ exists`.
- [ ] Commit `docs: sign-in, secrets, editable settings, operations and installer`.

### Final: independent review

- Opus subagent reviews the whole branch against this plan and the spec; every finding (including minor ones) is fixed with a RED→GREEN test; then PR → CI → merge.
