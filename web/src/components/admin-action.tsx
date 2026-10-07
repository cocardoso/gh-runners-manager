import { useRef, useState, type ReactNode } from "react";
import { Button, Dialog, DialogRoot, DialogTitle, SensitiveInput, useKumoToastManager } from "@cloudflare/kumo";
import { ApiError } from "@/api/client";
import { getAdminToken, setAdminToken } from "@/lib/admin-token";

/** Asks for the control plane's admin token (kept for this tab only). */
export function TokenDialog({ open, error, onCancel, onToken }: { open: boolean; error?: string; onCancel: () => void; onToken: (t: string) => void }) {
  const [value, setValue] = useState("");
  return (
    <DialogRoot open={open} onOpenChange={(o) => !o && onCancel()}>
      <Dialog size="sm" className="flex flex-col gap-4 p-6">
        <DialogTitle className="text-lg font-semibold">Admin token required</DialogTitle>
        <p className="text-sm text-kumo-subtle">This action needs the control plane's admin token. It is kept for this browser tab only.</p>
        {error && <p className="text-sm text-kumo-danger">{error}</p>}
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (value.trim()) onToken(value.trim());
          }}
        >
          <SensitiveInput label="Admin token" value={value} onValueChange={setValue} autoFocus />
          <div className="flex justify-end gap-2">
            <Button variant="secondary" type="button" onClick={onCancel}>
              Cancel
            </Button>
            <Button variant="primary" type="submit" disabled={!value.trim()}>
              Continue
            </Button>
          </div>
        </form>
      </Dialog>
    </DialogRoot>
  );
}

export type AdminCall = (authorization: string) => Promise<unknown>;

/**
 * Runs admin API calls: asks for the token when none is stored, forgets it on a 401,
 * and reports the outcome with a toast. Render `dialog` once in the page.
 */
export function useAdminAction(): { run: (success: string, call: AdminCall) => void; busy: boolean; dialog: ReactNode } {
  const toast = useKumoToastManager();
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const pending = useRef<{ success: string; call: AdminCall } | null>(null);

  const execute = async (success: string, call: AdminCall, token: string) => {
    setBusy(true);
    try {
      await call(`Bearer ${token}`);
      toast.add({ title: success, variant: "success" });
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setAdminToken(null);
      toast.add({ title: "The action failed", description: e instanceof Error ? e.message : String(e), variant: "error" });
    } finally {
      setBusy(false);
    }
  };

  const run = (success: string, call: AdminCall) => {
    const token = getAdminToken();
    if (token) {
      void execute(success, call, token);
      return;
    }
    pending.current = { success, call };
    setAsking(true);
  };

  const dialog = (
    <TokenDialog
      open={asking}
      onCancel={() => {
        pending.current = null;
        setAsking(false);
      }}
      onToken={(t) => {
        setAdminToken(t);
        setAsking(false);
        const p = pending.current;
        pending.current = null;
        if (p) void execute(p.success, p.call, t);
      }}
    />
  );
  return { run, busy, dialog };
}
