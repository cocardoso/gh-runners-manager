import { useState } from "react";
import { useKumoToastManager } from "@cloudflare/kumo";
import { tr } from "@/i18n";

export type AdminCall = () => Promise<unknown>;

/** Runs an admin API call (the session authorizes it) and toasts the outcome. */
export function useAdminAction(): { run: (success: string, call: AdminCall) => void; busy: boolean } {
  const toast = useKumoToastManager();
  const [busy, setBusy] = useState(false);
  const run = (success: string, call: AdminCall) => {
    setBusy(true);
    call()
      .then(() => toast.add({ title: success, variant: "success" }))
      .catch((e: unknown) => toast.add({ title: tr("shell.actionFailed"), description: e instanceof Error ? e.message : String(e), variant: "error" }))
      .finally(() => setBusy(false));
  };
  return { run, busy };
}
