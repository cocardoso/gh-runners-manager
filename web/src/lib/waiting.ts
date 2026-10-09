import type { Key, Params } from "@/i18n";

const REASONS = new Set(["scale_set_limit", "global_limit", "memory_budget", "host_memory", "disk"]);

/** Whether the scheduler's waiting reason has a translation. */
export const knownReason = (reason: string | undefined): reason is string => !!reason && REASONS.has(reason);

/** "Jobs are waiting: the memory budget is full", for a scheduler waiting reason. */
export function waitingTitle(t: (key: Key, params?: Params) => string, reason: string) {
  return t("templates.scaleSets.waiting", { reason: knownReason(reason) ? t(`overview.now.reasons.${reason}` as Key) : reason });
}
