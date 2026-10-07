import { Tabs } from "@cloudflare/kumo";
import { useNavigate } from "@tanstack/react-router";
import type { DetailSearch } from "@/router";

/** Underline tabs bound to the ?tab= search parameter. */
export function DetailTabs({ tabs, value }: { tabs: { value: string; label: string }[]; value: string }) {
  const navigate = useNavigate();
  return (
    <div className="-mt-2 overflow-x-auto border-b border-kumo-line">
      <Tabs
        variant="underline"
        tabs={tabs}
        value={value}
        onValueChange={(v) => void navigate({ to: ".", search: (prev: DetailSearch) => ({ ...prev, tab: v === tabs[0]?.value ? undefined : v }), replace: true })}
      />
    </div>
  );
}
