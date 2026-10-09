import { useId, useState, type FormEvent } from "react";
import { Banner, Button, Checkbox, Dialog, DialogRoot, DialogTitle, Empty, Input, LayerCard, Link, Table, Textarea, Tooltip, useKumoToastManager } from "@cloudflare/kumo";
import { PencilSimpleIcon, PlusIcon, StackIcon, TrashIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type TemplateComponent, type TemplateProfile } from "@/api/client";
import { useTemplateProfiles, useTemplates } from "@/api/queries";
import { ErrorState, Loading, Truncate } from "@/components/common";
import { ViewToggle } from "@/components/view-toggle";
import { useViewMode } from "@/lib/view-mode";
import { useT, type Key } from "@/i18n";

const message = (e: unknown) => (e instanceof Error ? e.message.replace(/^template: /, "") : String(e));
const NAME = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/;
// Package names, the same in every language.
const APT_EXAMPLE = "libpq-dev, zip";
const TOOL_NAMES: Record<string, string> = { node: "Node.js", python: "Python", go: "Go" };
const HINTS = new Set(["github-cli", "zstd", "nodejs", "python", "azure-cli"]);

/** "22, 24" → ["22", "24"]. */
const splitList = (v: string) =>
  v
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

function componentLabel(c: TemplateComponent) {
  return (c.report ?? [c.id]).join(", ");
}

function preinstalled(p: TemplateProfile) {
  return Object.entries(p.toolcache ?? {})
    .filter(([, vs]) => (vs ?? []).length > 0)
    .map(([tool, vs]) => `${TOOL_NAMES[tool] ?? tool} ${(vs ?? []).join(", ")}`);
}

/** Edit, and delete unless the profile is the default one or in use (then the reason is told). */
function ProfileActions({ p, onEdit, onDelete }: { p: TemplateProfile; onEdit: () => void; onDelete: () => void }) {
  const t = useT();
  const users = p.used_by ?? [];
  const keptReason = p.name === "default" ? t("templates.profiles.defaultKept") : users.length > 0 ? t("templates.profiles.usedKept", { names: users.join(", ") }) : "";
  return (
    <span className="flex gap-2">
      <Button size="sm" variant="secondary" icon={PencilSimpleIcon} onClick={onEdit}>
        {t("templates.profiles.edit")}
      </Button>
      {keptReason ? (
        // A disabled button takes no focus: its wrapper carries the reason.
        <Tooltip
          content={keptReason}
          render={
            <span tabIndex={0} aria-label={keptReason}>
              <Button size="sm" variant="secondary-destructive" icon={TrashIcon} aria-label={t("templates.profiles.deleteLabel", { name: p.name })} disabled>
                {t("templates.profiles.delete")}
              </Button>
            </span>
          }
        />
      ) : (
        <Button size="sm" variant="secondary-destructive" icon={TrashIcon} aria-label={t("templates.profiles.deleteLabel", { name: p.name })} onClick={onDelete}>
          {t("templates.profiles.delete")}
        </Button>
      )}
    </span>
  );
}

function ActiveTemplate({ p, release }: { p: TemplateProfile; release?: string }) {
  const t = useT();
  return p.active_template_id ? (
    <Link href={`/templates/${encodeURIComponent(p.active_template_id)}`}>{release || p.active_template_id}</Link>
  ) : (
    <span className="text-kumo-subtle">{t("templates.profiles.notBuilt")}</span>
  );
}

function ProfileCard({
  p,
  release,
  components,
  canAct,
  onEdit,
  onDelete,
}: {
  p: TemplateProfile;
  release?: string;
  components: TemplateComponent[];
  canAct: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const t = useT();
  const byID = new Map(components.map((c) => [c.id, c]));
  const leftOut = (p.remove ?? []).map((id) => (byID.get(id) ? componentLabel(byID.get(id)!) : id));
  const pre = preinstalled(p);
  const users = p.used_by ?? [];
  const rows: [string, string][] = [
    [t("templates.profiles.usedBy"), users.length > 0 ? users.join(", ") : t("templates.profiles.nobody")],
    [t("templates.profiles.leftOut"), leftOut.length > 0 ? leftOut.join(" · ") : t("templates.profiles.nothingLeftOut")],
    [t("templates.profiles.preinstalled"), pre.length > 0 ? pre.join(" · ") : t("templates.profiles.nothingPreinstalled")],
  ];
  if ((p.apt ?? []).length > 0) rows.push([t("templates.profiles.apt"), (p.apt ?? []).join(", ")]);
  if (p.script) rows.push([t("templates.profiles.script"), t("templates.profiles.hasScript")]);
  return (
    <section aria-label={p.name}>
      <LayerCard>
        <LayerCard.Secondary className="flex flex-wrap items-center justify-between gap-2">
          <h3 className="font-heading text-base font-semibold text-kumo-default">{p.name}</h3>
          {canAct && <ProfileActions p={p} onEdit={onEdit} onDelete={onDelete} />}
        </LayerCard.Secondary>
        <LayerCard.Primary className="p-0">
          <dl className="divide-y divide-kumo-line">
            <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1 px-4 py-2.5 text-sm">
              <dt className="text-kumo-subtle">{t("templates.profiles.active")}</dt>
              <dd className="min-w-0">
                <ActiveTemplate p={p} release={release} />
              </dd>
            </div>
            {rows.map(([label, value]) => (
              <div key={label} className="flex min-w-0 flex-wrap justify-between gap-x-3 gap-y-1 px-4 py-2.5 text-sm">
                <dt className="text-kumo-subtle">{label}</dt>
                <dd className="min-w-0 break-words sm:text-right">{value}</dd>
              </div>
            ))}
          </dl>
        </LayerCard.Primary>
      </LayerCard>
    </section>
  );
}

function ProfileEditor({ initial, components, tools, onClose }: { initial?: TemplateProfile; components: TemplateComponent[]; tools: string[]; onClose: () => void }) {
  const t = useT();
  const id = useId();
  const qc = useQueryClient();
  const toast = useKumoToastManager();
  const [name, setName] = useState(initial?.name ?? "");
  const [remove, setRemove] = useState<Set<string>>(new Set(initial?.remove ?? []));
  const [toolcache, setToolcache] = useState<Record<string, string>>(
    Object.fromEntries(tools.map((tool) => [tool, (initial?.toolcache?.[tool] ?? []).join(", ")])),
  );
  const [apt, setApt] = useState((initial?.apt ?? []).join(", "));
  const [script, setScript] = useState(initial?.script ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const nameBad = !initial && name.trim() !== "" && !NAME.test(name.trim());
  // Each tool's dependents: unchecking a tool unchecks what needs it.
  const dependents = new Map<string, string[]>();
  for (const c of components) if (c.needs) dependents.set(c.needs, [...(dependents.get(c.needs) ?? []), c.id]);

  // Checked means the image has it; unchecking leaves it out.
  const toggle = (c: TemplateComponent, included: boolean) => {
    const next = new Set(remove);
    if (included) {
      next.delete(c.id);
      if (c.needs) next.delete(c.needs); // what it needs comes back too
    } else {
      next.add(c.id);
      for (const d of dependents.get(c.id) ?? []) next.add(d); // what needs it goes too
    }
    setRemove(next);
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim() || nameBad) return;
    setBusy(true);
    setError(undefined);
    try {
      const body = {
        remove: [...remove].sort(),
        toolcache: Object.fromEntries(Object.entries(toolcache).map(([tool, v]) => [tool, splitList(v)])),
        apt: splitList(apt),
        script,
      };
      // A new profile never replaces one saved under the same name.
      const header = initial ? {} : { "If-None-Match": "*" };
      unwrap(await api.PUT("/api/v1/template-profiles/{name}", { params: { path: { name: name.trim() }, header }, body }));
      toast.add({ title: t("templates.profiles.saved", { name: name.trim() }), description: t("templates.profiles.savedHelp"), variant: "success" });
      void qc.invalidateQueries({ queryKey: ["templates"] });
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <DialogRoot open onOpenChange={(o) => !o && onClose()}>
      <Dialog size="xl" className="flex max-h-[90dvh] flex-col overflow-hidden p-0">
        <div className="border-b border-kumo-line px-6 py-4">
          <DialogTitle className="text-lg font-semibold">
            {initial ? t("templates.profiles.editTitle", { name: initial.name }) : t("templates.profiles.newTitle")}
          </DialogTitle>
          <p className="mt-1 text-sm text-kumo-subtle">{t("templates.profiles.field.rebuild")}</p>
        </div>
        <form className="flex min-h-0 flex-1 flex-col" onSubmit={submit}>
          <div className="flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto px-6 py-5">
            {error && <Banner variant="error" title={error} />}
            {!initial && (
              <Input
                label={t("templates.profiles.field.name")}
                value={name}
                onChange={(e) => setName(e.target.value)}
                error={nameBad ? t("templates.profiles.field.nameHelp") : undefined}
                description={nameBad ? undefined : t("templates.profiles.field.nameHelp")}
              />
            )}
            <fieldset className="flex flex-col gap-2" aria-describedby={`${id}-components-help`}>
              <legend className="text-base font-medium text-kumo-default">{t("templates.profiles.field.components")}</legend>
              <p id={`${id}-components-help`} className="text-sm text-kumo-subtle">
                {t("templates.profiles.field.componentsHelp")}
              </p>
              <div className="grid gap-x-6 gap-y-2 sm:grid-cols-2">
                {components.map((c) => (
                  <div key={c.id} className="flex min-w-0 flex-col">
                    <Checkbox
                      label={componentLabel(c)}
                      checked={!remove.has(c.id)}
                      onCheckedChange={(on) => toggle(c, !!on)}
                      aria-describedby={HINTS.has(c.id) ? `${id}-hint-${c.id}` : undefined}
                    />
                    {HINTS.has(c.id) && (
                      <span id={`${id}-hint-${c.id}`} className="pl-6 text-xs text-kumo-subtle">
                        {t(`templates.profiles.hints.${c.id}` as Key)}
                      </span>
                    )}
                  </div>
                ))}
              </div>
            </fieldset>
            <fieldset className="flex flex-col gap-3">
              <legend className="text-base font-medium text-kumo-default">{t("templates.profiles.field.toolcache")}</legend>
              <p className="text-sm text-kumo-subtle">{t("templates.profiles.field.toolcacheHelp")}</p>
              <div className="grid gap-4 sm:grid-cols-3">
                {tools.map((tool) => (
                  <Input
                    key={tool}
                    label={TOOL_NAMES[tool] ?? tool}
                    placeholder={tool === "python" ? "3.12" : tool === "go" ? "1.24" : "22, 24"}
                    value={toolcache[tool] ?? ""}
                    onChange={(e) => setToolcache({ ...toolcache, [tool]: e.target.value })}
                    description={t("templates.profiles.field.versions")}
                  />
                ))}
              </div>
            </fieldset>
            <Input
              label={t("templates.profiles.field.apt")}
              placeholder={APT_EXAMPLE}
              value={apt}
              onChange={(e) => setApt(e.target.value)}
              description={t("templates.profiles.field.aptHelp")}
            />
            <div className="flex flex-col gap-1.5">
              <label htmlFor={`${id}-script`} className="text-base font-medium text-kumo-default">
                {t("templates.profiles.field.script")}
              </label>
              <Textarea
                id={`${id}-script`}
                className="min-h-32 font-mono text-sm"
                value={script}
                onChange={(e) => setScript(e.target.value)}
                spellCheck={false}
                autoCapitalize="off"
                autoCorrect="off"
                aria-describedby={`${id}-script-help`}
              />
              <span id={`${id}-script-help`} className="text-sm text-kumo-subtle">
                {t("templates.profiles.field.scriptHelp")}
              </span>
            </div>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-3 border-t border-kumo-line px-6 py-4">
            <Button variant="secondary" type="button" onClick={onClose}>
              {t("templates.profiles.field.cancel")}
            </Button>
            <Button variant="primary" type="submit" loading={busy} disabled={!name.trim() || nameBad}>
              {t("templates.profiles.field.save")}
            </Button>
          </div>
        </form>
      </Dialog>
    </DialogRoot>
  );
}

function DeleteProfile({ name, onClose }: { name: string; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const toast = useKumoToastManager();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const remove = async () => {
    setBusy(true);
    setError(undefined);
    try {
      unwrap(await api.DELETE("/api/v1/template-profiles/{name}", { params: { path: { name } } }));
      toast.add({ title: t("templates.profiles.deleted", { name }), variant: "success" });
      void qc.invalidateQueries({ queryKey: ["templates"] });
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <DialogRoot open onOpenChange={(o) => !o && onClose()}>
      <Dialog className="flex flex-col gap-4 p-6">
        <DialogTitle className="text-lg font-semibold">{t("templates.profiles.deleteTitle", { name })}</DialogTitle>
        <p className="text-sm text-kumo-subtle">{t("templates.profiles.deleteHelp")}</p>
        {error && <Banner variant="error" title={error} />}
        <div className="flex justify-end gap-3">
          <Button variant="secondary" onClick={onClose}>
            {t("templates.profiles.field.cancel")}
          </Button>
          <Button variant="destructive" loading={busy} onClick={remove}>
            {t("templates.profiles.delete")}
          </Button>
        </div>
      </Dialog>
    </DialogRoot>
  );
}

/** The Profiles tab of the Templates page. */
/** One row per profile: its template, who uses it and what it changes. */
function ProfileTable({ profiles, releases, components, canAct, onEdit, onDelete }: {
  profiles: TemplateProfile[];
  releases: Map<string, string | undefined>;
  components: TemplateComponent[];
  canAct: boolean;
  onEdit: (p: TemplateProfile) => void;
  onDelete: (p: TemplateProfile) => void;
}) {
  const t = useT();
  const byID = new Map(components.map((c) => [c.id, c]));
  return (
    <LayerCard>
      <LayerCard.Primary className="overflow-x-auto p-0">
        <Table layout="auto" className="min-w-[48rem]">
          <Table.Header>
            <Table.Row>
              <Table.Head>{t("templates.scaleSets.columns.name")}</Table.Head>
              <Table.Head>{t("templates.profiles.active")}</Table.Head>
              <Table.Head>{t("templates.profiles.usedBy")}</Table.Head>
              <Table.Head>{t("templates.profiles.leftOut")}</Table.Head>
              <Table.Head>{t("templates.profiles.preinstalled")}</Table.Head>
              {canAct && (
                <Table.Head>
                  <span className="sr-only">{t("templates.scaleSets.columns.actions")}</span>
                </Table.Head>
              )}
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {profiles.map((p) => {
              const leftOut = (p.remove ?? []).map((id) => (byID.get(id) ? componentLabel(byID.get(id)!) : id));
              const pre = preinstalled(p);
              const users = p.used_by ?? [];
              return (
                <Table.Row key={p.name}>
                  <Table.Cell className="font-medium">{p.name}</Table.Cell>
                  <Table.Cell>
                    <ActiveTemplate p={p} release={p.active_template_id ? releases.get(p.active_template_id) : undefined} />
                  </Table.Cell>
                  <Table.Cell>{users.length > 0 ? users.join(", ") : <span className="text-kumo-subtle">{t("templates.profiles.nobody")}</span>}</Table.Cell>
                  <Table.Cell>
                    {leftOut.length > 0 ? <Truncate className="max-w-64" text={leftOut.join(" · ")} /> : <span className="text-kumo-subtle">{t("templates.profiles.nothingLeftOut")}</span>}
                  </Table.Cell>
                  <Table.Cell>{pre.length > 0 ? pre.join(" · ") : <span className="text-kumo-subtle">{t("templates.profiles.nothingPreinstalled")}</span>}</Table.Cell>
                  {canAct && (
                    <Table.Cell className="whitespace-nowrap">
                      <ProfileActions p={p} onEdit={() => onEdit(p)} onDelete={() => onDelete(p)} />
                    </Table.Cell>
                  )}
                </Table.Row>
              );
            })}
          </Table.Body>
        </Table>
      </LayerCard.Primary>
    </LayerCard>
  );
}

export function ProfilesTab({ canAct }: { canAct: boolean }) {
  const t = useT();
  const q = useTemplateProfiles();
  const versions = useTemplates();
  const releases = new Map((versions.data?.templates ?? []).map((v) => [v.id, v.slim_release]));
  const [editing, setEditing] = useState<TemplateProfile | "new" | null>(null);
  const [deleting, setDeleting] = useState<string>();
  const [view, setView] = useViewMode("template-profiles", "cards");
  if (q.isLoading) return <Loading />;
  if (q.error || !q.data) return <ErrorState error={q.error} />;
  const profiles = q.data.profiles ?? [];
  const components = q.data.components ?? [];
  const tools = q.data.toolcache_tools ?? [];
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="max-w-3xl text-sm text-kumo-subtle">{t("templates.profiles.description")}</p>
        <div className="flex items-center gap-2">
          <ViewToggle value={view} onChange={setView} />
          {canAct && (
            <Button variant="secondary" icon={PlusIcon} onClick={() => setEditing("new")}>
              {t("templates.profiles.new")}
            </Button>
          )}
        </div>
      </div>
      {profiles.length === 0 ? (
        <Empty icon={<StackIcon size={48} className="text-kumo-inactive" />} title={t("templates.profiles.tab")} />
      ) : view === "list" ? (
        <ProfileTable profiles={profiles} releases={releases} components={components} canAct={canAct} onEdit={setEditing} onDelete={(p) => setDeleting(p.name)} />
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">
          {profiles.map((p) => (
            <ProfileCard key={p.name} p={p} release={p.active_template_id ? releases.get(p.active_template_id) : undefined} components={components} canAct={canAct} onEdit={() => setEditing(p)} onDelete={() => setDeleting(p.name)} />
          ))}
        </div>
      )}
      {editing && <ProfileEditor initial={editing === "new" ? undefined : editing} components={components} tools={tools} onClose={() => setEditing(null)} />}
      {deleting && <DeleteProfile name={deleting} onClose={() => setDeleting(undefined)} />}
    </div>
  );
}
