import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

test("signing in shows the page that was asked for", async () => {
  let session: unknown = { state: "signed_out" };
  const calls = mockApi({
    "/api/v1/auth/session": () => session,
    "POST /api/v1/auth/login": () => {
      session = { state: "signed_in", username: "admin", csrf: "c2" };
      return json(session);
    },
  });
  const user = userEvent.setup();
  renderApp("/jobs");
  await user.type(await screen.findByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "correct horse battery");
  await user.click(screen.getByRole("button", { name: "Sign in" }));
  expect(await screen.findByRole("heading", { name: "Jobs" })).toBeInTheDocument();
  const login = calls.find((c) => c.method === "POST")!;
  expect(login.url.pathname).toBe("/api/v1/auth/login");
});

test("a wrong password is reported", async () => {
  mockApi({
    "/api/v1/auth/session": { state: "signed_out" },
    "POST /api/v1/auth/login": () => json({ status: 401, title: "Unauthorized", detail: "auth: wrong username or password" }, 401),
  });
  const user = userEvent.setup();
  renderApp("/");
  await user.type(await screen.findByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "nope nope nope");
  await user.click(screen.getByRole("button", { name: "Sign in" }));
  expect(await screen.findByText(/wrong username or password/)).toBeInTheDocument();
});

test("first-run setup needs the setup token and a long, confirmed password", async () => {
  let session: unknown = { state: "setup" };
  const calls = mockApi({
    "/api/v1/auth/session": () => session,
    "POST /api/v1/auth/setup": () => {
      session = { state: "signed_out" };
      return json(session);
    },
  });
  const user = userEvent.setup();
  renderApp("/");
  await user.type(await screen.findByLabelText("Setup token"), "tok");
  expect(screen.getByLabelText("Username")).toHaveValue("admin"); // suggested
  await user.type(screen.getByLabelText("Password"), "short");
  await user.type(screen.getByLabelText("Confirm password"), "short");
  expect(screen.getByRole("button", { name: "Create account" })).toBeDisabled();
  expect(screen.getByText(/at least 12 characters/)).toBeInTheDocument();
  await user.clear(screen.getByLabelText("Password"));
  await user.type(screen.getByLabelText("Password"), "correct horse battery");
  await user.clear(screen.getByLabelText("Confirm password"));
  await user.type(screen.getByLabelText("Confirm password"), "correct horse batterY");
  expect(screen.getByRole("button", { name: "Create account" })).toBeDisabled();
  await user.clear(screen.getByLabelText("Confirm password"));
  await user.type(screen.getByLabelText("Confirm password"), "correct horse battery");
  await user.click(screen.getByRole("button", { name: "Create account" }));
  expect(await screen.findByRole("button", { name: "Sign in" })).toBeInTheDocument();
  const body = await (calls.find((c) => c.method === "POST")!.request as Request).clone().json();
  expect(body).toEqual({ setup_token: "tok", username: "admin", password: "correct horse battery" });
});

test("an expired session sends the user back to sign in", async () => {
  let session: unknown = { state: "signed_in", username: "admin", csrf: "c" };
  mockApi({
    "/api/v1/auth/session": () => session,
    "/api/v1/jobs": () => {
      session = { state: "signed_out" };
      return json({ status: 401, title: "Unauthorized", detail: "sign in first" }, 401);
    },
  });
  renderApp("/jobs");
  expect(await screen.findByRole("button", { name: "Sign in" })).toBeInTheDocument();
});

test("the account page changes the password and signs out", async () => {
  let session: unknown = { state: "signed_in", username: "admin", csrf: "c9" };
  const calls = mockApi({
    "/api/v1/auth/session": () => session,
    "POST /api/v1/auth/password": () => new Response(null, { status: 204 }),
    "POST /api/v1/auth/logout": () => {
      session = { state: "signed_out" };
      return new Response(null, { status: 204 });
    },
  });
  const user = userEvent.setup();
  renderApp("/account");
  await user.type(await screen.findByLabelText("Current password"), "correct horse battery");
  await user.type(screen.getByLabelText("New password"), "another long password");
  await user.type(screen.getByLabelText("Confirm new password"), "another long password");
  await user.click(screen.getByRole("button", { name: "Change password" }));
  expect(await screen.findByText("Password changed")).toBeInTheDocument();
  const change = calls.find((c) => c.url.pathname === "/api/v1/auth/password")!;
  expect(change.headers.get("X-CSRF-Token")).toBe("c9");
  await user.click(screen.getByRole("button", { name: "Sign out" }));
  expect(await screen.findByRole("button", { name: "Sign in" })).toBeInTheDocument();
  await waitFor(() => expect(calls.some((c) => c.url.pathname === "/api/v1/auth/logout")).toBe(true));
});
