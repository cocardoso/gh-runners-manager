import { en, messagesFor } from "./en";
import { translate, type Key, type Params } from "./index";
import type { Locale } from "./locales";

const say = (locale: Locale, key: Key, params?: Params) => translate(messagesFor(locale), en, locale, key, params);

test("one new item is singular in every language", () => {
  const want: Record<Locale, RegExp> = { en: /^1 new\b/, "pt-BR": /^1 novo\b/, es: /^1 nuevo\b/, fr: /^1 nouveau\b/, it: /^1 nuovo\b/ };
  for (const [locale, re] of Object.entries(want) as [Locale, RegExp][]) {
    for (const key of ["overview.jobs.pending", "environments.list.pending"] as Key[]) expect(say(locale, key, { count: 1, n: "1" })).toMatch(re);
    expect(say(locale, "environments.liveLogs.paused", { count: 1, n: "1" })).toMatch(new RegExp(re.source.replace("^", "· ")));
  }
  expect(say("pt-BR", "overview.jobs.pending", { count: 2, n: "2" })).toMatch(/^2 novos\b/);
});

test("a relative time reads naturally after the waiting and outage labels", () => {
  expect(say("pt-BR", "templates.scaleSets.since", { time: "há 5 min." })).toBe("Esperando há 5 min.");
  expect(say("en", "templates.scaleSets.since", { time: "5 min. ago" })).toBe("Since 5 min. ago");
  expect(say("pt-BR", "settings.cache.downSince", { time: "há 5 min." })).toBe("fora do ar há 5 min.,");
  for (const locale of ["pt-BR", "fr", "it"] as Locale[]) {
    expect(say(locale, "templates.scaleSets.since", { time: "X" })).not.toMatch(/^(Desde|Da|Depuis) X/);
  }
});

test("the delete warning needs no article before the resource type", () => {
  for (const locale of ["pt-BR", "es", "fr", "it"] as Locale[]) {
    const text = say(locale, "shell.deleteResource.warning" as Key, { name: "env-1", type: "ambiente" });
    expect(text).toMatch(/env-1 \(ambiente\)/);
  }
});

test("one glossary per language", () => {
  // pt-BR: "escutando"; control plane translated; cores = núcleos; concurrency = simultaneidade.
  const pt = JSON.stringify(messagesFor("pt-BR"));
  expect(pt).not.toMatch(/ouvindo|:"Cores"|Concorrência|control plane/i);
  const fr = JSON.stringify(messagesFor("fr"));
  expect(fr).not.toMatch(/:"Concurrence"|control plane/);
  const it = JSON.stringify(messagesFor("it"));
  expect(it).not.toMatch(/:"Concorrenza"|control plane|:"Jobs"|:"Scale sets"|:"Templates"/);
  const es = JSON.stringify(messagesFor("es"));
  expect(es).not.toMatch(/control plane|Compilar ahora|:"compilando"/i);
});

test("French punctuation keeps its narrow no-break space", () => {
  const fr = Object.values(JSON.parse(JSON.stringify(messagesFor("fr")))).map((v) => JSON.stringify(v)).join("");
  expect(fr).not.toMatch(/ [:?!»;]/); // a plain space would let the sign wrap to the next line
  expect(fr).not.toMatch(/« /);
  expect(say("fr", "environments.list.failedAt", { stage: "boot", reason: "x" })).toBe(say("fr", "templates.failedAt", { stage: "boot", reason: "x" }));
});

test("units follow the language", () => {
  expect(say("fr", "overview.resources.memoryTitle")).toMatch(/Mo/);
});
