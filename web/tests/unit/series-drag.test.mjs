import test from "node:test";
import assert from "node:assert/strict";
import {
  placeSeriesBlock,
  seriesJoinOrder,
  nextSeriesReview,
} from "../../src/admin/series/seriesDragFlow.ts";
test("a drag moves a complete block and preserves existing relative order", () => {
  const all = ["a", "b", "c", "d"];
  assert.deepEqual(
    placeSeriesBlock(all, ["c", "d"], { row: "b", edge: "before" }),
    ["a", "c", "d", "b"],
  );
  assert.deepEqual(placeSeriesBlock(all, ["a"], { row: "c", edge: "after" }), [
    "b",
    "c",
    "a",
    "d",
  ]);
  assert.deepEqual(placeSeriesBlock(all, ["a"], { row: "", edge: "before" }), [
    "b",
    "c",
    "d",
    "a",
  ]);
  assert.deepEqual(
    placeSeriesBlock(all, ["a"], { row: "a", edge: "after" }),
    all,
  );
  assert.deepEqual(all, ["a", "b", "c", "d"]);
  assert.throws(() =>
    placeSeriesBlock(all, ["missing"], { row: "a", edge: "before" }),
  );
});
test("joining trusts explicit placement and otherwise appends, without guessing titles", () => {
  const p = {
    order: ["a", "x", "b", "y"],
    new_work_ids: ["x", "y"],
    existing_work_ids: ["a", "b"],
    order_basis: "contextual",
  };
  assert.deepEqual(seriesJoinOrder(p), p.order);
  assert.deepEqual(seriesJoinOrder({ ...p, order_basis: "natural" }), [
    "a",
    "b",
    "x",
    "y",
  ]);
  assert.deepEqual(seriesJoinOrder(p, { row: "a", edge: "before" }), [
    "x",
    "y",
    "a",
    "b",
  ]);
});
test("continuous review selects the vacated position and handles last/empty pages", () => {
  assert.equal(nextSeriesReview([{ id: "b" }, { id: "c" }], 0).id, "b");
  assert.equal(nextSeriesReview([{ id: "b" }], 24).id, "b");
  assert.equal(nextSeriesReview([], 0), undefined);
});
