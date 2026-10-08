import type { ReactNode } from "react";
import { Button, InputGroup, Select } from "@cloudflare/kumo";
import { MagnifyingGlassIcon, XIcon } from "@phosphor-icons/react";
import { tr } from "@/i18n";

export interface FilterDef {
  key: string;
  label: string;
  value: string | undefined;
  options: Record<string, string>;
}

/** Search plus select filters above a resource list, in the Cloudflare dashboard style. */
export function ListToolbar({
  search,
  onSearch,
  searchLabel,
  filters,
  onFilter,
  onClear,
  children,
}: {
  search: string;
  onSearch: (q: string) => void;
  searchLabel: string;
  filters: FilterDef[];
  onFilter: (key: string, value: string | undefined) => void;
  onClear: () => void;
  children?: ReactNode;
}) {
  const active = !!search || filters.some((f) => f.value);
  return (
    <div className="flex flex-wrap items-center gap-2">
      <InputGroup className="w-full sm:w-72">
        <InputGroup.Addon>
          <MagnifyingGlassIcon />
        </InputGroup.Addon>
        <InputGroup.Input type="search" aria-label={searchLabel} placeholder={searchLabel} value={search} onChange={(e) => onSearch(e.target.value)} />
      </InputGroup>
      {filters.map((f) => (
        <Select
          key={f.key}
          aria-label={f.label}
          className="min-w-36"
          value={f.value ?? ""}
          onValueChange={(v) => onFilter(f.key, (v as string) || undefined)}
          items={{ "": tr("shell.filters.all", { label: f.label }), ...Object.fromEntries(Object.entries(f.options).map(([k, v]) => [k, tr("shell.filters.option", { label: f.label, value: v })])) }}
        />
      ))}
      {active && (
        <Button variant="ghost" icon={XIcon} onClick={onClear}>
          {tr("shell.filters.clear")}
        </Button>
      )}
      {children && <div className="ml-auto flex items-center gap-2">{children}</div>}
    </div>
  );
}
