import test from "node:test";
import assert from "node:assert/strict";
import {
  startTagTrail,
  visitTag,
  backTag,
  currentTrail,
  readTagTrail,
  resolveTagTrail,
  trailState,
  preservedTagIDs,
  dropsTagSessions,
  normalizeTagDetailSearch,
} from "../../src/admin/tags/tagTrail.ts";
const root =
  "?library_id=lib&category=parody&q=stars&page=2&sort=usage&order=desc";
const location = (trail) => ({
  search: trail.nodes.at(-1).search,
  state: trailState(trail),
});
test("unique trail truncates cycles, preserves directory and independent detail queries", () => {
  const a = startTagTrail(root, "lib", "a", "session");
  const edited = currentTrail(
    a,
    `${a.nodes[0].search}&relations=edit&r_page=3`,
  );
  const b = visitTag(edited, edited.nodes[0].search, "b");
  assert.deepEqual(
    b.nodes.map((n) => n.id),
    ["a", "b"],
  );
  assert.equal(
    new URLSearchParams(b.nodes[1].search).get("category"),
    "parody",
  );
  assert.equal(new URLSearchParams(b.nodes[1].search).get("q"), "stars");
  assert.equal(new URLSearchParams(b.nodes[1].search).has("relations"), false);
  assert.deepEqual(visitTag(b, b.nodes[1].search, "a"), edited);
  assert.deepEqual(backTag(b), edited);
  assert.equal(backTag(a), null);
  assert.equal(a.directory, root);
  assert.deepEqual(readTagTrail(trailState(b), "lib", "b"), b);
  assert.equal(readTagTrail(trailState(b), "other", "b"), null);
});
test("legacy links import one verified parent only; self, cycle, foreign and external parents are roots", () => {
  const search = `${root}&tag=b&parent_tag=a`;
  assert.deepEqual(
    resolveTagTrail(
      search,
      { tagReturn: `${root}&tag=a` },
      "lib",
      "k",
    ).nodes.map((n) => n.id),
    ["a", "b"],
  );
  for (const tagReturn of [
    `${root}&tag=a&parent_tag=b`,
    "?library_id=other&tag=a",
    "https://external.invalid/admin/tags?library_id=lib&tag=a",
  ])
    assert.deepEqual(
      resolveTagTrail(search, { tagReturn }, "lib", "k").nodes.map((n) => n.id),
      ["b"],
    );
  assert.equal(
    resolveTagTrail(
      `${root}&tag=a&parent_tag=a`,
      { tagReturn: `${root}&tag=a` },
      "lib",
      "k",
    ).nodes.length,
    1,
  );
  assert.equal(resolveTagTrail(search, null, "lib", "k").nodes.length, 1);
  const duplicate = startTagTrail(root, "lib", "a", "k");
  duplicate.nodes.push({ ...duplicate.nodes[0] });
  assert.equal(readTagTrail(trailState(duplicate), "lib", "a"), null);
});
test("draft preservation follows only retained nodes, so popped dirty descendants prompt", () => {
  const a = startTagTrail(root, "lib", "a", "k");
  const b = visitTag(a, a.nodes[0].search, "b");
  assert.deepEqual(preservedTagIDs(location(a), location(b)), ["a", "b"]);
  assert.equal(dropsTagSessions(location(a), location(b)), false);
  assert.deepEqual(preservedTagIDs(location(b), location(a)), ["a"]);
  assert.equal(dropsTagSessions(location(b), location(a)), true);
  assert.equal(preservedTagIDs(location(b), { search: root }), false);
  assert.equal(dropsTagSessions(location(b), { search: root }), true);
});
test("only default character display clears hidden filters, editor and parody keep theirs", () => {
  const old = `${root}&tag=a&d_q=hidden&d_page=3&d_sort=count&d_order=desc&display_snapshot=old&images=edit&parent_tag=b`;
  const character = new URLSearchParams(
    normalizeTagDetailSearch(old, "character"),
  );
  for (const key of [
    "d_q",
    "d_page",
    "d_sort",
    "d_order",
    "display_snapshot",
    "images",
    "parent_tag",
  ])
    assert.equal(character.has(key), false, key);
  for (const key of ["q", "page", "sort", "order"])
    assert.equal(character.get(key), new URLSearchParams(root).get(key));
  assert.equal(
    new URLSearchParams(normalizeTagDetailSearch(old, "parody")).get("d_q"),
    "hidden",
  );
  assert.equal(
    new URLSearchParams(
      normalizeTagDetailSearch(`${old}&relations=edit`, "character"),
    ).get("d_q"),
    "hidden",
  );
});
