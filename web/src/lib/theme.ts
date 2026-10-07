import { useCallback, useEffect, useState } from "react";

export type ThemePreference = "system" | "light" | "dark";
const KEY = "ghrm.theme";

/** The stored preference; blocked or empty storage means "system". */
export function readThemePreference(): ThemePreference {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "light" || v === "dark") return v;
  } catch {
    // storage may be unavailable
  }
  return "system";
}

function systemDark(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia("(prefers-color-scheme: dark)").matches;
}

/** Applies the theme Kumo reads: data-mode on the root element. */
export function applyTheme(pref: ThemePreference) {
  const dark = pref === "dark" || (pref === "system" && systemDark());
  document.documentElement.setAttribute("data-mode", dark ? "dark" : "light");
}

export function useTheme() {
  const [pref, setPref] = useState<ThemePreference>(readThemePreference);
  useEffect(() => {
    applyTheme(pref);
    if (pref !== "system" || typeof window.matchMedia !== "function") return;
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => applyTheme("system");
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [pref]);
  const set = useCallback((p: ThemePreference) => {
    setPref(p);
    try {
      if (p === "system") localStorage.removeItem(KEY);
      else localStorage.setItem(KEY, p);
    } catch {
      // ignore
    }
  }, []);
  return [pref, set] as const;
}

/** Whether Kumo is currently rendering in dark mode (follows data-mode on the root element). */
export function useIsDark(): boolean {
  const [dark, setDark] = useState(() => document.documentElement.getAttribute("data-mode") === "dark");
  useEffect(() => {
    const el = document.documentElement;
    const obs = new MutationObserver(() => setDark(el.getAttribute("data-mode") === "dark"));
    obs.observe(el, { attributes: true, attributeFilter: ["data-mode"] });
    return () => obs.disconnect();
  }, []);
  return dark;
}
