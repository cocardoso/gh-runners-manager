import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useJobGitHub } from "./queries";
import { mockApi } from "@/test/api-mock";

afterEach(() => vi.unstubAllGlobals());

test("job events do not refetch the GitHub details", async () => {
  const calls = mockApi({ "/api/v1/jobs/j1/github": { available: false, reason: "x", steps: [] } });
  const qc = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  const { result } = renderHook(() => useJobGitHub("j1"), { wrapper });
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  await qc.invalidateQueries({ queryKey: ["job", "j1"] });
  expect(calls.filter((c) => c.url.pathname.endsWith("/github"))).toHaveLength(1);
});
