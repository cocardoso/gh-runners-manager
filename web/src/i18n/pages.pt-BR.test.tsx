import { screen } from "@testing-library/react";
import { renderApp } from "@/test/render-app";
import { env, job, mockApi, scaleSet } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

// One page per area, in Portuguese: the dictionaries are wired, not only filled.
test.each([
  ["/", "Visão geral"],
  ["/environments", "Ambientes"],
  ["/settings", "Configurações"],
])("%s renders in pt-BR", async (path, heading) => {
  mockApi({ "/api/v1/environments/env-1": env() });
  renderApp(path, { locale: "pt-BR" });
  expect(await screen.findByRole("heading", { level: 1, name: heading })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Visão geral" })).toBeInTheDocument();
});

test("the settings page cards are in pt-BR", async () => {
  mockApi();
  renderApp("/settings", { locale: "pt-BR" });
  expect(await screen.findByRole("heading", { name: "Histórico" })).toBeInTheDocument();
  expect(await screen.findByRole("button", { name: /Limpar agora/ })).toBeInTheDocument();
});

test("the sign-in page is in pt-BR", async () => {
  mockApi({ "/api/v1/auth/session": { state: "signed_out" } });
  renderApp("/", { locale: "pt-BR" });
  expect(await screen.findByRole("button", { name: "Entrar" })).toBeInTheDocument();
});

test("pagination speaks Portuguese", async () => {
  const jobs = Array.from({ length: 30 }, (_, i) => job({ id: `job-${i}`, status: "completed", result: "succeeded" }));
  mockApi({ "/api/v1/jobs": { jobs } });
  renderApp("/jobs", { locale: "pt-BR" });
  expect(await screen.findByText(/Mostrando 1.25 de 30/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Próxima página" })).toBeInTheDocument();
});

test("copy buttons and the sidebar toggle speak Portuguese", async () => {
  mockApi({ "/api/v1/scale-sets": { scale_sets: [scaleSet()] } });
  renderApp("/scale-sets", { locale: "pt-BR" });
  expect(await screen.findByRole("button", { name: "Copiar para a área de transferência" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Alternar barra lateral" })).toBeInTheDocument();
});
