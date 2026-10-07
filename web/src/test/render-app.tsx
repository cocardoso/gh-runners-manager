import { render } from "@testing-library/react";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "@/app";
import { FakeEventSource } from "./fake-event-source";

export function renderApp(path = "/") {
  const history = createMemoryHistory({ initialEntries: [path] });
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(<App history={history} queryClient={queryClient} createEventSource={(u) => new FakeEventSource(u) as unknown as EventSource} />);
  return { ...utils, history, queryClient };
}
