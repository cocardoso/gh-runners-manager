import { forwardRef } from "react";
import { Link } from "@tanstack/react-router";
import type { LinkComponentProps } from "@cloudflare/kumo";

/** Lets Kumo components (sidebar, breadcrumbs, links) navigate with the client-side router.
 * Kumo passes the destination as href (links, sidebar) or as to (breadcrumbs). */
export const AppLink = forwardRef<HTMLAnchorElement, LinkComponentProps>(function AppLink({ href: hrefProp, ...props }, ref) {
  const { to, ...rest } = props as LinkComponentProps & { to?: string };
  const href = hrefProp ?? to;
  if (!href || /^[a-z]+:/i.test(href) || props.target === "_blank") return <a ref={ref} href={href} {...rest} />;
  // The router takes the path and the query apart: a query left in `to` is dropped.
  const [path, query = ""] = href.split("?");
  const search = Object.fromEntries(new URLSearchParams(query));
  return <Link ref={ref} to={path} search={search as never} {...rest} />;
});
