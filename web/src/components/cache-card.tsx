import { Badge, LayerCard, Meter, Table } from "@cloudflare/kumo";
import { useCache } from "@/api/queries";
import { ErrorState, Loading, RelativeTime } from "./common";

const gb = (bytes: number) => `${Math.round(bytes / 1024 ** 3)} GB`;

function ratio(hits: number, misses: number) {
  const total = hits + misses;
  return total > 0 ? `${Math.round((hits / total) * 100)}%` : "—";
}

/** The registry cache on the job network: whether each origin answers, how often images come from it, and its disk. */
// checked is false until the control plane has checked the cache once (Go's zero time).
function checked(at: string | undefined) {
  return !!at && !at.startsWith("0001-");
}

export function CacheCard() {
  const cache = useCache();
  return (
    <section aria-labelledby="cache-title">
      <LayerCard>
        <LayerCard.Secondary>
          <h2 id="cache-title" className="text-base font-medium">
            Registry cache
          </h2>
        </LayerCard.Secondary>
        <LayerCard.Primary className="flex flex-col gap-4 p-4">
          {cache.isLoading ? (
            <Loading />
          ) : cache.error || !cache.data ? (
            <ErrorState error={cache.error} />
          ) : !cache.data.enabled ? (
            <p className="text-sm text-kumo-subtle">
              No registry cache is configured. With one (install.sh creates it), jobs pull Docker Hub, GHCR, MCR and Quay images from the job network instead of
              the internet.
            </p>
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <span className="font-mono">{cache.data.address}</span>
                {!checked(cache.data.checked_at) ? (
                  <Badge variant="neutral" appearance="dot">
                    Checking…
                  </Badge>
                ) : cache.data.up ? (
                  <Badge variant="success" appearance="dot">
                    Answering
                  </Badge>
                ) : (
                  <Badge variant="error" appearance="dot">
                    Not answering: jobs pull from the registries
                  </Badge>
                )}
                {checked(cache.data.checked_at) && (
                  <span className="text-kumo-subtle">
                    {!cache.data.up && cache.data.down_since ? (
                      <>
                        down since <RelativeTime value={cache.data.down_since} />,{" "}
                      </>
                    ) : null}
                    checked <RelativeTime value={cache.data.checked_at} />
                  </span>
                )}
              </div>
              {cache.data.disk_budget_bytes > 0 && (
                <Meter
                  label="Disk"
                  value={cache.data.disk_used_bytes}
                  max={cache.data.disk_budget_bytes}
                  customValue={`${gb(cache.data.disk_used_bytes)} of ${gb(cache.data.disk_budget_bytes)}`}
                />
              )}
              <Table aria-label="Registry cache origins">
                <Table.Header>
                  <Table.Row>
                    <Table.Head>Registry</Table.Head>
                    <Table.Head>State</Table.Head>
                    <Table.Head>From the cache</Table.Head>
                    <Table.Head>Blobs (cache / registry)</Table.Head>
                  </Table.Row>
                </Table.Header>
                <Table.Body>
                  {(cache.data.origins ?? []).map((o) => (
                    <Table.Row key={o.origin}>
                      <Table.Cell className="font-mono text-sm">{o.origin}</Table.Cell>
                      <Table.Cell>
                        {o.up ? (
                          <Badge variant="success" appearance="dot">
                            Answering
                          </Badge>
                        ) : (
                          <span className="text-sm text-kumo-danger">{o.error || "Not answering"}</span>
                        )}
                      </Table.Cell>
                      <Table.Cell className="tabular-nums">{ratio(o.blob_hits, o.blob_misses)}</Table.Cell>
                      <Table.Cell className="tabular-nums">
                        {Math.round(o.blob_hits)} / {Math.round(o.blob_misses)}
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
