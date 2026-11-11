import test from "node:test";
import assert from "node:assert/strict";
import {
  discoverySupported,
  draftFor,
  selectionDirty,
  canResolve,
  proposalChanged,
  pageChanged,
} from "../../src/admin/versions/discoveryFlow.ts";
const plan = {
  id: "a",
  revision: 1,
  state: "pending",
  kind: "create_work",
  new_edition_ids: ["a", "b", "c"],
  primary: true,
};
test("enabled old capability never enables discovery", () => {
  assert.equal(discoverySupported({ enabled: true, available: true }), false);
  assert.equal(
    discoverySupported({ discovery: true, proposals: false }),
    false,
  );
  assert.equal(discoverySupported({ discovery: true, proposals: true }), true);
});
test("member exclusion remains a frozen draft until explicit resolve", () => {
  const d = draftFor(plan);
  d.selected = ["a", "b"];
  assert.equal(selectionDirty(d), true);
  assert.equal(canResolve(d), true);
  const incoming = { ...plan, revision: 2 };
  assert.equal(proposalChanged(incoming, d.baseline), true);
  assert.equal(d.baseline.revision, 1);
  assert.deepEqual(d.selected, ["a", "b"]);
  assert.equal(
    selectionDirty(draftFor({ ...incoming, new_edition_ids: d.selected })),
    false,
  );
});
test("empty, insufficient and foreign members cannot be resolved", () => {
  for (const selected of [[], ["a"], ["a", "foreign"], ["a", "a"]])
    assert.equal(canResolve({ ...draftFor(plan), selected }), false);
  assert.equal(
    canResolve({
      ...draftFor({ ...plan, kind: "add_to_work" }),
      selected: ["a"],
    }),
    true,
  );
});
test("background changes are detected without treating old responses as new", () => {
  assert.equal(proposalChanged({ ...plan, revision: 0 }, plan), false);
  assert.equal(proposalChanged({ ...plan, state: "superseded" }, plan), true);
  assert.equal(pageChanged([plan], [{ ...plan, revision: 2 }]), true);
  assert.equal(pageChanged([plan], [{ ...plan }]), false);
  assert.equal(pageChanged([plan], []), true);
});
