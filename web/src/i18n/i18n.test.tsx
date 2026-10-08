import { act, render, screen } from "@testing-library/react";
import { currentLocale, detectLocale, formatLocale, I18nProvider, I18nRemount, translate, useI18n } from "./index";
import { en } from "./en";
import { formatRelative } from "@/lib/format";

test("detects the browser language, regional variants included", () => {
  expect(detectLocale(null, ["pt-BR", "en"])).toBe("pt-BR");
  expect(detectLocale(null, ["pt-PT"])).toBe("pt-BR");
  expect(detectLocale(null, ["es-MX"])).toBe("es");
  expect(detectLocale(null, ["fr-CA"])).toBe("fr");
  expect(detectLocale(null, ["it-IT"])).toBe("it");
  expect(detectLocale(null, ["de-DE", "fr"])).toBe("fr");
  expect(detectLocale(null, ["de-DE"])).toBe("en");
});

test("a stored choice wins; an invalid stored locale is ignored", () => {
  expect(detectLocale("it", ["pt-BR"])).toBe("it");
  expect(detectLocale("de", ["pt-BR"])).toBe("pt-BR");
  expect(detectLocale("{garbage", [])).toBe("en");
});

const sample = { greet: "Hello, {name}", jobs: { one: "{count} job", other: "{count} jobs" } } as const;

test("interpolation and plural rules per locale", () => {
  const fr = { greet: "Bonjour, {name}", jobs: { one: "{count} job", other: "{count} jobs" } };
  expect(translate(sample as never, sample as never, "en", "greet" as never, { name: "Ana" })).toBe("Hello, Ana");
  expect(translate(sample as never, sample as never, "en", "jobs" as never, { count: 0 })).toBe("0 jobs");
  expect(translate(fr as never, sample as never, "fr", "jobs" as never, { count: 0 })).toBe("0 job"); // French: 0 is singular
  expect(translate(fr as never, sample as never, "fr", "jobs" as never, { count: 2 })).toBe("2 jobs");
});

test("falls back to English for a missing key", () => {
  expect(translate({} as never, sample as never, "pt-BR", "greet" as never, { name: "Ana" })).toBe("Hello, Ana");
});

function Probe() {
  const { t, setLocale, locale } = useI18n();
  return (
    <button onClick={() => setLocale("pt-BR")}>
      {locale}:{t("common.loading")}
    </button>
  );
}

test("switching the language re-renders, stores the choice and sets html lang", async () => {
  localStorage.clear();
  render(
    <I18nProvider initial="en">
      <Probe />
    </I18nProvider>,
  );
  expect(screen.getByRole("button")).toHaveTextContent(`en:${en.common.loading}`);
  act(() => screen.getByRole("button").click());
  expect(screen.getByRole("button")).toHaveTextContent(/^pt-BR:/);
  expect(localStorage.getItem("ghrm.locale")).toBe("pt-BR");
  expect(document.documentElement.lang).toBe("pt-BR");
});

const fiveMinutesAgo = new Date(Date.now() - 5 * 60_000).toISOString();

function Plain() {
  // Uses no hook: only the remount on a language change updates it.
  return <span data-testid="plain">{formatRelative(fiveMinutesAgo)}</span>;
}

function Switch() {
  const { setLocale } = useI18n();
  return <button onClick={() => setLocale("pt-BR")}>switch</button>;
}

test("text formatted outside React follows a language switch", () => {
  localStorage.clear();
  render(
    <I18nProvider initial="en">
      <I18nRemount>
        <Plain />
      </I18nRemount>
      <Switch />
    </I18nProvider>,
  );
  expect(screen.getByTestId("plain")).toHaveTextContent("5 min. ago");
  act(() => screen.getByRole("button", { name: "switch" }).click());
  expect(screen.getByTestId("plain")).toHaveTextContent("há 5 min.");
});

test("Brazilian Portuguese says zero in the plural", () => {
  const pt = { items: { one: "{count} ambiente", other: "{count} ambientes" } };
  expect(translate(pt as never, pt as never, "pt-BR", "items" as never, { count: 0 })).toBe("0 ambientes");
  expect(translate(pt as never, pt as never, "pt-BR", "items" as never, { count: 1 })).toBe("1 ambiente");
});

test("numbers and dates use the browser's region of the chosen language", () => {
  expect(formatLocale("es", ["es-MX", "en"])).toBe("es-MX");
  expect(formatLocale("en", ["en-GB"])).toBe("en-GB");
  expect(formatLocale("pt-BR", ["pt-PT"])).toBe("pt-PT");
  expect(formatLocale("fr", ["de-DE", "en-US"])).toBe("fr");
});

test("unmounting the provider forgets its language", () => {
  const { unmount } = render(
    <I18nProvider initial="it">
      <span />
    </I18nProvider>,
  );
  expect(currentLocale()).toBe("it");
  unmount();
  expect(currentLocale()).toBe("en");
});
