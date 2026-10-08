import { messagesFor } from "./en";
import { locales } from "./locales";

function leaves(node: unknown, prefix = ""): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(node as Record<string, unknown>)) {
    const key = prefix + k;
    if (typeof v === "string") out[key] = v;
    else Object.assign(out, leaves(v, key + "."));
  }
  return out;
}

const params = (s: string) => [...new Set([...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))].sort().join(",");
const english = leaves(messagesFor("en"));

test.each(locales.filter((l) => l !== "en"))("%s has every key, with the same parameters", (locale) => {
  const got = leaves(messagesFor(locale));
  expect(Object.keys(got).sort()).toEqual(Object.keys(english).sort());
  for (const [key, text] of Object.entries(english)) {
    expect(`${key}: ${params(got[key] ?? "")}`).toBe(`${key}: ${params(text)}`);
  }
});

test.each(locales.filter((l) => l !== "en"))("%s is translated", (locale) => {
  // Product names and short tokens may stay as they are; sentences may not.
  const same = Object.entries(leaves(messagesFor(locale))).filter(([k, v]) => v === english[k] && v.split(" ").length >= 3);
  expect(same.map(([k]) => k)).toEqual([]);
});
