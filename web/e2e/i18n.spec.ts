import { expect, test, trackErrors } from "./fixtures";

test("the language menu switches the interface and the choice survives a reload", async ({ page, demo }) => {
  const errors = trackErrors(page);
  await page.goto(`${demo.url}/`);
  await expect(page.getByRole("link", { name: "Overview" })).toBeVisible();
  await page.getByRole("button", { name: /Language/ }).click();
  await page.getByRole("menuitemradio", { name: "Français" }).click();
  await expect(page.getByRole("link", { name: "Vue d'ensemble" })).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
  await page.reload();
  await expect(page.getByRole("link", { name: "Vue d'ensemble" })).toBeVisible();
  // Back to English for the other tests sharing this browser storage.
  await page.getByRole("button", { name: /Langue/ }).click();
  await page.getByRole("menuitemradio", { name: "English" }).click();
  await expect(page.getByRole("link", { name: "Overview" })).toBeVisible();
  expect(errors).toEqual([]);
});
