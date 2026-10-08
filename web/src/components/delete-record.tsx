import { useState } from "react";
import { Banner, Button, Dialog, DialogRoot, DialogTitle, useKumoToastManager } from "@cloudflare/kumo";
import { TrashIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { api, unwrap } from "@/api/client";
import { useT } from "@/i18n";

const kinds = {
  template: { back: "/templates" },
  environment: { back: "/environments" },
};

/** Deletes a finished record (a failed or deleted template, a destroyed environment) after a confirmation. */
export function DeleteRecord({ kind, id }: { kind: keyof typeof kinds; id: string }) {
  const k = kinds[kind];
  const t = useT();
  const qc = useQueryClient();
  const toast = useKumoToastManager();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const remove = async () => {
    setBusy(true);
    setError(undefined);
    try {
      if (kind === "template") unwrap(await api.DELETE("/api/v1/templates/{id}", { params: { path: { id } } }));
      else unwrap(await api.DELETE("/api/v1/environments/{id}", { params: { path: { id } } }));
      setOpen(false);
      toast.add({ title: t("environments.deleteRecord.deleted"), description: id, variant: "success" });
      for (const key of ["templates", "template", "environments", "environment", "jobs", "job", "stats", "overview", "repositories"]) void qc.invalidateQueries({ queryKey: [key] });
      // The record was history: show the history it was part of.
      void navigate({ to: k.back, search: { tab: "history" } });
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
        onClick={() => {
          setError(undefined);
          setOpen(true);
        }}
      >
        {t(`environments.deleteRecord.${kind}.button`)}
      </Button>
      <DialogRoot open={open} onOpenChange={setOpen}>
        <Dialog size="sm" className="flex flex-col gap-4 p-6">
          <DialogTitle className="text-lg font-semibold">{t("environments.deleteRecord.title", { id })}</DialogTitle>
          <p className="text-sm text-kumo-subtle">{t(`environments.deleteRecord.${kind}.text`)}</p>
          {error && <Banner variant="error" title={error} />}
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setOpen(false)}>
              {t("environments.deleteRecord.cancel")}
            </Button>
            <Button variant="destructive" loading={busy} onClick={() => void remove()}>
              {t("environments.deleteRecord.delete")}
            </Button>
          </div>
        </Dialog>
      </DialogRoot>
    </>
  );
}
