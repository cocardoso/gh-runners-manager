import { Tabs } from "@cloudflare/kumo";
import { useNavigate } from "@tanstack/react-router";
import type { DetailSearch } from "@/router";

/** Underline tabs bound to the ?tab= search parameter. With push, each switch is a
 * browser history entry (list pages: Back returns to the other tab). */
export function DetailTabs({ tabs, value, push = false }: { tabs: { value: string; label: string }[]; value: string; push?: boolean }) {
  const navigate = useNavigate();
  return (
    <div className="-mt-2 overflow-x-auto border-b border-kumo-line">
      <Tabs
        variant="underline"
        tabs={tabs}
        value={value}
        onValueChange={(v) => void navigate({ to: ".", search: (prev: DetailSearch) => ({ ...prev, tab: v === tabs[0]?.value ? undefined : v }), replace: !push })}
      />
    </div>
  );
}
