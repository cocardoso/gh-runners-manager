import { memo, useEffect, useLayoutEffect, useRef, type ReactNode } from "react";
import { useVirtualizer, type Rect } from "@tanstack/react-virtual";
import { Button, cn } from "@cloudflare/kumo";
import { ArrowDownIcon, ArrowUpIcon } from "@phosphor-icons/react";
import type { LogEntry } from "@/api/client";
import { tr } from "@/i18n";
import { formatNumber } from "@/lib/format";
import { parseAnsi, stripAnsi, type AnsiSegment } from "@/lib/ansi";

export interface LogViewProps {
  lines: LogEntry[];
  /** Keep the newest line in view. */
  follow: boolean;
  onFollowChange: (follow: boolean) => void;
  /** Whether following is possible at all (false for a finished stream). */
  canFollow?: boolean;
  wrap: boolean;
  showTimestamps: boolean;
  showLineNumbers: boolean;
  search?: string;
  /** Index of the line to bring into view (search navigation). */
  focusLine?: number;
  hasEarlier?: boolean;
  loadingEarlier?: boolean;
  onLoadEarlier?: () => void;
  dropped?: number;
  className?: string;
  empty?: ReactNode;
  /** Line number of lines[0]. */
  firstLineNumber?: number;
  /** Initial viewport size, for tests and the first paint. */
  initialRect?: Rect;
}

const ROW = 20;

function segmentClass(s: AnsiSegment) {
  return cn(
    s.fg && `ansi-fg-${s.fg}`,
    s.bg && `ansi-bg-${s.bg}`,
    s.bold && "font-semibold",
    s.dim && "opacity-70",
    s.italic && "italic",
    s.underline && "underline",
  );
}

function highlight(text: string, query: string): ReactNode {
  const lower = text.toLowerCase();
  const q = query.toLowerCase();
  const parts: ReactNode[] = [];
  let at = 0;
  for (let i = lower.indexOf(q); i >= 0; i = lower.indexOf(q, i + q.length)) {
    if (i > at) parts.push(text.slice(at, i));
    parts.push(
      <mark key={i} className="rounded-sm bg-kumo-warning/40 text-inherit">
        {text.slice(i, i + q.length)}
      </mark>,
    );
    at = i + q.length;
  }
  if (at < text.length) parts.push(text.slice(at));
  return parts;
}

// The runner prefixes job log lines with its own ISO timestamp; the viewer has a timestamp column.
const RUNNER_TS = /^\uFEFF?\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z /;

/** Renders one log line: GitHub Actions workflow commands, ANSI colours and search highlights. */
function LineText({ text: raw, search }: { text: string; search?: string }) {
  const text = raw.replace(RUNNER_TS, "");
  const cmd = /^##\[(group|endgroup|error|warning|notice|debug|command)\](.*)$/.exec(stripAnsi(text));
  if (cmd) {
    const [, kind, rest = ""] = cmd;
    const cls = {
      group: "font-semibold text-kumo-default",
      endgroup: "text-kumo-inactive",
      error: "text-kumo-danger",
      warning: "text-kumo-warning",
      notice: "text-kumo-info",
      debug: "text-kumo-subtle",
      command: "text-kumo-link",
    }[kind!];
    if (kind === "endgroup") return <span className={cls}>{" "}</span>;
    return <span className={cls}>{search ? highlight(rest, search) : rest}</span>;
  }
  if (search && stripAnsi(text).toLowerCase().includes(search.toLowerCase())) return <>{highlight(stripAnsi(text), search)}</>;
  return (
    <>
      {parseAnsi(text).map((s, i) => {
        const cls = segmentClass(s);
        const style = s.fgRgb || s.bgRgb ? { color: s.fgRgb, backgroundColor: s.bgRgb } : undefined;
        return cls || style ? (
          <span key={i} className={cls} style={style}>
            {s.text}
          </span>
        ) : (
          <span key={i}>{s.text}</span>
        );
      })}
    </>
  );
}

const Row = memo(function Row({
  entry,
  number,
  showTimestamps,
  showLineNumbers,
  search,
  wrap,
  focused,
}: {
  entry: LogEntry;
  number: number;
  showTimestamps: boolean;
  showLineNumbers: boolean;
  search?: string;
  wrap: boolean;
  focused: boolean;
}) {
  return (
    <div role="listitem" className={cn("flex gap-3 px-3 hover:bg-kumo-tint", focused && "bg-kumo-tint")}>
      {showLineNumbers && <span className="w-12 shrink-0 select-none text-right text-kumo-inactive tabular-nums">{number}</span>}
      {showTimestamps && <span className="shrink-0 select-none text-kumo-subtle tabular-nums">{entry.time.slice(11, 23)}</span>}
      <span className={cn("min-w-0 flex-1", wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre")}>
        <LineText text={entry.text} search={search} />
      </span>
    </div>
  );
});

function nearBottom(el: HTMLElement) {
  return el.scrollHeight - el.scrollTop - el.clientHeight < 32;
}

/** A virtualized, ANSI-aware log viewer styled like Kumo's code blocks. */
export function LogView({
  lines,
  follow,
  onFollowChange,
  canFollow = true,
  wrap,
  showTimestamps,
  showLineNumbers,
  search,
  focusLine,
  hasEarlier,
  loadingEarlier,
  onLoadEarlier,
  dropped = 0,
  className,
  empty,
  initialRect,
  firstLineNumber = 1,
}: LogViewProps) {
  // tr, not useT: the viewer also renders outside the i18n provider, as in its tests.
  const t = tr;
  const scroller = useRef<HTMLDivElement>(null);
  const v = useVirtualizer({
    count: lines.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => ROW,
    overscan: 20,
    initialRect,
    getItemKey: (i) => lines[i]?.offset ?? i,
  });

  // Keep the newest line in view while following.
  useEffect(() => {
    if (follow && lines.length > 0) v.scrollToIndex(lines.length - 1, { align: "end" });
  }, [follow, lines, v]);

  // Keep the reader's place when earlier lines are prepended.
  const prevFirst = useRef<number | undefined>(undefined);
  useLayoutEffect(() => {
    const first = lines[0]?.offset;
    const before = prevFirst.current;
    prevFirst.current = first;
    if (before === undefined || first === undefined || first >= before || follow) return;
    const added = lines.findIndex((l) => l.offset === before);
    if (added > 0) v.scrollToIndex(added, { align: "start" });
  }, [lines, follow, v]);

  useEffect(() => {
    if (focusLine !== undefined && focusLine >= 0) v.scrollToIndex(focusLine, { align: "center" });
  }, [focusLine, v]);

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    const bottom = nearBottom(el);
    if (follow && !bottom) onFollowChange(false);
    else if (!follow && bottom && canFollow && el.scrollTop > 0) onFollowChange(true);
  };

  return (
    <div className={cn("relative flex min-h-0 flex-col overflow-hidden rounded-lg border border-kumo-line bg-kumo-recessed", className)}>
      {(hasEarlier || dropped > 0) && (
        <div className="flex flex-wrap items-center gap-2 border-b border-kumo-line px-3 py-1.5 text-sm text-kumo-subtle">
          {hasEarlier ? <span>{t("environments.log.earlierNotLoaded")}</span> : null}
          {dropped > 0 && <span>{t("environments.log.trimmed", { count: dropped, n: formatNumber(dropped) })}</span>}
          {hasEarlier && onLoadEarlier && (
            <Button size="xs" variant="secondary" icon={ArrowUpIcon} loading={loadingEarlier} onClick={onLoadEarlier}>
              {t("environments.log.loadEarlier")}
            </Button>
          )}
        </div>
      )}
      <div
        ref={scroller}
        role="log"
        aria-live="off"
        tabIndex={0}
        onScroll={onScroll}
        className="min-h-0 flex-1 overflow-auto font-mono text-[13px] leading-5 text-kumo-default focus:outline-none"
      >
        {lines.length === 0 ? (
          <div className="p-4 font-sans text-sm text-kumo-subtle">{empty ?? t("environments.log.noLinesYet")}</div>
        ) : (
          <div role="list" style={{ height: v.getTotalSize(), position: "relative", minWidth: "100%", width: wrap ? "100%" : "max-content" }}>
            {v.getVirtualItems().map((item) => {
              const entry = lines[item.index]!;
              return (
                <div
                  key={item.key}
                  data-index={item.index}
                  ref={wrap ? v.measureElement : undefined}
                  style={{ position: "absolute", top: 0, left: 0, right: 0, transform: `translateY(${item.start}px)`, minHeight: ROW }}
                >
                  <Row
                    entry={entry}
                    number={item.index + firstLineNumber}
                    showTimestamps={showTimestamps}
                    showLineNumbers={showLineNumbers}
                    search={search || undefined}
                    wrap={wrap}
                    focused={item.index === focusLine}
                  />
                </div>
              );
            })}
          </div>
        )}
      </div>
      {!follow && canFollow && lines.length > 0 && (
        <div className="pointer-events-none absolute inset-x-0 bottom-3 flex justify-center">
          <Button className="pointer-events-auto shadow-md" size="sm" variant="primary" icon={ArrowDownIcon} onClick={() => onFollowChange(true)}>
            {t("environments.log.jumpToLatest")}
          </Button>
        </div>
      )}
    </div>
  );
}
