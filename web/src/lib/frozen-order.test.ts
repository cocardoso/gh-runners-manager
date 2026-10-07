import { renderHook } from "@testing-library/react";
import { useFrozenOrder } from "./frozen-order";

type Row = { id: string; v: number };
const key = (r: Row) => r.id;

test("while frozen, rows keep their order, update in place and new rows wait", () => {
  const { result, rerender } = renderHook(({ items, frozen }) => useFrozenOrder(items, key, frozen), {
    initialProps: { items: [{ id: "a", v: 1 }, { id: "b", v: 1 }], frozen: false },
  });
  expect(result.current.rows.map((r) => r.id)).toEqual(["a", "b"]);
  rerender({ items: [{ id: "c", v: 1 }, { id: "b", v: 2 }, { id: "a", v: 1 }], frozen: true });
  expect(result.current.rows).toEqual([{ id: "a", v: 1 }, { id: "b", v: 2 }]);
  expect(result.current.pending).toBe(1);
  rerender({ items: [{ id: "c", v: 1 }, { id: "b", v: 2 }, { id: "a", v: 1 }], frozen: false });
  expect(result.current.rows.map((r) => r.id)).toEqual(["c", "b", "a"]);
  expect(result.current.pending).toBe(0);
});
