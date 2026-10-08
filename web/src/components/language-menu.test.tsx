import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => {
  FakeEventSource.reset();
  localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

test("the language menu lists every language and remembers the choice", async () => {
  mockApi();
  const user = userEvent.setup();
  renderApp("/");
  await user.click(await screen.findByRole("button", { name: /Language/ }));
  for (const name of ["English", "Português (Brasil)", "Español", "Français", "Italiano"]) {
    expect(await screen.findByRole("menuitemradio", { name })).toBeInTheDocument();
  }
  await user.click(screen.getByRole("menuitemradio", { name: "Português (Brasil)" }));
  await waitFor(() => expect(document.documentElement.lang).toBe("pt-BR"));
  expect(localStorage.getItem("ghrm.locale")).toBe("pt-BR");
  // Keyboard and screen-reader users stay where they were.
  await waitFor(() => expect(document.activeElement?.getAttribute("aria-label")).toMatch(/^Idioma/));
});

test("the sign-in page offers the language menu too", async () => {
  mockApi({ "/api/v1/auth/session": { state: "signed_out" } });
  renderApp("/");
  expect(await screen.findByRole("button", { name: /Language/ })).toBeInTheDocument();
});
