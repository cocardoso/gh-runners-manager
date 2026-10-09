import { gibFromMB, mbFromGiB } from "./memory";

test("memory is shown in GiB and sent in MB", () => {
  expect(gibFromMB(4096)).toBe("4");
  expect(gibFromMB(1536)).toBe("1.5");
  expect(gibFromMB(3000)).toBe("2.93");
  expect(mbFromGiB("3")).toBe(3072);
  expect(mbFromGiB("1.5")).toBe(1536);
  expect(mbFromGiB("1,5")).toBe(1536);
  expect(mbFromGiB(gibFromMB(4096))).toBe(4096);
  expect(mbFromGiB("")).toBeUndefined();
  expect(mbFromGiB("abc")).toBeUndefined();
});
