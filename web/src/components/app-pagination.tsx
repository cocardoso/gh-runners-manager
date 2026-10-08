import { Pagination } from "@cloudflare/kumo";
import { formatNumber } from "@/lib/format";
import { useT } from "@/i18n";

/** Kumo's pagination, in the interface language. */
export function AppPagination(props: { page: number; setPage: (page: number) => void; perPage: number; totalCount: number }) {
  const t = useT();
  return (
    <Pagination
      {...props}
      labels={{
        navigation: t("shell.pagination.navigation"),
        firstPage: t("shell.pagination.firstPage"),
        previousPage: t("shell.pagination.previousPage"),
        nextPage: t("shell.pagination.nextPage"),
        lastPage: t("shell.pagination.lastPage"),
        pageNumber: t("shell.pagination.pageNumber"),
      }}
    >
      <Pagination.Info>
        {({ pageShowingRange, totalCount }) => t("shell.pagination.showing", { range: pageShowingRange, total: formatNumber(totalCount ?? 0) })}
      </Pagination.Info>
      <Pagination.Controls />
    </Pagination>
  );
}
