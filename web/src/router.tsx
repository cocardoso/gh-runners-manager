import { createRootRoute, createRoute, createRouter, type RouterHistory } from "@tanstack/react-router";
import { Shell } from "@/components/shell";
import { OverviewPage } from "@/pages/overview";
import { JobsPage } from "@/pages/jobs";
import { JobDetailPage } from "@/pages/job-detail";
import { EnvironmentsPage } from "@/pages/environments";
import { EnvironmentDetailPage } from "@/pages/environment-detail";
import { ScaleSetsPage } from "@/pages/scale-sets";
import { TemplatesPage } from "@/pages/templates";
import { LiveLogsPage } from "@/pages/live-logs";
import { SettingsPage } from "@/pages/settings";
import { NotFoundPage } from "@/pages/not-found";

const rootRoute = createRootRoute({ component: Shell, notFoundComponent: NotFoundPage });

const routes = [
  createRoute({ getParentRoute: () => rootRoute, path: "/", component: OverviewPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/jobs", component: JobsPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/jobs/$id", component: JobDetailPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/environments", component: EnvironmentsPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/environments/$id", component: EnvironmentDetailPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/scale-sets", component: ScaleSetsPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/templates", component: TemplatesPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/logs", component: LiveLogsPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings", component: SettingsPage }),
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
