import test from "node:test";
import assert from "node:assert/strict";
import {
  emptyReviewSelection,
  reconcileReviewSelection,
  selectReviewItem,
  clearReviewSelection,
  titleOverlap,
} from "../../src/admin/imports/importReviewFlow.ts";
const item = (id, r = 1) => ({
  id,
  expected_revision: r,
  expected_edition_revision: 1,
  candidate_revision: 1,
  candidate_acceptable: true,
  status: "ready",
  decision: "",
});
test("default selection spans the whole batch; exclusions survive polling and new results", () => {
  const rows = Array.from({ length: 60 }, (_, i) => item(String(i)));
  let s = reconcileReviewSelection(emptyReviewSelection(), rows);
  assert.equal(Object.keys(s.selected).length, 60);
  s = selectReviewItem(s, rows[30], false);
  s = reconcileReviewSelection(s, [...rows, item("new")]);
  assert.equal(s.selected["30"], undefined);
  assert.equal(Object.keys(s.selected).length, 60);
  s = clearReviewSelection(s, rows);
  assert.equal(Object.keys(s.selected).length, 0);
  assert.equal(reconcileReviewSelection(s, rows).selected["0"], undefined);
});
test("candidate, member and edition changes cannot silently rebase selections", () => {
  for (const key of [
    "candidate_revision",
    "expected_revision",
    "expected_edition_revision",
  ]) {
    let s = reconcileReviewSelection(emptyReviewSelection(), [item("a")]);
    const changed = { ...item("a"), [key]: 2 };
    s = reconcileReviewSelection(s, [changed]);
    assert.equal(s.selected.a, undefined);
    assert.equal(s.stale.a, true);
    assert.equal(reconcileReviewSelection(s, [changed]).selected.a, undefined);
    s = selectReviewItem(s, changed, true);
    assert.equal(s.selected.a[key], 2);
  }
});
test("nonready or completed items are excluded; limits are explicit", () => {
  const rows = [
    { ...item("a"), candidate_acceptable: false, status: "fetch_error" },
    { ...item("b"), decision: "accept" },
  ];
  assert.equal(
    Object.keys(reconcileReviewSelection(emptyReviewSelection(), rows).selected)
      .length,
    0,
  );
  const many = Array.from({ length: 1001 }, (_, i) => item(String(i)));
  assert.equal(
    Object.keys(reconcileReviewSelection(emptyReviewSelection(), many).selected)
      .length,
    0,
  );
  let s = emptyReviewSelection();
  for (const row of many.slice(0, 1000)) s = selectReviewItem(s, row, true);
  assert.throws(() => selectReviewItem(s, many[1000], true), /1000/);
});
test("title overlap normalizes NFKC and case, retains original text and ignores extension/punctuation", () => {
  const values = ["[Ｃｉｒｃｌｅ] 星の旅行.CBZ", "circle 星の旅行", "星の旅行"];
  const p = titleOverlap(values);
  for (let i = 0; i < values.length; i++)
    assert.equal(p[i].map((v) => v.text).join(""), values[i]);
  assert.equal(
    p[0]
      .filter((v) => v.matched)
      .map((v) => v.text)
      .join(""),
    "Ｃｉｒｃｌｅ星の旅行",
  );
  assert.ok(p[0].some((v) => !v.matched && v.text.includes(".CBZ")));
  assert.ok(titleOverlap(["---.cbz", "…", ""])[0].every((v) => !v.matched));
});

test("older selection reads cannot replace or invalidate a newer explicit selection", () => {
  const current = {
    ...item("a", 3),
    candidate_revision: 3,
    expected_edition_revision: 3,
  };
  let state = selectReviewItem(emptyReviewSelection(), current, true);
  state = reconcileReviewSelection(state, [item("a", 2)]);
  assert.equal(state.selected.a.expected_revision, 3);
  assert.equal(state.stale.a, undefined);
});

test("best comparison chooses filename coverage, ties prefer original, missing and empty remain safe", async () => {
  const { reviewTitle } =
    await import("../../src/admin/imports/importReviewFlow.ts");
  assert.equal(
    reviewTitle("[Ｃｉｒｃｌｅ] 星の旅行.cbz", "circle 星の旅行", "星の旅行")
      .original,
    false,
  );
  assert.equal(
    reviewTitle("星の旅行.cbz", "romanization", "星の旅行").original,
    true,
  );
  assert.equal(reviewTitle("FILE.cbz", "file", "ＦＩＬＥ").original, true);
  assert.equal(reviewTitle("unknown.cbz", "abcd", "日本語").original, true);
  assert.equal(reviewTitle("file.cbz", "file", "").original, false);
  assert.equal(reviewTitle("file.cbz", "", "").value, "");
  const result = reviewTitle(
    "alpha.cbz",
    "unrelated duplicate",
    "alpha duplicate",
  );
  assert.equal(result.parts.length, 2);
  assert.equal(
    result.parts[1]
      .filter((p) => p.matched)
      .map((p) => p.text)
      .join(""),
    "alpha",
  );
  assert.equal(result.parts[0].map((p) => p.text).join(""), "alpha.cbz");
});

test("only a completed explicit review advances, retaining latest batch revision", async () => {
  const { finishMetadataReview } =
    await import("../../src/admin/imports/importReviewFlow.ts");
  const calls = [];
  const advance = async (b) => calls.push(b.revision);
  for (const b of [
    { stage: "metadata", state: "review", pending: 1 },
    { stage: "metadata", state: "processing", pending: 0 },
    { stage: "versions", state: "review", pending: 0 },
  ])
    assert.equal(await finishMetadataReview(async () => b, advance), false);
  assert.deepEqual(calls, []);
  const b = { stage: "metadata", state: "review", pending: 0, revision: 9 };
  assert.equal(await finishMetadataReview(async () => b, advance), true);
  assert.deepEqual(calls, [9]);
  await assert.rejects(
    () =>
      finishMetadataReview(
        async () => b,
        async () => {
          throw Error("conflict");
        },
      ),
    /conflict/,
  );
  assert.equal(await finishMetadataReview(async () => b, advance), true);
});
