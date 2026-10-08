import { createContext, Fragment, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { en, messagesFor } from "./en";
import { locales, type Locale } from "./locales";

export { locales, localeNames, type Locale } from "./locales";
export type Messages = typeof en;
type Plural = { one: string; other: string; zero?: string; two?: string; few?: string; many?: string };
type Leaves<T, P extends string = ""> = {
  [K in keyof T & string]: T[K] extends string ? `${P}${K}` : T[K] extends Plural ? `${P}${K}` : Leaves<T[K], `${P}${K}.`>;
}[keyof T & string];
export type Key = Leaves<Messages>;
export type Params = Record<string, string | number>;

const catalogs = Object.fromEntries(locales.map((l) => [l, messagesFor(l)])) as Record<Locale, Messages>;

// The language in use, for code outside React (formatters, tooltips). The provider sets
// it before its tree renders; I18nRemount remounts the app when it changes.
let active: Locale = "en";
// The locale numbers and dates are written in: the chosen language in the browser's
// region when it has one (es-MX, en-GB), else the language itself.
let activeFormat: string = "en";
// Set by a language switch: the language menu takes the focus back when it remounts.
let refocusLanguageMenu = false;

/** The language in use. */
export function currentLocale(): Locale {
  return active;
}

/** The locale for numbers and dates (the chosen language, in the browser's region). */
export function currentFormatLocale(): string {
  return activeFormat;
}

/** The first browser language in the chosen language (with its region), else the language. */
export function formatLocale(locale: Locale, languages: readonly string[]): string {
  const primary = locale.split("-")[0];
  return languages.find((tag) => tag.toLowerCase().split("-")[0] === primary) ?? locale;
}

function activate(l: Locale) {
  active = l;
  activeFormat = formatLocale(l, browserLanguages());
}

function browserLanguages(): readonly string[] {
  return typeof navigator === "undefined" ? [] : (navigator.languages ?? [navigator.language]);
}

/** True once after a language switch, for the menu that made it. */
export function takeLanguageMenuFocus(): boolean {
  const take = refocusLanguageMenu;
  refocusLanguageMenu = false;
  return take;
}

/** Translates outside React, in the language in use. */
export function tr(key: Key, params?: Params): string {
  return translate(catalogs[active], en, active, key, params);
}
const STORAGE = "ghrm.locale";

export function detectLocale(stored: string | null, languages: readonly string[]): Locale {
  if (stored && (locales as readonly string[]).includes(stored)) return stored as Locale;
  for (const tag of languages) {
    const primary = tag.toLowerCase().split("-")[0];
    if (primary === "pt") return "pt-BR";
    const hit = locales.find((l) => l === primary);
    if (hit) return hit;
  }
  return "en";
}

const rulesCache = new Map<string, Intl.PluralRules>();
function pluralRules(locale: string) {
  let r = rulesCache.get(locale);
  if (!r) rulesCache.set(locale, (r = new Intl.PluralRules(locale)));
  return r;
}

function lookup(messages: unknown, key: string): unknown {
  return key.split(".").reduce<unknown>((node, part) => (node && typeof node === "object" ? (node as Record<string, unknown>)[part] : undefined), messages);
}

export function translate(messages: Messages, fallback: Messages, locale: Locale, key: Key, params: Params = {}): string {
  let entry = lookup(messages, key) ?? lookup(fallback, key);
  if (entry && typeof entry === "object") {
    const forms = entry as Plural;
    const n = Number(params.count ?? 0);
    // CLDR puts 0 with "one" in Portuguese; in Brazil zero takes the plural ("0 ambientes").
    const rule = (locale === "pt-BR" && n === 0 ? "other" : pluralRules(locale).select(n)) as keyof Plural;
    entry = (n === 0 && forms.zero) || forms[rule] || forms.other;
  }
  if (typeof entry !== "string") return key;
  return entry.replace(/\{(\w+)\}/g, (m, name: string) => (name in params ? String(params[name]) : m));
}

type Ctx = { locale: Locale; setLocale: (l: Locale) => void; t: (key: Key, params?: Params) => string };
const I18nContext = createContext<Ctx | null>(null);

function readStored(): string | null {
  try {
    return localStorage.getItem(STORAGE);
  } catch {
    return null;
  }
}

export function I18nProvider({ children, initial }: { children: ReactNode; initial?: Locale }) {
  const [locale, setState] = useState<Locale>(() => {
    activate(initial ?? detectLocale(readStored(), browserLanguages()));
    return active;
  });
  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);
  // Without a provider, code outside React speaks English again.
  useEffect(() => () => activate("en"), []);
  const setLocale = useCallback((l: Locale) => {
    try {
      localStorage.setItem(STORAGE, l);
    } catch {
      /* private mode: the choice lasts for this page */
    }
    document.documentElement.lang = l;
    activate(l);
    refocusLanguageMenu = true;
    setState(l);
  }, []);
  const value = useMemo<Ctx>(() => ({ locale, setLocale, t: (key, params) => translate(catalogs[locale], en, locale, key, params) }), [locale, setLocale]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

/** Remounts its children on a language change, so text formatted outside React follows.
 * Wrap only the pages: the data, the event stream and the toasts live above it. */
export function I18nRemount({ children }: { children: ReactNode }) {
  const { locale } = useI18n();
  return <Fragment key={locale}>{children}</Fragment>;
}

export function useI18n(): Ctx {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error("useI18n outside I18nProvider");
  return ctx;
}

export const useT = () => useI18n().t;
