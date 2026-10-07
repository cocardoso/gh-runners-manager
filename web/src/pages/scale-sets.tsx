import { useState } from "react";
import { Badge, Banner, Button, ClipboardText, Empty, Grid, LayerCard, Link, Meter, useKumoToastManager } from "@cloudflare/kumo";
import { PencilSimpleIcon, PlusIcon, StackIcon, TrashIcon, WarningIcon, WarningCircleIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type ScaleSet } from "@/api/client";
import { useScaleSets } from "@/api/queries";
import { DeleteResource } from "@/blocks/delete-resource/delete-resource";
import { ScaleSetDialog } from "@/components/scale-set-editor";
import { ErrorState, Loading, Page, RelativeTime } from "@/components/common";
import { DefinitionList } from "@/components/definition-list";
import { formatMB, isSet } from "@/lib/format";

function list(v: unknown) {
  return Array.isArray(v) && v.length ? v.join(", ") : "—";
}

function ScaleSetCard({ s, onEdit, onRemove }: { s: ScaleSet; onEdit: () => void; onRemove: () => void }) {
  const config = s.settings ?? undefined;
  const max = config?.max_concurrent || undefined;
  const ui = s.source === "ui";
  return (
    <section id={s.name} aria-labelledby={`ss-${s.name}`} className="scroll-mt-20">
      <LayerCard>
        <LayerCard.Secondary className="flex flex-wrap items-center justify-between gap-2">
          <h2 id={`ss-${s.name}`} className="min-w-0 truncate text-base font-semibold text-kumo-default">
            {s.name}
          </h2>
          <span className="flex flex-wrap items-center gap-1">
            {s.removed ? (
              <Badge variant="neutral" appearance="dot">
                Removed, draining
              </Badge>
            ) : s.listening ? (
              <Badge variant="success" appearance="dot">
                Listening
              </Badge>
            ) : (
              <Badge variant="error" appearance="dot">
                Not listening
              </Badge>
            )}
            {ui && !s.removed && (
              <>
                <Button variant="ghost" size="sm" shape="square" icon={PencilSimpleIcon} aria-label={`Edit ${s.name}`} onClick={onEdit} />
                <Button variant="ghost" size="sm" shape="square" icon={TrashIcon} aria-label={`Remove ${s.name}`} onClick={onRemove} />
              </>
            )}
          </span>
        </LayerCard.Secondary>
        <LayerCard.Primary className="flex flex-col gap-4">
          {s.listen_error && <Banner variant="error" icon={<WarningCircleIcon weight="fill" />} title="The listener stopped" description={s.listen_error} />}
          {s.waiting && (
            <Banner
              variant="alert"
              icon={<WarningIcon weight="fill" />}
              title={`Jobs are waiting: ${s.waiting}`}
              description={isSet(s.waiting_since) ? <>Since <RelativeTime value={s.waiting_since} /></> : undefined}
            />
          )}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <div className="text-sm text-kumo-subtle">Desired</div>
              <div className="font-heading text-2xl font-semibold tabular-nums">{s.desired}</div>
            </div>
            <div>
              <div className="text-sm text-kumo-subtle">Live environments</div>
              <div className="font-heading text-2xl font-semibold tabular-nums">{s.live}</div>
            </div>
          </div>
          {max ? <Meter label="Concurrency" value={s.live} max={max} customValue={`${s.live} / ${max}`} /> : null}
          <div className="flex flex-col gap-1.5">
            <span className="text-sm text-kumo-subtle">Use it in a workflow</span>
            <ClipboardText text={`runs-on: ${s.name}`} size="base" />
          </div>
        </LayerCard.Primary>
        {config && (
          <LayerCard.Primary className="border-t border-kumo-line p-0">
            <DefinitionList
              items={[
                ["Repository or organization", config.url ? <Link href={config.url} target="_blank" rel="noreferrer">{config.url}</Link> : "—"],
                ["Labels", list(config.labels)],
                ["Resources", `${config.cores ?? "?"} cores · ${config.memory_mb ? formatMB(config.memory_mb) : "?"}`],
                ["Runner group", config.runner_group || "default"],
                ["Credential", config.credential || "—"],
                ["Keep failed environments", config.keep_on_failure_minutes ? `${config.keep_on_failure_minutes} min` : "No"],
                ["Source", ui ? "Created in the UI" : "Defined in ghrm.yaml (edit the file to change it)"],
              ]}
            />
          </LayerCard.Primary>
        )}
      </LayerCard>
    </section>
  );
}

export function ScaleSetsPage() {
  const sets = useScaleSets();
  const qc = useQueryClient();
  const toast = useKumoToastManager();
  const [editing, setEditing] = useState<ScaleSet | "new" | null>(null);
  const [removing, setRemoving] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const remove = async () => {
    if (!removing) return;
    setBusy(true);
    setError(undefined);
    try {
      unwrap(await api.DELETE("/api/v1/scale-sets/{name}", { params: { path: { name: removing } } }));
      toast.add({ title: `${removing} removed`, description: "Running environments finish first. It stays registered on GitHub until you delete it there.", variant: "success" });
      setRemoving(undefined);
      void qc.invalidateQueries({ queryKey: ["scale-sets"] });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Page
      title="Scale sets"
      description="Each scale set listens to GitHub and creates one environment per job."
      actions={
        <Button variant="primary" icon={PlusIcon} onClick={() => setEditing("new")}>
          New scale set
        </Button>
      }
    >
      {sets.isLoading ? (
        <Loading />
      ) : sets.error ? (
        <ErrorState error={sets.error} />
      ) : (sets.data ?? []).length === 0 ? (
        <Empty
          icon={<StackIcon size={48} className="text-kumo-inactive" />}
          title="No scale sets"
          description="Create one with New scale set (add a GitHub credential in Settings first), or add it under scale_sets in ghrm.yaml."
        />
      ) : (
        <Grid variant="2up" gap="base">
          {(sets.data ?? []).map((s) => (
            <ScaleSetCard key={s.name} s={s} onEdit={() => setEditing(s)} onRemove={() => setRemoving(s.name)} />
          ))}
        </Grid>
      )}
      {editing && (
        <ScaleSetDialog
          name={editing === "new" ? undefined : editing.name}
          initial={editing === "new" ? undefined : editing.settings}
          onClose={() => setEditing(null)}
        />
      )}
      <DeleteResource
        open={!!removing}
        onOpenChange={(o) => !o && setRemoving(undefined)}
        resourceType="Scale set"
        resourceName={removing ?? ""}
        onDelete={remove}
        isDeleting={busy}
        deleteButtonText="Remove scale set"
        errorMessage={error}
      />
    </Page>
  );
}
