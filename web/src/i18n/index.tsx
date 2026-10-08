import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { en } from "./en";
import { locales, type Locale } from "./locales";
import { ptBR } from "./pt-BR";
import { es } from "./es";
import { fr } from "./fr";
import { it } from "./it";

export { locales, localeNames, type Locale } from "./locales";
export type Messages = typeof en;
type Plural = { one: string; other: string; zero?: string; two?: string; few?: string; many?: string };
type Leaves<T, P extends string = ""> = {
  [K in keyof T & string]: T[K] extends string ? `${P}${K}` : T[K] extends Plural ? `${P}${K}` : Leaves<T[K], `${P}${K}.`>;
}[keyof T & string];
export type Key = Leaves<Messages>;
export type Params = Record<string, string | number>;

const catalogs: Record<Locale, Messages> = { en, "pt-BR": ptBR, es, fr, it };
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

function lookup(messages: unknown, key: string): unknown {
  return key.split(".").reduce<unknown>((node, part) => (node && typeof node === "object" ? (node as Record<string, unknown>)[part] : undefined), messages);
}

export function translate(messages: Messages, fallback: Messages, locale: Locale, key: Key, params: Params = {}): string {
  let entry = lookup(messages, key) ?? lookup(fallback, key);
  if (entry && typeof entry === "object") {
    const forms = entry as Plural;
    const rule = new Intl.PluralRules(locale).select(Number(params.count ?? 0)) as keyof Plural;
    entry = (Number(params.count) === 0 && forms.zero) || forms[rule] || forms.other;
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
  const [locale, setState] = useState<Locale>(() => initial ?? detectLocale(readStored(), navigator.languages ?? [navigator.language]));
  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);
  const setLocale = useCallback((l: Locale) => {
    try {
      localStorage.setItem(STORAGE, l);
    } catch {
      /* private mode: the choice lasts for this page */
    }
    document.documentElement.lang = l;
    setState(l);
  }, []);
  const value = useMemo<Ctx>(() => ({ locale, setLocale, t: (key, params) => translate(catalogs[locale], en, locale, key, params) }), [locale, setLocale]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): Ctx {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error("useI18n outside I18nProvider");
  return ctx;
}

export const useT = () => useI18n().t;
