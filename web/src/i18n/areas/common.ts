import type { Locale } from "../locales";

const en = {
  loading: "Loading…",
  notYet: "not yet",
  justNow: "just now",
  language: "Language: {language}",
  view: { label: "View", cards: "Cards", list: "List" },
  details: "Details",
};

export const common: Record<Locale, typeof en> = {
  en,
  "pt-BR": {
    loading: "Carregando…",
    notYet: "ainda não",
    justNow: "agora mesmo",
    language: "Idioma: {language}",
    view: { label: "Visualização", cards: "Cards", list: "Lista" },
    details: "Detalhes",
  },
  es: {
    loading: "Cargando…",
    notYet: "todavía no",
    justNow: "justo ahora",
    language: "Idioma: {language}",
    view: { label: "Vista", cards: "Tarjetas", list: "Lista" },
    details: "Detalles",
  },
  fr: {
    loading: "Chargement…",
    notYet: "pas encore",
    justNow: "à l'instant",
    language: "Langue : {language}",
    view: { label: "Affichage", cards: "Cartes", list: "Liste" },
    details: "Détails",
  },
  it: {
    loading: "Caricamento…",
    notYet: "non ancora",
    justNow: "proprio ora",
    language: "Lingua: {language}",
    view: { label: "Vista", cards: "Schede", list: "Elenco" },
    details: "Dettagli",
  },
};
