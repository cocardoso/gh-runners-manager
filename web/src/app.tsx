import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, type RouterHistory } from "@tanstack/react-router";
import { Toasty, TooltipProvider } from "@cloudflare/kumo";
import { LiveProvider } from "@/lib/live";
import { createAppRouter } from "./router";

export function newQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // The event stream invalidates queries; polling is a slow safety net.
        staleTime: 10_000,
        refetchInterval: 60_000,
        retry: 2,
      },
    },
  });
}

export function App({
  history,
  queryClient,
  createEventSource,
}: {
  history?: RouterHistory;
  queryClient?: QueryClient;
  createEventSource?: (url: string) => EventSource;
}) {
  const [qc] = useState(() => queryClient ?? newQueryClient());
  const [router] = useState(() => createAppRouter(history));
  return (
    <QueryClientProvider client={qc}>
      <LiveProvider createEventSource={createEventSource}>
        <TooltipProvider>
          <Toasty>
            <RouterProvider router={router} />
          </Toasty>
        </TooltipProvider>
      </LiveProvider>
    </QueryClientProvider>
  );
}
