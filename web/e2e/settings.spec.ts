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
  await form.getByRole("combobox", { name: "Credential", exact: true }).click();
  await page.getByRole("option", { name: "e2e-cred" }).click();
  await form.getByRole("combobox", { name: "Repository or organization", exact: true }).click();
  await page.getByRole("option", { name: /octo\/infra/ }).click();
  await expect(form.getByLabel("Name")).toHaveValue("infra");
  await form.getByLabel("Name").fill("e2e-set");
  await page.getByRole("button", { name: "Save scale set" }).click();
  const card = page.locator("section").filter({ has: page.getByRole("heading", { name: "e2e-set", exact: true }) });
  await expect(card.getByText("Listening")).toBeVisible();
  await expect(card.getByText("Created in the UI")).toBeVisible();

  await card.getByRole("button", { name: "Remove e2e-set" }).click();
  await page.getByRole("textbox", { name: "Type e2e-set to confirm deletion" }).fill("e2e-set");
  await page.getByRole("button", { name: "Remove scale set" }).click();
  // exact: the "e2e-set removed" toast has a heading too.
  await expect(page.getByRole("heading", { name: "e2e-set", exact: true })).toBeHidden();
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

test("the history fields line up, with or without a hint below", async ({ page, demo }) => {
  await page.goto(`${demo.url}/settings`);
  const top = async (name: string) => (await page.getByRole("spinbutton", { name, exact: true }).boundingBox())!.y;
  expect(await top("Keep history (days)")).toBe(await top("Keep audit events (days)"));
});

test("a template profile is created, offered to scale sets and deleted", async ({ page, demo }) => {
  const errors = trackErrors(page);
  await page.goto(`${demo.url}/templates?tab=profiles`);
  await page.getByRole("button", { name: "New profile" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Name").fill("e2e-lean");
  await dialog.getByRole("checkbox", { name: "Azure CLI", exact: true }).click();
  await dialog.getByLabel("Go", { exact: true }).fill("1.24");
  await dialog.getByRole("button", { name: "Save profile" }).click();
  const card = page.getByRole("region", { name: "e2e-lean" });
  await expect(card.getByText("Go 1.24")).toBeVisible();
  await expect(card.getByText(/Azure CLI · Azure CLI \(azure-devops\)/)).toBeVisible();

  await page.goto(`${demo.url}/scale-sets`);
  await page.getByRole("button", { name: "New scale set" }).click();
  await page.getByRole("dialog").getByRole("combobox", { name: "Template profile" }).click();
  await expect(page.getByRole("option", { name: "e2e-lean" })).toBeVisible();
  await page.keyboard.press("Escape");
  await page.keyboard.press("Escape");

  await page.goto(`${demo.url}/templates?tab=profiles`);
  await page.getByRole("button", { name: "Delete e2e-lean" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Delete" }).click();
  await expect(page.getByRole("region", { name: "e2e-lean" })).toBeHidden();
  expect(errors).toEqual([]);
});
