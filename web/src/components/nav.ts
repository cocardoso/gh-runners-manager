import { BriefcaseIcon, CubeIcon, GaugeIcon, GearSixIcon, PackageIcon, StackIcon, TerminalWindowIcon } from "@phosphor-icons/react";

export const navItems = [
  { href: "/", label: "Overview", icon: GaugeIcon },
  { href: "/jobs", label: "Jobs", icon: BriefcaseIcon },
  { href: "/environments", label: "Environments", icon: CubeIcon },
  { href: "/scale-sets", label: "Scale sets", icon: StackIcon },
  { href: "/templates", label: "Templates", icon: PackageIcon },
  { href: "/logs", label: "Live logs", icon: TerminalWindowIcon },
  { href: "/settings", label: "Settings", icon: GearSixIcon },
] as const;
