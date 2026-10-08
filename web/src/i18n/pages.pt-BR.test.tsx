import { screen } from "@testing-library/react";
import { renderApp } from "@/test/render-app";
import { env, mockApi } from "@/test/api-mock";
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
