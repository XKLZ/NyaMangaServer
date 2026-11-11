import test from "node:test";
import assert from "node:assert/strict";
import {
  feedbackDraft,
  feedbackDirty,
  feedbackExportPages,
} from "../../src/admin/versions/recommendationFlow.ts";
test("feedback is frozen until explicitly reloaded and draft differences remain visible", () => {
  const record = { revision: 1, feedback: null };
  const draft = feedbackDraft(record);
  assert.equal(feedbackDirty(record, draft), false);
  const edited = { ...draft, relation: "same_work", note: "anonymous note" };
  assert.equal(feedbackDirty(record, edited), true);
  const incoming = {
    revision: 2,
    feedback: {
      relation: "unrelated",
      note: "other reviewer",
      labels: { assessment: "incorrect" },
    },
  };
  assert.equal(edited.note, "anonymous note");
  assert.equal(record.revision, 1);
  assert.deepEqual(feedbackDraft(incoming), {
    relation: "unrelated",
    note: "other reviewer",
    assessment: "incorrect",
  });
});
test("export pagination counts the entire SQL task summary and misses independently of displayed ten rows", () => {
  assert.equal(feedbackExportPages(undefined), 1);
  const summary = {
    layers: [
      {
        same_work: 20,
        same_series: 5,
        unrelated: 7,
        uncertain: 3,
        unreviewed: 65,
      },
      {
        same_work: 1,
        same_series: 0,
        unrelated: 0,
        uncertain: 0,
        unreviewed: 0,
      },
    ],
    recorded_misses: 0,
  };
  assert.equal(feedbackExportPages(summary), 3);
  assert.equal(feedbackExportPages({ ...summary, recorded_misses: 151 }), 4);
});
