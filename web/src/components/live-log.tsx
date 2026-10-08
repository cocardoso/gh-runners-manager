import { useDeferredValue, useMemo, useState } from "react";
import { Button, InputGroup, Tabs, Tooltip, cn, useKumoToastManager } from "@cloudflare/kumo";
import {
  ArrowsInLineHorizontalIcon,
  ClockIcon,
  CopyIcon,
  DownloadSimpleIcon,
  ListNumbersIcon,
  MagnifyingGlassIcon,
  PauseIcon,
  PlayIcon,
  CaretUpIcon,
  CaretDownIcon,
} from "@phosphor-icons/react";
import type { Icon } from "@phosphor-icons/react";
import { api, unwrap } from "@/api/client";
import { tr } from "@/i18n";
import { stripAnsi } from "@/lib/ansi";
import { formatNumber } from "@/lib/format";
import { useLogStream, type LogStreamName } from "@/lib/use-log-stream";
import { LogView } from "./log-view";

/** Reads a whole stream through the REST API, page by page, as raw text. */
async function readAll(envId: string, stream: LogStreamName): Promise<string[]> {
  const parts: string[] = [];
  let offset = 0;
  for (let page = 0; page < 1000; page++) {
    const res = unwrap(await api.GET("/api/v1/environments/{id}/logs/{stream}", { params: { path: { id: envId, stream }, query: { offset, limit: 10000 } } }));
    const entries = res.entries ?? [];
    for (const e of entries) parts.push(`${e.time}\t${stripAnsi(e.text)}\n`);
    if (entries.length === 0 || res.next <= offset) break;
    offset = res.next;
  }
  return parts;
}

function ToggleButton({ pressed, onClick, label, icon }: { pressed: boolean; onClick: () => void; label: string; icon: Icon }) {
  return (
    <Tooltip
      content={label}
      render={
        <Button
          variant={pressed ? "secondary" : "ghost"}
          size="sm"
          shape="square"
          icon={icon}
          aria-label={label}
          aria-pressed={pressed}
          onClick={onClick}
        />
      }
    />
  );
}

export interface LiveLogProps {
  envId: string;
  streams: LogStreamName[];
  defaultStream: LogStreamName;
  /** Whether the environment can still write lines (false once destroyed). */
  live: boolean;
  /** Search to start with (for example a step name). */
  initialSearch?: string;
  className?: string;
  createEventSource?: (url: string) => EventSource;
}

/** A log panel: stream tabs, follow/pause, search, wrap, timestamps, copy and download. */
export function LiveLog({ envId, streams, defaultStream, live, initialSearch = "", className, createEventSource }: LiveLogProps) {
  // tr, not useT: the viewer also renders outside the i18n provider, as in its tests.
  const t = tr;
  const toast = useKumoToastManager();
  const [stream, setStream] = useState<LogStreamName>(defaultStream);
  const [follow, setFollow] = useState(true);
  const [wrap, setWrap] = useState(false);
  const [timestamps, setTimestamps] = useState(false);
  const [numbers, setNumbers] = useState(true);
  const [query, setQuery] = useState(initialSearch);
  // -1 means no match is focused (following the tail).
  const [matchIndex, setMatchIndex] = useState(initialSearch ? 0 : -1);
  const [downloading, setDownloading] = useState(false);
  const search = useDeferredValue(query.trim());

  const log = useLogStream({ envId, stream, follow: follow && live && !(search && matchIndex >= 0), createEventSource });
  const matches = useMemo(() => {
    if (!search) return [];
    const q = search.toLowerCase();
    const out: number[] = [];
    log.lines.forEach((l, i) => {
      if (stripAnsi(l.text).toLowerCase().includes(q)) out.push(i);
    });
    return out;
  }, [log.lines, search]);
  const current = matches.length && matchIndex >= 0 ? Math.min(matchIndex, matches.length - 1) : -1;

  const step = (d: number) => {
    if (!matches.length) return;
    setFollow(false);
    setMatchIndex(current < 0 ? (d > 0 ? 0 : matches.length - 1) : (current + d + matches.length) % matches.length);
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(log.lines.map((l) => stripAnsi(l.text)).join("\n"));
      toast.add({ title: t("environments.log.copied", { count: log.lines.length, n: formatNumber(log.lines.length) }) });
    } catch {
      toast.add({ title: t("environments.log.copyFailed"), variant: "error" });
    }
  };

  const download = async () => {
    setDownloading(true);
    try {
      const parts = await readAll(envId, stream);
      const url = URL.createObjectURL(new Blob(parts, { type: "text/plain" }));
      const a = document.createElement("a");
      a.href = url;
      a.download = `${envId}-${stream}.log`;
      a.click();
      // Revoking in the same task can cancel the download in some browsers.
      setTimeout(() => URL.revokeObjectURL(url), 0);
    } catch (e) {
      toast.add({ title: t("environments.log.downloadFailed"), description: e instanceof Error ? e.message : String(e), variant: "error" });
    } finally {
      setDownloading(false);
    }
  };

  const state = live ? log.state : log.state === "loading" || log.state === "error" ? log.state : "ended";
  const dot =
    state === "live" ? "bg-kumo-success" : state === "reconnecting" || state === "error" ? "bg-kumo-danger" : state === "paused" ? "bg-kumo-warning" : "bg-kumo-inactive";

  return (
    <div className={cn("flex min-h-0 flex-col gap-2", className)}>
      <div className="flex flex-wrap items-center gap-2">
        <Tabs
          size="sm"
          tabs={streams.map((s) => ({ value: s, label: s }))}
          value={stream}
          onValueChange={(v) => {
            setStream(v as LogStreamName);
            setFollow(true);
            setMatchIndex(-1);
          }}
          className="max-w-full overflow-x-auto"
        />
        <span className="flex items-center gap-1.5 text-sm text-kumo-subtle" role="status">
          <span className={cn("size-2 rounded-full", dot)} />
          {t(`environments.log.state.${state}`)} · {t("environments.log.lines", { count: log.lines.length, n: formatNumber(log.lines.length) })}
        </span>
        <div className="ml-auto flex flex-wrap items-center gap-1">
          <InputGroup size="sm" className="w-48 sm:w-60">
            <InputGroup.Addon>
              <MagnifyingGlassIcon />
            </InputGroup.Addon>
            <InputGroup.Input
              type="search"
              aria-label={t("environments.log.searchLabel")}
              placeholder={t("environments.log.searchPlaceholder")}
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setMatchIndex(0);
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") step(e.shiftKey ? -1 : 1);
              }}
            />
            {search && (
              <InputGroup.Suffix className="whitespace-nowrap text-xs tabular-nums">
                {matches.length ? t("environments.log.matchOf", { current: formatNumber(current + 1), total: formatNumber(matches.length) }) : t("environments.log.noMatches")}
              </InputGroup.Suffix>
            )}
          </InputGroup>
          <Button variant="ghost" size="sm" shape="square" icon={CaretUpIcon} aria-label={t("environments.log.previousMatch")} disabled={!matches.length} onClick={() => step(-1)} />
          <Button variant="ghost" size="sm" shape="square" icon={CaretDownIcon} aria-label={t("environments.log.nextMatch")} disabled={!matches.length} onClick={() => step(1)} />
          {live && (
            <Button variant="secondary" size="sm" icon={follow ? PauseIcon : PlayIcon} onClick={() => setFollow(!follow)}>
              {follow ? t("environments.log.pause") : t("environments.log.resume")}
            </Button>
          )}
          <ToggleButton pressed={wrap} onClick={() => setWrap(!wrap)} label={t("environments.log.wrap")} icon={ArrowsInLineHorizontalIcon} />
          <ToggleButton pressed={timestamps} onClick={() => setTimestamps(!timestamps)} label={t("environments.log.timestamps")} icon={ClockIcon} />
          <ToggleButton pressed={numbers} onClick={() => setNumbers(!numbers)} label={t("environments.log.lineNumbers")} icon={ListNumbersIcon} />
          <Tooltip content={t("environments.log.copyTooltip")} render={<Button variant="ghost" size="sm" shape="square" icon={CopyIcon} aria-label={t("environments.log.copy")} onClick={copy} />} />
          <Tooltip
            content={t("environments.log.downloadTooltip")}
            render={<Button variant="ghost" size="sm" shape="square" icon={DownloadSimpleIcon} aria-label={t("environments.log.download")} loading={downloading} onClick={download} />}
          />
        </div>
      </div>
      {log.error && <p className="text-sm text-kumo-danger">{log.error}</p>}
      <LogView
        className="h-[60vh] min-h-80"
        lines={log.lines}
        follow={follow && live && current < 0}
        canFollow={live}
        onFollowChange={(f) => {
          setFollow(f);
          if (f) setMatchIndex(-1);
        }}
        wrap={wrap}
        showTimestamps={timestamps}
        showLineNumbers={numbers}
        search={search}
        focusLine={current >= 0 ? matches[current] : undefined}
        firstLineNumber={log.firstLine}
        hasEarlier={log.hasEarlier}
        loadingEarlier={log.loadingEarlier}
        onLoadEarlier={log.loadEarlier}
        dropped={log.dropped}
        empty={log.state === "loading" ? t("environments.log.loading") : live ? t("environments.log.waiting") : t("environments.log.noLines")}
      />
    </div>
  );
}
