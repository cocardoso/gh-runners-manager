import { Button, Tooltip } from "@cloudflare/kumo";
import { ListBulletsIcon, SquaresFourIcon } from "@phosphor-icons/react";
import type { ViewMode } from "@/lib/view-mode";
import { useT } from "@/i18n";

/** Switches a list page between cards and a compact list. */
export function ViewToggle({ value, onChange }: { value: ViewMode; onChange: (mode: ViewMode) => void }) {
  const t = useT();
  const options = [
    { mode: "cards" as const, icon: SquaresFourIcon, label: t("common.view.cards") },
    { mode: "list" as const, icon: ListBulletsIcon, label: t("common.view.list") },
  ];
  return (
    <div role="group" aria-label={t("common.view.label")} className="inline-flex shrink-0 gap-0.5 rounded-lg border border-kumo-line bg-kumo-base p-0.5">
      {options.map((o) => (
        <Tooltip
          key={o.mode}
          content={o.label}
          render={
            <Button
              size="sm"
              shape="square"
              variant={value === o.mode ? "secondary" : "ghost"}
              icon={o.icon}
              aria-label={o.label}
              aria-pressed={value === o.mode}
              onClick={() => onChange(o.mode)}
            />
          }
        />
      ))}
    </div>
  );
}
