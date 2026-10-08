import { Button, DropdownMenu, Tooltip } from "@cloudflare/kumo";
import { TranslateIcon } from "@phosphor-icons/react";
import { localeNames, locales, useI18n, type Locale } from "@/i18n";

/** Chooses the interface language (stored in this browser). */
export function LanguageMenu() {
  const { locale, setLocale, t } = useI18n();
  const label = t("common.language", { language: localeNames[locale] });
  return (
    <DropdownMenu>
      <Tooltip content={label} render={<DropdownMenu.Trigger render={<Button variant="ghost" shape="square" icon={TranslateIcon} aria-label={label} />} />} />
      <DropdownMenu.Content>
        <DropdownMenu.RadioGroup value={locale} onValueChange={(v) => setLocale(v as Locale)}>
          {locales.map((l) => (
            <DropdownMenu.RadioItem key={l} value={l} lang={l}>
              {localeNames[l]}
            </DropdownMenu.RadioItem>
          ))}
        </DropdownMenu.RadioGroup>
      </DropdownMenu.Content>
    </DropdownMenu>
  );
}
