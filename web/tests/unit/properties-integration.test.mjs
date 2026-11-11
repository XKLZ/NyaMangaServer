import assert from "node:assert/strict";
import test from "node:test";
import {
  displayedValue,
  editorReducer,
  isDirty,
  sameValue,
  savePayload,
  submitDraft,
  valueText,
} from "../../src/admin/metadataForm.ts";
import {
  propertiesFromURL,
  savedConditions,
  validateProperties,
  writeSearchParams,
} from "../../src/shared/propertyFilters.ts";
import {
  dictionaryLabel,
  mergeSuggestionPage,
  requestSuggestionPage,
  suggestionConditions,
  utf8QueryValid,
} from "../../src/admin/tagSuggestions.ts";
const property = (value, automatic = value, origin = "tags") => ({
  value,
  origin,
  state: value === null ? "unknown" : "normal",
  evidence: ["anonymous"],
  automatic: {
    value: automatic,
    origin: "tags",
    state: automatic === null ? "unknown" : "normal",
    evidence: ["anonymous"],
  },
});
const book = () => ({
  id: "anonymous",
  revision: 9,
  metadata: {
    fields: { title: "Anonymous", language: "legacy", tags: [] },
    overrides: {},
    properties: {
      rule_version: 1,
      languages: property(["english", "japanese"]),
      translated: property(null),
      color_mode: property("color"),
      censorship: property(null),
      extraneous_ads: property(true),
    },
    source: null,
  },
});
const initial = () => ({ base: book(), draft: {}, pending: {} });
const change = (state, key, value) =>
  editorReducer(state, { type: "change", key, value });

test("languages are authoritative ordered arrays, not legacy fields or TagName sets", () => {
  let state = initial();
  assert.deepEqual(displayedValue(state, "languages"), ["english", "japanese"]);
  state = change(state, "languages", ["japanese", "english"]);
  assert.equal(isDirty(state), true);
  assert.deepEqual(savePayload(state).fields.languages, {
    op: "set",
    value: ["japanese", "english"],
  });
  assert.equal("language" in savePayload(state).fields, false);
  assert.equal(
    sameValue("languages", ["english", "japanese"], ["japanese", "english"]),
    false,
  );
  state = change(state, "languages", ["english", "japanese"]);
  assert.equal(isDirty(state), false);
  assert.equal(valueText(["english", "japanese"]), "english、japanese");
});

test("false is a valid explicit value and unknown uses empty", () => {
  let state = change(initial(), "translated", false);
  state = change(state, "extraneous_ads", false);
  state = change(state, "languages", []);
  state = change(state, "color_mode", null);
  assert.deepEqual(savePayload(state).fields, {
    translated: { op: "set", value: false },
    extraneous_ads: { op: "set", value: false },
    languages: { op: "empty" },
    color_mode: { op: "empty" },
  });
  assert.equal(valueText(false), "否");
});

test("legacy manual and explicit unknown can restore backend automatic values", () => {
  let state = initial();
  state.base.metadata.properties.languages = property(
    ["legacy free text"],
    ["japanese", "english"],
    "legacy_manual",
  );
  state.base.metadata.properties.translated = property(null, true, "manual");
  state = editorReducer(state, { type: "restore", key: "languages" });
  state = editorReducer(state, { type: "restore", key: "translated" });
  assert.deepEqual(displayedValue(state, "languages"), ["japanese", "english"]);
  assert.equal(displayedValue(state, "translated"), true);
  assert.deepEqual(savePayload(state).fields.translated, { op: "inherit" });
  state = change(state, "tags", [{ namespace: "language", name: "chinese" }]);
  assert.deepEqual(
    displayedValue(state, "languages"),
    ["japanese", "english"],
    "tag draft never triggers client inference",
  );
});

test("all attributes, basic fields and tags use one save with the frozen revision", async () => {
  let state = change(initial(), "title", "edited");
  state = change(state, "translated", false);
  state = change(state, "tags", [{ namespace: "other", name: "custom" }]);
  state = change(state, "censorship", "mosaic");
  let calls = 0;
  await submitDraft(state, async (id, revision, fields) => {
    calls++;
    assert.equal(id, "anonymous");
    assert.equal(revision, 9);
    assert.deepEqual(Object.keys(fields), [
      "title",
      "translated",
      "tags",
      "censorship",
    ]);
    return state.base;
  });
  assert.equal(calls, 1);
  await assert.rejects(
    submitDraft(state, async () => {
      throw new Error("409");
    }),
    /409/,
  );
  assert.equal(isDirty(state), true);
});

test("missing properties cannot silently fall back or save new fields", () => {
  let state = initial();
  delete state.base.metadata.properties;
  assert.equal(displayedValue(state, "languages"), undefined);
  state = change(state, "translated", false);
  assert.throws(() => savePayload(state), /尚未就绪/);
});

test("language limits and property enum values are validated", () => {
  assert.throws(
    () => savePayload(change(initial(), "languages", ["x".repeat(65)])),
    /语言/,
  );
  assert.throws(
    () =>
      savePayload(
        change(
          initial(),
          "languages",
          Array.from({ length: 201 }, (_, i) => `lang${i}`),
        ),
      ),
    /语言/,
  );
  assert.throws(
    () => savePayload(change(initial(), "translated", "false")),
    /布尔/,
  );
  assert.throws(
    () => savePayload(change(initial(), "color_mode", "made up")),
    /颜色/,
  );
});

const filters = {
  languages: { any_of: ["chinese", "japanese"], include_unknown: true },
  translated: ["no", "unknown"],
  color_mode: ["color"],
  censorship: ["mosaic"],
  extraneous_ads: ["no"],
};
test("URL and match detail encode JSON properties without stringifying objects", () => {
  const params = writeSearchParams(new URLSearchParams(), {
    properties: filters,
    page: 2,
    snapshot_revision: "snapshot",
  });
  assert.deepEqual(
    propertiesFromURL(new URLSearchParams(params.toString())),
    filters,
  );
  assert.equal(params.get("properties").includes("[object Object]"), false);
  assert.equal(params.get("snapshot_revision"), "snapshot");
});

test("saved filters survive reopen, old searches work, reset removes previous filters", () => {
  const saved = savedConditions({
    properties: filters,
    query: "anonymous",
    page: 4,
    snapshot_revision: "stale",
  });
  assert.deepEqual(saved.properties, filters);
  assert.equal(saved.page, 1);
  assert.equal(saved.snapshot_revision, undefined);
  const params = writeSearchParams(new URLSearchParams(), saved);
  assert.deepEqual(propertiesFromURL(params), filters);
  writeSearchParams(params, { properties: undefined, query: "", page: 1 });
  assert.equal(params.has("properties"), false);
  assert.equal(propertiesFromURL(new URLSearchParams()), undefined);
  assert.equal(savedConditions({ page: 2 }).properties, undefined);
});

test("invalid JSON, unknown keys and invalid filter values fail closed", () => {
  for (const text of [
    "{",
    "null",
    "[]",
    '{"unknown":["yes"]}',
    '{"translated":["false"]}',
    '{"languages":{}}',
    '{"languages":{"include_unknown":"yes"}}',
    '{"languages":{"any_of":["en"],"wrong":true}}',
  ]) {
    assert.throws(() =>
      propertiesFromURL(new URLSearchParams({ properties: text })),
    );
  }
  assert.throws(() => validateProperties({ translated: ["yes", "yes"] }));
  assert.deepEqual(
    validateProperties({ languages: { include_unknown: true } }),
    { languages: { include_unknown: true } },
  );
  assert.equal(validateProperties({ translated: [] }), undefined);
});

const item = (name, extra = {}) => ({
  namespace: "female",
  name,
  translation: name,
  usage_count: 0,
  dictionary_match: true,
  tag_id: null,
  local_exists: false,
  ...extra,
});
const page = (page, items, snapshot = "v1") => ({
  page,
  items,
  total: 3,
  page_size: 50,
  snapshot_revision: snapshot,
  dictionary_state: "ready",
});
test("page append preserves server rank and nullable IDs, isolates new conditions", () => {
  let result = mergeSuggestionPage(
    null,
    "query1",
    page(1, [
      item("popular", { usage_count: 9, local_exists: true, tag_id: "id" }),
    ]),
  );
  result = mergeSuggestionPage(result, "query1", page(2, [item("unused")]));
  assert.deepEqual(
    result.result.items.map((t) => t.name),
    ["popular", "unused"],
  );
  assert.equal(result.result.items[1].tag_id, null);
  assert.throws(() =>
    mergeSuggestionPage(result, "query2", page(3, [item("old")])),
  );
  assert.throws(() =>
    mergeSuggestionPage(result, "query1", page(3, [item("old")], "v2")),
  );
  assert.deepEqual(
    mergeSuggestionPage(
      result,
      "query2",
      page(1, [item("new")]),
    ).result.items.map((t) => t.name),
    ["new"],
  );
});

test("snapshot expiry restarts at page one with original query/exclusion intact", async () => {
  const input = {
    ...suggestionConditions("female", "中文", [
      { namespace: "female", name: "selected" },
    ]),
    page: 2,
    snapshot_revision: "old",
  };
  const requests = [];
  const result = await requestSuggestionPage(
    input,
    new AbortController().signal,
    async (request) => {
      requests.push(request);
      if (requests.length === 1)
        throw { code: "tag_suggestions_snapshot_expired" };
      return page(1, [item("refreshed")], "new");
    },
  );
  assert.equal(result.page, 1);
  assert.equal(requests[1].page, 1);
  assert.equal(requests[1].snapshot_revision, undefined);
  assert.deepEqual(requests[1].exclude, input.exclude);
  assert.equal(requests[1].q, "中文");
});

test("cancelled page and excluded condition changes cannot publish stale results", async () => {
  const controller = new AbortController();
  let complete;
  const input = { ...suggestionConditions("female", "tag", []), page: 1 };
  const pending = requestSuggestionPage(
    input,
    controller.signal,
    () =>
      new Promise((resolve) => {
        complete = resolve;
      }),
  );
  controller.abort();
  complete(page(1, [item("stale")]));
  assert.equal(await pending, null);
  assert.notEqual(
    JSON.stringify(suggestionConditions("female", "tag", [])),
    JSON.stringify(
      suggestionConditions("female", "tag", [
        { namespace: "female", name: "added" },
      ]),
    ),
  );
  const selected = [
    { namespace: "female", name: "a" },
    { namespace: "female", name: "b" },
  ];
  assert.deepEqual(
    suggestionConditions("female", "", selected),
    suggestionConditions("female", "", [...selected].reverse()),
  );
});

test("dictionary membership is tri-state independent of translation and UTF8 query is bounded", () => {
  assert.equal(dictionaryLabel(true), "已收录");
  assert.equal(dictionaryLabel(false), "译库未收录");
  assert.equal(dictionaryLabel(null), "译库待核对");
  assert.equal(dictionaryLabel(undefined), "译库待核对");
  assert.equal(
    dictionaryLabel(item("untranslated", { translation: "" }).dictionary_match),
    "已收录",
  );
  assert.equal(utf8QueryValid("字".repeat(170)), true);
  assert.equal(utf8QueryValid("字".repeat(171)), false);
});

test("clearing an already unknown language does not create a false draft", () => {
  const state = initial();
  state.base.metadata.properties.languages = property(null);
  assert.equal(isDirty(change(state, "languages", [])), false);
});
