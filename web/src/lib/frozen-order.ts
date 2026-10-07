import { useState } from "react";

function same(a: string[], b: string[]) {
  return a.length === b.length && a.every((k, i) => k === b[i]);
}

/**
 * Keeps the order of rows stable while frozen (the pointer is over the table): existing
 * rows update in place and new rows are held back and counted until the table is released.
 */
export function useFrozenOrder<T>(items: T[], key: (item: T) => string, frozen: boolean): { rows: T[]; pending: number } {
  const keys = items.map(key);
  const [order, setOrder] = useState(keys);
  if (!frozen) {
    if (!same(order, keys)) setOrder(keys);
    return { rows: items, pending: 0 };
  }
  const latest = new Map(items.map((i) => [key(i), i]));
  const rows = order.flatMap((k) => latest.get(k) ?? []);
  const shown = new Set(order);
  return { rows, pending: keys.filter((k) => !shown.has(k)).length };
}
