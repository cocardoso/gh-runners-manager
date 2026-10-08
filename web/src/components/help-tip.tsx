import type { ReactNode } from "react";
import { Button, Tooltip } from "@cloudflare/kumo";
import { InfoIcon } from "@phosphor-icons/react";
import { useT } from "@/i18n";

/** A "?" next to a label that explains where a value is used. */
export function HelpTip({ text }: { text: ReactNode }) {
  const t = useT();
  return (
    <Tooltip
      content={<span className="block max-w-xs">{text}</span>}
      render={<Button type="button" variant="ghost" size="xs" shape="square" icon={InfoIcon} aria-label={t("shell.help.more")} />}
    />
  );
}

/** A field label with its help. */
export function HelpLabel({ label, help }: { label: ReactNode; help: ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1">
      {label}
      <HelpTip text={help} />
    </span>
  );
}
