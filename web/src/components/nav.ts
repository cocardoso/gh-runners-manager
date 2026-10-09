import { BriefcaseIcon, CubeIcon, FolderSimpleIcon, GaugeIcon, GearSixIcon, PackageIcon, StackIcon, TerminalWindowIcon } from "@phosphor-icons/react";
import type { Key } from "@/i18n";

/** A link inside a sidebar item: a tab (?tab=) or a section (#id) of its page. */
export interface NavSub {
  href: string;
  labelKey: Key;
}

/**
 * The sidebar pages, grouped: inventory (what exists and can be used), activity (what
 * happens or happened) and system. An item with subs opens to its page's tabs or sections;
 * "scale-sets" lists one link per scale set; badge "jobs" counts the running jobs.
 * labelKey is translated where it renders.
 */
export const navGroups = [
  { id: "home", items: [{ href: "/", labelKey: "shell.nav.overview", icon: GaugeIcon }] },
  {
    id: "inventory",
    labelKey: "shell.nav.groups.inventory",
    items: [
      { href: "/repositories", labelKey: "shell.nav.repositories", icon: FolderSimpleIcon },
      { href: "/scale-sets", labelKey: "shell.nav.scaleSets", icon: StackIcon, subs: "scale-sets" },
      {
        href: "/templates",
        labelKey: "shell.nav.templates",
        icon: PackageIcon,
        subs: [
          { href: "/templates", labelKey: "shell.nav.versions" },
          { href: "/templates?tab=profiles", labelKey: "templates.profiles.tab" },
          { href: "/templates?tab=history", labelKey: "templates.tabs.history" },
        ],
      },
    ],
  },
  {
    id: "activity",
    labelKey: "shell.nav.groups.activity",
    items: [
      {
        href: "/jobs",
        labelKey: "shell.nav.jobs",
        icon: BriefcaseIcon,
        badge: "jobs",
        subs: [
          { href: "/jobs", labelKey: "overview.jobs.tabs.inProgress" },
          { href: "/jobs?tab=history", labelKey: "overview.jobs.tabs.history" },
        ],
      },
      {
        href: "/environments",
        labelKey: "shell.nav.environments",
        icon: CubeIcon,
        subs: [
          { href: "/environments", labelKey: "environments.list.tabs.running" },
          { href: "/environments?tab=history", labelKey: "environments.list.tabs.history" },
        ],
      },
      { href: "/logs", labelKey: "shell.nav.events", icon: TerminalWindowIcon },
    ],
  },
  {
    id: "system",
    labelKey: "shell.nav.groups.system",
    items: [
      {
        href: "/settings",
        labelKey: "shell.nav.settings",
        icon: GearSixIcon,
        subs: [
          { href: "/settings#credentials", labelKey: "settings.sections.credentials" },
          { href: "/settings#capacity", labelKey: "settings.sections.capacity" },
          { href: "/settings#cache", labelKey: "settings.cache.title" },
          { href: "/settings#history", labelKey: "settings.history.title" },
        ],
      },
    ],
  },
] as const satisfies readonly {
  id: string;
  labelKey?: Key;
  items: readonly { href: string; labelKey: Key; icon: unknown; subs?: readonly NavSub[] | "scale-sets"; badge?: "jobs" }[];
}[];

export type NavItem = (typeof navGroups)[number]["items"][number];

/** Every page in sidebar order. */
export const navItems: readonly NavItem[] = navGroups.flatMap((g): readonly NavItem[] => g.items);

/** Whether a sub link is the place shown: same page and tab (no tab: the first one), or the
 * same section when it names one. hash comes without its "#". */
export function subActive(href: string, at: { pathname: string; tab?: string; hash: string }): boolean {
  const url = new URL(href, "http://x");
  if (url.pathname !== at.pathname) return false;
  if (url.hash) return url.hash.slice(1) === at.hash;
  return !at.hash && (url.searchParams.get("tab") ?? "") === (at.tab ?? "");
}
