import test from "node:test";
import assert from "node:assert/strict";
import { sourceTitleQuery } from "../../src/admin/books/bookTitle.ts";
import {
  moreSearchPage,
  searchRunning,
  searchError,
  sourceChoiceStale,
  sourceResultTags,
  sourceChannel,
} from "../../src/admin/imports/importSourceFlow.ts";
test("legacy browsing state migrates without retaining a selected candidate", () => {
  const old = {
    id: "anonymous",
    page: 2,
    listScroll: 345,
    choice: { result: { id: "must-not-select" } },
    detailScroll: 92,
  };
  const migrated = sourceChannel(old);
  assert.deepEqual(migrated, {
    id: "anonymous",
    page: 2,
    listScroll: 345,
    expanded: {},
  });
  assert.equal(old.choice.result.id, "must-not-select");
  const attempt = {
    result: { id: "retained-after-conflict" },
    requiresReview: true,
  };
  assert.deepEqual(
    sourceChannel({ ...migrated, attempt, expanded: { a: true } }).attempt,
    attempt,
  );
  assert.deepEqual(sourceChannel({ page: 99, listScroll: -1 }), {
    id: "",
    page: 20,
    listScroll: 0,
    expanded: {},
  });
});
test("title query is bounded display cleaning, preserves filenames and unmatched marks", () => {
  const original = "(Event) [Circle] 星の旅行 (特別版).CBZ";
  assert.equal(sourceTitleQuery(original), "星の旅行");
  assert.ok(original.endsWith(".CBZ"));
  assert.equal(
    sourceTitleQuery("星の旅行 [unfinished.cbz"),
    "星の旅行 [unfinished",
  );
  assert.equal([...sourceTitleQuery("漫".repeat(300) + ".zip")].length, 256);
});
test("more reads the incomplete current page, then the next, never past twenty", () => {
  assert.equal(moreSearchPage(1, 2), 1);
  assert.equal(moreSearchPage(1, 10), 2);
  assert.equal(moreSearchPage(20, 10), 20);
});
test("active reads and scoped snapshots cannot silently advance a chosen revision", () => {
  const s = { id: "anonymous", revision: 3, state: "running" };
  assert.equal(searchRunning(s), true);
  assert.equal(searchRunning({ ...s, state: "paused" }), false);
  const c = { search: s, result: { revision: 2 } };
  assert.equal(sourceChoiceStale(c, s, 2), false);
  assert.equal(sourceChoiceStale(c, { ...s, id: "other" }, 2), true);
  assert.equal(sourceChoiceStale(c, { ...s, revision: 4 }, 2), true);
  assert.equal(sourceChoiceStale(c, s, 3), true);
  assert.equal(c.search.revision, 3);
});
test("zero results are not parser, authentication, limit or network failures", () => {
  assert.match(searchError("invalid_response"), /解析/);
  assert.match(searchError("authentication_required"), /认证/);
  assert.match(searchError("search_limit_reached"), /上限/);
  assert.match(searchError("network_error"), /原候选仍保留/);
});

test("empty nullable provider tags remain renderable and translated tags win", () => {
  assert.deepEqual(
    sourceResultTags({ tags: null, fields: { tags: null } }),
    [],
  );
  const tags = [
    { namespace: "artist", name: "anonymous", translation: "匿名作者" },
  ];
  assert.deepEqual(sourceResultTags({ tags, fields: { tags: [] } }), tags);
  assert.equal(
    sourceResultTags({
      tags: [],
      fields: { tags: [{ namespace: "other", name: "custom" }] },
    })[0].translation,
    "",
  );
});
