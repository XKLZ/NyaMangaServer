import assert from "node:assert/strict";
import test from "node:test";
import {
  displayedValue,
  editorReducer,
  isDirty,
  savePayload,
  submitDraft,
  versionStatus,
} from "../../src/admin/metadataForm.ts";
import {
  availableTags,
  canonicalTag,
  loadSuggestions,
  mergeTags,
  namespaces,
  parseTags,
  tagKeyAction,
} from "../../src/admin/tagInput.ts";
const fields = () => ({
  title: "匿名标题",
  title_original: "Original",
  category: "Doujinshi",
  rating: 4,
  published_at: "2026-10-15T00:00:00Z",
  description: null,
  language: "chinese",
  tags: [{ namespace: "female", name: "schoolgirl uniform" }],
});
const book = (revision = 3) => ({
  id: "anonymous",
  revision,
  metadata: {
    fields: fields(),
    overrides: {},
    tags: [],
    source: { fields: { ...fields(), title: "来源标题" } },
  },
});
const state = () => ({ base: book(), draft: {}, pending: {} });
const change = (s, key, value) =>
  editorReducer(s, { type: "change", key, value });

test("save stays disabled until a real change, and reverting removes changes", () => {
  let s = state();
  assert.equal(isDirty(s), false);
  s = change(s, "title", "新标题");
  assert.equal(isDirty(s), true);
  s = change(s, "title", "匿名标题");
  assert.equal(isDirty(s), false);
  s = change(s, "description", "");
  assert.equal(isDirty(s), false);
  s = change(s, "tags", [...s.base.metadata.fields.tags]);
  assert.equal(isDirty(s), false);
});

test("one atomic payload contains only changed fields and handles empty values", () => {
  let s = change(state(), "title", "新标题");
  s = change(s, "rating", null);
  s = change(s, "tags", []);
  assert.deepEqual(savePayload(s), {
    expected_revision: 3,
    fields: {
      title: { op: "set", value: "新标题" },
      rating: { op: "empty" },
      tags: { op: "empty" },
    },
  });
  s = change(s, "published_at", "");
  assert.deepEqual(savePayload(s).fields.published_at, { op: "empty" });
});

test("restore source previews value and changes provenance even when value matches", () => {
  let s = state();
  s.base.metadata.overrides.title = { op: "set", value: "匿名标题" };
  s = editorReducer(s, { type: "restore", key: "title" });
  assert.equal(displayedValue(s, "title"), "来源标题");
  assert.deepEqual(savePayload(s).fields.title, { op: "inherit" });
  s = editorReducer(s, { type: "undo", key: "title" });
  assert.equal(isDirty(s), false);
  s.base.metadata.source = null;
  s = editorReducer(s, { type: "restore", key: "title" });
  assert.equal(displayedValue(s, "title"), null);
});

test("pending input protects drafts and blocks saving; tag undo clears pending input", () => {
  let s = editorReducer(state(), {
    type: "pending",
    namespace: "female",
    value: "未添加",
  });
  assert.equal(isDirty(s), true);
  assert.throws(() => savePayload(s), /先添加或清除/);
  const received = editorReducer(s, { type: "receive", book: book(10) });
  assert.equal(received.base.revision, 3);
  s = editorReducer(s, { type: "undo", key: "tags" });
  assert.equal(isDirty(s), false);
});

test("refresh does not overwrite draft or revision; rebase is explicit", () => {
  let s = change(state(), "title", "草稿");
  assert.equal(editorReducer(s, { type: "receive", book: book(10) }), s);
  s = editorReducer(s, { type: "rebase", book: book(10) });
  assert.equal(savePayload(s).expected_revision, 10);
  assert.equal(displayedValue(s, "title"), "草稿");
  assert.equal(
    editorReducer(state(), { type: "receive", book: book(10) }).base.revision,
    10,
  );
});

test("success clears drafts only after response; failures and conflict preserve all input", async () => {
  const s = change(change(state(), "title", "草稿"), "description", "说明");
  let complete;
  const response = new Promise((resolve) => {
    complete = resolve;
  });
  let published = false;
  const pending = submitDraft(s, async (id, revision, draft) => {
    assert.equal(id, "anonymous");
    assert.equal(revision, 3);
    assert.equal(Object.keys(draft).length, 2);
    return response;
  }).then((updated) => {
    published = true;
    return editorReducer(s, { type: "saved", book: updated });
  });
  await Promise.resolve();
  assert.equal(published, false);
  assert.equal(isDirty(s), true);
  complete(book(4));
  assert.equal(isDirty(await pending), false);
  for (const status of [0, 409, 500]) {
    await assert.rejects(
      submitDraft(s, async () => {
        throw Object.assign(new Error("failed"), { status });
      }),
      /failed/,
    );
    assert.equal(s.base.revision, 3);
    assert.equal(s.draft.title.value, "草稿");
    assert.equal(s.draft.description.value, "说明");
  }
});

test("field validation matches backend text, rating and time limits", () => {
  assert.throws(
    () => savePayload(change(state(), "title", "字".repeat(513))),
    /最多 512/,
  );
  assert.throws(() => savePayload(change(state(), "rating", 6)), /评分/);
  assert.throws(
    () => savePayload(change(state(), "published_at", "invalid")),
    /时间/,
  );
});

test("batch paste preserves spaces, respects quotes and routes namespaces", () => {
  assert.deepEqual(
    parseTags(
      "female:bondage female:schoolgirl uniform\nparody:love live;artist:someone",
      "other",
    ),
    [
      { namespace: "female", name: "bondage" },
      { namespace: "female", name: "schoolgirl uniform" },
      { namespace: "parody", name: "love live" },
      { namespace: "artist", name: "someone" },
    ],
  );
  assert.deepEqual(
    parseTags('female:"name, with comma" "other:another; name"', "other"),
    [
      { namespace: "female", name: "name, with comma" },
      { namespace: "other", name: "another; name" },
    ],
  );
  assert.deepEqual(
    parseTags('"female:name, with comma"; other:"another; name"', "other"),
    [
      { namespace: "female", name: "name, with comma" },
      { namespace: "other", name: "another; name" },
    ],
  );
  assert.deepEqual(parseTags(" Schoolgirl   Uniform ", "female"), [
    { namespace: "female", name: "schoolgirl uniform" },
  ]);
});

test("invalid paste is rejected as a batch, duplicates and limits are handled", () => {
  const existing = [{ namespace: "female", name: "bondage" }];
  assert.throws(
    () => parseTags("female:valid; bad-type:oops", "other"),
    /类型/,
  );
  assert.throws(() => parseTags('female:"unclosed', "other"), /引号/);
  assert.throws(() => parseTags(`female:${"x".repeat(129)}`, "other"), /128/);
  assert.deepEqual(
    mergeTags(existing, parseTags("female:Bondage; female:bondage", "other")),
    existing,
  );
  assert.throws(
    () =>
      mergeTags(
        [],
        Array.from({ length: 201 }, (_, i) => ({
          namespace: "other",
          name: `tag${i}`,
        })),
      ),
    /200/,
  );
  assert.equal(existing.length, 1);
});

test("candidate order and Chinese matches are preserved and selected items excluded", () => {
  const items = [
    { namespace: "female", name: "bondage", translation: "束缚" },
    {
      namespace: "female",
      name: "schoolgirl uniform",
      translation: "学生制服",
    },
  ];
  assert.deepEqual(
    availableTags(items, [{ namespace: "female", name: "Bondage" }]),
    [items[1]],
  );
  assert.equal(namespaces.find(([ns]) => ns === "parody")[1], "原作");
  assert.deepEqual(
    canonicalTag({ namespace: " FEMALE ", name: " Schoolgirl   Uniform " }),
    { namespace: "female", name: "schoolgirl uniform" },
  );
});

test("cancelled transports cannot publish stale suggestions", async () => {
  let resolve;
  const signal = new AbortController();
  const promise = loadSuggestions(
    () =>
      new Promise((r) => {
        resolve = r;
      }),
    signal.signal,
  );
  signal.abort();
  resolve({ items: [{ name: "stale" }] });
  assert.equal(await promise, null);
  assert.deepEqual(
    await loadSuggestions(
      async () => ({ items: [] }),
      new AbortController().signal,
    ),
    { items: [] },
  );
});

test("keyboard navigation, add, remove, Escape and composition are distinct", () => {
  assert.equal(tagKeyAction("ArrowDown", false, "", true, -1, 3).index, 0);
  assert.equal(tagKeyAction("ArrowUp", false, "", true, 0, 3).index, 2);
  assert.equal(tagKeyAction("Enter", false, "", true, 1, 3).action, "select");
  assert.equal(
    tagKeyAction("Enter", false, "custom tag", true, -1, 0).action,
    "create",
  );
  assert.equal(
    tagKeyAction("Backspace", false, "", true, -1, 0).action,
    "remove",
  );
  assert.equal(
    tagKeyAction("Backspace", false, "x", true, -1, 0).action,
    "none",
  );
  assert.equal(tagKeyAction("Escape", false, "", true, 1, 3).action, "close");
  for (const key of ["Enter", "Backspace", "ArrowDown"])
    assert.equal(tagKeyAction(key, true, "", true, 1, 3).action, "none");
});

test("version status uses the current member; no relationship adds no badge", () => {
  const b = book();
  assert.deepEqual(versionStatus(b), []);
  b.work_id = "work";
  b.organization = {
    works: [{ id: "work", preferred_edition_id: b.id }],
    editions: [{ id: b.id, work_id: "work", version_labels: ["初版"] }],
    series: [{ id: "series", name: "匿名系列" }],
  };
  assert.deepEqual(versionStatus(b), [
    { id: "work", status: "首选版本", label: "初版" },
  ]);
  b.organization.works[0].preferred_edition_id = "other";
  assert.equal(versionStatus(b)[0].status, "其他版本");
});
