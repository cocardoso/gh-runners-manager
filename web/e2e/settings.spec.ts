import { expect, test, trackErrors } from "./fixtures";

test("a credential and a scale set are created, tested and removed from the UI", async ({ page, demo }) => {
  const errors = trackErrors(page);
  await page.goto(`${demo.url}/settings`);
  await page.getByRole("button", { name: "Add credential" }).click();
  expect(await page.getByRole("dialog").evaluate((d) => d.getBoundingClientRect().width)).toBeGreaterThan(700);
  await page.getByLabel("Name").fill("e2e-cred");
  await page.getByLabel("Token").fill("github_pat_e2e_token");
  await page.getByRole("button", { name: "Save credential" }).click();
  const row = page.getByRole("row").filter({ hasText: "e2e-cred" });
  await expect(row.getByText("…oken")).toBeVisible();
  await row.getByRole("button", { name: "Test e2e-cred" }).click();
  await expect(row.getByText(/Works: signed in as demo-user/)).toBeVisible();

  await page.goto(`${demo.url}/scale-sets`);
  await page.getByRole("button", { name: "New scale set" }).click();
  const form = page.getByRole("dialog");
  // The repository is picked from what the credential reaches (the demo answers like GitHub).
  await form.getByRole("combobox", { name: /^Credential/ }).click();
  await page.getByRole("option", { name: "e2e-cred" }).click();
  await form.getByRole("combobox", { name: /^Repository or organization/ }).click();
  await page.getByRole("option", { name: /octo\/infra/ }).click();
  await expect(form.getByLabel("Name")).toHaveValue("infra");
  await form.getByLabel("Name").fill("e2e-set");
  await page.getByRole("button", { name: "Save scale set" }).click();
  const card = page.locator("section").filter({ has: page.getByRole("heading", { name: "e2e-set" }) });
  await expect(card.getByText("Listening")).toBeVisible();
  await expect(card.getByText("Created in the UI")).toBeVisible();

  await card.getByRole("button", { name: "Remove e2e-set" }).click();
  await page.getByRole("textbox", { name: "Type e2e-set to confirm deletion" }).fill("e2e-set");
  await page.getByRole("button", { name: "Remove scale set" }).click();
  await expect(page.getByRole("heading", { name: "e2e-set" })).toBeHidden();
  expect(errors).toEqual([]);
});

test("history settings are saved and a cleanup previews what it deletes", async ({ page, demo }) => {
  const errors = trackErrors(page);
  await page.goto(`${demo.url}/settings`);
  const card = page.locator("section").filter({ has: page.getByRole("heading", { name: "History" }) });
  await card.getByRole("radio", { name: "Manual" }).click();
  await card.getByLabel("Keep history (days)").fill("14");
  await card.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("History settings saved")).toBeVisible();
  await page.reload();
  await expect(card.getByRole("radio", { name: "Manual" })).toBeChecked();
  await expect(card.getByLabel("Keep history (days)")).toHaveValue("14");

  await card.getByRole("button", { name: /Clean up now/ }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("status")).toHaveText(/Nothing to delete|environment/);
  await dialog.getByRole("button", { name: "Cancel" }).click();
  expect(errors).toEqual([]);
});

test("the scale set form keeps its buttons in view and fits a phone", async ({ page, demo }) => {
  await page.goto(`${demo.url}/scale-sets`);
  await page.getByRole("button", { name: "New scale set" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Save scale set" })).toBeInViewport();
  const width = await dialog.evaluate((d) => d.getBoundingClientRect().width);
  expect(width).toBeGreaterThan(700); // the wide layout on a desktop
  expect(await dialog.evaluate((d) => d.scrollWidth <= d.clientWidth)).toBe(true);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload(); // lay the page out at phone size, as a phone would
  await page.getByRole("button", { name: "New scale set" }).click();
  await expect(dialog.getByRole("button", { name: "Save scale set" })).toBeInViewport();
  expect(await dialog.evaluate((d) => d.scrollWidth <= d.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
