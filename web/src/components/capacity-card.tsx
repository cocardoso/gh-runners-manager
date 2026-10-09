import { useId, useState } from "react";
import { Banner, Button, Input, LayerCard } from "@cloudflare/kumo";
import { InfoIcon } from "@phosphor-icons/react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "@/api/client";
import type { components } from "@/api/schema";
import { useCapacity } from "@/api/queries";
import { useAdminAction } from "@/components/admin-action";
import { ErrorState, Loading } from "@/components/common";
import { DefinitionList } from "@/components/definition-list";
import { helpField } from "@/components/help-tip";
import { formatMB, formatPercent } from "@/lib/format";
import { gibFromMB, mbFromGiB } from "@/lib/memory";
import { useT } from "@/i18n";

type Capacity = components["schemas"]["CapacityView"];

// The same limits as the control plane (internal/config).
const MAX_ENVIRONMENTS = 100;
const MIN_BUDGET_MB = 512;

const whole = (v: string) => (v.trim() === "" || !Number.isInteger(Number(v)) ? undefined : Number(v));

function CapacityForm({ initial }: { initial: Capacity }) {
  const t = useT();
  const id = useId();
  const qc = useQueryClient();
  const admin = useAdminAction();
  const [environments, setEnvironments] = useState(String(initial.max_environments));
  const [budget, setBudget] = useState(gibFromMB(initial.memory_budget_mb));
  const [margin, setMargin] = useState(gibFromMB(initial.memory_margin_mb));
  const [disk, setDisk] = useState(String(initial.max_disk_percent));

  const envN = whole(environments);
  const budgetMB = mbFromGiB(budget);
  const marginMB = mbFromGiB(margin);
  const diskN = disk.trim() === "" ? undefined : Number(disk);
  const errors = {
    environments: envN === undefined || envN < 1 || envN > MAX_ENVIRONMENTS ? t("settings.capacity.range", { min: 1, max: MAX_ENVIRONMENTS }) : undefined,
    budget: budgetMB === undefined || budgetMB < MIN_BUDGET_MB ? t("settings.capacity.atLeast", { min: formatMB(MIN_BUDGET_MB) }) : undefined,
    margin: marginMB === undefined || marginMB < 0 ? t("settings.capacity.atLeast", { min: formatMB(0) }) : undefined,
    disk: diskN === undefined || !(diskN > 0 && diskN <= 100) ? t("settings.capacity.range", { min: 1, max: 100 }) : undefined,
  };
  const invalid = Object.values(errors).some(Boolean);
  const host = initial.host_memory_total_mb;
  const budgetHint = host && budgetMB && budgetMB > host ? t("settings.capacity.overcommit", { total: formatMB(host) }) : host ? t("settings.capacity.hostMemory", { total: formatMB(host) }) : undefined;

  const save = () =>
    admin.run(t("settings.capacity.saved"), async () => {
      unwrap(
        await api.PUT("/api/v1/capacity", {
          body: { max_environments: envN!, memory_budget_mb: budgetMB!, memory_margin_mb: marginMB!, max_disk_percent: diskN! },
        }),
      );
      for (const key of ["capacity", "settings", "overview"]) await qc.invalidateQueries({ queryKey: [key] });
    });
  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-kumo-subtle">{t("settings.capacity.intro")}</p>
      {/* items-start: a field without a hint keeps its own height, so the inputs line up. */}
      <div className="grid items-start gap-4 sm:grid-cols-2">
        <Input
          {...helpField(`${id}-environments`, t("settings.capacity.maxEnvironments"), t("settings.capacity.maxEnvironmentsTip"))}
          type="number"
          min={1}
          max={MAX_ENVIRONMENTS}
          value={environments}
          onChange={(e) => setEnvironments(e.target.value)}
          error={errors.environments}
        />
        <Input
          {...helpField(`${id}-budget`, t("settings.capacity.memoryBudget"), t("settings.capacity.memoryBudgetTip"))}
          type="number"
          inputMode="decimal"
          min={MIN_BUDGET_MB / 1024}
          step="any"
          value={budget}
          onChange={(e) => setBudget(e.target.value)}
          error={errors.budget}
          description={errors.budget ? undefined : budgetHint}
        />
        <Input
          {...helpField(`${id}-margin`, t("settings.capacity.memoryMargin"), t("settings.capacity.memoryMarginTip"))}
          type="number"
          inputMode="decimal"
          min={0}
          step="any"
          value={margin}
          onChange={(e) => setMargin(e.target.value)}
          error={errors.margin}
        />
        <Input
          {...helpField(`${id}-disk`, t("settings.capacity.maxDisk"), t("settings.capacity.maxDiskTip"))}
          type="number"
          min={1}
          max={100}
          value={disk}
          onChange={(e) => setDisk(e.target.value)}
          error={errors.disk}
        />
      </div>
      <div>
        <Button variant="primary" loading={admin.busy} disabled={invalid} onClick={save}>
          {t("settings.capacity.save")}
        </Button>
      </div>
    </div>
  );
}

/** The capacity limits: edited here, unless ghrm.yaml has a capacity section. */
export function CapacityCard() {
  const t = useT();
  const capacity = useCapacity();
  const c = capacity.data;
  return (
    <section aria-labelledby="capacity-title">
      <LayerCard>
        <LayerCard.Secondary>
          <h2 id="capacity-title" className="text-base font-medium">
            {t("settings.sections.capacity")}
          </h2>
        </LayerCard.Secondary>
        <LayerCard.Primary className="flex flex-col gap-4 p-4">
          {capacity.isPending ? (
            <Loading />
          ) : capacity.error || !c ? (
            <ErrorState error={capacity.error} />
          ) : c.source === "file" ? (
            <>
              <Banner variant="secondary" icon={<InfoIcon weight="fill" />} title={t("settings.capacity.inFile")} />
              <DefinitionList
                items={[
                  [t("settings.fields.maxEnvironments"), String(c.max_environments)],
                  [t("settings.fields.memoryBudget"), formatMB(c.memory_budget_mb)],
                  [t("settings.fields.memoryMargin"), formatMB(c.memory_margin_mb)],
                  [t("settings.fields.maxDisk"), formatPercent(c.max_disk_percent / 100)],
                ]}
              />
            </>
          ) : (
            <CapacityForm initial={c} />
          )}
        </LayerCard.Primary>
      </LayerCard>
    </section>
  );
}
