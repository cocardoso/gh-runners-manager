import { BriefcaseIcon, CubeIcon, FolderSimpleIcon, GaugeIcon, GearSixIcon, PackageIcon, StackIcon, TerminalWindowIcon } from "@phosphor-icons/react";

/**
 * The sidebar pages, grouped: inventory (what exists and can be used) and
 * activity (what happens or happened). labelKey is translated where it renders.
 */
export const navGroups = [
  { id: "home", items: [{ href: "/", labelKey: "shell.nav.overview", icon: GaugeIcon }] },
  {
    id: "inventory",
    labelKey: "shell.nav.groups.inventory",
    items: [
      { href: "/repositories", labelKey: "shell.nav.repositories", icon: FolderSimpleIcon },
      { href: "/scale-sets", labelKey: "shell.nav.scaleSets", icon: StackIcon },
      { href: "/templates", labelKey: "shell.nav.templates", icon: PackageIcon },
    ],
  },
  {
    id: "activity",
    labelKey: "shell.nav.groups.activity",
    items: [
      { href: "/jobs", labelKey: "shell.nav.jobs", icon: BriefcaseIcon },
      { href: "/environments", labelKey: "shell.nav.environments", icon: CubeIcon },
      { href: "/logs", labelKey: "shell.nav.events", icon: TerminalWindowIcon },
    ],
  },
  { id: "system", items: [{ href: "/settings", labelKey: "shell.nav.settings", icon: GearSixIcon }] },
] as const;

export type NavItem = (typeof navGroups)[number]["items"][number];

/** Every page in sidebar order. */
export const navItems: readonly NavItem[] = navGroups.flatMap((g): readonly NavItem[] => g.items);
