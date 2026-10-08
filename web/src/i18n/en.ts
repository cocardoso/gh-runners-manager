import { common } from "./areas/common";
import { environments } from "./areas/environments";
import { overview } from "./areas/overview";
import { settings } from "./areas/settings";
import { shell } from "./areas/shell";
import { templates } from "./areas/templates";

const areas = { common, shell, overview, environments, templates, settings };

/** The messages of one language, area by area (each area file holds every language). */
export function messagesFor(locale: keyof typeof common) {
  return {
    common: areas.common[locale],
    shell: areas.shell[locale],
    overview: areas.overview[locale],
    environments: areas.environments[locale],
    templates: areas.templates[locale],
    settings: areas.settings[locale],
  };
}

/** English, the source of every key. */
export const en = messagesFor("en");
