import { useSyncExternalStore, type ReactNode } from "react";
import { Breadcrumbs, Empty, Loader, Tooltip, cn } from "@cloudflare/kumo";
import { HouseIcon, WarningCircleIcon } from "@phosphor-icons/react";
import { PageHeader } from "@/blocks/page-header/page-header";
import { formatAbsolute, formatRelative } from "@/lib/format";

let tick = Date.now();
const subscribers = new Set<() => void>();
setInterval(() => {
  tick = Date.now();
  subscribers.forEach((fn) => fn());
}, 15_000);

function subscribeTick(fn: () => void) {
  subscribers.add(fn);
  return () => {
    subscribers.delete(fn);
  };
}

/** The current time, refreshed every 15 s, shared by all relative timestamps. */
export function useNow(): Date {
  const t = useSyncExternalStore(subscribeTick, () => tick);
  return new Date(t);
}

export function RelativeTime({ value, className }: { value: string | undefined; className?: string }) {
  const now = useNow();
  return (
    <Tooltip
      content={formatAbsolute(value)}
      render={
        <time dateTime={value} className={cn("whitespace-nowrap", className)}>
          {formatRelative(value, now)}
        </time>
      }
    />
  );
}

/** One line of text that truncates, with the full value in a tooltip. */
export function Truncate({ text, className }: { text: string; className?: string }) {
  return <Tooltip content={text} render={<span className={cn("block min-w-0 truncate", className)}>{text}</span>} />;
}

export interface Crumb {
  label: string;
  href?: string;
}

export function Page({
  title,
  description,
  crumbs = [],
  actions,
  children,
}: {
  title: string;
  description?: string;
  crumbs?: Crumb[];
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="mx-auto flex w-full max-w-[1400px] flex-col gap-6">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
        <PageHeader
          className="min-w-0 flex-1"
          breadcrumbs={
            <Breadcrumbs className="pb-2">
              <Breadcrumbs.Link href="/" icon={<HouseIcon size={16} />}>
                gh-runners-manager
              </Breadcrumbs.Link>
              {crumbs.map((c) => (
                <span key={c.label} className="contents">
                  <Breadcrumbs.Separator />
                  {c.href ? <Breadcrumbs.Link href={c.href}>{c.label}</Breadcrumbs.Link> : null}
                </span>
              ))}
              <Breadcrumbs.Separator />
              <Breadcrumbs.Current>
                <span className="block max-w-[40vw] truncate sm:max-w-md" title={title}>
                  {title}
                </span>
              </Breadcrumbs.Current>
            </Breadcrumbs>
          }
          title={title}
          description={description}
        />
        {actions && <div className="flex shrink-0 flex-wrap items-center gap-2 pb-3 pl-3 sm:pl-0">{actions}</div>}
      </div>
      {children}
    </div>
  );
}

export function Loading({ label = "Loading…" }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 p-6 text-kumo-subtle" role="status">
      <Loader /> {label}
    </div>
  );
}

export function ErrorState({ error }: { error: unknown }) {
  return (
    <Empty
      icon={<WarningCircleIcon size={40} className="text-kumo-danger" />}
      title="Could not load this data"
      description={error instanceof Error ? error.message : String(error)}
    />
  );
}
