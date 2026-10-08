import type { Locale } from "../locales";

const en = {
  loading: "Loading…",
  notYet: "not yet",
  justNow: "just now",
  language: "Language: {language}",
};

export const common: Record<Locale, typeof en> = {
  en,
  "pt-BR": {
    loading: "Carregando…",
    notYet: "ainda não",
    justNow: "agora mesmo",
    language: "Idioma: {language}",
  },
  es: {
    loading: "Cargando…",
    notYet: "todavía no",
    justNow: "justo ahora",
    language: "Idioma: {language}",
  },
  fr: {
    loading: "Chargement…",
    notYet: "pas encore",
    justNow: "à l'instant",
    language: "Langue : {language}",
  },
  it: {
    loading: "Caricamento…",
    notYet: "non ancora",
    justNow: "proprio ora",
    language: "Lingua: {language}",
  },
};
