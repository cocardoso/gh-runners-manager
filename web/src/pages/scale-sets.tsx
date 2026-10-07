import { Badge, Banner, ClipboardText, Empty, Grid, LayerCard, Link, Meter } from "@cloudflare/kumo";
import { StackIcon, WarningIcon, WarningCircleIcon } from "@phosphor-icons/react";
import type { ScaleSet } from "@/api/client";
import { useScaleSets, useSettings } from "@/api/queries";
import { ErrorState, Loading, Page, RelativeTime } from "@/components/common";
import { DefinitionList } from "@/components/definition-list";
import { formatMB, isSet } from "@/lib/format";

type Config = Record<string, unknown>;

function list(v: unknown) {
  return Array.isArray(v) && v.length ? v.join(", ") : "—";
}

function ScaleSetCard({ s, config }: { s: ScaleSet; config?: Config }) {
  const max = typeof config?.max_concurrent === "number" ? config.max_concurrent : undefined;
  return (
    <section id={s.name} aria-labelledby={`ss-${s.name}`} className="scroll-mt-20">
      <LayerCard>
        <LayerCard.Secondary className="flex flex-wrap items-center justify-between gap-2">
          <h2 id={`ss-${s.name}`} className="min-w-0 truncate text-base font-semibold text-kumo-default">
            {s.name}
          </h2>
          {s.listening ? (
            <Badge variant="success" appearance="dot">
              Listening
            </Badge>
          ) : (
            <Badge variant="error" appearance="dot">
              Not listening
            </Badge>
          )}
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
                ["Repository or organization", typeof config.url === "string" ? <Link href={config.url} target="_blank" rel="noreferrer">{config.url}</Link> : "—"],
                ["Labels", list(config.labels)],
                ["Resources", `${config.cores ?? "?"} cores · ${typeof config.memory_mb === "number" ? formatMB(config.memory_mb) : "?"}`],
                ["Runner group", String(config.runner_group || "default")],
                ["Credential", String(config.credential ?? "—")],
                ["Keep failed environments", config.keep_on_failure_minutes ? `${config.keep_on_failure_minutes} min` : "No"],
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
  const settings = useSettings();
  const configs = new Map((settings.data?.scale_sets ?? []).map((c) => [String((c as Config).name), c as Config]));
  return (
    <Page title="Scale sets" description="Each scale set listens to GitHub and creates one environment per job.">
      {sets.isLoading ? (
        <Loading />
      ) : sets.error ? (
        <ErrorState error={sets.error} />
      ) : (sets.data ?? []).length === 0 ? (
        <Empty
          icon={<StackIcon size={48} className="text-kumo-inactive" />}
          title="No scale sets"
          description="Add a scale set under scale_sets in ghrm.yaml and restart the control plane."
        />
      ) : (
        <Grid variant="2up" gap="base">
          {(sets.data ?? []).map((s) => (
            <ScaleSetCard key={s.name} s={s} config={configs.get(s.name)} />
          ))}
        </Grid>
      )}
    </Page>
  );
}
