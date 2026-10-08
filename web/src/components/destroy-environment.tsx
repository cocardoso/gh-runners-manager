import { useState } from "react";
import { Button, useKumoToastManager } from "@cloudflare/kumo";
import { TrashIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import { useT } from "@/i18n";
import { DeleteResource } from "@/blocks/delete-resource/delete-resource";

/** The destroy action, behind a type-the-name confirmation. */
export function DestroyEnvironment({ id, disabled }: { id: string; disabled?: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const toast = useKumoToastManager();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const destroy = async () => {
    setBusy(true);
    setError(undefined);
    try {
      unwrap(await api.POST("/api/v1/environments/{id}/destroy", { params: { path: { id } } }));
      setOpen(false);
      toast.add({ title: t("environments.destroy.requested"), description: t("environments.destroy.requestedDescription", { id }), variant: "success" });
      void qc.invalidateQueries({ queryKey: ["environment", id] });
      void qc.invalidateQueries({ queryKey: ["environments"] });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Button
        variant="secondary-destructive"
        icon={TrashIcon}
        disabled={disabled}
        onClick={() => {
          setError(undefined);
          setOpen(true);
        }}
      >
        {t("environments.destroy.button")}
      </Button>
      <DeleteResource
        open={open}
        onOpenChange={setOpen}
        resourceType={t("environments.destroy.resourceType")}
        resourceName={id}
        onDelete={destroy}
        isDeleting={busy}
        deleteButtonText={t("environments.destroy.confirm")}
        errorMessage={error}
      />
    </>
  );
}
