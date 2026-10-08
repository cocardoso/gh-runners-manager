import { Page } from "@/components/common";
import { useT } from "@/i18n";

export function NotFoundPage() {
  const t = useT();
  return <Page title={t("shell.notFound")}>{null}</Page>;
}
