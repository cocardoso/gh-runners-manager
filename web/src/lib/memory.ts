/** Memory is edited in GiB and sent in MB. */

/** The GiB a field shows for mb: 4096 → "4", 1536 → "1.5". */
export function gibFromMB(mb: number): string {
  return String(Math.round((mb / 1024) * 100) / 100);
}

/** The MB of a GiB field ("2", "1.5", "1,5"), or undefined when it is not a number. A field
 * still showing original keeps its exact MB, as the GiB shown are rounded. */
export function mbFromGiB(value: string, original?: number): number | undefined {
  if (original !== undefined && value.trim() === gibFromMB(original)) return original;
  const n = Number(value.trim().replace(",", "."));
  return value.trim() === "" || !Number.isFinite(n) ? undefined : Math.round(n * 1024);
}
