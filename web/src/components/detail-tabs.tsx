import type { ReactNode } from "react";
import { Tabs } from "@cloudflare/kumo";
import { useNavigate } from "@tanstack/react-router";
import type { DetailSearch } from "@/router";

/** Underline tabs bound to the ?tab= search parameter. With push, each switch is a
 * browser history entry (list pages: Back returns to the other tab). */
export function DetailTabs({
  tabs,
  value,
  push = false,
  defaultValue = tabs[0]?.value,
  children,
}: {
  tabs: { value: string; label: string }[];
  value: string;
  push?: boolean;
  /** The tab shown without ?tab= (the first one unless said otherwise). */
  defaultValue?: string;
  /** The selected tab's content, in a tab panel named after it. */
  children?: ReactNode;
}) {
  const navigate = useNavigate();
  const label = tabs.find((t) => t.value === value)?.label;
  return (
    <>
      <div className="-mt-2 overflow-x-auto border-b border-kumo-line">
        <Tabs
          variant="underline"
          tabs={tabs}
          value={value}
          onValueChange={(v) => void navigate({ to: ".", search: (prev: DetailSearch & { page?: number }) => ({ ...prev, page: undefined, tab: v === defaultValue ? undefined : v }), replace: !push })}
        />
      </div>
      <div role="tabpanel" aria-label={label} className="flex flex-col gap-6">
        {children}
      </div>
    </>
  );
}
