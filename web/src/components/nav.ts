import { BriefcaseIcon, CubeIcon, GaugeIcon, GearSixIcon, PackageIcon, StackIcon, TerminalWindowIcon } from "@phosphor-icons/react";

/** The sidebar pages; labelKey is translated where it renders. */
export const navItems = [
  { href: "/", labelKey: "shell.nav.overview", icon: GaugeIcon },
  { href: "/jobs", labelKey: "shell.nav.jobs", icon: BriefcaseIcon },
  { href: "/environments", labelKey: "shell.nav.environments", icon: CubeIcon },
  { href: "/scale-sets", labelKey: "shell.nav.scaleSets", icon: StackIcon },
  { href: "/templates", labelKey: "shell.nav.templates", icon: PackageIcon },
  { href: "/logs", labelKey: "shell.nav.liveLogs", icon: TerminalWindowIcon },
  { href: "/settings", labelKey: "shell.nav.settings", icon: GearSixIcon },
] as const;
