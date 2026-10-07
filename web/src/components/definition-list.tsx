import type { ReactNode } from "react";

export function DefinitionList({ items }: { items: [string, ReactNode][] }) {
  return (
    <dl>
      {items.map(([label, value]) => (
        <div key={label} className="grid grid-cols-1 gap-1 border-b border-kumo-line px-4 py-2.5 last:border-b-0 sm:grid-cols-[14rem_1fr] sm:gap-4">
          <dt className="text-sm text-kumo-subtle">{label}</dt>
          <dd className="min-w-0 break-words text-sm text-kumo-default">{value ?? "—"}</dd>
        </div>
      ))}
    </dl>
  );
}
