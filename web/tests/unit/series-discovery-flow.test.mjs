import test from "node:test";
import assert from "node:assert/strict";
import {
  seriesDraft,
  seriesOrder,
  seriesDirty,
  canResolveSeries,
  moveSeriesWork,
  seriesChanged,
  seriesOrderNotice,
} from "../../src/admin/series/seriesDiscoveryFlow.ts";
const plan = {
  id: "p",
  kind: "create_series",
  revision: 2,
  state: "pending",
  new_work_ids: ["a", "b", "c"],
  existing_work_ids: [],
  order: ["a", "b", "c"],
  name: "Memory",
};
test("series drafts preserve their original revision and order", () => {
  const d = seriesDraft(plan);
  assert.equal(seriesDirty(d), false);
  const moved = moveSeriesWork(d, "c", -1);
  assert.deepEqual(seriesOrder(moved), ["a", "c", "b"]);
  assert.deepEqual(d.order, ["a", "b", "c"]);
  assert.equal(seriesDirty(moved), true);
  assert.equal(moved.baseline.revision, 2);
  assert.equal(seriesChanged({ ...plan, revision: 3 }, d.baseline), true);
  assert.equal(seriesChanged({ ...plan, revision: 1 }, d.baseline), false);
});
test("series order notices identify inference and preserve legacy behavior", () => {
  assert.match(
    seriesOrderNotice({ ...plan, order_basis: "contextual" }).title,
    /推断/,
  );
  assert.match(
    seriesOrderNotice({ ...plan, order_basis: "explicit" }).title,
    /明确卷序/,
  );
  assert.match(
    seriesOrderNotice({ ...plan, order_basis: "existing" }).description,
    /不会重排/,
  );
  assert.match(seriesOrderNotice(plan).description, /旧版/);
  assert.equal(
    canResolveSeries(seriesDraft({ ...plan, name: "", name_required: true })),
    false,
  );
});
test("excluded members and names require resolve, not silent accept changes", () => {
  let d = seriesDraft(plan);
  d = { ...d, selected: ["a", "c"], name: "New name" };
  assert.equal(seriesDirty(d), true);
  assert.deepEqual(seriesOrder(d), ["a", "c"]);
  assert.equal(canResolveSeries(d), true);
  for (const selected of [[], ["a"], ["a", "foreign"], ["a", "a"]])
    assert.equal(canResolveSeries({ ...d, selected }), false);
  assert.equal(canResolveSeries({ ...d, name: " " }), false);
});
test("existing relative order cannot be changed but new members may be placed", () => {
  const p = {
    ...plan,
    kind: "add_to_series",
    new_work_ids: ["c"],
    existing_work_ids: ["a", "b"],
  };
  const d = seriesDraft(p);
  assert.equal(canResolveSeries(d), true);
  const moved = moveSeriesWork(d, "c", -1);
  assert.deepEqual(seriesOrder(moved), ["a", "c", "b"]);
  assert.equal(canResolveSeries(moved), true);
  assert.equal(moveSeriesWork(d, "a", 1), d);
  assert.equal(canResolveSeries({ ...d, order: ["b", "a", "c"] }), false);
  assert.equal(canResolveSeries({ ...d, selected: [] }), false);
});

test("explicit rebase keeps edits and exclusions while honoring current existing order", async () => {
  const { rebaseSeriesDraft, canResolveSeries } =
    await import("../../src/admin/series/seriesDiscoveryFlow.ts");
  const fresh = {
    id: "fresh",
    kind: "add_to_series",
    name: "Current name",
    new_work_ids: ["x", "y", "new"],
    existing_work_ids: ["b", "a"],
    order: ["b", "a", "x", "y", "new"],
  };
  const d = {
    baseline: { ...fresh, existing_work_ids: ["a", "b"] },
    name: "Old name",
    selected: ["x"],
    order: ["a", "y", "x", "b"],
  };
  const rebased = rebaseSeriesDraft(d, fresh);
  assert.deepEqual(rebased.selected, ["x"]);
  assert.deepEqual(rebased.order, ["b", "y", "x", "a", "new"]);
  assert.equal(rebased.name, "Current name");
  assert.equal(canResolveSeries(rebased), true);
  const created = rebaseSeriesDraft(
    { ...d, name: "Edited name" },
    {
      ...fresh,
      kind: "create_series",
      existing_work_ids: [],
      order: ["x", "y", "new"],
    },
  );
  assert.equal(created.name, "Edited name");
});
