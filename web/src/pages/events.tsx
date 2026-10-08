import { useEffect, useMemo, useRef, useState } from "react";
import { Button, LayerCard, Link, cn } from "@cloudflare/kumo";
import { ArrowDownIcon, PauseIcon, PlayIcon } from "@phosphor-icons/react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useVirtualizer } from "@tanstack/react-virtual";
import { api, unwrap, type ApiEvent } from "@/api/client";
import { Page } from "@/components/common";
import { ListToolbar } from "@/components/list-toolbar";
import { LevelBadge } from "@/components/status-badge";
import { currentFormatLocale, tr, useT } from "@/i18n";
import { formatNumber } from "@/lib/format";
import { useLiveEvents } from "@/lib/live";
import type { ListSearch } from "@/router";

export const EVENT_CAP = 5000;
const PAGE = 500;

const levels = ["info", "warn", "error"] as const;
const kinds = ["environment", "job", "scaleset", "agent", "reaper", "controller", "audit"] as const;
const levelOptions = () => Object.fromEntries(levels.map((l) => [l, tr(`environments.liveLogs.level.${l}`)]));
const kindOptions = () => Object.fromEntries(kinds.map((k) => [k, tr(`environments.liveLogs.kind.${k}`)]));

function mergeSorted(a: ApiEvent[], b: ApiEvent[]) {
  const map = new Map<number, ApiEvent>();
  for (const e of a) map.set(e.seq, e);
  for (const e of b) map.set(e.seq, e);
  return [...map.values()].sort((x, y) => x.seq - y.seq);
}

function Row({ e }: { e: ApiEvent }) {
  const t = useT();
  return (
    <div role="listitem" className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-kumo-line px-3 py-1.5 text-sm sm:flex-nowrap">
      <time className="shrink-0 font-mono text-xs text-kumo-subtle tabular-nums" dateTime={e.time}>
        {new Date(e.time).toLocaleTimeString(currentFormatLocale(), { hour12: false })}
      </time>
      <span className="shrink-0">
        <LevelBadge level={e.level} />
      </span>
      <span className="w-40 shrink-0 truncate font-mono text-xs text-kumo-subtle" title={e.kind}>
        {e.kind}
      </span>
      <span className={cn("min-w-0 flex-1 break-words", e.level === "error" && "text-kumo-danger")}>{e.message}</span>
      <span className="flex shrink-0 gap-2 text-xs">
        {e.scale_set && <span className="text-kumo-subtle">{e.scale_set}</span>}
        {e.environment_id && <Link href={`/environments/${encodeURIComponent(e.environment_id)}`}>{t("environments.liveLogs.envLink")}</Link>}
        {e.job_id && <Link href={`/jobs/${encodeURIComponent(e.job_id)}`}>{t("environments.liveLogs.jobLink")}</Link>}
      </span>
    </div>
  );
}

/** The global event stream, filterable and pausable, like Workers Logs' live view. */
export function EventsPage() {
  const search = useSearch({ strict: false }) as ListSearch;
  const navigate = useNavigate();
  const t = useT();
  const set = (patch: Partial<ListSearch>) => void navigate({ to: "/logs", search: (prev: ListSearch) => ({ ...prev, ...patch }), replace: true });
  const [events, setEvents] = useState<ApiEvent[]>([]);
  const [paused, setPaused] = useState(false);
  const [shownUpTo, setShownUpTo] = useState(Infinity);
  const [loadingEarlier, setLoadingEarlier] = useState(false);
  const [hasEarlier, setHasEarlier] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let cancelled = false;
    api
      .GET("/api/v1/events", { params: { query: { newest: true, limit: PAGE } } })
      .then((r) => {
        if (cancelled) return;
        const page = unwrap(r).events ?? [];
        setEvents((cur) => mergeSorted(page as ApiEvent[], cur).slice(-EVENT_CAP));
        setHasEarlier(page.length === PAGE);
      })
      .catch((e: unknown) => !cancelled && setError(e instanceof Error ? e.message : String(e)));
    return () => {
      cancelled = true;
    };
  }, []);

  // While paused (reading history) keep up to twice the cap so loaded pages are not trimmed away.
  useLiveEvents((e) => setEvents((cur) => (cur.length && cur[cur.length - 1]!.seq >= e.seq ? cur : [...cur, e].slice(-(paused ? EVENT_CAP * 2 : EVENT_CAP)))));

  const loadEarlier = async () => {
    const first = events[0]?.seq;
    if (!first) return;
    pause(); // reading history: keep it from being trimmed or pushed around
    setLoadingEarlier(true);
    try {
      const page = unwrap(await api.GET("/api/v1/events", { params: { query: { newest: true, before: first, limit: PAGE } } })).events ?? [];
      setHasEarlier(page.length === PAGE);
      // Reading history may exceed the cap up to twice its size; following trims back to it.
      setEvents((cur) => mergeSorted(page as ApiEvent[], cur).slice(0, EVENT_CAP * 2));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoadingEarlier(false);
    }
  };

  const filtered = useMemo(() => {
    const q = search.q?.toLowerCase();
    return events.toReversed().filter(
      (e) =>
        e.seq <= shownUpTo &&
        (!search.level || e.level === search.level || (search.level === "warn" && e.level === "warning")) &&
        (!search.kind || e.kind.split(".")[0] === search.kind) &&
        (!search.scale_set || e.scale_set === search.scale_set) &&
        (!q || `${e.message} ${e.kind} ${e.environment_id ?? ""} ${e.job_id ?? ""}`.toLowerCase().includes(q)),
    );
  }, [events, search, shownUpTo]);
  const pending = paused ? events.filter((e) => e.seq > shownUpTo).length : 0;
  const scaleSets = Object.fromEntries([...new Set(events.map((e) => e.scale_set).filter(Boolean) as string[])].sort().map((s) => [s, s]));

  const scroller = useRef<HTMLDivElement>(null);
  const v = useVirtualizer({ count: filtered.length, getScrollElement: () => scroller.current, estimateSize: () => 34, overscan: 15, getItemKey: (i) => filtered[i]!.seq });

  const pause = () => {
    if (paused) return;
    setPaused(true);
    setShownUpTo(events.at(-1)?.seq ?? 0);
  };
  const togglePause = () => {
    if (!paused) return pause();
    setPaused(false);
    setShownUpTo(Infinity);
    scroller.current?.scrollTo({ top: 0 });
  };

  return (
    <Page
      title={t("environments.liveLogs.title")}
      description={t("environments.liveLogs.description")}
      actions={
        <Button variant="secondary" icon={paused ? PlayIcon : PauseIcon} onClick={togglePause}>
          {paused ? t("environments.liveLogs.resume") : t("environments.liveLogs.pause")}
        </Button>
      }
    >
      <ListToolbar
        search={search.q ?? ""}
        onSearch={(q) => set({ q: q || undefined })}
        searchLabel={t("environments.liveLogs.search")}
        filters={[
          { key: "level", label: t("environments.liveLogs.filter.level"), value: search.level, options: levelOptions() },
          { key: "kind", label: t("environments.liveLogs.filter.kind"), value: search.kind, options: kindOptions() },
          { key: "scale_set", label: t("environments.liveLogs.filter.scaleSet"), value: search.scale_set, options: scaleSets },
        ]}
        onFilter={(key, value) => set({ [key]: value })}
        onClear={() => void navigate({ to: "/logs", search: {}, replace: true })}
      >
        <span className="text-sm text-kumo-subtle" aria-live="polite">
          {paused ? t("environments.liveLogs.paused", { count: pending, n: formatNumber(pending) }) : t("environments.liveLogs.count", { count: filtered.length, n: formatNumber(filtered.length) })}
        </span>
      </ListToolbar>
      {error && <p className="text-sm text-kumo-danger">{error}</p>}
      <LayerCard>
        <LayerCard.Primary className="p-0">
          <div
            ref={scroller}
            role="log"
            aria-live="off"
            className="h-[65vh] min-h-80 overflow-auto"
            // Newest events are inserted at the top: reading further down pauses so rows stay put.
            onScroll={(e) => e.currentTarget.scrollTop > 0 && pause()}
          >
            {filtered.length === 0 ? (
              <p className="p-4 text-sm text-kumo-subtle">{t("environments.liveLogs.empty")}</p>
            ) : (
              <div role="list" style={{ height: v.getTotalSize(), position: "relative" }}>
                {v.getVirtualItems().map((item) => (
                  <div key={item.key} data-index={item.index} ref={v.measureElement} style={{ position: "absolute", top: 0, left: 0, right: 0, transform: `translateY(${item.start}px)` }}>
                    <Row e={filtered[item.index]!} />
                  </div>
                ))}
              </div>
            )}
          </div>
          {hasEarlier && (
            <div className="flex items-center gap-2 border-t border-kumo-line px-3 py-1.5 text-sm text-kumo-subtle">
              {t("environments.liveLogs.olderNotLoaded")}
              <Button size="xs" variant="secondary" icon={ArrowDownIcon} loading={loadingEarlier} onClick={loadEarlier}>
                {t("environments.liveLogs.loadOlder")}
              </Button>
            </div>
          )}
        </LayerCard.Primary>
      </LayerCard>
    </Page>
  );
}
