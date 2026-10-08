import type { ReactNode } from "react";
import { Badge, LayerCard, Meter, Table } from "@cloudflare/kumo";
import { useCache } from "@/api/queries";
import { useT } from "@/i18n";
import { formatNumber, formatPercent } from "@/lib/format";
import { ErrorState, Loading, RelativeTime } from "./common";

const gb = (bytes: number) => `${formatNumber(Math.round(bytes / 1024 ** 3))} GB`;

function ratio(hits: number, misses: number) {
  const total = hits + misses;
  return total > 0 ? formatPercent(hits / total) : "—";
}

/** Puts node where {time} is in a translated text. */
function withTime(text: string, node: ReactNode) {
  const [before, after = ""] = text.split("{time}");
  return (
    <>
      {before}
      {node}
      {after}
    </>
  );
}

/** The registry cache on the job network: whether each origin answers, how often images come from it, and its disk. */
// checked is false until the control plane has checked the cache once (Go's zero time).
function checked(at: string | undefined) {
  return !!at && !at.startsWith("0001-");
}

export function CacheCard() {
  const t = useT();
  const cache = useCache();
  return (
    <section aria-labelledby="cache-title">
      <LayerCard>
        <LayerCard.Secondary>
          <h2 id="cache-title" className="text-base font-medium">
            {t("settings.cache.title")}
          </h2>
        </LayerCard.Secondary>
        <LayerCard.Primary className="flex flex-col gap-4 p-4">
          {cache.isLoading ? (
            <Loading />
          ) : cache.error || !cache.data ? (
            <ErrorState error={cache.error} />
          ) : !cache.data.enabled ? (
            <p className="text-sm text-kumo-subtle">{t("settings.cache.none")}</p>
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <span className="font-mono">{cache.data.address}</span>
                {!checked(cache.data.checked_at) ? (
                  <Badge variant="neutral" appearance="dot">
                    {t("settings.cache.checking")}
                  </Badge>
                ) : cache.data.up ? (
                  <Badge variant="success" appearance="dot">
                    {t("settings.cache.answering")}
                  </Badge>
                ) : (
                  <Badge variant="error" appearance="dot">
                    {t("settings.cache.notAnsweringFallback")}
                  </Badge>
                )}
                {checked(cache.data.checked_at) && (
                  <span className="text-kumo-subtle">
                    {!cache.data.up && cache.data.down_since ? (
                      <>
                        {withTime(t("settings.cache.downSince"), <RelativeTime value={cache.data.down_since} />)}{" "}
                      </>
                    ) : null}
                    {withTime(t("settings.cache.checked"), <RelativeTime value={cache.data.checked_at} />)}
                  </span>
                )}
              </div>
              {cache.data.disk_budget_bytes > 0 && (
                <Meter
                  label={t("settings.cache.disk")}
                  value={cache.data.disk_used_bytes}
                  max={cache.data.disk_budget_bytes}
                  customValue={t("settings.cache.diskValue", { used: gb(cache.data.disk_used_bytes), budget: gb(cache.data.disk_budget_bytes) })}
                />
              )}
              <Table aria-label={t("settings.cache.origins")}>
                <Table.Header>
                  <Table.Row>
                    <Table.Head>{t("settings.cache.registry")}</Table.Head>
                    <Table.Head>{t("settings.cache.state")}</Table.Head>
                    <Table.Head>{t("settings.cache.fromCache")}</Table.Head>
                    <Table.Head>{t("settings.cache.blobs")}</Table.Head>
                  </Table.Row>
                </Table.Header>
                <Table.Body>
                  {(cache.data.origins ?? []).map((o) => (
                    <Table.Row key={o.origin}>
                      <Table.Cell className="font-mono text-sm">{o.origin}</Table.Cell>
                      <Table.Cell>
                        {o.up ? (
                          <Badge variant="success" appearance="dot">
                            {t("settings.cache.answering")}
                          </Badge>
                        ) : (
                          <span className="text-sm text-kumo-danger">{o.error || t("settings.cache.notAnswering")}</span>
                        )}
                      </Table.Cell>
                      <Table.Cell className="tabular-nums">{ratio(o.blob_hits, o.blob_misses)}</Table.Cell>
                      <Table.Cell className="tabular-nums">
                        {formatNumber(Math.round(o.blob_hits))} / {formatNumber(Math.round(o.blob_misses))}
                      </Table.Cell>
                    </Table.Row>
                  ))}
                </Table.Body>
              </Table>
            </>
          )}
        </LayerCard.Primary>
      </LayerCard>
    </section>
  );
}
