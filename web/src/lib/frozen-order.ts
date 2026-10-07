import { useState } from "react";

function same(a: string[], b: string[]) {
  return a.length === b.length && a.every((k, i) => k === b[i]);
}

/**
 * Keeps the rows stable while frozen (the pointer is over the table): existing rows update in
 * place, rows that left keep their last data and position, and new rows are held back and
 * counted until the table is released.
 */
export function useFrozenOrder<T>(items: T[], key: (item: T) => string, frozen: boolean): { rows: T[]; pending: number } {
  const keys = items.map(key);
  const [shown, setShown] = useState({ keys, items });
  if (!frozen) {
    if (!same(shown.keys, keys)) setShown({ keys, items });
    return { rows: items, pending: 0 };
  }
  const latest = new Map(items.map((i) => [key(i), i]));
  const known = new Map(shown.items.map((i) => [key(i), i]));
  const rows = shown.keys.flatMap((k) => latest.get(k) ?? known.get(k) ?? []);
  const visible = new Set(shown.keys);
  return { rows, pending: keys.filter((k) => !visible.has(k)).length };
}
