import { createRootRoute, createRoute, createRouter, type RouterHistory } from "@tanstack/react-router";
import { Shell } from "@/components/shell";
import { OverviewPage } from "@/pages/overview";
import { JobsPage } from "@/pages/jobs";
import { JobDetailPage } from "@/pages/job-detail";
import { EnvironmentsPage } from "@/pages/environments";
import { EnvironmentDetailPage } from "@/pages/environment-detail";
import { ScaleSetsPage } from "@/pages/scale-sets";
import { TemplatesPage } from "@/pages/templates";
import { TemplateDetailPage } from "@/pages/template-detail";
import { LiveLogsPage } from "@/pages/live-logs";
import { SettingsPage } from "@/pages/settings";
import { AccountPage } from "@/pages/account";
import { NotFoundPage } from "@/pages/not-found";

const str = (v: unknown) => (typeof v === "string" && v !== "" ? v : typeof v === "number" ? String(v) : undefined);
const num = (v: unknown) => (typeof v === "number" && v > 0 ? Math.floor(v) : typeof v === "string" && Number(v) > 0 ? Math.floor(Number(v)) : undefined);

export interface ListSearch {
  q?: string;
  status?: string;
  scale_set?: string;
  repo?: string;
  range?: string;
  state?: string;
  level?: string;
  kind?: string;
  page?: number;
}

const listSearch = (s: Record<string, unknown>): ListSearch => ({
  q: str(s.q),
  status: str(s.status),
  scale_set: str(s.scale_set),
  repo: str(s.repo),
  range: str(s.range),
  state: str(s.state),
  level: str(s.level),
  kind: str(s.kind),
  page: num(s.page),
});

export interface DetailSearch {
  tab?: string;
}

const detailSearch = (s: Record<string, unknown>): DetailSearch => ({ tab: str(s.tab) });

const rootRoute = createRootRoute({ component: Shell, notFoundComponent: NotFoundPage });

const routes = [
  createRoute({ getParentRoute: () => rootRoute, path: "/", component: OverviewPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/jobs", component: JobsPage, validateSearch: listSearch }),
  createRoute({ getParentRoute: () => rootRoute, path: "/jobs/$id", component: JobDetailPage, validateSearch: detailSearch }),
  createRoute({ getParentRoute: () => rootRoute, path: "/environments", component: EnvironmentsPage, validateSearch: listSearch }),
  createRoute({ getParentRoute: () => rootRoute, path: "/environments/$id", component: EnvironmentDetailPage, validateSearch: detailSearch }),
  createRoute({ getParentRoute: () => rootRoute, path: "/scale-sets", component: ScaleSetsPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/templates", component: TemplatesPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/templates/$id", component: TemplateDetailPage, validateSearch: detailSearch }),
  createRoute({ getParentRoute: () => rootRoute, path: "/logs", component: LiveLogsPage, validateSearch: listSearch }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings", component: SettingsPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/account", component: AccountPage }),
];

const routeTree = rootRoute.addChildren(routes);

export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, history, defaultPreload: "intent", scrollRestoration: true });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
