import { useCallback, useState } from "react";

export type ViewMode = "cards" | "list";

const key = (page: string) => `ghrm.view.${page}`;

/** The view a list page was last shown in; blocked or empty storage gives the page's default. */
export function readViewMode(page: string, fallback: ViewMode): ViewMode {
  try {
    const v = localStorage.getItem(key(page));
    if (v === "cards" || v === "list") return v;
  } catch {
    // storage may be unavailable
  }
  return fallback;
}

/** A list page's view (cards or list), remembered per page in this browser. */
export function useViewMode(page: string, fallback: ViewMode): [ViewMode, (mode: ViewMode) => void] {
  const [mode, setMode] = useState<ViewMode>(() => readViewMode(page, fallback));
  const set = useCallback(
    (next: ViewMode) => {
      setMode(next);
      try {
        localStorage.setItem(key(page), next);
      } catch {
        // the choice lasts until the page is left
      }
    },
    [page],
  );
  return [mode, set];
}
