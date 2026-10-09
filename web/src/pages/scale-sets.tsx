import { useState } from "react";
import { withNode } from "@/i18n/nodes";
import { Badge, Banner, Button, ClipboardText, Collapsible, Empty, Grid, LayerCard, Link, Meter, Table, useKumoToastManager } from "@cloudflare/kumo";
import { InfoIcon, PencilSimpleIcon, PlusIcon, StackIcon, TrashIcon, WarningIcon, WarningCircleIcon } from "@phosphor-icons/react";
import type { ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type ScaleSet } from "@/api/client";
import { useProfileFallback, useScaleSets } from "@/api/queries";
import { DeleteResource } from "@/blocks/delete-resource/delete-resource";
import { ScaleSetDialog } from "@/components/scale-set-editor";
import { ErrorState, Loading, Page, RelativeTime } from "@/components/common";
import { DefinitionList } from "@/components/definition-list";
import { ViewToggle } from "@/components/view-toggle";
import { useViewMode } from "@/lib/view-mode";
import { useT, type Key, type Params } from "@/i18n";
import { formatMB, isSet } from "@/lib/format";
import { waitingTitle } from "@/lib/waiting";

function list(v: unknown) {
  return Array.isArray(v) && v.length ? v.join(", ") : "—";
}

type T = (key: Key, params?: Params) => string;

const resources = (t: T, config: NonNullable<ScaleSet["settings"]>) =>
  t("templates.scaleSets.fields.resourcesValue", { cores: config.cores ?? "?", memory: config.memory_mb ? formatMB(config.memory_mb) : "?" });

/** A scale set's settings, shown on demand. */
function details(t: T, s: ScaleSet, config: NonNullable<ScaleSet["settings"]>): [string, ReactNode][] {
  return [
    [t("templates.scaleSets.fields.repo"), config.url ? <Link href={config.url} target="_blank" rel="noreferrer">{config.url}</Link> : "—"],
    [t("templates.scaleSets.fields.labels"), list(config.labels)],
    [t("templates.scaleSets.fields.resources"), resources(t, config)],
    [t("templates.scaleSets.fields.profile"), config.template_profile || "default"],
    [t("templates.scaleSets.fields.warm"), config.warm_runners ? String(config.warm_runners) : t("templates.scaleSets.fields.no")],
    [t("templates.scaleSets.fields.runnerGroup"), config.runner_group || "default"],
    [t("templates.scaleSets.fields.credential"), config.credential || "—"],
    [
      t("templates.scaleSets.fields.keepFailed"),
      config.keep_on_failure_minutes ? t("templates.scaleSets.fields.minutes", { minutes: config.keep_on_failure_minutes }) : t("templates.scaleSets.fields.no"),
    ],
    [t("templates.scaleSets.fields.source"), s.source === "ui" ? t("templates.scaleSets.fields.sourceUi") : t("templates.scaleSets.fields.sourceFile")],
  ];
}

function StatusBadge({ s }: { s: ScaleSet }) {
  const t = useT();
  return s.removed ? (
    <Badge variant="neutral" appearance="dot">
      {t("templates.scaleSets.status.removed")}
    </Badge>
  ) : s.listening ? (
    <Badge variant="success" appearance="dot">
      {t("templates.scaleSets.status.listening")}
    </Badge>
  ) : (
    <Badge variant="error" appearance="dot">
      {t("templates.scaleSets.status.notListening")}
    </Badge>
  );
}

function Actions({ s, onEdit, onRemove }: { s: ScaleSet; onEdit: () => void; onRemove: () => void }) {
  const t = useT();
  if (s.source !== "ui" || s.removed) return null;
  return (
    <>
      <Button variant="ghost" size="sm" shape="square" icon={PencilSimpleIcon} aria-label={t("templates.scaleSets.edit", { name: s.name })} onClick={onEdit} />
      <Button variant="ghost" size="sm" shape="square" icon={TrashIcon} aria-label={t("templates.scaleSets.remove", { name: s.name })} onClick={onRemove} />
    </>
  );
}

function ScaleSetCard({ s, onEdit, onRemove }: { s: ScaleSet; onEdit: () => void; onRemove: () => void }) {
  const config = s.settings ?? undefined;
  const max = config?.max_concurrent || undefined;
  const fallback = useProfileFallback();
  const t = useT();
  return (
    <section id={s.name} aria-labelledby={`ss-${s.name}`} className="scroll-mt-20">
      <LayerCard>
        <LayerCard.Secondary className="flex flex-wrap items-center justify-between gap-2">
          <h2 id={`ss-${s.name}`} className="min-w-0 truncate text-base font-semibold text-kumo-default">
            {s.name}
          </h2>
          <span className="flex flex-wrap items-center gap-1">
            <StatusBadge s={s} />
            <Actions s={s} onEdit={onEdit} onRemove={onRemove} />
          </span>
        </LayerCard.Secondary>
        <LayerCard.Primary className="flex flex-col gap-4">
          {fallback(config?.template_profile) && (
            <Banner variant="secondary" icon={<InfoIcon weight="fill" />} title={t("templates.profiles.fallback", { name: config?.template_profile ?? "" })} />
          )}
          {s.listen_error && <Banner variant="error" icon={<WarningCircleIcon weight="fill" />} title={t("templates.scaleSets.listenerStopped")} description={s.listen_error} />}
          {s.waiting && (
            <Banner
              variant="alert"
              icon={<WarningIcon weight="fill" />}
              title={waitingTitle(t, s.waiting)}
              description={isSet(s.waiting_since) ? withNode(t("templates.scaleSets.since"), "{time}", <RelativeTime value={s.waiting_since} />) : undefined}
            />
          )}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <div className="text-sm text-kumo-subtle">{t("templates.scaleSets.desired")}</div>
              <div className="font-heading text-2xl font-semibold tabular-nums">{s.desired}</div>
            </div>
            <div>
              <div className="text-sm text-kumo-subtle">{t("templates.scaleSets.live")}</div>
              <div className="font-heading text-2xl font-semibold tabular-nums">{s.live}</div>
            </div>
          </div>
          {max ? <Meter label={t("templates.scaleSets.concurrency")} value={s.live} max={max} customValue={`${s.live} / ${max}`} /> : null}
          <div className="flex flex-col gap-1.5">
            <span className="text-sm text-kumo-subtle">{t("templates.scaleSets.useIt")}</span>
            <ClipboardText text={`runs-on: ${s.name}`} size="base" tooltip={{ text: t("shell.copy.short"), copiedText: t("shell.copy.copied") }} labels={{ copyAction: t("shell.copy.action") }} />
          </div>
        </LayerCard.Primary>
        {config && (
          <LayerCard.Primary className="border-t border-kumo-line p-0">
            {/* Closed by default: the card leads with what runs; the settings are a click away. */}
            <Collapsible.Root>
              <Collapsible.DefaultTrigger className="w-full px-4 py-3">{t("common.details")}</Collapsible.DefaultTrigger>
              <Collapsible.DefaultPanel>
                <DefinitionList items={details(t, s, config)} />
              </Collapsible.DefaultPanel>
            </Collapsible.Root>
          </LayerCard.Primary>
        )}
      </LayerCard>
    </section>
  );
}

/** One row per scale set: what it runs now and how it is sized. */
function ScaleSetTable({ sets, onEdit, onRemove }: { sets: ScaleSet[]; onEdit: (s: ScaleSet) => void; onRemove: (s: ScaleSet) => void }) {
  const t = useT();
  return (
    <LayerCard>
      <LayerCard.Primary className="overflow-x-auto p-0">
        <Table layout="auto" className="min-w-[48rem]">
          <Table.Header>
            <Table.Row>
              <Table.Head>{t("templates.scaleSets.columns.name")}</Table.Head>
              <Table.Head>{t("templates.scaleSets.columns.status")}</Table.Head>
              <Table.Head>{t("templates.scaleSets.desired")}</Table.Head>
              <Table.Head>{t("templates.scaleSets.concurrency")}</Table.Head>
              <Table.Head>{t("templates.scaleSets.fields.resources")}</Table.Head>
              <Table.Head>{t("templates.scaleSets.fields.profile")}</Table.Head>
              <Table.Head>
                <span className="sr-only">{t("templates.scaleSets.columns.actions")}</span>
              </Table.Head>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {sets.map((s) => {
              const config = s.settings ?? undefined;
              const max = config?.max_concurrent;
              return (
                <Table.Row key={s.name} id={s.name} className="scroll-mt-20">
                  <Table.Cell className="font-medium">{s.name}</Table.Cell>
                  <Table.Cell>
                    <span className="flex flex-col items-start gap-1">
                      <StatusBadge s={s} />
                      {s.listen_error && <span className="text-xs text-kumo-danger">{t("templates.scaleSets.listenerStopped")}</span>}
                      {s.waiting && <span className="text-xs text-kumo-warning">{waitingTitle(t, s.waiting)}</span>}
                    </span>
                  </Table.Cell>
                  <Table.Cell className="tabular-nums">{s.desired}</Table.Cell>
                  <Table.Cell className="tabular-nums">{max ? `${s.live} / ${max}` : s.live}</Table.Cell>
                  <Table.Cell>{config ? resources(t, config) : "—"}</Table.Cell>
                  <Table.Cell>{config?.template_profile || "default"}</Table.Cell>
                  <Table.Cell className="text-right whitespace-nowrap">
                    <Actions s={s} onEdit={() => onEdit(s)} onRemove={() => onRemove(s)} />
                  </Table.Cell>
                </Table.Row>
              );
            })}
          </Table.Body>
        </Table>
      </LayerCard.Primary>
    </LayerCard>
  );
}

export function ScaleSetsPage() {
  const t = useT();
  const sets = useScaleSets();
  const qc = useQueryClient();
  const toast = useKumoToastManager();
  const [view, setView] = useViewMode("scale-sets", "cards");
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
      toast.add({ title: t("templates.scaleSets.removed", { name: removing }), description: t("templates.scaleSets.removedHelp"), variant: "success" });
      setRemoving(undefined);
      void qc.invalidateQueries({ queryKey: ["scale-sets"] });
      void qc.invalidateQueries({ queryKey: ["templates", "profiles"] });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Page
      title={t("templates.scaleSets.title")}
      description={t("templates.scaleSets.description")}
      actions={
        <div className="flex items-center gap-2">
          <ViewToggle value={view} onChange={setView} />
          <Button variant="primary" icon={PlusIcon} onClick={() => setEditing("new")}>
            {t("templates.scaleSets.new")}
          </Button>
        </div>
      }
    >
      {sets.isLoading ? (
        <Loading />
      ) : sets.error ? (
        <ErrorState error={sets.error} />
      ) : (sets.data ?? []).length === 0 ? (
        <Empty
          icon={<StackIcon size={48} className="text-kumo-inactive" />}
          title={t("templates.scaleSets.empty.title")}
          description={t("templates.scaleSets.empty.description")}
        />
      ) : view === "list" ? (
        <ScaleSetTable sets={sets.data ?? []} onEdit={setEditing} onRemove={(s) => setRemoving(s.name)} />
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
        resourceType={t("templates.scaleSets.deleteType")}
        resourceName={removing ?? ""}
        onDelete={remove}
        isDeleting={busy}
        deleteButtonText={t("templates.scaleSets.deleteButton")}
        errorMessage={error}
      />
    </Page>
  );
}
