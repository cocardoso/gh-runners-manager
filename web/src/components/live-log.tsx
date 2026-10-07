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
import { stripAnsi } from "@/lib/ansi";
import { useLogStream, type LogStreamName, type LogStreamState } from "@/lib/use-log-stream";
import { LogView } from "./log-view";

const stateLabel: Record<LogStreamState, string> = {
  loading: "Loading",
  connecting: "Connecting",
  live: "Live",
  reconnecting: "Reconnecting",
  paused: "Paused",
  ended: "Ended",
  error: "Error",
};

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
  const toast = useKumoToastManager();
  const [stream, setStream] = useState<LogStreamName>(defaultStream);
  const [follow, setFollow] = useState(true);
  const [wrap, setWrap] = useState(false);
  const [timestamps, setTimestamps] = useState(false);
  const [numbers, setNumbers] = useState(true);
  const [query, setQuery] = useState(initialSearch);
  const [matchIndex, setMatchIndex] = useState(0);
  const [downloading, setDownloading] = useState(false);
  const search = useDeferredValue(query.trim());

  const log = useLogStream({ envId, stream, follow: follow && live, createEventSource });
  const matches = useMemo(() => {
    if (!search) return [];
    const q = search.toLowerCase();
    const out: number[] = [];
    log.lines.forEach((l, i) => {
      if (stripAnsi(l.text).toLowerCase().includes(q)) out.push(i);
    });
    return out;
  }, [log.lines, search]);
  const current = matches.length ? Math.min(matchIndex, matches.length - 1) : -1;

  const step = (d: number) => {
    if (!matches.length) return;
    setFollow(false);
    setMatchIndex((current + d + matches.length) % matches.length);
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(log.lines.map((l) => stripAnsi(l.text)).join("\n"));
      toast.add({ title: `Copied ${log.lines.length.toLocaleString()} lines` });
    } catch {
      toast.add({ title: "Could not copy to the clipboard", variant: "error" });
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
      URL.revokeObjectURL(url);
    } catch (e) {
      toast.add({ title: "Download failed", description: e instanceof Error ? e.message : String(e), variant: "error" });
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
            setMatchIndex(0);
          }}
          className="max-w-full overflow-x-auto"
        />
        <span className="flex items-center gap-1.5 text-sm text-kumo-subtle" role="status">
          <span className={cn("size-2 rounded-full", dot)} />
          {stateLabel[state]} · {log.lines.length.toLocaleString()} lines
        </span>
        <div className="ml-auto flex flex-wrap items-center gap-1">
          <InputGroup size="sm" className="w-48 sm:w-60">
            <InputGroup.Addon>
              <MagnifyingGlassIcon />
            </InputGroup.Addon>
            <InputGroup.Input
              type="search"
              aria-label="Search the log"
              placeholder="Search"
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
                {matches.length ? `${current + 1} of ${matches.length}` : "0 matches"}
              </InputGroup.Suffix>
            )}
          </InputGroup>
          <Button variant="ghost" size="sm" shape="square" icon={CaretUpIcon} aria-label="Previous match" disabled={!matches.length} onClick={() => step(-1)} />
          <Button variant="ghost" size="sm" shape="square" icon={CaretDownIcon} aria-label="Next match" disabled={!matches.length} onClick={() => step(1)} />
          {live && (
            <Button variant="secondary" size="sm" icon={follow ? PauseIcon : PlayIcon} onClick={() => setFollow(!follow)}>
              {follow ? "Pause" : "Resume"}
            </Button>
          )}
          <ToggleButton pressed={wrap} onClick={() => setWrap(!wrap)} label="Wrap lines" icon={ArrowsInLineHorizontalIcon} />
          <ToggleButton pressed={timestamps} onClick={() => setTimestamps(!timestamps)} label="Show timestamps" icon={ClockIcon} />
          <ToggleButton pressed={numbers} onClick={() => setNumbers(!numbers)} label="Show line numbers" icon={ListNumbersIcon} />
          <Tooltip content="Copy loaded lines" render={<Button variant="ghost" size="sm" shape="square" icon={CopyIcon} aria-label="Copy" onClick={copy} />} />
          <Tooltip
            content="Download the whole stream"
            render={<Button variant="ghost" size="sm" shape="square" icon={DownloadSimpleIcon} aria-label="Download" loading={downloading} onClick={download} />}
          />
        </div>
      </div>
      {log.error && <p className="text-sm text-kumo-danger">{log.error}</p>}
      <LogView
        className="h-[60vh] min-h-80"
        lines={log.lines}
        follow={follow && live && current < 0}
        canFollow={live}
        onFollowChange={setFollow}
        wrap={wrap}
        showTimestamps={timestamps}
        showLineNumbers={numbers}
        search={search}
        focusLine={current >= 0 ? matches[current] : undefined}
        hasEarlier={log.hasEarlier}
        loadingEarlier={log.loadingEarlier}
        onLoadEarlier={log.loadEarlier}
        dropped={log.dropped}
        empty={log.state === "loading" ? "Loading…" : live ? "Waiting for the first line…" : "This stream has no lines."}
      />
    </div>
  );
}
