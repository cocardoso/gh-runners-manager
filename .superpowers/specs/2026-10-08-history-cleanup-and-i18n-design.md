# History cleanup and UI i18n — design

Status: approved in conversation on 2026-10-08. Kept out of the repository (local process file).

Two independent parts, each with its own plan, PR and release: **A. History cleanup** (v0.2.0), then **B. UI i18n** (v0.3.0).

---

## A. History cleanup

### A.1 Goal

Nothing is ever deleted today: environments, jobs, events, log streams (rows and files under `data_dir/logs/<env>`) and template records grow forever, and a failed template record stays visible for good. The operator needs to remove old history, automatically or by hand, from the UI.

Success criteria:

- A **History** setting chooses **automatic** (default) or **manual** cleanup, how many days to keep (default **30**, 1–365) and how many days to keep audit events (default **365**, at least the history days, at most 3650).
- In automatic mode a daily sweep deletes history older than the setting; in manual mode nothing is deleted unless the operator asks.
- In both modes the UI offers **Clean up now…**: pick a date, see how many items would go, confirm.
- A failed or deleted template record can be deleted from the UI; so can a destroyed environment (with its jobs, events and logs).
- Nothing that is running or still needed is ever deleted (see A.2).
- Every manual deletion is audited; every sweep records an event with the counts.

### A.2 What history is, and what is never deleted

Deleted by age (`before` = now − days):

| Item | Eligible when |
|---|---|
| Environment (row, its log streams and the `logs/<id>` directory) | `state = destroyed`, `state_changed_at < before`, and it is not the build or verify environment of a template that is not `failed`/`deleted` (its logs are that template's build and verification record). |
| Job | `finished_at != 0` and `finished_at < before`, or its environment is deleted in the same sweep. |
| Event (not audit) | `ts < before`. |
| Audit event (`kind` starting with `audit.`) | `ts < auditBefore` (now − audit days). |
| Template record | `state IN (failed, deleted)` and `updated_at < before`. |

Never deleted: environments in any state other than `destroyed`; templates in any other state (building, verifying, ready, active, retired, held); scale sets, credentials, users, sessions, settings, secrets.

Deleting a failed template record also removes the guard that waits a day before retrying the same inputs (`check`), so the next check may build them again: deleting a failure means "try again".

### A.3 Components

- **Store** (`internal/store/history.go`):
  - `HistoryCounts{Environments, Jobs, Events, AuditEvents, Templates int}`.
  - `CountHistory(ctx, before, auditBefore) (HistoryCounts, error)`.
  - `DeleteHistory(ctx, before, auditBefore) (HistoryCounts, []string /*environment ids*/, error)`: one transaction, using the rules of A.2.
  - `DeleteEnvironmentHistory(ctx, id) error`: `ErrNotFound`; `ErrInUse` when not `destroyed` or referenced by a kept template; deletes the environment, its jobs, events and log streams.
  - `DeleteTemplateRecord(ctx, id) error`: `ErrNotFound`; `ErrInUse` unless `failed`/`deleted`.
  - `Vacuum(ctx)`.
- **Logs** (`internal/logs`): `RemoveEnvironment(envID) error` removes `logs/<envID>` (validated id; missing directory is fine).
- **Retention** (`internal/retention`):
  - Settings in the `meta` table: `history.mode` (`automatic`|`manual`), `history.days`, `history.audit_days`; `Settings(ctx)` returns the defaults when unset; `PutSettings(ctx, s)` validates.
  - `Preview(ctx, before, auditBefore)`, `Clean(ctx, before, auditBefore) (HistoryCounts, error)` (store delete, log directories, vacuum when anything was deleted).
  - `Run(ctx)`: every day at the backup hour + 30 minutes (local), when the mode is automatic, cleans with the configured days and records `retention.cleaned` (info, with the counts; failures `retention.failed`, error).
- **API**:
  - `GET /api/v1/history/settings`, `PUT /api/v1/history/settings` (audited `history_settings`).
  - `POST /api/v1/history/cleanup` with `{before, audit_before?, dry_run}` → counts (`dry_run` counts only; a real run is audited `history_cleanup` with the counts). `before` must be in the past. `audit_before` defaults to `now − audit days`, and never later than `before`.
  - `DELETE /api/v1/templates/{id}` (audited `template_delete`), `DELETE /api/v1/environments/{id}` (audited `environment_delete`): 404 unknown, 409 not deletable, with the reason.
  - Deletions publish events so open pages refresh (`retention.*`, `template.deleted_record`, `environment.deleted_record`).
- **UI**:
  - Settings: a **History** card — mode (automatic/manual), days, audit days, Save; **Clean up now…** dialog with a date (default: today − days), a preview of the counts (dry run), and a confirm button that shows the result.
  - Templates (list row and detail): **Delete** on `failed`/`deleted` records, with a confirmation.
  - Environment detail: **Delete from history** on `destroyed` environments, with a confirmation; the API's 409 reason is shown.
  - The event stream invalidates templates, environments, jobs and events on the new events.

### A.4 Testing

- Store: each rule of A.2 (eligible and protected rows, including the template-referenced environment and running environments), counts equal deletions, audit cut-off separate.
- Retention: defaults, validation (days range, audit ≥ days), automatic vs manual in the daily run, log directories removed, event recorded.
- API: settings round trip, dry run vs real run, past-only `before`, 404/409 on deletes, audit events.
- UI: History card save, cleanup dialog preview and confirm, Delete buttons shown only for deletable items.
- Real check in production after release: delete the failed template record of 2026-10-08, preview a cleanup.

---

## B. UI i18n

### B.1 Goal

The interface is in English only. Add **Portuguese (Brazil), Spanish, French and Italian**. Only the interface is translated: menus, pages, buttons, labels, empty states, toasts, dialogs and validation messages written by the UI. Messages produced by the server (events, failure reasons, logs, API errors) are shown as they come, in English.

Success criteria:

- Language: the browser's (`pt*` → pt-BR, `es*`, `fr*`, `it*`, anything else → English), overridable with a selector in the user menu, saved in `localStorage` (per browser); the sign-in and setup pages also follow the browser and offer the selector.
- Dates, times, durations, relative times, numbers and byte sizes are formatted for the chosen language (`Intl`).
- Every language has every key (checked by TypeScript and a test); no visible English literal is left in components (lint rule).
- `<html lang>` follows the language.

### B.2 Approach

In-house typed dictionaries, no dependency:

- `web/src/i18n/en.ts` is the source: a nested object of messages; `type Messages = typeof en`.
- `pt-BR.ts`, `es.ts`, `fr.ts`, `it.ts` are `Messages` (TypeScript fails on a missing or extra key).
- Interpolation `{name}` and plurals via `Intl.PluralRules` (`{ one: "…", other: "…" }` entries, selected by a `count` parameter).
- `I18nProvider` + `useT()` returning `t(key, params)` with typed keys; `useLocale()` for `Intl` formatting; `format.ts` takes the locale.
- Lazy loading is not needed (≈600 strings × 5 languages is small).

Alternatives considered: i18next + react-i18next (≈40 KB, weaker key typing), Lingui (needs a compiler plugin). Rejected for size and machinery.

### B.3 Testing

- Unit: language detection, fallback, plural selection, interpolation, `Intl` formatting per locale.
- A test that every locale has the same keys and the same `{params}` per message as `en`.
- ESLint rule (`react/jsx-no-literals`-style, configured to allow punctuation and symbols) on `src/` except tests.
- Component tests keep running in English; one test per page family renders in pt-BR.
- E2E: switching the language changes the navigation and persists across reloads.
- Screenshots stay in English.
