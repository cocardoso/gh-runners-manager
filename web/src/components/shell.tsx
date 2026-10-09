import { useEffect, useState } from "react";
import { Button, LinkProvider, Sidebar, Tooltip, useSidebar } from "@cloudflare/kumo";
import { Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { ListIcon, MagnifyingGlassIcon, MonitorIcon, MoonIcon, SunIcon, UserCircleIcon } from "@phosphor-icons/react";
import { useOverview, useScaleSets, useSession } from "@/api/queries";
import { LoginPage, SetupPage } from "@/pages/sign-in";
import { ErrorState, Loading } from "./common";
import { AppLink } from "./app-link";
import { GlobalSearch } from "./command-palette";
import { LiveIndicator } from "./live-indicator";
import { navGroups, subActive, type NavItem } from "./nav";
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

/** Running jobs, and whether any job waits for a runner, for the Jobs item. */
function JobsBadge() {
  const t = useT();
  const kpis = useOverview().data?.kpis;
  if (!kpis) return null;
  const waiting = kpis.queued_jobs > 0;
  return (
    <span className="ml-auto flex items-center gap-1.5">
      {waiting && <span role="img" aria-label={t("shell.nav.badge.waiting")} className="size-2 rounded-full bg-kumo-warning" />}
      {kpis.running_jobs > 0 && (
        <Sidebar.MenuBadge aria-label={t("shell.nav.badge.running", { n: kpis.running_jobs })}>{kpis.running_jobs}</Sidebar.MenuBadge>
      )}
    </span>
  );
}

/** A sidebar item. With subs it opens (Cloudflare-style) to its page's tabs or sections, and
 * to one link per scale set; collapsed to icons, it is a plain link to its page. */
function NavEntry({ item, at, open, onOpenChange }: { item: NavItem; at: At; open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const { state, isMobile } = useSidebar();
  const scaleSets = useScaleSets();
  const label = t(item.labelKey);
  const active = isActive(at.pathname, item.href);
  const badge = "badge" in item ? <JobsBadge /> : null;
  const subs =
    !("subs" in item)
      ? []
      : item.subs === "scale-sets"
        ? [{ href: "/scale-sets", label: t("shell.nav.allScaleSets") }, ...(scaleSets.data ?? []).map((ss) => ({ href: `/scale-sets#${encodeURIComponent(ss.name)}`, label: ss.name }))]
        : item.subs.map((sub) => ({ href: sub.href, label: t(sub.labelKey) }));
  if (subs.length === 0 || (state === "collapsed" && !isMobile))
    return (
      <Sidebar.MenuItem>
        <Sidebar.MenuButton icon={item.icon} href={item.href} active={active} tooltip={label}>
          {label}
          {badge}
        </Sidebar.MenuButton>
      </Sidebar.MenuItem>
    );
  return (
    <Sidebar.MenuItem>
      <Sidebar.Collapsible open={open} onOpenChange={onOpenChange}>
        <Sidebar.CollapsibleTrigger
          render={
            <Sidebar.MenuButton icon={item.icon} active={active && !open}>
              {label}
              {badge}
              <Sidebar.MenuChevron />
            </Sidebar.MenuButton>
          }
        />
        <Sidebar.CollapsibleContent>
          <Sidebar.MenuSub>
            {subs.map((sub) => (
              <Sidebar.MenuSubButton key={sub.href} href={sub.href} active={subActive(sub.href, at)}>
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
    const id = decodeURIComponent(at.hash);
    let tries = 0;
    const timer = setInterval(() => {
      const el = document.getElementById(id);
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
  const t = useT();
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
