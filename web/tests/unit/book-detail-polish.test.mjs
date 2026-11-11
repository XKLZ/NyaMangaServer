import assert from "node:assert/strict";
import test from "node:test";
import {
  preferredBookTitle,
  cleanBookTitle,
  copyBookTitle,
} from "../../src/admin/books/bookTitle.ts";
import { selectedDictionaryLabel } from "../../src/admin/tagSuggestions.ts";
import { coverPageReady } from "../../src/admin/coverFlow.ts";
import {
  currentMembership,
  memberPage,
  frozenRevisions,
  seriesDestination,
} from "../../src/admin/books/bookOrganizationFlow.ts";

test("effective original title is displayed and copied; empty original falls back", async () => {
  const book = {
    title: "English title",
    metadata: { fields: { title_original: "[注釈] 日本語タイトル" } },
  };
  assert.equal(cleanBookTitle(preferredBookTitle(book)), "日本語タイトル");
  let copied;
  await copyBookTitle(preferredBookTitle(book), {
    writeText: async (value) => {
      copied = value;
    },
  });
  assert.equal(copied, "日本語タイトル");
  for (const original of [null, undefined, "", "  "])
    assert.equal(
      preferredBookTitle({
        ...book,
        metadata: { fields: { title_original: original } },
      }),
      book.title,
    );
  assert.equal(preferredBookTitle({ title: "Fallback" }), "Fallback");
  assert.equal(book.metadata.fields.title_original, "[注釈] 日本語タイトル");
});
test("selected chips mark only confirmed dictionary misses", () => {
  assert.equal(selectedDictionaryLabel(false), "未收录");
  for (const value of [true, null, undefined])
    assert.equal(selectedDictionaryLabel(value), "");
});
test("load control stays for missing, interrupted or failed images; completed page hides it", () => {
  const items = [{ page_index: 1 }, { page_index: 2 }];
  assert.equal(coverPageReady(undefined, {}, 1), false);
  assert.equal(coverPageReady(items, { "1:1": { url: "one" } }, 1), false);
  assert.equal(
    coverPageReady(
      items,
      { "1:1": { url: "one" }, "1:2": { error: "failed" } },
      1,
    ),
    false,
  );
  const images = { "1:1": { url: "one" }, "1:2": { url: "retried" } };
  assert.equal(coverPageReady(items, images, 1), true);
  assert.equal(coverPageReady(items, images, 2), false);
});
test("organization locates the current book anchor, rather than a related organization", () => {
  const context = {
    editions: [{ id: "book", work_id: "work" }],
    works: [{ id: "other" }, { id: "work", series_id: "s", position: 17 }],
    series: [{ id: "wrong" }, { id: "s" }],
  };
  const m = currentMembership(context, "book");
  assert.equal(m.work.id, "work");
  assert.equal(m.series.id, "s");
  assert.equal(memberPage(m.work.position), 3);
  assert.equal(memberPage(1), 1);
  assert.equal(memberPage(8), 1);
  assert.equal(memberPage(9), 2);
});
test("new context does not silently rebase aggregate revisions or mutate the draft", () => {
  const base = {
    expected_revisions: { "work:i": 1, "series:old": 2, "edition:e": 3 },
  };
  const extra = {
    expected_revisions: {
      "work:i": 9,
      "series:old": 8,
      "edition:e": 7,
      "series:target": 4,
    },
  };
  const expected = frozenRevisions(base, [extra]);
  assert.deepEqual(expected, {
    "work:i": 1,
    "series:old": 2,
    "edition:e": 3,
    "series:target": 4,
  });
  assert.equal(base.expected_revisions["work:i"], 1);
  assert.equal(frozenRevisions(extra, [])["work:i"], 9); // explicit recheck supplies a new base
});
test("current membership determines atomic transfer versus initial join", () => {
  assert.deepEqual(seriesDestination("old", "new", "book"), {
    action: "series_transfer",
    id: "old",
    target_series_id: "new",
    work_ids: ["book"],
  });
  assert.deepEqual(seriesDestination(undefined, "new", "book"), {
    action: "series_add",
    id: "new",
    work_ids: ["book"],
  });
  assert.throws(() => seriesDestination("same", "same", "book"));
});
