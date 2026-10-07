import { useState } from "react";
import { Button, Dialog, DialogRoot, DialogTitle, SensitiveInput, Tooltip, useKumoToastManager } from "@cloudflare/kumo";
import { TrashIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, unwrap } from "@/api/client";
import { useSettings } from "@/api/queries";
import { DeleteResource } from "@/blocks/delete-resource/delete-resource";
import { getAdminToken, setAdminToken } from "@/lib/admin-token";

function TokenDialog({ open, onCancel, onToken }: { open: boolean; onCancel: () => void; onToken: (t: string) => void }) {
  const [value, setValue] = useState("");
  return (
    <DialogRoot open={open} onOpenChange={(o) => !o && onCancel()}>
      <Dialog size="sm" className="flex flex-col gap-4 p-6">
        <DialogTitle className="text-lg font-semibold">Admin token required</DialogTitle>
        <p className="text-sm text-kumo-subtle">
          Destroying an environment needs the control plane's admin token. It is kept for this browser tab only.
        </p>
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

/** The destroy action: admin token (once per tab), then type-the-name confirmation. */
export function DestroyEnvironment({ id, disabled }: { id: string; disabled?: boolean }) {
  const settings = useSettings();
  const qc = useQueryClient();
  const toast = useKumoToastManager();
  const [step, setStep] = useState<"idle" | "token" | "confirm">("idle");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const allowed = settings.data?.admin_actions === true;
  const start = () => {
    setError(undefined);
    setStep(getAdminToken() ? "confirm" : "token");
  };
  const destroy = async () => {
    setBusy(true);
    setError(undefined);
    try {
      unwrap(
        await api.POST("/api/v1/environments/{id}/destroy", {
          params: { path: { id }, header: { Authorization: `Bearer ${getAdminToken() ?? ""}` } },
        }),
      );
      setStep("idle");
      toast.add({ title: "Destroy requested", description: `${id} is being destroyed.`, variant: "success" });
      void qc.invalidateQueries({ queryKey: ["environment", id] });
      void qc.invalidateQueries({ queryKey: ["environments"] });
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setAdminToken(null);
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const button = (
    <Button variant="secondary-destructive" icon={TrashIcon} disabled={disabled || !allowed} onClick={start}>
      Destroy
    </Button>
  );
  return (
    <>
      {allowed ? button : <Tooltip content="Start the control plane with an admin token to enable this action." render={<span>{button}</span>} />}
      <TokenDialog
        open={step === "token"}
        onCancel={() => setStep("idle")}
        onToken={(t) => {
          setAdminToken(t);
          setStep("confirm");
        }}
      />
      <DeleteResource
        open={step === "confirm"}
        onOpenChange={(o) => !o && setStep("idle")}
        resourceType="Environment"
        resourceName={id}
        onDelete={destroy}
        isDeleting={busy}
        deleteButtonText="Destroy environment"
        errorMessage={error}
      />
    </>
  );
}
