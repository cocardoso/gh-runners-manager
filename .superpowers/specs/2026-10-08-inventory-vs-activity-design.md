# Inventory vs activity — design

Status: approved in conversation on 2026-10-08 (local process file).

Principle: inventory (what exists and can be used) is separate from activity (what happens or happened). Every list opens on "now"; history has its own tab.

- Navigation groups: Overview · Inventory (Repositories, Scale sets, Templates) · Activity (Jobs, Environments, Events) · Settings. "Live logs" becomes "Events" (the system's event log); job and environment logs stay as tabs of their detail pages.
- Templates: tab "In use and available" (default): an in-progress build card (stage, link to its log), an "In use" card (active template: versions, VMID, size, active since, fidelity), "Available for rollback" (ready versions and the bootstrap, Activate/Pin). Tab "Build history": failed, deleted, retired, with result, reason, when; Delete record lives there.
- Environments: tab "Running" (default): every environment that exists now (not destroyed), failed-and-kept ones badged "kept for debugging until HH:MM". Tab "History": destroyed ones with job, result, lifetime.
- Jobs: tab "In progress" (default): assigned and running. Tab "History": completed, with result, scale set and period filters.
- Repositories (new page, `GET /api/v1/repositories`): one row per GitHub repository or organization a scale set serves: link, scale sets, credential, jobs running now, jobs in 24 h with success rate, last job (when, result); organizations also list the repositories seen in their jobs. Aggregated on the server from scale sets (settings registry) and jobs.
- Tabs are links (`?tab=history`), so the browser's back button and shared links keep the view.
- Tests: each tab and the new page, in English and pt-BR; e2e for the menu and tabs; screenshots refreshed.
