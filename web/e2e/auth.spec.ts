import { demoAccount, expect, test } from "./fixtures";

// These tests start without a session.
test("signing in, using the app, and signing out", async ({ browser, demo }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(`${demo.url}/jobs`);
  await page.getByLabel("Username").fill(demoAccount.username);
  await page.getByLabel("Password").fill("not the password");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByText(/wrong username or password/)).toBeVisible();
  await page.getByLabel("Password").fill(demoAccount.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Jobs", exact: true }).first()).toBeVisible();
  const cookies = await context.cookies();
  const session = cookies.find((c) => c.name === "ghrm_session");
  expect(session?.httpOnly).toBe(true);
  expect(session?.sameSite).toBe("Strict");

  await page.getByRole("button", { name: /^Account/ }).click();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  await page.goto(`${demo.url}/environments`);
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  await context.close();
});

test("the API refuses anonymous calls", async ({ playwright, demo }) => {
  const anon = await playwright.request.newContext();
  expect((await anon.get(`${demo.url}/api/v1/environments`)).status()).toBe(401);
  expect((await anon.get(`${demo.url}/api/v1/auth/session`)).status()).toBe(200);
  await anon.dispose();
});
