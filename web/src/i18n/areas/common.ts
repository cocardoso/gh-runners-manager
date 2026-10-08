import type { Locale } from "../locales";

const en = {
  loading: "Loading…",
  notYet: "not yet",
  language: "Language: {language}",
};

export const common: Record<Locale, typeof en> = {
  en,
  "pt-BR": {
    loading: "Carregando…",
    notYet: "ainda não",
    language: "Idioma: {language}",
  },
  es: {
    loading: "Cargando…",
    notYet: "todavía no",
    language: "Idioma: {language}",
  },
  fr: {
    loading: "Chargement…",
    notYet: "pas encore",
    language: "Langue : {language}",
  },
  it: {
    loading: "Caricamento…",
    notYet: "non ancora",
    language: "Lingua: {language}",
  },
};
