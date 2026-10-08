export const locales = ["en", "pt-BR", "es", "fr", "it"] as const;
export type Locale = (typeof locales)[number];

/** Each language in its own words, for the language menu. */
export const localeNames: Record<Locale, string> = {
  en: "English",
  "pt-BR": "Português (Brasil)",
  es: "Español",
  fr: "Français",
  it: "Italiano",
};
