import { act, render, screen } from "@testing-library/react";
import { detectLocale, I18nProvider, translate, useI18n } from "./index";
import { en } from "./en";

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
