import { useState } from "react";
import { Button, LinkProvider, Sidebar, Tooltip, useSidebar } from "@cloudflare/kumo";
import { Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { ListIcon, MagnifyingGlassIcon, MonitorIcon, MoonIcon, SunIcon, UserCircleIcon } from "@phosphor-icons/react";
import { useSession } from "@/api/queries";
import { LoginPage, SetupPage } from "@/pages/sign-in";
import { ErrorState, Loading } from "./common";
import { AppLink } from "./app-link";
import { GlobalSearch } from "./command-palette";
import { LiveIndicator } from "./live-indicator";
import { navItems } from "./nav";
import { useTheme, type ThemePreference } from "@/lib/theme";
import { LanguageMenu } from "./language-menu";

function isActive(pathname: string, href: string) {
  return href === "/" ? pathname === "/" : pathname === href || pathname.startsWith(`${href}/`);
}

const nextTheme: Record<ThemePreference, ThemePreference> = { system: "light", light: "dark", dark: "system" };
const themeIcon = { system: MonitorIcon, light: SunIcon, dark: MoonIcon };

function ThemeToggle() {
  const [pref, setPref] = useTheme();
  const Icon = themeIcon[pref];
  const label = `Theme: ${pref} (switch to ${nextTheme[pref]})`;
  return <Tooltip content={label} render={<Button variant="ghost" shape="square" icon={Icon} aria-label={label} onClick={() => setPref(nextTheme[pref])} />} />;
}

function MobileMenuButton() {
  const { isMobile, setOpenMobile } = useSidebar();
  if (!isMobile) return null;
  return <Button variant="ghost" shape="square" icon={ListIcon} aria-label="Open navigation" onClick={() => setOpenMobile(true)} />;
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
  const navigate = useNavigate();
  const label = username ? `Account: ${username}` : "Account";
  return (
    <Tooltip
      content={label}
      render={<Button variant="ghost" shape="square" icon={UserCircleIcon} aria-label={label} onClick={() => void navigate({ to: "/account" })} />}
    />
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
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const [searchOpen, setSearchOpen] = useState(false);
  return (
    <LinkProvider component={AppLink}>
      <Sidebar.Provider defaultOpen collapsible="icon" className="min-h-dvh">
        <Sidebar aria-label="Main navigation" className="sticky top-0 h-dvh">
          <Sidebar.Header>
            <Logo />
          </Sidebar.Header>
          <Sidebar.Content>
            <Sidebar.Group>
              <Sidebar.Menu>
                {navItems.map((n) => (
                  <Sidebar.MenuItem key={n.href}>
                    <Sidebar.MenuButton icon={n.icon} href={n.href} active={isActive(pathname, n.href)} tooltip={n.label}>
                      {n.label}
                    </Sidebar.MenuButton>
                  </Sidebar.MenuItem>
                ))}
              </Sidebar.Menu>
            </Sidebar.Group>
          </Sidebar.Content>
          <Sidebar.Footer>
            <Sidebar.Trigger />
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
              <span className="truncate">Search…</span>
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
