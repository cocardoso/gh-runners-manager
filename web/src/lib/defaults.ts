/** Merges options over defaults, ignoring keys explicitly set to undefined. */
export function withDefaults<T extends object>(defaults: Required<T>, opts: T): Required<T> {
  const out = { ...defaults };
  for (const [k, v] of Object.entries(opts)) if (v !== undefined) (out as Record<string, unknown>)[k] = v;
  return out;
}
