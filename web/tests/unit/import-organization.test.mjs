import test from "node:test";
import assert from "node:assert/strict";
import {
  nextSuggestionIndex,
  shouldCompleteReview,
  workReviewDirty,
  workReviewDraft,
} from "../../src/admin/imports/importOrganizationFlow.ts";
test("review advances at the vacated position, including last/empty pages", () => {
  assert.equal(nextSuggestionIndex(25, 4), 4);
  assert.equal(nextSuggestionIndex(1, 24), 0);
  assert.equal(nextSuggestionIndex(0, 0), 0);
});
test("only explicit settled suggestion review can advance", () => {
  const ready = {
    can_complete: true,
    revalidating: false,
    active_tasks: false,
    pending_proposals: 0,
  };
  assert.equal(shouldCompleteReview(false, ready, false), false);
  assert.equal(shouldCompleteReview(true, ready, true), false);
  assert.equal(shouldCompleteReview(true, ready, false), true);
  for (const changed of [
    { can_complete: false },
    { revalidating: true },
    { active_tasks: true },
    { pending_proposals: 1 },
    { recommendation_skipped: true },
    { failure: { job_id: "anonymous", stage: "versions" } },
  ])
    assert.equal(
      shouldCompleteReview(true, { ...ready, ...changed }, false),
      false,
    );
});
test("member drafts retain their original snapshot and become clean when restored", () => {
  const p = { id: "anonymous", revision: 1, new_edition_ids: ["a", "b"] };
  const d = workReviewDraft(p);
  d.selected.pop();
  assert.equal(workReviewDirty(d), true);
  assert.deepEqual(p.new_edition_ids, ["a", "b"]);
  d.selected.push("b");
  assert.equal(workReviewDirty(d), false);
  assert.equal(d.baseline.revision, 1);
});
