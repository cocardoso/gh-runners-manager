import { useEffect, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, type RouterHistory } from "@tanstack/react-router";
import { Toasty, TooltipProvider } from "@cloudflare/kumo";
import { LiveProvider } from "@/lib/live";
import { onUnauthorized } from "@/api/auth-state";
import { I18nProvider, I18nRemount, type Locale } from "@/i18n";
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
  locale,
}: {
  history?: RouterHistory;
  queryClient?: QueryClient;
  createEventSource?: (url: string) => EventSource;
  /** Fixes the language (tests); otherwise the stored choice or the browser's. */
  locale?: Locale;
}) {
  const [qc] = useState(() => queryClient ?? newQueryClient());
  const [router] = useState(() => createAppRouter(history));
  // A 401 means the session ended: re-read it, which shows the sign-in page.
  useEffect(() => {
    onUnauthorized(() => void qc.invalidateQueries({ queryKey: ["session"] }));
    return () => onUnauthorized(null);
  }, [qc]);
  return (
    <I18nProvider initial={locale}>
      <QueryClientProvider client={qc}>
        <LiveProvider createEventSource={createEventSource}>
          <TooltipProvider>
            <Toasty>
              <I18nRemount>
                <RouterProvider router={router} />
              </I18nRemount>
            </Toasty>
          </TooltipProvider>
        </LiveProvider>
      </QueryClientProvider>
    </I18nProvider>
  );
}
