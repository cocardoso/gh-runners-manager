import type { ReactNode } from "react";
import { Button, Popover } from "@cloudflare/kumo";
import { InfoIcon } from "@phosphor-icons/react";
import { useT } from "@/i18n";

/** A "?" next to a label that explains where a value is used. It opens on a click or a tap, as a phone has no hover. */
export function HelpTip({ text }: { text: ReactNode }) {
  const t = useT();
  return (
    <Popover>
      <Popover.Trigger render={<Button type="button" variant="ghost" size="xs" shape="square" icon={InfoIcon} aria-label={t("shell.help.more")} />} />
      <Popover.Content>
        <Popover.Description className="block max-w-xs text-sm">{text}</Popover.Description>
      </Popover.Content>
    </Popover>
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

/** A field's label with its help, named by the label's text alone: Kumo's field label wraps
 * the help button too, which would read "Name More information". Pass a unique id. */
export function helpField(id: string, label: string, help: ReactNode) {
  return { label: <HelpLabel label={<span id={id}>{label}</span>} help={help} />, "aria-labelledby": id };
}
