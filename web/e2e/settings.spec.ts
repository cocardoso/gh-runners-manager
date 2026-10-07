import { expect, test, trackErrors } from "./fixtures";

test("a credential and a scale set are created, tested and removed from the UI", async ({ page, demo }) => {
  const errors = trackErrors(page);
  await page.goto(`${demo.url}/settings`);
  await page.getByRole("button", { name: "Add credential" }).click();
  await page.getByLabel("Name").fill("e2e-cred");
  await page.getByLabel("Token").fill("github_pat_e2e_token");
  await page.getByRole("button", { name: "Save credential" }).click();
  const row = page.getByRole("row").filter({ hasText: "e2e-cred" });
  await expect(row.getByText("…oken")).toBeVisible();
  await row.getByRole("button", { name: "Test e2e-cred" }).click();
  await expect(row.getByText(/Works: signed in as demo-user/)).toBeVisible();

  await page.goto(`${demo.url}/scale-sets`);
  await page.getByRole("button", { name: "New scale set" }).click();
  await page.getByLabel("Name").fill("e2e-set");
  await page.getByLabel("Repository or organization URL").fill("https://github.com/octo/e2e");
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
