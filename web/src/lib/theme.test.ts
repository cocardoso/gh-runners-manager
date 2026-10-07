import { readThemePreference } from "./theme";

test("blocked storage falls back to the system theme", () => {
  const get = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw new DOMException("blocked", "SecurityError");
  });
  expect(readThemePreference()).toBe("system");
  get.mockRestore();
});
