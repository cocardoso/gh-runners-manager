import { useEffect, useState, type ReactNode } from "react";
import { Button, LinkProvider, Sidebar, Tooltip, useSidebar } from "@cloudflare/kumo";
import { Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { ListIcon, MagnifyingGlassIcon, MonitorIcon, MoonIcon, SunIcon, UserCircleIcon } from "@phosphor-icons/react";
import { useOverview, useScaleSets, useSession } from "@/api/queries";
import { LoginPage, SetupPage } from "@/pages/sign-in";
import { ErrorState, Loading } from "./common";
import { AppLink } from "./app-link";
import { GlobalSearch } from "./command-palette";
import { LiveIndicator } from "./live-indicator";
import { hashId, navGroups, subActive, type NavItem } from "./nav";
import { useTheme, type ThemePreference } from "@/lib/theme";
import { LanguageMenu } from "./language-menu";
import { useT } from "@/i18n";

function isActive(pathname: string, href: string) {
  return href === "/" ? pathname === "/" : pathname === href || pathname.startsWith(`${href}/`);
}

const nextTheme: Record<ThemePreference, ThemePreference> = { system: "light", light: "dark", dark: "system" };
const themeIcon = { system: MonitorIcon, light: SunIcon, dark: MoonIcon };

function ThemeToggle() {
  const t = useT();
  const [pref, setPref] = useTheme();
  const Icon = themeIcon[pref];
  const label = t("shell.theme.label", { current: t(`shell.theme.${pref}`), next: t(`shell.theme.${nextTheme[pref]}`) });
  return <Tooltip content={label} render={<Button variant="ghost" shape="square" icon={Icon} aria-label={label} onClick={() => setPref(nextTheme[pref])} />} />;
}

function MobileMenuButton() {
  const t = useT();
  const { isMobile, setOpenMobile } = useSidebar();
  if (!isMobile) return null;
  return <Button variant="ghost" shape="square" icon={ListIcon} aria-label={t("shell.header.openNavigation")} onClick={() => setOpenMobile(true)} />;
}

function Logo() {
  return (
    <div className="flex min-w-0 items-center gap-2 px-1 py-1">
      <img src="/favicon.svg" alt="" className="size-6 shrink-0" />
      <span className="truncate font-semibold text-kumo-default group-data-[state=collapsed]:hidden">gh-runners-manager</span>
    </div>
  );
}

function AccountButton({ username }: { username?: string }) {
  const t = useT();
  const navigate = useNavigate();
  const label = username ? t("shell.header.accountOf", { username }) : t("shell.header.account");
  return (
    <Tooltip
      content={label}
      render={<Button variant="ghost" shape="square" icon={UserCircleIcon} aria-label={label} onClick={() => void navigate({ to: "/account" })} />}
    />
  );
}

interface At {
  pathname: string;
  tab?: string;
  hash: string;
}

/** The Jobs item's running count and waiting dot, and the words a screen reader says for them. */
function useJobsBadge(label: string): { badge: ReactNode; name: string } {
  const t = useT();
  const kpis = useOverview().data?.kpis;
  if (!kpis) return { badge: null, name: label };
  const waiting = kpis.queued_jobs > 0;
  const running = kpis.running_jobs;
  const name = [label, running > 0 && t("shell.nav.badge.running", { n: running }), waiting && t("shell.nav.badge.waiting")].filter(Boolean).join(", ");
  // The button is named in words; the dot and the count are for the eye.
  const badge = (
    <span aria-hidden className="ml-auto flex items-center gap-1.5">
      {waiting && <span className="size-2 rounded-full bg-kumo-warning" />}
      {running > 0 && <Sidebar.MenuBadge>{running}</Sidebar.MenuBadge>}
    </span>
  );
  return { badge, name };
}

/** A sidebar item. With subs it opens (Cloudflare-style) to its page's tabs or sections, and
 * to one link per scale set; collapsed to icons, it is a plain link to its page. */
function NavEntry({
  item,
  at,
  scaleSets,
  jobs,
  open,
  onOpenChange,
}: {
  item: NavItem;
  at: At;
  scaleSets: string[];
  jobs: { badge: ReactNode; name: string };
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT();
  const { state, isMobile, setOpenMobile } = useSidebar();
  const label = t(item.labelKey);
  const { badge, name } = "badge" in item ? jobs : { badge: null, name: label };
  const active = isActive(at.pathname, item.href);
  const subs =
    !("subs" in item)
      ? []
      : item.subs === "scale-sets"
        ? [{ href: "/scale-sets", label: t("shell.nav.allScaleSets") }, ...scaleSets.map((ss) => ({ href: `/scale-sets#${encodeURIComponent(ss)}`, label: ss }))]
        : item.subs.map((sub) => ({ href: sub.href, label: t(sub.labelKey) }));
  // On a phone the menu is a drawer over the page: a link closes it. A link to the section
  // already shown scrolls to it again (the location does not change).
  const followed = (href: string) => {
    if (isMobile) setOpenMobile(false);
    const id = hashId(href);
    if (id && id === at.hash && new URL(href, "http://x").pathname === at.pathname) document.getElementById(id)?.scrollIntoView({ block: "start", behavior: "smooth" });
  };
  if (subs.length === 0 || (state === "collapsed" && !isMobile))
    return (
      <Sidebar.MenuItem>
        <Sidebar.MenuButton icon={item.icon} href={item.href} active={active} tooltip={label} aria-label={name === label ? undefined : name} onClick={() => followed(item.href)}>
          {label}
          {badge}
        </Sidebar.MenuButton>
      </Sidebar.MenuItem>
    );
  // The item stays marked while open unless one of its links is the place shown (a detail
  // page, or Settings without a section, has none).
  const shown = subs.some((sub) => subActive(sub.href, at));
  return (
    <Sidebar.MenuItem>
      <Sidebar.Collapsible open={open} onOpenChange={onOpenChange}>
        <Sidebar.CollapsibleTrigger
          render={
            <Sidebar.MenuButton icon={item.icon} active={active && !(open && shown)} aria-label={name === label ? undefined : name}>
              {label}
              {badge}
              <Sidebar.MenuChevron />
            </Sidebar.MenuButton>
          }
        />
        <Sidebar.CollapsibleContent aria-label={label}>
          <Sidebar.MenuSub>
            {subs.map((sub) => (
              <Sidebar.MenuSubButton key={sub.href} href={sub.href} active={subActive(sub.href, at)} onClick={() => followed(sub.href)}>
                <span className="truncate">{sub.label}</span>
              </Sidebar.MenuSubButton>
            ))}
          </Sidebar.MenuSub>
        </Sidebar.CollapsibleContent>
      </Sidebar.Collapsible>
    </Sidebar.MenuItem>
  );
}

/** Scrolls to the #section a link names, once the page has rendered it (lists load later). */
function useScrollToHash(at: At) {
  useEffect(() => {
    if (!at.hash) return;
    let tries = 0;
    const timer = setInterval(() => {
      const el = document.getElementById(at.hash);
      if (el || ++tries > 40) {
        clearInterval(timer);
        el?.scrollIntoView({ block: "start", behavior: "smooth" });
      }
    }, 50);
    return () => clearInterval(timer);
  }, [at.pathname, at.hash]);
}

/** A sidebar section; a labelled one is announced as a group (its label hides when the sidebar collapses to icons). */
function NavGroup({ id, label, children }: { id: string; label?: string; children: React.ReactNode }) {
  return (
    <Sidebar.Group role={label ? "group" : undefined} aria-labelledby={label ? id : undefined}>
      {label && <Sidebar.GroupLabel id={id}>{label}</Sidebar.GroupLabel>}
      <Sidebar.Menu>{children}</Sidebar.Menu>
    </Sidebar.Group>
  );
}

/** Signs the visitor in (or creates the first account) before showing the app. */
export function Shell() {
  const session = useSession();
  if (session.isLoading) return <div className="p-6"><Loading /></div>;
  if (session.error || !session.data) return <div className="p-6"><ErrorState error={session.error} /></div>;
  if (session.data.state === "setup") return <SetupPage />;
  if (session.data.state !== "signed_in") return <LoginPage />;
  return <AppShell username={session.data.username} />;
}

function AppShell({ username }: { username?: string }) {
  const location = useRouterState({ select: (s) => s.location });
  const at: At = { pathname: location.pathname, tab: (location.search as { tab?: string }).tab, hash: location.hash };
  const pathname = at.pathname;
  const [searchOpen, setSearchOpen] = useState(false);
  // An item is open while its page is shown, unless closed by hand there; another page's
  // item stays as it was left. Each choice remembers the page it was made on.
  const [opened, setOpened] = useState<Record<string, { open: boolean; on: string }>>({});
  const isOpen = (href: string) => {
    const choice = opened[href];
    const active = isActive(pathname, href);
    return choice && (!active || choice.on === pathname) ? choice.open : active;
  };
  useScrollToHash(at);
  const scaleSets = useScaleSets();
  const scaleSetNames = (scaleSets.data ?? []).map((ss) => ss.name);
  const t = useT();
  const jobs = useJobsBadge(t("shell.nav.jobs"));
  return (
    <LinkProvider component={AppLink}>
      <Sidebar.Provider defaultOpen collapsible="icon" className="min-h-dvh">
        <Sidebar aria-label={t("shell.header.mainNavigation")} className="md:sticky md:top-0 md:h-dvh">
          <Sidebar.Header>
            <Logo />
          </Sidebar.Header>
          <Sidebar.Content>
            {navGroups.map((g) => (
              <NavGroup key={g.id} id={`nav-${g.id}`} label={"labelKey" in g ? t(g.labelKey) : undefined}>
                {g.items.map((n) => (
                  <NavEntry
                    key={n.href}
                    item={n}
                    at={at}
                    scaleSets={scaleSetNames}
                    jobs={jobs}
                    open={isOpen(n.href)}
                    onOpenChange={(o) => setOpened((prev) => ({ ...prev, [n.href]: { open: o, on: pathname } }))}
                  />
                ))}
              </NavGroup>
            ))}
          </Sidebar.Content>
          <Sidebar.Footer>
            <Sidebar.Trigger aria-label={t("shell.sidebar.toggle")} />
          </Sidebar.Footer>
        </Sidebar>
        <div className="flex min-w-0 flex-1 flex-col bg-kumo-canvas">
          <header className="sticky top-0 z-10 flex h-14 items-center gap-2 border-b border-kumo-line bg-kumo-base px-3 md:px-6">
            <MobileMenuButton />
            <button
              type="button"
              onClick={() => setSearchOpen(true)}
              className="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-lg border border-kumo-line bg-kumo-base px-3 text-left text-sm text-kumo-subtle hover:bg-kumo-tint sm:max-w-sm"
            >
              <MagnifyingGlassIcon size={16} className="shrink-0" />
              <span className="truncate">{t("shell.header.search")}</span>
              <kbd className="ml-auto hidden rounded border border-kumo-hairline px-1.5 text-xs sm:inline">⌘K</kbd>
            </button>
            <div className="ml-auto flex items-center gap-1">
              <LiveIndicator />
              <LanguageMenu />
              <ThemeToggle />
              <AccountButton username={username} />
            </div>
          </header>
          <main className="min-w-0 flex-1 p-3 md:p-6">
            <Outlet />
          </main>
        </div>
        <GlobalSearch open={searchOpen} onOpenChange={setSearchOpen} />
      </Sidebar.Provider>
    </LinkProvider>
  );
}
