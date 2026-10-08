import { useEffect, useMemo, useState } from "react";
import { CommandPalette } from "@cloudflare/kumo";
import { useNavigate } from "@tanstack/react-router";
import { BriefcaseIcon, CubeIcon, StackIcon, ArrowRightIcon } from "@phosphor-icons/react";
import { useEnvironments, useJobs, useScaleSets } from "@/api/queries";
import { navItems } from "./nav";
import { stateLabel } from "@/i18n/labels";
import { useT } from "@/i18n";

export interface PaletteItem {
  id: string;
  title: string;
  description?: string;
  href: string;
  /** Extra text that matches the search (ids, run ids, runner names, IPs). */
  keywords: string;
  icon: React.ReactNode;
}

export interface PaletteGroup {
  id: string;
  label: string;
  items: PaletteItem[];
}

export function filterGroups(groups: PaletteGroup[], query: string, perGroup = 8): PaletteGroup[] {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  return groups
    .map((g) => ({
      ...g,
      items: g.items.filter((it) => {
        const hay = `${it.title} ${it.description ?? ""} ${it.keywords}`.toLowerCase();
        return terms.every((t) => hay.includes(t));
      }).slice(0, terms.length ? perGroup : Math.min(perGroup, 5)),
    }))
    .filter((g) => g.items.length > 0);
}

function usePaletteGroups(): { groups: PaletteGroup[]; loading: boolean } {
  const jobs = useJobs({ limit: 500 });
  const envs = useEnvironments({ limit: 500 });
  const sets = useScaleSets();
  const t = useT();
  const groups = useMemo<PaletteGroup[]>(
    () => [
      {
        id: "pages",
        label: t("shell.palette.pages"),
        items: navItems.map((n) => ({ id: `page:${n.href}`, title: t(n.labelKey), href: n.href, keywords: "", icon: <n.icon size={16} /> })),
      },
      {
        id: "jobs",
        label: t("shell.nav.jobs"),
        items: (jobs.data ?? []).map((j) => ({
          id: `job:${j.id}`,
          title: j.display_name || j.id,
          description: `${j.repository} · ${stateLabel(j.status)}${j.result ? ` (${stateLabel(j.result)})` : ""}`,
          href: `/jobs/${encodeURIComponent(j.id)}`,
          keywords: `${j.id} ${j.run_id ?? ""} ${j.workflow_ref ?? ""} ${j.runner_name ?? ""} ${j.scale_set}`,
          icon: <BriefcaseIcon size={16} />,
        })),
      },
      {
        id: "environments",
        label: t("shell.nav.environments"),
        items: (envs.data ?? []).map((e) => ({
          id: `env:${e.id}`,
          title: e.id,
          description: `${e.scale_set} · ${stateLabel(e.state)}`,
          href: `/environments/${encodeURIComponent(e.id)}`,
          keywords: `${e.runner_name ?? ""} ${e.ip ?? ""} ${e.runtime_ref ?? ""} ${e.job_id ?? ""}`,
          icon: <CubeIcon size={16} />,
        })),
      },
      {
        id: "scale-sets",
        label: t("shell.nav.scaleSets"),
        items: (sets.data ?? []).map((s) => ({
          id: `ss:${s.name}`,
          title: s.name,
          description: s.listening ? t("shell.palette.listening") : t("shell.palette.notListening"),
          href: `/scale-sets#${encodeURIComponent(s.name)}`,
          keywords: String(s.github_id),
          icon: <StackIcon size={16} />,
        })),
      },
    ],
    [jobs.data, envs.data, sets.data, t],
  );
  return { groups, loading: jobs.isLoading || envs.isLoading || sets.isLoading };
}

function PaletteBody({ onClose }: { onClose: () => void }) {
  const t = useT();
  const navigate = useNavigate();
  const [search, setSearch] = useState("");
  const { groups, loading } = usePaletteGroups();
  const filtered = useMemo(() => filterGroups(groups, search), [groups, search]);
  const go = (item: PaletteItem) => {
    onClose();
    void navigate({ to: item.href.split("#")[0]!, hash: item.href.split("#")[1] });
  };
  return (
    <CommandPalette.Root<PaletteGroup, PaletteItem>
      open
      onOpenChange={(o) => !o && onClose()}
      items={filtered}
      value={search}
      onValueChange={setSearch}
      itemToStringValue={(g) => g.label}
      filter={() => true}
      onSelect={(item) => go(item)}
      getSelectableItems={(gs) => gs.flatMap((g) => g.items)}
    >
      <CommandPalette.Input placeholder={t("shell.palette.placeholder")} aria-label={t("shell.palette.search")} />
      <CommandPalette.List>
        {loading && filtered.length <= 1 ? (
          <CommandPalette.Loading />
        ) : (
          <>
            <CommandPalette.Results>
              {(group: PaletteGroup) => (
                <CommandPalette.Group key={group.id} items={group.items}>
                  <CommandPalette.GroupLabel>{group.label}</CommandPalette.GroupLabel>
                  <CommandPalette.Items>
                    {(item: PaletteItem) => (
                      <CommandPalette.Item key={item.id} value={item} onClick={() => go(item)}>
                        <span className="flex min-w-0 items-center gap-3">
                          <span className="text-kumo-subtle">{item.icon}</span>
                          <span className="min-w-0 truncate">{item.title}</span>
                          {item.description && <span className="min-w-0 truncate text-sm text-kumo-subtle">{item.description}</span>}
                        </span>
                      </CommandPalette.Item>
                    )}
                  </CommandPalette.Items>
                </CommandPalette.Group>
              )}
            </CommandPalette.Results>
            <CommandPalette.Empty>{t("shell.palette.nothingMatches", { query: search })}</CommandPalette.Empty>
          </>
        )}
      </CommandPalette.List>
      <CommandPalette.Footer>
        <span className="flex items-center gap-2">
          <kbd className="rounded border border-kumo-hairline bg-kumo-base px-1.5 py-0.5 text-[10px]">↑↓</kbd>
          <span>{t("shell.palette.navigate")}</span>
        </span>
        <span className="flex items-center gap-2">
          <kbd className="rounded border border-kumo-hairline bg-kumo-base px-1.5 py-0.5 text-[10px]">↵</kbd>
          <span>{t("shell.palette.open")}</span>
          <ArrowRightIcon size={12} />
        </span>
      </CommandPalette.Footer>
    </CommandPalette.Root>
  );
}

/** ⌘K / Ctrl+K palette. Data loads only while it is open. */
export function GlobalSearch({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        onOpenChange(!open);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onOpenChange]);
  return open ? <PaletteBody onClose={() => onOpenChange(false)} /> : null;
}
