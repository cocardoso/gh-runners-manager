import { forwardRef } from "react";
import { Link } from "@tanstack/react-router";
import type { LinkComponentProps } from "@cloudflare/kumo";

/** Lets Kumo components (sidebar, breadcrumbs, links) navigate with the client-side router. */
export const AppLink = forwardRef<HTMLAnchorElement, LinkComponentProps>(function AppLink({ href, ...props }, ref) {
  if (!href || /^[a-z]+:/i.test(href) || props.target === "_blank") return <a ref={ref} href={href} {...props} />;
  const { to: _to, ...rest } = props as LinkComponentProps & { to?: string };
  void _to;
  return <Link ref={ref} to={href} {...rest} />;
});
