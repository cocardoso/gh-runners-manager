import { render } from "@testing-library/react";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "@/app";
import type { Locale } from "@/i18n";
import { FakeEventSource } from "./fake-event-source";

export function renderApp(path = "/", { locale = "en" }: { locale?: Locale } = {}) {
  const history = createMemoryHistory({ initialEntries: [path] });
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(<App history={history} queryClient={queryClient} createEventSource={(u) => new FakeEventSource(u) as unknown as EventSource} locale={locale} />);
  return { ...utils, history, queryClient };
}
